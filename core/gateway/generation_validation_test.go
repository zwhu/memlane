package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type controllableProbeEngine struct {
	*fakeEngine
	probeFn func(ctx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error)
}

func (e *controllableProbeEngine) ProbeModel(ctx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
	if e.probeFn != nil {
		return e.probeFn(ctx, req)
	}
	return e.fakeEngine.ProbeModel(ctx, req)
}

func pollValidationUntilTerminal(t *testing.T, s *Server, op string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := manage(t, s, "GET", "/v1/generation/validations/"+op, "", http.StatusOK)
		body := decodeResponse[map[string]any](t, res)
		status, _ := body["status"].(string)
		if status != "running" && status != "queued" {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("validation %s did not reach terminal state in time", op)
	return nil
}

func TestAsyncValidationDoesNotHoldGenerationMuOrWriteZombieSQLiteRows(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(ctx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return engine.ProbeResult{
				Status:         "passed",
				PlainPassed:    true,
				SchemaPassed:   true,
				QualityPassed:  true,
				StructuredMode: "json_schema",
			}, nil
		case <-ctx.Done():
			return engine.ProbeResult{}, ctx.Err()
		}
	}

	s, store, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
	})

	manage(t, s, "PUT", "/api/generation/profiles/p1",
		`{"mode":"provider","provider":"openai","model":"gpt-slow","endpoint":"https://provider.invalid/v1","api_key":"secret-key-p1"}`,
		http.StatusOK)

	// POST /v1/generation/validations returns HTTP 202 immediately without blocking on ProbeModel.
	accepted := manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-slow-1"}`, http.StatusAccepted)
	acceptedBody := decodeResponse[map[string]any](t, accepted)
	if acceptedBody["status"] != "running" || acceptedBody["operation_id"] != "op-slow-1" {
		t.Fatalf("unexpected 202 response: %+v", acceptedBody)
	}
	if startedAt, _ := acceptedBody["started_at"].(float64); startedAt <= 0 {
		t.Fatalf("missing started_at in 202 response: %+v", acceptedBody)
	}
	resultMap, _ := acceptedBody["result"].(map[string]any)
	if resultMap["operation_id"] != "op-slow-1" || resultMap["status"] != "running" {
		t.Fatalf("unexpected 202 result wrapper: %+v", acceptedBody)
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("background ProbeModel did not start")
	}

	// 1. Zero queued/running rows written to SQLite (no zombie rows on crash).
	if n := countRows(t, store, "generation_validations"); n != 0 {
		t.Fatalf("generation_validations rows while running = %d, want 0", n)
	}

	// 2. GET /v1/generation/validations/{op} hits in-memory active state cleanly.
	running := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/validations/op-slow-1", "", http.StatusOK))
	if running["status"] != "running" || running["phase"] != "probe" {
		t.Fatalf("unexpected active validation view: %+v", running)
	}

	// 3. generationMu is NOT held during ProbeModel: concurrent writes and MCP TryLock succeed immediately!
	if !s.generationMu.TryLock() {
		t.Fatal("generationMu was held during slow ProbeModel (blocks MCP fast path)")
	}
	s.generationMu.Unlock()

	manage(t, s, "PUT", "/api/generation/profiles/p2",
		`{"mode":"provider","provider":"ollama","model":"qwen3","endpoint":"http://127.0.0.1:11434"}`,
		http.StatusOK)

	// 4. Conflict guards: same operation_id -> 409 operation_id_conflict; same profile -> 409 validation_in_progress.
	requireCode(t, manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-slow-1"}`, http.StatusConflict), "operation_id_conflict")
	requireCode(t, manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-slow-dup-profile"}`, http.StatusConflict), "validation_in_progress")

	// 5. Different profile CAN validate concurrently without blocking.
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p2","revision":1},"operation_id":"op-other-profile"}`, http.StatusAccepted)

	// Release probes and observe clean terminal transition with zero 404 window.
	close(release)
	final := pollValidationUntilTerminal(t, s, "op-slow-1")
	if final["status"] != "passed" || final["plain_passed"] != true || final["schema_passed"] != true || final["quality_passed"] != true {
		t.Fatalf("unexpected terminal validation: %+v", final)
	}
	final2 := pollValidationUntilTerminal(t, s, "op-other-profile")
	if final2["status"] != "passed" {
		t.Fatalf("unexpected second profile terminal validation: %+v", final2)
	}

	// Reusing completed operation_id still returns 409 operation_id_conflict.
	requireCode(t, manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-slow-1"}`, http.StatusConflict), "operation_id_conflict")
}

func TestSyncValidateReleasesGenerationMuDuringProbe(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(ctx context.Context, _ engine.ProbeRequest) (engine.ProbeResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return engine.ProbeResult{Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true, StructuredMode: "json_schema"}, nil
		case <-ctx.Done():
			return engine.ProbeResult{}, ctx.Err()
		}
	}

	s, _, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
	})
	manage(t, s, "PUT", "/api/generation/profiles/p-sync",
		`{"mode":"provider","provider":"ollama","model":"qwen3","endpoint":"http://127.0.0.1:11434"}`, http.StatusOK)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- manage(t, s, "POST", "/api/generation/validate", `{"profile_id":"p-sync","operation_id":"op-sync-1"}`, http.StatusOK)
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("sync ProbeModel did not start")
	}
	if !s.generationMu.TryLock() {
		t.Fatal("POST /api/generation/validate held generationMu across ProbeModel")
	}
	s.generationMu.Unlock()

	close(release)
	select {
	case w := <-done:
		body := decodeResponse[map[string]any](t, w)
		if body["status"] != "passed" || body["operation_id"] != "op-sync-1" {
			t.Fatalf("unexpected sync validate response: %+v", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sync validate did not finish")
	}
}

func TestDeleteCancelsSlowValidationWaitsForExitAndPreservesExistingPassed(t *testing.T) {
	ctx := context.Background()
	var probeExited atomic.Bool
	var mode atomic.Value
	mode.Store("pass")
	started := make(chan struct{}, 4)

	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(pCtx context.Context, _ engine.ProbeRequest) (engine.ProbeResult, error) {
		if mode.Load().(string) == "pass" {
			return engine.ProbeResult{
				Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true, StructuredMode: "json_schema",
			}, nil
		}
		probeExited.Store(false)
		started <- struct{}{}
		<-pCtx.Done()
		// Simulate stopSubprocessGroup cleanup latency before returning.
		time.Sleep(60 * time.Millisecond)
		probeExited.Store(true)
		return engine.ProbeResult{}, pCtx.Err()
	}

	s, store, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
	})

	saved := decodeResponse[generationView](t, manage(t, s, "PUT", "/api/generation/profiles/p1",
		`{"mode":"provider","provider":"openai","model":"gpt-test","endpoint":"https://provider.invalid/v1","api_key":"secret-cancel-key"}`,
		http.StatusOK))
	saved2 := decodeResponse[generationView](t, manage(t, s, "PUT", "/api/generation/profiles/p2",
		`{"mode":"provider","provider":"ollama","model":"qwen-unval","endpoint":"http://127.0.0.1:11434"}`,
		http.StatusOK))

	// 1. Validate p1 first -> passed (registered_revision = 1).
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-p1-pass"}`, http.StatusAccepted)
	if got := pollValidationUntilTerminal(t, s, "op-p1-pass"); got["status"] != "passed" {
		t.Fatalf("initial pass failed: %+v", got)
	}
	valid, err := store.IsGenerationDigestValidated(ctx, saved.Digest)
	must(t, err)
	if !valid {
		t.Fatal("p1 should be validated")
	}

	// 2. Start slow re-validation on p1, advance generation_state.revision to make expected_revision stale,
	//    then DELETE with stale expected_revision -> must wait for probeExited, confirm local_process_exited=true,
	//    persist validation_interrupted with registered_revision=0, and NOT revoke existing passed!
	mode.Store("slow")
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p1","revision":1},"operation_id":"op-p1-cancel-1"}`, http.StatusAccepted)
	<-started

	// Advance state.revision while validation is in flight.
	must(t, store.SetActiveGeneration(ctx, saved.ProfileID, saved.Revision))

	delRes := decodeResponse[struct {
		Result struct {
			OperationID        string `json:"operation_id"`
			LocalProcessExited bool   `json:"local_process_exited"`
		} `json:"result"`
	}](t, manage(t, s, "DELETE", "/v1/generation/validations/op-p1-cancel-1",
		`{"expected_revision":0,"operation_id":"cancel-req-1"}`, http.StatusOK))
	if !probeExited.Load() {
		t.Fatal("DELETE returned before ProbeModel finished cleanup")
	}
	if delRes.Result.OperationID != "op-p1-cancel-1" || !delRes.Result.LocalProcessExited {
		t.Fatalf("unexpected DELETE receipt: %+v", delRes)
	}

	// Immediately query GET -> zero 404 gap, persisted terminal validation_interrupted at rev=0.
	gotCancel := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/validations/op-p1-cancel-1", "", http.StatusOK))
	if gotCancel["status"] != "failed" || gotCancel["error_code"] != "validation_interrupted" || gotCancel["registered_revision"] != float64(0) {
		t.Fatalf("unexpected cancelled revalidation receipt: %+v", gotCancel)
	}

	// Existing passed validation on p1 MUST remain intact (and activation still permitted).
	valid, err = store.IsGenerationDigestValidated(ctx, saved.Digest)
	must(t, err)
	if !valid {
		t.Fatal("cancelled revalidation revoked existing passed validation")
	}
	manage(t, s, "POST", "/api/generation/activate", `{"profile_id":"p1","revision":1}`, http.StatusOK)

	// Idempotent DELETE on already-terminal op returns 200 local_process_exited=true.
	idem := decodeResponse[struct {
		Result struct {
			OperationID        string `json:"operation_id"`
			LocalProcessExited bool   `json:"local_process_exited"`
		} `json:"result"`
	}](t, manage(t, s, "DELETE", "/v1/generation/validations/op-p1-cancel-1", "", http.StatusOK))
	if idem.Result.OperationID != "op-p1-cancel-1" || !idem.Result.LocalProcessExited {
		t.Fatalf("unexpected idempotent DELETE receipt: %+v", idem)
	}

	// DELETE on unknown op returns 404.
	requireCode(t, manage(t, s, "DELETE", "/v1/generation/validations/nonexistent-op", "", http.StatusNotFound), "generation_validation_not_found")

	// 3. Unvalidated p2: two consecutive cancellations get rev=1 and rev=2 (no 0==0 tie).
	for i, op := range []string{"op-p2-cancel-1", "op-p2-cancel-2"} {
		manage(t, s, "POST", "/v1/generation/validations",
			fmt.Sprintf(`{"profile":{"id":"p2","revision":1},"operation_id":%q}`, op), http.StatusAccepted)
		<-started
		manage(t, s, "DELETE", "/v1/generation/validations/"+op, "", http.StatusOK)
		rec, err := store.GetGenerationValidation(ctx, op)
		must(t, err)
		if rec.RegisteredRevision != i+1 || rec.ErrorCode != "validation_interrupted" {
			t.Fatalf("%s = %+v, want rev=%d validation_interrupted", op, rec, i+1)
		}
	}
	valid2, err := store.IsGenerationDigestValidated(ctx, saved2.Digest)
	must(t, err)
	if valid2 {
		t.Fatal("unvalidated p2 became validated after cancellations")
	}
}

func TestDeleteReapsEphemeralAgentBridgeBeforeReturning(t *testing.T) {
	endpointCh := make(chan string, 1)
	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(ctx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
		endpointCh <- req.Environment["LLM_ENDPOINT"]
		<-ctx.Done()
		return engine.ProbeResult{}, ctx.Err()
	}

	bridgeTokenFile := filepath.Join(t.TempDir(), "bridge.token")
	must(t, os.WriteFile(bridgeTokenFile, []byte("test-bridge-token-for-ephemeral-reap"), 0o600))
	s, _, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
		c.BridgeEndpoint = "http://127.0.0.1:18174/v1"
		c.BridgeTokenFile = bridgeTokenFile
	})

	manage(t, s, "PUT", "/api/generation/profiles/agent-codex",
		`{"mode":"agent_bridge","bridge_kind":"codex","model":"gpt-5.4"}`, http.StatusOK)

	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"agent-codex","revision":1},"operation_id":"op-bridge-reap"}`, http.StatusAccepted)

	var ep string
	select {
	case ep = <-endpointCh:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge probe did not receive ephemeral endpoint")
	}
	if !strings.HasPrefix(ep, "http://127.0.0.1:") || ep == "http://127.0.0.1:18174/v1" {
		t.Fatalf("expected isolated ephemeral bridge endpoint, got %q", ep)
	}

	delRes := decodeResponse[struct {
		Result struct {
			OperationID        string `json:"operation_id"`
			LocalProcessExited bool   `json:"local_process_exited"`
		} `json:"result"`
	}](t, manage(t, s, "DELETE", "/v1/generation/validations/op-bridge-reap", "", http.StatusOK))
	if !delRes.Result.LocalProcessExited {
		t.Fatalf("expected local_process_exited=true, got %+v", delRes)
	}

	// Ephemeral completion bridge MUST already be shut down before DELETE returned.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(strings.TrimSuffix(ep, "/v1") + "/health")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("ephemeral completion bridge at %s is still accepting connections after DELETE returned", ep)
	}
}

func TestServerShutdownCancelsInFlightValidationsAndPersistsOwnerInterrupted(t *testing.T) {
	ctx := context.Background()
	started := make(chan string, 2)
	var mu sync.Mutex
	exited := map[string]bool{}

	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(pCtx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
		model := req.Environment["LLM_MODEL"]
		if strings.HasSuffix(model, "fast-pass") {
			return engine.ProbeResult{Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true, StructuredMode: "json_schema"}, nil
		}
		started <- model
		<-pCtx.Done()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		exited[model] = true
		mu.Unlock()
		return engine.ProbeResult{}, pCtx.Err()
	}

	s, store, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
	})

	p1 := decodeResponse[generationView](t, manage(t, s, "PUT", "/api/generation/profiles/p-unval",
		`{"mode":"provider","provider":"ollama","model":"slow-unval","endpoint":"http://127.0.0.1:11434"}`, http.StatusOK))
	p2 := decodeResponse[generationView](t, manage(t, s, "PUT", "/api/generation/profiles/p-passed",
		`{"mode":"provider","provider":"ollama","model":"fast-pass","endpoint":"http://127.0.0.1:11434"}`, http.StatusOK))

	// First pass p2, then switch model behavior via another slow probe on p2? Use same digest by revalidating p2 after changing probeFn!
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p-passed","revision":1},"operation_id":"op-p2-pass"}`, http.StatusAccepted)
	if got := pollValidationUntilTerminal(t, s, "op-p2-pass"); got["status"] != "passed" {
		t.Fatalf("p2 initial pass failed: %+v", got)
	}

	// Switch probeFn so both p1 and p2 revalidation block until shutdown cancels them.
	ctrl.probeFn = func(pCtx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
		model := req.Environment["LLM_MODEL"]
		started <- model
		<-pCtx.Done()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		exited[model] = true
		mu.Unlock()
		return engine.ProbeResult{}, pCtx.Err()
	}

	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p-unval","revision":1},"operation_id":"op-shut-unval"}`, http.StatusAccepted)
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p-passed","revision":1},"operation_id":"op-shut-passed"}`, http.StatusAccepted)
	<-started
	<-started

	shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	must(t, s.Shutdown(shutCtx))

	mu.Lock()
	exitedCount := len(exited)
	mu.Unlock()
	if exitedCount != 2 {
		t.Fatalf("Shutdown returned before both probes exited: exited=%d", exitedCount)
	}

	rec1, err := store.GetGenerationValidation(ctx, "op-shut-unval")
	must(t, err)
	if rec1.Status != "result_unknown" || rec1.ErrorCode != "validation_owner_interrupted" || rec1.RegisteredRevision != 1 || rec1.Digest != p1.Digest {
		t.Fatalf("unexpected shutdown record for unvalidated profile: %+v", rec1)
	}

	rec2, err := store.GetGenerationValidation(ctx, "op-shut-passed")
	must(t, err)
	if rec2.Status != "result_unknown" || rec2.ErrorCode != "validation_owner_interrupted" || rec2.RegisteredRevision != 0 || rec2.Digest != p2.Digest {
		t.Fatalf("unexpected shutdown record for passed profile: %+v", rec2)
	}
	valid2, err := store.IsGenerationDigestValidated(ctx, p2.Digest)
	must(t, err)
	if !valid2 {
		t.Fatal("Shutdown interrupted revalidation revoked existing passed state")
	}

	// New validation requests after shutdown are rejected cleanly.
	requireCode(t, manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p-unval","revision":1},"operation_id":"op-after-shutdown"}`, http.StatusServiceUnavailable),
		"generation_validation_unavailable")
}

func TestGetGenerationValidationSinglePKAndSecretRedactionInvariants(t *testing.T) {
	ctx := context.Background()
	const secret1 = "secretalpha111222"
	const secret2 = "secretbeta333444"
	const bridgeSecret = "secretbridge555666"

	bridgeFile := filepath.Join(t.TempDir(), "bridge.token")
	must(t, os.WriteFile(bridgeFile, []byte(bridgeSecret), 0o600))
	s, store, _ := testServer(t, func(c *Config) {
		c.BridgeEndpoint = "http://127.0.0.1:18174/v1"
		c.BridgeTokenFile = bridgeFile
	})

	p1 := decodeResponse[generationView](t, manage(t, s, "PUT", "/api/generation/profiles/p1",
		fmt.Sprintf(`{"mode":"provider","provider":"openai","model":"m1","endpoint":"https://provider.invalid/v1","api_key":%q}`, secret1), http.StatusOK))
	manage(t, s, "PUT", "/api/generation/profiles/p2",
		fmt.Sprintf(`{"mode":"provider","provider":"openai","model":"m2","endpoint":"https://provider.invalid/v1","api_key":%q}`, secret2), http.StatusOK)

	// 1. Validation whose operation_id embeds a secret MUST be hidden as 404 (safeModelDigest filter).
	taintedOp := "op-" + secret1
	must(t, store.RecordGenerationValidation(ctx, domain.GenerationValidation{
		OperationID: taintedOp, ProfileID: "p1", ProfileRevision: 1, Digest: p1.Digest,
		RegisteredRevision: 1, Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true,
	}))
	requireCode(t, manage(t, s, "GET", "/v1/generation/validations/"+taintedOp, "", http.StatusNotFound), "generation_validation_not_found")
	requireCode(t, manage(t, s, "DELETE", "/v1/generation/validations/"+taintedOp, "", http.StatusNotFound), "generation_validation_not_found")

	// 2. Validation whose error_code / agent_defaults embeds another profile's secret or bridge token MUST be redacted.
	must(t, store.RecordGenerationValidation(ctx, domain.GenerationValidation{
		OperationID: "op-clean-id", ProfileID: "p1", ProfileRevision: 1, Digest: p1.Digest,
		RegisteredRevision: 2, Status: "failed",
		ErrorCode:   "err_" + secret2,
		PayloadJSON: fmt.Sprintf(`{"agent_defaults":{"model":"gpt-%s","provider":"openai"}}`, bridgeSecret),
	}))
	w := manage(t, s, "GET", "/v1/generation/validations/op-clean-id", "", http.StatusOK)
	raw := w.Body.String()
	for _, secret := range []string{secret1, secret2, bridgeSecret} {
		if strings.Contains(raw, secret) {
			t.Fatalf("GET /v1/generation/validations leaked secret %q in %s", secret, raw)
		}
	}
	got := decodeResponse[map[string]any](t, w)
	if got["status"] != "failed" || got["error_code"] != "" || got["agent_defaults"] != nil {
		t.Fatalf("unexpected redacted validation view: %+v", got)
	}

	// 3. Registering a validation with an operation_id containing another profile's secret is rejected.
	requireCode(t, manage(t, s, "POST", "/v1/generation/validations",
		fmt.Sprintf(`{"profile":{"id":"p1","revision":1},"operation_id":"op-%s"}`, secret2), http.StatusBadRequest), "invalid_argument")

	// 4. Non-existent or malformed op returns 404.
	requireCode(t, manage(t, s, "GET", "/v1/generation/validations/does-not-exist", "", http.StatusNotFound), "generation_validation_not_found")
	requireCode(t, manage(t, s, "GET", "/v1/generation/validations/bad.op$id", "", http.StatusNotFound), "generation_validation_not_found")
}

func TestDeleteReportsCleanupUnconfirmedWhenSubprocessGroupFailsToExit(t *testing.T) {
	started := make(chan struct{}, 1)
	ctrl := &controllableProbeEngine{fakeEngine: newFakeEngine()}
	ctrl.probeFn = func(ctx context.Context, _ engine.ProbeRequest) (engine.ProbeResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return engine.ProbeResult{}, fmt.Errorf("run engines.cognee.probe: %w",
			errors.Join(ctx.Err(), errors.New("Cognee subprocess group 4242 did not exit")))
	}

	s, _, _ := testServer(t, func(c *Config) {
		c.Engine = ctrl
		c.Models = ctrl
	})
	manage(t, s, "PUT", "/api/generation/profiles/p-stuck",
		`{"mode":"provider","provider":"ollama","model":"qwen3","endpoint":"http://127.0.0.1:11434"}`, http.StatusOK)
	manage(t, s, "POST", "/v1/generation/validations",
		`{"profile":{"id":"p-stuck","revision":1},"operation_id":"op-stuck"}`, http.StatusAccepted)
	<-started

	delRes := decodeResponse[struct {
		Result struct {
			OperationID        string `json:"operation_id"`
			LocalProcessExited bool   `json:"local_process_exited"`
		} `json:"result"`
	}](t, manage(t, s, "DELETE", "/v1/generation/validations/op-stuck", "", http.StatusOK))
	if delRes.Result.LocalProcessExited {
		t.Fatalf("expected local_process_exited=false when subprocess group did not exit: %+v", delRes)
	}
	got := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/validations/op-stuck", "", http.StatusOK))
	if got["status"] != "result_unknown" || got["error_code"] != "validation_cleanup_unconfirmed" {
		t.Fatalf("unexpected cleanup_unconfirmed receipt: %+v", got)
	}
}

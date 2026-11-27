package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func generationPollingApp(t *testing.T, handler http.HandlerFunc) (*testHost, chan func()) {
	t.Helper()
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	server := newTestHTTP(handler)

	a := newTestHost(Config{GenerationSetupOnly: true, GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.client.HTTP = server.client
	updates := make(chan func(), 256)
	a.dispatch = func(fn func()) { updates <- fn }
	a.integrations.ValidationOperation = "ui-long-validation"
	a.integrations.Validation.Status = "running"
	a.integrations.Message = "正在验证"
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-long-validation", Kind: "validations", Profile: "local", Revision: 3}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a.integrations.ValidationCancel != nil {
			a.integrations.ValidationCancel()
		}
	})
	return a, updates
}

func TestGenerationPollingReceivesOriginalTerminalAfterNinetyRunningReads(t *testing.T) {
	var reads, mutations atomic.Int32
	a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
			http.Error(w, "unexpected mutation", 500)
			return
		}
		if r.URL.Path == "/v1/generation" {
			json.NewEncoder(w).Encode(GenerationDirectory{})
			return
		}
		status := "running"
		if reads.Add(1) > 90 {
			status = "passed"
		}
		json.NewEncoder(w).Encode(GenerationValidation{Status: status, Plain: true, Schema: true, Quality: true})
	})
	a.pollGenerationValidationWithTiming("ui-long-validation", time.Nanosecond, 5*time.Second)
	deadline := time.After(5 * time.Second)
	for a.integrations.Validation.Status != "passed" {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("observation ended before the original terminal receipt")
		}
	}
	pending, err := a.loadGenerationOperations()
	if reads.Load() != 91 || mutations.Load() != 0 || err != nil || len(pending) != 0 || a.integrations.Message != "" {
		t.Fatal("terminal reconciliation repeated a model operation or lost its journal state")
	}
}

func TestGenerationPollingDeadlineKeepsOriginalOperationWithoutCancellingModel(t *testing.T) {
	var mutations atomic.Int32
	a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
		}
		json.NewEncoder(w).Encode(GenerationValidation{Status: "running"})
	})
	a.pollGenerationValidationWithTiming("ui-long-validation", time.Hour, 30*time.Millisecond)
	deadline := time.After(3 * time.Second)
	for !strings.Contains(a.integrations.Error, "核对原验证") {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("observation expiry did not explain how to reconcile the original receipt")
		}
	}
	pending, err := a.loadGenerationOperations()
	if err != nil || len(pending) != 1 || mutations.Load() != 0 || a.integrations.Validation.Status != "running" || a.integrations.ValidationOperation != "ui-long-validation" {
		t.Fatal("observation expiry changed or cancelled the backend operation")
	}
}

func TestCancelGenerationValidationHandlesExitUnconfirmedAnd404(t *testing.T) {
	t.Run("local_process_exited true transitions to interrupted and clears journal", func(t *testing.T) {
		prevInterval := generationValidationPollInterval
		generationValidationPollInterval = time.Millisecond
		t.Cleanup(func() { generationValidationPollInterval = prevInterval })

		var deleted atomic.Bool
		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodDelete && r.URL.Path == "/v1/generation/validations/ui-long-validation":
				deleted.Store(true)
				json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{"operation_id": "ui-long-validation", "local_process_exited": true},
				})
			case r.Method == http.MethodGet && r.URL.Path == "/v1/generation/validations/ui-long-validation":
				if deleted.Load() {
					json.NewEncoder(w).Encode(GenerationValidation{Status: "failed", ErrorCode: "validation_interrupted"})
					return
				}
				json.NewEncoder(w).Encode(GenerationValidation{Status: "running"})
			case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
				json.NewEncoder(w).Encode(GenerationDirectory{})
			default:
				http.NotFound(w, r)
			}
		})

		a.cancelGenerationValidation()
		deadline := time.After(5 * time.Second)
		for a.integrations.Busy || a.integrations.Validation.Status != "failed" {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("cancel did not reach terminal interrupted state: busy=%v val=%+v", a.integrations.Busy, a.integrations.Validation)
			}
		}
		if a.integrations.Validation.ErrorCode != "validation_interrupted" || a.integrations.Message != "" {
			t.Fatalf("unexpected cancelled state: val=%+v msg=%q", a.integrations.Validation, a.integrations.Message)
		}
		if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 0 {
			t.Fatalf("cancelled validation left pending op: %v (%v)", pending, err)
		}
	})

	t.Run("local_process_exited false surfaces unconfirmed exit error", func(t *testing.T) {
		prevInterval := generationValidationPollInterval
		generationValidationPollInterval = time.Hour
		t.Cleanup(func() { generationValidationPollInterval = prevInterval })

		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodDelete:
				json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{"operation_id": "ui-long-validation", "local_process_exited": false},
				})
			case http.MethodGet:
				json.NewEncoder(w).Encode(GenerationValidation{Status: "running"})
			}
		})
		a.cancelGenerationValidation()
		deadline := time.After(5 * time.Second)
		for a.integrations.Busy || !strings.Contains(a.integrations.Error, "尚未确认验证进程已退出") {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("expected unconfirmed exit error, got busy=%v err=%q", a.integrations.Busy, a.integrations.Error)
			}
		}
		if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 1 {
			t.Fatalf("unconfirmed exit should keep pending operation: %v (%v)", pending, err)
		}
	})

	t.Run("delete 404 clears journal and allows retry", func(t *testing.T) {
		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"generation_validation_not_found"}`))
				return
			}
			json.NewEncoder(w).Encode(GenerationDirectory{})
		})
		a.cancelGenerationValidation()
		deadline := time.After(5 * time.Second)
		for a.integrations.Busy || a.integrations.Validation.ErrorCode != "validation_not_recorded" {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("delete 404 did not transition to validation_not_recorded: busy=%v val=%+v", a.integrations.Busy, a.integrations.Validation)
			}
		}
		if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 0 {
			t.Fatalf("delete 404 did not clear pending journal: %v (%v)", pending, err)
		}
	})
}

func TestGenerationDirectoryValidatedAndLatestValidationWithCancelledRevalidations(t *testing.T) {
	ref := &GenerationRef{ID: "prov", Revision: 1}
	row := GenerationRow{
		Revision: 1,
		Digest:   "digest-passed",
		Profile:  GenerationProfile{Mode: "provider", Provider: "openai", Model: "gpt-test", Endpoint: "https://example.test/v1"},
	}

	// Existing passed at rev=1 plus two cancelled revalidations at rev=0 must preserve validated=true and unique latestValidation.
	dir := GenerationDirectory{
		Profiles: map[string][]GenerationRow{"prov": {row}},
		Validations: map[string]GenerationValidation{
			"op-pass":     {Status: "passed", Digest: "digest-passed", Plain: true, Schema: true, Quality: true, RegisteredRevision: 1},
			"op-cancel-1": {Status: "failed", Digest: "digest-passed", ErrorCode: "validation_interrupted", RegisteredRevision: 0},
			"op-cancel-2": {Status: "failed", Digest: "digest-passed", ErrorCode: "validation_interrupted", RegisteredRevision: 0},
		},
	}
	if !dir.validated(ref) {
		t.Fatal("cancelled revalidations at rev=0 revoked existing passed validation")
	}
	latestID, latest := dir.latestValidation("digest-passed")
	if latestID != "op-pass" || latest.Status != "passed" {
		t.Fatalf("latestValidation = (%q, %+v), want op-pass", latestID, latest)
	}

	// Unvalidated digest with consecutive cancellations at rev=1 and rev=2 must not tie at 0==0.
	unvalRow := GenerationRow{Revision: 1, Digest: "digest-unval", Profile: row.Profile}
	unvalDir := GenerationDirectory{
		Profiles: map[string][]GenerationRow{"prov": {unvalRow}},
		Validations: map[string]GenerationValidation{
			"op-c1": {Status: "failed", Digest: "digest-unval", ErrorCode: "validation_interrupted", RegisteredRevision: 1},
			"op-c2": {Status: "failed", Digest: "digest-unval", ErrorCode: "validation_interrupted", RegisteredRevision: 2},
		},
	}
	if unvalDir.validated(ref) {
		t.Fatal("cancelled-only digest reported as validated")
	}
	latestID, latest = unvalDir.latestValidation("digest-unval")
	if latestID != "op-c2" || latest.ErrorCode != "validation_interrupted" {
		t.Fatalf("unvalidated latestValidation = (%q, %+v), want op-c2", latestID, latest)
	}
}

func TestValidationCleanupUnconfirmedSurvivesPollAndRefresh(t *testing.T) {
	row := GenerationRow{
		Revision: 3,
		Digest:   "digest-unconfirmed",
		Profile:  GenerationProfile{Mode: "provider", Provider: "ollama", Model: "qwen3", Endpoint: "http://127.0.0.1:11434"},
	}
	cleanupReceipt := GenerationValidation{
		Reference: &GenerationRef{ID: "local", Revision: 3},
		Digest:    row.Digest,
		Status:    "result_unknown",
		ErrorCode: "validation_cleanup_unconfirmed",
	}
	dir := GenerationDirectory{
		Revision:    5,
		Profiles:    map[string][]GenerationRow{"local": {row}},
		Validations: map[string]GenerationValidation{"ui-long-validation": cleanupReceipt},
	}

	t.Run("settings poll and subsequent RefreshIntegrations preserve pending journal", func(t *testing.T) {
		var refreshes atomic.Int32
		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/generation/validations/ui-long-validation":
				json.NewEncoder(w).Encode(cleanupReceipt)
			case "/v1/generation":
				refreshes.Add(1)
				json.NewEncoder(w).Encode(dir)
			default:
				http.NotFound(w, r)
			}
		})
		a.pollGenerationValidationWithTiming("ui-long-validation", time.Millisecond, 5*time.Second)
		deadline := time.After(5 * time.Second)
		for refreshes.Load() == 0 || a.integrations.Loading || a.integrations.Validation.ErrorCode != "validation_cleanup_unconfirmed" {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("did not complete poll+refresh: refreshes=%d loading=%v val=%+v",
					refreshes.Load(), a.integrations.Loading, a.integrations.Validation)
			}
		}
		pending, err := a.loadGenerationOperations()
		if err != nil || len(pending) != 1 || pending[0].Operation != "ui-long-validation" {
			t.Fatalf("RefreshIntegrations cleared validation_cleanup_unconfirmed pending op: %+v (%v)", pending, err)
		}
		if len(a.integrations.PendingOperations) != 1 {
			t.Fatalf("in-memory PendingOperations lost cleanup_unconfirmed lock: %+v", a.integrations.PendingOperations)
		}
	})

	t.Run("onboarding validate and subsequent RefreshIntegrations preserve pending journal", func(t *testing.T) {
		var opID atomic.Value
		var refreshes atomic.Int32
		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/validations":
				var body struct {
					OperationID string `json:"operation_id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				opID.Store(body.OperationID)
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{
					"status":       "running",
					"operation_id": body.OperationID,
					"result":       map[string]any{"operation_id": body.OperationID, "status": "running"},
				})
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/generation/validations/"):
				json.NewEncoder(w).Encode(cleanupReceipt)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
				refreshes.Add(1)
				id, _ := opID.Load().(string)
				out := dir
				if id != "" {
					out.Validations = map[string]GenerationValidation{id: cleanupReceipt}
				}
				json.NewEncoder(w).Encode(out)
			default:
				http.NotFound(w, r)
			}
		})
		// Start from a clean journal for onboarding run.
		if !a.finishGenerationOperation("ui-long-validation") {
			t.Fatal("failed to clear seed operation")
		}
		a.initialSetup = true
		controlClient, ok := a.accessClient()
		if !ok {
			t.Fatal("missing onboarding control client")
		}
		run := onboardingGenerationRun{
			client:     controlClient,
			directory:  dir,
			operations: a.generationOperationsDir(),
			ref:        &GenerationRef{ID: "local", Revision: 3},
		}
		err := run.validate(context.Background(), func(GenerationValidation, time.Time) {}, func() bool { return true })
		if err == nil {
			t.Fatal("expected onboarding validate to fail on validation_cleanup_unconfirmed")
		}
		recordedOp, _ := opID.Load().(string)
		if recordedOp == "" {
			t.Fatal("missing recorded onboarding validation op")
		}
		beforeRefresh := refreshes.Load()
		a.RefreshIntegrations()
		deadline := time.After(5 * time.Second)
		for refreshes.Load() == beforeRefresh || a.integrations.Loading {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("onboarding RefreshIntegrations did not complete: refreshes=%d loading=%v", refreshes.Load(), a.integrations.Loading)
			}
		}
		pending, loadErr := a.loadGenerationOperations()
		if loadErr != nil || len(pending) != 1 || pending[0].Operation != recordedOp {
			t.Fatalf("onboarding RefreshIntegrations cleared validation_cleanup_unconfirmed op: %+v (%v)", pending, loadErr)
		}
	})

	t.Run("DELETE with missing operation_id is fail-closed", func(t *testing.T) {
		prevInterval := generationValidationPollInterval
		generationValidationPollInterval = time.Hour
		t.Cleanup(func() { generationValidationPollInterval = prevInterval })

		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodDelete:
				w.Write([]byte(`{"result":{}}`))
			case http.MethodGet:
				json.NewEncoder(w).Encode(GenerationValidation{Status: "running"})
			}
		})
		a.cancelGenerationValidation()
		deadline := time.After(5 * time.Second)
		for a.integrations.Busy || !strings.Contains(a.integrations.Error, "尚未确认验证进程已退出") {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("malformed DELETE receipt did not fail closed: busy=%v err=%q", a.integrations.Busy, a.integrations.Error)
			}
		}
		if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 1 {
			t.Fatalf("malformed DELETE receipt cleared pending journal: %+v (%v)", pending, err)
		}
	})

	t.Run("sync POST result_unknown validation_cleanup_unconfirmed keeps journal", func(t *testing.T) {
		a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/validations":
				json.NewEncoder(w).Encode(cleanupReceipt)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
				json.NewEncoder(w).Encode(dir)
			default:
				http.NotFound(w, r)
			}
		})
		if !a.finishGenerationOperation("ui-long-validation") {
			t.Fatal("failed to clear seed op")
		}
		a.integrations.Generation = dir
		a.integrations.editProfile("local", row)
		a.generationAction("validations")
		deadline := time.After(5 * time.Second)
		for a.integrations.Busy || a.integrations.Validation.ErrorCode != "validation_cleanup_unconfirmed" {
			select {
			case fn := <-updates:
				fn()
			case <-deadline:
				t.Fatalf("generationAction did not finish: busy=%v val=%+v", a.integrations.Busy, a.integrations.Validation)
			}
		}
		if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 1 {
			t.Fatalf("generationAction cleared validation_cleanup_unconfirmed journal: %+v (%v)", pending, err)
		}
	})
}

func TestDiscardDraftDuringActiveAsyncValidationPreservesProgressAndStopButton(t *testing.T) {
	prevInterval := generationValidationPollInterval
	generationValidationPollInterval = time.Hour
	t.Cleanup(func() { generationValidationPollInterval = prevInterval })

	row := GenerationRow{
		Revision: 3,
		Digest:   "digest-active",
		Profile:  GenerationProfile{Mode: "provider", Provider: "ollama", Model: "qwen3", Endpoint: "http://127.0.0.1:11434"},
	}
	dir := GenerationDirectory{
		Revision: 4,
		Profiles: map[string][]GenerationRow{"local": {row}},
	}
	var pollReads atomic.Int32
	a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/generation/validations/ui-long-validation":
			pollReads.Add(1)
			json.NewEncoder(w).Encode(GenerationValidation{Status: "running", Phase: "probe"})
		case "/v1/generation":
			json.NewEncoder(w).Encode(dir)
		default:
			http.NotFound(w, r)
		}
	})
	a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
	a.integrations.Generation = dir
	a.integrations.editProfile("local", row)
	a.integrations.ValidationOperation = "ui-long-validation"
	a.integrations.Validation = GenerationValidation{Status: "running", Phase: "probe"}
	a.integrations.ValidationStartedAt = time.Now()
	a.integrations.Model = "edited-draft-model"

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.generationSettings(c) })
	}, 1200, 820)
	tt.Frame()
	if !tt.HasText("停止验证") || tt.HasText("待核对操作") {
		t.Fatalf("active validation editor should show 停止验证 without duplicate 待核对操作: %v", tt.Texts())
	}

	clickProjectUI(t, tt, "取消修改")
	deadline := time.After(5 * time.Second)
	for pollReads.Load() == 0 {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("discarding draft did not resume polling for active validation")
		}
	}
	// Drain queued UI update from poll response.
	for len(updates) > 0 {
		(<-updates)()
	}
	if a.integrations.Model != "qwen3" {
		t.Fatalf("draft model was not restored: got %q, want qwen3", a.integrations.Model)
	}
	if a.integrations.ValidationOperation != "ui-long-validation" || !generationValidationActive(a.integrations.Validation) {
		t.Fatalf("active validation state lost after discarding draft: op=%q val=%+v",
			a.integrations.ValidationOperation, a.integrations.Validation)
	}
	tt.Frame()
	if !tt.HasText("停止验证") || tt.HasText("待核对操作") {
		t.Fatalf("after discarding draft, expected 停止验证 visible without duplicate 待核对操作: %v", tt.Texts())
	}
}

func TestReopenWithPendingAsyncValidationShowsReviewAndRecoversOrUnlocks(t *testing.T) {
	prevInterval := generationValidationPollInterval
	generationValidationPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { generationValidationPollInterval = prevInterval })

	row := GenerationRow{
		Revision: 3,
		Digest:   "digest-reopen",
		Profile:  GenerationProfile{Mode: "provider", Provider: "ollama", Model: "qwen3", Endpoint: "http://127.0.0.1:11434"},
	}
	dir := GenerationDirectory{
		Revision: 4,
		Profiles: map[string][]GenerationRow{"local": {row}},
	}
	var finished atomic.Bool
	a, updates := generationPollingApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/generation":
			out := dir
			if finished.Load() {
				out.Validations = map[string]GenerationValidation{
					"ui-long-validation": {
						Reference: &GenerationRef{ID: "local", Revision: 3},
						Digest:    row.Digest,
						Status:    "passed",
						Plain:     true,
						Schema:    true,
						Quality:   true,
					},
				}
			}
			json.NewEncoder(w).Encode(out)
		case "/v1/generation/validations/ui-long-validation":
			if finished.Load() {
				json.NewEncoder(w).Encode(GenerationValidation{
					Reference: &GenerationRef{ID: "local", Revision: 3},
					Digest:    row.Digest,
					Status:    "passed",
					Plain:     true,
					Schema:    true,
					Quality:   true,
				})
				return
			}
			json.NewEncoder(w).Encode(GenerationValidation{Status: "running", Phase: "probe"})
		default:
			http.NotFound(w, r)
		}
	})
	// Simulate cold reopen: journal still holds ui-long-validation on disk, but UI memory is blank.
	a.integrations = IntegrationState{}
	a.RefreshIntegrations()
	deadline := time.After(5 * time.Second)
	for !a.integrations.GenerationLoaded || a.integrations.Loading {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("reopen RefreshIntegrations did not complete")
		}
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.generationSettings(c) })
	}, 1200, 820)
	tt.Frame()
	if !tt.HasText("待核对操作") || !tt.HasText("验证配置 · local") || !tt.HasText("核对原操作") {
		t.Fatalf("reopened pending async validation missing review card: %v", tt.Texts())
	}

	// Click "核对原操作" -> resumes active polling, displays "停止验证", hides duplicate pending card.
	clickProjectUI(t, tt, "核对原操作")
	deadline = time.After(5 * time.Second)
	for !generationValidationActive(a.integrations.Validation) {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatalf("reviewing pending validation did not recover running state: val=%+v", a.integrations.Validation)
		}
	}
	tt.Frame()
	if !tt.HasText("停止验证") || tt.HasText("待核对操作") {
		t.Fatalf("recovered active validation should show 停止验证 without duplicate 待核对操作: %v", tt.Texts())
	}

	// Once backend completes, poller clears .ui-operations and unlocks buttons.
	finished.Store(true)
	deadline = time.After(5 * time.Second)
	for a.integrations.Validation.Status != "passed" || a.integrations.Loading || len(a.integrations.PendingOperations) != 0 {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatalf("recovered validation did not unlock upon completion: val=%+v loading=%v pending=%+v",
				a.integrations.Validation, a.integrations.Loading, a.integrations.PendingOperations)
		}
	}
	if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 0 {
		t.Fatalf("completed validation left pending journal on disk: %+v (%v)", pending, err)
	}
}

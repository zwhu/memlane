package settings

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

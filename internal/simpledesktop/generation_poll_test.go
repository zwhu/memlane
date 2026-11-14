package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func generationPollingApp(t *testing.T, handler http.HandlerFunc) (*App, chan func()) {
	t.Helper()
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	a := New(Config{GenerationSetupOnly: true, GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 256)
	a.Update = func(fn func()) { updates <- fn }
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

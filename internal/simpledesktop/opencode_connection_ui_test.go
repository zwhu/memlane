package simpledesktop

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenCodeKnownRejectedOperationClearsPendingWithoutMutation(t *testing.T) {
	root := t.TempDir()
	var mutations atomic.Int32
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
			http.Error(w, "unexpected mutation", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/connections/operations/ui-opencode-rejected":
			json.NewEncoder(w).Encode(map[string]any{
				"recorded": true, "status": "complete", "outcome": "rejected", "error_code": "host_configuration_changed",
				"operation_id": "ui-opencode-rejected", "instance_id": "opencode-test", "action": "activate",
				"result": map[string]any{"connection": AgentConnection{ID: "opencode-test", Kind: "opencode", Status: "prepared"}},
			})
		case "/v1/connections":
			json.NewEncoder(w).Encode(ConnectionsDirectory{})
		case "/v1/generation":
			json.NewEncoder(w).Encode(GenerationDirectory{})
		default:
			http.Error(w, "unexpected request", 500)
		}
	})
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	op := generationUIOperation{Operation: "ui-opencode-rejected", Kind: "connection_activate", Profile: "opencode-test"}
	if err := a.recordGenerationOperation(op); err != nil {
		t.Fatal(err)
	}
	a.reviewConnectionOperation(op)
	generationUpdate(t, updates)
	if !strings.Contains(a.integrations.Message, "确认拒绝") {
		t.Fatal(a.integrations.Message)
	}
	pending, err := a.loadGenerationOperations()
	if err != nil || len(pending) != 0 {
		t.Fatal("known rejection remained unknown", pending, err)
	}
	generationUpdate(t, updates)
	if mutations.Load() != 0 {
		t.Fatal("review resubmitted installation")
	}
	if !connectionActionAllowed(AgentConnection{Kind: "opencode", Detected: true, Status: "prepared"}, "prepare") {
		t.Fatal("configuration conflict cannot be prepared again")
	}
	if connectionActionAllowed(AgentConnection{Kind: "opencode", Detected: true, Status: "integration_pending"}, "prepare") {
		t.Fatal("missing component became bindable")
	}
}

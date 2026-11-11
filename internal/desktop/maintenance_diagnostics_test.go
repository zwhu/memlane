package desktop

import (
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func TestDiagnosticSummaryExcludesConfigurationAndRecordPayloads(t *testing.T) {
	a := New("test", nil)
	a.runtimeStatus = RuntimeStatus{Phase: "ready", Message: "runtime-private-detail"}
	a.libraryError = "library-private-detail"
	a.items = []MemoryItem{{ID: "memory", Text: "private-knowledge-body"}}
	revision := records.ProcessingRevision{ID: "revision", Spec: records.ProcessingSpec{Mode: "provider", Provider: "OpenAI", Model: "model", Endpoint: "https://private-endpoint.invalid", Credential: "private-credential-reference"}}
	a.processing.Loaded = true
	a.processing.Configuration.Current = records.ProcessingSelection{Active: true, Revision: &revision}
	a.processing.Configuration.Runtime = map[string]records.ProcessingReadiness{revision.ID: {State: "ready"}}
	summary := a.maintenanceDiagnosticSummary()
	for _, secret := range []string{"runtime-private-detail", "library-private-detail", "private-knowledge-body", "private-endpoint", "private-credential-reference"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("diagnostic summary copied configuration/record payload:%s", secret)
		}
	}
	if !strings.Contains(summary, "服务阶段：ready") {
		t.Fatal("runtime receipt missing from diagnostic summary")
	}
}

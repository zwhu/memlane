package agentbridge

import (
	"encoding/json"
	"testing"
)

func TestSearchSchemaMatchesScopeSelectionContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  string
		valid bool
	}{
		{"project", `{"query":"trace","scope":"project"}`, true},
		{"multiple", `{"query":"trace","scopes":["project","common"]}`, true},
		{"implicit", `{"query":"trace"}`, true},
		{"empty_multi", `{"query":"trace","scope":"project","scopes":[]}`, true},
		{"empty_single", `{"query":"trace","scope":"","scopes":["project"]}`, true},
		{"ambiguous", `{"query":"trace","scope":"project","scopes":["project"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validate("memory_search", json.RawMessage(tc.args))
			if (err == nil) != tc.valid {
				t.Fatalf("scope contract valid=%v: %v", tc.valid, err)
			}
		})
	}
}

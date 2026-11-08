package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestJSONImportCannotStampOfficialOriginalMetadata(t *testing.T) {
	s := &Server{}
	for _, draft := range []*records.CogneeImport{
		{Material: &records.CogneeOriginal{Instance: uuid.NewString(), Session: "codex_synthetic", OfficialState: "persisted"}},
		{Native: &records.CodexOriginal{Instance: uuid.NewString(), Session: "codex_synthetic"}},
	} {
		_, err := s.perform(context.Background(), Request{Op: "cognee_import", UserConfirmed: true,
			CogneeImport: draft})
		var denied *records.AccessError
		if !errors.As(err, &denied) || denied.Code != "invalid_provenance" {
			t.Fatalf("claimed JSON source stamp accepted: %v", err)
		}
	}
}

// Synthetic transport regression only; real App/official evidence is separate.
func TestOfficialOriginalCurrentAuthorizationAndText(t *testing.T) {
	for _, test := range []struct {
		name, text, state string
		status            int
		present           bool
	}{
		{"available", "actual original", "available", http.StatusOK, true},
		{"changed", "changed upstream", "upstream_changed", http.StatusOK, true},
		{"removed", "", "upstream_unavailable", http.StatusOK, false},
		{"revoked", "", "", http.StatusForbidden, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			instance, dataset, rawID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			material := records.CogneeMaterial{Instance: instance, Dataset: dataset, Session: "codex_synthetic", CogneeVersion: "1.6.7"}
			if test.present {
				material.Raw = []records.CogneeRaw{{ID: rawID, Kind: "qa", Sequence: 0, Persisted: true, Text: test.text}}
			}
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "synthetic-key" || r.Header.Get("Authorization") != "" {
					t.Error("wrong credential transport")
				}
				if r.URL.Query().Get("expected_instance") != instance {
					t.Error("missing instance check")
				}
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(material)
			}))
			defer host.Close()
			s := &Server{config: Config{Root: t.TempDir()}}
			if err := s.writeCogneeToken(instance, dataset, "synthetic-key"); err != nil {
				t.Fatal(err)
			}
			v := records.Version{Text: "actual original", SourceDetail: &records.AgentSource{References: []string{"cognee://" + dataset + "/sessions/codex_synthetic/qa/" + rawID}, Official: &records.CogneeOriginal{Instance: instance, Session: "codex_synthetic", Kind: "qa"}}}
			state, err := s.verifyOfficialOriginal(context.Background(), records.CogneeBinding{Instance: instance, Dataset: dataset, Endpoint: host.URL}, v)
			if test.status == http.StatusForbidden {
				if err == nil || state != "" {
					t.Fatal("revoked authorization still readable")
				}
			} else if err != nil || state != test.state {
				t.Fatalf("state=%s err=%v", state, err)
			}
		})
	}
}

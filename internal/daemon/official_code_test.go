package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func TestOfficialCodePreflightAndNoLegacyScanOrQuery(t *testing.T) {
	for _, mode := range []string{"enable", "wrong_user", "not_empty", "old_component"} {
		t.Run(mode, func(t *testing.T) {
			s, c, _ := codeFaultFixture(t)
			ctx := context.Background()
			s.config.Root = t.TempDir()
			stopped, err := s.store.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation})
			if err != nil {
				t.Fatal(err)
			}
			scope := records.OfficialCodeScope{Instance: uuid.NewString(), Dataset: uuid.NewString(), User: uuid.NewString()}
			reads := 0
			var hasData atomic.Bool
			hasData.Store(mode == "not_empty")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected scan/write request")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					caps := []string{"official_code_scope_v1"}
					if mode == "old_component" {
						caps = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"instance": scope.Instance, "source_commit": official.CodingMemoryServiceCommit, "capabilities": caps})
					return
				}
				if r.URL.Path != "/api/v1/memlane/code/scope" || r.URL.Query().Get("dataset") != scope.Dataset || r.URL.Query().Get("expected_instance") != scope.Instance {
					t.Error("wrong scoped probe")
					w.WriteHeader(404)
					return
				}
				user := scope.User
				if mode == "wrong_user" {
					user = uuid.NewString()
				}
				json.NewEncoder(w).Encode(map[string]any{"instance": scope.Instance, "dataset": scope.Dataset, "user": user, "empty": !hasData.Load()})
			}))
			defer server.Close()
			scope.Endpoint = server.URL
			if err := s.writeCogneeToken(scope.Instance, scope.Dataset, "synthetic-token"); err != nil {
				t.Fatal(err)
			}
			a := records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: c.Project, Path: c.Worktree, ExpectedGeneration: stopped.Generation, Official: &scope}
			out, err := s.codeAction(ctx, a)
			if mode != "enable" {
				if err == nil {
					t.Fatal("unverified code scope enabled")
				}
				fresh, e := s.store.Code(ctx, c.ID)
				if e != nil || fresh.Enabled || fresh.Official != nil {
					t.Fatal("failed probe changed permission", fresh, e)
				}
				if mode == "old_component" && reads != 1 {
					t.Fatal("old component received scope probe")
				}
				return
			}
			if err != nil || out.Official == nil {
				t.Fatal(out, err)
			}
			legacyCaptures := 0
			s.codeCapture = func(context.Context, string, string) (codegraph.Manifest, error) {
				legacyCaptures++
				t.Error("official scope reached legacy scanner")
				return codegraph.Manifest{}, nil
			}
			s.refreshCode(ctx, out)
			fresh, err := s.store.Code(ctx, c.ID)
			if err != nil || fresh.Error != "official_code_backend_unavailable" || legacyCaptures != 0 {
				t.Fatal(fresh, err, legacyCaptures)
			}
			if _, err := s.queryCode(ctx, records.CodeQuery{ID: c.ID}); err == nil {
				t.Fatal("official state queried legacy graph")
			}
			stop, err := s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: out.Generation})
			if err != nil {
				t.Fatal(err)
			}
			hasData.Store(true)
			// Existing official data may be retained on re-enable, but omitting
			// the scope field must still run the real official preflight.
			on, err := s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: c.Project, Path: c.Worktree, ExpectedGeneration: stop.Generation})
			if err != nil || on.Official == nil || reads != 4 {
				t.Fatal("re-enable skipped live scope check", on, reads, err)
			}
		})
	}
}

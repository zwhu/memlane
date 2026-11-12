package daemon

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCogneeReadReauthorizationChecksServiceAndLateRevocation(t *testing.T) {
	for _, mode := range []string{"read", "upstream_denied", "revoked_in_flight", "stale_review"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db, err := store.Open(filepath.Join(root, "test.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := filepath.Join(root, "repo")
			if err = exec.Command("git", "init", "-q", repo).Run(); err != nil {
				t.Fatal(err)
			}
			project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic read review", Path: repo})
			if err != nil {
				t.Fatal(err)
			}
			draft := records.ConnectionDraft{Agent: "codex", Name: "synthetic", Project: project.ID, AllowedScopes: []string{project.ID}}
			c, err := db.ConfigureConnection(ctx, draft)
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset := uuid.NewString(), uuid.NewString()
			s := &Server{store: db, config: Config{Root: root}}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected request")
					w.WriteHeader(403)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/identity"):
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7", "adapter": "memlane-material-graph-v1"})
				case r.URL.Path == "/api/v1/datasets/"+dataset+"/data":
					if mode == "upstream_denied" {
						w.WriteHeader(403)
						return
					}
					if mode == "revoked_in_flight" {
						s.agentAccess.Lock()
						e := db.Disconnect(ctx, c.ID)
						s.agentAccess.Unlock()
						if e != nil {
							t.Error(e)
						}
					}
					json.NewEncoder(w).Encode([]records.OfficialDocument{})
				case r.URL.Path == "/api/v1/datasets/"+dataset+"/schema":
					json.NewEncoder(w).Encode(map[string]any{})
				default:
					t.Error("unexpected route", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			old := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: upstream.URL, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo}
			if _, err = db.BindCognee(ctx, old); err != nil {
				t.Fatal(err)
			}
			if err = s.writeCogneeToken(instance, dataset, "synthetic-token"); err != nil {
				t.Fatal(err)
			}
			if err = db.Disconnect(ctx, c.ID); err != nil {
				t.Fatal(err)
			}
			draft.ID = c.ID
			fresh, err := db.ConfigureConnection(ctx, draft)
			if err != nil {
				t.Fatal(err)
			}
			q := records.CogneeReadReauthorization{Previous: old, Operation: uuid.NewString(), Generation: fresh.Generation}
			if mode == "stale_review" {
				q.Previous.Operation = uuid.NewString()
			}
			if _, err = s.perform(ctx, Request{Op: "cognee_read_reauthorize", CogneeReadReauthorization: &q}); err == nil || calls != 0 {
				t.Fatal("unconfirmed review performed work", err, calls)
			}
			out, err := s.reauthorizeCogneeRead(ctx, q)
			rows, e := db.CogneeBindings(ctx, project.ID)
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e)
			}
			if mode != "read" {
				if err == nil || out.Operation != "" || rows[0] != old {
					t.Fatal("failed review changed grant", out, rows, err)
				}
				if mode == "stale_review" && calls != 0 {
					t.Fatal("stale review contacted service")
				}
				return
			}
			if err != nil || rows[0] != out || out.Generation != fresh.Generation {
				t.Fatal(out, err)
			}
			again, err := s.reauthorizeCogneeRead(ctx, q)
			if err != nil || again != out {
				t.Fatal("review retry failed", again, err)
			}
			// The existing credential-refresh flow must preserve the new receipt fields.
			refresh := out
			refresh.Operation = uuid.NewString()
			refresh.ReadReviewDigest = ""
			refresh.ReadPreviousOperation = ""
			refreshed, err := s.authorizeCognee(ctx, records.CogneeAuthorization{Binding: refresh, Credential: "synthetic-token"})
			if err != nil || refreshed != out {
				t.Fatal("credential refresh lost reviewed binding", refreshed, err)
			}
			if err = db.AuthorizeCogneeBinding(ctx, old); err == nil {
				t.Fatal("old read generation became authorized")
			}
		})
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestVisualizationDaemonCurrentBindingAndRevocation(t *testing.T) {
	for _, mode := range []string{"read", "disconnect_during_read", "submission_only", "wrong_dataset", "missing_project", "authorize", "authorize_revoked", "authorize_disconnect"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := filepath.Join(root, "synthetic-repo")
			if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
			project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "Synthetic graph", Path: repo})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Synthetic graph", Project: project.ID, AllowedScopes: []string{project.ID}})
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset := uuid.NewString(), uuid.NewString()
			s := &Server{store: db, config: Config{Root: root}}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected write or missing credential")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					json.NewEncoder(w).Encode(map[string]any{"instance": instance, "cognee_version": "1.6.7", "source_commit": official.CodingMemoryServiceCommit, "capabilities": []string{"official_visualization_v1"}})
					return
				}
				if r.URL.Path == "/api/v1/memlane/datasets/"+dataset+"/visualization-scope" {
					if mode == "authorize_revoked" {
						w.WriteHeader(403)
						return
					}
					if mode == "authorize_disconnect" {
						s.agentAccess.Lock()
						err := db.Disconnect(ctx, connection.ID)
						s.agentAccess.Unlock()
						if err != nil {
							t.Error(err)
						}
					}
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "dataset": dataset})
					return
				}
				if r.URL.Path != "/api/v1/memlane/datasets/"+dataset+"/visualization" {
					t.Error("wrong graph scope")
					w.WriteHeader(404)
					return
				}
				if mode == "disconnect_during_read" {
					s.agentAccess.Lock()
					err := db.Disconnect(ctx, connection.ID)
					s.agentAccess.Unlock()
					if err != nil {
						t.Error(err)
					}
				}
				json.NewEncoder(w).Encode(records.OfficialVisualization{Instance: instance, Dataset: dataset, Scope: "bounded_subgraph", MaxNodes: 1000, Payload: json.RawMessage(`{"nodes":[],"links":[],"color_maps":{}}`)})
			}))
			defer server.Close()
			binding := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: server.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, MaterialSubmission: mode == "submission_only"}
			if _, err := db.BindCognee(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err := s.writeCogneeToken(instance, dataset, "synthetic-token"); err != nil {
				t.Fatal(err)
			}
			q := records.OfficialVisualizationQuery{Project: project.ID, Instance: instance, Dataset: dataset, MaxNodes: 1000}
			if mode == "wrong_dataset" {
				q.Dataset = uuid.NewString()
			}
			if mode == "missing_project" {
				q.Project = ""
			}
			if mode == "authorize" || mode == "authorize_revoked" || mode == "authorize_disconnect" {
				out, err := s.perform(ctx, Request{Op: "official_visualization_authorize", OfficialVisualization: &q})
				if mode == "authorize" {
					if err != nil || out.(records.CogneeBinding) != binding {
						t.Fatal(out, err)
					}
				} else if err == nil {
					t.Fatal("revoked live scope accepted")
				}
				return
			}
			out, err := s.readOfficialVisualization(ctx, q)
			if mode == "read" {
				if err != nil || out.Payload == nil {
					t.Fatal(err)
				}
			} else if err == nil || out.Payload != nil {
				t.Fatal("unauthorized graph published", err)
			}
			if mode != "read" && mode != "disconnect_during_read" && reads != 0 {
				t.Fatal("unauthorized HTTP read", reads)
			}
			for _, table := range []string{"memories", "versions", "jobs", "code_jobs", "code_worktrees"} {
				var n int
				if err := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatal("graph read wrote data or scanned", table, n, err)
				}
			}
		})
	}
}

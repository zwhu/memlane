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
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestOfficialSessionDaemonReadsWithoutMirroringAndRejectsRevocation(t *testing.T) {
	for _, mode := range []string{"read", "disconnect_in_flight", "submission_only"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := filepath.Join(root, "synthetic-repo")
			if out, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
				t.Fatal(e, string(out))
			}
			project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic official session", Path: repo})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "synthetic session access", Project: project.ID, AllowedScopes: []string{project.ID}})
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
			selected := records.SessionSummary{Session: "codex_synthetic", Dataset: dataset, User: owner}
			s := &Server{store: db, config: Config{Root: root}}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected write or credential")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				if r.URL.Path != "/api/v1/sessions/codex_synthetic" {
					t.Error("unexpected path", r.URL.Path)
				}
				if mode == "disconnect_in_flight" {
					s.agentAccess.Lock()
					e := db.Disconnect(ctx, connection.ID)
					s.agentAccess.Unlock()
					if e != nil {
						t.Error(e)
					}
				}
				json.NewEncoder(w).Encode(records.SessionDetail{SessionSummary: selected, QACount: 1, QAs: []records.SessionQA{{ID: "synthetic-qa", Question: "合成问题", Answer: "合成回答"}}})
			}))
			defer server.Close()
			binding := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: server.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, MaterialSubmission: mode == "submission_only"}
			if _, err = db.BindCognee(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err = s.writeCogneeToken(instance, dataset, "synthetic-token"); err != nil {
				t.Fatal(err)
			}
			out, err := s.readOfficialSession(ctx, records.SessionQuery{Project: project.ID, Instance: instance, Dataset: dataset, Selected: &selected}, true)
			if mode == "read" {
				if err != nil || len(out.(records.SessionDetail).QAs) != 1 {
					t.Fatal("official read failed", err)
				}
			} else if err == nil || out != nil {
				t.Fatal("unapproved session read returned content", mode)
			}
			if mode == "submission_only" && reads != 0 {
				t.Fatal("material grant accessed historical sessions")
			}
			for _, table := range []string{"memories", "versions", "jobs"} {
				var n int
				if e := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatal("session read mirrored data", table, n, e)
				}
			}
		})
	}
}

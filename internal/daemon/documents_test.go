package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestOfficialDocumentDaemonRequiresCurrentGrantAndDoesNotMirror(t *testing.T) {
	for _, mode := range []string{"read", "disconnect_in_flight", "submission_only", "wrong_dataset", "empty_project", "session", "session_disconnect_in_flight", "session_submission_only", "knowledge", "knowledge_disconnect_in_flight", "knowledge_submission_only", "import_source", "import_source_disconnect_in_flight", "import_source_submission_only"} {
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
			project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic document read", Path: repo})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "synthetic official documents", Project: project.ID, AllowedScopes: []string{project.ID}})
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset := uuid.NewString(), uuid.NewString()
			selected := records.OfficialDocument{ID: uuid.NewString(), Dataset: dataset, Name: "合成官方文档", MIMEType: "text/plain"}
			target := records.OfficialDocument{ID: uuid.NewString(), Dataset: dataset, Name: "原始依据", MIMEType: "text/plain"}
			const parentText, targetText = "Preserved knowledge", "Preserved original source"
			if strings.HasPrefix(mode, "import_source") {
				library, memory := uuid.NewString(), uuid.NewString()
				meta := func(id, origin, body string, refs []records.OfficialDocumentReference) json.RawMessage {
					digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
					b, _ := json.Marshal(map[string]any{"memlane_import_v1": map[string]any{"import_id": id, "library": library, "project": project.ID, "memory": id, "revision": 1, "origin": origin, "digest": digest, "sources": refs}})
					return b
				}
				ref := records.OfficialDocumentReference{Document: target.ID, Memory: memory, Project: project.ID, Revision: 1, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(targetText)))}
				selected.Metadata = meta(uuid.NewString(), "knowledge", parentText, []records.OfficialDocumentReference{ref})
				target.Metadata = meta(memory, "raw", targetText, nil)
			}
			if strings.HasPrefix(mode, "session") || strings.HasPrefix(mode, "knowledge") {
				selected.Metadata = json.RawMessage(`{"node_set":["session_learnings","session_learnings:source-session"]}`)
			}
			row := records.SessionSummary{Session: "source-session", Dataset: dataset, User: uuid.NewString()}
			s := &Server{store: db, config: Config{Root: root}}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected write/credential")
					w.WriteHeader(403)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/identity") {
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/processing-status") {
					if mode == "knowledge_disconnect_in_flight" {
						s.agentAccess.Lock()
						e := db.Disconnect(ctx, connection.ID)
						s.agentAccess.Unlock()
						if e != nil {
							t.Error(e)
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"total": 1, "completed": 1, "pending": 0, "items": []any{map[string]any{"id": selected.ID, "completed": true}}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/data") {
					if strings.HasPrefix(mode, "import_source") {
						json.NewEncoder(w).Encode([]records.OfficialDocument{selected, target})
						return
					}
					json.NewEncoder(w).Encode([]records.OfficialDocument{selected})
					return
				}
				if r.URL.Path == "/api/v1/sessions" {
					json.NewEncoder(w).Encode(records.SessionPage{Sessions: []records.SessionSummary{row}, Total: 1, Limit: 100})
					return
				}
				if r.URL.Path == "/api/v1/sessions/source-session" {
					if mode == "session_disconnect_in_flight" {
						s.agentAccess.Lock()
						e := db.Disconnect(ctx, connection.ID)
						s.agentAccess.Unlock()
						if e != nil {
							t.Error(e)
						}
					}
					json.NewEncoder(w).Encode(records.SessionDetail{SessionSummary: row, QACount: 1, QAs: []records.SessionQA{{ID: "source-qa", Question: "synthetic source", Answer: "synthetic answer"}}})
					return
				}
				if strings.HasPrefix(mode, "import_source") && r.URL.Path == "/api/v1/datasets/"+dataset+"/data/"+target.ID+"/raw" {
					if mode == "import_source_disconnect_in_flight" {
						s.agentAccess.Lock()
						e := db.Disconnect(ctx, connection.ID)
						s.agentAccess.Unlock()
						if e != nil {
							t.Error(e)
						}
					}
					fmt.Fprint(w, targetText)
					return
				}
				if r.URL.Path != "/api/v1/datasets/"+dataset+"/data/"+selected.ID+"/raw" {
					t.Error("unexpected source path")
					w.WriteHeader(404)
					return
				}
				if mode == "disconnect_in_flight" {
					s.agentAccess.Lock()
					err := db.Disconnect(ctx, connection.ID)
					s.agentAccess.Unlock()
					if err != nil {
						t.Error(err)
					}
				}
				if strings.HasPrefix(mode, "import_source") {
					fmt.Fprint(w, parentText)
				} else if strings.HasPrefix(mode, "session") || strings.HasPrefix(mode, "knowledge") {
					fmt.Fprint(w, "# Session learning (session source-session)\n\nA synthetic lesson.\n")
				} else {
					fmt.Fprint(w, "合成官方正文；仅从官方读取。")
				}
			}))
			defer server.Close()
			binding := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: server.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, MaterialSubmission: strings.HasSuffix(mode, "submission_only")}
			if _, err = db.BindCognee(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err = s.writeCogneeToken(instance, dataset, "synthetic-token"); err != nil {
				t.Fatal(err)
			}
			q := records.OfficialDocumentQuery{Project: project.ID, Instance: instance, Dataset: dataset, Selected: &selected}
			if mode == "wrong_dataset" {
				q.Dataset = uuid.NewString()
			}
			if mode == "empty_project" {
				q.Project = ""
			}
			var out any
			if strings.HasPrefix(mode, "import_source") {
				q.SourceDocument = target.ID
				out, err = s.readOfficialDocumentMode(ctx, q, "import_source")
			} else if strings.HasPrefix(mode, "knowledge") {
				q.Limit = 20
				out, err = s.readOfficialDocumentMode(ctx, q, "knowledge")
			} else if strings.HasPrefix(mode, "session") {
				out, err = s.readOfficialDocumentMode(ctx, q, "session")
			} else {
				out, err = s.readOfficialDocument(ctx, q, true)
			}
			if mode == "import_source" {
				if err != nil || out.(records.OfficialDocumentSource).Content.Text != targetText {
					t.Fatal(out, err)
				}
			} else if mode == "read" {
				if err != nil || !strings.Contains(out.(records.OfficialDocumentText).Text, "合成官方正文") {
					t.Fatal(out, err)
				}
			} else if mode == "knowledge" {
				if err != nil || len(out.(records.OfficialKnowledgePage).Items) != 1 || out.(records.OfficialKnowledgePage).Items[0].ProcessingState != "completed" {
					t.Fatal(out, err)
				}
			} else if mode == "session" {
				if err != nil || out.(records.OfficialDocumentSession).Detail.Session != row.Session {
					t.Fatal(out, err)
				}
			} else if err == nil || out != nil {
				t.Fatal("unapproved official document escaped", out, err)
			}
			if (strings.HasSuffix(mode, "submission_only") || mode == "wrong_dataset" || mode == "empty_project") && reads != 0 {
				t.Fatal("unauthorized network work", reads)
			}
			for _, table := range []string{"memories", "versions", "jobs"} {
				var n int
				if e := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatal("document was mirrored", table, n, e)
				}
			}
		})
	}
}

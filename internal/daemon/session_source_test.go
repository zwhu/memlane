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

func TestOfficialSourceSessionVerifiesLineageTailAndOwner(t *testing.T) {
	for _, mode := range []string{"exact", "outside_tail", "ambiguous_owner", "owner_changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := filepath.Join(root, "repo")
			if out, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
				t.Fatal(e, string(out))
			}
			project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic lineage", Path: repo})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "synthetic source reader", Project: project.ID, AllowedScopes: []string{project.ID}})
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset, qaID, owner := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			const session = "codex_synthetic_source"
			const original = "Question: synthetic question\n\nAnswer: synthetic answer"
			draft := records.CogneeImport{ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, Project: project.ID, ExternalID: "cognee://" + dataset + "/sessions/" + session + "/qa/" + qaID, Origin: "raw", Title: "synthetic raw", Text: original, Fragment: original, Material: &records.CogneeOriginal{Instance: instance, Session: session, Kind: "qa", OfficialState: "persisted"}}
			raw, err := db.ImportCognee(ctx, draft)
			if err != nil {
				t.Fatal(err)
			}
			draft.ExternalID = "cognee://" + dataset + "/sessions/" + session + "/data/" + uuid.NewString()
			draft.Origin = "knowledge"
			draft.Title = "synthetic knowledge"
			draft.Text = "synthetic conclusion"
			draft.Fragment = ""
			draft.Material.Kind = "data"
			draft.Sources = []records.MergeSource{{Memory: raw.Record.Memory, Revision: 1, Quote: original}}
			knowledge, err := db.ImportCognee(ctx, draft)
			if err != nil {
				t.Fatal(err)
			}
			row := records.SessionSummary{Session: session, Dataset: dataset, User: owner}
			changed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/memlane/identity":
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
				case "/api/v1/memlane/datasets/" + dataset + "/sessions/" + session:
					json.NewEncoder(w).Encode(records.CogneeMaterial{Instance: instance, Dataset: dataset, Session: session, CogneeVersion: "1.6.7", Raw: []records.CogneeRaw{{ID: qaID, Kind: "qa", Text: original, Persisted: true}}})
				case "/api/v1/sessions":
					rows := []records.SessionSummary{row}
					if mode == "ambiguous_owner" {
						other := row
						other.User = uuid.NewString()
						rows = append(rows, other)
					}
					json.NewEncoder(w).Encode(records.SessionPage{Sessions: rows, Total: len(rows), Limit: 100})
				case "/api/v1/sessions/" + session:
					id := qaID
					if mode == "outside_tail" {
						id = uuid.NewString()
					}
					if mode == "owner_changed" && !changed {
						changed = true
						_, e := db.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: knowledge.Record.Memory, Dataset: project.ID, ExpectedRevision: 1, Title: "changed knowledge", Text: "changed conclusion"})
						if e != nil {
							t.Error(e)
						}
					}
					json.NewEncoder(w).Encode(records.SessionDetail{SessionSummary: row, QACount: 25, QAs: []records.SessionQA{{ID: id, Question: "synthetic question", Answer: "synthetic answer"}}})
				default:
					t.Error("unexpected path", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			binding := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: server.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo}
			if _, err = db.BindCognee(ctx, binding); err != nil {
				t.Fatal(err)
			}
			s := &Server{store: db, config: Config{Root: root}}
			if e := s.writeCogneeToken(instance, dataset, "synthetic"); e != nil {
				t.Fatal(e)
			}
			q := records.SourceRead{Project: project.ID, Owner: knowledge.Record.Memory, OwnerRevision: 1, Memory: raw.Record.Memory, Revision: 1}
			result, err := s.readOfficialSourceSession(ctx, q)
			if mode == "ambiguous_owner" || mode == "owner_changed" {
				if err == nil || len(result.Detail.QAs) > 0 {
					t.Fatal("unverified source exposed", mode)
				}
				return
			}
			if err != nil || result.TargetID != qaID || result.TargetLoaded != (mode == "exact") || result.Detail.QACount != 25 {
				t.Fatal("lineage/tail mapping wrong", result, err)
			}
			var versions int
			if e := db.DB.QueryRow("SELECT count(*) FROM versions").Scan(&versions); e != nil || versions != 2 {
				t.Fatal("source read duplicated stored versions", versions, e)
			}
		})
	}
}

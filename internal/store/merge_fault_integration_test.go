package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMigrationFailsClosed(t *testing.T) {
	for _, kind := range []string{"backup", "experiment", "future"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.sqlite")
			s, e := open(path, false)
			mustMerge(t, e)
			before := sqlRows(t, s.DB, "scopes")
			version := 2
			switch kind {
			case "backup":
				mustMerge(t, os.WriteFile(filepath.Join(filepath.Dir(path), "backups"), []byte("fault fixture"), 0600))
			case "experiment":
				_, e = s.DB.Exec(`CREATE TABLE p3_experiment_fixture(id TEXT)`)
				mustMerge(t, e)
			case "future":
				_, e = s.DB.Exec(`PRAGMA user_version=99`)
				mustMerge(t, e)
				version = 99
			}
			mustMerge(t, s.Close())
			_, e = Open(path)
			if e == nil {
				t.Fatal("unsafe migration accepted")
			}
			// Read-only check without opening through a migrator.
			db, e := sql.Open("sqlite", "file:"+path+"?mode=ro")
			mustMerge(t, e)
			defer db.Close()
			var v, n int
			mustMerge(t, db.QueryRow(`PRAGMA user_version`).Scan(&v))
			if v != version {
				t.Fatal("version changed after failure")
			}
			mustMerge(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'merge_%'`).Scan(&n))
			if n != 0 {
				t.Fatal("partial formal schema")
			}
			if len(before) != len(sqlRows(t, db, "scopes")) {
				t.Fatal("legacy scopes changed")
			}
		})
	}
}
func TestMergeInvalidResultsAndEditedFrozenInput(t *testing.T) {
	for _, kind := range []string{"extra_approval", "action", "attempt", "input_digest", "source", "quote", "empty", "source_edited"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := mergeDB(t)
			x, _, raw := mergeFixture(t, s)
			mustMerge(t, s.ClaimMerge(mergeCtx, x.ID, x.Attempt))
			var p map[string]any
			mustMerge(t, json.Unmarshal(raw, &p))
			switch kind {
			case "extra_approval":
				p["approved"] = true
			case "action":
				p["action"] = "delete"
			case "attempt":
				p["attempt"] = 2
			case "input_digest":
				p["input_digest"] = "forged"
			case "source":
				p["sources"].([]any)[1].(map[string]any)["revision"] = 2
			case "quote":
				p["sources"].([]any)[1].(map[string]any)["quote"] = "unsubmitted source"
			case "empty":
				p["text"] = " "
			case "source_edited":
				_, e := s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: "user-edit", Memory: x.Snapshot.Inputs[0].Memory, Dataset: x.Project, ExpectedRevision: 1, Text: "user changed while model running"})
				mustMerge(t, e)
			}
			b, _ := json.Marshal(p)
			task, e := s.CompleteMerge(mergeCtx, x.ID, x.Attempt, b, nil, "")
			if kind == "source_edited" {
				mustMerge(t, e)
				if task.ReviewState != "expired" {
					t.Fatal("stale result pending")
				}
				if task.Proposal.Text != p["text"] {
					t.Fatal("frozen proposal lost")
				}
			} else {
				if e == nil {
					t.Fatal("invalid result staged")
				}
				if len(sqlRows(t, s.DB, "merge_proposals")) != 0 {
					t.Fatal("invalid result durable")
				}
			}
			if len(sqlRows(t, s.DB, "memories")) != 2 || len(sqlRows(t, s.DB, "merge_receipts")) != 0 {
				t.Fatal("result changed knowledge")
			}
		})
	}
}

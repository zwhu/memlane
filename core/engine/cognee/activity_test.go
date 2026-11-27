package cognee

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

func seedOfficialActivityDB(t *testing.T, storePath string) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(storePath, "system", "databases", "cognee_db")
	requireNoError(t, os.MkdirAll(filepath.Dir(dbPath), 0o700))
	db, err := sql.Open("sqlite", dbPath)
	requireNoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE datasets (
			id TEXT PRIMARY KEY,
			name TEXT,
			created_at TEXT
		);
		CREATE TABLE pipeline_runs (
			id TEXT PRIMARY KEY,
			pipeline_run_id TEXT,
			parent_operation_id TEXT,
			dataset_id TEXT,
			pipeline_name TEXT,
			operation_name TEXT,
			status TEXT,
			outcome TEXT,
			background INTEGER,
			error_class TEXT,
			origin TEXT,
			created_at TEXT,
			started_at TEXT,
			ended_at TEXT
		);
	`)
	requireNoError(t, err)
	return db
}

func TestProjectCodeActivityAndPipelineRuns(t *testing.T) {
	storePath := t.TempDir()
	db := seedOfficialActivityDB(t, storePath)

	const (
		codeDS  = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		otherDS = "11111111-2222-4333-8444-555555555555"
	)
	_, err := db.Exec(`INSERT INTO datasets (id, name, created_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"aaaaaaaabbbb4ccc8dddeeeeeeeeeeee", "demo-code", "2026-11-27T10:00:00Z",
		otherDS, "other", "2026-11-27T09:00:00Z",
	)
	requireNoError(t, err)

	// Insert an older run predating dataset creation (must be ignored by generation filter),
	// a completed run after creation, and a run for otherDS (real Cognee schema has no kind column).
	_, err = db.Exec(`INSERT INTO pipeline_runs
		(id, pipeline_run_id, parent_operation_id, dataset_id, pipeline_name, operation_name, status, outcome, background, error_class, origin, created_at, started_at, ended_at)
		VALUES
		('old-row', 'old-run', '', ?, 'code_graph_pipeline', 'remember', 'DATASET_PROCESSING_STARTED', '', 0, '', 'api', '2026-11-27T09:00:00Z', '2026-11-27T09:00:00Z', ''),
		('done-row', 'done-run', '', ?, 'code_graph_pipeline', 'remember', 'DATASET_PROCESSING_COMPLETED', 'succeeded', 0, '', 'api', '2026-11-27T10:05:00Z', '2026-11-27T10:05:00Z', '2026-11-27T10:06:00Z'),
		('other-row', 'other-run', '', ?, 'cognify_pipeline', 'cognify', 'DATASET_PROCESSING_COMPLETED', 'succeeded', 1, '', 'api', '2026-11-27T10:10:00Z', '2026-11-27T10:10:00Z', '2026-11-27T10:11:00Z')
	`, "aaaaaaaabbbb4ccc8dddeeeeeeeeeeee", "aaaaaaaabbbb4ccc8dddeeeeeeeeeeee", otherDS)
	requireNoError(t, err)

	proj := NewProjector(storePath, time.UTC)

	// 1. ProjectCodeActivity ignores old-run predating generation and sees done-run.
	snap, err := proj.ProjectCodeActivity(t.Context(), codeDS)
	requireNoError(t, err)
	if snap.Active || snap.RunID != "done-run" || snap.Status != "DATASET_PROCESSING_COMPLETED" || snap.CodeDatasetID != codeDS {
		t.Fatalf("unexpected completed code activity: %+v", snap)
	}

	// 2. Insert an active operation run (empty pipeline_name -> derived kind="operation") -> Active=true, Status="running".
	_, err = db.Exec(`INSERT INTO pipeline_runs
		(id, pipeline_run_id, parent_operation_id, dataset_id, pipeline_name, operation_name, status, outcome, background, error_class, origin, created_at, started_at, ended_at)
		VALUES ('act-row', 'act-run', '', ?, '', 'remember', '', '', 0, '', 'api', '2026-11-27T10:15:00Z', '2026-11-27T10:15:00Z', '')`,
		"aaaaaaaabbbb4ccc8dddeeeeeeeeeeee")
	requireNoError(t, err)

	snap, err = proj.ProjectCodeActivity(t.Context(), codeDS)
	requireNoError(t, err)
	if !snap.Active || snap.RunID != "act-run" || snap.Status != "running" {
		t.Fatalf("unexpected active code activity: %+v", snap)
	}

	// 3. Missing dataset returns ErrNotFound.
	if _, err := proj.ProjectCodeActivity(t.Context(), "99999999-9999-4999-8999-999999999999"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("missing dataset err = %v, want ErrNotFound", err)
	}

	// 4. ListPipelineRuns derives kind ("pipeline" vs "operation") and supports filtering + pagination.
	byDS, err := proj.ListPipelineRuns(t.Context(), engine.PipelineRunsQuery{DatasetID: otherDS, Limit: 10})
	requireNoError(t, err)
	if len(byDS) != 1 || byDS[0].PipelineRunID != "other-run" || !byDS[0].Background || byDS[0].Kind != "pipeline" {
		t.Fatalf("ListPipelineRuns(otherDS) = %+v", byDS)
	}
	codeRuns, err := proj.ListPipelineRuns(t.Context(), engine.PipelineRunsQuery{DatasetID: codeDS, Limit: 10})
	requireNoError(t, err)
	if len(codeRuns) < 1 || codeRuns[0].Kind != "operation" {
		t.Fatalf("expected derived kind=operation for act-run, got %+v", codeRuns)
	}

	// 5. Unparseable dataset created_at fails closed.
	const badDS = "22222222-3333-4444-8555-666666666666"
	_, err = db.Exec(`INSERT INTO datasets (id, name, created_at) VALUES (?, ?, ?)`, badDS, "bad-ts", "not-a-timestamp")
	requireNoError(t, err)
	if _, err := proj.ProjectCodeActivity(t.Context(), badDS); err == nil {
		t.Fatal("expected fail-closed error for unparseable dataset created_at")
	}

	allAllowed, err := proj.ListPipelineRuns(t.Context(), engine.PipelineRunsQuery{AllowedDatasetIDs: []string{codeDS, otherDS}, Limit: 2, Offset: 1})
	requireNoError(t, err)
	if len(allAllowed) != 2 || allAllowed[0].PipelineRunID != "other-run" {
		t.Fatalf("ListPipelineRuns(paginated) = %+v", allAllowed)
	}

	emptyAllowed, err := proj.ListPipelineRuns(t.Context(), engine.PipelineRunsQuery{AllowedDatasetIDs: []string{}})
	requireNoError(t, err)
	if len(emptyAllowed) != 0 {
		t.Fatalf("expected empty runs for empty allowed set, got %+v", emptyAllowed)
	}
}

func TestAdapterEnsureDatasetAndIndexCode(t *testing.T) {
	a, _ := openTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiPrefix + "/datasets":
			writeTestResponse(t, w, `{"ok":true,"id":"ds-code-1","name":"proj-code","created_at":"2026-11-27T00:00:00Z"}`)
		case apiPrefix + "/code-index":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(80 * time.Millisecond):
			}
			writeTestResponse(t, w, `{"ok":true,"status":"completed","dataset_id":"ds-code-1","pipeline_run_id":"run-9","items_processed":4}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	a.client.http.Timeout = 40 * time.Millisecond
	a.client.extractHTTP.Timeout = 2 * time.Second

	if _, ok := engine.AsCodeIndexer(a); !ok {
		t.Fatal("adapter does not expose CodeIndexer")
	}
	if _, ok := engine.AsActivityReader(a); !ok {
		t.Fatal("adapter does not expose ActivityReader")
	}

	rec, err := a.EnsureDataset(context.Background(), "proj-code")
	requireNoError(t, err)
	if rec.ID != "ds-code-1" || rec.Name != "proj-code" {
		t.Fatalf("EnsureDataset = %+v", rec)
	}

	// Verify /code-index uses extractHTTP (80ms > 40ms http.Timeout).
	res, err := a.IndexCode(context.Background(), engine.CodeIndexRequest{DatasetID: "ds-code-1", RepoPath: "/tmp/repo"})
	requireNoError(t, err)
	if res.Status != "completed" || res.PipelineRunID != "run-9" || res.ItemsProcessed != 4 {
		t.Fatalf("IndexCode = %+v", res)
	}
}

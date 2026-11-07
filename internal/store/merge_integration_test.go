package store

// SQLite integration checks use actual schema v2 and disposable databases.
// Model results and index receipts in this file are explicitly fault/protocol
// fixtures. Real traex and Rust acceptance lives in daemon integration checks.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

var mergeCtx = context.Background()

func mustMerge(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func mergeDB(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.sqlite")
	s, e := Open(path)
	mustMerge(t, e)
	t.Cleanup(func() { s.Close() })
	return s, path
}
func mergeFixture(t *testing.T, s *Store) (records.MergeTask, records.MergeReview, []byte) {
	t.Helper()
	project := uuid.NewString()
	_, e := s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','Synthetic')`, project)
	mustMerge(t, e)
	d := records.MergeStart{OperationID: uuid.NewString(), Project: project}
	for _, text := range []string{"Synthetic project uses pnpm.", "Synthetic project commits pnpm-lock.yaml."} {
		id := uuid.NewString()
		_, e := s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Dataset: project, Memory: id, Text: text, Source: "synthetic-agent", SourceDetail: &records.AgentSource{Fragment: text, References: []string{"synthetic://source"}}})
		mustMerge(t, e)
		d.Inputs = append(d.Inputs, records.MergeTarget{Memory: id, Revision: 1})
	}
	task, created, e := s.StartMerge(mergeCtx, d)
	mustMerge(t, e)
	if !created {
		t.Fatal("new task not created")
	}
	repeated, created, e := s.StartMerge(mergeCtx, d)
	mustMerge(t, e)
	if created || repeated.ID != task.ID {
		t.Fatal("start replay")
	}
	changed := d
	changed.Inputs = append([]records.MergeTarget{}, d.Inputs...)
	changed.Inputs[1].Revision = 2
	_, _, e = s.StartMerge(mergeCtx, changed)
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("start conflict: %v", e)
	}
	p := records.MergeProposal{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: task.Attempt, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Action: "merge", Title: "Synthetic policy", Text: "Synthetic project uses pnpm and commits pnpm-lock.yaml."}
	for _, v := range task.Snapshot.Inputs {
		p.Sources = append(p.Sources, records.MergeSource{Memory: v.Memory, Revision: v.Revision, Quote: v.Text})
	}
	b, _ := json.Marshal(p)
	return task, records.MergeReview{Task: task.ID, Proposal: p.ID, Digest: records.MergeDigest(p), Receipt: uuid.NewString()}, b
}
func completeFixture(t *testing.T, s *Store, x records.MergeTask, b []byte) {
	t.Helper()
	mustMerge(t, s.ClaimMerge(mergeCtx, x.ID, x.Attempt))
	_, e := s.CompleteMerge(mergeCtx, x.ID, x.Attempt, b, map[string]bool{"protocol_fixture": true}, "")
	mustMerge(t, e)
}
func sqlRows(t *testing.T, db *sql.DB, table string) [][]any {
	t.Helper()
	r, e := db.Query("SELECT * FROM " + table + " ORDER BY 1,2")
	mustMerge(t, e)
	defer r.Close()
	cols, e := r.Columns()
	mustMerge(t, e)
	var out [][]any
	for r.Next() {
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range values {
			ptr[i] = &values[i]
		}
		mustMerge(t, r.Scan(ptr...))
		out = append(out, values)
	}
	mustMerge(t, r.Err())
	return out
}
func TestMergeV2MigrationBackupRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.sqlite")
	s, e := open(path, false)
	mustMerge(t, e)
	project, id := uuid.NewString(), uuid.NewString()
	_, e = s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','Synthetic')`, project)
	mustMerge(t, e)
	v, e := s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: "legacy-op", Memory: id, Dataset: project, Text: "legacy body", SourceDetail: &records.AgentSource{Fragment: "actual submitted fixture", References: []string{"synthetic://legacy"}}})
	mustMerge(t, e)
	mustMerge(t, s.MarkIndexed(mergeCtx, v.Record, engine.Receipt{Generation: "protocol-fixture", ChunkIDs: []string{uuid.NewString()}}))
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: "legacy-edit", Memory: id, Dataset: project, ExpectedRevision: 1, Text: "current legacy body"})
	mustMerge(t, e)
	_, e = s.ConfigureConnection(mergeCtx, records.ConnectionDraft{Agent: "codex", Name: "fixture connection", Project: project, AllowedScopes: []string{project}})
	mustMerge(t, e)
	_, e = s.DB.Exec(`INSERT INTO repositories(common_dir,scope) VALUES('/synthetic/repo',?)`, project)
	mustMerge(t, e)
	tables := []string{"memories", "versions", "operations", "jobs", "index_bindings", "scopes", "memory_metadata", "activities", "connections", "repositories"}
	before := map[string][][]any{}
	for _, table := range tables {
		before[table] = sqlRows(t, s.DB, table)
	}
	// Force committed data to remain in WAL. VACUUM INTO must include it.
	_, e = s.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	for _, table := range tables {
		if len(before[table]) == 0 {
			t.Fatalf("fixture missing %s", table)
		}
	}
	// Keep a v2 reader open so the legacy owner's close cannot checkpoint away
	// the committed WAL. This auxiliary handle is only a migration fixture.
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	readTx, e := reader.Begin()
	mustMerge(t, e)
	var hold int
	mustMerge(t, readTx.QueryRow(`SELECT count(*) FROM versions`).Scan(&hold))
	mustMerge(t, s.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("WAL fixture absent")
	}
	s, e = Open(path)
	mustMerge(t, readTx.Rollback())
	mustMerge(t, reader.Close())
	mustMerge(t, e)
	if _, e = Open(path); e == nil {
		t.Fatal("second database owner allowed")
	}
	var version int
	mustMerge(t, s.DB.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 3 {
		t.Fatal(version)
	}
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], sqlRows(t, s.DB, table)) {
			t.Fatalf("migration changed %s", table)
		}
	}
	var experiment int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'p3_experiment_%'`).Scan(&experiment))
	if experiment != 0 {
		t.Fatal("formal migration created experiment")
	}
	mustMerge(t, s.Close())
	s, e = Open(path)
	mustMerge(t, e)
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], sqlRows(t, s.DB, table)) {
			t.Fatalf("reopen changed %s", table)
		}
	}
	mustMerge(t, s.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	info, e := os.Stat(backups[0])
	mustMerge(t, e)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup permissions")
	}
	restored, e := open(backups[0], false)
	mustMerge(t, e)
	defer restored.Close()
	mustMerge(t, restored.DB.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 2 {
		t.Fatal("backup not v2")
	}
	var integrity string
	mustMerge(t, restored.DB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], sqlRows(t, restored.DB, table)) {
			t.Fatalf("backup lost %s", table)
		}
	}
	_, e = restored.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: "legacy-edit", Memory: id, Dataset: project, ExpectedRevision: 1, Text: "current legacy body"})
	mustMerge(t, e)
}
func TestMergeAtomicReviewRestoreAndConflicts(t *testing.T) {
	s, _ := mergeDB(t)
	x, r, b := mergeFixture(t, s)
	completeFixture(t, s, x, b)
	if len(sqlRows(t, s.DB, "versions")) != 2 {
		t.Fatal("proposal changed knowledge")
	}
	_, e := s.CompleteMerge(mergeCtx, x.ID, x.Attempt, b, nil, "")
	mustMerge(t, e)
	var changed records.MergeProposal
	mustMerge(t, json.Unmarshal(b, &changed))
	changed.Text += " changed"
	altered, _ := json.Marshal(changed)
	_, e = s.CompleteMerge(mergeCtx, x.ID, x.Attempt, altered, nil, "")
	if !errors.Is(e, ErrConflict) {
		t.Fatal("result ID rebound", e)
	}
	task, e := s.ReviewMerge(mergeCtx, r, true)
	mustMerge(t, e)
	if task.Receipt == nil || len(sqlRows(t, s.DB, "versions")) != 5 || len(sqlRows(t, s.DB, "merge_sources")) != 2 {
		t.Fatal("incomplete merge")
	}
	for _, src := range x.Snapshot.Inputs {
		v, e := s.Current(mergeCtx, src.Memory)
		mustMerge(t, e)
		if v.State != "archived" || v.Revision != 2 || v.Text != src.Text || v.SourceDetail.Fragment != src.SourceDetail.Fragment {
			t.Fatal("archive lost body/source")
		}
		_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: x.Project, Memory: v.Memory, ExpectedRevision: 2, Text: "invalid unarchive"})
		if !errors.Is(e, ErrConflict) {
			t.Fatal("archived edit allowed")
		}
	}
	repeated, e := s.ReviewMerge(mergeCtx, r, true)
	mustMerge(t, e)
	if *repeated.Receipt != *task.Receipt {
		t.Fatal("receipt replay")
	}
	other := r
	other.Receipt = uuid.NewString()
	repeated, e = s.ReviewMerge(mergeCtx, other, true)
	mustMerge(t, e)
	if *repeated.Receipt != *task.Receipt {
		t.Fatal("proposal replay")
	}
	other = r
	other.Digest = "changed"
	_, e = s.ReviewMerge(mergeCtx, other, true)
	if !errors.Is(e, ErrConflict) {
		t.Fatal("receipt digest rebound")
	}
	x2, r2, b2 := mergeFixture(t, s)
	completeFixture(t, s, x2, b2)
	r2.Receipt = r.Receipt
	_, e = s.ReviewMerge(mergeCtx, r2, true)
	if !errors.Is(e, ErrConflict) {
		t.Fatal("receipt proposal rebound")
	}
	_, e = s.CancelMerge(mergeCtx, x.ID)
	mustMerge(t, e)
	repeated, e = s.ReviewMerge(mergeCtx, r, true)
	mustMerge(t, e)
	if repeated.Receipt == nil {
		t.Fatal("cancel undid commit")
	}
	src := x.Snapshot.Inputs[0]
	restored, e := s.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Dataset: x.Project, Memory: src.Memory, ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if restored.Revision != 3 || restored.State != "active" || restored.Text != src.Text {
		t.Fatal("restore not new revision")
	}
}
func TestMergeExpiredRejectRollbackAndAttempts(t *testing.T) {
	for _, i := range []int{0, 1} {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			s, _ := mergeDB(t)
			x, r, b := mergeFixture(t, s)
			completeFixture(t, s, x, b)
			_, e := s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: x.Project, Memory: x.Snapshot.Inputs[i].Memory, ExpectedRevision: 1, Text: "user edit"})
			mustMerge(t, e)
			_, e = s.ReviewMerge(mergeCtx, r, true)
			var a *records.AccessError
			if !errors.As(e, &a) || a.Code != "proposal_expired" {
				t.Fatal(e)
			}
			other, e := s.Current(mergeCtx, x.Snapshot.Inputs[1-i].Memory)
			mustMerge(t, e)
			if other.State != "active" || other.Revision != 1 || len(sqlRows(t, s.DB, "memories")) != 2 {
				t.Fatal("partial archive")
			}
			status, e := s.MergeTask(mergeCtx, x.ID)
			mustMerge(t, e)
			if status.ReviewState != "expired" {
				t.Fatal(status)
			}
		})
	}
	t.Run("reject", func(t *testing.T) {
		s, _ := mergeDB(t)
		x, r, b := mergeFixture(t, s)
		completeFixture(t, s, x, b)
		_, e := s.ReviewMerge(mergeCtx, r, false)
		mustMerge(t, e)
		_, e = s.ReviewMerge(mergeCtx, r, false)
		mustMerge(t, e)
		_, e = s.ReviewMerge(mergeCtx, r, true)
		if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
		if len(sqlRows(t, s.DB, "versions")) != 2 {
			t.Fatal("reject changed knowledge")
		}
	})
	t.Run("rollback", func(t *testing.T) {
		s, _ := mergeDB(t)
		x, r, b := mergeFixture(t, s)
		completeFixture(t, s, x, b)
		tables := []string{"memories", "versions", "memory_metadata", "activities", "operations", "jobs", "merge_sources", "merge_receipts", "merge_proposals"}
		before := map[string][][]any{}
		for _, table := range tables {
			before[table] = sqlRows(t, s.DB, table)
		}
		_, e := s.DB.Exec(`CREATE TRIGGER merge_fault BEFORE INSERT ON merge_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`)
		mustMerge(t, e)
		_, e = s.ReviewMerge(mergeCtx, r, true)
		if e == nil {
			t.Fatal("fault ignored")
		}
		for _, table := range tables {
			if !reflect.DeepEqual(before[table], sqlRows(t, s.DB, table)) {
				t.Fatalf("partial %s", table)
			}
		}
		_, e = s.DB.Exec(`DROP TRIGGER merge_fault`)
		mustMerge(t, e)
		_, e = s.ReviewMerge(mergeCtx, r, true)
		mustMerge(t, e)
	})
	t.Run("restart-and-cancel", func(t *testing.T) {
		s, path := mergeDB(t)
		x, _, b := mergeFixture(t, s)
		mustMerge(t, s.ClaimMerge(mergeCtx, x.ID, x.Attempt))
		mustMerge(t, s.Close())
		fresh, e := Open(path)
		mustMerge(t, e)
		defer fresh.Close()
		mustMerge(t, fresh.InterruptMerges(mergeCtx))
		status, e := fresh.MergeTask(mergeCtx, x.ID)
		mustMerge(t, e)
		if status.State != "interrupted" || status.Attempt != 2 {
			t.Fatal(status)
		}
		_, e = fresh.CompleteMerge(mergeCtx, x.ID, 1, b, nil, "")
		if e == nil {
			t.Fatal("old attempt accepted")
		}
		x2, r2, b2 := mergeFixture(t, fresh)
		completeFixture(t, fresh, x2, b2)
		_, e = fresh.CancelMerge(mergeCtx, x2.ID)
		mustMerge(t, e)
		_, e = fresh.ReviewMerge(mergeCtx, r2, true)
		if e == nil {
			t.Fatal("cancel applied")
		}
		_, e = fresh.CompleteMerge(mergeCtx, x2.ID, 1, b2, nil, "")
		if e == nil {
			t.Fatal("late result accepted")
		}
	})
}
func TestMergeCancelApplyCompetition(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, _ := mergeDB(t)
		x, r, b := mergeFixture(t, s)
		completeFixture(t, s, x, b)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = s.CancelMerge(mergeCtx, x.ID) }()
		go func() { defer wg.Done(); _, _ = s.ReviewMerge(mergeCtx, r, true) }()
		wg.Wait()
		status, e := s.MergeTask(mergeCtx, x.ID)
		mustMerge(t, e)
		if status.ReviewState == "applied" {
			if status.Receipt == nil || len(sqlRows(t, s.DB, "versions")) != 5 {
				t.Fatal("lost committed fact")
			}
		} else if status.ReviewState == "cancelled" {
			if status.Receipt != nil || len(sqlRows(t, s.DB, "versions")) != 2 {
				t.Fatal("cancel partial commit")
			}
		} else {
			t.Fatal(status.ReviewState)
		}
	}
}

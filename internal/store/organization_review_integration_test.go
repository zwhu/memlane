package store

// Model results are protocol fixtures; SQLite, version gates, decisions and
// rollback use the real implementation. App/Agent/Rust acceptance is separate.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func reviewOrganizationFixture(t *testing.T, s *Store, action string) (records.OrganizationTask, records.OrganizationReview) {
	t.Helper()
	project, destination := uuid.NewString(), uuid.NewString()
	_, e := s.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','source','/synthetic/source'),(?,'project','destination','/synthetic/destination')`, project, destination)
	mustMerge(t, e)
	d := records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Review: true, Destinations: []string{destination}}
	for _, body := range []string{"项目使用 pnpm 管理依赖。", "修改依赖必须提交 pnpm-lock.yaml。"} {
		id := uuid.NewString()
		_, e = s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: id, Dataset: project, Text: body})
		mustMerge(t, e)
		d.Inputs = append(d.Inputs, records.MergeTarget{Memory: id, Revision: 1})
	}
	task, _, e := s.StartOrganization(mergeCtx, d)
	mustMerge(t, e)
	item := records.OrganizationItem{Index: 1, Action: action, Reason: "具体材料支持的合成建议", Tags: []string{}}
	for _, v := range task.Snapshot.Inputs {
		item.Sources = append(item.Sources, records.MergeSource{Memory: v.Memory, Revision: v.Revision, Quote: v.Text})
	}
	switch action {
	case "merge", "overwrite":
		item.Title = "新约定"
		item.Text = "项目用 pnpm 并提交锁文件。"
	}
	switch action {
	case "overwrite", "delete", "move":
		item.Target = d.Inputs[0].Memory
	}
	if action == "move" {
		item.Destination = destination
	}
	r := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: 1, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Items: []records.OrganizationItem{item}, Explanation: "审核协议夹具"}
	raw, _ := json.Marshal(r)
	stageOrganizationFixture(t, s, task, raw)
	task, e = s.FinishOrganization(mergeCtx, task.ID, 1)
	mustMerge(t, e)
	if task.State != "completed" || task.Entries[0].State != "review_pending" || organizationCount(t, s, `SELECT count(*) FROM memories`) != 2 {
		t.Fatal("proposal changed knowledge", task)
	}
	return task, records.OrganizationReview{Task: task.ID, Item: task.Entries[0].ID, Digest: records.MergeDigest(item), Receipt: uuid.NewString(), Confirm: true}
}

func TestOrganizationReviewActionsAndReplay(t *testing.T) {
	for _, action := range []string{"merge", "overwrite", "delete", "move", "duplicate", "conflict"} {
		t.Run(action, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, d := reviewOrganizationFixture(t, s, action)
			done, e := s.ReviewOrganization(mergeCtx, d)
			mustMerge(t, e)
			if done.Entries[0].State != "applied" || done.Entries[0].Receipt == nil {
				t.Fatal(done)
			}
			before := sqlRows(t, s.DB, "versions")
			replay, e := s.ReviewOrganization(mergeCtx, d)
			mustMerge(t, e)
			if !reflect.DeepEqual(done.Entries[0].Receipt, replay.Entries[0].Receipt) || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
				t.Fatal("replay changed fact")
			}
			d.Confirm = false
			_, e = s.ReviewOrganization(mergeCtx, d)
			if !errors.Is(e, ErrConflict) {
				t.Fatal("changed decision accepted", e)
			}
			receipt := done.Entries[0].Receipt
			switch action {
			case "merge":
				if organizationCount(t, s, `SELECT count(*) FROM memories WHERE state='archived'`) != 2 || organizationCount(t, s, `SELECT count(*) FROM memories`) != 3 {
					t.Fatal("merge not atomic")
				}
			case "overwrite":
				v, e := s.Current(mergeCtx, receipt.Memory)
				mustMerge(t, e)
				if v.Revision != 2 || v.Text != task.Entries[0].Item.Text {
					t.Fatal(v)
				}
			case "delete":
				v, e := s.Current(mergeCtx, receipt.Memory)
				mustMerge(t, e)
				if v.Revision != 2 || v.State != "deleted" {
					t.Fatal(v)
				}
			case "move":
				v, e := s.Current(mergeCtx, receipt.Memory)
				mustMerge(t, e)
				if v.Dataset != task.Snapshot.Destinations[0].ID || v.Revision != 2 || v.Operation != "move" {
					t.Fatal(v)
				}
				if organizationCount(t, s, `SELECT count(*) FROM scope_history`) != 1 {
					t.Fatal("scope association missing")
				}
			default:
				if len(before) != 2 || receipt.Memory != "" {
					t.Fatal("observation mutated knowledge")
				}
			}
		})
	}
}
func TestOrganizationReviewRejectExpiryAndRollback(t *testing.T) {
	for _, mode := range []string{"reject", "source_expiry", "destination_expiry", "sql_rollback", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, d := reviewOrganizationFixture(t, s, "move")
			before := sqlRows(t, s.DB, "versions")
			switch mode {
			case "reject":
				d.Confirm = false
				done, e := s.ReviewOrganization(mergeCtx, d)
				mustMerge(t, e)
				if done.Entries[0].State != "rejected" {
					t.Fatal(done)
				}
				_, e = s.ReviewOrganization(mergeCtx, d)
				mustMerge(t, e)
			case "source_expiry":
				source := task.Snapshot.Inputs[0]
				_, e := s.Apply(mergeCtx, Mutation{Kind: "edit", Memory: source.Memory, Dataset: task.Project, OperationID: uuid.NewString(), ExpectedRevision: 1, Text: "修改后的依据"})
				mustMerge(t, e)
				before = sqlRows(t, s.DB, "versions")
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("source expiry accepted")
				}
			case "destination_expiry":
				_, e := s.DB.Exec(`UPDATE scopes SET path='/synthetic/changed' WHERE id=?`, task.Snapshot.Destinations[0].ID)
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("destination expiry accepted")
				}
			case "sql_rollback":
				_, e := s.DB.Exec(`CREATE TRIGGER scope_fail BEFORE INSERT ON scope_history BEGIN SELECT RAISE(ABORT,'SQL scope history fixture'); END`)
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("SQL fixture did not fail")
				}
				if !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) || organizationCount(t, s, `SELECT count(*) FROM organization_decisions`) != 0 {
					t.Fatal("partial review commit")
				}
				_, e = s.DB.Exec(`DROP TRIGGER scope_fail`)
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				mustMerge(t, e)
				return
			case "cancel":
				_, e := s.CancelOrganization(mergeCtx, task.ID)
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("cancelled review accepted")
				}
			}
			if !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
				t.Fatal("non-applied review mutated knowledge")
			}
		})
	}
}

func TestOrganizationV4MigrationPreservesCommittedWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v4.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	mustMerge(t, migrateMerge(tx))
	mustMerge(t, migrateOrganization(tx))
	mustMerge(t, tx.Commit())
	legacy.Schema = 4
	task, _, raw := organizationFixture(t, legacy)
	stageOrganizationFixture(t, legacy, task, raw)
	_, e = legacy.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	_, e = legacy.ApplyOrganizationItem(mergeCtx, task.ID, 1, 2)
	mustMerge(t, e)
	_, e = legacy.FinishOrganization(mergeCtx, task.ID, 1)
	mustMerge(t, e)
	rows, e := legacy.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, e)
	var tables []string
	for rows.Next() {
		var name string
		mustMerge(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	mustMerge(t, rows.Err())
	rows.Close()
	before := map[string][][]any{}
	for _, name := range tables {
		before[name] = sqlRows(t, legacy.DB, name)
	}
	_, e = legacy.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	hold, e := reader.Begin()
	mustMerge(t, e)
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&count))
	mustMerge(t, legacy.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("committed WAL absent")
	}
	current, e := Open(path)
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	mustMerge(t, e)
	defer current.Close()
	if current.Schema != SchemaVersion {
		t.Fatal(current.Schema)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) {
			t.Fatal("legacy changed", name)
		}
	}
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v4-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 4 {
		t.Fatal(version)
	}
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("backup changed", name)
		}
	}
	restored := &Store{DB: backup, Schema: 4}
	saved, e := restored.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if saved.Entries[0].Receipt == nil {
		t.Fatal("v4 receipt lost")
	}
	v, e := restored.Current(mergeCtx, saved.Entries[0].Receipt.Memory)
	mustMerge(t, e)
	_, e = restored.Apply(mergeCtx, Mutation{Kind: "edit", Memory: v.Memory, Dataset: v.Dataset, OperationID: uuid.NewString(), ExpectedRevision: v.Revision, Text: "v4 user edit"})
	mustMerge(t, e)
	v, e = restored.Apply(mergeCtx, Mutation{Kind: "restore", Memory: v.Memory, Dataset: v.Dataset, OperationID: uuid.NewString(), ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if v.Revision != 3 {
		t.Fatal("v4 restore failed")
	}
}

func TestOrganizationReviewSummaryRepeatedOverwriteLineage(t *testing.T) {
	s, _ := mergeDB(t)
	base, _, raw := organizationFixture(t, s)
	stageOrganizationFixture(t, s, base, raw)
	summary, e := s.ApplyOrganizationItem(mergeCtx, base.ID, 1, 1)
	mustMerge(t, e)
	target := summary.Receipt.Memory
	for _, rev := range []int64{1, 2} {
		current, e := s.Current(mergeCtx, target)
		mustMerge(t, e)
		d := records.OrganizationStart{OperationID: uuid.NewString(), Project: base.Project, Review: true, Inputs: []records.MergeTarget{{Memory: target, Revision: rev}, {Memory: base.Snapshot.Inputs[0].Memory, Revision: 1}}}
		task, _, e := s.StartOrganization(mergeCtx, d)
		mustMerge(t, e)
		item := records.OrganizationItem{Index: 1, Action: "overwrite", Target: target, Title: "确认新摘要", Text: "用户审核的新约定", Tags: []string{}, Reason: "测试摘要与第二次覆盖的冻结来源"}
		for _, input := range task.Snapshot.Inputs {
			item.Sources = append(item.Sources, records.MergeSource{Memory: input.Memory, Revision: input.Revision, Quote: input.Text})
		}
		result := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: 1, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Items: []records.OrganizationItem{item}, Explanation: "协议夹具"}
		b, _ := json.Marshal(result)
		stageOrganizationFixture(t, s, task, b)
		_, e = s.FinishOrganization(mergeCtx, task.ID, 1)
		mustMerge(t, e)
		_, e = s.ReviewOrganization(mergeCtx, records.OrganizationReview{Task: task.ID, Item: task.Snapshot.ItemID(1), Digest: records.MergeDigest(item), Receipt: uuid.NewString(), Confirm: true})
		mustMerge(t, e)
		next, e := s.Current(mergeCtx, target)
		mustMerge(t, e)
		if next.Revision != rev+1 || next.Derived.State != "user_confirmed" {
			t.Fatal(next)
		}
		history, e := s.OrganizationSources(mergeCtx, target, rev+1)
		mustMerge(t, e)
		if len(history) != 2 {
			t.Fatal("approved sources not exact", history)
		}
		old, e := s.OrganizationSources(mergeCtx, target, rev)
		mustMerge(t, e)
		if len(old) != 2 {
			t.Fatal("old lineage lost", old)
		}
		if current.Revision != rev {
			t.Fatal(current)
		}
	}
}

func TestOrganizationMovedProvenanceScopeFilter(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := reviewOrganizationFixture(t, s, "move")
	target := task.Entries[0].Item.Target
	_, e := s.DB.Exec(`UPDATE memory_metadata SET source_detail='{"fragment":"oldScopePrivateMarker","references":["synthetic://private/original"]}' WHERE memory=?`, target)
	mustMerge(t, e)
	done, e := s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	id := done.Entries[0].Receipt.Memory
	v, e := s.Current(mergeCtx, id)
	mustMerge(t, e)
	if v.SourceDetail == nil || v.Original == "" {
		t.Fatal("trusted history lost")
	}
	public := v
	mustMerge(t, s.FilterAgentVersion(mergeCtx, &public, []string{v.Dataset}))
	if public.Original != "" || public.SourceDetail != nil || public.Text != v.Text {
		t.Fatal("old scope leaked or current body lost", public)
	}
	allowed := v
	mustMerge(t, s.FilterAgentVersion(mergeCtx, &allowed, []string{v.Dataset, task.Project}))
	if allowed.Original != v.Original || allowed.SourceDetail == nil {
		t.Fatal("explicit old-scope access lost")
	}
	start, _, e := s.StartOrganization(mergeCtx, records.OrganizationStart{OperationID: uuid.NewString(), Project: v.Dataset, Review: true, Inputs: []records.MergeTarget{{Memory: v.Memory, Revision: v.Revision}}})
	mustMerge(t, e)
	if start.Snapshot.Inputs[0].SourceDetail != nil {
		t.Fatal("organizer prompt leaked old scope")
	}
	other := uuid.NewString()
	_, e = s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: other, Dataset: v.Dataset, Text: "目标项目另一条安全约定"})
	mustMerge(t, e)
	merged, _, e := s.StartMerge(mergeCtx, records.MergeStart{OperationID: uuid.NewString(), Project: v.Dataset, Inputs: []records.MergeTarget{{Memory: v.Memory, Revision: v.Revision}, {Memory: other, Revision: 1}}})
	mustMerge(t, e)
	if merged.Snapshot.Inputs[0].SourceDetail != nil {
		t.Fatal("legacy merge prompt leaked old scope")
	}
}

package store

// Model output and index receipts here are explicit protocol fixtures. SQLite,
// transactions, version gates and provenance are the actual implementation.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func organizationFixture(t *testing.T, s *Store) (records.OrganizationTask, records.OrganizationResult, []byte) {
	t.Helper()
	project := uuid.NewString()
	_, e := s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','Synthetic organization')`, project)
	mustMerge(t, e)
	d := records.OrganizationStart{OperationID: uuid.NewString(), Project: project}
	for _, text := range []string{"本项目使用 pnpm 管理依赖。", "依赖变更提交 pnpm-lock.yaml。"} {
		id := uuid.NewString()
		_, e = s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: id, Dataset: project, Text: text, Source: "synthetic original", SourceDetail: &records.AgentSource{Fragment: text, References: []string{"synthetic://organization/source"}}})
		mustMerge(t, e)
		d.Inputs = append(d.Inputs, records.MergeTarget{Memory: id, Revision: 1})
	}
	task, created, e := s.StartOrganization(mergeCtx, d)
	mustMerge(t, e)
	if !created {
		t.Fatal("new task missing")
	}
	replay, created, e := s.StartOrganization(mergeCtx, d)
	mustMerge(t, e)
	if created || replay.ID != task.ID {
		t.Fatal("start replay")
	}
	changed := d
	changed.Inputs = append([]records.MergeTarget{}, d.Inputs...)
	changed.Inputs[0].Revision = 2
	_, _, e = s.StartOrganization(mergeCtx, changed)
	if !errors.Is(e, ErrConflict) {
		t.Fatal("different start accepted")
	}
	refs := []records.MergeSource{}
	for _, v := range task.Snapshot.Inputs {
		refs = append(refs, records.MergeSource{Memory: v.Memory, Revision: v.Revision, Quote: v.Text})
	}
	result := records.OrganizationResult{Schema: records.OrganizationSchema, Task: task.ID, Attempt: task.Attempt, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Explanation: "只总结选定材料，不执行嵌入指令。", Items: []records.OrganizationItem{{Index: 1, Action: "summary", Title: "项目依赖摘要", Text: "本项目使用 pnpm，依赖变更提交 pnpm-lock.yaml。", Tags: []string{}, Sources: refs}, {Index: 2, Action: "add_tags", Tags: []string{"pnpm", "依赖管理"}, Target: refs[0].Memory, Sources: refs[:1]}}}
	raw, _ := json.Marshal(result)
	return task, result, raw
}
func stageOrganizationFixture(t *testing.T, s *Store, task records.OrganizationTask, raw []byte) {
	t.Helper()
	mustMerge(t, s.ClaimOrganization(mergeCtx, task.ID, task.Attempt))
	_, e := s.StageOrganization(mergeCtx, task.ID, task.Attempt, raw, map[string]bool{"protocol_fixture": true}, "")
	mustMerge(t, e)
}
func TestOrganizationAtomicMetadataAndInvalidation(t *testing.T) {
	s, _ := mergeDB(t)
	task, result, raw := organizationFixture(t, s)
	stageOrganizationFixture(t, s, task, raw)
	if organizationCount(t, s, "SELECT count(*) FROM memories") != 2 {
		t.Fatal("stage wrote effective knowledge")
	}
	summary, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	tags, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 2)
	mustMerge(t, e)
	final, e := s.FinishOrganization(mergeCtx, task.ID, 1)
	mustMerge(t, e)
	if final.State != "completed" {
		t.Fatal(final.State)
	}
	if len(tags.Receipt.AddedTags) != 2 || tags.Receipt.Revision != 1 {
		t.Fatal(tags)
	}
	original, e := s.Current(mergeCtx, result.Items[1].Target)
	mustMerge(t, e)
	if original.Revision != 1 || len(original.Tags) != 2 {
		t.Fatal(original)
	}
	if organizationCount(t, s, "SELECT count(*) FROM versions") != 3 || organizationCount(t, s, "SELECT count(*) FROM tag_events") != 2 {
		t.Fatal("tags rewrote versions")
	}
	if organizationCount(t, s, "SELECT count(*) FROM activities") != 0 {
		t.Fatal("tags fabricated activity")
	}
	v, e := s.Current(mergeCtx, summary.Receipt.Memory)
	mustMerge(t, e)
	if v.Derived == nil || v.Derived.State != "valid" {
		t.Fatal(v)
	}
	sources, e := s.OrganizationSources(mergeCtx, v.Memory, 1)
	mustMerge(t, e)
	if len(sources) != 2 || sources[0].Input.Roots[0].Memory == v.Memory {
		t.Fatal(sources)
	}
	chunk := uuid.NewString()
	mustMerge(t, s.MarkIndexed(mergeCtx, v.Record, engine.Receipt{Generation: "explicit-index-fixture", ChunkIDs: []string{chunk}}))
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: task.Snapshot.Inputs[0].Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "本项目改用其他明确约定。"})
	mustMerge(t, e)
	v, e = s.Current(mergeCtx, v.Memory)
	mustMerge(t, e)
	if v.Derived.State != "stale" || v.IndexState == "indexed" {
		t.Fatal(v)
	}
	candidate := engine.Candidate{ChunkID: chunk, Dataset: v.Dataset, Memory: v.Memory, Revision: 1, Generation: "explicit-index-fixture", Text: v.Text, StartByte: 0, EndByte: len(v.Text)}
	_, valid, e := s.Resolve(mergeCtx, candidate)
	mustMerge(t, e)
	if valid {
		t.Fatal("stale summary returned")
	}
	e = s.MarkIndexed(mergeCtx, v.Record, engine.Receipt{Generation: "late-index-fixture", ChunkIDs: []string{uuid.NewString()}})
	if !errors.Is(e, ErrConflict) {
		t.Fatal("late index reactivated summary")
	}
	page, e := s.List(mergeCtx, records.ListQuery{Datasets: []string{task.Project}})
	mustMerge(t, e)
	for _, item := range page.Items {
		if item.Memory == v.Memory {
			t.Fatal("stale summary default listing")
		}
	}
	replay, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	if !reflect.DeepEqual(replay.Receipt, summary.Receipt) {
		t.Fatal("receipt replay")
	}
	// User edit is an explicit confirmation, preserves lineage and makes v2 current.
	v, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 1, Text: "用户核对后确认的当前项目约定。"})
	mustMerge(t, e)
	if v.Revision != 2 || v.Derived.State != "user_confirmed" {
		t.Fatal(v)
	}
	sources, e = s.OrganizationSources(mergeCtx, v.Memory, 2)
	mustMerge(t, e)
	if len(sources) != 2 {
		t.Fatal("lost historical lineage")
	}
	if organizationCount(t, s, "SELECT count(*) FROM activities") != 1 {
		t.Fatal("edit fabricated activity")
	}
}
func TestOrganizationPartialRollbackResumeAndCancel(t *testing.T) {
	s, _ := mergeDB(t)
	task, _, raw := organizationFixture(t, s)
	stageOrganizationFixture(t, s, task, raw)
	first, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	_, e = s.DB.Exec(`CREATE TRIGGER org_receipt_fault BEFORE UPDATE OF receipt ON organization_items WHEN NEW.position=2 BEGIN SELECT RAISE(ABORT,'explicit SQL fault'); END`)
	mustMerge(t, e)
	_, e = s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 2)
	if e == nil {
		t.Fatal("SQL fault not reached")
	}
	if organizationCount(t, s, "SELECT count(*) FROM memory_tags") != 0 || organizationCount(t, s, "SELECT count(*) FROM tag_events") != 0 || organizationCount(t, s, "SELECT count(*) FROM memories") != 3 {
		t.Fatal("partial item rollback failed")
	}
	mustMerge(t, s.FailOrganizationItem(mergeCtx, task.ID, 1, 2))
	final, e := s.FinishOrganization(mergeCtx, task.ID, 1)
	mustMerge(t, e)
	if final.State != "partial_failed" {
		t.Fatal(final.State)
	}
	_, e = s.DB.Exec(`DROP TRIGGER org_receipt_fault`)
	mustMerge(t, e)
	resume := records.OrganizationResume{Task: task.ID, OperationID: uuid.NewString(), ExpectedAttempt: 1}
	next, created, e := s.ResumeOrganization(mergeCtx, resume)
	mustMerge(t, e)
	if !created || next.Attempt != 2 {
		t.Fatal(next)
	}
	replay, created, e := s.ResumeOrganization(mergeCtx, resume)
	mustMerge(t, e)
	if created || replay.Attempt != 2 {
		t.Fatal("resume replay")
	}
	same, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 2, 1)
	mustMerge(t, e)
	if !reflect.DeepEqual(same.Receipt, first.Receipt) {
		t.Fatal("resume repeated summary")
	}
	_, e = s.ApplyOrganizationItem(mergeCtx, task.ID, 2, 2)
	mustMerge(t, e)
	final, e = s.FinishOrganization(mergeCtx, task.ID, 2)
	mustMerge(t, e)
	if final.State != "completed" || organizationCount(t, s, "SELECT count(*) FROM memories") != 3 {
		t.Fatal(final)
	}
	late, e := s.StageOrganization(mergeCtx, task.ID, 1, raw, nil, "")
	if e == nil || late.Attempt != 2 {
		t.Fatal("old attempt accepted")
	}
	other, _, payload := organizationFixture(t, s)
	mustMerge(t, s.ClaimOrganization(mergeCtx, other.ID, 1))
	_, e = s.CancelOrganization(mergeCtx, other.ID)
	mustMerge(t, e)
	_, e = s.StageOrganization(mergeCtx, other.ID, 1, payload, nil, "")
	if e == nil {
		t.Fatal("cancelled output accepted")
	}
	interrupted, _, payload := organizationFixture(t, s)
	stageOrganizationFixture(t, s, interrupted, payload)
	applied, e := s.ApplyOrganizationItem(mergeCtx, interrupted.ID, 1, 1)
	mustMerge(t, e)
	mustMerge(t, s.InterruptOrganizations(mergeCtx))
	current, e := s.OrganizationTask(mergeCtx, interrupted.ID)
	mustMerge(t, e)
	if current.State != "interrupted" || current.Attempt != 2 {
		t.Fatal(current)
	}
	current, created, e = s.ResumeOrganization(mergeCtx, records.OrganizationResume{Task: interrupted.ID, OperationID: uuid.NewString(), ExpectedAttempt: 2})
	mustMerge(t, e)
	if !created || current.Attempt != 3 {
		t.Fatal(current)
	}
	preserved, e := s.ApplyOrganizationItem(mergeCtx, interrupted.ID, 3, 1)
	mustMerge(t, e)
	if !reflect.DeepEqual(preserved.Receipt, applied.Receipt) {
		t.Fatal("restart duplicated applied fact")
	}
	_, e = s.ApplyOrganizationItem(mergeCtx, interrupted.ID, 3, 2)
	mustMerge(t, e)
	_, e = s.FinishOrganization(mergeCtx, interrupted.ID, 3)
	mustMerge(t, e)
}
func TestOrganizationV3BackupAndLegacyRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-v3.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	mustMerge(t, migrateMerge(tx))
	mustMerge(t, tx.Commit())
	legacy.Schema = 3
	task, decision, payload := mergeFixture(t, legacy)
	completeFixture(t, legacy, task, payload)
	_, e = legacy.ReviewMerge(mergeCtx, decision, true)
	mustMerge(t, e)
	tables := []string{"memories", "versions", "operations", "jobs", "index_bindings", "scopes", "memory_metadata", "activities", "connections", "repositories", "merge_tasks", "merge_attempts", "merge_proposals", "merge_decisions", "merge_receipts", "merge_sources"}
	before := map[string][][]any{}
	for _, table := range tables {
		before[table] = sqlRows(t, legacy.DB, table)
	}
	_, e = legacy.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	readTx, e := reader.Begin()
	mustMerge(t, e)
	var held int
	mustMerge(t, readTx.QueryRow(`SELECT count(*) FROM memories`).Scan(&held))
	mustMerge(t, legacy.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("committed WAL fixture missing")
	}
	s, e := Open(path)
	mustMerge(t, readTx.Rollback())
	mustMerge(t, reader.Close())
	mustMerge(t, e)
	defer s.Close()
	if s.Schema != SchemaVersion {
		t.Fatal(s.Schema)
	}
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], sqlRows(t, s.DB, table)) {
			t.Fatal("legacy table changed", table)
		}
	}
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v3-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	stat, e := os.Stat(backups[0])
	mustMerge(t, e)
	if stat.Mode().Perm() != 0600 {
		t.Fatal("backup permissions")
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 3 {
		t.Fatal(version)
	}
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], sqlRows(t, backup, table)) {
			t.Fatal("backup dropped data", table)
		}
	}
	restored := &Store{DB: backup, Schema: 3}
	v, e := restored.Current(mergeCtx, task.Snapshot.Inputs[0].Memory)
	mustMerge(t, e)
	if v.State != "archived" {
		t.Fatal(v)
	}
	v, e = restored.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v.Revision, RestoreRevision: 1})
	mustMerge(t, e)
	if v.Revision != 3 || v.State != "active" {
		t.Fatal("old contract restore", v)
	}
}

func organizationCount(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	mustMerge(t, s.DB.QueryRow(q).Scan(&n))
	return n
}

func TestOrganizationInvalidOutputsAndScopeGate(t *testing.T) {
	for _, kind := range []string{"approval", "delete", "wrong_attempt", "wrong_digest", "forged_quote", "unknown_source", "duplicate_index", "empty_summary", "tag_target", "blank_result"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, _, raw := organizationFixture(t, s)
			mustMerge(t, s.ClaimOrganization(mergeCtx, task.ID, 1))
			var body map[string]any
			mustMerge(t, json.Unmarshal(raw, &body))
			items := body["items"].([]any)
			first := items[0].(map[string]any)
			switch kind {
			case "approval":
				body["approved"] = true
			case "delete":
				first["action"] = "delete"
			case "wrong_attempt":
				body["attempt"] = 2
			case "wrong_digest":
				body["input_digest"] = "forged"
			case "forged_quote":
				first["sources"].([]any)[0].(map[string]any)["quote"] = "unsubmitted original"
			case "unknown_source":
				first["sources"].([]any)[0].(map[string]any)["memory"] = uuid.NewString()
			case "duplicate_index":
				items[1].(map[string]any)["index"] = 1
			case "empty_summary":
				first["text"] = " "
			case "tag_target":
				items[1].(map[string]any)["target"] = uuid.NewString()
			case "blank_result":
				body["items"] = []any{}
				body["explanation"] = " "
			}
			payload, _ := json.Marshal(body)
			_, e := task.Snapshot.Decode(payload)
			if e == nil {
				t.Fatal("invalid output validated")
			}
			failed, e := s.StageOrganization(mergeCtx, task.ID, 1, payload, map[string]bool{"protocol_fixture": true}, "")
			mustMerge(t, e)
			if failed.State != "failed" || failed.Error != "invalid_proposal" || failed.Result != nil {
				t.Fatal(failed)
			}
			if organizationCount(t, s, "SELECT count(*) FROM memories") != 2 || organizationCount(t, s, "SELECT count(*) FROM organization_items") != 0 {
				t.Fatal("invalid output changed data")
			}
		})
	}
	s, _ := mergeDB(t)
	task, _, _ := organizationFixture(t, s)
	_, _, e := s.StartOrganization(mergeCtx, records.OrganizationStart{Project: uuid.NewString(), OperationID: uuid.NewString(), Inputs: []records.MergeTarget{{Memory: task.Snapshot.Inputs[0].Memory, Revision: 1}}})
	if e == nil {
		t.Fatal("unknown project")
	}
	var global string
	mustMerge(t, s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&global))
	_, _, e = s.StartOrganization(mergeCtx, records.OrganizationStart{Project: global, OperationID: uuid.NewString(), Inputs: []records.MergeTarget{{Memory: task.Snapshot.Inputs[0].Memory, Revision: 1}}})
	if e == nil {
		t.Fatal("global authorization implicitly expanded")
	}
	other, _, _ := organizationFixture(t, s)
	_, _, e = s.StartOrganization(mergeCtx, records.OrganizationStart{Project: task.Project, OperationID: uuid.NewString(), Inputs: []records.MergeTarget{{Memory: other.Snapshot.Inputs[0].Memory, Revision: 1}}})
	if e == nil {
		t.Fatal("cross project source accepted")
	}
}

func TestOrganizationStaleSummaryCannotMerge(t *testing.T) {
	for _, mode := range []string{"start", "list", "pending", "confirm", "applied"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, _, raw := organizationFixture(t, s)
			stageOrganizationFixture(t, s, task, raw)
			summary, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
			mustMerge(t, e)
			start := records.MergeStart{OperationID: uuid.NewString(), Project: task.Project, Inputs: []records.MergeTarget{{Memory: summary.Receipt.Memory, Revision: 1}, {Memory: task.Snapshot.Inputs[1].Memory, Revision: 1}}}
			var review records.MergeReview
			if mode != "start" {
				m, _, e := s.StartMerge(mergeCtx, start)
				mustMerge(t, e)
				p := records.MergeProposal{Schema: m.Snapshot.Schema, Task: m.ID, Attempt: m.Attempt, Digest: m.Snapshot.Digest, ID: m.Snapshot.ProposalID(), Action: "merge", Title: "合并", Text: "合并后的项目约定"}
				for _, v := range m.Snapshot.Inputs {
					p.Sources = append(p.Sources, records.MergeSource{Memory: v.Memory, Revision: v.Revision, Quote: v.Text})
				}
				b, _ := json.Marshal(p)
				completeFixture(t, s, m, b)
				review = records.MergeReview{Task: m.ID, Proposal: p.ID, Digest: records.MergeDigest(p), Receipt: uuid.NewString()}
				if mode == "applied" {
					_, e = s.ReviewMerge(mergeCtx, review, true)
					mustMerge(t, e)
				}
			}
			source := task.Snapshot.Inputs[0]
			_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: source.Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "来源更新后的规则"})
			mustMerge(t, e)
			switch mode {
			case "start":
				_, _, e = s.StartMerge(mergeCtx, start)
				if e == nil {
					t.Fatal("stale summary accepted")
				}
			case "list":
				page, e := s.MergeList(mergeCtx, records.MergeListQuery{Project: task.Project})
				mustMerge(t, e)
				if len(page.Items) != 1 || page.Items[0].ReviewState != "expired" {
					t.Fatal(page)
				}
			case "pending":
				m, e := s.MergeTask(mergeCtx, review.Task)
				mustMerge(t, e)
				if m.ReviewState != "expired" {
					t.Fatal(m.ReviewState)
				}
			case "confirm":
				m, e := s.ReviewMerge(mergeCtx, review, true)
				if e == nil && m.ReviewState == "applied" {
					t.Fatal("stale proposal applied")
				}
			case "applied":
				m, e := s.ReviewMerge(mergeCtx, review, true)
				mustMerge(t, e)
				if m.ReviewState != "applied" {
					t.Fatal("confirmed fact was rewritten")
				}
			}
		})
	}
}

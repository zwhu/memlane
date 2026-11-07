package store

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

func TestExperienceCopyReplayAndSource(t *testing.T) {
	s, _ := mergeDB(t)
	_, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	base, _, _ := organizationFixture(t, s)
	source := base.Snapshot.Inputs[0]
	d := records.ExperienceCopy{OperationID: uuid.NewString(), Project: base.Project, Memory: source.Memory, Revision: 1}
	r, e := s.CopyExperience(mergeCtx, d)
	mustMerge(t, e)
	v, e := s.Current(mergeCtx, r.Memory)
	mustMerge(t, e)
	if v.ScopeKind != "experience" || v.Text != source.Text || v.Experience == nil || v.Experience.Kind != "copy" || v.Derived.Kind != "global_copy" {
		t.Fatal(v)
	}
	before := sqlRows(t, s.DB, "versions")
	replay, e := s.CopyExperience(mergeCtx, d)
	mustMerge(t, e)
	if !reflect.DeepEqual(replay, r) || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
		t.Fatal("copy replay duplicated")
	}
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: base.Project, Memory: source.Memory, ExpectedRevision: 1, Text: "new project rule"})
	mustMerge(t, e)
	replay, e = s.CopyExperience(mergeCtx, d)
	mustMerge(t, e)
	if !reflect.DeepEqual(replay, r) {
		t.Fatal("source edit undid approved copy")
	}
	other := d
	other.OperationID = uuid.NewString()
	_, e = s.CopyExperience(mergeCtx, other)
	if e == nil {
		t.Fatal("new stale copy accepted")
	}
	oldSource, e := s.OrganizationSources(mergeCtx, r.Memory, 1)
	mustMerge(t, e)
	if len(oldSource) != 1 || oldSource[0].Revision != 1 || oldSource[0].Quote != source.Text {
		t.Fatal("source history lost")
	}
	if !oldSource[0].Changed || oldSource[0].CurrentRevision != 2 {
		t.Fatal("source change not visible")
	}
	_, e = s.Apply(mergeCtx, Mutation{Kind: "delete", OperationID: uuid.NewString(), Dataset: v.Dataset, Memory: v.Memory, ExpectedRevision: 1})
	mustMerge(t, e)
	_, e = s.CopyExperience(mergeCtx, d)
	mustMerge(t, e)
	v, e = s.Current(mergeCtx, r.Memory)
	mustMerge(t, e)
	if v.State != "deleted" || v.Revision != 2 {
		t.Fatal("replay restored deleted experience")
	}
}
func globalExperienceFixture(t *testing.T, s *Store) (records.OrganizationTask, records.OrganizationReview) {
	t.Helper()
	_, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	base, _, _ := organizationFixture(t, s)
	task, _, e := s.StartOrganization(mergeCtx, records.OrganizationStart{OperationID: uuid.NewString(), Project: base.Project, Review: true, Global: true, Purpose: "global", Inputs: []records.MergeTarget{{Memory: base.Snapshot.Inputs[0].Memory, Revision: 1}}})
	mustMerge(t, e)
	source := task.Snapshot.Inputs[0]
	dest := task.Snapshot.Destinations[0]
	item := records.OrganizationItem{Index: 1, Action: "global_experience", Title: "先核对工具约定", Text: "变更依赖时，应先遵循项目自己的依赖管理约定。", Conditions: "适用于存在已确认依赖管理规则的项目；项目明确要求优先。", Destination: dest.ID, Reason: "只泛化给定项目的依赖约定", Tags: []string{}, Sources: []records.MergeSource{{Memory: source.Memory, Revision: 1, Quote: source.Text}}}
	result := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: 1, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Items: []records.OrganizationItem{item}, Explanation: "global protocol fixture"}
	raw, e := json.Marshal(result)
	mustMerge(t, e)
	stageOrganizationFixture(t, s, task, raw)
	task, e = s.FinishOrganization(mergeCtx, task.ID, 1)
	mustMerge(t, e)
	if task.Entries[0].State != "review_pending" || organizationCount(t, s, `SELECT count(*) FROM global_experiences`) != 0 {
		t.Fatal("global proposal applied early", task)
	}
	return task, records.OrganizationReview{Task: task.ID, Item: task.Snapshot.ItemID(1), Digest: records.MergeDigest(item), Receipt: uuid.NewString(), Confirm: true}
}
func TestExperiencePreviewApproveConditionsRestore(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := globalExperienceFixture(t, s)
	done, e := s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	r := done.Entries[0].Receipt
	v, e := s.Current(mergeCtx, r.Memory)
	mustMerge(t, e)
	if v.Experience == nil || v.Experience.Kind != "distill" || v.Experience.Conditions != task.Entries[0].Item.Conditions || v.Original != task.Entries[0].Item.Text || v.SourceDetail.Fragment != "" {
		t.Fatal("wrong current/global provenance", v)
	}
	changed := "仅用于明确允许的另一种条件"
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 1, Text: "user checked generic body", Conditions: &changed})
	mustMerge(t, e)
	restored, e := s.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if restored.Revision != 3 || restored.Experience.Conditions != task.Entries[0].Item.Conditions {
		t.Fatal("conditions restore lost", restored)
	}
	history, e := s.History(mergeCtx, v.Memory, 0, 50)
	mustMerge(t, e)
	if history.Items[1].Experience.Conditions != changed || history.Items[2].Experience.Conditions != task.Entries[0].Item.Conditions {
		t.Fatal("mixed-version history conditions", history)
	}
	sources, e := s.OrganizationSources(mergeCtx, v.Memory, 3)
	mustMerge(t, e)
	if len(sources) != 1 || sources[0].Revision != 1 {
		t.Fatal("global restored lineage lost")
	}
	replay, e := s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	if !reflect.DeepEqual(replay.Entries[0].Receipt, r) {
		t.Fatal("global replay changed confirmed fact")
	}
}

func TestExperienceAtomicFailureAndScope(t *testing.T) {
	for _, mode := range []string{"copy", "distill"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, d := globalExperienceFixture(t, s)
			before := sqlRows(t, s.DB, "versions")
			_, e := s.DB.Exec(`CREATE TRIGGER experience_fail BEFORE INSERT ON global_experiences BEGIN SELECT RAISE(ABORT,'SQL global metadata fault fixture'); END`)
			mustMerge(t, e)
			copy := records.ExperienceCopy{OperationID: uuid.NewString(), Project: task.Project, Memory: task.Snapshot.Inputs[0].Memory, Revision: 1}
			if mode == "copy" {
				_, e = s.CopyExperience(mergeCtx, copy)
			} else {
				_, e = s.ReviewOrganization(mergeCtx, d)
			}
			if e == nil || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) || organizationCount(t, s, `SELECT count(*) FROM global_experience_decisions`) != 0 {
				t.Fatal("global SQL failure did not roll back")
			}
			_, e = s.DB.Exec(`DROP TRIGGER experience_fail`)
			mustMerge(t, e)
			if mode == "copy" {
				_, e = s.CopyExperience(mergeCtx, copy)
				mustMerge(t, e)
				wrong := copy
				wrong.Revision = 2
				_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: copy.Memory, Dataset: copy.Project, ExpectedRevision: 1, Text: "new source"})
				mustMerge(t, e)
				_, e = s.CopyExperience(mergeCtx, wrong)
				if !errors.Is(e, ErrConflict) {
					t.Fatal("operation reused for a different source", e)
				}
				wrong.OperationID = uuid.NewString()
				wrong.Project = task.Snapshot.Destinations[0].ID
				_, e = s.CopyExperience(mergeCtx, wrong)
				if e == nil {
					t.Fatal("non-project source scope accepted")
				}
			} else {
				_, e = s.ReviewOrganization(mergeCtx, d)
				mustMerge(t, e)
			}
		})
	}
}

func TestExperienceV5MigrationPreservesWALAndDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v5.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	mustMerge(t, migrateMerge(tx))
	mustMerge(t, migrateOrganization(tx))
	mustMerge(t, migrateOrganizationReviews(tx))
	mustMerge(t, tx.Commit())
	legacy.Schema = 5
	task, d := reviewOrganizationFixture(t, legacy, "merge")
	_, e = legacy.ReviewOrganization(mergeCtx, d)
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
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v5-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("wrong migration backup", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 5 || integrity != "ok" {
		t.Fatal("wrong backup", version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("old data or backup changed", name)
		}
	}
	replay, e := current.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	if replay.ID != task.ID || replay.Entries[0].Receipt == nil {
		t.Fatal("old decision replay lost")
	}
}

func TestExperienceCopiedSummaryMarksChangedRoot(t *testing.T) {
	s, _ := mergeDB(t)
	_, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	task, _, raw := organizationFixture(t, s)
	stageOrganizationFixture(t, s, task, raw)
	summary, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	r, e := s.CopyExperience(mergeCtx, records.ExperienceCopy{OperationID: uuid.NewString(), Project: task.Project, Memory: summary.Receipt.Memory, Revision: 1})
	mustMerge(t, e)
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: task.Snapshot.Inputs[0].Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "new underlying rule"})
	mustMerge(t, e)
	sources, e := s.OrganizationSources(mergeCtx, r.Memory, 1)
	mustMerge(t, e)
	if len(sources) != 1 || !sources[0].Changed || sources[0].CurrentRevision != 1 {
		t.Fatal("stale same-version summary was not marked", sources)
	}
	v, e := s.Current(mergeCtx, r.Memory)
	mustMerge(t, e)
	if v.Derived.State != "user_confirmed" || v.Revision != 1 {
		t.Fatal("source change rewrote confirmed global copy")
	}
}
func TestExperienceRejectAndExpire(t *testing.T) {
	for _, mode := range []string{"reject", "source", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := mergeDB(t)
			task, d := globalExperienceFixture(t, s)
			switch mode {
			case "reject":
				d.Confirm = false
				_, e := s.ReviewOrganization(mergeCtx, d)
				mustMerge(t, e)
			case "source":
				source := task.Snapshot.Inputs[0]
				_, e := s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: source.Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "new source"})
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("stale global proposal accepted")
				}
			case "cancel":
				_, e := s.CancelOrganization(mergeCtx, task.ID)
				mustMerge(t, e)
				_, e = s.ReviewOrganization(mergeCtx, d)
				if e == nil {
					t.Fatal("cancelled global proposal accepted")
				}
			}
			if organizationCount(t, s, `SELECT count(*) FROM global_experiences`) != 0 {
				t.Fatal("unconfirmed global knowledge created")
			}
		})
	}
}

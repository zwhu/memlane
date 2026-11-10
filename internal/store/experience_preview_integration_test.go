package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func previewFixture(t *testing.T, s *Store) records.ExperiencePreview {
	t.Helper()
	task, r := globalExperienceFixture(t, s)
	input := task.Snapshot.Inputs[0]
	edit := records.ExperienceReviewEdit{Title: "未确认的标题", Text: "仍在编辑的候选", Conditions: "项目约定优先"}
	r.Edit = &edit
	return records.ExperiencePreview{ID: uuid.NewString(), Project: task.Project, Source: records.MergeTarget{Memory: input.Memory, Revision: input.Revision}, TaskID: task.ID, Candidate: r.Item, Open: true, Page: "通用知识", ReturnPage: "项目", ReturnScope: task.Project, ReturnSelected: input.Memory, Scroll: 84, BodyScroll: 84, ReturnScroll: 32, Focus: "experience-review-text-" + r.Item, Drafts: map[string]records.ExperiencePreviewDraft{r.Item: {ProposalDigest: r.Digest, Value: edit, Request: &r}}}
}

func TestExperiencePreviewRecoveryCASAndDraftIsolation(t *testing.T) {
	s, _ := mergeDB(t)
	p := previewFixture(t, s)
	head, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	w := records.ExperiencePreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p}
	before := sqlRows(t, s.DB, "versions")
	done, err := s.SaveExperiencePreview(mergeCtx, w)
	mustMerge(t, err)
	if done.Generation != 1 {
		t.Fatal(done)
	}
	replay, err := s.SaveExperiencePreview(mergeCtx, w)
	mustMerge(t, err)
	if replay != done || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
		t.Fatal("draft or replay changed knowledge")
	}
	changed := w
	changed.Preview.Page = "总览"
	_, err = s.SaveExperiencePreview(mergeCtx, changed)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	changed.Operation = uuid.NewString()
	_, err = s.SaveExperiencePreview(mergeCtx, changed)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("late write accepted", err)
	}
	changed.Generation = 1
	_, err = s.SaveExperiencePreview(mergeCtx, changed)
	mustMerge(t, err)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	mustMerge(t, s.Close())
	reopened, err := Open(path)
	mustMerge(t, err)
	defer reopened.Close()
	got, err := reopened.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Task == nil || got.Task.ID != p.TaskID || got.Source == nil || got.Source.Revision != 1 || got.Preview == nil || got.Preview.Drafts[p.Candidate].Value != p.Drafts[p.Candidate].Value || got.Preview.Focus != p.Focus || got.Generation != 2 {
		t.Fatal(got)
	}
	_, err = reopened.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: p.Project, Memory: p.Source.Memory, ExpectedRevision: 1, Text: "新的v2项目约定"})
	mustMerge(t, err)
	got, err = reopened.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Source.Revision != 1 || got.Source.Text == "新的v2项目约定" || got.Preview.Drafts[p.Candidate].Value.Text != "仍在编辑的候选" {
		t.Fatal("draft/source re-labelled", got)
	}
}

func TestExperiencePreviewPendingStartOnlyReadsOriginalReceipt(t *testing.T) {
	s, _ := mergeDB(t)
	p := previewFixture(t, s)
	originalTask := p.TaskID
	p.TaskID = ""
	p.Candidate = ""
	p.Drafts = nil
	d := records.OrganizationStart{OperationID: uuid.NewString(), Project: p.Project, Inputs: []records.MergeTarget{p.Source}, Review: true, Global: true, Purpose: "global", Processing: &records.ProcessingTaskAuthorization{Revision: uuid.NewString(), Generation: 1}}
	p.Start = &d
	head, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	_, err = s.SaveExperiencePreview(mergeCtx, records.ExperiencePreviewWrite{Identity: head.Identity, Generation: 0, Operation: uuid.NewString(), Preview: p})
	mustMerge(t, err)
	n := organizationCount(t, s, `SELECT count(*) FROM organization_tasks`)
	got, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Task != nil || got.Preview.Start.OperationID != d.OperationID || organizationCount(t, s, `SELECT count(*) FROM organization_tasks`) != n {
		t.Fatal("getter launched pending operation", got)
	}
	_, err = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: p.Project, Memory: p.Source.Memory, ExpectedRevision: 1, Text: "v2正文"})
	mustMerge(t, err)
	got, err = s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Source.Revision != 1 || got.Source.Text == "v2正文" {
		t.Fatal("pending source mislabeled", got)
	}
	// Transport fixture: an already committed task with a lost start response.
	_, err = s.DB.Exec(`UPDATE organization_tasks SET operation_id=?,request_digest=? WHERE id=?`, d.OperationID, records.MergeDigest(d), originalTask)
	mustMerge(t, err)
	got, err = s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Task == nil || got.Preview.Start != nil || got.Preview.TaskID != got.Task.ID || organizationCount(t, s, `SELECT count(*) FROM organization_tasks`) != n {
		t.Fatal("lost receipt not recovered", got)
	}
}

func TestExperiencePreviewRestoreRetiresIdentityAndDrafts(t *testing.T) {
	s, _ := mergeDB(t)
	p := previewFixture(t, s)
	head, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	w := records.ExperiencePreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: p}
	_, err = s.SaveExperiencePreview(mergeCtx, w)
	mustMerge(t, err)
	_, data, err := s.snapshot(mergeCtx)
	mustMerge(t, err)
	restored, _, err := PrepareRestoredSnapshot(context.Background(), data, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, restored, 0600))
	next, err := Open(path)
	mustMerge(t, err)
	defer next.Close()
	state, err := next.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if state.Identity == head.Identity || state.Preview != nil || state.Generation != 0 || organizationCount(t, next, `SELECT count(*) FROM experience_previews`) != 0 {
		t.Fatal("old preview survived restore", state)
	}
	w.Generation = 0
	w.Operation = uuid.NewString()
	_, err = next.SaveExperiencePreview(mergeCtx, w)
	if err == nil || err.Error() != "preview_retired" {
		t.Fatal("old writer adopted restored DB", err)
	}
	// Cross-library identity, wrong task/source and candidate tampering fail.
	other, _ := mergeDB(t)
	_, err = other.SaveExperiencePreview(mergeCtx, w)
	if err == nil {
		t.Fatal("cross-library write accepted")
	}
	p.Project = uuid.NewString()
	w.Identity = state.Identity
	w.Preview = p
	_, err = next.SaveExperiencePreview(mergeCtx, w)
	if err == nil {
		t.Fatal("wrong scope accepted")
	}
}

func TestExperiencePreviewSchema21MigrationPreservesSourceAndBackup(t *testing.T) {
	s, _ := mergeDB(t)
	_ = previewFixture(t, s)
	_, err := s.DB.Exec(`DROP TABLE experience_previews; DROP TABLE experience_preview_head; PRAGMA user_version=21`)
	mustMerge(t, err)
	s.Schema = 21
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v21-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 || current.Schema != 22 {
		t.Fatal("schema21 backup or migration absent", backups, current.Schema)
	}
	old, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer old.Close()
	var version int
	mustMerge(t, old.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 21 {
		t.Fatal("migration rewrote original backup", version)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, old, table)) || !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) {
			t.Fatal("migration changed old record", table)
		}
	}
	head, err := current.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if head.Identity == "" || head.Preview != nil || head.Generation != 0 {
		t.Fatal("migration invented preview", head)
	}
}

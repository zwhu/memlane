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

func organizationPreviewFixture(t *testing.T, s *Store) records.OrganizationPreview {
	t.Helper()
	task, _, _ := organizationFixture(t, s)
	p := records.OrganizationPreview{ID: uuid.NewString(), Open: true, New: true, Project: task.Project, Kind: "knowledge"}
	for _, input := range task.Snapshot.Inputs {
		p.Inputs = append(p.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	return p
}

func TestOrganizationPreviewLostStartRecoversFrozenTaskAfterConfigurationChange(t *testing.T) {
	s, _ := mergeDB(t)
	p := organizationPreviewFixture(t, s)
	v, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("original-model")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("original-dependency")
	processingVerifiedFixture(t, s, v.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: v.ID}, dependency, processingTime)
	mustMerge(t, err)
	p.Start = &records.OrganizationStart{OperationID: uuid.NewString(), Project: p.Project, Inputs: p.Inputs, Processing: &records.ProcessingTaskAuthorization{Revision: v.ID, Generation: 1}}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	w := records.OrganizationPreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: p}
	done, err := s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	task, created, err := s.StartProcessingOrganization(mergeCtx, *p.Start, dependency)
	mustMerge(t, err)
	if !created {
		t.Fatal("new task absent")
	}
	next, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: v.ID, Spec: processingProvider("next-model")}, processingTime)
	mustMerge(t, err)
	processingVerifiedFixture(t, s, next.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: next.ID, ExpectedGeneration: 1}, dependency, processingTime)
	mustMerge(t, err)
	before := sqlRows(t, s.DB, "organization_attempts")
	read, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Task == nil || read.Task.ID != task.ID || read.Preview.New || read.Preview.TaskID != task.ID || read.Preview.Start == nil || read.Task.Snapshot.Processing.Revision.ID != v.ID || !reflect.DeepEqual(before, sqlRows(t, s.DB, "organization_attempts")) {
		t.Fatal("recovery changed original authority or started work", read)
	}
	w.Generation, w.Operation, w.Preview = done.Generation, uuid.NewString(), *read.Preview
	w.Preview.Start = nil
	_, err = s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	read, err = s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Preview.Start != nil || read.Task.ID != task.ID {
		t.Fatal("known start not acknowledged")
	}
	for _, mutate := range []func(*records.OrganizationPreview){
		func(p *records.OrganizationPreview) { p.New = true },
		func(p *records.OrganizationPreview) { p.Inputs = p.Inputs[:1] },
		func(p *records.OrganizationPreview) { p.Review = true },
	} {
		bad := *read.Preview
		mutate(&bad)
		_, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: read.Identity, Generation: read.Generation, Operation: uuid.NewString(), Preview: bad})
		if err == nil {
			t.Fatal("known task rebound to incompatible draft", bad)
		}
	}
}

func TestOrganizationPreviewRestoreRetiresBothLedgersAndStartIntents(t *testing.T) {
	s, _ := mergeDB(t)
	p := organizationPreviewFixture(t, s)
	p.Start = &records.OrganizationStart{OperationID: uuid.NewString(), Project: p.Project, Inputs: p.Inputs, Processing: &records.ProcessingTaskAuthorization{Revision: uuid.NewString(), Generation: 1}}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	w := records.OrganizationPreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: p}
	_, err = s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	ep := previewFixture(t, s)
	eh, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	_, err = s.SaveExperiencePreview(mergeCtx, records.ExperiencePreviewWrite{Identity: eh.Identity, Operation: uuid.NewString(), Preview: ep})
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
	state, err := next.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	experience, err := next.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if state.Identity == head.Identity || state.Preview != nil || state.Generation != 0 || experience.Identity == eh.Identity || experience.Preview != nil {
		t.Fatal("restored draft identity survived", state, experience)
	}
	for _, table := range []string{"organization_previews", "organization_start_intents", "experience_previews"} {
		if organizationCount(t, next, "SELECT count(*) FROM "+table) != 0 {
			t.Fatal("old drafts/intents survived", table)
		}
	}
	w.Generation = 0
	w.Operation = uuid.NewString()
	if _, err = next.SaveOrganizationPreview(mergeCtx, w); err == nil || err.Error() != "preview_retired" {
		t.Fatal("old writer adopted restored root", err)
	}
}

func TestOrganizationPreviewSchema22MigrationPreservesOriginalBackup(t *testing.T) {
	s, _ := mergeDB(t)
	ep := previewFixture(t, s)
	head, err := s.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	_, err = s.SaveExperiencePreview(mergeCtx, records.ExperiencePreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: ep})
	mustMerge(t, err)
	_, err = s.DB.Exec("DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; DROP TABLE index_rebuild_controls; DROP TABLE index_attempts; DROP TABLE agent_connection_batch_head; DROP TABLE agent_connection_batches; DROP TABLE organization_start_intents; DROP TABLE organization_previews; DROP TABLE organization_preview_head; PRAGMA user_version=22")
	mustMerge(t, err)
	s.Schema = 22
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path))
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v22-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("schema22 backup or migration absent", backups, current.Schema)
	}
	old, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer old.Close()
	var version int
	mustMerge(t, old.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 22 {
		t.Fatal("original backup overwritten", version)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, old, table)) || !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) {
			t.Fatal("old data changed", table)
		}
	}
	state, err := current.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if state.Identity == "" || state.Preview != nil || state.Generation != 0 {
		t.Fatal("migration invented preview", state)
	}
	got, err := current.ExperiencePreview(mergeCtx)
	mustMerge(t, err)
	if got.Preview == nil || got.Preview.ID != ep.ID || got.Identity != head.Identity {
		t.Fatal("existing L04 ledger changed", got)
	}
}

func TestOrganizationPreviewDraftCASDoesNotChangeKnowledge(t *testing.T) {
	s, _ := mergeDB(t)
	p := organizationPreviewFixture(t, s)
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	before := sqlRows(t, s.DB, "versions")
	w := records.OrganizationPreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p}
	done, err := s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	if done.Generation != 1 {
		t.Fatal(done)
	}
	replay, err := s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	if replay.Identity != done.Identity || replay.Generation != done.Generation || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
		t.Fatal("draft altered knowledge or replay")
	}
	read, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Preview == nil || len(read.Inputs) != 2 || read.Task != nil || read.Preview.ID != p.ID {
		t.Fatal("draft restore not independent", read)
	}
	w.Preview.Scroll = 30
	if _, err = s.SaveOrganizationPreview(mergeCtx, w); !errors.Is(err, ErrConflict) {
		t.Fatal("replay accepted different draft", err)
	}
	w.Operation = uuid.NewString()
	if _, err = s.SaveOrganizationPreview(mergeCtx, w); !errors.Is(err, ErrConflict) {
		t.Fatal("old generation accepted", err)
	}
	w.Generation = 1
	_, err = s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
}

func TestOrganizationPreviewUnknownStartReadAndExplicitAbandon(t *testing.T) {
	s, _ := mergeDB(t)
	p := organizationPreviewFixture(t, s)
	first, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("preview-model")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("preview-model-dependency")
	processingVerifiedFixture(t, s, first.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: first.ID}, dependency, processingTime)
	mustMerge(t, err)
	p.Start = &records.OrganizationStart{OperationID: uuid.NewString(), Project: p.Project, Inputs: p.Inputs, Processing: &records.ProcessingTaskAuthorization{Revision: first.ID, Generation: 1}}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	w := records.OrganizationPreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p}
	done, err := s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	before := organizationCount(t, s, "SELECT count(*) FROM organization_tasks")
	read, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Task != nil || read.Preview.Start.OperationID != p.Start.OperationID || organizationCount(t, s, "SELECT count(*) FROM organization_tasks") != before {
		t.Fatal("read started work")
	}
	w.Generation = done.Generation
	w.Operation = uuid.NewString()
	w.Preview.Start = nil
	if _, err = s.SaveOrganizationPreview(mergeCtx, w); err == nil {
		t.Fatal("unknown start dropped without explicit abandon")
	}
	w.AbandonStart = true
	_, err = s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	if _, _, err = s.StartProcessingOrganization(mergeCtx, *p.Start, dependency); err == nil {
		t.Fatal("late abandoned start created work")
	}
	if organizationCount(t, s, "SELECT count(*) FROM organization_tasks") != before {
		t.Fatal("abandon changed task count")
	}
}

func TestOrganizationPreviewRecoversSameTaskAndFrozenDecision(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := reviewOrganizationFixture(t, s, "merge")
	p := records.OrganizationPreview{ID: uuid.NewString(), Open: true, Project: task.Project, Kind: "knowledge", TaskID: task.ID, Candidate: task.Entries[0].ID, Review: true, Decisions: map[string]records.OrganizationReview{d.Item + "true": d}, RejectReasons: map[string]string{d.Item: "保留独立来源"}, Scroll: 80, ReviewScroll: map[string]float32{d.Item: 80}}
	for _, input := range task.Snapshot.Inputs {
		p.Inputs = append(p.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	w := records.OrganizationPreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: p}
	done, err := s.SaveOrganizationPreview(mergeCtx, w)
	mustMerge(t, err)
	before := sqlRows(t, s.DB, "versions")
	read, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Task.ID != task.ID || read.Preview.Decisions[d.Item+"true"].Receipt != d.Receipt || read.Preview.Scroll != 80 || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
		t.Fatal("restore changed task/request/knowledge")
	}
	w.Generation = done.Generation
	w.Operation = uuid.NewString()
	w.Preview.Decisions = map[string]records.OrganizationReview{}
	if _, err = s.SaveOrganizationPreview(mergeCtx, w); err == nil {
		t.Fatal("frozen decision deleted")
	}
}

func TestOrganizationPreviewTaskReentryAndFrozenResume(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := reviewOrganizationFixture(t, s, "merge")
	p := records.OrganizationPreview{ID: uuid.NewString(), Open: true, Project: task.Project, Kind: "knowledge", Review: true, TaskID: task.ID, Candidate: d.Item, RejectReasons: map[string]string{d.Item: "原任务未提交草稿"}}
	for _, input := range task.Snapshot.Inputs {
		p.Inputs = append(p.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	_, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: head.Identity, Operation: uuid.NewString(), Preview: p})
	mustMerge(t, err)
	other := organizationPreviewFixture(t, s)
	_, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: head.Identity, Generation: 1, Operation: uuid.NewString(), Preview: other})
	mustMerge(t, err)
	before := sqlRows(t, s.DB, "organization_attempts")
	read, err := s.OrganizationPreviewFor(mergeCtx, task.ID)
	mustMerge(t, err)
	if read.Preview == nil || read.Preview.ID != p.ID || read.Preview.RejectReasons[d.Item] != p.RejectReasons[d.Item] || read.Generation != 2 || !reflect.DeepEqual(before, sqlRows(t, s.DB, "organization_attempts")) {
		t.Fatal("task reentry lost independent draft or started work", read)
	}
	resume := records.OrganizationResume{Task: task.ID, OperationID: uuid.NewString(), ExpectedAttempt: 1}
	p.Resume = &resume
	write := records.OrganizationPreviewWrite{Identity: head.Identity, Generation: 2, Operation: uuid.NewString(), Preview: p}
	done, err := s.SaveOrganizationPreview(mergeCtx, write)
	mustMerge(t, err)
	write.Generation = done.Generation
	write.Operation = uuid.NewString()
	changed := resume
	changed.OperationID = uuid.NewString()
	write.Preview.Resume = &changed
	if _, err = s.SaveOrganizationPreview(mergeCtx, write); err == nil || err.Error() != "preview_request_frozen" {
		t.Fatal("resume request changed", err)
	}
	write.Preview.Resume = nil
	if _, err = s.SaveOrganizationPreview(mergeCtx, write); err == nil || err.Error() != "preview_request_frozen" {
		t.Fatal("unknown resume dropped", err)
	}
	// Explicit failure fixture, followed by the real resume transaction.
	_, err = s.DB.Exec("UPDATE organization_tasks SET state='partial_failed' WHERE id=?", task.ID)
	mustMerge(t, err)
	resumed, created, err := s.ResumeOrganization(mergeCtx, resume)
	mustMerge(t, err)
	if !created || resumed.Attempt != 2 {
		t.Fatal("original resume absent", resumed)
	}
	_, err = s.SaveOrganizationPreview(mergeCtx, write)
	mustMerge(t, err)
}

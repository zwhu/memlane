package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func preferenceFixture(t *testing.T) (*Store, records.ProjectPreferenceQuery, records.ProjectPreferenceConfirmation) {
	t.Helper()
	s, _ := mergeDB(t)
	scopes, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	pref := ""
	for _, v := range scopes {
		if v.Kind == "preference" {
			pref = v.ID
		}
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if output, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	project, e := s.RegisterProject(mergeCtx, records.ScopeDraft{Name: "Generated comparison project", Path: repo})
	mustMerge(t, e)
	versions := []records.Version{}
	for i, scope := range []string{project.ID, pref} {
		text := []string{"Synthetic explicit project convention.", "Synthetic personal default suggestion."}[i]
		v, e := s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: text, Title: "Independent record", Source: "Generated user source"})
		mustMerge(t, e)
		versions = append(versions, v)
	}
	q := records.ProjectPreferenceQuery{Project: project.ID, ProjectRecord: records.UsageTarget{Memory: versions[0].Memory, Dataset: project.ID, Revision: 1}, PreferenceRecord: records.UsageTarget{Memory: versions[1].Memory, Dataset: pref, Revision: 1}, Datasets: []string{project.ID, pref}}
	comparison, e := s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	r := records.ProjectPreferenceConfirmation{OperationID: uuid.NewString(), LibraryIdentity: comparison.LibraryIdentity, Query: q, ProjectQuote: versions[0].Text, PreferenceQuote: versions[1].Text, Conditions: "Only this explicitly selected project and these versions.", Reason: "User explicitly selected the project convention for this reference.", ConfirmProjectRule: true, Decision: "use_project"}
	return s, q, r
}

func TestProjectPreferenceReadConfirmReplayAndIndependentRecords(t *testing.T) {
	s, q, r := preferenceFixture(t)
	before := map[string][][]any{}
	for _, name := range []string{"memories", "versions", "memory_metadata", "jobs", "scope_history", "usage_events"} {
		before[name] = sqlRows(t, s.DB, name)
	}
	p, e := s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.Project.ID != q.Project || p.Project.Kind != "project" || p.Project.Path == "" || p.ProjectRecord.Text != r.ProjectQuote || p.PreferenceRecord.Text != r.PreferenceQuote || p.Confirmation != nil || p.ProjectConditionsRecorded || p.PreferenceConditionsRecorded {
		t.Fatal("comparison invented semantics or lost records", p)
	}
	if organizationCount(t, s, `SELECT count(*) FROM project_preference_decisions`) != 0 {
		t.Fatal("opening comparison confirmed a decision")
	}
	d, e := s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	if d.Source != "user" || d.Decision != "use_project" || d.ID != r.OperationID || d.State != "confirmed" || d.ConfirmedMS <= 0 || d.LibraryIdentity != r.LibraryIdentity {
		t.Fatal(d)
	}
	replay, e := s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	if !reflect.DeepEqual(d, replay) || organizationCount(t, s, `SELECT count(*) FROM project_preference_decisions`) != 1 {
		t.Fatal("confirmation replay changed or duplicated", replay)
	}
	for name, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, s.DB, name)) {
			t.Fatal("confirmation changed independent knowledge/index/usage", name)
		}
	}
	p, e = s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.Confirmation == nil || !reflect.DeepEqual(*p.Confirmation, d) {
		t.Fatal("missing exact-pair confirmation", p)
	}
	altered := r
	altered.Conditions = "Changed applicability"
	if _, e = s.ConfirmProjectPreference(mergeCtx, altered); !errors.Is(e, records.ErrConflict) {
		t.Fatal("same operation changed confirmation", e)
	}
	// Explicit confirmation is never defaulted from merely opening or selecting.
	for _, change := range []func(*records.ProjectPreferenceConfirmation){func(x *records.ProjectPreferenceConfirmation) { x.ConfirmProjectRule = false }, func(x *records.ProjectPreferenceConfirmation) { x.Decision = "use_preference" }, func(x *records.ProjectPreferenceConfirmation) { x.ProjectQuote = "not present" }, func(x *records.ProjectPreferenceConfirmation) { x.PreferenceQuote = "not present" }, func(x *records.ProjectPreferenceConfirmation) { x.Conditions = "" }, func(x *records.ProjectPreferenceConfirmation) { x.LibraryIdentity = uuid.NewString() }} {
		bad := r
		bad.OperationID = uuid.NewString()
		change(&bad)
		if _, e = s.ConfirmProjectPreference(mergeCtx, bad); e == nil {
			t.Fatal("invalid confirmation accepted", bad)
		}
	}
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: q.PreferenceRecord.Memory, Dataset: q.PreferenceRecord.Dataset, ExpectedRevision: 1, Text: "New personal default"})
	mustMerge(t, e)
	if _, e = s.ProjectPreferenceComparison(mergeCtx, q); e == nil {
		t.Fatal("old preference version accepted")
	}
	if _, e = s.ConfirmProjectPreference(mergeCtx, r); e == nil {
		t.Fatal("old operation revived a superseded confirmation")
	}
	q.PreferenceRecord.Revision = 2
	p, e = s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.Confirmation != nil {
		t.Fatal("old badge moved onto new version")
	}
	restored, e := s.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: q.PreferenceRecord.Memory, Dataset: q.PreferenceRecord.Dataset, ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if restored.Revision != 3 {
		t.Fatal("restore did not create new version", restored)
	}
	q.PreferenceRecord.Revision = 3
	p, e = s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.Confirmation != nil {
		t.Fatal("old badge revived by knowledge restore")
	}
}

func TestProjectPreferenceStrictKindsOriginStateScopeAndCAS(t *testing.T) {
	for _, scenario := range []string{"read_scope", "project_scope", "preference_kind", "raw", "imported", "deleted", "stale", "move", "revision"} {
		t.Run(scenario, func(t *testing.T) {
			s, q, r := preferenceFixture(t)
			switch scenario {
			case "read_scope":
				q.Datasets = []string{q.Project}
			case "project_scope":
				q.Project = q.PreferenceRecord.Dataset
				q.ProjectRecord.Dataset = q.Project
			case "preference_kind":
				q.PreferenceRecord = q.ProjectRecord
			case "raw", "imported":
				_, e := s.DB.Exec(`UPDATE memory_metadata SET origin=? WHERE memory=?`, scenario, q.ProjectRecord.Memory)
				mustMerge(t, e)
			case "deleted":
				_, e := s.Apply(mergeCtx, Mutation{Kind: "delete", OperationID: uuid.NewString(), Memory: q.PreferenceRecord.Memory, Dataset: q.PreferenceRecord.Dataset, ExpectedRevision: 1})
				mustMerge(t, e)
				q.PreferenceRecord.Revision = 2
			case "stale":
				_, e := s.DB.Exec(`INSERT INTO derived_memories(memory,revision,state) VALUES(?,1,'stale')`, q.ProjectRecord.Memory)
				mustMerge(t, e)
			case "move":
				_, e := s.DB.Exec(`UPDATE memories SET dataset=? WHERE id=?`, q.PreferenceRecord.Dataset, q.ProjectRecord.Memory)
				mustMerge(t, e)
			case "revision":
				q.ProjectRecord.Revision = 2
			}
			if _, e := s.ProjectPreferenceComparison(mergeCtx, q); e == nil {
				t.Fatal("invalid comparison accepted", scenario)
			}
			r.Query = q
			if _, e := s.ConfirmProjectPreference(mergeCtx, r); e == nil {
				t.Fatal("invalid confirmation accepted", scenario)
			}
			if organizationCount(t, s, `SELECT count(*) FROM project_preference_decisions`) != 0 {
				t.Fatal("failed request persisted decision")
			}
		})
	}
}

func TestProjectPreferenceBackupRestoreRetiresIdentityAndMigration28(t *testing.T) {
	s, q, r := preferenceFixture(t)
	_, e := s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	vault := t.TempDir()
	id := uuid.NewString()
	receipt, e := s.Backup(mergeCtx, vault, id)
	mustMerge(t, e)
	if receipt.Schema != SchemaVersion {
		t.Fatal(receipt)
	}
	snapshot, e := os.ReadFile(filepath.Join(vault, "backups/manual", id, "snapshot.sqlite"))
	mustMerge(t, e)
	data, _, e := PrepareRestoredSnapshot(context.Background(), snapshot, uuid.NewString())
	mustMerge(t, e)
	restoredPath := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(restoredPath, data, 0600))
	restored, e := Open(restoredPath)
	mustMerge(t, e)
	defer restored.Close()
	p, e := restored.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.LibraryIdentity == r.LibraryIdentity || p.Confirmation != nil {
		t.Fatal("restore revived confirmation", p)
	}
	lookup := records.ProjectPreferenceDecisionsQuery{LibraryIdentity: r.LibraryIdentity, Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: q.Datasets}
	if _, e = restored.ProjectPreferenceDecisions(mergeCtx, lookup); e == nil {
		t.Fatal("lookup accepted retired library identity")
	}
	lookup.LibraryIdentity = p.LibraryIdentity
	page, e := restored.ProjectPreferenceDecisions(mergeCtx, lookup)
	mustMerge(t, e)
	if len(page.Decisions) != 0 {
		t.Fatal("lookup revived restored history", page)
	}
	if organizationCount(t, restored, `SELECT count(*) FROM project_preference_decisions WHERE state='retired' AND json_extract(payload,'$.state')='retired'`) != 1 {
		t.Fatal("historical decision not explicitly retired")
	}
	if _, e = restored.ConfirmProjectPreference(mergeCtx, r); e == nil {
		t.Fatal("old library request replayed after restore")
	}
	r.LibraryIdentity = p.LibraryIdentity
	if _, e = restored.ConfirmProjectPreference(mergeCtx, r); !errors.Is(e, records.ErrConflict) {
		t.Fatal("retired operation reused for different library", e)
	}
	r.OperationID = uuid.NewString()
	_, e = restored.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	// A real schema28 database has no schema29 table. Keep the migration strict.
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, e := Open(path)
	mustMerge(t, e)
	_, e = legacy.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions;PRAGMA user_version=28`)
	mustMerge(t, e)
	mustMerge(t, legacy.Close())
	old, e := os.ReadFile(path)
	mustMerge(t, e)
	prepared, _, e := PrepareRestoredSnapshot(mergeCtx, old, uuid.NewString())
	mustMerge(t, e)
	if len(prepared) < 100 {
		t.Fatal("old backup not migrated")
	}
	upgraded, e := Open(path)
	mustMerge(t, e)
	defer upgraded.Close()
	if upgraded.Schema != SchemaVersion || organizationCount(t, upgraded, `SELECT count(*) FROM project_preference_decisions`) != 0 {
		t.Fatal("schema28 migration failed")
	}
}

func TestProjectPreferenceRestartAndPersistedConditions(t *testing.T) {
	s, q, r := preferenceFixture(t)
	// Explicit historical structured metadata fixture. Ordinary preference prose
	// is not parsed into conditions; persisted metadata is returned unchanged.
	_, e := s.DB.Exec(`INSERT INTO global_experiences(memory,revision,kind,conditions) VALUES(?,1,'copy','Persisted applicability from historical scope')`, q.ProjectRecord.Memory)
	mustMerge(t, e)
	p, e := s.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if !p.ProjectConditionsRecorded || p.ProjectConditions != "Persisted applicability from historical scope" || p.PreferenceConditionsRecorded || p.ProjectRecord.Experience == nil {
		t.Fatal("conditions invented or dropped", p)
	}
	d, e := s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	mustMerge(t, s.Close())
	reopened, e := Open(path)
	mustMerge(t, e)
	defer reopened.Close()
	p, e = reopened.ProjectPreferenceComparison(mergeCtx, q)
	mustMerge(t, e)
	if p.LibraryIdentity != r.LibraryIdentity || p.Confirmation == nil || !reflect.DeepEqual(*p.Confirmation, d) {
		t.Fatal("restart changed durable decision", p)
	}
}

func TestProjectPreferenceDecisionsDiscoverOnlyCurrentAuthorizedPairs(t *testing.T) {
	s, q, r := preferenceFixture(t)
	lookup := records.ProjectPreferenceDecisionsQuery{LibraryIdentity: r.LibraryIdentity, Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: q.Datasets}
	page, e := s.ProjectPreferenceDecisions(mergeCtx, lookup)
	mustMerge(t, e)
	if page.Decisions == nil || len(page.Decisions) != 0 {
		t.Fatal("unknown relation invented", page)
	}
	_, e = s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	r.OperationID = uuid.NewString()
	r.Conditions = "Latest explicit applicability for these same versions"
	latest, e := s.ConfirmProjectPreference(mergeCtx, r)
	mustMerge(t, e)
	before := sqlRows(t, s.DB, "project_preference_decisions")
	page, e = s.ProjectPreferenceDecisions(mergeCtx, lookup)
	mustMerge(t, e)
	if len(page.Decisions) != 1 || page.Decisions[0].ID != latest.ID || page.LibraryIdentity != r.LibraryIdentity || page.ProjectRecord != q.ProjectRecord || len(page.CoverageDatasets) != 2 {
		t.Fatal("current target did not discover latest exact pair", page)
	}
	limited := lookup
	limited.Datasets = []string{q.Project}
	page, e = s.ProjectPreferenceDecisions(mergeCtx, limited)
	mustMerge(t, e)
	if len(page.Decisions) != 0 || len(page.CoverageDatasets) != 1 {
		t.Fatal("unreadable preference exposed", page)
	}
	for _, bad := range []records.ProjectPreferenceDecisionsQuery{{LibraryIdentity: uuid.NewString(), Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: q.Datasets}, {Project: q.Project, ProjectRecord: records.UsageTarget{Memory: q.ProjectRecord.Memory, Dataset: q.Project, Revision: 2}, Datasets: q.Datasets}, {Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: []string{q.PreferenceRecord.Dataset}}} {
		if _, e = s.ProjectPreferenceDecisions(mergeCtx, bad); e == nil {
			t.Fatal("lookup failure became empty current page", bad)
		}
	}
	if !reflect.DeepEqual(before, sqlRows(t, s.DB, "project_preference_decisions")) {
		t.Fatal("lookup changed ledger")
	}
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: q.PreferenceRecord.Memory, Dataset: q.PreferenceRecord.Dataset, ExpectedRevision: 1, Text: "New preference version"})
	mustMerge(t, e)
	page, e = s.ProjectPreferenceDecisions(mergeCtx, lookup)
	mustMerge(t, e)
	if len(page.Decisions) != 0 {
		t.Fatal("historical preference promoted to current", page)
	}
	_, e = s.DB.Exec(`INSERT INTO derived_memories(memory,revision,state) VALUES(?,1,'stale')`, q.ProjectRecord.Memory)
	mustMerge(t, e)
	if _, e = s.ProjectPreferenceDecisions(mergeCtx, lookup); e == nil {
		t.Fatal("stale project target became known empty")
	}
	mustMerge(t, s.Close())
	if _, e = s.ProjectPreferenceDecisions(mergeCtx, lookup); e == nil {
		t.Fatal("query failure became known empty")
	}
}

func TestProjectPreferenceDecisionsRejectInactivePreferenceAndRestore(t *testing.T) {
	for _, state := range []string{"deleted", "stale", "moved", "retired", "raw"} {
		t.Run(state, func(t *testing.T) {
			s, q, r := preferenceFixture(t)
			_, e := s.ConfirmProjectPreference(mergeCtx, r)
			mustMerge(t, e)
			lookup := records.ProjectPreferenceDecisionsQuery{Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: q.Datasets}
			switch state {
			case "deleted":
				_, e = s.Apply(mergeCtx, Mutation{Kind: "delete", OperationID: uuid.NewString(), Memory: q.PreferenceRecord.Memory, Dataset: q.PreferenceRecord.Dataset, ExpectedRevision: 1})
			case "stale":
				_, e = s.DB.Exec(`INSERT INTO derived_memories(memory,revision,state) VALUES(?,1,'stale')`, q.PreferenceRecord.Memory)
			case "moved":
				_, e = s.DB.Exec(`UPDATE memories SET dataset=? WHERE id=?`, q.Project, q.PreferenceRecord.Memory)
			case "retired":
				_, e = s.DB.Exec(`UPDATE project_preference_decisions SET state='retired'`)
			case "raw":
				_, e = s.DB.Exec(`UPDATE memory_metadata SET origin='raw' WHERE memory=?`, q.PreferenceRecord.Memory)
			}
			mustMerge(t, e)
			page, e := s.ProjectPreferenceDecisions(mergeCtx, lookup)
			mustMerge(t, e)
			if len(page.Decisions) != 0 {
				t.Fatal("invalid pair exposed as current", state, page)
			}
		})
	}
}

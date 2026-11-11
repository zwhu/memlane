package store

import (
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"reflect"
	"testing"
)

func TestDeletionImpactRealDirectLineageAndScope(t *testing.T) {
	s, _ := mergeDB(t)
	_, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	task, _, raw := organizationFixture(t, s)
	stageOrganizationFixture(t, s, task, raw)
	summary, e := s.ApplyOrganizationItem(mergeCtx, task.ID, 1, 1)
	mustMerge(t, e)
	source := task.Snapshot.Inputs[0]
	copied, e := s.CopyExperience(mergeCtx, records.ExperienceCopy{OperationID: uuid.NewString(), Project: task.Project, Memory: source.Memory, Revision: source.Revision})
	mustMerge(t, e)
	cv, e := s.Current(mergeCtx, copied.Memory)
	mustMerge(t, e)
	// A second persisted lineage table can describe the very same tuple.
	_, e = s.DB.Exec(`INSERT INTO merge_sources SELECT memory,revision,source_memory,source_revision,snapshot,quote FROM organization_sources WHERE memory=? AND source_memory=?`, summary.Receipt.Memory, source.Memory)
	mustMerge(t, e)
	q := records.DeletionImpactQuery{Memory: source.Memory, Dataset: task.Project, ExpectedRevision: source.Revision, Datasets: []string{cv.Dataset, task.Project, task.Project, "nonexistent-scope"}}
	before := map[string][][]any{}
	for _, name := range []string{"versions", "memories", "derived_memories", "organization_sources", "jobs"} {
		before[name] = sqlRows(t, s.DB, name)
	}
	impact, e := s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if impact.DirectMemoryCount != 2 || impact.ReferenceCount != 2 || len(impact.References) != 2 || len(impact.CoverageDatasets) != 2 || impact.Truncated {
		t.Fatal(impact)
	}
	for _, r := range impact.References {
		if r.SourceRevision != source.Revision || r.Revision != 1 {
			t.Fatal(r)
		}
		if r.Memory == summary.Receipt.Memory && !reflect.DeepEqual(r.Relations, []string{"merge", "organization"}) {
			t.Fatal("cross-table tuple not deduplicated", r)
		}
		if r.Memory == copied.Memory && !reflect.DeepEqual(r.Relations, []string{"experience_copy"}) {
			t.Fatal(r)
		}
	}
	for name, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, s.DB, name)) {
			t.Fatal("read changed knowledge/review/index state", name)
		}
	}
	limited := q
	limited.Datasets = []string{task.Project}
	p, e := s.DeletionImpact(mergeCtx, limited)
	mustMerge(t, e)
	if p.ReferenceCount != 1 || p.DirectMemoryCount != 1 || len(p.CoverageDatasets) != 1 {
		t.Fatal("scope leak", p)
	}
	// User edit preserves the real current lineage; old revisions are not extra references.
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: summary.Receipt.Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "user confirmed summary"})
	mustMerge(t, e)
	p, e = s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 2 {
		t.Fatal("historical lineage overcounted", p)
	}
	for _, bad := range []records.DeletionImpactQuery{{Memory: q.Memory, Dataset: q.Dataset, ExpectedRevision: 2, Datasets: q.Datasets}, {Memory: q.Memory, Dataset: cv.Dataset, ExpectedRevision: 1, Datasets: q.Datasets}, {Memory: q.Memory, Dataset: q.Dataset, ExpectedRevision: 1, Datasets: []string{cv.Dataset}}, {Memory: q.Memory, Dataset: q.Dataset, ExpectedRevision: 0, Datasets: q.Datasets}} {
		if _, e = s.DeletionImpact(mergeCtx, bad); e == nil {
			t.Fatal("unbound target accepted", bad)
		}
	}
	// Changing source version invalidates the old target receipt rather than returning zero.
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: source.Memory, Dataset: task.Project, ExpectedRevision: 1, Text: "new source version"})
	mustMerge(t, e)
	if _, e = s.DeletionImpact(mergeCtx, q); e == nil {
		t.Fatal("old source CAS accepted")
	}
	q.ExpectedRevision = 2
	p, e = s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 2 {
		t.Fatal("references to older source version hidden", p)
	}
	// Logical deletion is distinct from deleting referenced knowledge. Restoring
	// a dependent creates a new version and reinstates its actual current links.
	deleted, e := s.Apply(mergeCtx, Mutation{Kind: "delete", OperationID: uuid.NewString(), Memory: summary.Receipt.Memory, Dataset: task.Project, ExpectedRevision: 2})
	mustMerge(t, e)
	p, e = s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 1 {
		t.Fatal("deleted dependent counted as active", p)
	}
	restored, e := s.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: summary.Receipt.Memory, Dataset: task.Project, ExpectedRevision: deleted.Revision, RestoreRevision: 2})
	mustMerge(t, e)
	if restored.Revision <= deleted.Revision {
		t.Fatal("restore rewound revision", restored)
	}
	p, e = s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 2 {
		t.Fatal("restored current lineage missing", p)
	}
}

func TestDeletionImpactBoundedDetailsAndUnknownIsError(t *testing.T) {
	s, _ := mergeDB(t)
	task, _, _ := organizationFixture(t, s)
	source := task.Snapshot.Inputs[0]
	q := records.DeletionImpactQuery{Memory: source.Memory, Dataset: task.Project, ExpectedRevision: 1, Datasets: []string{task.Project}}
	p, e := s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 0 || p.References == nil {
		t.Fatal(p)
	}
	for i := 0; i < 53; i++ {
		id := uuid.NewString()
		_, e = s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: id, Dataset: task.Project, Text: "synthetic dependent"})
		mustMerge(t, e)
		_, e = s.DB.Exec(`INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,1,?,1,?,'{}','synthetic')`, id, source.Memory, task.Project)
		mustMerge(t, e)
	}
	p, e = s.DeletionImpact(mergeCtx, q)
	mustMerge(t, e)
	if p.ReferenceCount != 53 || p.DirectMemoryCount != 53 || len(p.References) != 50 || !p.Truncated {
		t.Fatal(p)
	}
	q.Memory = uuid.NewString()
	if _, e = s.DeletionImpact(mergeCtx, q); e == nil {
		t.Fatal("unknown target became known zero")
	}
	mustMerge(t, s.Close())
	if _, e = s.DeletionImpact(mergeCtx, q); e == nil {
		t.Fatal("store failure became known zero")
	}
}

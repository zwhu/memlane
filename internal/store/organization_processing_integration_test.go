package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Storage/selection fixtures, separately from actual pi inference and windows.
func TestMaterialOrganizationFreezesProcessingAcrossAllProposalSchemas(t *testing.T) {
	for _, tc := range []struct {
		name, schema   string
		review, global bool
		purpose        string
		count          int
	}{
		{"summary-tags", records.OrganizationSchema, false, false, "", 2},
		{"review", records.OrganizationReviewSchema, true, false, "", 2},
		{"review-global", records.OrganizationGlobalSchema, true, true, "", 2},
		{"global-only", records.OrganizationGlobalSchema, true, true, "global", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := mergeDB(t)
			_, err := s.Scopes(mergeCtx)
			mustMerge(t, err)
			base, _, _ := organizationFixture(t, s)
			first, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("material-first")}, processingTime)
			mustMerge(t, err)
			dependency := records.MergeDigest("material-first-dependency")
			processingVerifiedFixture(t, s, first.ID, dependency)
			_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: first.ID}, dependency, processingTime)
			mustMerge(t, err)
			d := records.OrganizationStart{OperationID: uuid.NewString(), Project: base.Project, Review: tc.review, Global: tc.global, Purpose: tc.purpose, Processing: &records.ProcessingTaskAuthorization{Revision: first.ID, Generation: 1}}
			for _, input := range base.Snapshot.Inputs[:tc.count] {
				d.Inputs = append(d.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
			}
			before := sqlRows(t, s.DB, "versions")
			task, created, err := s.StartProcessingOrganization(mergeCtx, d, dependency)
			mustMerge(t, err)
			if !created || task.Snapshot.Schema != tc.schema || task.Snapshot.Purpose != tc.purpose || len(task.Snapshot.Inputs) != tc.count || task.Snapshot.Processing == nil || task.Snapshot.Processing.Revision.ID != first.ID || task.Snapshot.Processing.DependencyDigest != dependency || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
				t.Fatal("incorrect task scope, executor or early mutation", task)
			}
			sealed := task.Snapshot
			mustMerge(t, sealed.Seal())
			if sealed.Digest != task.Snapshot.Digest {
				t.Fatal("processing snapshot cannot round-trip")
			}
			second, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: first.ID, Spec: processingProvider("material-second")}, processingTime)
			mustMerge(t, err)
			secondDep := records.MergeDigest("material-second-dependency")
			processingVerifiedFixture(t, s, second.ID, secondDep)
			_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 1}, secondDep, processingTime)
			mustMerge(t, err)
			replayed, found, err := s.ReplayOrganizationStart(mergeCtx, d)
			mustMerge(t, err)
			if !found || replayed.ID != task.ID || replayed.Snapshot.Processing.Revision.ID != first.ID {
				t.Fatal("replay retargeted existing task")
			}
			d.OperationID = uuid.NewString()
			_, _, err = s.StartProcessingOrganization(mergeCtx, d, secondDep)
			if !errors.Is(err, ErrConflict) {
				t.Fatal("new task accepted retired selection", err)
			}
		})
	}
}

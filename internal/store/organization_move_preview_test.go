package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestMoveConsentPreviewPersistsOnlyExactTaskProposal(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := reviewOrganizationFixture(t, s, "move")
	p := records.OrganizationPreview{ID: uuid.NewString(), Open: true, Project: task.Project, Kind: "knowledge", TaskID: task.ID, Candidate: d.Item, Review: true, MoveAuthorizations: map[string]string{d.Item: task.ID + ":" + d.Digest}}
	for _, input := range task.Snapshot.Inputs {
		p.Inputs = append(p.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	for _, scope := range task.Snapshot.Destinations {
		p.Destinations = append(p.Destinations, scope.ID)
	}
	head, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	head, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p})
	mustMerge(t, err)
	read, err := s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Preview == nil || read.Preview.MoveAuthorizations[d.Item] != task.ID+":"+d.Digest {
		t.Fatal("scope consent draft was not retained")
	}
	p.MoveAuthorizations[d.Item] = "different-task:" + d.Digest
	_, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p})
	if err == nil {
		t.Fatal("preview accepted consent from another task")
	}
	p.MoveAuthorizations[d.Item] = task.ID + ":" + records.MergeDigest("different-proposal")
	_, err = s.SaveOrganizationPreview(mergeCtx, records.OrganizationPreviewWrite{Identity: head.Identity, Generation: head.Generation, Operation: uuid.NewString(), Preview: p})
	if err == nil {
		t.Fatal("preview accepted consent for another proposal")
	}
	read, err = s.OrganizationPreview(mergeCtx)
	mustMerge(t, err)
	if read.Preview.MoveAuthorizations[d.Item] != task.ID+":"+d.Digest {
		t.Fatal("invalid write changed previous consent")
	}
}

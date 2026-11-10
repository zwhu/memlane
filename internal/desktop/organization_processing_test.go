package desktop

import (
	"context"
	"testing"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestOrganizationProcessingReadDoesNotEnableAndRetiresLateLibraryReceipt(t *testing.T) {
	configuration := records.ProcessingConfiguration{Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "pi-config", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "selected-model"}, Validation: &records.ProcessingValidation{State: "succeeded"}}}}
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "processing_get" {
			t.Errorf("chooser performed %s instead of reading", r.Op)
			return serviceipc.Response{OK: false}
		}
		return serviceipc.Response{OK: true, Result: configuration}
	})
	a.visualFixture = false
	a.organization.Epoch = "old-library"
	a.refreshOrganizationProcessing()
	a.organization = OrganizationUI{Epoch: "new-library"}
	awaitExperienceDispatch(t, dispatch)
	if a.organization.ProcessingLoaded || a.organization.Processing.Current.Revision != nil {
		t.Fatal("late processing read crossed library epoch")
	}
	a.organization.Client = serviceipc.OrganizationClient{Root: a.library.Root}
	a.refreshOrganizationProcessing()
	awaitExperienceDispatch(t, dispatch)
	if !a.organization.ProcessingLoaded || !organizationProcessingAvailable(a.organization.Processing) || a.organization.Processing.Current.Revision.Spec.Model != "selected-model" {
		t.Fatal("current ledger not retained")
	}
	a.organization.Processing.Current.Active = false
	if organizationProcessingAvailable(a.organization.Processing) {
		t.Fatal("saved-only selection became available")
	}
}

func TestOrganizationActionCannotAdoptOldLibraryTask(t *testing.T) {
	a := New("test", nil)
	a.libraryContext = context.Background()
	dispatch := make(chan func(), 2)
	a.dispatch = func(fn func()) { dispatch <- fn }
	a.organization.Epoch = "old-library"
	a.organizationAction(func(context.Context) (records.OrganizationTask, error) {
		return records.OrganizationTask{ID: "old-task"}, nil
	}, true)
	a.organization = OrganizationUI{Epoch: "new-library", Selected: "new-task"}
	awaitExperienceDispatch(t, dispatch)
	if a.organization.Selected != "new-task" || a.organization.Task != nil || a.organization.Busy {
		t.Fatal("old task adopted into new library", a.organization)
	}
}

func TestOrganizationReopenPreservesUnknownStartRequest(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	request := &records.OrganizationStart{OperationID: "original-operation", Project: "original-project", Inputs: []records.MergeTarget{{Memory: "original-source", Revision: 3}}}
	a.organization.Start = request
	a.organization.Project = request.Project
	a.beginOrganization()
	if a.organization.Start != request || a.organization.Project != request.Project || request.Inputs[0].Revision != 3 || !a.organization.New || a.page != "整理记录" {
		t.Fatal("reopening created a different request")
	}
}

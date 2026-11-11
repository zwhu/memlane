package desktop

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestOrganizationProcessingRequiresCurrentRuntimeReady(t *testing.T) {
	c := records.ProcessingConfiguration{Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "current", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi"}, Validation: &records.ProcessingValidation{State: "succeeded"}}}}
	for _, state := range []string{"", "dependency_changed", "unavailable", "verification_required", "ready"} {
		c.Runtime = map[string]records.ProcessingReadiness{"current": {State: state}}
		if organizationProcessingAvailable(c) != (state == "ready") {
			t.Errorf("runtime %q incorrectly available", state)
		}
	}
	c.Runtime = map[string]records.ProcessingReadiness{"other": {State: "ready"}}
	if organizationProcessingAvailable(c) {
		t.Fatal("another revision readiness adopted")
	}
}

func TestOrganizationUnavailableRuntimeCannotCreateStart(t *testing.T) {
	for _, state := range []string{"", "dependency_changed", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			a := New("test", nil)
			a.libraryContext = context.Background()
			a.organization.Project = "project"
			a.organization.ProcessingLoaded = true
			a.organization.Chosen = map[string]MemoryItem{"one": {ID: "one", Revision: 1}}
			a.organization.Processing = records.ProcessingConfiguration{Runtime: map[string]records.ProcessingReadiness{"current": {State: state}}, Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "current", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi"}, Validation: &records.ProcessingValidation{State: "succeeded"}}}}
			a.startOrganization()
			if a.organization.Start != nil || a.organization.Busy || len(a.organization.Chosen) != 1 {
				t.Fatal("runtime not ready crossed actual start gate")
			}
		})
	}
}
func TestOrganizationProcessingReadFailureRetiresOldReadyObservation(t *testing.T) {
	a, updates := processingUIFixture(t, func(serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{Code: "unavailable", Error: "synthetic read failure"}
	})
	a.organization.ProcessingLoaded = true
	a.organization.Processing.Current.Active = true
	a.refreshOrganizationProcessing()
	awaitProcessingDispatch(t, updates)
	if a.organization.ProcessingLoaded || a.organization.ProcessingError == "" {
		t.Fatal("failed new read retained current readiness authority")
	}
}

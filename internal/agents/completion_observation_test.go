package agents

import (
	"context"
	"testing"
)

func TestCodexCapabilityObservationActualProtocolFixture(t *testing.T) {
	o, _ := completionFixture(t, "ok")
	x, err := RunCodexCompletion(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if x.Observation == nil || !x.Observation.ResponseComplete || !x.Observation.LocalSchemaValid || !x.Observation.IsolationConfigured || !x.Reaped || !x.GroupGone {
		t.Fatal(x.Observation)
	}
}

func TestCodexCapabilityObservationRejectsActualInvalidSchema(t *testing.T) {
	o, _ := completionFixture(t, "schema")
	x, err := RunCodexCompletion(context.Background(), o)
	if err == nil || x.Observation == nil || !x.Observation.ResponseComplete || x.Observation.LocalSchemaValid {
		t.Fatal(x.Observation, err)
	}
}

package processing

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"testing"
	"time"
)

func TestCapabilityEvidencePersistsRunnerFactsNotAggregate(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "observed"}[observed], func(t *testing.T) {
			o, s, r := ownerFixture(t)
			o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
				x := fixtureCompletion(b)
				if observed {
					x.Observation = &agents.CompletionObservation{ResponseComplete: true, LocalSchemaValid: true, IsolationConfigured: true, IsolationPolicy: "pi_private_no_session_no_tools_v1"}
				}
				return x, nil
			}
			v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			done := waitValidation(t, s, v.ID)
			if done.State != "succeeded" {
				t.Fatal(done)
			}
			if !observed {
				if done.Evidence != nil {
					t.Fatal("aggregate invented proof")
				}
				return
			}
			e := done.Evidence
			if e == nil || e.ValidationID != v.ID || e.Revision != v.Revision || e.Attempt != v.Attempt || e.DependencyDigest != v.DependencyDigest || !e.StructureValid || !e.ResponseComplete || !e.IsolationConfigured {
				t.Fatal(e)
			}
		})
	}
}

func TestCapabilityEvidenceTimeoutDoesNotSealSuccess(t *testing.T) {
	o, s, r := ownerFixture(t)
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		x := fixtureCompletion(b)
		x.Observation = &agents.CompletionObservation{ResponseComplete: true, LocalSchemaValid: true, IsolationConfigured: true, IsolationPolicy: "pi_private_no_session_no_tools_v1"}
		return x, context.DeadlineExceeded
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := waitValidation(t, s, v.ID)
	if done.State != "failed" || done.ErrorCode != "model_timeout" || done.Evidence != nil {
		t.Fatal(done)
	}
	_ = time.Second
}

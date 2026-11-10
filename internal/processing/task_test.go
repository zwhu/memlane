package processing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestTaskBindingUsesFrozenRevisionAndRejectsChangedDependencies(t *testing.T) {
	o, db, r := ownerFixture(t)
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		return fixtureCompletion(b), nil
	}
	v, e := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if e != nil {
		t.Fatal(e)
	}
	terminal := waitValidation(t, db, v.ID)
	_, e = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID})
	if e != nil {
		t.Fatal(e)
	}
	config, e := db.ProcessingConfiguration(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	frozen := records.ProcessingSnapshot{Revision: *config.Current.Revision, SelectionGeneration: config.Current.Generation, DependencyDigest: terminal.DependencyDigest, FrozenMS: time.Now().UnixMilli()}
	newer := r.Spec
	newer.Model = "different-model"
	second, e := db.SaveProcessing(context.Background(), records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: r.ID, Spec: newer}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	v, e = o.Begin(context.Background(), uuid.NewString(), second.ID)
	if e != nil {
		t.Fatal(e)
	}
	waitValidation(t, db, v.ID)
	_, e = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 1})
	if e != nil {
		t.Fatal(e)
	}
	b, e := o.ResolveTask(context.Background(), frozen)
	if e != nil || b.Spec.Model != r.Spec.Model {
		t.Fatal("configuration switch retargeted frozen task", b, e)
	}
	if _, e = o.ResolveCurrentTask(context.Background(), records.ProcessingTaskAuthorization{Revision: r.ID, Generation: 1}); !errors.Is(e, records.ErrConflict) {
		t.Fatal("new task accepted old selection", e)
	}
	o.Resolve = func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (PiBinding, error) {
		return PiBinding{Spec: spec, Dependency: records.MergeDigest("changed runtime")}, nil
	}
	if _, e = o.ResolveTask(context.Background(), frozen); e == nil {
		t.Fatal("dependency change retained execution permission")
	}
}

package processing

// These tests use actual SQLite and explicit model protocol fixtures. They do
// not execute pi, use cloud authentication or establish backend capability.
import (
	"context"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func ownerFixture(t *testing.T) (*Owner, *store.Store, records.ProcessingRevision) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	scopes, err := s.Scopes(context.Background())
	if err != nil || len(scopes) == 0 {
		t.Fatal(err)
	}
	c, err := s.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "pi", Name: "explicit model fixture", AllowedScopes: []string{scopes[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.SaveProcessing(context.Background(), records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: c.ID, ConnectionGeneration: c.Generation}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o := &Owner{Ledger: s, Lifecycle: context.Background(), Acquire: func(ctx context.Context) (func(), error) {
		return func() {}, ctx.Err()
	}, Resolve: func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (PiBinding, error) {
		return PiBinding{Spec: spec, Dependency: records.MergeDigest("explicit protocol fixture")}, nil
	}}
	t.Cleanup(o.Close)
	return o, s, r
}

func fixtureCompletion(b PiBinding) agents.PiCompletionExecution {
	return agents.PiCompletionExecution{SelectedProvider: b.Spec.Provider, SelectedModel: b.Spec.Model,
		PiRPCProcess: agents.PiRPCProcess{NativeSchemaRequested: true,
			ProposalProcess: agents.ProposalProcess{Started: true, Terminal: true, Reaped: true, GroupGone: true}}}
}

func waitValidation(t *testing.T, s *store.Store, id string) records.ProcessingValidation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.ProcessingValidation(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "validating" {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("validation did not reach its persistent terminal state")
	return records.ProcessingValidation{}
}

func TestValidationOwnerReplayNeverRerunsAndSubmissionDoesNotOwnTask(t *testing.T) {
	o, s, r := ownerFixture(t)
	var calls atomic.Int64
	started, finish := make(chan struct{}), make(chan struct{})
	o.Run = func(ctx context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		calls.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return agents.PiCompletionExecution{}, ctx.Err()
		case <-finish:
			return fixtureCompletion(b), nil
		}
	}
	request, stop := context.WithCancel(context.Background())
	operation := uuid.NewString()
	v, err := o.Begin(request, operation, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	<-started
	replay, err := o.Begin(context.Background(), operation, r.ID)
	if err != nil || replay.ID != v.ID {
		t.Fatal("active replay changed attempt", replay, err)
	}
	close(finish)
	terminal := waitValidation(t, s, v.ID)
	if terminal.State != "succeeded" {
		t.Fatal("submission cancellation killed background task", terminal)
	}
	replay, err = o.Begin(context.Background(), operation, r.ID)
	if err != nil || replay.State != "succeeded" || calls.Load() != 1 {
		t.Fatal("terminal replay reran inference", replay, err, calls.Load())
	}
	configuration, err := s.ProcessingConfiguration(context.Background())
	if err != nil || configuration.Current.Active {
		t.Fatal("validation activated configuration", err)
	}
}

func TestValidationOwnerExplicitActivationUsesFreshDependency(t *testing.T) {
	o, s, r := ownerFixture(t)
	dependency := records.MergeDigest("first dependency")
	o.Resolve = func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (PiBinding, error) {
		return PiBinding{Spec: spec, Dependency: dependency}, nil
	}
	if _, err := o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID}); err == nil {
		t.Fatal("unverified candidate activated")
	}
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		return fixtureCompletion(b), nil
	}
	validation, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal := waitValidation(t, s, validation.ID); terminal.State != "succeeded" {
		t.Fatal("validation did not succeed", terminal)
	}
	receipt, err := o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID, ExpectedGeneration: 0})
	if err != nil || receipt.Revision != r.ID || receipt.Generation != 1 {
		t.Fatal("verified candidate was not explicitly activated", receipt, err)
	}
	configuration, err := s.ProcessingConfiguration(context.Background())
	if err != nil || !configuration.Current.Active || configuration.Current.Revision == nil || configuration.Current.Revision.ID != r.ID {
		t.Fatal("activation did not update the current selection", configuration, err)
	}
	dependency = records.MergeDigest("changed dependency")
	if _, err = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID, ExpectedGeneration: 1}); err == nil {
		t.Fatal("dependency drift reused an old validation")
	}
}

func TestValidationOwnerCancelRejectsLateSuccessAndCloseWaits(t *testing.T) {
	o, s, r := ownerFixture(t)
	started, cleanup, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	o.Run = func(ctx context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		close(started)
		<-ctx.Done()
		close(cleanup)
		<-release
		out := fixtureCompletion(b)
		out.AbortSent, out.AbortAcknowledged = true, true
		return out, nil // explicit ignored-cancellation fixture
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = o.Cancel(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	<-cleanup
	closed := make(chan struct{})
	go func() { o.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned before runner cleanup")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	<-closed
	if terminal := waitValidation(t, s, v.ID); terminal.State != "cancelled" || terminal.ReceiptDigest != "" {
		t.Fatal("late success survived cancellation", terminal)
	}
	status, err := o.Status(context.Background(), v.ID)
	if err != nil || status.Progress == nil || !status.Progress.AbortSent || !status.Progress.AbortAcknowledged || !status.Progress.Reaped || !status.Progress.GroupGone {
		t.Fatal("owned cancellation process observation was lost", status, err)
	}
	if _, err = o.Begin(context.Background(), uuid.NewString(), r.ID); err == nil {
		t.Fatal("closed owner accepted work")
	}
}

func TestValidationOwnerQueueRevocationPreventsInference(t *testing.T) {
	o, s, r := ownerFixture(t)
	queued, release := make(chan struct{}), make(chan struct{})
	o.Acquire = func(ctx context.Context) (func(), error) {
		close(queued)
		select {
		case <-release:
			return func() {}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var calls atomic.Int64
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		calls.Add(1)
		return fixtureCompletion(b), nil
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-queued
	if err = s.Disconnect(context.Background(), r.Spec.Connection); err != nil {
		t.Fatal(err)
	}
	close(release)
	terminal := waitValidation(t, s, v.ID)
	if terminal.Diagnostics == nil || terminal.Diagnostics.Stage != "preflight" || terminal.Diagnostics.ResolveCode != "connection_changed" {
		t.Fatal("resolver failure diagnostic missing", terminal)
	}
	if terminal.State != "failed" || terminal.ErrorCode != "connection_changed" || calls.Load() != 0 {
		t.Fatal("queued revoked connection executed", terminal, calls.Load())
	}
}

func TestValidationOwnerPostInferenceDriftAndCrashRecovery(t *testing.T) {
	o, s, r := ownerFixture(t)
	var changed atomic.Bool
	o.Resolve = func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (PiBinding, error) {
		return PiBinding{Spec: spec, Dependency: records.MergeDigest(changed.Load()), DependencyParts: map[string]string{"settings.json": records.MergeDigest(changed.Load())}}, nil
	}
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		changed.Store(true)
		return fixtureCompletion(b), nil
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal := waitValidation(t, s, v.ID); terminal.State != "failed" || terminal.ErrorCode != "dependency_changed" {
		t.Fatal("dependency drift produced success", terminal)
	}
	status, err := o.Status(context.Background(), v.ID)
	if err != nil || status.Validation.Diagnostics == nil || status.Validation.Diagnostics.Stage != "postflight" || !reflect.DeepEqual(status.Validation.Diagnostics.ChangedNames, []string{"settings.json"}) {
		t.Fatal("postflight changed names lost", status, err)
	}
	configuration, err := s.ProcessingConfiguration(context.Background())
	if err != nil || configuration.Saved["agent"].Validation.Diagnostics == nil {
		t.Fatal("saved configuration lost persisted diagnostics", configuration, err)
	}
	interrupted, err := s.BeginProcessingValidation(context.Background(), uuid.NewString(), r.ID, records.MergeDigest("interrupted fixture"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InterruptProcessingValidations(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if terminal := waitValidation(t, s, interrupted.ID); terminal.State != "failed" || terminal.ErrorCode != "owner_interrupted" {
		t.Fatal("new owner revived interrupted validation", terminal)
	}
}

type finalizationRaceLedger struct {
	*store.Store
	injected atomic.Bool
}

func (s *finalizationRaceLedger) FinishProcessingValidation(ctx context.Context, id string, attempt int64, state, receipt, code string, now time.Time, diagnostics ...records.ProcessingValidationDiagnostics) error {
	if s.injected.CompareAndSwap(false, true) {
		// Cancellation wins exactly after the owner's read, before SQL CAS.
		if err := s.RequestProcessingValidationCancel(ctx, id); err != nil {
			return err
		}
	}
	return s.Store.FinishProcessingValidation(ctx, id, attempt, state, receipt, code, now, diagnostics...)
}

func TestValidationOwnerCancellationAtFinalCASStillPersistsTerminal(t *testing.T) {
	o, s, r := ownerFixture(t)
	o.Ledger = &finalizationRaceLedger{Store: s}
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		return fixtureCompletion(b), nil
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal := waitValidation(t, s, v.ID); terminal.State != "cancelled" || terminal.ReceiptDigest != "" {
		t.Fatal("cancellation CAS left validation running or successful", terminal)
	}
}

func TestValidationOwnerRuntimeDriftRequiresRevalidationAndExplicitReactivation(t *testing.T) {
	o, s, r := ownerFixture(t)
	oldDependency, newDependency := records.MergeDigest("old helper path"), records.MergeDigest("new helper path")
	var dependency atomic.Value
	dependency.Store(oldDependency)
	o.Resolve = func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (PiBinding, error) {
		return PiBinding{Spec: spec, Dependency: dependency.Load().(string)}, nil
	}
	release := make(chan struct{})
	var calls atomic.Int64
	o.Run = func(ctx context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		calls.Add(1)
		if b.Dependency == newDependency {
			select {
			case <-release:
			case <-ctx.Done():
				return agents.PiCompletionExecution{}, ctx.Err()
			}
		}
		return fixtureCompletion(b), nil
	}
	first, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waitValidation(t, s, first.ID).State != "succeeded" {
		t.Fatal("initial validation failed")
	}
	activation, err := o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	dependency.Store(newDependency)
	observed, err := o.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observed.Runtime[r.ID].State != "dependency_changed" || !observed.Current.Active || observed.Current.Generation != activation.Generation || observed.Saved["agent"].Validation.ID != first.ID || observed.Saved["agent"].Validation.State != "succeeded" {
		t.Fatal("read-only drift inspection rewrote ledger or reused old success", observed)
	}
	if calls.Load() != 1 {
		t.Fatal("configuration read executed a model")
	}
	if _, err = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID, ExpectedGeneration: activation.Generation}); err == nil {
		t.Fatal("old success authorized new dependencies")
	}
	second, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Attempt != first.Attempt+1 || second.DependencyDigest != newDependency {
		t.Fatal("revalidation reused old success", second)
	}
	during, err := s.ProcessingConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if during.Current.Active || during.Current.Generation != activation.Generation+1 {
		t.Fatal("revalidation kept prior active authority", during)
	}
	close(release)
	if waitValidation(t, s, second.ID).State != "succeeded" {
		t.Fatal("new runtime did not validate")
	}
	after, err := o.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Current.Active || after.Runtime[r.ID].State != "ready" {
		t.Fatal("success implicitly reactivated selection", after)
	}
	if _, err = o.ResolveCurrentTask(context.Background(), records.ProcessingTaskAuthorization{Revision: r.ID, Generation: after.Current.Generation}); err == nil {
		t.Fatal("task ran before explicit reactivation")
	}
	if _, err = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID, ExpectedGeneration: activation.Generation}); err == nil {
		t.Fatal("old activation generation accepted")
	}
	reactivated, err := o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: r.ID, ExpectedGeneration: after.Current.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Generation != after.Current.Generation+1 {
		t.Fatal("explicit reactivation did not advance authority", reactivated)
	}
	old, err := s.ProcessingValidation(context.Background(), first.ID)
	if err != nil || old.State != "succeeded" || old.DependencyDigest != oldDependency {
		t.Fatal("historical success was overwritten", old, err)
	}
}

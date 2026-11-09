package processing

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type Ledger interface {
	Connection(context.Context, string) (records.Connection, error)
	ProcessingConfiguration(context.Context) (records.ProcessingConfiguration, error)
	ProcessingRevision(context.Context, string) (records.ProcessingRevision, error)
	ProcessingValidation(context.Context, string) (records.ProcessingValidation, error)
	BeginProcessingValidation(context.Context, string, string, string, time.Time) (records.ProcessingValidation, error)
	RequestProcessingValidationCancel(context.Context, string) error
	FinishProcessingValidation(context.Context, string, int64, string, string, string, time.Time) error
}

type validationRun struct {
	revision string
	cancel   context.CancelFunc
	progress records.ProcessingValidationProgress
}

// Owner lives in the daemon, sharing its lifecycle and actual execution slot.
// A frontend request context owns only submission, never the background probe.
type Owner struct {
	Ledger                          Ledger
	Lifecycle                       context.Context
	ClientRoot, ServiceRoot, Helper string
	Acquire                         func(context.Context) (func(), error)
	Resolve                         func(context.Context, string, string, string, records.ProcessingSpec) (PiBinding, error)
	Run                             func(context.Context, PiBinding, func(int) error, func(string)) (agents.PiCompletionExecution, error)
	Poison                          func()
	mu                              sync.Mutex
	wg                              sync.WaitGroup
	closed                          bool
	runs                            map[string]validationRun
}

func (o *Owner) resolve(ctx context.Context, spec records.ProcessingSpec) (PiBinding, error) {
	if spec.Mode != "agent" || spec.Agent != "pi" {
		return PiBinding{}, records.MergeError("unsupported_processing")
	}
	c, err := o.Ledger.Connection(ctx, spec.Connection)
	if err != nil || !c.Enabled || c.Agent != spec.Agent || c.Generation != spec.ConnectionGeneration {
		return PiBinding{}, records.MergeError("connection_changed")
	}
	if o.Resolve != nil {
		return o.Resolve(ctx, o.ClientRoot, o.ServiceRoot, o.Helper, spec)
	}
	return ResolvePi(ctx, o.ClientRoot, o.ServiceRoot, o.Helper, spec)
}

func (o *Owner) Begin(ctx context.Context, operation, revision string) (records.ProcessingValidation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out records.ProcessingValidation
	if o.closed || o.Lifecycle == nil || o.Lifecycle.Err() != nil {
		return out, records.MergeError("service_stopping")
	}
	v, err := o.Ledger.ProcessingRevision(ctx, revision)
	if err != nil {
		return out, err
	}
	configuration, err := o.Ledger.ProcessingConfiguration(ctx)
	if err != nil {
		return out, err
	}
	if configuration.Saved[v.Spec.Mode].ID != revision {
		return out, records.ErrConflict
	}
	binding, err := o.resolve(ctx, v.Spec)
	if err != nil {
		return out, err
	}
	// Begin's operation replay is checked before deciding to launch. A repeat
	// of an old terminal operation must return its original probe, not rerun it.
	out, err = o.Ledger.BeginProcessingValidation(ctx, operation, revision, binding.Dependency, time.Now())
	if err != nil {
		return out, err
	}
	out, err = o.Ledger.ProcessingValidation(ctx, out.ID)
	if err != nil || out.State != "validating" {
		return out, err
	}
	if _, live := o.runs[out.ID]; live {
		return out, nil
	}
	for _, active := range o.runs {
		if active.revision == revision {
			active.cancel()
		}
	}
	if o.runs == nil {
		o.runs = make(map[string]validationRun)
	}
	runCtx, cancel := context.WithTimeout(o.Lifecycle, 2*time.Minute)
	o.runs[out.ID] = validationRun{revision: revision, cancel: cancel, progress: records.ProcessingValidationProgress{Events: map[string]int{}}}
	o.wg.Add(1)
	go o.execute(runCtx, cancel, out, binding)
	return out, nil
}

func (o *Owner) execute(ctx context.Context, cancel context.CancelFunc, validation records.ProcessingValidation, binding PiBinding) {
	defer o.wg.Done()
	defer cancel()
	defer func() {
		o.mu.Lock()
		delete(o.runs, validation.ID)
		o.mu.Unlock()
	}()
	state, receipt, code := "failed", "", "runner_failed"
	defer func() {
		persist, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for attempt := 0; attempt < 2; attempt++ {
			latest, err := o.Ledger.ProcessingValidation(persist, validation.ID)
			if err != nil {
				break
			}
			if latest.State != "validating" {
				return // a newer attempt superseded this probe
			}
			if latest.CancelRequested {
				state, receipt, code = "cancelled", "", ""
			}
			err = o.Ledger.FinishProcessingValidation(persist, validation.ID, validation.Attempt, state, receipt, code, time.Now())
			if err == nil {
				return
			}
			if !errors.Is(err, records.ErrConflict) {
				break
			}
		}
		// An unpersisted terminal state is unknown. Stop the owner so restart
		// reconciliation records interruption; never leave a false success.
		if o.Poison != nil {
			o.Poison()
		}
	}()
	if o.Acquire == nil {
		code = "execution_slot_unavailable"
		return
	}
	release, err := o.Acquire(ctx)
	if err != nil {
		code = "cancelled_or_timeout"
		return
	}
	defer release()
	// The queue can be long; recheck dependencies before incurring model cost.
	actual, err := o.resolve(ctx, binding.Spec)
	if err != nil || actual.Dependency != binding.Dependency {
		code = "dependency_changed"
		return
	}
	run := o.Run
	if run == nil {
		run = VerifyPi
	}
	execution, err := run(ctx, binding, func(pid int) error {
		connection, err := o.Ledger.Connection(ctx, binding.Spec.Connection)
		if err != nil || !connection.Enabled || connection.Generation != binding.Spec.ConnectionGeneration {
			return records.MergeError("connection_changed")
		}
		o.mu.Lock()
		active := o.runs[validation.ID]
		active.progress.Started, active.progress.PID = true, pid
		o.runs[validation.ID] = active
		o.mu.Unlock()
		return nil
	}, func(event string) {
		o.mu.Lock()
		active := o.runs[validation.ID]
		active.progress.Events[event]++
		o.mu.Unlock()
	})
	if execution.Started && !execution.GroupGone && o.Poison != nil {
		o.Poison()
	}
	if err != nil || ctx.Err() != nil || !execution.Terminal || !execution.Reaped || !execution.GroupGone ||
		!execution.NativeSchemaRequested || execution.SelectedProvider != binding.Spec.Provider || execution.SelectedModel != binding.Spec.Model {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "model_timeout"
		} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			code = "owner_interrupted"
		}
		return
	}
	actual, err = o.resolve(ctx, binding.Spec)
	if err != nil || actual.Dependency != binding.Dependency {
		code = "dependency_changed"
		return
	}
	state, receipt, code = "succeeded", records.MergeDigest(execution), ""
}

func (o *Owner) Cancel(ctx context.Context, id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.Ledger.RequestProcessingValidationCancel(ctx, id); err != nil {
		return err
	}
	if run, ok := o.runs[id]; ok {
		run.cancel()
	}
	return nil
}

func (o *Owner) Status(ctx context.Context, id string) (records.ProcessingValidationStatus, error) {
	validation, err := o.Ledger.ProcessingValidation(ctx, id)
	if err != nil {
		return records.ProcessingValidationStatus{}, err
	}
	out := records.ProcessingValidationStatus{Validation: validation}
	o.mu.Lock()
	defer o.mu.Unlock()
	if active, ok := o.runs[id]; ok {
		progress := active.progress
		progress.Events = make(map[string]int, len(active.progress.Events))
		for event, count := range active.progress.Events {
			progress.Events[event] = count
		}
		out.Progress = &progress
	}
	return out, nil
}

func (o *Owner) Close() {
	o.mu.Lock()
	o.closed = true
	for _, run := range o.runs {
		run.cancel()
	}
	o.mu.Unlock()
	o.wg.Wait()
}

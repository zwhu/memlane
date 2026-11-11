package processing

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
	FinishProcessingValidation(context.Context, string, int64, string, string, string, time.Time, ...records.ProcessingValidationDiagnostics) error
	ActivateProcessing(context.Context, records.ProcessingActivation, string, time.Time) (records.ProcessingActivationReceipt, error)
}

// Configuration observes current dependencies without mutating durable success
// or selection. Runtime inspection is not an inference/verification attempt.
func (o *Owner) Configuration(ctx context.Context) (records.ProcessingConfiguration, error) {
	config, err := o.Ledger.ProcessingConfiguration(ctx)
	if err != nil {
		return config, err
	}
	config.Runtime = map[string]records.ProcessingReadiness{}
	revisions := map[string]records.ProcessingRevision{}
	for _, v := range config.Saved {
		revisions[v.ID] = v
	}
	if config.Current.Revision != nil {
		v := *config.Current.Revision
		revisions[v.ID] = v
	}
	for id, v := range revisions {
		readiness := records.ProcessingReadiness{State: "verification_required"}
		if v.Validation != nil && v.Validation.State == "succeeded" && !v.Validation.CancelRequested {
			binding, resolveErr := o.resolve(ctx, v.Spec)
			if resolveErr != nil {
				_, code, _ := processingDependencyFailure(PiBinding{}, PiBinding{}, resolveErr)
				readiness = records.ProcessingReadiness{State: "unavailable", ErrorCode: code}
			} else if binding.Dependency != v.Validation.DependencyDigest {
				readiness = records.ProcessingReadiness{State: "dependency_changed", ErrorCode: "dependency_changed"}
			} else {
				readiness.State = "ready"
			}
		}
		config.Runtime[id] = readiness
	}
	return config, nil
}

func (o *Owner) Activate(ctx context.Context, request records.ProcessingActivation) (records.ProcessingActivationReceipt, error) {
	var out records.ProcessingActivationReceipt
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed || o.Lifecycle == nil || o.Lifecycle.Err() != nil {
		return out, records.MergeError("service_stopping")
	}
	revision, err := o.Ledger.ProcessingRevision(ctx, request.Revision)
	if err != nil {
		return out, err
	}
	binding, err := o.resolve(ctx, revision.Spec)
	if err != nil {
		return out, err
	}
	return o.Ledger.ActivateProcessing(ctx, request, binding.Dependency, time.Now())
}

type validationRun struct {
	revision string
	cancel   context.CancelFunc
	progress records.ProcessingValidationProgress
}

// Owner lives in the daemon, sharing its lifecycle and actual execution slot.
// A frontend request context owns only submission, never the background probe.
type Owner struct {
	credentialMu                    sync.Mutex
	Credentials                     CredentialStore
	HTTPComplete                    func(context.Context, HTTPBinding, string, string, json.RawMessage) (HTTPCompletion, error)
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
	cogneeGateways                  map[*CogneeGateway]struct{}
	runs                            map[string]validationRun
	observations                    map[string]records.ProcessingValidationProgress
	observationOrder                []string
}

func (o *Owner) remember(id string, progress records.ProcessingValidationProgress) {
	if o.observations == nil {
		o.observations = make(map[string]records.ProcessingValidationProgress)
	}
	if _, exists := o.observations[id]; !exists {
		o.observationOrder = append(o.observationOrder, id)
	}
	o.observations[id] = progress
	for len(o.observationOrder) > 64 {
		oldest := o.observationOrder[0]
		o.observationOrder = o.observationOrder[1:]
		delete(o.observations, oldest)
	}
}

func (o *Owner) resolve(ctx context.Context, spec records.ProcessingSpec) (PiBinding, error) {
	if spec.Mode == "provider" || spec.Mode == "magpie" {
		return o.resolveHTTP(ctx, spec)
	}
	if spec.Mode != "agent" || (spec.Agent != "pi" && spec.Agent != "codex") {
		return PiBinding{}, records.MergeError("unsupported_processing")
	}
	c, err := o.Ledger.Connection(ctx, spec.Connection)
	if err != nil || !c.Enabled || c.Agent != spec.Agent || c.Generation != spec.ConnectionGeneration {
		return PiBinding{}, records.MergeError("connection_changed")
	}
	if o.Resolve != nil {
		return o.Resolve(ctx, o.ClientRoot, o.ServiceRoot, o.Helper, spec)
	}
	if spec.Agent == "codex" {
		return ResolveCodex(ctx, o.ClientRoot, o.ServiceRoot, o.Helper, spec)
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
		if active, ok := o.runs[validation.ID]; ok {
			o.remember(validation.ID, active.progress)
		}
		delete(o.runs, validation.ID)
		o.mu.Unlock()
	}()
	state, receipt, code := "failed", "", "runner_failed"
	diagnostics := records.ProcessingValidationDiagnostics{Stage: "queue"}
	var evidence *records.ProcessingCapabilityEvidence
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
			if ctx.Err() != nil || code == "model_timeout" || code == "owner_interrupted" {
				evidence = nil
			}
			if latest.CancelRequested {
				evidence = nil
				state, receipt, code = "cancelled", "", ""
			}
			if binding.Spec.Mode == "provider" || binding.Spec.Mode == "magpie" {
				finisher, ok := o.Ledger.(httpValidationLedger)
				if !ok {
					code = "http_receipt_unavailable"
					state = "failed"
					receipt = ""
					err = o.Ledger.FinishProcessingValidation(persist, validation.ID, validation.Attempt, state, receipt, code, time.Now(), diagnostics)
				} else {
					o.mu.Lock()
					observed := o.runs[validation.ID].progress.HTTP
					o.mu.Unlock()
					err = finisher.FinishHTTPProcessingValidation(persist, validation.ID, validation.Attempt, state, receipt, code, time.Now(), diagnostics, observed)
				}
			} else if evidence != nil {
				if finisher, ok := o.Ledger.(agentEvidenceLedger); ok {
					err = finisher.FinishAgentProcessingValidation(persist, validation.ID, validation.Attempt, state, receipt, code, time.Now(), diagnostics, evidence)
				} else {
					err = records.MergeError("evidence_ledger_unavailable")
				}
			} else {
				err = o.Ledger.FinishProcessingValidation(persist, validation.ID, validation.Attempt, state, receipt, code, time.Now(), diagnostics)
			}
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
	diagnostics.Stage = "preflight"
	actual, err := o.resolve(ctx, binding.Spec)
	if err != nil || actual.Dependency != binding.Dependency {
		code, diagnostics.ResolveCode, diagnostics.ChangedNames = processingDependencyFailure(binding, actual, err)
		return
	}
	if binding.Spec.Mode == "provider" || binding.Spec.Mode == "magpie" {
		diagnostics.Stage = "model"
		httpResult, runErr := o.verifyHTTP(ctx, validation.ID, binding)
		if runErr != nil || ctx.Err() != nil || !httpResult.Terminal || !httpResult.Receipt.RequestSent || !httpResult.Receipt.NativeSchemaRequested || !httpResult.Receipt.ResponseComplete || !httpResult.Receipt.SchemaValid {
			code = processingHTTPErrorCode(ctx, runErr)
			return
		}
		receipt = records.MergeDigest(httpResult.Receipt)
	} else {
		run := o.Run
		if run == nil {
			run = VerifyPi
			if binding.Spec.Agent == "codex" {
				run = VerifyCodex
			}
		}
		diagnostics.Stage = "model"
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
		evidence = observedCapability(validation, binding, execution)
		o.mu.Lock()
		active := o.runs[validation.ID]
		active.progress.Terminal = execution.Terminal
		active.progress.Reaped = execution.Reaped
		active.progress.GroupGone = execution.GroupGone
		active.progress.NativeSchemaRequested = execution.NativeSchemaRequested
		active.progress.AbortSent = execution.AbortSent
		active.progress.AbortAcknowledged = execution.AbortAcknowledged
		o.runs[validation.ID] = active
		o.mu.Unlock()
		if execution.Started && !execution.GroupGone && o.Poison != nil {
			o.Poison()
		}
		if err != nil || ctx.Err() != nil || !execution.Terminal || !execution.Reaped || !execution.GroupGone ||
			!execution.NativeSchemaRequested || execution.SelectedProvider != binding.Spec.Provider || execution.SelectedModel != binding.Spec.Model ||
			binding.Spec.Agent == "codex" && !CodexCompletionConfirmed(execution, binding) {
			if binding.Spec.Agent == "codex" && err != nil {
				code = agents.CodexCompletionErrorCode(err)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "model_timeout"
			} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				code = "owner_interrupted"
			}
			return
		}
		receipt = records.MergeDigest(execution)
	}
	diagnostics.Stage = "postflight"
	actual, err = o.resolve(ctx, binding.Spec)
	if err != nil || actual.Dependency != binding.Dependency {
		code, diagnostics.ResolveCode, diagnostics.ChangedNames = processingDependencyFailure(binding, actual, err)
		return
	}
	state, code = "succeeded", ""
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
		if progress.HTTP != nil {
			copy := *progress.HTTP
			progress.HTTP = &copy
		}
		out.Progress = &progress
	} else if observed, ok := o.observations[id]; ok {
		progress := observed
		progress.Events = make(map[string]int, len(observed.Events))
		for event, count := range observed.Events {
			progress.Events[event] = count
		}
		if progress.HTTP != nil {
			copy := *progress.HTTP
			progress.HTTP = &copy
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
	for gateway := range o.cogneeGateways {
		gateway.stop()
	}
	o.mu.Unlock()
	o.wg.Wait()
}

// Diagnostics contain a bounded stage, a trusted error code and dependency
// names only. Neither configuration/credential values nor hashes leave memory.
func processingDependencyFailure(before, after PiBinding, err error) (string, string, []string) {
	if err != nil {
		var access *records.AccessError
		if errors.As(err, &access) {
			return access.Code, access.Code, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "dependency_timeout", "dependency_timeout", nil
		}
		if errors.Is(err, context.Canceled) {
			return "owner_interrupted", "owner_interrupted", nil
		}
		return "dependency_unavailable", "dependency_unavailable", nil
	}
	changed := []string{}
	for name, digest := range before.DependencyParts {
		if after.DependencyParts[name] != digest {
			changed = append(changed, name)
		}
	}
	for name := range after.DependencyParts {
		if _, ok := before.DependencyParts[name]; !ok {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return "dependency_changed", "", changed
}

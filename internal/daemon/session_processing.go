package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/processing"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type sessionProcessingOwner struct {
	server     *Server
	lifecycle  context.Context
	mu         sync.Mutex
	closed     bool
	runs       map[string]context.CancelFunc
	wg         sync.WaitGroup
	capability string
}

func newSessionProcessingOwner(ctx context.Context, s *Server) (*sessionProcessingOwner, error) {
	if err := s.store.InterruptSessionProcessing(ctx); err != nil {
		return nil, err
	}
	o := &sessionProcessingOwner{server: s, lifecycle: ctx, runs: map[string]context.CancelFunc{}, capability: uuid.NewString() + uuid.NewString()}
	// A private host/service capability, rotated on every daemon incarnation.
	// It grants no scope by itself and is never copied into task receipts.
	if err := serviceipc.WriteJSON(filepath.Join(s.config.Root, "coding-memory-owner.json"), map[string]any{"protocol": 1, "socket": serviceipc.SocketPath(s.config.Root), "token": o.capability}); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *sessionProcessingOwner) quiesce() {
	o.mu.Lock()
	o.closed = true
	for _, cancel := range o.runs {
		cancel()
	}
	o.mu.Unlock()
}

func (o *sessionProcessingOwner) Close() {
	o.quiesce()
	o.wg.Wait()
}

func sessionProcessingQuery(q records.SessionProcessingRequest) records.SessionQuery {
	return records.SessionQuery{Project: q.Project, Instance: q.Instance, Dataset: q.Dataset,
		Selected: &records.SessionSummary{Session: q.Session, User: q.User, Dataset: q.Dataset}}
}

func (s *Server) sessionProcessingSource(ctx context.Context, q records.SessionProcessingRequest) (records.CogneeBinding, error) {
	query := sessionProcessingQuery(q)
	b, err := s.sessionBinding(ctx, query)
	if err != nil {
		return b, err
	}
	value, err := s.readOfficialSession(ctx, query, true)
	if err != nil {
		return b, err
	}
	detail, ok := value.(records.SessionDetail)
	if !ok || records.SessionProcessingSourceDigest(detail) != q.ObservedDigest {
		return b, records.MergeError("stale_source")
	}
	fresh, err := s.sessionBinding(ctx, query)
	if err == nil && fresh != b {
		err = records.MergeError("scope_changed")
	}
	return b, err
}

func sessionProcessingCapability(ctx context.Context, b records.CogneeBinding, token string, automatic bool) error {
	var identity struct {
		Instance     string   `json:"instance"`
		Version      string   `json:"cognee_version"`
		Capabilities []string `json:"capabilities"`
		SourceCommit string   `json:"source_commit"`
	}
	if err := official.Request(ctx, b.Endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &identity); err != nil {
		return sessionReadError(err)
	}
	if identity.Instance == b.Instance && identity.Version == "1.6.7" {
		if automatic && !official.CodingMemoryIdentity(identity.SourceCommit, identity.Capabilities) {
			return records.MergeError("session_processing_unavailable")
		}
		for _, capability := range identity.Capabilities {
			if capability == "session_improve_v1" {
				return nil
			}
		}
	}
	return records.MergeError("session_processing_unavailable")
}

func (o *sessionProcessingOwner) Start(ctx context.Context, q records.SessionProcessingRequest) (records.SessionProcessingTask, error) {
	s := o.server
	prior, exists, err := s.store.ReplaySessionProcessing(ctx, q)
	if exists || err != nil {
		return prior, err
	}
	if s.processing == nil {
		return records.SessionProcessingTask{}, records.MergeError("unsupported_processing")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.lifecycle.Err() != nil {
		return records.SessionProcessingTask{}, records.MergeError("service_stopping")
	}
	prior, exists, err = s.store.ReplaySessionProcessing(ctx, q)
	if exists || err != nil {
		return prior, err
	}
	if len(o.runs) != 0 {
		return records.SessionProcessingTask{}, records.MergeError("busy")
	}
	b, err := s.sessionProcessingSource(ctx, q)
	if err != nil {
		return records.SessionProcessingTask{}, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err == nil {
		err = sessionProcessingCapability(ctx, b, token, q.AutomaticGrant != nil)
	}
	if err != nil {
		return records.SessionProcessingTask{}, err
	}
	model, err := s.processing.ResolveCurrentTask(ctx, q.Processing)
	if err != nil {
		return records.SessionProcessingTask{}, err
	}
	if o.lifecycle.Err() != nil {
		return records.SessionProcessingTask{}, records.MergeError("service_stopping")
	}
	s.agentAccess.RLock()
	task, err := s.store.PrepareSessionProcessing(ctx, b, q, model.Dependency)
	s.agentAccess.RUnlock()
	if err != nil || task.State != "prepared" {
		return task, err
	}
	runCtx, cancel := context.WithTimeout(o.lifecycle, 5*time.Minute)
	o.runs[q.Operation] = cancel
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer cancel()
		defer func() { o.mu.Lock(); delete(o.runs, q.Operation); o.mu.Unlock() }()
		o.run(runCtx, cancel, q.Operation)
	}()
	return task, nil
}

func (o *sessionProcessingOwner) Cancel(ctx context.Context, operation string) (records.SessionProcessingTask, error) {
	if err := o.server.store.CancelSessionProcessing(ctx, operation); err != nil {
		return records.SessionProcessingTask{}, err
	}
	o.mu.Lock()
	if cancel := o.runs[operation]; cancel != nil {
		cancel()
	}
	o.mu.Unlock()
	return o.server.store.SessionProcessingTask(ctx, operation)
}

func (o *sessionProcessingOwner) run(ctx context.Context, cancel context.CancelFunc, operation string) {
	s := o.server
	f, err := s.store.LoadSessionProcessing(ctx, operation)
	if err != nil {
		return
	}
	if s.store.ClaimSessionProcessing(ctx, operation, f.Task.Library) != nil {
		return
	}
	state, code := "failed", "session_processing_failed"
	var receipt *records.SessionProcessingReceipt
	var gateway *processing.CogneeGateway
	var serviceToken string
	started, terminal := false, false
	revoked := make(chan struct{}, 1)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-tick.C:
				b, e := s.sessionBinding(monitorCtx, sessionProcessingQuery(f.Task.Request))
				if e == nil && f.Task.Request.AutomaticGrant != nil {
					e = s.store.AuthorizeCodingMemoryGrant(monitorCtx, *f.Task.Request.AutomaticGrant)
				}
				if monitorCtx.Err() != nil {
					return
				}
				if e != nil || b != f.Binding {
					select {
					case revoked <- struct{}{}:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		stopMonitor()
		<-monitorDone
		if ctx.Err() != nil {
			state, code = "cancelled", "cancelled"
			if o.lifecycle.Err() != nil {
				state, code = "interrupted", "service_stopping"
			}
			select {
			case <-revoked:
				state, code = "failed", "scope_denied"
			default:
			}
		}
		if started && !terminal {
			cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
			body, _ := json.Marshal(map[string]string{"instance": f.Binding.Instance, "library": f.Task.Library, "operation": operation, "owner": o.capability})
			var result struct{ Instance, Library, Operation, State string }
			err := official.Request(cleanup, f.Binding.Endpoint, "/api/v1/memlane/session-improve/cancel", "POST", serviceToken, "application/json", body, &result)
			done()
			if err != nil || result.Instance != f.Binding.Instance || result.Library != f.Task.Library || result.Operation != operation || result.State != "stopped" {
				state, code = "interrupted", "session_cleanup_unconfirmed"
			}
		}
		executions := json.RawMessage(`[]`)
		if gateway != nil {
			cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
			err := gateway.Close(cleanup)
			done()
			if err != nil {
				state, code = "interrupted", "executor_reap_failed"
			}
			observations := gateway.Receipts()
			if state == "completed" {
				for _, observation := range observations {
					if !observation.Success {
						state, code = "failed", "model_execution_failed"
					}
				}
			}
			executions, _ = json.Marshal(observations)
			if string(executions) == "null" {
				executions = json.RawMessage(`[]`)
			}
		}
		finish, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		s.agentAccess.RLock()
		defer s.agentAccess.RUnlock()
		if state == "completed" {
			if f.Task.Request.AutomaticGrant != nil && s.store.AuthorizeCodingMemoryGrant(finish, *f.Task.Request.AutomaticGrant) != nil {
				state, code = "failed", "scope_denied"
			}
			bindings, accessErr := s.store.CogneeBindings(finish, f.Binding.Project)
			matched := false
			for _, binding := range bindings {
				matched = matched || binding == f.Binding
			}
			if accessErr == nil {
				accessErr = s.store.AuthorizeCogneeBinding(finish, f.Binding)
			}
			if accessErr != nil || !matched {
				state, code = "failed", "scope_denied"
			}
		}
		if err := s.store.FinishSessionProcessing(finish, operation, f.Task.Library, state, code, receipt, executions); err != nil {
			// Invalid/mismatched completion must still seal a failed operation;
			// never leave a runnable prepared task or adopt a raw foreign reply.
			fallbackState, fallbackCode := "interrupted", "execution_receipt_unsealed"
			if current, loadErr := s.store.LoadSessionProcessing(finish, operation); loadErr == nil && current.Task.CancelRequested {
				fallbackState, fallbackCode = "cancelled", "cancelled"
			}
			_ = s.store.FinishSessionProcessing(finish, operation, f.Task.Library, fallbackState, fallbackCode, nil, executions)
		}
	}()
	q := f.Task.Request
	if q.AutomaticGrant != nil && s.store.AuthorizeCodingMemoryGrant(ctx, *q.AutomaticGrant) != nil {
		code = "scope_denied"
		return
	}
	b, err := s.sessionProcessingSource(ctx, q)
	if err != nil || b != f.Binding {
		code = "stale_source"
		return
	}
	serviceToken, err = s.cogneeToken(b.Instance, b.Dataset)
	if err == nil {
		err = sessionProcessingCapability(ctx, b, serviceToken, q.AutomaticGrant != nil)
	}
	if err != nil {
		code = "session_processing_unavailable"
		return
	}
	if _, err = s.processing.ResolveTask(ctx, f.Processing); err != nil {
		code = "dependency_changed"
		return
	}
	gateway, err = s.processing.StartCogneeGateway(ctx, f.Processing)
	if err != nil {
		code = "processing_unavailable"
		return
	}
	body, err := json.Marshal(struct {
		Owner      string                     `json:"owner,omitempty"`
		Automatic  bool                       `json:"automatic,omitempty"`
		Instance   string                     `json:"instance"`
		Library    string                     `json:"library"`
		Operation  string                     `json:"operation"`
		Project    string                     `json:"project"`
		Dataset    string                     `json:"dataset"`
		Session    string                     `json:"session"`
		Processing records.ProcessingSnapshot `json:"processing"`
		Gateway    map[string]string          `json:"gateway"`
	}{o.capability, q.AutomaticGrant != nil, b.Instance, f.Task.Library, operation, q.Project, q.Dataset, q.Session, f.Processing, map[string]string{"endpoint": gateway.Endpoint, "token": gateway.Token}})
	if err != nil || len(body) > 16384 {
		code = "session_request_unbounded"
		return
	}
	var result records.SessionProcessingReceipt
	started = true
	if err = official.Request(ctx, b.Endpoint, "/api/v1/memlane/session-improve", "POST", serviceToken, "application/json", body, &result); err != nil {
		return
	}
	terminal = true
	receipt = &result
	if result.Status != "completed" && result.Status != "skipped" {
		code = "official_stage_failed"
		return
	}
	if _, err = s.processing.ResolveTask(ctx, f.Processing); err != nil {
		code = "dependency_changed"
		return
	}
	fresh, err := s.sessionProcessingSource(ctx, q)
	if err != nil || fresh != b {
		code = "source_changed"
		return
	}
	state, code = "completed", ""
}

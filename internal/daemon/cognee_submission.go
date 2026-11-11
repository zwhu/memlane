package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// The daemon owns the task lifecycle. IPC cancellation never detaches a paid
// executor; restart records interruption and requires a new explicit selection.
type cogneeSubmissionOwner struct {
	server    *Server
	lifecycle context.Context
	mu        sync.Mutex
	closed    bool
	runs      map[string]context.CancelFunc
	wg        sync.WaitGroup
}

func newCogneeSubmissionOwner(ctx context.Context, s *Server) (*cogneeSubmissionOwner, error) {
	if err := s.store.InterruptCogneeSubmissions(ctx); err != nil {
		return nil, err
	}
	return &cogneeSubmissionOwner{server: s, lifecycle: ctx, runs: map[string]context.CancelFunc{}}, nil
}
func (o *cogneeSubmissionOwner) Close() {
	o.quiesce()
	o.wg.Wait()
}

func (o *cogneeSubmissionOwner) quiesce() {
	o.mu.Lock()
	o.closed = true
	for _, cancel := range o.runs {
		cancel()
	}
	o.mu.Unlock()
}
func (o *cogneeSubmissionOwner) Start(ctx context.Context, q records.CogneeSubmissionRequest) (records.CogneeSubmissionTask, error) {
	s := o.server
	s.agentAccess.RLock()
	replay, exists, replayErr := s.store.ReplayCogneeSubmission(ctx, q)
	s.agentAccess.RUnlock()
	if replayErr != nil {
		return records.CogneeSubmissionTask{}, replayErr
	}
	if exists {
		return replay, nil
	}
	// Reserve the single owner slot before creating a durable task. A
	// rejected busy start cannot overshadow the active task in project latest.
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.lifecycle.Err() != nil {
		return records.CogneeSubmissionTask{}, records.MergeError("service_stopping")
	}
	s.agentAccess.RLock()
	replay, exists, replayErr = s.store.ReplayCogneeSubmission(ctx, q)
	s.agentAccess.RUnlock()
	if replayErr != nil {
		return records.CogneeSubmissionTask{}, replayErr
	}
	if exists {
		return replay, nil
	}
	if len(o.runs) >= 1 {
		return records.CogneeSubmissionTask{}, records.MergeError("busy")
	}
	binding, err := s.processing.ResolveCurrentTask(ctx, q.Processing)
	if err != nil {
		return records.CogneeSubmissionTask{}, err
	}
	s.agentAccess.RLock()
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	var selected records.CogneeBinding
	count := 0
	for _, b := range bindings {
		if b.Dataset == q.Dataset && b.MaterialSubmission {
			selected = b
			count++
		}
	}
	if err == nil && count != 1 {
		err = &records.AccessError{Code: "cognee_authorization_required", Message: "explicit material dataset authorization required"}
	}
	if err == nil {
		_, err = s.store.PrepareCogneeSubmission(ctx, selected, q, binding.Dependency)
	}
	s.agentAccess.RUnlock()
	if err != nil {
		return records.CogneeSubmissionTask{}, err
	}
	task, err := s.store.CogneeSubmissionTask(ctx, q.Operation)
	if err != nil {
		return task, err
	}
	if task.State != "prepared" {
		return task, nil
	}
	runCtx, cancel := context.WithTimeout(o.lifecycle, 5*time.Minute)
	o.runs[q.Operation] = cancel
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer cancel()
		defer func() { o.mu.Lock(); delete(o.runs, q.Operation); o.mu.Unlock() }()
		o.run(runCtx, q.Operation)
	}()
	return task, nil
}
func (o *cogneeSubmissionOwner) Cancel(ctx context.Context, id string) (records.CogneeSubmissionTask, error) {
	if err := o.server.store.CancelCogneeSubmission(ctx, id); err != nil {
		return records.CogneeSubmissionTask{}, err
	}
	o.mu.Lock()
	if cancel := o.runs[id]; cancel != nil {
		cancel()
	}
	o.mu.Unlock()
	return o.server.store.CogneeSubmissionTask(ctx, id)
}
func (o *cogneeSubmissionOwner) run(ctx context.Context, id string) {
	s := o.server
	b, materials, err := s.store.LoadCogneeSubmission(ctx, id)
	if err != nil || len(materials) == 0 {
		return
	}
	library := materials[0].Library
	if err = s.store.ClaimCogneeSubmission(ctx, id, library); err != nil {
		return
	}
	state, code := "failed", "material_processing_failed"
	defer func() {
		if ctx.Err() != nil && code != "material_cleanup_unconfirmed" && code != "executor_reap_failed" {
			state, code = "cancelled", "cancelled"
			if o.lifecycle.Err() != nil {
				state, code = "interrupted", "service_stopping"
			}
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.store.FinishCogneeSubmission(finishCtx, id, library, state, code)
	}()
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		code = "cognee_authorization_required"
		return
	}
	var identity struct {
		Instance     string   `json:"instance"`
		Version      string   `json:"cognee_version"`
		Capabilities []string `json:"capabilities"`
	}
	if err = readCognee(ctx, b.Endpoint, "/api/v1/memlane/identity", token, &identity); err != nil {
		code = "cognee_unavailable"
		return
	}
	compatible := false
	for _, capability := range identity.Capabilities {
		if capability == "material_submission_v1" {
			compatible = true
		}
	}
	if identity.Instance != b.Instance || identity.Version != "1.6.7" || !compatible {
		code = "material_submission_unavailable"
		return
	}
	gateway, err := s.processing.StartCogneeGateway(ctx, materials[0].Processing)
	if err != nil {
		code = "processing_unavailable"
		return
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if gateway.Close(closeCtx) != nil {
			state, code = "failed", "executor_reap_failed"
		}

		receipts := gateway.Receipts()
		if state == "completed" {
			if len(receipts) == 0 {
				state, code = "failed", "model_execution_unconfirmed"
			}
			for _, receipt := range receipts {
				if !receipt.Success {
					state, code = "failed", "model_execution_failed"
				}
			}
		}
		raw, _ := json.Marshal(receipts)
		if s.store.SealCogneeSubmissionExecutions(closeCtx, id, library, raw) != nil {
			state, code = "interrupted", "execution_receipt_unsealed"
		}
	}()
	pythonStarted := false
	defer func() {
		if state == "completed" || !pythonStarted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		body, _ := json.Marshal(map[string]string{"instance": b.Instance, "library": library, "operation": id})
		var stopped struct{ Instance, Library, Operation, State string }
		cleanupErr := official.Request(cleanupCtx, b.Endpoint, "/api/v1/memlane/material-submission/cancel", "POST", token, "application/json", body, &stopped)
		if cleanupErr != nil || stopped.Instance != b.Instance || stopped.Library != library || stopped.Operation != id || stopped.State != "stopped" {
			state, code = "interrupted", "material_cleanup_unconfirmed"
		}
	}()
	for _, material := range materials {
		if ctx.Err() != nil {
			return
		}
		s.agentAccess.RLock()
		err = s.store.AuthorizeCogneeSubmissionBinding(ctx, b)
		s.agentAccess.RUnlock()
		if err != nil {
			code = "scope_denied"
			return
		}
		if _, err = s.processing.ResolveTask(ctx, material.Processing); err != nil {
			code = "dependency_changed"
			return
		}
		current, e := s.store.Current(ctx, material.Memory)
		if e != nil || current.Revision != material.Revision || current.Dataset != material.Project || current.State != "active" || records.MergeDigest(current.Text) != records.MergeDigest(material.Text) {
			code = "stale_source"
			return
		}
		body, _ := json.Marshal(struct {
			Material records.CogneeSubmissionMaterial `json:"material"`
			Gateway  map[string]string                `json:"gateway"`
		}{material, map[string]string{"endpoint": gateway.Endpoint, "token": gateway.Token}})
		var receipt records.CogneeSubmissionReceipt
		pythonStarted = true
		if err = official.Request(ctx, b.Endpoint, "/api/v1/memlane/material-submission", "POST", token, "application/json", body, &receipt); err != nil {
			return
		}
		if _, err = s.processing.ResolveTask(ctx, material.Processing); err != nil {
			code = "dependency_changed"
			return
		}
		s.agentAccess.RLock()
		err = s.store.AuthorizeCogneeSubmissionBinding(ctx, b)
		if err == nil && ctx.Err() == nil {
			err = s.store.CompleteCogneeSubmission(ctx, receipt)
		}
		s.agentAccess.RUnlock()
		if err != nil {
			code = "invalid_provenance"
			return
		}
	}
	state, code = "completed", ""
}

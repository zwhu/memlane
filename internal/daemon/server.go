// Package daemon owns the single business database and supervised worker.
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

type Config = serviceipc.Config
type Request = serviceipc.Request
type Response = serviceipc.Response
type State = serviceipc.State

var ServiceVersion = "0.0.1"
var SocketPath = serviceipc.SocketPath
var Resume = serviceipc.Resume
var MarkStopped = serviceipc.MarkStopped
var writeJSON = serviceipc.WriteJSON

type Server struct {
	proposalGuard        string
	scheduleFailure      string
	organizerOnce        sync.Once
	organizerSlot        chan struct{}
	organizationMu       sync.Mutex
	organizationWG       sync.WaitGroup
	organizationRuns     map[string]organizationOwner
	organizationOutcomes map[string]organizationOutcome
	lifecycle            context.Context
	mergeMu              sync.Mutex
	mergeWG              sync.WaitGroup
	mergeRuns            map[string]context.CancelFunc
	config               Config
	store                *store.Store
	mu                   sync.Mutex
	worker               *engine.Client
	state                State
	status               string
	stop                 context.CancelFunc
	wake                 chan struct{}
	work                 chan struct{}
	gate                 chan struct{}
	agentAccess          sync.RWMutex // scope changes wait for previously authorized tools
}

func Run(ctx context.Context, config Config) error {
	if !filepath.IsAbs(config.Root) {
		return errors.New("explicit absolute service root required")
	}
	if err := os.MkdirAll(config.Root, 0700); err != nil {
		return err
	}
	ownership, err := os.OpenFile(filepath.Join(config.Root, ".service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer ownership.Close()
	if err = syscall.Flock(int(ownership.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("service root already owned")
	}
	statePath := filepath.Join(config.Root, "service-state.json")
	state := State{}
	if data, e := os.ReadFile(statePath); e == nil {
		if e = json.Unmarshal(data, &state); e != nil {
			return errors.New("invalid persisted service state")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// A user stop survives both an App restart and a launchd restart.
	if state.Stopped {
		return nil
	}
	db, err := store.Open(filepath.Join(config.Root, "memlane.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err = prepareOrganizationLeases(ctx, db); err != nil {
		return err
	}
	db.ServiceStopGeneration = state.StopGeneration
	if err = db.CancelRecoveriesForStop(ctx, state.LastStoppedMS, state.StopGeneration); err != nil {
		return err
	}
	if err = db.InterruptMerges(ctx); err != nil {
		return err
	}
	if err = db.InterruptOrganizations(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard, err := os.Executable()
	if err != nil {
		return err
	}
	s := &Server{proposalGuard: guard, organizationRuns: make(map[string]organizationOwner), lifecycle: ctx, mergeRuns: make(map[string]context.CancelFunc), config: config, store: db, state: state, status: "preparing", stop: cancel, wake: make(chan struct{}, 1), work: make(chan struct{}, 1), gate: make(chan struct{}, 1)}
	path := SocketPath(config.Root)
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm() != 0700 || !info.IsDir() {
		return errors.New("IPC directory is not private")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("IPC path is not a socket")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	var handlers sync.WaitGroup
	go func() { <-ctx.Done(); listener.Close() }()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); s.supervise(ctx) }()
	schedulesDone := make(chan struct{})
	go func() { defer close(schedulesDone); s.runSchedules(ctx) }()
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				break
			}
			cancel()
			<-workerDone
			<-schedulesDone
			handlers.Wait()
			s.mergeWG.Wait()
			s.organizationWG.Wait()
			_ = db.InterruptMerges(context.Background())
			if finalErr := s.finalizeOrganizationShutdown(context.Background()); finalErr != nil {
				return errors.Join(e, finalErr)
			}
			return e
		}
		handlers.Add(1)
		go func() { defer handlers.Done(); defer conn.Close(); s.handle(ctx, conn) }()
	}
	<-workerDone
	<-schedulesDone
	handlers.Wait()
	s.mergeWG.Wait()
	s.organizationWG.Wait()
	if err = s.finalizeOrganizationShutdown(context.Background()); err != nil {
		return err
	}
	return db.InterruptMerges(context.Background())
}

func (s *Server) supervise(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		w := s.worker
		s.worker = nil
		s.status = "stopped"
		s.mu.Unlock()
		if w != nil {
			w.Close()
		}
	}()
	for ctx.Err() == nil {
		s.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		recent := []time.Time{}
		for _, t := range s.state.Restarts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		s.state.Restarts = recent
		if len(recent) >= 3 {
			s.status = "failed"
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
			continue
		}
		s.status = "preparing"
		s.mu.Unlock()
		start, stop := context.WithTimeout(ctx, 60*time.Second)
		worker, err := engine.Start(start, s.config.Worker)
		stop()
		if err == nil {
			s.mu.Lock()
			s.worker = worker
			s.status = "ready"
			s.mu.Unlock()
			s.replay(ctx, worker)
			ticker := time.NewTicker(500 * time.Millisecond)
		ready:
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
					return
				case <-worker.Done():
					break ready
				case <-s.wake:
					worker.Close()
					break ready
				case <-s.work:
					s.replay(ctx, worker)
				case <-ticker.C:
					s.replay(ctx, worker)
				}
			}
			ticker.Stop()
			s.mu.Lock()
			s.worker = nil
			s.status = "recovering"
			s.mu.Unlock()
			worker.Close()
		}
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		s.state.Restarts = append(s.state.Restarts, time.Now())
		e := writeJSON(filepath.Join(s.config.Root, "service-state.json"), s.state)
		count := len(s.state.Restarts)
		s.mu.Unlock()
		if e != nil {
			s.stop()
			return
		}
		delay := time.Duration(1<<uint(count-1)) * time.Second
		if s.config.RetryDelayMS > 0 {
			delay = time.Duration(s.config.RetryDelayMS) * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.wake:
			timer.Stop()
		}
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-closed:
		}
	}()
	// Same uid is checked independently of directory/socket permissions.
	if err := serviceipc.SameUser(conn); err != nil {
		return
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	encoder := json.NewEncoder(conn)
	seen := map[string]bool{}
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		if !scanner.Scan() {
			return
		}
		var request Request
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&request)
		if err == nil {
			var trailing any
			if e := decoder.Decode(&trailing); e != io.EOF {
				err = errors.New("one service request per JSONL line required")
			}
		}
		response := Response{Protocol: 1, ID: request.ID}
		if err == nil && (request.Protocol != 1 || request.ID == "" || len(request.ID) > 128 || seen[request.ID] || len(seen) >= 10000) {
			err = errors.New("invalid service protocol or request ID")
		}
		seen[request.ID] = true
		if err == nil {
			response.Result, err = s.perform(ctx, request)
		}
		response.OK = err == nil
		if err != nil {
			response.Error = err.Error()
			var access *records.AccessError
			if errors.As(err, &access) {
				response.Code = access.Code
			} else if errors.Is(err, store.ErrConflict) {
				response.Code = "conflict"
			} else if errors.Is(err, sql.ErrNoRows) {
				response.Code = "not_found"
			} else {
				response.Code = "service_error"
			}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if encoder.Encode(response) != nil {
			return
		}
		if r := request.Op; (r == "stop" || r == "quiesce") && err == nil {
			s.stop()
			return
		}
	}
}

func (s *Server) perform(ctx context.Context, r Request) (any, error) {
	s.mu.Lock()
	worker, status := s.worker, s.status
	s.mu.Unlock()
	operation, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var selected engine.Worker
	if worker != nil {
		selected = &serialWorker{client: worker, gate: s.gate}
	}
	svc := memory.Service{Store: s.store, Engine: selected}
	switch r.Op {
	case "schedules":
		return s.store.Schedules(operation)
	case "schedule_runs":
		return s.store.ScheduleRuns(operation, r.ScheduleID)
	case "schedule_put":
		if !r.UserConfirmed {
			return nil, records.MergeError("confirmation_required")
		}
		if r.ScheduleDraft == nil {
			return nil, records.MergeError("invalid_request")
		}
		return s.putSchedule(operation, *r.ScheduleDraft)
	case "experience_copy":
		if !r.UserConfirmed {
			return nil, records.MergeError("confirmation_required")
		}
		if r.ExperienceCopy == nil {
			return nil, records.MergeError("invalid_request")
		}
		receipt, err := s.store.CopyExperience(operation, *r.ExperienceCopy)
		if err == nil {
			select {
			case s.work <- struct{}{}:
			default:
			}
		}
		return receipt, err
	case "organization_start", "organization_cancel", "organization_resume":
		if !r.UserConfirmed {
			return nil, records.MergeError("confirmation_required")
		}
		if r.Op == "organization_start" {
			if r.OrganizationStart == nil {
				return nil, records.MergeError("invalid_request")
			}
			return s.startOrganization(operation, *r.OrganizationStart)
		}
		if r.Op == "organization_resume" {
			if r.OrganizationResume == nil {
				return nil, records.MergeError("invalid_request")
			}
			return s.resumeOrganization(operation, *r.OrganizationResume)
		}
		return s.cancelOrganization(operation, r.OrganizationTask)
	case "organization_review":
		if !r.UserConfirmed {
			return nil, records.MergeError("confirmation_required")
		}
		if r.OrganizationReview == nil {
			return nil, records.MergeError("invalid_request")
		}
		t, err := s.store.ReviewOrganization(operation, *r.OrganizationReview)
		if err == nil {
			select {
			case s.work <- struct{}{}:
			default:
			}
		}
		return t, err
	case "organization_get":
		return s.getOrganization(operation, r.OrganizationTask)
	case "organization_list":
		q := records.MergeListQuery{}
		if r.OrganizationList != nil {
			q = *r.OrganizationList
		}
		return s.listOrganizations(operation, q)
	case "organization_sources":
		return s.store.OrganizationSources(operation, r.Memory, r.ExpectedRevision)

	case "merge_start", "merge_cancel", "merge_confirm", "merge_reject":
		if !r.UserConfirmed {
			return nil, &records.AccessError{Code: "confirmation_required", Message: "explicit action from trusted Memlane host required"}
		}
		switch r.Op {
		case "merge_start":
			return s.startMerge(operation, r.MergeStart)
		case "merge_cancel":
			return s.cancelMerge(operation, r.MergeTask)
		default:
			t, err := s.store.ReviewMerge(operation, r.MergeReview, r.Op == "merge_confirm")
			if err == nil && t.Receipt != nil {
				select {
				case s.work <- struct{}{}:
				default:
				}
			}
			return t, err
		}
	case "merge_get":
		return s.store.MergeTask(operation, r.MergeTask)
	case "merge_list":
		if r.MergeList == nil {
			return nil, records.MergeError("invalid_request")
		}
		return s.store.MergeList(operation, *r.MergeList)
	case "connections":
		return s.store.Connections(operation)
	case "configure_connection":
		if !r.UserConfirmed {
			return nil, &records.AccessError{Code: "confirmation_required", Message: "connect or change scope explicitly in Memlane"}
		}
		s.agentAccess.Lock()
		defer s.agentAccess.Unlock()
		return s.store.ConfigureConnection(operation, r.Connection)
	case "disconnect":
		if !r.UserConfirmed {
			return nil, &records.AccessError{Code: "confirmation_required", Message: "disconnect explicitly in Memlane"}
		}
		s.agentAccess.Lock()
		defer s.agentAccess.Unlock()
		return nil, s.store.Disconnect(operation, r.ConnectionID)
	case "agent":
		s.agentAccess.RLock()
		defer s.agentAccess.RUnlock()
		result, err := s.agent(operation, r.AgentRequest, &svc)
		if err == nil && r.AgentRequest.Tool == "memory_save" {
			select {
			case s.work <- struct{}{}:
			default:
			}
		}
		return result, err
	case "health":
		pid := 0
		busy := false
		if worker != nil {
			pid = worker.PID()
			var health struct {
				Busy bool `json:"busy"`
			}
			check, stop := context.WithTimeout(operation, time.Second)
			if worker.Call(check, "health", nil, &health) == nil {
				busy = health.Busy
			}
			stop()
		}
		s.mu.Lock()
		scheduleFailure := s.scheduleFailure
		s.mu.Unlock()
		return map[string]any{"service_version": ServiceVersion, "state": status, "worker_pid": pid, "worker_busy": busy, "schedule_error": scheduleFailure}, nil
	case "apply":
		if r.Mutation.Kind != "save" && !r.UserConfirmed {
			return nil, errors.New("knowledge changes require an explicit user confirmation")
		}
		v, err := s.store.Apply(operation, r.Mutation)
		if err != nil {
			return nil, err
		}
		if v.State == "active" {
			select {
			case s.work <- struct{}{}:
			default:
			}
		}
		return v, nil
	case "get":
		return s.store.Current(operation, r.Memory)
	case "scopes":
		return s.store.Scopes(operation)
	case "check_workspace":
		return nil, s.store.CheckBoundWorkspace(operation, r.Scope, r.Project.Path)
	case "register_project":
		if !r.UserConfirmed {
			return nil, errors.New("project registration requires user confirmation")
		}
		return s.store.RegisterProject(operation, r.Project)
	case "list":
		return s.store.List(operation, r.ListQuery)
	case "history":
		return s.store.History(operation, r.Memory, r.BeforeRevision, r.Limit)
	case "activities":
		return s.store.Activities(operation, r.Timezone)
	case "retry_index":
		if err := s.store.Requeue(operation, r.Memory, r.ExpectedRevision); err != nil {
			return nil, err
		}
		if worker == nil {
			s.mu.Lock()
			s.state.Restarts = nil
			err := writeJSON(filepath.Join(s.config.Root, "service-state.json"), s.state)
			s.mu.Unlock()
			if err != nil {
				return nil, err
			}
			select {
			case s.wake <- struct{}{}:
			default:
			}
		} else {
			select {
			case s.work <- struct{}{}:
			default:
			}
		}
		return map[string]bool{"queued": true}, nil
	case "library_search":
		scopes, err := s.store.Scopes(operation)
		if err != nil {
			return nil, err
		}
		datasets := []string{}
		if r.Scope != "" {
			datasets = append(datasets, r.Scope)
		}
		found := r.Scope == ""
		for _, scope := range scopes {
			if scope.ID == r.Scope {
				found = true
			}
			if scope.ID != r.Scope && (r.AllScopes || scope.Kind == "preference" || scope.Kind == "experience") {
				datasets = append(datasets, scope.ID)
			}
		}
		if !found {
			return nil, errors.New("unknown search scope")
		}
		return svc.Search(operation, datasets, r.Query, r.Limit)
	case "index":
		return nil, svc.IndexCurrent(operation, r.Memory)
	case "search":
		return svc.Search(operation, r.Datasets, r.Query, r.Limit)
	case "retry":
		s.mu.Lock()
		s.state.Restarts = nil
		err := writeJSON(filepath.Join(s.config.Root, "service-state.json"), s.state)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return map[string]bool{"retry_requested": true}, nil
	case "stop":
		s.mu.Lock()
		if !s.state.Stopped {
			s.state.StopGeneration++
		}
		if s.state.StopGeneration == 0 {
			s.state.StopGeneration = 1
		}
		s.state.Stopped = true
		s.state.LastStoppedMS = time.Now().UnixMilli()
		cutoff := s.state.LastStoppedMS
		epoch := s.state.StopGeneration
		err := writeJSON(filepath.Join(s.config.Root, "service-state.json"), s.state)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		_ = s.store.CancelRecoveriesForStop(operation, cutoff, epoch)
		return map[string]bool{"stopped": true}, nil
	case "quiesce":
		return map[string]bool{"paused_for_update": true}, nil
	default:
		return nil, errors.New("unsupported service operation")
	}
}

// serialWorker queues service requests before the worker's single operation
// slot; cancellation while waiting never sends a late request to the worker.
type serialWorker struct {
	client *engine.Client
	gate   chan struct{}
}

func (w *serialWorker) Index(ctx context.Context, r engine.Record) (engine.Receipt, error) {
	select {
	case w.gate <- struct{}{}:
	case <-ctx.Done():
		return engine.Receipt{}, ctx.Err()
	}
	defer func() { <-w.gate }()
	return w.client.Index(ctx, r)
}
func (w *serialWorker) Search(ctx context.Context, dataset, query string, k int) ([]engine.Candidate, error) {
	select {
	case w.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-w.gate }()
	return w.client.Search(ctx, dataset, query, k)
}
func (s *Server) replay(ctx context.Context, worker *engine.Client) {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT j.memory FROM jobs j JOIN memories m ON m.id=j.memory AND m.current_revision=j.revision WHERE j.state IN ('pending','failed','indexing') AND m.state='active'`)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	svc := memory.Service{Store: s.store, Engine: &serialWorker{client: worker, gate: s.gate}}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		_ = svc.IndexCurrent(ctx, id)
	}
}

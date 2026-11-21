package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Config configures a Supervisor guarding one store root and its loopback services.
type Config struct {
	StoreRoot               string
	ConfigPath              string
	PhaseOnReady            string
	ServiceURL              string
	CompletionURL           string
	ControlURL              string
	RuntimeConfig           string
	GenerationMode          string
	GenerationReference     string
	LogDir                  string
	LogLimitBytes           int64
	MonitorInterval         time.Duration
	DescendantTrackInterval time.Duration
	ReadyPollInterval       time.Duration
	HealthFailureMax        int
	HealthTimeout           time.Duration
	ExternalOwner           bool
	KeepOnTimeout           bool
	OnCrash                 func(ServiceStatus)
}

// SupervisorSnapshot captures the supervisor's aggregate state and child statuses.
type SupervisorSnapshot struct {
	State    ServiceState    `json:"state"`
	OwnerPID int             `json:"owner_pid"`
	Phase    string          `json:"phase"`
	Services []ServiceStatus `json:"services"`
}

// Supervisor owns an exclusive store lock, ordered service groups, log trimming,
// and debounced loopback health monitoring without automatic restarts.
type Supervisor struct {
	cfg Config

	stopMu        sync.Mutex
	mu            sync.Mutex
	owner         *OwnerLock
	ownerPID      int
	ownerStarted  string
	canonicalCfg  string
	phase         string
	state         ServiceState
	order         []*ManagedProcess
	byRole        map[string]*ManagedProcess
	descendants   map[int]string
	monitorCancel context.CancelFunc
	stopped       bool

	wg       sync.WaitGroup
	exitedCh chan ServiceStatus
}

// New constructs a Supervisor with production-safe defaults.
func New(cfg Config) *Supervisor {
	if cfg.PhaseOnReady == "" {
		cfg.PhaseOnReady = "ready"
	}
	if cfg.LogLimitBytes <= 0 {
		cfg.LogLimitBytes = DefaultLogLimitBytes
	}
	if cfg.MonitorInterval <= 0 {
		cfg.MonitorInterval = time.Second
	}
	if cfg.DescendantTrackInterval <= 0 {
		cfg.DescendantTrackInterval = 10 * time.Second
	}
	if cfg.ReadyPollInterval <= 0 {
		cfg.ReadyPollInterval = 50 * time.Millisecond
	}
	if cfg.HealthFailureMax <= 0 {
		cfg.HealthFailureMax = 3
	}
	if cfg.HealthTimeout <= 0 {
		cfg.HealthTimeout = time.Second
	}
	return &Supervisor{
		cfg:         cfg,
		state:       StatePending,
		byRole:      make(map[string]*ManagedProcess),
		descendants: make(map[int]string),
		exitedCh:    make(chan ServiceStatus, 16),
	}
}

// ExternalOwner reports whether an external child (e.g. scripts/dev.py) owns
// .service-owner and .memlane-dev-owner.json rather than this Supervisor directly.
func (s *Supervisor) ExternalOwner() bool {
	return s.cfg.ExternalOwner
}

// Exited returns a buffered notification channel emitting unexpected child exits.
func (s *Supervisor) Exited() <-chan ServiceStatus {
	return s.exitedCh
}

func (s *Supervisor) buildOwnerRecord(phase string, procs []*ManagedProcess) OwnerRecord {
	children := make([]int, 0, len(procs))
	serviceChildren := make(map[string]int, len(procs))
	for _, proc := range procs {
		if !proc.Exited() {
			children = append(children, proc.PID())
		}
		serviceChildren[proc.Spec().Role] = proc.PID()
	}
	var observed map[string]string
	if len(s.descendants) > 0 {
		observed = make(map[string]string, len(s.descendants))
		for pid, started := range s.descendants {
			observed[strconv.Itoa(pid)] = started
		}
	}
	return OwnerRecord{
		PID:                 s.ownerPID,
		Started:             s.ownerStarted,
		Phase:               phase,
		Config:              s.canonicalCfg,
		OfficialData:        s.owner.StoreRoot(),
		ServiceURL:          s.cfg.ServiceURL,
		CompletionURL:       s.cfg.CompletionURL,
		ProjectControlURL:   s.cfg.ControlURL,
		RuntimeConfig:       s.cfg.RuntimeConfig,
		GenerationMode:      s.cfg.GenerationMode,
		GenerationReference: s.cfg.GenerationReference,
		Children:            children,
		ServiceChildren:     serviceChildren,
		ObservedDescendants: observed,
	}
}

func (s *Supervisor) refreshDescendants(ctx context.Context, procs []*ManagedProcess, persistOwner bool) {
	roots := make([]int, 0, len(procs))
	for _, proc := range procs {
		if pid := proc.PID(); pid > 1 {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	table, err := ProcessTable(probeCtx)
	cancel()
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.descendants == nil {
		s.descendants = make(map[int]string)
	}
	changed := trackDescendantsFromTable(roots, s.descendants, table)
	var lock *OwnerLock
	var rec OwnerRecord
	if changed && persistOwner && !s.stopped && s.owner != nil {
		lock = s.owner
		rec = s.buildOwnerRecord(s.phase, slices.Clone(s.order))
	}
	s.mu.Unlock()
	if lock != nil {
		_ = lock.WriteRecord(rec)
	}
}

func (s *Supervisor) teardownProcesses(ctx context.Context, procs []*ManagedProcess, errPrefix string) (bool, []error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Capture any detached-PGID grandchildren before killing parent process groups
	// severs PPID ancestry.
	s.refreshDescendants(ctx, procs, false)

	var errs []error
	allStopped := true
	var descGrace, descKillWait time.Duration
	for i := len(procs) - 1; i >= 0; i-- {
		proc := procs[i]
		spec := proc.Spec()
		if spec.StopGrace > descGrace {
			descGrace = spec.StopGrace
		}
		if spec.KillWait > descKillWait {
			descKillWait = spec.KillWait
		}
		if err := proc.StopGroup(spec.StopGrace, spec.KillWait); err != nil {
			allStopped = false
			errs = append(errs, fmt.Errorf("%s %s: %w", errPrefix, spec.Role, err))
		}
	}
	if descGrace > 5*time.Second {
		descGrace = 5 * time.Second
	}
	if descKillWait > 5*time.Second {
		descKillWait = 5 * time.Second
	}

	s.mu.Lock()
	descSnapshot := maps.Clone(s.descendants)
	s.descendants = make(map[int]string)
	s.mu.Unlock()

	if len(descSnapshot) > 0 {
		if err := StopTrackedDescendants(ctx, descSnapshot, descGrace, descKillWait); err != nil {
			allStopped = false
			errs = append(errs, fmt.Errorf("%s descendants: %w", errPrefix, err))
		}
	}
	return allStopped, errs
}

func validateSupervisorInputs(cfg Config, specs []ServiceSpec) error {
	for _, origin := range []string{cfg.ServiceURL, cfg.CompletionURL, cfg.ControlURL} {
		if origin == "" {
			continue
		}
		if _, err := ValidateLoopbackURL(origin); err != nil {
			return err
		}
	}
	seenRoles := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if spec.Role == "" {
			return errors.New("service spec requires a role")
		}
		if _, dup := seenRoles[spec.Role]; dup {
			return fmt.Errorf("duplicate service role: %s", spec.Role)
		}
		seenRoles[spec.Role] = struct{}{}
		if spec.BindURL != "" {
			if _, err := ValidateLoopbackURL(spec.BindURL); err != nil {
				return fmt.Errorf("service %s invalid bind_url: %w", spec.Role, err)
			}
		}
		if spec.HealthURL != "" {
			if _, _, _, err := parseLoopbackURL(spec.HealthURL, false); err != nil {
				return fmt.Errorf("service %s invalid health_url: %w", spec.Role, err)
			}
		}
	}
	return nil
}

// Start acquires the canonical owner lock (unless Config.ExternalOwner is set),
// launches each spec sequentially, writes compatible .memlane-dev-owner.json
// metadata when owning the lock, and begins background monitoring.
func (s *Supervisor) Start(ctx context.Context, specs []ServiceSpec) error {
	if err := validateSupervisorInputs(s.cfg, specs); err != nil {
		return err
	}
	canonicalConfig, err := canonicalPathMaybeMissing(s.cfg.ConfigPath)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.owner != nil || len(s.order) > 0 || s.state == StateStarting || s.state == StateReady {
		s.mu.Unlock()
		return errors.New("supervisor already active")
	}
	s.state = StateStarting
	s.stopped = false
	s.canonicalCfg = canonicalConfig
	s.mu.Unlock()

	resetFailedPreLock := func() {
		s.mu.Lock()
		s.state = StateStopped
		s.stopped = true
		s.mu.Unlock()
	}

	var lock *OwnerLock
	var ownerPID int
	var ownerStarted string
	if !s.cfg.ExternalOwner {
		lock, err = AcquireOwner(s.cfg.StoreRoot)
		if err != nil {
			resetFailedPreLock()
			return err
		}
		ownerPID = os.Getpid()
		ownerStarted, err = ProcessStartTime(ctx, ownerPID)
		if err != nil {
			_ = lock.Release()
			resetFailedPreLock()
			return err
		}
	}

	s.stopMu.Lock()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		if lock != nil {
			_ = lock.Release()
		}
		s.stopMu.Unlock()
		return errors.New("supervisor stopped during start")
	}
	s.owner = lock
	s.ownerPID = ownerPID
	s.ownerStarted = ownerStarted
	s.phase = "starting"
	s.order = make([]*ManagedProcess, 0, len(specs))
	s.byRole = make(map[string]*ManagedProcess, len(specs))
	s.descendants = make(map[int]string)
	var initialRec OwnerRecord
	if lock != nil {
		initialRec = s.buildOwnerRecord("starting", nil)
	}
	s.mu.Unlock()

	if lock != nil {
		if err := lock.WriteRecord(initialRec); err != nil {
			_ = lock.RemoveRecord()
			_ = lock.Release()
			s.mu.Lock()
			s.owner = nil
			s.ownerPID = 0
			s.order = nil
			s.byRole = make(map[string]*ManagedProcess)
			s.state = StateStopped
			s.stopped = true
			s.mu.Unlock()
			s.stopMu.Unlock()
			return err
		}
	}
	s.stopMu.Unlock()

	rollback := func(startErr error) error {
		s.stopMu.Lock()
		defer s.stopMu.Unlock()

		s.mu.Lock()
		cancel := s.monitorCancel
		s.monitorCancel = nil
		s.stopped = true
		procs := slices.Clone(s.order)
		heldLock := s.owner
		s.owner = nil
		s.order = nil
		s.byRole = make(map[string]*ManagedProcess)
		s.phase = ""
		s.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		s.wg.Wait()

		var errs []error
		errs = append(errs, startErr)
		allStopped, stopErrs := s.teardownProcesses(context.Background(), procs, "rollback")
		errs = append(errs, stopErrs...)
		if heldLock != nil {
			if allStopped {
				if rmErr := heldLock.RemoveRecord(); rmErr != nil {
					errs = append(errs, rmErr)
				}
			}
			if relErr := heldLock.Release(); relErr != nil {
				errs = append(errs, relErr)
			}
		}

		s.mu.Lock()
		s.ownerPID = 0
		s.state = StateStopped
		s.mu.Unlock()
		return errors.Join(errs...)
	}

	for _, rawSpec := range specs {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		spec := rawSpec
		if spec.LogLimitBytes <= 0 {
			spec.LogLimitBytes = s.cfg.LogLimitBytes
		}
		if spec.BindURL != "" {
			if err := CheckPortFree(spec.BindURL); err != nil {
				return rollback(fmt.Errorf("service %s bind check failed: %w", spec.Role, err))
			}
		}
		proc, err := StartManagedProcess(spec, s.handleUnexpectedExit)
		if err != nil {
			return rollback(fmt.Errorf("start %s: %w", spec.Role, err))
		}

		s.stopMu.Lock()
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = proc.StopGroup(proc.Spec().StopGrace, proc.Spec().KillWait)
			s.stopMu.Unlock()
			return rollback(errors.New("supervisor stopped during start"))
		}
		s.order = append(s.order, proc)
		s.byRole[spec.Role] = proc
		if lock == nil && s.ownerPID == 0 {
			s.ownerPID = proc.PID()
		}
		var rec OwnerRecord
		if lock != nil {
			rec = s.buildOwnerRecord("starting", s.order)
		}
		s.mu.Unlock()

		var writeErr error
		if lock != nil {
			writeErr = lock.WriteRecord(rec)
		}
		s.stopMu.Unlock()
		if writeErr != nil {
			return rollback(writeErr)
		}
		var waitErr error
		if spec.ReadyCheck != nil {
			waitErr = WaitReady(ctx, proc, spec.ReadyCheck, spec.ReadyTimeout, s.cfg.ReadyPollInterval, spec.AllowCleanExit)
		} else if spec.HealthURL != "" {
			waitErr = WaitHTTPReady(ctx, proc, spec.HealthURL, spec.HealthHeaders, spec.ReadyTimeout)
		}
		if waitErr != nil {
			isTimeoutOrCancel := errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled)
			if s.cfg.KeepOnTimeout && isTimeoutOrCancel && !proc.Exited() {
				s.stopMu.Lock()
				s.mu.Lock()
				if s.stopped {
					s.mu.Unlock()
					s.stopMu.Unlock()
					return rollback(waitErr)
				}
				monitorCtx, cancel := context.WithCancel(context.Background())
				s.monitorCancel = cancel
				s.wg.Add(1)
				s.mu.Unlock()
				s.stopMu.Unlock()
				go s.runMonitor(monitorCtx)
				return waitErr
			}
			return rollback(waitErr)
		}
		proc.SetHealthState(StateReady)
	}

	s.stopMu.Lock()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		s.stopMu.Unlock()
		return rollback(errors.New("supervisor stopped during start"))
	}
	var readyRec OwnerRecord
	if lock != nil {
		readyRec = s.buildOwnerRecord(s.cfg.PhaseOnReady, s.order)
	}
	s.mu.Unlock()

	if lock != nil {
		if err := lock.WriteRecord(readyRec); err != nil {
			s.stopMu.Unlock()
			return rollback(err)
		}
	}

	monitorCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.phase = s.cfg.PhaseOnReady
	s.state = StateReady
	s.monitorCancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	s.stopMu.Unlock()

	go s.runMonitor(monitorCtx)
	return nil
}

// WaitReady waits until check succeeds while also observing exit of the primary
// supervised process (if any). Useful when retrying readiness after KeepOnTimeout.
func (s *Supervisor) WaitReady(ctx context.Context, check func(context.Context) (bool, error)) error {
	s.mu.Lock()
	var primary *ManagedProcess
	var allowClean bool
	if len(s.order) > 0 {
		primary = s.order[0]
		allowClean = primary.Spec().AllowCleanExit
	}
	pollInterval := s.cfg.ReadyPollInterval
	s.mu.Unlock()

	if err := WaitReady(ctx, primary, check, 0, pollInterval, allowClean); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.stopped && s.state == StateStarting {
		s.state = StateReady
		s.phase = s.cfg.PhaseOnReady
	}
	s.mu.Unlock()
	if primary != nil {
		primary.SetHealthState(StateReady)
	}
	return nil
}

func (s *Supervisor) handleUnexpectedExit(status ServiceStatus) {
	s.mu.Lock()
	stopped := s.stopped
	onCrash := s.cfg.OnCrash
	if !stopped && (s.state == StateStarting || s.state == StateReady || s.state == StateDegraded) {
		s.state = StateExited
	}
	s.mu.Unlock()

	if stopped {
		return
	}
	select {
	case s.exitedCh <- status:
	default:
	}
	if onCrash != nil {
		onCrash(status)
	}
}

func (s *Supervisor) runMonitor(ctx context.Context) {
	defer s.wg.Done()
	client := newLoopbackProbeClient(s.cfg.HealthTimeout)
	failures := make(map[string]int)

	ticker := time.NewTicker(s.cfg.MonitorInterval)
	defer ticker.Stop()
	var lastDescendantTrack time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		s.mu.Lock()
		procs := slices.Clone(s.order)
		s.mu.Unlock()

		for _, proc := range procs {
			if ctx.Err() != nil {
				return
			}
			spec := proc.Spec()
			if spec.LogPath != "" {
				limit := spec.LogLimitBytes
				if limit <= 0 {
					limit = s.cfg.LogLimitBytes
				}
				_ = TrimServiceLog(spec.LogPath, limit)
			}
		}
		if s.cfg.LogDir != "" {
			_ = TrimServiceLogDir(s.cfg.LogDir, s.cfg.LogLimitBytes)
		}
		if s.cfg.StoreRoot != "" {
			storeLogsDir := filepath.Join(s.cfg.StoreRoot, "logs")
			if filepath.Clean(storeLogsDir) != filepath.Clean(s.cfg.LogDir) {
				_ = TrimServiceLogDir(storeLogsDir, s.cfg.LogLimitBytes)
			}
		}
		if len(procs) > 0 && (lastDescendantTrack.IsZero() || time.Since(lastDescendantTrack) >= s.cfg.DescendantTrackInterval) {
			lastDescendantTrack = time.Now()
			s.refreshDescendants(ctx, procs, !s.cfg.ExternalOwner)
		}

		allReady := len(procs) > 0
		for _, proc := range procs {
			if ctx.Err() != nil {
				return
			}
			spec := proc.Spec()
			if proc.Exited() && !spec.AllowCleanExit {
				allReady = false
				continue
			}
			if spec.HealthCheck == nil && spec.HealthURL == "" {
				continue
			}

			var probeErr error
			if spec.HealthCheck != nil {
				probeCtx, cancel := context.WithTimeout(ctx, s.cfg.HealthTimeout)
				probeErr = spec.HealthCheck(probeCtx)
				cancel()
			} else {
				probeErr = probeEndpointOnce(ctx, client, spec.HealthURL, spec.HealthHeaders)
			}
			if ctx.Err() != nil {
				return
			}
			if proc.Exited() && !spec.AllowCleanExit {
				allReady = false
				continue
			}
			if probeErr == nil {
				failures[spec.Role] = 0
				proc.SetHealthState(StateReady)
				continue
			}
			allReady = false
			failures[spec.Role]++
			if failures[spec.Role] >= s.cfg.HealthFailureMax {
				proc.SetLastError(probeErr.Error())
				proc.SetHealthState(StateDegraded)
			}
		}

		if allReady {
			s.mu.Lock()
			if !s.stopped && s.state == StateStarting {
				s.state = StateReady
				s.phase = s.cfg.PhaseOnReady
			}
			s.mu.Unlock()
		}
	}
}

func probeEndpointOnce(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) error {
	_, _, targetURL, err := parseLoopbackURL(rawURL, false)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Stop stops background monitoring, shuts down children in strict reverse startup order,
// and removes the owner receipt only when every process group exits cleanly.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()

	s.mu.Lock()
	cancel := s.monitorCancel
	s.monitorCancel = nil
	procs := slices.Clone(s.order)
	lock := s.owner
	s.order = nil
	s.byRole = make(map[string]*ManagedProcess)
	s.owner = nil
	s.phase = ""
	s.stopped = true
	if cancel == nil && lock == nil && len(procs) == 0 {
		s.state = StateStopped
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	allStopped, stopErrs := s.teardownProcesses(ctx, procs, "stop")

	if lock != nil {
		if allStopped {
			if err := lock.RemoveRecord(); err != nil {
				stopErrs = append(stopErrs, err)
			}
		}
		if err := lock.Release(); err != nil {
			stopErrs = append(stopErrs, err)
		}
	}

	s.mu.Lock()
	s.ownerPID = 0
	s.state = StateStopped
	s.mu.Unlock()

	return errors.Join(stopErrs...)
}

// Snapshot returns a defensive view of aggregate supervisor and service state.
func (s *Supervisor) Snapshot() SupervisorSnapshot {
	s.mu.Lock()
	baseState := s.state
	ownerPID := s.ownerPID
	phase := s.phase
	procs := slices.Clone(s.order)
	s.mu.Unlock()

	statuses := make([]ServiceStatus, 0, len(procs))
	aggState := baseState
	anyDegraded := false
	anyExited := false
	for _, proc := range procs {
		st := proc.Status()
		statuses = append(statuses, st)
		switch st.State {
		case StateExited:
			anyExited = true
		case StateDegraded:
			anyDegraded = true
		}
	}
	if baseState != StateStopped {
		switch {
		case anyExited:
			aggState = StateExited
		case anyDegraded:
			aggState = StateDegraded
		}
	}
	return SupervisorSnapshot{
		State:    aggState,
		OwnerPID: ownerPID,
		Phase:    phase,
		Services: statuses,
	}
}

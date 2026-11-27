package gateway

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/engine"
	daemonkit "github.com/zwhu/memlane/kit/daemon"
)

const (
	healthCacheTTL             = 2 * time.Second
	healthBoundedWaitLimit     = 150 * time.Millisecond
	capabilitiesFirstWaitLimit = 50 * time.Millisecond
)

type engineHealth struct {
	engine.HealthStatus
	Error string `json:"error,omitempty"`
}

func (s *Server) probeEngineHealth(ctx context.Context, timeout time.Duration) engineHealth {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	state, err := s.cfg.Engine.Health(probeCtx)
	if err == nil {
		err = probeCtx.Err()
	}
	if err != nil {
		state.OK = false
	}
	return engineHealth{state, sanitizeError(err)}
}

func (s *Server) engineHealth(ctx context.Context) engineHealth {
	return s.probeEngineHealth(ctx, 3*time.Second)
}

func (s *Server) uptime() int64 { return int64(time.Since(s.since) / time.Second) }

// WaitLoopback blocks until ctx is canceled or the loopback HTTP server exits,
// propagating any unexpected listener error.
func (s *Server) WaitLoopback(ctx context.Context) error {
	s.mu.Lock()
	l := s.listener
	s.mu.Unlock()
	if l == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case <-l.done:
		return l.err
	}
}

type runtimeMonitorHistory struct {
	mu                sync.Mutex
	lastOK, lastError time.Time

	hasSnapshot       bool
	cachedHealth      engineHealth
	cachedAt          time.Time
	capabilitiesEpoch int64
	refreshInFlight   bool
	refreshGen        uint64
	refreshCancel     context.CancelFunc
	refreshWaiters    []chan struct{}

	hasEnginePush     bool
	pushedEngineOK    bool
	pushedEngineErr   string
	startupEngineErr  string
	startupPrepareErr string

	managedEngineGateEnforced bool
	managedEngineGateReady    bool
}

func (h *runtimeMonitorHistory) record(checked, failure time.Time) (lastOK, lastError time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if failure.IsZero() && checked.After(h.lastOK) {
		h.lastOK = checked
	}
	if failure.After(h.lastError) {
		h.lastError = failure
	}
	return h.lastOK, h.lastError
}

// storeSnapshotLocked stores next while caller holds h.mu.
// Note: recovering engine health clears startupEngineErr but preserves startupPrepareErr.
func (h *runtimeMonitorHistory) storeSnapshotLocked(next engineHealth) int64 {
	changed := !h.hasSnapshot || h.cachedHealth.OK != next.OK || !reflect.DeepEqual(h.cachedHealth.Capabilities, next.Capabilities)
	if changed && (next.OK || h.hasSnapshot) {
		h.capabilitiesEpoch++
	}
	h.hasSnapshot = true
	h.cachedHealth = next
	h.cachedAt = time.Now()
	if next.OK {
		h.startupEngineErr = ""
		h.hasEnginePush = true
		h.pushedEngineOK = true
		h.pushedEngineErr = ""
	}
	return h.capabilitiesEpoch
}

func (h *runtimeMonitorHistory) snapshot() (health engineHealth, epoch int64, fresh bool, hasSnap bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.hasSnapshot {
		return engineHealth{}, h.capabilitiesEpoch, false, false
	}
	return h.cachedHealth, h.capabilitiesEpoch, time.Since(h.cachedAt) < healthCacheTTL, true
}

func (h *runtimeMonitorHistory) startupErrors() (engineErr, prepareErr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startupEngineErr, h.startupPrepareErr
}

// SetManagedEngineGate enables and updates the consumption gate for InboxWorker.
// While enforced and not ready, new captures remain pending instead of failing against
// an unready or mismatched engine port.
func (s *Server) SetManagedEngineGate(ready bool) {
	s.monitorHistory.mu.Lock()
	s.monitorHistory.managedEngineGateEnforced = true
	s.monitorHistory.managedEngineGateReady = ready
	s.monitorHistory.mu.Unlock()
}

func (s *Server) managedEngineGateOpen() bool {
	s.monitorHistory.mu.Lock()
	defer s.monitorHistory.mu.Unlock()
	if !s.monitorHistory.managedEngineGateEnforced {
		return true
	}
	return s.monitorHistory.managedEngineGateReady
}

// VerifyEngineReachable runs an authenticated Engine.Health probe and updates
// the health cache. Useful in attach mode before marking the engine ready.
func (s *Server) VerifyEngineReachable(ctx context.Context) bool {
	health := s.probeEngineHealth(ctx, 3*time.Second)
	s.monitorHistory.mu.Lock()
	s.monitorHistory.storeSnapshotLocked(health)
	s.monitorHistory.mu.Unlock()
	return health.OK
}

// NotifyEngineStatus updates the cached engine readiness state when the daemon
// observes cognee-engine starting, becoming ready, degrading, or exiting.
func (s *Server) NotifyEngineStatus(ok bool, errMsg string) {
	sanitized := ""
	if strings.TrimSpace(errMsg) != "" {
		sanitized = sanitizeError(errors.New(errMsg))
	}
	s.monitorHistory.mu.Lock()
	s.monitorHistory.hasEnginePush = true
	s.monitorHistory.pushedEngineOK = ok
	s.monitorHistory.pushedEngineErr = sanitized
	s.monitorHistory.refreshGen++
	staleCancel := s.monitorHistory.refreshCancel
	s.monitorHistory.refreshCancel = nil
	s.monitorHistory.refreshInFlight = false
	staleWaiters := s.monitorHistory.refreshWaiters
	s.monitorHistory.refreshWaiters = nil
	if ok {
		s.monitorHistory.startupEngineErr = ""
		s.monitorHistory.cachedAt = time.Time{}
		if !s.monitorHistory.hasSnapshot || !s.monitorHistory.cachedHealth.OK {
			s.monitorHistory.capabilitiesEpoch++
		}
		s.monitorHistory.cachedHealth.OK = true
		s.monitorHistory.cachedHealth.Error = ""
		s.monitorHistory.hasSnapshot = true
	} else {
		if s.monitorHistory.hasSnapshot && s.monitorHistory.cachedHealth.OK {
			s.monitorHistory.capabilitiesEpoch++
		}
		s.monitorHistory.cachedHealth.OK = false
		s.monitorHistory.cachedHealth.Error = sanitized
		s.monitorHistory.cachedAt = time.Now()
		s.monitorHistory.hasSnapshot = true
	}
	s.monitorHistory.mu.Unlock()

	if staleCancel != nil {
		staleCancel()
	}
	for _, ch := range staleWaiters {
		close(ch)
	}

	if ok {
		s.triggerEngineHealthRefresh(3 * time.Second)
	}
}

// InvalidateHealthCache forces the next health or capabilities read to refresh
// the cached engine snapshot.
func (s *Server) InvalidateHealthCache() {
	s.monitorHistory.mu.Lock()
	s.monitorHistory.cachedAt = time.Time{}
	s.monitorHistory.refreshGen++
	staleCancel := s.monitorHistory.refreshCancel
	s.monitorHistory.refreshCancel = nil
	s.monitorHistory.refreshInFlight = false
	staleWaiters := s.monitorHistory.refreshWaiters
	s.monitorHistory.refreshWaiters = nil
	s.monitorHistory.mu.Unlock()
	if staleCancel != nil {
		staleCancel()
	}
	for _, ch := range staleWaiters {
		close(ch)
	}
}

// RecordStartupEngineFailure records an engine startup/reachability failure so
// /api/health and /v1/runtime/monitor expose the exact reason while the gateway stays up.
func (s *Server) RecordStartupEngineFailure(_ context.Context, err error) {
	if err == nil {
		return
	}
	msg := sanitizeError(err)
	if msg == "" {
		msg = "engine_startup_failed"
	}
	s.monitorHistory.mu.Lock()
	s.monitorHistory.startupEngineErr = msg
	s.monitorHistory.mu.Unlock()
	s.NotifyEngineStatus(false, msg)
}

// RecordStartupPrepareFailure records an initial gateway preparation failure
// (such as initial catalog projection failure) separately from engine health
// so subsequent engine health probes cannot erase it until preparation succeeds.
func (s *Server) RecordStartupPrepareFailure(_ context.Context, err error) {
	if err == nil {
		return
	}
	msg := sanitizeError(err)
	if msg == "" {
		msg = "gateway_startup_prepare_failed"
	}
	s.monitorHistory.mu.Lock()
	s.monitorHistory.startupPrepareErr = msg
	s.monitorHistory.mu.Unlock()
}

// ClearStartupPrepareFailure clears any recorded startup preparation error.
func (s *Server) ClearStartupPrepareFailure() {
	s.monitorHistory.mu.Lock()
	s.monitorHistory.startupPrepareErr = ""
	s.monitorHistory.mu.Unlock()
}

func (s *Server) triggerEngineHealthRefresh(timeout time.Duration) <-chan struct{} {
	done := make(chan struct{})
	s.monitorHistory.mu.Lock()
	s.monitorHistory.refreshWaiters = append(s.monitorHistory.refreshWaiters, done)
	if s.monitorHistory.refreshInFlight {
		s.monitorHistory.mu.Unlock()
		return done
	}
	s.monitorHistory.refreshInFlight = true
	gen := s.monitorHistory.refreshGen
	probeCtx, cancel := context.WithCancel(context.Background())
	s.monitorHistory.refreshCancel = cancel
	s.monitorHistory.mu.Unlock()

	go func() {
		defer cancel()
		next := s.probeEngineHealth(probeCtx, timeout)
		s.monitorHistory.mu.Lock()
		if s.monitorHistory.refreshGen != gen {
			s.monitorHistory.mu.Unlock()
			return
		}
		s.monitorHistory.storeSnapshotLocked(next)
		waiters := s.monitorHistory.refreshWaiters
		s.monitorHistory.refreshWaiters = nil
		s.monitorHistory.refreshInFlight = false
		s.monitorHistory.refreshCancel = nil
		s.monitorHistory.mu.Unlock()

		for _, ch := range waiters {
			close(ch)
		}
	}()
	return done
}

func (s *Server) probeAndCacheEngineHealth(ctx context.Context, timeout time.Duration) engineHealth {
	done := s.triggerEngineHealthRefresh(timeout)
	select {
	case <-done:
		health, _, _, _ := s.monitorHistory.snapshot()
		return health
	case <-ctx.Done():
		return engineHealth{Error: sanitizeError(ctx.Err())}
	}
}

// boundedHealthSnapshot returns a fresh cached snapshot immediately or triggers
// an async refresh and waits at most waitLimit, preventing /api/health from
// being stalled by a slow 3s /v1/runtime/monitor probe.
func (s *Server) boundedHealthSnapshot(ctx context.Context, waitLimit time.Duration) engineHealth {
	health, _, fresh, _ := s.monitorHistory.snapshot()
	if fresh {
		return health
	}
	done := s.triggerEngineHealthRefresh(3 * time.Second)
	timer := time.NewTimer(waitLimit)
	defer timer.Stop()
	select {
	case <-done:
		health, _, _, _ = s.monitorHistory.snapshot()
		return health
	case <-timer.C:
		health, _, _, _ = s.monitorHistory.snapshot()
		return health
	case <-ctx.Done():
		return engineHealth{Error: sanitizeError(ctx.Err())}
	}
}

func (s *Server) inspectDaemonEngine(ctx context.Context) (daemonMap map[string]any, phase string, serviceErr string, engineSvc *daemonkit.ServiceStatus) {
	if s.cfg.DaemonStatus == nil {
		return nil, "", "", nil
	}
	daemonMap = s.cfg.DaemonStatus(ctx)
	if daemonMap == nil {
		return nil, "", "", nil
	}
	if services, ok := daemonMap["services"].([]daemonkit.ServiceStatus); ok {
		for i := range services {
			svc := services[i]
			if svc.Role != "cognee-engine" {
				continue
			}
			svcCopy := svc
			engineSvc = &svcCopy
			if svc.LastError != "" {
				serviceErr = sanitizeError(errors.New(svc.LastError))
			}
			switch {
			case svc.ExitedAt != nil || svc.State == daemonkit.StateExited:
				return daemonMap, "exited", serviceErr, engineSvc
			case svc.State == daemonkit.StateStarting || svc.State == daemonkit.StatePending:
				return daemonMap, "starting", serviceErr, engineSvc
			case svc.State == daemonkit.StateDegraded:
				return daemonMap, "degraded", serviceErr, engineSvc
			case svc.State == daemonkit.StateReady:
				return daemonMap, "ready", serviceErr, engineSvc
			}
		}
	}
	if state, ok := daemonMap["state"].(daemonkit.ServiceState); ok && state == daemonkit.StateStarting {
		return daemonMap, "starting", "", nil
	}
	if state, ok := daemonMap["state"].(string); ok && state == string(daemonkit.StateStarting) {
		return daemonMap, "starting", "", nil
	}
	return daemonMap, "", "", nil
}

func resolveOwnerPID(daemonMap map[string]any) int {
	if daemonMap != nil {
		switch v := daemonMap["owner_pid"].(type) {
		case int:
			if v > 0 {
				return v
			}
		case int64:
			if v > 0 {
				return int(v)
			}
		case float64:
			if v > 0 {
				return int(v)
			}
		}
	}
	return os.Getpid()
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inbox, err := s.inbox.Summary(ctx, "")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	daemonMap, daemonPhase, svcErr, _ := s.inspectDaemonEngine(ctx)
	startupEngineErr, _ := s.monitorHistory.startupErrors()
	var health engineHealth
	status := "ready"
	engineErr := ""
	switch daemonPhase {
	case "starting":
		status = "starting"
		health.OK = false
	case "exited":
		status = "exited"
		health.OK = false
		engineErr = firstNonEmptyString(svcErr, startupEngineErr, "cognee-engine exited")
	default:
		health = s.boundedHealthSnapshot(ctx, healthBoundedWaitLimit)
		if !health.OK {
			status = "degraded"
			engineErr = firstNonEmptyString(health.Error, svcErr, startupEngineErr)
		}
	}
	resp := map[string]any{
		"ok":             health.OK,
		"gateway_ok":     true,
		"gateway_ready":  true,
		"engine_ok":      health.OK,
		"engine_ready":   health.OK,
		"owner_pid":      resolveOwnerPID(daemonMap),
		"status":         status,
		"uptime_seconds": s.uptime(),
		"inbox":          inbox,
	}
	if engineErr != "" {
		resp["engine_error"] = engineErr
	}
	writeJSON(w, 200, resp)
}

func (s *Server) completionHealth(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CompletionHandler == nil {
		writeError(w, http.StatusNotFound, "not_configured", "completion handler is not configured")
		return
	}
	req := r.Clone(r.Context())
	req.RequestURI = ""
	req.URL.Path = "/health"
	req.URL.RawPath = ""
	req.Header.Del("Authorization")
	req.Header.Del("X-Memlane-Management")
	s.cfg.CompletionHandler.ServeHTTP(w, req)
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, daemonPhase, _, _ := s.inspectDaemonEngine(ctx)
	health, epoch, fresh, hasSnap := s.monitorHistory.snapshot()
	switch daemonPhase {
	case "starting", "exited":
		health.OK = false
	default:
		if !fresh {
			done := s.triggerEngineHealthRefresh(3 * time.Second)
			if !hasSnap {
				timer := time.NewTimer(capabilitiesFirstWaitLimit)
				select {
				case <-done:
					health, epoch, _, _ = s.monitorHistory.snapshot()
				case <-timer.C:
				case <-ctx.Done():
				}
				timer.Stop()
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"api_versions":       []string{"v2", "v1"},
		"reading_contract":   "catalog-cursor-v1",
		"capabilities":       health.Capabilities,
		"engine_ready":       health.OK,
		"capabilities_epoch": epoch,
		"features": map[string]bool{
			"catalog_cursor":   true,
			"reading_state":    true,
			"async_capture":    true,
			"model_controller": s.cfg.Models != nil,
			"local_body":       s.cfg.Projector != nil,
			"completion":       s.cfg.CompletionHandler != nil,
			"mcp":              s.cfg.MCPHandler != nil,
			"profiles":         s.cfg.ProfilesHandler != nil,
		},
	})
}

func monitorUnix(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixMilli()) / 1000
}

func monitorISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type monitorInboxView struct {
	InboxSummary
	AwaitingPublish  int    `json:"awaiting_publish"`
	PublishFailed    int    `json:"publish_failed"`
	ExtractionPaused bool   `json:"extraction_paused"`
	PauseReason      string `json:"pause_reason,omitempty"`
}

func (s *Server) extractionPauseState(ctx context.Context, enginePhase, startupEngineErr, startupPrepareErr string) (paused bool, reason string) {
	if startupEngineErr != "" {
		return true, startupEngineErr
	}
	if startupPrepareErr != "" {
		return true, startupPrepareErr
	}
	if enginePhase == "starting" {
		return true, "engine_starting"
	}
	if enginePhase == "exited" {
		return true, "engine_exited"
	}
	if code := s.startupModelError(); code != "" {
		return true, code
	}
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err == nil && hasPending {
		return true, "handoff_unconfirmed"
	}
	if state, err := s.cfg.Store.GetGenerationState(ctx); err == nil {
		if strings.TrimSpace(state.StartupError) != "" {
			return true, strings.TrimSpace(state.StartupError)
		}
		if state.ActiveProfileID == "" || state.ActiveRevision <= 0 {
			return true, "no_active_profile"
		}
	}
	return false, ""
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (s *Server) monitor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inbox, err := s.inbox.Summary(ctx, "")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	daemon := map[string]any{"pid": os.Getpid(), "uptime_seconds": s.uptime(), "goroutines": runtime.NumGoroutine(), "mem_alloc_bytes": memory.Alloc}
	daemonExtra, daemonPhase, svcErr, _ := s.inspectDaemonEngine(ctx)
	if daemonExtra != nil {
		maps.Copy(daemon, daemonExtra)
	}
	started := time.Now()
	var health engineHealth
	if daemonPhase != "starting" && daemonPhase != "exited" {
		health = s.probeAndCacheEngineHealth(ctx, 3*time.Second)
	}
	latency := float64(time.Since(started).Microseconds()) / 1000
	checked := time.Now()

	state, enginePhase, statusSummary := "ready", "ready", "提炼服务运行正常"
	var failure time.Time
	var nextAction string
	lastErrorText := health.Error
	startupEngineErr, startupPrepareErr := s.monitorHistory.startupErrors()

	switch daemonPhase {
	case "starting":
		state, enginePhase = "starting", "starting"
		statusSummary = "记忆引擎启动中，已可正常浏览和采集"
		lastErrorText = ""
	case "exited":
		state, enginePhase, failure = "exited", "exited", checked
		statusSummary = "Cognee 进程已退出，服务未自动重启"
		nextAction = "请在服务日志中核对退出原因，再手动重启服务。"
		lastErrorText = firstNonEmptyString(svcErr, startupEngineErr, lastErrorText)
	default:
		if !health.OK {
			state, enginePhase, failure = "degraded", "degraded", checked
			if lastErrorText == "" {
				lastErrorText = firstNonEmptyString(svcErr, startupEngineErr, "engine_unavailable")
			}
			_, statusSummary, nextAction = describeInboxFailure(lastErrorText)
		}
	}

	processes := map[string]any{"control": map[string]any{"pid": os.Getpid(), "alive": true}}
	var logTail []string
	if services, ok := daemon["services"].([]daemonkit.ServiceStatus); ok {
		safeServices := make([]daemonkit.ServiceStatus, 0, len(services))
		for _, service := range services {
			if service.LastError != "" {
				service.LastError = sanitizeError(errors.New(service.LastError))
			}
			var safeTail []string
			for _, line := range service.TailLines {
				safeTail = append(safeTail, daemonkit.RedactLogLine(line))
			}
			service.TailLines = safeTail
			safeServices = append(safeServices, service)
			if service.Role != "cognee-engine" {
				continue
			}
			alive := service.ExitedAt == nil && service.State != daemonkit.StateExited
			processes["official"] = map[string]any{"pid": service.PID, "alive": alive}
			if r.URL.Query().Get("include_logs") == "1" {
				logTail = append(logTail, service.TailLines...)
			}
			if !alive {
				state, enginePhase = "exited", "exited"
				if service.ExitedAt != nil {
					failure = *service.ExitedAt
				} else {
					failure = checked
				}
				statusSummary = "Cognee 进程已退出，服务未自动重启"
				nextAction = "请在服务日志中核对退出原因，再手动重启服务。"
				if service.LastError != "" {
					lastErrorText = service.LastError
				}
			}
		}
		daemon["services"] = safeServices
	}

	lastOK, lastError := s.monitorHistory.record(checked, failure)
	owner := daemon["owner_pid"]
	if owner == nil {
		owner = os.Getpid()
	}
	var projects int
	if err := s.cfg.Store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projects); err != nil {
		writeDomainError(w, err)
		return
	}

	startupError := firstNonEmptyString(startupEngineErr, startupPrepareErr, s.startupModelError())
	if startupError == "" {
		if genState, genErr := s.cfg.Store.GetGenerationState(ctx); genErr == nil {
			startupError = strings.TrimSpace(genState.StartupError)
		}
	}
	paused, pauseReason := s.extractionPauseState(ctx, enginePhase, startupEngineErr, startupPrepareErr)
	inboxView := monitorInboxView{
		InboxSummary:     inbox,
		AwaitingPublish:  inbox.CatalogPublishPending,
		PublishFailed:    inbox.CatalogPublishFailed,
		ExtractionPaused: paused,
		PauseReason:      pauseReason,
	}

	writeJSON(w, 200, map[string]any{
		"gateway_ok":     true,
		"engine_ok":      health.OK,
		"engine_phase":   enginePhase,
		"startup_error":  startupError,
		"daemon":         daemon,
		"engine":         health,
		"inbox":          inboxView,
		"state":          state,
		"owner_pid":      owner,
		"owner_phase":    daemon["phase"],
		"processes":      processes,
		"checked_at":     monitorUnix(checked),
		"last_ok_at":     monitorUnix(lastOK),
		"latency_ms":     latency,
		"last_error":     lastErrorText,
		"checked_at_iso": monitorISO(checked),
		"last_ok_at_iso": monitorISO(lastOK),
		"last_error_at":  monitorISO(lastError),
		"status_summary": statusSummary,
		"next_action":    nextAction,
		"log_tail":       logTail,
		"summary": map[string]any{
			"projects_count":  projects,
			"filtered_count":  inbox.FilteredCount,
			"recent_filtered": inbox.RecentFiltered,
		},
	})
}

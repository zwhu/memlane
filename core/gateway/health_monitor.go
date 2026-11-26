package gateway

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/engine"
	daemonkit "github.com/zwhu/memlane/kit/daemon"
)

type engineHealth struct {
	engine.HealthStatus
	Error string `json:"error,omitempty"`
}

func (s *Server) engineHealth(ctx context.Context) engineHealth {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, err := s.cfg.Engine.Health(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		state.OK = false
	}
	return engineHealth{state, sanitizeError(err)}
}

func (s *Server) uptime() int64 { return int64(time.Since(s.since) / time.Second) }

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	inbox, err := s.inbox.Summary(r.Context(), "")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	health := s.engineHealth(r.Context())
	status := "ready"
	if !health.OK {
		status = "degraded"
	}
	writeJSON(w, 200, map[string]any{"ok": health.OK, "status": status, "uptime_seconds": s.uptime(), "engine_ok": health.OK, "inbox": inbox})
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
	health := s.engineHealth(r.Context())
	writeJSON(w, 200, map[string]any{"api_versions": []string{"v2", "v1"}, "reading_contract": "catalog-cursor-v1",
		"capabilities": health.Capabilities, "features": map[string]bool{"catalog_cursor": true, "reading_state": true,
			"async_capture": true, "model_controller": s.cfg.Models != nil, "local_body": s.cfg.Projector != nil,
			"completion": s.cfg.CompletionHandler != nil, "mcp": s.cfg.MCPHandler != nil, "profiles": s.cfg.ProfilesHandler != nil}})
}

type runtimeMonitorHistory struct {
	mu                sync.Mutex
	lastOK, lastError time.Time
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
	if s.cfg.DaemonStatus != nil {
		maps.Copy(daemon, s.cfg.DaemonStatus(ctx))
	}
	started := time.Now()
	health := s.engineHealth(ctx)
	latency := float64(time.Since(started).Microseconds()) / 1000
	checked := time.Now()
	state, statusSummary := "ready", "提炼服务运行正常"
	var failure time.Time
	var nextAction string
	lastErrorText := health.Error
	if !health.OK {
		state, failure = "degraded", checked
		if lastErrorText == "" {
			lastErrorText = "engine_unavailable"
		}
		_, statusSummary, nextAction = describeInboxFailure(lastErrorText)
	}
	processes := map[string]any{"control": map[string]any{"pid": os.Getpid(), "alive": true}}
	var logTail []string
	if services, ok := daemon["services"].([]daemonkit.ServiceStatus); ok {
		safeServices := make([]daemonkit.ServiceStatus, 0, len(services))
		for _, service := range services {
			service.LastError = sanitizeError(errors.New(service.LastError))
			var safeTail []string
			for _, line := range service.TailLines {
				safeTail = append(safeTail, daemonkit.RedactLogLine(line))
			}
			service.TailLines = safeTail
			safeServices = append(safeServices, service)
			if service.Role != "cognee-engine" {
				continue
			}
			alive := service.ExitedAt == nil
			processes["official"] = map[string]any{"pid": service.PID, "alive": alive}
			if r.URL.Query().Get("include_logs") == "1" {
				logTail = append(logTail, service.TailLines...)
			}
			if !alive {
				state, failure = "exited", *service.ExitedAt
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
	writeJSON(w, 200, map[string]any{"daemon": daemon, "engine": health, "inbox": inbox,
		"state": state, "owner_pid": owner, "owner_phase": daemon["phase"], "processes": processes,
		"checked_at": monitorUnix(checked), "last_ok_at": monitorUnix(lastOK), "latency_ms": latency, "last_error": lastErrorText,
		"checked_at_iso": monitorISO(checked), "last_ok_at_iso": monitorISO(lastOK), "last_error_at": monitorISO(lastError),
		"status_summary": statusSummary, "next_action": nextAction, "log_tail": logTail,
		"summary": map[string]any{"projects_count": projects, "filtered_count": inbox.FilteredCount, "recent_filtered": inbox.RecentFiltered}})
}

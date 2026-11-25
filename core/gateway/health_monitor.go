package gateway

import (
	"context"
	"maps"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/zwhu/memlane/core/engine"
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

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	health := s.engineHealth(r.Context())
	writeJSON(w, 200, map[string]any{"api_versions": []string{"v2", "v1"}, "reading_contract": "catalog-cursor-v1",
		"capabilities": health.Capabilities, "features": map[string]bool{"catalog_cursor": true, "reading_state": true,
			"async_capture": true, "model_controller": s.cfg.Models != nil, "local_body": s.cfg.Projector != nil,
			"completion": s.cfg.CompletionHandler != nil, "mcp": s.cfg.MCPHandler != nil, "profiles": s.cfg.ProfilesHandler != nil}})
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
	writeJSON(w, 200, map[string]any{"daemon": daemon, "engine": s.engineHealth(ctx), "inbox": inbox})
}

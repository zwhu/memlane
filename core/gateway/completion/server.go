package completion

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/agents/bridge"
)

const MaxBridgeConcurrencyLimit = 32

type ActiveProfile struct {
	Mode, BridgeKind, BridgeProvider, Model, RouteModel, Executable, AuthHome, ExpectedFingerprint string
	FollowAgentDefaults                                                                            bool
	Defaults                                                                                       *agents.ModelDefaults
	ExtensionBytes                                                                                 []byte
}

type ActiveProfileGate interface {
	ResolveActive(ctx context.Context) (*ActiveProfile, error)
}

type StaticProfileGate struct{ Profile *ActiveProfile }

func (g StaticProfileGate) ResolveActive(ctx context.Context) (*ActiveProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profile, _, err := checkProfile(g.Profile)
	return profile, err
}

type Config struct {
	Token                       string
	MaxConcurrency, MaxInflight int
	MaxBodyBytes                int64
	IdleTimeout                 time.Duration
	Gate                        ActiveProfileGate
	Executor                    func(context.Context, bridge.ExecOptions, *bridge.ParsedRequest) (string, error)
	BridgeOptionsCustomizer     func(*bridge.ExecOptions)
	OnTrace                     func(bridge.TraceEvent)
}

type Server struct {
	cfg      Config
	slot     chan struct{}
	admitted atomic.Int32
	auth     []byte
	mu       sync.Mutex
	listener *loopbackServer
}

type loopbackServer struct {
	server   *http.Server
	stop     context.CancelFunc
	done     chan struct{}
	serveErr error
}

func DefaultMemlaneBridgeOptions(opts *bridge.ExecOptions) {
	opts.ClientName = "memlane_completion"
	opts.PermissionProfile = "memlane_completion"
	opts.AgentName = "memlane-processing"
	opts.ProcessingEnvKey = "MEMLANE_PROCESSING_SESSION"
	opts.SchemaEventPrefix = "memlane_schema"
	opts.SchemaName = "memlane_organization"
}

func NewServer(cfg Config) (*Server, error) {
	if len(cfg.Token) < 24 {
		return nil, errors.New("completion token must contain at least 24 bytes")
	}
	if cfg.MaxConcurrency == 0 {
		cfg.MaxConcurrency = 2
	}
	if cfg.MaxInflight == 0 {
		cfg.MaxInflight = MaxBridgeConcurrencyLimit
	}
	if cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > cfg.MaxInflight || cfg.MaxInflight > MaxBridgeConcurrencyLimit {
		return nil, errors.New("completion concurrency or inflight limit invalid")
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = bridge.MaxBodyBytes
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 90 * time.Second
	}
	if cfg.MaxBodyBytes < 0 || cfg.IdleTimeout < 0 {
		return nil, errors.New("completion body or idle limit invalid")
	}
	if cfg.Executor == nil {
		cfg.Executor = bridge.Execute
	}
	if cfg.BridgeOptionsCustomizer == nil {
		cfg.BridgeOptionsCustomizer = DefaultMemlaneBridgeOptions
	}
	auth := []byte("Bearer " + cfg.Token)
	cfg.Token = ""
	s := &Server{cfg: cfg, slot: make(chan struct{}, cfg.MaxConcurrency), auth: auth}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			s.health(w, r)
		case "/v1/chat/completions":
			s.complete(w, r)
		default:
			writeError(w, "not_found", 404)
		}
	})
}

func gatewayError(code string, status int) *bridge.BridgeError {
	return &bridge.BridgeError{Code: code, Status: status}
}

func checkProfile(input *ActiveProfile) (*ActiveProfile, string, error) {
	if input == nil || input.Mode != "agent_bridge" {
		return nil, "", gatewayError("bridge_inactive", 503)
	}
	active := *input
	active.ExtensionBytes = bytes.Clone(input.ExtensionBytes)
	if active.BridgeKind == "pi" && active.ExtensionBytes == nil {
		active.ExtensionBytes = bridge.PiExtensionSource()
	}
	if !active.FollowAgentDefaults && active.Model == "" {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	opts := agents.ModelDefaultsOptions{}
	runtime, err := agents.CheckBridgeRuntime(active.BridgeKind, active.Executable, active.AuthHome, opts)
	if err != nil {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	fingerprint, err := agents.BridgeRuntimeFingerprint(active.BridgeKind, active.Executable, active.AuthHome, active.ExtensionBytes, opts)
	if err != nil || active.ExpectedFingerprint != "" && fingerprint != active.ExpectedFingerprint {
		return nil, "", gatewayError("generation_bridge_changed_revalidate", 409)
	}
	var defaults agents.ModelDefaults
	if active.Defaults == nil {
		defaults, err = agents.ReadModelDefaults(active.BridgeKind, runtime.Home, opts)
		if err != nil {
			return nil, "", gatewayError("bridge_profile_mismatch", 409)
		}
	} else {
		defaults = *active.Defaults
	}
	if defaults.Model == "" {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	if active.FollowAgentDefaults {
		active.Model = defaults.Model
	} else if defaults.Model != active.Model {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	provider := map[string]string{"codex": "openai", "traex": "trae", "antigravity": "google"}[active.BridgeKind]
	if active.BridgeKind == "pi" {
		provider = defaults.Provider
		if provider != "deepseek" && provider != "ollama" {
			return nil, "", gatewayError("bridge_profile_mismatch", 409)
		}
	}
	if defaults.Provider != "" && defaults.Provider != provider {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	if !active.FollowAgentDefaults && active.BridgeProvider != "" && active.BridgeProvider != provider {
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	active.Executable, active.AuthHome, active.BridgeProvider = runtime.Executable, runtime.Home, provider
	active.Defaults = &defaults
	return &active, fingerprint, nil
}

func (s *Server) resolve(ctx context.Context) (*ActiveProfile, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", gatewayError("bridge_busy", 429)
	}
	switch gate := s.cfg.Gate.(type) {
	case StaticProfileGate:
		return checkProfile(gate.Profile)
	case *StaticProfileGate:
		if gate != nil {
			return checkProfile(gate.Profile)
		}
	}
	if s.cfg.Gate == nil {
		return nil, "", gatewayError("bridge_inactive", 503)
	}
	gate := reflect.ValueOf(s.cfg.Gate)
	switch gate.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		if gate.IsNil() {
			return nil, "", gatewayError("bridge_inactive", 503)
		}
	}
	profile, err := s.cfg.Gate.ResolveActive(ctx)
	if err != nil {
		var safe *bridge.BridgeError
		if errors.As(err, &safe) && safe != nil {
			switch safe.Code {
			case "generation_bridge_changed_revalidate", "bridge_profile_mismatch":
				return nil, "", gatewayError(safe.Code, 409)
			case "bridge_inactive":
				return nil, "", gatewayError("bridge_inactive", 503)
			}
		}
		return nil, "", gatewayError("bridge_profile_mismatch", 409)
	}
	return checkProfile(profile)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method_not_allowed", 405)
		return
	}
	active, _, err := s.resolve(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ready", "model": active.Model, "agent_default": active.Defaults,
		"provider": active.BridgeKind, "active_or_waiting": s.admitted.Load(), "max_concurrency": s.cfg.MaxConcurrency, "max_inflight": s.cfg.MaxInflight})
}

func (s *Server) admit() bool {
	for {
		existing := s.admitted.Load()
		if existing >= int32(s.cfg.MaxInflight) {
			return false
		}
		if s.admitted.CompareAndSwap(existing, existing+1) {
			return true
		}
	}
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method_not_allowed", 405)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), s.auth) != 1 {
		writeError(w, "unauthorized", 401)
		return
	}
	if !s.admit() {
		writeError(w, "bridge_busy", 429)
		return
	}
	defer s.admitted.Add(-1)
	ctx := r.Context()
	active, stamp, err := s.resolve(ctx)
	if err != nil {
		writeFailure(w, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, "request_body_too_large", 413)
		} else {
			writeError(w, "invalid_request", 400)
		}
		return
	}
	expectedModel := active.Model
	if active.FollowAgentDefaults && active.RouteModel != "" && active.RouteModel != active.Model {
		var envelope struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Model == active.RouteModel {
			expectedModel = active.RouteModel
		}
	}
	req, err := bridge.ParseChatRequest(raw, expectedModel, s.cfg.MaxBodyBytes)
	if err != nil {
		writeFailure(w, err)
		return
	}
	select {
	case <-ctx.Done():
		writeError(w, "bridge_busy", 429)
		return
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	}
	if ctx.Err() != nil {
		writeError(w, "bridge_busy", 429)
		return
	}
	queued, queuedStamp, err := s.resolve(ctx)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if active.BridgeKind != queued.BridgeKind || active.AuthHome != queued.AuthHome ||
		active.Executable != queued.Executable || active.ExpectedFingerprint != queued.ExpectedFingerprint {
		writeError(w, "bridge_profile_mismatch", 409)
		return
	}
	if active.FollowAgentDefaults {
		// Freeze the live Agent model snapshot upon entering the execution slot.
		active = queued
	} else {
		if active.BridgeProvider != queued.BridgeProvider || active.Model != queued.Model {
			writeError(w, "bridge_profile_mismatch", 409)
			return
		}
		if stamp != queuedStamp {
			writeError(w, "generation_bridge_changed_revalidate", 409)
			return
		}
	}
	id := randomID()
	opts := bridge.ExecOptions{RequestID: id, BridgeKind: active.BridgeKind, Executable: active.Executable, AuthHome: active.AuthHome,
		Model: active.Model, BridgeProvider: active.BridgeProvider, Defaults: active.Defaults, IdleTimeout: s.cfg.IdleTimeout, OnTrace: s.cfg.OnTrace}
	s.cfg.BridgeOptionsCustomizer(&opts)
	content, err := s.cfg.Executor(ctx, opts, req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": "chatcmpl-" + id, "object": "chat.completion", "created": time.Now().Unix(), "model": active.Model,
		"choices": []any{map[string]any{"index": 0, "message": bridge.Message{Role: "assistant", Content: content}, "finish_reason": "stop"}}})
}

func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(data[:])
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, code string, status int) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code, "type": "completion_error"}})
}

func writeFailure(w http.ResponseWriter, err error) {
	var be *bridge.BridgeError
	var ae *agents.AgentError
	if errors.As(err, &be) && be != nil && safeCode(be.Code) && be.Status >= 400 && be.Status <= 599 {
		writeError(w, be.Code, be.Status)
	} else if errors.As(err, &ae) && ae != nil && safeCode(ae.Code) && ae.Status >= 400 && ae.Status <= 599 {
		writeError(w, ae.Code, ae.Status)
	} else {
		writeError(w, "bridge_execution_failed", 502)
	}
}

func (s *Server) start(addr string) (string, *loopbackServer, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return "", nil, errors.New("completion listener requires 127.0.0.1")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", nil, errors.New("completion listener requires a numeric port")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return "", nil, errors.New("completion listener already started")
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return "", nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	instance := &loopbackServer{stop: stop, done: make(chan struct{})}
	instance.server = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	s.listener = instance
	go func() {
		err := instance.server.Serve(ln)
		if !errors.Is(err, http.ErrServerClosed) {
			instance.serveErr = err
		}
		close(instance.done)
	}()
	return ln.Addr().String(), instance, nil
}

func (s *Server) StartLoopback(addr string) (string, error) {
	actual, _, err := s.start(addr)
	return actual, err
}

func (s *Server) ListenAndServeLoopback(ctx context.Context, addr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, instance, err := s.start(addr)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		timeout, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.shutdownInstance(timeout, instance)
	case <-instance.done:
		return instance.serveErr
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	instance := s.listener
	s.mu.Unlock()
	if instance == nil {
		return nil
	}
	return s.shutdownInstance(ctx, instance)
}

func (s *Server) shutdownInstance(ctx context.Context, instance *loopbackServer) error {
	instance.stop()
	if err := instance.server.Shutdown(ctx); err != nil {
		return err
	}
	select {
	case <-instance.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	if s.listener == instance {
		s.listener = nil
	}
	s.mu.Unlock()
	return instance.serveErr
}

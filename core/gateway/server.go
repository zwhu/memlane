package gateway

import (
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/agentauth"
	"github.com/zwhu/memlane/kit/agents"
)

type Config struct {
	Store             *domain.Store
	Engine            engine.MemoryEngine
	Models            engine.ModelController
	Projector         engine.CatalogProjector
	CompletionHandler http.Handler
	MCPHandler        http.Handler
	ProfilesHandler   http.Handler
	ManagementToken   string
	AgentTokens       map[string]string
	OutputLanguage    string
	MaxBodyBytes      int64
	SearchTimeout        time.Duration
	InboxWorkers         int
	InboxPollInterval    time.Duration
	InboxMaxPendingItems int
	InboxMaxPendingBytes int64
	InboxPruneInterval   int
	InboxPruneRetention  time.Duration
	DiscoveryOptions     agents.DiscoveryOptions
	ConflictPolicy    *agents.ConflictPolicy
	DaemonStatus      func(context.Context) map[string]any
	CursorSecret      []byte
	ProbeRuntime      map[string]any
	BridgeEndpoint    string
	BridgeTokenFile   string
}

type Server struct {
	cfg          Config
	handler      http.Handler
	inbox        *InboxWorker
	cursors      cursorCodec
	auth         *agentauth.Authenticator
	secrets      secretFileCache
	searches     userSearchHistory
	prompts      pendingPromptStore
	since        time.Time
	mu           sync.Mutex
	listener     *loopback
	registryMu   sync.Mutex
	generationMu sync.Mutex
	commonMu     sync.Mutex
	catalogSync  catalogSyncCoordinator

	startupModelMu        sync.Mutex
	startupModelErr       string
	activeDigestMu        sync.Mutex
	activeDigestProfileID string
	activeDigestRevision  int
	activeDigestValue     string
	reconcileMu           sync.Mutex
	reconcileNext         time.Time
	reconcileBackoff      time.Duration

	codeIndexMu       sync.Mutex
	codeIndexInFlight map[string]struct{}

	monitorHistory runtimeMonitorHistory
}

type loopback struct {
	http   *http.Server
	stopMu sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Store == nil || cfg.Engine == nil || len(cfg.ManagementToken) < 24 {
		return nil, errors.New("store, engine and management token of at least 24 bytes required")
	}
	if cfg.MaxBodyBytes < 0 || cfg.SearchTimeout < 0 || cfg.InboxWorkers < 0 || cfg.InboxPollInterval < 0 ||
		cfg.InboxMaxPendingItems < 0 || cfg.InboxMaxPendingBytes < 0 || cfg.InboxPruneInterval < 0 || cfg.InboxPruneRetention < 0 {
		return nil, errors.New("gateway limits must be positive")
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 4 << 20
	}
	if cfg.SearchTimeout == 0 {
		cfg.SearchTimeout = 15 * time.Second
	}
	if cfg.InboxWorkers == 0 {
		cfg.InboxWorkers = 2
	}
	if cfg.InboxPollInterval == 0 {
		cfg.InboxPollInterval = 2 * time.Second
	}
	if cfg.InboxMaxPendingItems == 0 {
		cfg.InboxMaxPendingItems = 10000
	}
	if cfg.InboxMaxPendingBytes == 0 {
		cfg.InboxMaxPendingBytes = 256 << 20
	}
	if cfg.InboxPruneInterval == 0 {
		cfg.InboxPruneInterval = 64
	}
	if cfg.InboxPruneRetention == 0 {
		cfg.InboxPruneRetention = 24 * time.Hour
	}
	if cfg.OutputLanguage == "" {
		cfg.OutputLanguage = "zh-CN"
	}
	if cfg.Models == nil {
		cfg.Models, _ = engine.AsModelController(cfg.Engine)
	}
	if cfg.Projector == nil {
		cfg.Projector, _ = engine.AsCatalogProjector(cfg.Engine)
	}
	secret := slices.Clone(cfg.CursorSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	cfg.CursorSecret = nil
	cfg.AgentTokens = maps.Clone(cfg.AgentTokens)
	cfg.ProbeRuntime = maps.Clone(cfg.ProbeRuntime)
	cfg.DiscoveryOptions.Kinds = slices.Clone(cfg.DiscoveryOptions.Kinds)
	cfg.DiscoveryOptions.AppDirs = slices.Clone(cfg.DiscoveryOptions.AppDirs)
	cfg.DiscoveryOptions.RuntimeOverrides = maps.Clone(cfg.DiscoveryOptions.RuntimeOverrides)
	policy := agents.ConflictPolicy{
		ManagedMarkers:   []string{"memlane-cognee", "memlane_cognee", "memlane-memory", "memlane"},
		ConflictKeywords: []string{"cognee"},
	}
	if cfg.ConflictPolicy != nil {
		policy = *cfg.ConflictPolicy
		policy.ManagedMarkers = slices.Clone(policy.ManagedMarkers)
		policy.ConflictKeywords = slices.Clone(policy.ConflictKeywords)
		policy.WorkspaceDirs = slices.Clone(policy.WorkspaceDirs)
	}
	cfg.ConflictPolicy = &policy
	auth := agentauth.New(agentauth.Config{
		Store:       cfg.Store,
		AgentTokens: cfg.AgentTokens,
	})
	if setter, ok := cfg.MCPHandler.(interface{ SetAuthenticator(*agentauth.Authenticator) }); ok {
		setter.SetAuthenticator(auth)
	}
	s := &Server{cfg: cfg, cursors: cursorCodec{secret}, auth: auth, since: time.Now()}
	s.inbox = &InboxWorker{cfg: cfg, wake: make(chan struct{}, cfg.InboxWorkers), server: s}
	s.handler = s.routes()
	return s, nil
}

func (s *Server) routes() http.Handler {
	const completionPath = "/v1/chat/completions"
	mux := http.NewServeMux()
	both := func(method, path string, f http.HandlerFunc) {
		for _, version := range []string{"/api", "/v1"} {
			mux.HandleFunc(method+" "+version+path, f)
		}
	}
	both("GET", "/health", s.health)
	both("GET", "/completion/health", s.completionHealth)
	both("GET", "/capabilities", s.capabilities)
	both("GET", "/runtime/monitor", s.monitor)
	both("GET", "/overview/bootstrap", s.overview)
	both("GET", "/overview/day", s.overviewDay)
	both("GET", "/projects", s.listProjects)
	both("GET", "/linkable-datasets", s.listLinkableDatasets)
	both("PUT", "/defaults", s.updateDefaults)
	both("POST", "/projects", s.createProject)
	both("GET", "/projects/{id}", s.getProject)
	both("GET", "/projects/{id}/filtered-captures", s.listFilteredCaptures)
	both("PATCH", "/projects/{id}", s.updateProject)
	both("PUT", "/projects/{id}/name", s.updateProject)
	both("PUT", "/projects/{id}/auto-capture", s.toggleCapture)
	both("POST", "/projects/{id}/aliases", s.addAlias)
	both("DELETE", "/projects/{id}", s.deleteProject)
	both("POST", "/projects/{id}/retry", s.retryProject)
	both("GET", "/projects/{id}/code-activity", s.projectCodeActivity)
	both("POST", "/projects/{id}/code-dataset", s.ensureProjectCodeDataset)
	both("POST", "/projects/{id}/code-index", s.triggerProjectCodeIndex)
	both("GET", "/activity/pipeline-runs", s.listPipelineRuns)
	mux.HandleFunc("PUT /api/projects/{id}/acl", s.projectACL)
	mux.HandleFunc("PUT /api/projects/{id}/acl/{agent_id}", s.projectACL)
	mux.HandleFunc("PUT /v1/projects/{id}/overrides", s.projectACL)
	mux.HandleFunc("DELETE /api/projects/{id}/acl/{agent_id}", s.projectACL)
	mux.HandleFunc("GET /api/knowledge", s.knowledge)
	mux.HandleFunc("GET /v1/knowledge/recall-events", s.recallEvents)
	mux.HandleFunc("GET /v1/knowledge/recalls", s.knowledgeRecalls)
	mux.HandleFunc("GET /v1/knowledge/recall-counts", s.knowledgeRecalls)
	both("GET", "/projects/{id}/knowledge", s.knowledge)
	mux.HandleFunc("GET /api/knowledge/{ds}/{doc}", s.document)
	mux.HandleFunc("GET /api/knowledge/{ds}/{doc}/body", s.document)
	mux.HandleFunc("DELETE /api/knowledge/{ds}/{doc}", s.deleteDocument)
	both("GET", "/datasets/{ds}/documents/{doc}", s.document)
	both("GET", "/datasets/{ds}/documents/{doc}/body", s.document)
	mux.HandleFunc("GET /api/raw-records", s.rawRecords)
	both("GET", "/projects/{id}/raw-records", s.rawRecords)
	both("GET", "/projects/{id}/raw-records/sessions/{sid}/children", s.rawRecords)
	both("GET", "/raw-records/session-history", s.rawSessionHistory)
	both("GET", "/datasets/{ds}/sessions/{sid}", s.session)
	mux.HandleFunc("GET /api/common", s.commonList)
	mux.HandleFunc("GET /v1/common/knowledge", s.commonList)
	mux.HandleFunc("GET /api/common/{doc}", s.commonDocument)
	mux.HandleFunc("POST /api/common", s.saveCommon)
	mux.HandleFunc("PATCH /api/common/{doc}", s.saveCommon)
	mux.HandleFunc("PUT /api/common/{doc}", s.saveCommon)
	mux.HandleFunc("DELETE /api/common/{doc}", s.deleteCommon)
	mux.HandleFunc("PUT /api/common/acl/{agent_id}", s.commonACL)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("POST /api/search", s.search)
	mux.HandleFunc("GET /v1/search/metadata", s.search)
	both("GET", "/search/recent", s.searchRecent)
	both("GET", "/reading-state", s.readingState)
	both("POST", "/reading-state/mark-read", s.markReading)
	both("POST", "/reading-state/mark-all-read", s.markReading)
	both("GET", "/graph", s.graph)
	both("GET", "/generation", s.generation)
	both("PUT", "/generation/profiles/{id}", s.saveProfile)
	mux.HandleFunc("POST /api/generation/profiles", s.saveProfile)
	mux.HandleFunc("DELETE /api/generation/profiles/{id}", s.removeProfile)
	mux.HandleFunc("POST /v1/generation/profiles/{id}/delete", s.removeProfile)
	mux.HandleFunc("POST /api/generation/validate", s.validateProfile)
	mux.HandleFunc("POST /v1/generation/validations", s.validateProfile)
	mux.HandleFunc("GET /v1/generation/validations/{op}", s.generationValidation)
	mux.HandleFunc("GET /v1/generation/operations/{op}", s.generationOperation)
	mux.HandleFunc("GET /v1/generation/handoffs", s.serveHandoffs)
	mux.HandleFunc("GET /v1/generation/handoffs/{op}", s.serveHandoffs)
	mux.HandleFunc("POST /v1/generation/handoffs", s.activateProfile)
	mux.HandleFunc("POST /api/generation/activate", s.activateProfile)
	both("PUT", "/generation/next", s.activateProfile)
	both("GET", "/agents", s.listAgents)
	both("GET", "/connections", s.listConnections)
	both("POST", "/agents", s.connectAgent)
	both("POST", "/agents/{id}/retire", s.retireAgent)
	mux.HandleFunc("GET /api/agents/conflicts", s.agentConflicts)
	mux.HandleFunc("POST /api/agents/conflicts", s.agentConflicts)
	mux.HandleFunc("POST /ingest", s.agentOnly(s.ingest))
	mux.HandleFunc("POST /recall", s.agentOnly(s.recall))
	mux.HandleFunc("POST /session-end", s.agentOnly(s.sessionEnd))
	if s.cfg.CompletionHandler != nil {
		mux.Handle(completionPath, s.cfg.CompletionHandler)
	}
	if s.cfg.MCPHandler != nil {
		mux.Handle("/mcp", s.cfg.MCPHandler)
		mux.Handle("/mcp/", s.cfg.MCPHandler)
	}
	if s.cfg.ProfilesHandler != nil {
		mux.Handle("/api/profiles", s.cfg.ProfilesHandler)
		mux.Handle("/api/profiles/", s.cfg.ProfilesHandler)
		profiles := http.StripPrefix("/v1/profiles", s.cfg.ProfilesHandler)
		mux.Handle("/v1/profiles", profiles)
		mux.Handle("/v1/profiles/", profiles)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, origin := range r.Header.Values("Origin") {
			if origin != "" {
				writeError(w, 403, "browser_origin_rejected", "browser origins are rejected")
				return
			}
		}
		cleanPath := path.Clean(r.URL.Path)
		escapedPath := r.URL.EscapedPath()
		completion := r.URL.Path == completionPath && cleanPath == completionPath && escapedPath == completionPath
		// ServeMux cleans the escaped path before decoding route segments.
		muxPath, _ := url.PathUnescape(path.Clean(escapedPath))
		if !completion && (isManagementPath(r.URL.Path) || isManagementPath(cleanPath) || isManagementPath(muxPath)) &&
			!s.management(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func isManagementPath(p string) bool {
	return p == "/api" || p == "/v1" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/")
}

func bearer(r *http.Request) string {
	return agentauth.BearerToken(r)
}

func secretMatch(a, b string) bool {
	return agentauth.SecretMatch(a, b)
}

func (s *Server) matchingAgent(ctx context.Context, tokens ...string) (domain.Agent, string, error) {
	res, err := s.auth.Authenticate(ctx, tokens...)
	if err != nil {
		return domain.Agent{}, "", err
	}
	if res.Status == agentauth.StatusOK {
		return res.Agent, "", nil
	}
	return domain.Agent{}, res.Code, nil
}

func (s *Server) management(w http.ResponseWriter, r *http.Request) bool {
	authVals := r.Header.Values("Authorization")
	mgmtVals := r.Header.Values("X-Memlane-Management")
	apiKeyVals := r.Header.Values("X-Api-Key")
	if len(authVals) > 1 || len(mgmtVals) > 1 || len(apiKeyVals) > 1 {
		writeError(w, 401, "unauthorized", "management credentials required")
		return false
	}
	var token string
	if len(authVals) == 1 {
		fields := strings.Fields(authVals[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeError(w, 401, "unauthorized", "management credentials required")
			return false
		}
		token = fields[1]
	}
	header := ""
	if len(mgmtVals) == 1 {
		header = strings.TrimSpace(mgmtVals[0])
		if header == "" {
			writeError(w, 401, "unauthorized", "management credentials required")
			return false
		}
	}
	agentKey := ""
	if len(apiKeyVals) == 1 {
		agentKey = strings.TrimSpace(apiKeyVals[0])
		if agentKey == "" {
			writeError(w, 401, "unauthorized", "management credentials required")
			return false
		}
	}
	if token != "" && header != "" && !secretMatch(token, header) {
		writeError(w, 401, "unauthorized", "management credentials required")
		return false
	}
	if agentKey != "" && ((token != "" && !secretMatch(agentKey, token)) || (header != "" && !secretMatch(agentKey, header))) {
		writeError(w, 401, "unauthorized", "management credentials required")
		return false
	}
	if secretMatch(token, s.cfg.ManagementToken) || secretMatch(header, s.cfg.ManagementToken) {
		return true
	}
	if token != "" || header != "" || agentKey != "" {
		if _, code, err := s.matchingAgent(r.Context(), token, header, agentKey); err != nil {
			writeDomainError(w, err)
			return false
		} else if code != "agent_not_registered" {
			writeError(w, 403, "management_required", "management credentials required")
			return false
		}
	}
	writeError(w, 401, "unauthorized", "management credentials required")
	return false
}

func (s *Server) agentOnly(f func(http.ResponseWriter, *http.Request, domain.Agent)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := agentauth.RequestToken(r)
		if token == "" {
			writeError(w, 401, "unauthorized", "agent credentials required")
			return
		}
		a, code, err := s.matchingAgent(r.Context(), token)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if code != "" {
			writeError(w, 403, code, "active registered agent required")
			return
		}
		f(w, r, a)
	}
}

func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) Inbox() *InboxWorker   { return s.inbox }

func (s *Server) start(addr string) (string, *loopback, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return "", nil, errors.New("gateway listener requires 127.0.0.1")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return "", nil, errors.New("gateway listener already started")
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &loopback{cancel: cancel, done: make(chan struct{})}
	l.http = &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	s.listener = l
	s.inbox.Start(ctx)
	go func() {
		if err := l.http.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			l.err = err
			cancel()
		}
		close(l.done)
	}()
	return ln.Addr().String(), l, nil
}

func (s *Server) StartLoopback(addr string) (string, error) {
	actual, _, err := s.start(addr)
	return actual, err
}

func (s *Server) ListenAndServeLoopback(ctx context.Context, addr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, l, err := s.start(addr)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-l.done:
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.shutdown(stopCtx, l)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	l := s.listener
	s.mu.Unlock()
	if l == nil {
		return s.inbox.Stop(ctx)
	}
	return s.shutdown(ctx, l)
}

func (s *Server) shutdown(ctx context.Context, l *loopback) error {
	l.stopMu.Lock()
	defer l.stopMu.Unlock()
	s.mu.Lock()
	current := s.listener == l
	s.mu.Unlock()
	if !current {
		return l.err
	}
	if err := l.http.Shutdown(ctx); err != nil {
		l.cancel()
		return err
	}
	if err := s.inbox.Stop(ctx); err != nil {
		l.cancel()
		return err
	}
	l.cancel()
	select {
	case <-l.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.listener = nil
	s.mu.Unlock()
	return l.err
}

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/supervisor"
)

type FirstInstallation struct {
	Schema  int    `json:"schema"`
	StoreID string `json:"store_id"`
	Setup   bool   `json:"setup"`
}

type FirstRuntime struct {
	ConfigPath, StoreRoot, Resources, Python, HostHome string
	Plan                                               Plan
	ActivationFile                                     string
}

type firstRuntimeConfig struct {
	First          *FirstInstallation `json:"first_install"`
	Root           string             `json:"official_data"`
	Commit         string             `json:"service_commit"`
	Python         string             `json:"python"`
	ServiceURL     string             `json:"service_url"`
	CompletionURL  string             `json:"completion_url"`
	TokenFile      string             `json:"token_file"`
	GenerationFile string             `json:"generation_file"`
	GenerationMode string             `json:"generation_mode"`
	Model          string             `json:"model"`
	Control        struct {
		URL          string `json:"url"`
		RegistryFile string `json:"registry_file"`
		TokenFile    string `json:"management_token_file"`
		PluginDir    string `json:"plugin_dir"`
	} `json:"project_control"`

	receipt StoreReceipt
}

// FirstRuntimeOption customizes supervisor parameters used for a first-runtime lifecycle.
type FirstRuntimeOption func(*firstRuntimeOptions)

type firstRuntimeOptions struct {
	logLimitBytes     int64
	monitorInterval   time.Duration
	readyPollInterval time.Duration
	healthFailureMax  int
	healthTimeout     time.Duration
	stopGrace         time.Duration
	killWait          time.Duration
	onCrash           func(supervisor.ServiceStatus)
}

func defaultFirstRuntimeOptions() firstRuntimeOptions {
	return firstRuntimeOptions{
		logLimitBytes:     supervisor.DefaultLogLimitBytes,
		monitorInterval:   time.Second,
		readyPollInterval: 150 * time.Millisecond,
		healthFailureMax:  3,
		healthTimeout:     3 * time.Second,
		stopGrace:         10 * time.Second,
		killWait:          3 * time.Second,
	}
}

// WithFirstRuntimeLogLimit overrides the live-inode log truncation threshold (default 20 MiB).
func WithFirstRuntimeLogLimit(limit int64) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		if limit > 0 {
			o.logLimitBytes = limit
		}
	}
}

// WithFirstRuntimeMonitorInterval overrides the background log-trim and health monitor tick.
func WithFirstRuntimeMonitorInterval(interval time.Duration) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		if interval > 0 {
			o.monitorInterval = interval
		}
	}
}

// WithFirstRuntimeReadyPollInterval overrides the readiness check polling interval.
func WithFirstRuntimeReadyPollInterval(interval time.Duration) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		if interval > 0 {
			o.readyPollInterval = interval
		}
	}
}

// WithFirstRuntimeHealthPolicy overrides debounced health failure count and per-probe timeout.
func WithFirstRuntimeHealthPolicy(failureMax int, timeout time.Duration) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		if failureMax > 0 {
			o.healthFailureMax = failureMax
		}
		if timeout > 0 {
			o.healthTimeout = timeout
		}
	}
}

// WithFirstRuntimeStopTimeouts overrides SIGTERM grace and SIGKILL wait periods during shutdown.
func WithFirstRuntimeStopTimeouts(grace, killWait time.Duration) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		if grace > 0 {
			o.stopGrace = grace
		}
		if killWait > 0 {
			o.killWait = killWait
		}
	}
}

// WithFirstRuntimeOnCrash registers a callback invoked on unexpected child exit.
func WithFirstRuntimeOnCrash(fn func(supervisor.ServiceStatus)) FirstRuntimeOption {
	return func(o *firstRuntimeOptions) {
		o.onCrash = fn
	}
}

// FirstRuntimeHandle exposes the supervisor handle and explicit graceful shutdown hook
// for a first-runtime instance. Callers retain full control over when to terminate
// (for example, explicit application quit rather than macOS window close/hide).
type FirstRuntimeHandle struct {
	configPath string
	storeRoot  string
	owned      bool
	attached   bool
	stopGrace  time.Duration
	killWait   time.Duration
	sup        *supervisor.Supervisor

	mu                sync.Mutex
	cancelStart       context.CancelFunc
	shutdownRequested bool
}

// Supervisor returns the backing supervisor when this handle owns the launched process(es).
func (h *FirstRuntimeHandle) Supervisor() *supervisor.Supervisor {
	if h == nil {
		return nil
	}
	return h.sup
}

// Owned reports whether this process launched and owns the supervised process group(s).
func (h *FirstRuntimeHandle) Owned() bool {
	return h != nil && h.owned && h.sup != nil
}

// Attached reports whether this handle tracks an externally locked owner for the same store/config.
func (h *FirstRuntimeHandle) Attached() bool {
	return h != nil && h.attached
}

func (h *FirstRuntimeHandle) clearCancelStart() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.cancelStart = nil
	h.mu.Unlock()
}

func (h *FirstRuntimeHandle) isShutdownRequested() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shutdownRequested
}

// ConfigPath returns the runtime configuration path bound to this handle.
func (h *FirstRuntimeHandle) ConfigPath() string {
	if h == nil {
		return ""
	}
	return h.configPath
}

// StoreRoot returns the canonical store root bound to this handle.
func (h *FirstRuntimeHandle) StoreRoot() string {
	if h == nil {
		return ""
	}
	return h.storeRoot
}

// Snapshot returns the current aggregate supervisor and child service state.
func (h *FirstRuntimeHandle) Snapshot() supervisor.SupervisorSnapshot {
	if h == nil || h.sup == nil {
		return supervisor.SupervisorSnapshot{State: supervisor.StatePending}
	}
	return h.sup.Snapshot()
}

// Exited returns a notification channel for unexpected child exits (nil if not owned).
func (h *FirstRuntimeHandle) Exited() <-chan supervisor.ServiceStatus {
	if h == nil || h.sup == nil {
		return nil
	}
	return h.sup.Exited()
}

// Shutdown performs an idempotent explicit graceful shutdown (reverse-order SIGTERM -> SIGKILL
// for owned handles, or verified same-store owner shutdown for attached handles), cancels any
// in-flight startup wait, and unregisters the handle.
func (h *FirstRuntimeHandle) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	h.shutdownRequested = true
	cancel := h.cancelStart
	h.cancelStart = nil
	h.mu.Unlock()

	firstRuntimeHandles.unregister(h)
	if h.sup != nil {
		h.sup.MarkStopping()
	}
	if cancel != nil {
		cancel()
	}
	if h.owned && h.sup != nil {
		return h.sup.Stop(ctx)
	}
	if h.attached && h.storeRoot != "" && h.configPath != "" {
		return supervisor.StopVerifiedOwner(ctx, h.storeRoot, h.configPath, h.stopGrace, h.killWait)
	}
	return nil
}

type runtimeHandleRegistry struct {
	mu             sync.Mutex
	handles        map[string]*FirstRuntimeHandle
	active         map[*FirstRuntimeHandle]struct{}
	locks          map[string]*sync.Mutex
	nextStartupID  uint64
	startupCancels map[uint64]context.CancelFunc
}

var firstRuntimeHandles = &runtimeHandleRegistry{
	handles:        make(map[string]*FirstRuntimeHandle),
	active:         make(map[*FirstRuntimeHandle]struct{}),
	locks:          make(map[string]*sync.Mutex),
	startupCancels: make(map[uint64]context.CancelFunc),
}

func (r *runtimeHandleRegistry) storeMutex(canonicalStore string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.locks[canonicalStore]
	if m == nil {
		m = &sync.Mutex{}
		r.locks[canonicalStore] = m
	}
	return m
}

func (r *runtimeHandleRegistry) get(canonicalStore string) *FirstRuntimeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[canonicalStore]
}

func (r *runtimeHandleRegistry) registerInFlight(h *FirstRuntimeHandle) {
	if h == nil || h.storeRoot == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[h] = struct{}{}
	if r.handles[h.storeRoot] == nil {
		r.handles[h.storeRoot] = h
	}
}

func (r *runtimeHandleRegistry) set(h *FirstRuntimeHandle) {
	if h == nil || h.storeRoot == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[h] = struct{}{}
	r.handles[h.storeRoot] = h
}

func (r *runtimeHandleRegistry) unregister(h *FirstRuntimeHandle) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, h)
	if h.storeRoot != "" && r.handles[h.storeRoot] == h {
		delete(r.handles, h.storeRoot)
	}
}

func (r *runtimeHandleRegistry) findByConfig(configPath string) *FirstRuntimeHandle {
	canonicalCfg := canonicalRuntimePath(configPath)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.handles {
		if canonicalRuntimePath(h.configPath) == canonicalCfg {
			return h
		}
	}
	for h := range r.active {
		if canonicalRuntimePath(h.configPath) == canonicalCfg {
			return h
		}
	}
	return nil
}

func (r *runtimeHandleRegistry) all() []*FirstRuntimeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[*FirstRuntimeHandle]struct{}, len(r.active)+len(r.handles))
	out := make([]*FirstRuntimeHandle, 0, len(r.active)+len(r.handles))
	for h := range r.active {
		seen[h] = struct{}{}
		out = append(out, h)
	}
	for _, h := range r.handles {
		if _, ok := seen[h]; !ok {
			out = append(out, h)
		}
	}
	return out
}

func (r *runtimeHandleRegistry) registerStartup(cancel context.CancelFunc) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextStartupID++
	id := r.nextStartupID
	r.startupCancels[id] = cancel
	return id
}

func (r *runtimeHandleRegistry) unregisterStartup(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.startupCancels, id)
}

func (r *runtimeHandleRegistry) cancelAllStartups() {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.startupCancels))
	for _, fn := range r.startupCancels {
		cancels = append(cancels, fn)
	}
	r.mu.Unlock()
	for _, fn := range cancels {
		fn()
	}
}

// TrackStartupContext derives a cancellable startup context that is canceled immediately
// when ShutdownAllFirstRuntimes runs, preventing in-flight setup/launch goroutines from
// spawning orphan background services during application quit.
func TrackStartupContext(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	id := firstRuntimeHandles.registerStartup(cancel)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			firstRuntimeHandles.unregisterStartup(id)
			cancel()
		})
	}
}

func canonicalRuntimePath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if abs, absErr := filepath.Abs(resolved); absErr == nil {
			return abs
		}
		return resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// FirstRuntimeSupervisor returns the active supervised runtime handle associated with configPath, if any.
func FirstRuntimeSupervisor(configPath string) (*FirstRuntimeHandle, bool) {
	if config, err := loadFirstRuntimeConfig(configPath); err == nil {
		if h := firstRuntimeHandles.get(canonicalRuntimePath(config.Root)); h != nil {
			return h, true
		}
	}
	if h := firstRuntimeHandles.findByConfig(configPath); h != nil {
		return h, true
	}
	return nil, false
}

// ShutdownFirstRuntime cleanly shuts down the owned or attached runtime bound to configPath.
func ShutdownFirstRuntime(ctx context.Context, configPath string) error {
	h, ok := FirstRuntimeSupervisor(configPath)
	if !ok {
		return ShutdownExternalRuntime(ctx, configPath)
	}
	return h.Shutdown(ctx)
}

// ShutdownAllFirstRuntimes cancels any in-flight runtime startups and cleanly shuts down
// every owned or attached supervised runtime tracked by this process.
// Intended as the explicit-quit shutdown hook for desktop callers.
func ShutdownAllFirstRuntimes(ctx context.Context) error {
	firstRuntimeHandles.cancelAllStartups()
	handles := firstRuntimeHandles.all()
	var errs []error
	for _, h := range handles {
		if err := h.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ShutdownExternalRuntime stops a verified external owner holding the store referenced by
// configPath (for example, after keep-alive quit + reopen or a pre-existing dev launcher).
func ShutdownExternalRuntime(ctx context.Context, configPath string) error {
	if configPath == "" || !filepath.IsAbs(configPath) {
		return nil
	}
	f, err := os.OpenFile(configPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil
	}
	raw := make([]byte, info.Size())
	if _, err := f.Read(raw); err != nil {
		return nil
	}
	var cfg struct {
		Root string `json:"official_data"`
	}
	if json.Unmarshal(raw, &cfg) != nil || cfg.Root == "" || !filepath.IsAbs(cfg.Root) {
		return nil
	}
	if _, statErr := os.Stat(cfg.Root); errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	opts := defaultFirstRuntimeOptions()
	return supervisor.StopVerifiedOwner(ctx, cfg.Root, configPath, opts.stopGrace, opts.killWait)
}

func DefaultRuntimeConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library/Application Support/Memlane/runtime.json"), nil
}

func loadFirstRuntimeConfig(path string) (firstRuntimeConfig, error) {
	var config firstRuntimeConfig
	b, err := privateRead(path)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return config, err
	}
	if config.First == nil || config.First.Schema != 1 || len(config.First.StoreID) != 32 || !filepath.IsAbs(config.Root) {
		return config, errors.New("已有配置不属于首次安装，保留原配置与内容库")
	}
	b, err = privateRead(filepath.Join(config.Root, "store-identity.json"))
	if err != nil {
		return config, err
	}
	var receipt StoreReceipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		return config, err
	}
	if receipt.Schema != 1 || !receipt.StorageInitialized || receipt.OwnerID == "" || receipt.KeyID == "" || receipt.StoreID != config.First.StoreID || receipt.SourceCommit != config.Commit || config.TokenFile != filepath.Join(config.Root, "owner.token") {
		return config, errors.New("首次运行配置与官方库身份不同，保留原配置")
	}
	config.receipt = receipt
	return config, nil
}

func buildRecoverOptions(config firstRuntimeConfig, configPath string) supervisor.RecoverOptions {
	var checkURLs []string
	for _, raw := range []string{config.ServiceURL, config.CompletionURL, config.Control.URL} {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			checkURLs = append(checkURLs, trimmed)
		}
	}
	var lockFiles []string
	for _, target := range []string{config.GenerationFile, config.Control.RegistryFile} {
		if target == "" || !filepath.IsAbs(target) {
			continue
		}
		if info, err := os.Stat(filepath.Dir(target)); err == nil && info.IsDir() {
			lockFiles = append(lockFiles, target+".lock")
		}
	}
	return supervisor.RecoverOptions{
		StoreRoot:  config.Root,
		ConfigPath: configPath,
		CheckURLs:  checkURLs,
		LockFiles:  lockFiles,
	}
}

// RecoverFirstRuntimeOwner safely archives a stale .memlane-dev-owner.json receipt
// after confirming via internal/supervisor that no recorded process, port, or lock remains active.
func RecoverFirstRuntimeOwner(ctx context.Context, configPath string) (supervisor.RecoverResult, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return supervisor.RecoverResult{}, err
	}
	return supervisor.RecoverStaleOwner(ctx, buildRecoverOptions(config, configPath))
}

// PrepareFirstRuntime never treats a broken existing daily configuration as a
// fresh install. The only SDK initialization path is the explicitly new store.
func (e *Environment) PrepareFirstRuntime(ctx context.Context, configPath string, assets fs.FS) (FirstRuntime, error) {
	var prepared FirstRuntime
	if !filepath.IsAbs(configPath) {
		return prepared, errors.New("首次运行配置需要绝对路径")
	}
	root := filepath.Join(filepath.Dir(configPath), "library")
	_, statErr := os.Lstat(configPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return prepared, statErr
	}
	config, err := loadFirstRuntimeConfig(configPath)
	existing := err == nil
	if err != nil && (statErr == nil || !errors.Is(err, os.ErrNotExist)) {
		return prepared, err
	}
	if existing {
		root = config.Root
		if e.Options.ProtectedStore != root || config.Commit != e.Source.Managed.Spec.Commit {
			return prepared, errors.New("已有内容库绑定不同，不能切换或新建另一套库")
		}
	} else if e.Options.ProtectedStore != "" {
		return prepared, errors.New("已有内容库缺少原运行配置，请恢复配置后继续")
	}
	plan, err := e.Detect(ctx)
	prepared.Plan = plan
	if err != nil {
		return prepared, err
	}
	if !plan.Complete() {
		return prepared, errors.New("环境组件尚未全部准备完成")
	}
	python, err := e.runtimePython(ctx)
	if err != nil {
		return prepared, err
	}
	pluginRequirement, err := e.Requirement("agent-plugin")
	if err != nil {
		return prepared, err
	}
	plugin, err := e.Plugin.Probe(ctx, pluginRequirement)
	if err != nil {
		return prepared, err
	}
	if plugin.Status != Available {
		return prepared, errors.New("固定 Agent 插件尚未准备完成")
	}
	if !existing {
		engineAssets, err := fs.Sub(assets, "engine")
		if err != nil {
			return prepared, err
		}
		if _, err := e.InitializeStore(ctx, root, engineAssets); err != nil {
			return prepared, err
		}
	}
	resources, err := PrepareRuntimeResources(ctx, e.Installer().Root, assets)
	if err != nil {
		return prepared, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return prepared, err
	}
	err = runManaged(ctx, root, python, "-I", "-B", filepath.Join(resources, "engine/python/first_install_runtime.py"),
		"--store-root", root, "--runtime-config", configPath, "--plugin", plugin.Path, "--host-home", home, "--python-executable", python, "--graph-assets", filepath.Join(resources, "web/official-graph/dist"), "--prepare-only")
	if err != nil {
		return prepared, err
	}
	return FirstRuntime{ConfigPath: configPath, StoreRoot: root, Resources: resources, Python: python, HostHome: home, Plan: plan}, nil
}

func loopbackJSON(ctx context.Context, origin, endpoint, tokenFile, header, prefix string) (map[string]any, error) {
	validOrigin, err := supervisor.ValidateLoopbackURL(origin)
	if err != nil {
		return nil, errors.New("首次服务地址必须为明确的本机端口")
	}
	token, err := privateRead(tokenFile)
	if err != nil {
		return nil, err
	}
	result, err := supervisor.LoopbackGetJSON(
		ctx,
		validOrigin,
		endpoint,
		map[string]string{header: prefix + strings.TrimSpace(string(token))},
		3*time.Second,
	)
	if err != nil {
		var statusErr *supervisor.HTTPStatusError
		if errors.As(err, &statusErr) {
			return nil, fmt.Errorf("首次服务身份核对返回 %d", statusErr.StatusCode)
		}
		return nil, err
	}
	return result, nil
}

func FirstRuntimeReady(ctx context.Context, configPath string) (bool, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return false, err
	}
	owner, err := supervisor.VerifyOwnerRecord(ctx, config.Root, configPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if errors.Is(err, supervisor.ErrOwnerMismatch) {
		return false, errors.New("首次服务所有权记录不同，保留现场")
	}
	if errors.Is(err, supervisor.ErrOwnerProcessChanged) {
		return false, errors.New("首次服务所有者已变化，保留原记录等待核对")
	}
	if err != nil {
		return false, err
	}
	if owner.Phase != "first_install_setup" && owner.Phase != "ready" {
		return false, nil
	}
	me, err := loopbackJSON(ctx, config.ServiceURL, "/api/v1/users/me", config.TokenFile, "X-Api-Key", "")
	if err != nil {
		return false, err
	}
	if me["id"] != config.receipt.OwnerID {
		return false, errors.New("首次官方服务 owner 身份不同")
	}
	projects, err := loopbackJSON(ctx, config.Control.URL, "/v1/projects", config.Control.TokenFile, "Authorization", "Bearer ")
	if err != nil {
		return false, err
	}
	if _, ok := projects["revision"].(float64); !ok {
		return false, errors.New("首次管理服务回执不完整")
	}
	return true, nil
}

// StartFirstRuntime keeps the dev owner's existing process and recovery rules
// while delegating owner inspection/recovery, Setsid process-group lifecycle,
// live-inode log truncation/redaction, and readiness/health monitoring to internal/supervisor.
// A timeout does not kill or replace an owner whose startup is still in flight.
func StartFirstRuntime(ctx context.Context, prepared FirstRuntime) error {
	_, err := StartFirstRuntimeHandle(ctx, prepared)
	return err
}

// StartFirstRuntimeHandle starts (or attaches to) the first-runtime service stack
// via internal/supervisor and returns a handle supporting status inspection and explicit graceful shutdown.
func StartFirstRuntimeHandle(ctx context.Context, prepared FirstRuntime, opts ...FirstRuntimeOption) (*FirstRuntimeHandle, error) {
	for _, path := range []string{prepared.ConfigPath, prepared.StoreRoot, prepared.Resources, prepared.Python, prepared.HostHome} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("首次运行参数必须为明确的绝对路径")
		}
	}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		return nil, err
	}
	if config.Root != prepared.StoreRoot || config.Python != prepared.Python {
		return nil, errors.New("首次运行参数与原配置不同，未启动")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared.ActivationFile != "" && prepared.ActivationFile != filepath.Join(prepared.StoreRoot, "first-install-activation.json") {
		return nil, errors.New("首次启用回执位置不同")
	}

	cfgOpts := defaultFirstRuntimeOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfgOpts)
		}
	}

	canonicalStore := canonicalRuntimePath(prepared.StoreRoot)
	storeMu := firstRuntimeHandles.storeMutex(canonicalStore)
	storeMu.Lock()
	defer storeMu.Unlock()

	readyCheck := func(checkCtx context.Context) (bool, error) {
		ready, readyErr := FirstRuntimeReady(checkCtx, prepared.ConfigPath)
		if readyErr != nil && prepared.ActivationFile == "" {
			return false, readyErr
		}
		if prepared.ActivationFile != "" {
			complete, stateErr := FirstInitializationComplete(prepared.ConfigPath)
			if stateErr != nil {
				return false, stateErr
			}
			ready = ready && complete
		}
		return ready, nil
	}
	healthCheck := func(checkCtx context.Context) error {
		ready, readyErr := FirstRuntimeReady(checkCtx, prepared.ConfigPath)
		if readyErr != nil {
			return readyErr
		}
		if !ready {
			return errors.New("first runtime not ready")
		}
		return nil
	}

	existing := firstRuntimeHandles.get(canonicalStore)
	if prepared.ActivationFile != "" && existing != nil && existing.sup != nil && !existing.sup.ExternalOwner() {
		// Release an in-process Go-owned setup supervisor cleanly before handing off to first_install_runtime.py,
		// so dev.py stop never signals the Go host process itself.
		if err := existing.Shutdown(ctx); err != nil {
			return nil, err
		}
		existing = nil
	}

	inspection, err := supervisor.InspectOwner(prepared.StoreRoot)
	if err != nil {
		return nil, err
	}

	if prepared.ActivationFile == "" {
		if existing != nil && existing.Owned() {
			snap := existing.Snapshot()
			if snap.State == supervisor.StateStarting || snap.State == supervisor.StateReady || snap.State == supervisor.StateDegraded {
				// Reuse this process's in-flight or live handle instead of spawning a duplicate
				// launcher (even if an earlier attempt timed out before dev.py acquired the flock).
				if err := existing.sup.WaitReady(ctx, readyCheck); err != nil {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
						return existing, errors.New("首次服务就绪尚未确认，进度及所有权记录已保留")
					}
					var exitErr *supervisor.ExitedBeforeReadyError
					if errors.As(err, &exitErr) {
						_ = existing.Shutdown(context.Background())
						return nil, fmt.Errorf("首次运行入口退出，保留日志与原库：%v", exitErr)
					}
					return nil, err
				}
				return existing, nil
			}
			// Reap any dead monitor before inspecting and recovering the store.
			_ = existing.Shutdown(context.Background())
			existing = nil
		}
		if inspection.Locked {
			// An active external owner already holds .service-owner; never duplicate it,
			// but register an attached handle so explicit stop-and-quit can cleanly stop it.
			waitCtx, waitCancel := context.WithCancel(ctx)
			attached := &FirstRuntimeHandle{
				configPath:  prepared.ConfigPath,
				storeRoot:   canonicalStore,
				owned:       false,
				attached:    true,
				stopGrace:   cfgOpts.stopGrace,
				killWait:    cfgOpts.killWait,
				cancelStart: waitCancel,
			}
			firstRuntimeHandles.set(attached)
			waitErr := supervisor.WaitReady(waitCtx, nil, readyCheck, 0, cfgOpts.readyPollInterval, false)
			attached.clearCancelStart()
			waitCancel()
			if waitErr != nil {
				if attached.isShutdownRequested() {
					firstRuntimeHandles.unregister(attached)
					return nil, context.Canceled
				}
				if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
					return attached, errors.New("首次服务就绪尚未确认，进度及所有权记录已保留")
				}
				firstRuntimeHandles.unregister(attached)
				return nil, waitErr
			}
			return attached, nil
		}
		if existing != nil && !existing.Owned() {
			firstRuntimeHandles.unregister(existing)
			existing = nil
		}
		if inspection.HasReceipt {
			if _, recErr := supervisor.RecoverStaleOwner(ctx, buildRecoverOptions(config, prepared.ConfigPath)); recErr != nil {
				return nil, fmt.Errorf("首次服务所有权恢复未通过，保留现场：%w", recErr)
			}
		}
	} else if !inspection.Locked && inspection.HasReceipt {
		if _, recErr := supervisor.RecoverStaleOwner(ctx, buildRecoverOptions(config, prepared.ConfigPath)); recErr != nil {
			return nil, fmt.Errorf("首次服务所有权恢复未通过，保留现场：%w", recErr)
		}
	}

	logsDir := filepath.Join(filepath.Dir(prepared.ConfigPath), "runtime-logs")
	logPath := filepath.Join(logsDir, "launcher.log")

	arguments := []string{filepath.Join(prepared.Resources, "scripts/dev.py"), "start", "--config", prepared.ConfigPath, "--no-ui"}
	phaseOnReady := "ready"
	if config.First != nil && config.First.Setup && prepared.ActivationFile == "" {
		phaseOnReady = "first_install_setup"
	}
	allowCleanExit := false
	if prepared.ActivationFile != "" {
		arguments = []string{
			"-I", "-B", filepath.Join(prepared.Resources, "engine/python/first_install_runtime.py"),
			"--store-root", prepared.StoreRoot,
			"--runtime-config", prepared.ConfigPath,
			"--plugin", config.Control.PluginDir,
			"--host-home", prepared.HostHome,
			"--activate-file", prepared.ActivationFile,
		}
		allowCleanExit = true
	}

	envPath := os.Getenv("PATH")
	if envPath == "" {
		envPath = "/usr/bin:/bin:/usr/sbin:/sbin"
	}
	launcherEnv := []string{
		"HOME=" + prepared.HostHome,
		"PATH=" + filepath.Join(prepared.HostHome, ".local/bin") + ":" + envPath,
		"LANG=en_US.UTF-8",
		"PYTHONDONTWRITEBYTECODE=1",
	}

	sup := supervisor.New(supervisor.Config{
		StoreRoot:         prepared.StoreRoot,
		ConfigPath:        prepared.ConfigPath,
		PhaseOnReady:      phaseOnReady,
		ServiceURL:        config.ServiceURL,
		CompletionURL:     config.CompletionURL,
		ControlURL:        config.Control.URL,
		GenerationMode:    config.GenerationMode,
		LogDir:            logsDir,
		LogLimitBytes:     cfgOpts.logLimitBytes,
		MonitorInterval:   cfgOpts.monitorInterval,
		ReadyPollInterval: cfgOpts.readyPollInterval,
		HealthFailureMax:  cfgOpts.healthFailureMax,
		HealthTimeout:     cfgOpts.healthTimeout,
		ExternalOwner:     true,
		KeepOnTimeout:     true,
		OnCrash:           cfgOpts.onCrash,
	})

	spec := supervisor.ServiceSpec{
		Role:           "launcher",
		Command:        prepared.Python,
		Args:           arguments,
		Dir:            prepared.Resources,
		Env:            launcherEnv,
		LogPath:        logPath,
		LogLimitBytes:  cfgOpts.logLimitBytes,
		ReadyCheck:     readyCheck,
		HealthCheck:    healthCheck,
		AllowCleanExit: allowCleanExit,
		StopGrace:      cfgOpts.stopGrace,
		KillWait:       cfgOpts.killWait,
	}

	startCtx, startCancel := context.WithCancel(ctx)
	handle := &FirstRuntimeHandle{
		configPath:  prepared.ConfigPath,
		storeRoot:   canonicalStore,
		owned:       true,
		stopGrace:   cfgOpts.stopGrace,
		killWait:    cfgOpts.killWait,
		sup:         sup,
		cancelStart: startCancel,
	}
	// Register immediately before sup.Start spawns the Setsid child so explicit quit
	// during the first-startup window cancels and terminates it cleanly without orphans.
	firstRuntimeHandles.registerInFlight(handle)

	startErr := sup.Start(startCtx, []supervisor.ServiceSpec{spec})
	handle.clearCancelStart()
	startCancel()

	if handle.isShutdownRequested() {
		firstRuntimeHandles.unregister(handle)
		return nil, context.Canceled
	}
	if startErr != nil {
		snap := sup.Snapshot()
		if (errors.Is(startErr, context.DeadlineExceeded) || errors.Is(startErr, context.Canceled)) && snap.State != supervisor.StateStopped {
			// Startup remains in flight; preserve child, ownership record, and handle for retry or explicit quit.
			firstRuntimeHandles.set(handle)
			return handle, errors.New("首次服务就绪尚未确认，进度及所有权记录已保留")
		}
		firstRuntimeHandles.unregister(handle)
		var exitErr *supervisor.ExitedBeforeReadyError
		if errors.As(startErr, &exitErr) {
			return nil, fmt.Errorf("首次运行入口退出，保留日志与原库：%v", exitErr)
		}
		return nil, startErr
	}

	// If activation exited cleanly (idempotent already-completed activation), keep the existing live handle
	// if present rather than replacing it with an already-exited launcher supervisor.
	snap := sup.Snapshot()
	if allowCleanExit && len(snap.Services) == 1 && snap.Services[0].State == supervisor.StateStopped {
		firstRuntimeHandles.unregister(handle)
		_ = sup.Stop(context.Background())
		if existing != nil && existing.Owned() {
			exSnap := existing.Snapshot()
			if exSnap.State != supervisor.StateStopped && exSnap.State != supervisor.StateExited {
				return existing, nil
			}
		}
		attached := &FirstRuntimeHandle{
			configPath: prepared.ConfigPath,
			storeRoot:  canonicalStore,
			owned:      false,
			attached:   true,
			stopGrace:  cfgOpts.stopGrace,
			killWait:   cfgOpts.killWait,
		}
		firstRuntimeHandles.set(attached)
		return attached, nil
	}

	firstRuntimeHandles.set(handle)
	if existing != nil && existing != handle {
		// Old setup launcher was already stopped during activation; clean up its monitor goroutine.
		if existing.Owned() {
			_ = existing.Shutdown(context.Background())
		} else {
			firstRuntimeHandles.unregister(existing)
		}
	}
	return handle, nil
}

// StartFirstRuntimeSupervised directly supervises an ordered set of service specs under
// Go-owned .service-owner flock, compatible .memlane-dev-owner.json, live-inode 20 MiB log
// trimming, debounced health checks, and reverse-order SIGTERM -> SIGKILL shutdown.
func StartFirstRuntimeSupervised(ctx context.Context, configPath string, specs []supervisor.ServiceSpec, opts ...FirstRuntimeOption) (*FirstRuntimeHandle, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cfgOpts := defaultFirstRuntimeOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfgOpts)
		}
	}

	canonicalStore := canonicalRuntimePath(config.Root)
	storeMu := firstRuntimeHandles.storeMutex(canonicalStore)
	storeMu.Lock()
	defer storeMu.Unlock()

	inspection, err := supervisor.InspectOwner(config.Root)
	if err != nil {
		return nil, err
	}
	if inspection.Locked {
		return nil, supervisor.ErrOwnerLocked
	}
	if inspection.HasReceipt {
		if _, recErr := supervisor.RecoverStaleOwner(ctx, buildRecoverOptions(config, configPath)); recErr != nil {
			return nil, fmt.Errorf("首次服务所有权恢复未通过，保留现场：%w", recErr)
		}
	}

	phaseOnReady := "ready"
	if config.First != nil && config.First.Setup {
		phaseOnReady = "first_install_setup"
	}
	logsDir := filepath.Join(filepath.Dir(configPath), "runtime-logs")
	sup := supervisor.New(supervisor.Config{
		StoreRoot:         config.Root,
		ConfigPath:        configPath,
		PhaseOnReady:      phaseOnReady,
		ServiceURL:        config.ServiceURL,
		CompletionURL:     config.CompletionURL,
		ControlURL:        config.Control.URL,
		GenerationMode:    config.GenerationMode,
		LogDir:            logsDir,
		LogLimitBytes:     cfgOpts.logLimitBytes,
		MonitorInterval:   cfgOpts.monitorInterval,
		ReadyPollInterval: cfgOpts.readyPollInterval,
		HealthFailureMax:  cfgOpts.healthFailureMax,
		HealthTimeout:     cfgOpts.healthTimeout,
		ExternalOwner:     false,
		OnCrash:           cfgOpts.onCrash,
	})

	effectiveSpecs := make([]supervisor.ServiceSpec, len(specs))
	for i, s := range specs {
		if s.StopGrace <= 0 {
			s.StopGrace = cfgOpts.stopGrace
		}
		if s.KillWait <= 0 {
			s.KillWait = cfgOpts.killWait
		}
		if s.LogLimitBytes <= 0 {
			s.LogLimitBytes = cfgOpts.logLimitBytes
		}
		effectiveSpecs[i] = s
	}

	startCtx, startCancel := context.WithCancel(ctx)
	handle := &FirstRuntimeHandle{
		configPath:  configPath,
		storeRoot:   canonicalStore,
		owned:       true,
		stopGrace:   cfgOpts.stopGrace,
		killWait:    cfgOpts.killWait,
		sup:         sup,
		cancelStart: startCancel,
	}
	firstRuntimeHandles.registerInFlight(handle)
	err = sup.Start(startCtx, effectiveSpecs)
	handle.clearCancelStart()
	startCancel()
	if handle.isShutdownRequested() {
		firstRuntimeHandles.unregister(handle)
		return nil, context.Canceled
	}
	if err != nil {
		firstRuntimeHandles.unregister(handle)
		return nil, err
	}
	firstRuntimeHandles.set(handle)
	return handle, nil
}

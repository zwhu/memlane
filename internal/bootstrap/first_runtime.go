package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	GraphAssets    json.RawMessage    `json:"graph_assets"`
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
	return e.prepareFirstRuntime(ctx, configPath, assets, false)
}

// PrepareFirstRuntimeForUpgrade stages bundled resources for an existing store
// without rewriting its runtime configuration.
func (e *Environment) PrepareFirstRuntimeForUpgrade(ctx context.Context, configPath string, assets fs.FS) (FirstRuntime, error) {
	return e.prepareFirstRuntime(ctx, configPath, assets, true)
}

func (e *Environment) prepareFirstRuntime(ctx context.Context, configPath string, assets fs.FS, upgrade bool) (FirstRuntime, error) {
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
	if err != nil && (upgrade || statErr == nil || !errors.Is(err, os.ErrNotExist)) {
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
	var resources string
	if existing {
		resources, err = PrepareRuntimeResources(ctx, e.Installer().Root, assets)
		if err != nil {
			return prepared, err
		}
	} else {
		store, err := e.InitializeStore(ctx, root, assets)
		if err != nil {
			return prepared, err
		}
		resources = store.Resources
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return prepared, err
	}
	args := []string{
		"-I", "-B", filepath.Join(resources, "engine/python/first_install_runtime.py"),
		"--store-root", root,
		"--runtime-config", configPath,
		"--plugin", plugin.Path,
		"--host-home", home,
		"--python-executable", python,
		"--graph-assets", filepath.Join(resources, "web/official-graph/dist"),
		"--installation-root", e.Installer().Root,
		"--prepare-only",
	}
	if upgrade {
		args = append(args, "--skip-graph-refresh")
	}
	if err := runManaged(ctx, root, python, args...); err != nil {
		return prepared, err
	}
	return FirstRuntime{ConfigPath: configPath, StoreRoot: root, Resources: resources, Python: python, HostHome: home, Plan: plan}, nil
}

func loopbackJSON(ctx context.Context, origin, endpoint, tokenFile, header, prefix string) (map[string]any, error) {
	validOrigin, err := supervisor.ValidateLoopbackURL(origin)
	if err != nil {
		return nil, fatalFirstRuntimeReadiness{errors.New("首次服务地址必须为明确的本机端口")}
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

// Configuration and identity errors cannot be resolved by waiting for the APIs.
type fatalFirstRuntimeReadiness struct{ error }

func (err fatalFirstRuntimeReadiness) Unwrap() error { return err.error }

func FirstRuntimeReady(ctx context.Context, configPath string) (bool, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return false, fatalFirstRuntimeReadiness{err}
	}
	owner, err := supervisor.VerifyOwnerRecord(ctx, config.Root, configPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if errors.Is(err, supervisor.ErrOwnerMismatch) {
		return false, fatalFirstRuntimeReadiness{fmt.Errorf("首次服务所有权记录不同，保留现场: %w", err)}
	}
	if errors.Is(err, supervisor.ErrOwnerProcessChanged) {
		return false, fatalFirstRuntimeReadiness{fmt.Errorf("首次服务所有者已变化，保留原记录等待核对: %w", err)}
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
		return false, fatalFirstRuntimeReadiness{errors.New("首次官方服务 owner 身份不同")}
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
	config, err := validateFirstRuntimeLaunch(prepared)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared.ActivationFile != "" && prepared.ActivationFile != filepath.Join(prepared.StoreRoot, "first-install-activation.json") {
		return nil, errors.New("首次启用回执位置不同")
	}

	cfgOpts := resolveFirstRuntimeOptions(opts)

	canonicalStore := canonicalRuntimePath(prepared.StoreRoot)
	storeMu := firstRuntimeHandles.storeMutex(canonicalStore)
	storeMu.Lock()
	defer storeMu.Unlock()
	upgradeLock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		return nil, err
	}
	defer upgradeLock.Release()
	return startFirstRuntimeHandleLocked(ctx, prepared, config, cfgOpts, canonicalStore)
}

// startFirstRuntimeHandleLocked requires both the store mutex and upgrade lock.
func startFirstRuntimeHandleLocked(ctx context.Context, prepared FirstRuntime, config firstRuntimeConfig, cfgOpts firstRuntimeOptions, canonicalStore string) (*FirstRuntimeHandle, error) {
	var sup *supervisor.Supervisor
	var completedReplay bool
	if prepared.ActivationFile != "" {
		var request FirstActivationRequest
		if b, err := privateRead(prepared.ActivationFile); err == nil && json.Unmarshal(b, &request) == nil {
			completedReplay = request.Schema == 1 && request.StoreID == config.First.StoreID && request.Status == "completed"
		}
	}
	readyCheck := func(checkCtx context.Context) (bool, error) {
		if prepared.ActivationFile != "" {
			services := sup.Snapshot().Services
			if len(services) == 1 && services[0].State == supervisor.StateStopped &&
				services[0].ExitCode != nil && *services[0].ExitCode == 0 && services[0].Signal == "" {
				return true, nil
			}
			// A completed replay must finish checking its receipt, even if another
			// launcher is already serving this store.
			if completedReplay {
				return false, nil
			}
		}
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
	if prepared.ActivationFile != "" && !completedReplay && existing != nil && existing.sup != nil && !existing.sup.ExternalOwner() {
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

	sup = supervisor.New(supervisor.Config{
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
		Env:            firstRuntimeLauncherEnv(prepared.HostHome),
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

	// A completed activation replay can exit without starting a daemon.
	snap := sup.Snapshot()
	if allowCleanExit && len(snap.Services) == 1 && snap.Services[0].State == supervisor.StateStopped {
		firstRuntimeHandles.unregister(handle)
		_ = sup.Stop(context.Background())
		insp, err := supervisor.InspectOwnerReadOnly(prepared.StoreRoot)
		if err != nil {
			return nil, err
		}
		ready, readyErr := FirstRuntimeReady(ctx, prepared.ConfigPath)
		if existing != nil && existing.Owned() {
			exSnap := existing.Snapshot()
			if exSnap.State != supervisor.StateStopped && exSnap.State != supervisor.StateExited && ready && readyErr == nil {
				return existing, nil
			}
		}
		if !insp.Locked || !ready || readyErr != nil {
			normalPrepared := prepared
			normalPrepared.ActivationFile = ""
			reloaded, err := validateFirstRuntimeLaunch(normalPrepared)
			if err != nil {
				return nil, err
			}
			return startFirstRuntimeHandleLocked(ctx, normalPrepared, reloaded, cfgOpts, canonicalStore)
		}
		if existing != nil && existing.Attached() {
			return existing, nil
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

	cfgOpts := resolveFirstRuntimeOptions(opts)

	canonicalStore := canonicalRuntimePath(config.Root)
	storeMu := firstRuntimeHandles.storeMutex(canonicalStore)
	storeMu.Lock()
	defer storeMu.Unlock()
	upgradeLock, err := supervisor.AcquireUpgradeLock(config.Root)
	if err != nil {
		return nil, err
	}
	defer upgradeLock.Release()
	return startFirstRuntimeSupervisedLocked(ctx, configPath, config, specs, cfgOpts, canonicalStore)
}

// startFirstRuntimeSupervisedLocked requires both the store mutex and upgrade lock.
func startFirstRuntimeSupervisedLocked(ctx context.Context, configPath string, config firstRuntimeConfig, specs []supervisor.ServiceSpec, cfgOpts firstRuntimeOptions, canonicalStore string) (*FirstRuntimeHandle, error) {
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

func validateFirstRuntimeLaunch(prepared FirstRuntime) (firstRuntimeConfig, error) {
	for _, path := range []string{prepared.ConfigPath, prepared.StoreRoot, prepared.Resources, prepared.Python, prepared.HostHome} {
		if !filepath.IsAbs(path) {
			return firstRuntimeConfig{}, errors.New("首次运行参数必须为明确的绝对路径")
		}
	}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		return config, err
	}
	if config.Root != prepared.StoreRoot || config.Python != prepared.Python {
		return config, errors.New("首次运行参数与原配置不同，未启动")
	}
	return config, nil
}

func resolveFirstRuntimeOptions(opts []FirstRuntimeOption) firstRuntimeOptions {
	cfgOpts := defaultFirstRuntimeOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfgOpts)
		}
	}
	return cfgOpts
}

func firstRuntimeLauncherEnv(home string) []string {
	envPath := os.Getenv("PATH")
	if envPath == "" {
		envPath = "/usr/bin:/bin:/usr/sbin:/sbin"
	}
	return []string{
		"HOME=" + home,
		"PATH=" + filepath.Join(home, ".local/bin") + ":" + envPath,
		"LANG=en_US.UTF-8",
		"PYTHONDONTWRITEBYTECODE=1",
	}

}

// RuntimeUpgradeReceipt records a verified owner replacement and its download evidence.
type RuntimeUpgradeReceipt struct {
	Schema              int    `json:"schema"`
	StoreID             string `json:"store_id"`
	FromRuntimeHash     string `json:"from_runtime_hash,omitempty"`
	ToRuntimeHash       string `json:"to_runtime_hash"`
	OwnerFingerprint    string `json:"owner_fingerprint"`
	CompletedAt         string `json:"completed_at"`
	ZeroDownloads       bool   `json:"zero_downloads"`
	PriorWorkerRuntime  string `json:"prior_worker_runtime_hash,omitempty"`
	PriorWorkerPID      int    `json:"prior_worker_pid,omitempty"`
	PriorWorkerStarted  string `json:"prior_worker_started,omitempty"`
	PriorWorkerNetBytes int64  `json:"prior_worker_network_bytes,omitempty"`
	PriorWorkerSyncRan  bool   `json:"prior_worker_runtime_sync_ran,omitempty"`
}

// ZeroDownloadCheck describes whether the current runtime generation required downloads.
type ZeroDownloadCheck struct {
	Satisfied              bool                   `json:"satisfied"`
	Reason                 string                 `json:"reason,omitempty"`
	CurrentRuntimeHash     string                 `json:"current_runtime_hash,omitempty"`
	NetworkBytesDownloaded int64                  `json:"network_bytes_downloaded"`
	RuntimeSyncRan         bool                   `json:"runtime_sync_ran"`
	UpgradeReceipt         *RuntimeUpgradeReceipt `json:"upgrade_receipt,omitempty"`
}

// RuntimeUpgradeReceiptPath returns the receipt location for this runtime configuration.
func RuntimeUpgradeReceiptPath(componentRoot, configPath string) (string, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return "", err
	}
	return runtimeUpgradeReceiptPath(filepath.Join(root, "installation"), configPath), nil
}

func runtimeUpgradeReceiptPath(installationRoot, configPath string) string {
	return filepath.Join(installationRoot, "upgrade-receipt-"+selectionBinding(configPath)[:16]+".json")
}

// ReadRuntimeUpgradeReceipt reads and validates the retained upgrade receipt.
func ReadRuntimeUpgradeReceipt(componentRoot, configPath string) (RuntimeUpgradeReceipt, error) {
	path, err := RuntimeUpgradeReceiptPath(componentRoot, configPath)
	if err != nil {
		return RuntimeUpgradeReceipt{}, err
	}
	return readRuntimeUpgradeReceipt(path)
}

func readRuntimeUpgradeReceipt(path string) (RuntimeUpgradeReceipt, error) {
	data, err := privateRead(path)
	if err != nil {
		return RuntimeUpgradeReceipt{}, err
	}
	var receipt RuntimeUpgradeReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return RuntimeUpgradeReceipt{}, err
	}
	if receipt.Schema != 1 || receipt.StoreID == "" || receipt.ToRuntimeHash == "" || receipt.OwnerFingerprint == "" {
		return RuntimeUpgradeReceipt{}, errors.New("invalid runtime upgrade receipt")
	}
	return receipt, nil
}

// EvaluateZeroDownloads checks whether installation and upgrade evidence support zero downloads.
func EvaluateZeroDownloads(plan Plan, journal Journal, hasJournal bool, worker InstallWorkerState, hasWorker bool, bundledHash string, upgrade *RuntimeUpgradeReceipt) ZeroDownloadCheck {
	check := ZeroDownloadCheck{CurrentRuntimeHash: bundledHash, UpgradeReceipt: upgrade}
	switch {
	case !plan.Complete():
		check.Reason = "components_incomplete"
	case hasJournal && (journal.Status == Installing || journal.ActiveStep != ""):
		check.Reason = "installation_in_progress"
	case hasWorker && (worker.State == "starting" || worker.State == "running"):
		check.Reason = "install_worker_active"
	}
	if check.Reason != "" {
		return check
	}
	if hasWorker && (bundledHash == "" || worker.RuntimeHash == "" || worker.RuntimeHash == bundledHash) {
		check.NetworkBytesDownloaded = worker.NetworkBytesDownloaded
		check.RuntimeSyncRan = worker.RuntimeSyncRan
		if check.NetworkBytesDownloaded > 0 || check.RuntimeSyncRan {
			check.Reason = "current_generation_downloaded"
			return check
		}
	}
	if upgrade != nil && (bundledHash == "" || upgrade.ToRuntimeHash == bundledHash) && !upgrade.ZeroDownloads {
		check.Reason = "upgrade_used_downloads"
		return check
	}
	check.Satisfied = true
	return check
}

type OwnerRuntimeStatus struct {
	State                string                  `json:"state"`
	OwnerLocked          bool                    `json:"owner_locked"`
	HasReceipt           bool                    `json:"has_receipt"`
	OwnerPID             int                     `json:"owner_pid,omitempty"`
	OwnerStarted         string                  `json:"owner_started,omitempty"`
	OwnerPhase           string                  `json:"owner_phase,omitempty"`
	OwnerFingerprint     string                  `json:"owner_fingerprint,omitempty"`
	RunningRuntimeDir    string                  `json:"running_runtime_dir,omitempty"`
	RunningRuntimeHash   string                  `json:"running_runtime_hash,omitempty"`
	BundledRuntimeHash   string                  `json:"bundled_runtime_hash,omitempty"`
	StaleVersionDetected bool                    `json:"stale_version_detected"`
	GraphAssetsPath      string                  `json:"graph_assets_path,omitempty"`
	GraphAssetsStale     bool                    `json:"graph_assets_stale"`
	DeveloperCheckout    bool                    `json:"developer_checkout,omitempty"`
	UpgradeReceipt       *RuntimeUpgradeReceipt  `json:"upgrade_receipt,omitempty"`
	UpgradeReceiptError  string                  `json:"upgrade_receipt_error,omitempty"`
	Record               *supervisor.OwnerRecord `json:"-"`
}

var processArgsFn = supervisor.ProcessArgs

// InspectOwnerRuntimeStatus compares the verified owner with bundled assets without writing files.
func InspectOwnerRuntimeStatus(ctx context.Context, configPath, componentRoot string, assets fs.FS) (OwnerRuntimeStatus, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return OwnerRuntimeStatus{}, err
	}
	var bundledHash string
	if assets != nil {
		bundledHash, err = BundledRuntimeHash(assets)
		if err != nil {
			return OwnerRuntimeStatus{}, err
		}
	}
	return inspectOwnerRuntimeStatus(ctx, configPath, filepath.Join(root, "installation"), bundledHash)
}

// resolveRuntimePath resolves existing prefixes, including when installation
// has yet to be created or an old generation has been removed.
func resolveRuntimePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveRuntimePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func inspectOwnerRuntimeStatus(ctx context.Context, configPath, installationRoot, bundledHash string) (OwnerRuntimeStatus, error) {
	status := OwnerRuntimeStatus{BundledRuntimeHash: bundledHash}
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return status, err
	}
	installationRoot, err = resolveRuntimePath(installationRoot)
	if err != nil {
		return status, err
	}
	receipt, err := readRuntimeUpgradeReceipt(runtimeUpgradeReceiptPath(installationRoot, configPath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		status.UpgradeReceiptError = RedactInstallerText(err.Error())
	}
	if err == nil && receipt.StoreID == config.First.StoreID && (bundledHash == "" || receipt.ToRuntimeHash == bundledHash) {
		status.UpgradeReceipt = &receipt
	}
	_ = json.Unmarshal(config.GraphAssets, &status.GraphAssetsPath)
	if graphHash, ok := managedGraphAssetsHash(installationRoot, status.GraphAssetsPath); ok {
		status.GraphAssetsStale = bundledHash != "" && graphHash != bundledHash
	}
	inspection, err := supervisor.InspectOwnerReadOnly(config.Root)
	if err != nil {
		return status, err
	}
	status.OwnerLocked, status.HasReceipt = inspection.Locked, inspection.HasReceipt
	switch {
	case !inspection.Locked && !inspection.HasReceipt:
		status.State = "stopped"
	case !inspection.Locked:
		status.State = "stale_owner"
	case !inspection.HasReceipt:
		status.State = "starting"
	default:
		rec, err := supervisor.VerifyOwnerRecord(ctx, config.Root, configPath)
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		fingerprint := supervisor.OwnerFingerprint(rec)
		if err != nil || fingerprint != supervisor.OwnerFingerprint(inspection.Record) {
			status.State = "mismatch"
			return status, nil
		}
		status.OwnerPID, status.OwnerStarted, status.OwnerPhase = rec.PID, rec.Started, rec.Phase
		status.OwnerFingerprint, status.Record = fingerprint, &rec
		detectOwnerRuntime(&status, installationRoot, rec)
		status.StaleVersionDetected = !status.DeveloperCheckout && bundledHash != "" &&
			status.RunningRuntimeHash != "" && status.RunningRuntimeHash != bundledHash
		switch {
		case status.StaleVersionDetected:
			status.State = "stale_runtime"
		case rec.Phase == "starting":
			status.State = "starting"
		default:
			status.State = "ready"
		}
	}
	return status, nil
}

func detectOwnerRuntime(status *OwnerRuntimeStatus, root string, rec supervisor.OwnerRecord) {
	pids := []int{rec.PID}
	for _, pid := range rec.ServiceChildren {
		pids = append(pids, pid)
	}
	sort.Ints(pids[1:])
	for _, pid := range pids {
		argv, err := processArgsFn(pid)
		if err != nil {
			continue
		}
		for _, arg := range argv {
			if !filepath.IsAbs(arg) {
				continue
			}
			clean := filepath.Clean(arg)
			if !strings.HasSuffix(clean, "/scripts/dev.py") && !strings.Contains(clean, "/engine/python/") {
				continue
			}
			resolved, err := resolveRuntimePath(clean)
			if err != nil {
				continue
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
				status.DeveloperCheckout = true
				continue
			}
			generation, _, ok := strings.Cut(relative, "/")
			if hash, managed := RuntimeHashFromDir(generation); ok && managed && status.RunningRuntimeDir == "" {
				status.RunningRuntimeDir, status.RunningRuntimeHash = filepath.Join(root, generation), hash
			}
		}
	}
	if status.RunningRuntimeDir != "" || status.DeveloperCheckout || !filepath.IsAbs(rec.ResourcesDir) {
		return
	}
	resolved, err := filepath.EvalSymlinks(rec.ResourcesDir)
	if err != nil || filepath.Dir(resolved) != root {
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return
	}
	hash, ok := RuntimeHashFromDir(resolved)
	if ok && (rec.ResourcesHash == "" || rec.ResourcesHash == hash ||
		(len(rec.ResourcesHash) == 64 && strings.HasPrefix(rec.ResourcesHash, hash))) {
		status.RunningRuntimeDir, status.RunningRuntimeHash = resolved, hash
	}
}

func RestartStaleRuntimeOwner(ctx context.Context, prepared FirstRuntime, expectedFingerprint string, assets fs.FS, opts ...FirstRuntimeOption) (handle *FirstRuntimeHandle, err error) {
	options := resolveFirstRuntimeOptions(opts)
	err = restartStaleRuntimeOwner(ctx, prepared, expectedFingerprint, assets, options, func(prepared FirstRuntime, config firstRuntimeConfig, canonicalStore string) error {
		var startErr error
		handle, startErr = startFirstRuntimeHandleLocked(ctx, prepared, config, options, canonicalStore)
		return startErr
	})
	return handle, err
}

func RestartStaleRuntimeOwnerDetached(ctx context.Context, prepared FirstRuntime, expectedFingerprint string, assets fs.FS) error {
	return restartStaleRuntimeOwner(ctx, prepared, expectedFingerprint, assets, defaultFirstRuntimeOptions(), func(prepared FirstRuntime, config firstRuntimeConfig, _ string) error {
		return startFirstRuntimeDetachedLocked(ctx, prepared, config)
	})
}

func restartStaleRuntimeOwner(ctx context.Context, prepared FirstRuntime, fingerprint string, assets fs.FS, options firstRuntimeOptions, start func(FirstRuntime, firstRuntimeConfig, string) error) error {
	if strings.TrimSpace(fingerprint) == "" {
		return errors.New("restart requires an owner fingerprint")
	}
	if _, err := validateFirstRuntimeLaunch(prepared); err != nil {
		return err
	}
	if prepared.ActivationFile != "" {
		return errors.New("runtime restart cannot perform activation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	canonicalStore := canonicalRuntimePath(prepared.StoreRoot)
	storeMu := firstRuntimeHandles.storeMutex(canonicalStore)
	storeMu.Lock()
	defer storeMu.Unlock()
	lock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		return err
	}
	defer lock.Release()

	installationRoot := filepath.Dir(filepath.Clean(prepared.Resources))
	if err := validateManagedResourcePath(installationRoot); err != nil {
		return err
	}
	bundledHash, managed := RuntimeHashFromDir(prepared.Resources)
	if assets != nil {
		bundledHash, err = BundledRuntimeHash(assets)
		if err != nil {
			return err
		}
	} else if !managed {
		return errors.New("restart requires a prepared managed runtime generation")
	}
	target := filepath.Join(installationRoot, "runtime-"+bundledHash)
	if err := validateManagedResourcePath(target); err != nil {
		return err
	}
	status, err := inspectOwnerRuntimeStatus(ctx, prepared.ConfigPath, installationRoot, bundledHash)
	if err != nil {
		return err
	}
	if status.OwnerFingerprint != fingerprint {
		return supervisor.ErrOwnerFingerprintMismatch
	}
	if !status.StaleVersionDetected || status.DeveloperCheckout || status.OwnerPhase == "starting" {
		return errors.New("restart requires a stale managed runtime owner that has finished starting")
	}
	if assets == nil {
		script, err := os.Stat(filepath.Join(prepared.Resources, "scripts/dev.py"))
		if err != nil || !script.Mode().IsRegular() {
			return errors.New("prepared runtime launcher is missing or invalid")
		}
	} else if filepath.Clean(prepared.Resources) != target || !verifyPreparedRuntimeResources(target, assets) {
		prepared.Resources, err = PrepareRuntimeResources(ctx, installationRoot, assets)
		if err != nil {
			return err
		}
	} else {
		prepared.Resources = target
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h := firstRuntimeHandles.get(canonicalStore)
	if h != nil && h.Owned() && h.sup != nil && !h.sup.ExternalOwner() && status.OwnerPID == os.Getpid() {
		if err := h.Shutdown(ctx); err != nil {
			return err
		}
	} else {
		opts := supervisor.StopOwnerOptions{
			StoreRoot: prepared.StoreRoot, ConfigPath: prepared.ConfigPath,
			Grace: options.stopGrace, KillWait: options.killWait, ExpectedFingerprint: fingerprint,
		}
		if err := supervisor.StopVerifiedOwnerBound(ctx, opts, status.OwnerPID, status.OwnerStarted); err != nil {
			return err
		}
		if h != nil {
			if h.Owned() {
				if err := h.Shutdown(ctx); err != nil {
					return err
				}
			} else {
				firstRuntimeHandles.unregister(h)
			}
		}
	}
	if _, err := RefreshManagedGraphAssets(prepared.ConfigPath, installationRoot, prepared.Resources); err != nil {
		return err
	}
	config, err := validateFirstRuntimeLaunch(prepared)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := start(prepared, config, canonicalStore); err != nil {
		return err
	}
	post, err := inspectOwnerRuntimeStatus(ctx, prepared.ConfigPath, installationRoot, bundledHash)
	if err != nil {
		return fmt.Errorf("verify restarted runtime generation: %w", err)
	}
	if err := verifyRestartedRuntimeGeneration(post, bundledHash); err != nil {
		return err
	}
	worker, workerErr := ReadInstallWorkerState(filepath.Dir(installationRoot))
	receipt := RuntimeUpgradeReceipt{
		Schema: 1, StoreID: config.First.StoreID, FromRuntimeHash: status.RunningRuntimeHash,
		ToRuntimeHash: bundledHash, OwnerFingerprint: post.OwnerFingerprint,
		CompletedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		ZeroDownloads: errors.Is(workerErr, os.ErrNotExist),
	}
	if workerErr == nil {
		receipt.ZeroDownloads = (worker.RuntimeHash != "" && worker.RuntimeHash != bundledHash) ||
			(worker.NetworkBytesDownloaded == 0 && !worker.RuntimeSyncRan)
		receipt.PriorWorkerRuntime = worker.RuntimeHash
		receipt.PriorWorkerPID = worker.PID
		receipt.PriorWorkerStarted = worker.Started
		receipt.PriorWorkerNetBytes = worker.NetworkBytesDownloaded
		receipt.PriorWorkerSyncRan = worker.RuntimeSyncRan
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return atomicPrivateFile(runtimeUpgradeReceiptPath(installationRoot, prepared.ConfigPath), data)
}

func verifyRestartedRuntimeGeneration(post OwnerRuntimeStatus, bundledHash string) error {
	if !post.OwnerLocked || !post.HasReceipt || post.State != "ready" || post.StaleVersionDetected ||
		post.GraphAssetsStale || post.DeveloperCheckout || post.RunningRuntimeHash != bundledHash {
		return fmt.Errorf("restarted runtime generation mismatch: state=%s running=%q want=%q", post.State, post.RunningRuntimeHash, bundledHash)
	}
	return nil
}

// StartFirstRuntimeDetached leaves the launcher running if readiness outlasts ctx.
func StartFirstRuntimeDetached(ctx context.Context, prepared FirstRuntime) error {
	config, err := validateFirstRuntimeLaunch(prepared)
	if err != nil {
		return err
	}
	if prepared.ActivationFile != "" {
		return errors.New("detached runtime startup cannot perform activation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	storeMu := firstRuntimeHandles.storeMutex(canonicalRuntimePath(prepared.StoreRoot))
	storeMu.Lock()
	defer storeMu.Unlock()
	upgradeLock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		return err
	}
	defer upgradeLock.Release()
	return startFirstRuntimeDetachedLocked(ctx, prepared, config)
}

// startFirstRuntimeDetachedLocked requires both the store mutex and upgrade lock.
func startFirstRuntimeDetachedLocked(ctx context.Context, prepared FirstRuntime, config firstRuntimeConfig) error {
	inspection, err := supervisor.InspectOwnerReadOnly(prepared.StoreRoot)
	if err != nil {
		return err
	}
	if inspection.Locked {
		return waitFirstRuntimeDetached(ctx, prepared.StoreRoot, prepared.ConfigPath, nil)
	}
	if inspection.HasReceipt {
		if _, err := supervisor.RecoverStaleOwner(ctx, buildRecoverOptions(config, prepared.ConfigPath)); err != nil {
			return err
		}
	}
	logPath := filepath.Join(filepath.Dir(prepared.ConfigPath), "runtime-logs", "launcher.log")
	if err := rejectSymlinkAncestors(filepath.Dir(logPath)); err != nil {
		return err
	}
	logFile, err := supervisor.OpenPrivateLog(logPath)
	if err != nil {
		return err
	}
	if err := supervisor.TrimServiceLog(logPath, supervisor.DefaultLogLimitBytes); err != nil {
		logFile.Close()
		return err
	}

	cmd := exec.Command(prepared.Python, filepath.Join(prepared.Resources, "scripts/dev.py"), "start", "--config", prepared.ConfigPath, "--no-ui")
	cmd.Dir = prepared.Resources
	cmd.Env = firstRuntimeLauncherEnv(prepared.HostHome)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = ctx.Err()
	if err == nil {
		err = cmd.Start()
	}
	logFile.Close()
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return waitFirstRuntimeDetached(ctx, prepared.StoreRoot, prepared.ConfigPath, exited)
}

func waitFirstRuntimeDetached(ctx context.Context, storeRoot, configPath string, exited <-chan error) error {
	ticker := time.NewTicker(defaultFirstRuntimeOptions().readyPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if exited == nil {
			insp, err := supervisor.InspectOwnerReadOnly(storeRoot)
			switch {
			case err != nil:
				return err
			case !insp.Locked:
				return errors.New("competing runtime owner exited before readiness")
			case insp.ReceiptErr != nil:
				return insp.ReceiptErr
			case insp.HasReceipt:
				switch insp.Record.Phase {
				case "", "starting", "first_install_setup", "ready":
				default:
					return fmt.Errorf("competing runtime owner entered unexpected phase %q before readiness", insp.Record.Phase)
				}
			}
		}
		ready, readyErr := FirstRuntimeReady(ctx, configPath)
		if ready {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var fatal fatalFirstRuntimeReadiness
		if errors.As(readyErr, &fatal) {
			return readyErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			if ready, _ := FirstRuntimeReady(ctx, configPath); ready {
				return nil
			}
			insp, inspErr := supervisor.InspectOwnerReadOnly(storeRoot)
			if inspErr == nil && insp.Locked && (!insp.HasReceipt || insp.ReceiptErr == nil) {
				switch insp.Record.Phase {
				case "", "starting", "first_install_setup", "ready":
					exited = nil
					continue
				}
			}
			if err != nil {
				return fmt.Errorf("detached runtime launcher exited before readiness: %w", err)
			}
			return errors.New("detached runtime launcher exited before readiness")
		case <-ticker.C:
		}
	}
}

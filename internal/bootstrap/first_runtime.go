package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
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
	Executable                                         string
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
	executable, err := os.Executable()
	if err != nil {
		return prepared, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return prepared, err
	}
	var resources string
	if existing {
		if _, err := readFirstStoreInput(root, config.receipt); err != nil {
			return prepared, err
		}
		if config.GenerationFile != filepath.Join(root, "generation.json") {
			return prepared, errors.New("首次运行的提炼配置位置不同")
		}
		resources, err = PrepareRuntimeResources(ctx, e.Installer().Root, assets)
		if err != nil {
			return prepared, err
		}
		if !upgrade {
			if _, err := RefreshManagedGraphAssets(configPath, e.Installer().Root, resources); err != nil {
				return prepared, err
			}
		}
	} else {
		store, err := e.InitializeStore(ctx, root, assets)
		if err != nil {
			return prepared, err
		}
		resources, python = store.Resources, store.Python
		if err := writeFirstRuntimeConfig(configPath, store); err != nil {
			return prepared, err
		}
	}
	return FirstRuntime{ConfigPath: configPath, StoreRoot: root, Resources: resources, Python: python, HostHome: home, Executable: executable, Plan: plan}, nil
}

type firstStoreInput struct {
	StoreIdentity
	Root       string `json:"root"`
	Source     string `json:"source"`
	Archive    string `json:"archive"`
	ModelCache string `json:"model_cache"`
}

func readFirstStoreInput(root string, receipt StoreReceipt) (firstStoreInput, error) {
	var input firstStoreInput
	raw, err := privateRead(filepath.Join(root, "first-install-input.json"))
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, err
	}
	if input.Schema != 1 || !receipt.StorageInitialized || input.Root != root || input.StoreID != receipt.StoreID ||
		input.SourceCommit != receipt.SourceCommit || !filepath.IsAbs(input.Source) || !filepath.IsAbs(input.Archive) || !filepath.IsAbs(input.ModelCache) {
		return input, errors.New("首次安装输入与官方库身份不同")
	}
	return input, nil
}

func writeFirstRuntimeConfig(configPath string, store StoreResult) error {
	root := store.Root
	owner, err := supervisor.AcquireOwner(root)
	if err != nil {
		return err
	}
	defer owner.Release()
	if _, err := os.Lstat(configPath); err == nil {
		config, err := loadFirstRuntimeConfig(configPath)
		if err != nil {
			return err
		}
		if config.Root != root || config.First.StoreID != store.Receipt.StoreID || config.GenerationFile != filepath.Join(root, "generation.json") {
			return errors.New("首次运行配置已绑定到不同的内容库")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := privateRead(filepath.Join(root, "store-identity.json"))
	if err != nil {
		return err
	}
	var receipt StoreReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if receipt != store.Receipt || !receipt.StorageInitialized {
		return errors.New("首次安装内容库身份已变化")
	}
	input, err := readFirstStoreInput(root, store.Receipt)
	if err != nil {
		return err
	}
	if err := validatePrivateToken(store.TokenFile); err != nil {
		return err
	}
	environment := filepath.Join(root, "service-environment.json")
	if err := prepareFirstServiceEnvironment(environment, input.ModelCache); err != nil {
		return err
	}

	completionToken, managementToken := filepath.Join(root, "completion.token"), filepath.Join(root, "management.token")
	for _, name := range []string{completionToken, managementToken} {
		if err := validatePrivateToken(name); errors.Is(err, os.ErrNotExist) {
			token, err := firstRuntimeSecret()
			if err != nil {
				return err
			}
			if err := atomicPrivateFile(name, []byte(token)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}

	var origins [3]string
	for i := range origins {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer listener.Close()
		origins[i] = "http://" + listener.Addr().String()
	}
	service, gateway, completion := origins[0], origins[1], origins[2]
	return writeFirstRuntimeJSON(configPath, map[string]any{
		"first_install": FirstInstallation{Schema: 1, StoreID: input.StoreID, Setup: true},
		"store_root":    root, "official_data": root, "official_source": input.Source, "official_archive": input.Archive,
		"engine_work_dir": store.Resources, "python": store.Python, "service_commit": input.SourceCommit,
		"token_file": store.TokenFile, "service_environment_file": environment, "service_url": service, "engine_url": service,
		"completion_url": completion, "completion_token_file": completionToken, "management_token_file": managementToken, "gateway_url": gateway,
		"generation_file": filepath.Join(root, "generation.json"), "generation_mode": "unconfigured", "model": "",
		"client_state_file": filepath.Join(root, "client-state.json"), "graph_assets": filepath.Join(store.Resources, "web/official-graph/dist"),
		"projects": []any{}, "common_datasets": []any{},
		"project_control": map[string]any{
			"url": gateway, "registry_file": filepath.Join(root, "projects.json"), "management_token_file": managementToken,
			"agent_control_token_files": map[string]string{},
		},
	})
}

func readFirstRuntimeSecretFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode() != 0600 {
		return nil, errors.New("首次运行凭据文件需要权限 0600")
	}
	return privateRead(path)
}

func validatePrivateToken(path string) error {
	raw, err := readFirstRuntimeSecretFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return errors.New("首次运行凭据为空")
	}
	return nil
}

func firstRuntimeSecret() (string, error) {
	var secret [48]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

func writeFirstRuntimeJSON(path string, doc any) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicPrivateFile(path, raw)
}

func prepareFirstServiceEnvironment(path, modelCache string) error {
	expected := map[string]string{
		"EMBEDDING_PROVIDER": "fastembed", "EMBEDDING_MODEL": "BAAI/bge-small-zh-v1.5",
		"EMBEDDING_DIMENSIONS": "512", "EMBEDDING_MAX_TOKENS": "512", "HF_HOME": modelCache,
		"HF_HUB_OFFLINE": "1", "DEFAULT_USER_EMAIL": "memlane-local@example.com",
	}
	secretNames := []string{"FASTAPI_USERS_JWT_SECRET", "FASTAPI_USERS_RESET_PASSWORD_TOKEN_SECRET", "FASTAPI_USERS_VERIFICATION_TOKEN_SECRET"}
	raw, err := readFirstRuntimeSecretFile(path)
	if errors.Is(err, os.ErrNotExist) {
		for _, name := range secretNames {
			secret, err := firstRuntimeSecret()
			if err != nil {
				return err
			}
			expected[name] = secret
		}
		return writeFirstRuntimeJSON(path, expected)
	}
	if err != nil {
		return err
	}
	var existing map[string]string
	if err := json.Unmarshal(raw, &existing); err != nil {
		return err
	}
	if len(existing) != len(expected)+len(secretNames) {
		return errors.New("首次运行环境字段不同")
	}
	for name, value := range expected {
		if existing[name] != value {
			return errors.New("首次运行向量模型配置不同")
		}
	}
	for _, name := range secretNames {
		if strings.TrimSpace(existing[name]) == "" {
			return errors.New("首次运行环境凭据缺失")
		}
	}
	return nil
}

func firstRuntimeLaunchCommand(prepared FirstRuntime) (command string, args []string, err error) {
	command = prepared.Executable
	if command == "" {
		command, err = os.Executable()
	}
	if err == nil && !filepath.IsAbs(command) {
		err = errors.New("首次运行入口必须为绝对路径")
	}
	return command, []string{"daemon", "--config", prepared.ConfigPath}, err
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
			return nil, fmt.Errorf("首次服务身份核对返回 %d: %w", statusErr.StatusCode, err)
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
	health, err := loopbackJSON(ctx, config.Control.URL, "/api/health", config.Control.TokenFile, "Authorization", "Bearer ")
	if err == nil {
		return health["ok"] == true && health["engine_ok"] == true, nil
	}
	var statusErr *supervisor.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != 404 {
		return false, err
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
	if prepared.ActivationFile != "" {
		return startFirstRuntimeActivation(ctx, prepared, config, cfgOpts, canonicalStore)
	}
	readyCheck := func(checkCtx context.Context) (bool, error) {
		return FirstRuntimeReady(checkCtx, prepared.ConfigPath)
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
	inspection, err := supervisor.InspectOwner(prepared.StoreRoot)
	if err != nil {
		return nil, err
	}

	if existing != nil && existing.Owned() {
		snap := existing.Snapshot()
		if snap.State == supervisor.StateStarting || snap.State == supervisor.StateReady || snap.State == supervisor.StateDegraded {
			// Reuse this process's in-flight or live handle instead of spawning a duplicate
			// launcher (even if an earlier attempt timed out before the daemon acquired the flock).
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

	logsDir := filepath.Join(filepath.Dir(prepared.ConfigPath), "runtime-logs")
	logPath := filepath.Join(logsDir, "launcher.log")

	command, arguments, err := firstRuntimeLaunchCommand(prepared)
	if err != nil {
		return nil, err
	}
	phaseOnReady := "ready"
	if config.First.Setup {
		phaseOnReady = "first_install_setup"
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
		Role:          "launcher",
		Command:       command,
		Args:          arguments,
		Dir:           prepared.Resources,
		Env:           firstRuntimeLauncherEnv(prepared.HostHome, prepared.Resources),
		LogPath:       logPath,
		LogLimitBytes: cfgOpts.logLimitBytes,
		ReadyCheck:    readyCheck,
		HealthCheck:   healthCheck,
		StopGrace:     cfgOpts.stopGrace,
		KillWait:      cfgOpts.killWait,
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

	firstRuntimeHandles.set(handle)
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

func firstRuntimeLauncherEnv(home, resources string) []string {
	envPath := os.Getenv("PATH")
	if envPath == "" {
		envPath = "/usr/bin:/bin:/usr/sbin:/sbin"
	}
	return []string{
		"HOME=" + home,
		"MEMLANE_RUNTIME_RESOURCES=" + resources,
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
			if !strings.Contains(clean, "/engines/cognee/") {
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
		script, err := os.Stat(filepath.Join(prepared.Resources, "engines/cognee/runner.py"))
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
	if err := validateManagedResourcePath(filepath.Dir(logPath)); err != nil {
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

	command, args, err := firstRuntimeLaunchCommand(prepared)
	if err != nil {
		logFile.Close()
		return err
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = prepared.Resources
	cmd.Env = firstRuntimeLauncherEnv(prepared.HostHome, prepared.Resources)
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

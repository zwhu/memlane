package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/engine/cognee"
	"github.com/zwhu/memlane/core/gateway"
	"github.com/zwhu/memlane/core/gateway/completion"
	"github.com/zwhu/memlane/core/gateway/mcp"
	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/core/profiles"
	"github.com/zwhu/memlane/internal/bootstrap"
	owners "github.com/zwhu/memlane/internal/supervisor"
	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/daemon"
)

type StackConfig struct {
	Store                           *domain.Store
	Engine                          cognee.Config
	ManagementToken                 string
	CompletionToken                 string
	BridgeEndpoint, BridgeTokenFile string
	ProbeRuntime                    map[string]any
	AgentTokens                     map[string]string
	DaemonStatus                    func(context.Context) map[string]any
	DiscoveryOptions          agents.DiscoveryOptions
	OutputLanguage            string
	SessionExtractQuietWindow time.Duration
	SessionExtractMaxWait     time.Duration
	SessionExtractBatchSize   int
	DatasetExtractCooldown    time.Duration
}

type GatewayStack struct {
	Gateway  *gateway.Server
	Profiles *profiles.Service
}

type engineWithoutProjector struct {
	engine.MemoryEngine
	engine.ModelController
}

type activeProfileGate struct {
	store     *domain.Store
	discovery agents.DiscoveryOptions
}

func (g activeProfileGate) ResolveActive(ctx context.Context) (*completion.ActiveProfile, error) {
	state, err := g.store.GetGenerationState(ctx)
	if err != nil {
		return nil, err
	}
	if state.ActiveProfileID == "" {
		return nil, nil
	}
	p, err := g.store.GetGenerationProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
	if err != nil {
		return nil, err
	}
	if p.Removed || p.Mode != "agent_bridge" {
		return nil, nil
	}
	profile := &completion.ActiveProfile{
		Mode:                p.Mode,
		BridgeKind:          p.BridgeKind,
		BridgeProvider:      p.BridgeProvider,
		RouteModel:          p.Model,
		FollowAgentDefaults: true,
	}
	if p.BridgeKind != "" {
		opts := g.discovery
		opts.Kinds = []string{p.BridgeKind}
		if hosts := agents.DetectHosts(opts); len(hosts) > 0 {
			profile.Executable = hosts[0].Executable
			profile.AuthHome = hosts[0].ProfileDir
		}
		if profile.AuthHome == "" {
			profile.AuthHome = agents.DefaultProfileDir(p.BridgeKind, g.discovery)
		}
	}
	return profile, nil
}

func MountGatewayStack(cfg StackConfig) (*GatewayStack, error) {
	adapter, err := cognee.New(cfg.Engine)
	if err != nil {
		return nil, err
	}
	mcpServer, err := mcp.NewServer(mcp.Config{Store: cfg.Store, Engine: adapter})
	if err != nil {
		return nil, err
	}
	if cfg.CompletionToken == "" {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return nil, err
		}
		cfg.CompletionToken = hex.EncodeToString(secret[:])
	}
	completions, err := completion.NewServer(completion.Config{
		Token: cfg.CompletionToken,
		Gate:  activeProfileGate{store: cfg.Store, discovery: cfg.DiscoveryOptions},
	})
	if err != nil {
		return nil, err
	}
	profileService, err := profiles.NewService(profiles.Config{Store: cfg.Store, Engine: adapter})
	if err != nil {
		return nil, err
	}
	var memEngine engine.MemoryEngine = adapter
	var projector engine.CatalogProjector
	if cfg.Engine.StorePath != "" {
		projector = adapter
	} else {
		memEngine = engineWithoutProjector{MemoryEngine: adapter, ModelController: adapter}
	}
	server, err := gateway.NewServer(gateway.Config{
		Store: cfg.Store, Engine: memEngine, Models: adapter, Projector: projector,
		ManagementToken: cfg.ManagementToken, AgentTokens: cfg.AgentTokens, DaemonStatus: cfg.DaemonStatus,
		DiscoveryOptions: cfg.DiscoveryOptions,
		CompletionHandler: completions.Handler(), MCPHandler: mcpServer.Handler(), ProfilesHandler: profileService.Handler(),
		OutputLanguage: cfg.OutputLanguage, BridgeEndpoint: cfg.BridgeEndpoint, BridgeTokenFile: cfg.BridgeTokenFile,
		ProbeRuntime: cfg.ProbeRuntime,
		SessionExtractQuietWindow: cfg.SessionExtractQuietWindow,
		SessionExtractMaxWait:     cfg.SessionExtractMaxWait,
		SessionExtractBatchSize:   cfg.SessionExtractBatchSize,
		DatasetExtractCooldown:    cfg.DatasetExtractCooldown,
	})
	if err != nil {
		return nil, errors.Join(err, profileService.Close(context.Background()))
	}
	mcpServer.SetRememberService(server.MCPRememberService())
	mcpServer.SetRegistryLocker(server.RegistryLocker())
	return &GatewayStack{Gateway: server, Profiles: profileService}, nil
}

func runDaemon(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime configuration")
	listen := flags.String("listen", "", "IPv4 loopback listen address")
	endpoint := flags.String("engine-url", "", "existing engine URL")
	internalKey := flags.String("engine-token-file", "", "engine bearer token file")
	store := flags.String("store-root", "", "store root")
	managementKey := flags.String("management-token-file", "", "gateway management token file")
	noSpawn := flags.Bool("no-engine-spawn", false, "attach to an existing engine")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 {
		return 2
	}
	cfg, err := readRuntime(*path)
	if err != nil {
		return reportError(stderr, err)
	}
	cfg.StoreRoot = firstNonempty(*store, cfg.StoreRoot)
	cfg.EngineURL = firstNonempty(*endpoint, cfg.EngineURL)
	cfg.EngineTokenFile = firstNonempty(*internalKey, cfg.EngineTokenFile)
	cfg.ManagementTokenFile = firstNonempty(*managementKey, cfg.ManagementTokenFile)
	if *listen != "" {
		cfg.GatewayURL = "http://" + *listen
	}
	if err := cfg.validateStore(); err != nil {
		return reportError(stderr, err)
	}
	engineAddr, err := loopbackAddress(cfg.EngineURL)
	if err != nil {
		return reportError(stderr, fmt.Errorf("engine URL: %w", err))
	}
	cfg.EngineURL = "http://" + engineAddr
	gatewayAddr, err := loopbackAddress(cfg.GatewayURL)
	if err != nil {
		return reportError(stderr, err)
	}
	cfg.GatewayURL = "http://" + gatewayAddr
	if (*noSpawn || *endpoint != "") && cfg.EngineTokenFile == "" {
		return reportError(stderr, errors.New("external engine token file is required"))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serveDaemon(ctx, cfg, *noSpawn || *endpoint != "", stdout); err != nil {
		return reportError(stderr, err)
	}
	return 0
}

func serveDaemon(ctx context.Context, cfg runtimeConfig, attach bool, stdout io.Writer) (err error) {
	if cur, rlimitErr := daemon.EnsureFileDescriptorLimit(daemon.DefaultTargetNoFileLimit); rlimitErr != nil {
		log.Printf("daemon rlimit nofile raise skipped: current=%d err=%v", cur, rlimitErr)
	}
	management, err := hooks.ReadPrivateKey(cfg.ManagementTokenFile)
	if err != nil {
		return fmt.Errorf("management token: %w", err)
	}
	var completionToken string
	if cfg.CompletionTokenFile != "" {
		completionToken, err = hooks.ReadPrivateKey(cfg.CompletionTokenFile)
		if err != nil {
			return fmt.Errorf("completion token: %w", err)
		}
	}
	if resources := os.Getenv("MEMLANE_RUNTIME_RESOURCES"); resources != "" {
		if !filepath.IsAbs(resources) {
			return errors.New("runtime resource directory must be absolute")
		}
		if _, ok := bootstrap.RuntimeHashFromDir(resources); !ok {
			return errors.New("runtime resource directory must be a managed generation")
		}
		cfg.EngineWorkDir = resources
	}
	if !attach && cfg.Python == "" {
		return errors.New("managed Python executable is required")
	}

	endpoints := []string{cfg.GatewayURL}
	if !attach {
		endpoints = append(endpoints, cfg.EngineURL)
	}
	daemonConfig, err := daemonOwnerConfig(cfg, endpoints)
	if err != nil {
		return err
	}
	supervisor := daemon.New(daemonConfig)
	if err := supervisor.StartOwner(ctx); err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = errors.Join(err, supervisor.Stop(stop))
	}()

	// P0 + P2 #1: sweep stale daemon-run directories only after holding the
	// exclusive .service-owner flock, and before staging this run's directories.
	cleanupStaleDaemonRunDirs(cfg.StoreRoot)

	if cfg.CompletionTokenFile == "" {
		dir, tempErr := canonicalStoreRuntimeDir(cfg.StoreRoot, "memlane-completion-")
		if tempErr != nil {
			return tempErr
		}
		defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		completionToken = hex.EncodeToString(secret[:])
		cfg.CompletionTokenFile = filepath.Join(dir, "completion.token")
		if err := os.WriteFile(cfg.CompletionTokenFile, []byte(completionToken), 0o600); err != nil {
			return err
		}
	}
	probeRuntime, err := buildProbeRuntime(cfg)
	if err != nil {
		return err
	}
	stackCfg := StackConfig{ManagementToken: management, CompletionToken: completionToken,
		BridgeTokenFile: cfg.CompletionTokenFile, OutputLanguage: cfg.OutputLanguage,
		BridgeEndpoint: strings.TrimRight(cfg.GatewayURL, "/") + "/v1", ProbeRuntime: probeRuntime}

	if cfg.EngineWorkDir == "" && (!attach || cfg.Python != "") {
		folder, tempErr := canonicalStoreRuntimeDir(cfg.StoreRoot, "memlane-engine-code-")
		if tempErr != nil {
			return tempErr
		}
		defer func() { err = errors.Join(err, os.RemoveAll(folder)) }()
		cfg.EngineWorkDir, err = bootstrap.PrepareRuntimeResources(ctx, folder, memlane.GoEngineAssets)
		if err != nil {
			return err
		}
		if updatedConfig, cfgErr := daemonOwnerConfig(cfg, endpoints); cfgErr != nil {
			return cfgErr
		} else if len(updatedConfig.OwnerMetadata) > 0 {
			if metaErr := supervisor.UpdateOwnerMetadata(updatedConfig.OwnerMetadata); metaErr != nil {
				return metaErr
			}
		}
	}
	var specs []daemon.ServiceSpec
	if !attach {
		spec, _, cleanup, prepareErr := engineService(&cfg)
		if prepareErr != nil {
			return prepareErr
		}
		defer func() { err = errors.Join(err, cleanup()) }()
		specs = append(specs, spec)
	}
	stackCfg.Engine = cognee.Config{Endpoint: cfg.EngineURL, TokenFile: cfg.EngineTokenFile,
		StorePath: firstNonempty(cfg.OfficialData, cfg.StoreRoot), PythonBin: cfg.Python, WorkDir: cfg.EngineWorkDir}

	stackCfg.DaemonStatus = func(context.Context) map[string]any {
		s := supervisor.Snapshot()
		return map[string]any{"owner_pid": s.OwnerPID, "phase": s.Phase, "state": s.State, "services": s.Services}
	}
	// Coalesce active-session Q&As into debounced session flushes and space out
	// per-dataset extracts to prevent high-frequency Ladybug CHECKPOINT bursts.
	stackCfg.SessionExtractQuietWindow = 60 * time.Second
	stackCfg.SessionExtractMaxWait = 5 * time.Minute
	stackCfg.SessionExtractBatchSize = 8
	stackCfg.DatasetExtractCooldown = 15 * time.Second
	dbPath := filepath.Join(cfg.StoreRoot, "memlane.sqlite3")
	if err := importLegacyStore(cfg, dbPath); err != nil {
		return err
	}
	store, err := domain.OpenStore(dbPath)
	if err != nil {
		return err
	}
	stackCfg.Store = store
	defer func() { err = errors.Join(err, store.Close()) }()
	stack, err := MountGatewayStack(stackCfg)
	if err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = errors.Join(err, stack.Gateway.Shutdown(stop), stack.Profiles.Close(stop))
	}()

	// P1 #7: gate InboxWorker consumption until the target engine is verified
	// ready and startup preparation completes so new captures stay pending.
	stack.Gateway.SetManagedEngineGate(false)

	address, err := loopbackAddress(cfg.GatewayURL)
	if err != nil {
		return err
	}
	if _, err := stack.Gateway.StartLoopback(address); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Memlane gateway listening on %s\n", address); err != nil {
		return err
	}

	// P1 #6 + R2 #2: pre-register a single persistent background preparation
	// worker before starting async services. Track engine readiness and generation
	// under prepMu so degraded->ready re-opens the inbox gate, engine exit during
	// prepareGatewayStartup never opens the gate, and attach retries until reachable.
	startupCtx, cancelStartup := context.WithCancel(ctx)
	readyCh := make(chan struct{}, 1)
	notifyPrep := func() {
		select {
		case readyCh <- struct{}{}:
		default:
		}
	}
	var prepMu sync.Mutex
	var engineReady bool
	var engineGen uint64
	var prepared bool
	var startupWG sync.WaitGroup
	startupWG.Add(1)
	defer func() {
		cancelStartup()
		startupWG.Wait()
	}()
	go func() {
		defer startupWG.Done()
		if attach {
			retryTicker := time.NewTicker(2 * time.Second)
			defer retryTicker.Stop()
			for {
				if startupCtx.Err() != nil {
					return
				}
				if !stack.Gateway.VerifyEngineReachable(startupCtx) {
					if startupCtx.Err() == nil {
						stack.Gateway.RecordStartupEngineFailure(startupCtx, errors.New("attached engine is unreachable"))
					}
				} else if prepErr := prepareGatewayStartup(startupCtx, stack.Gateway); prepErr != nil {
					if startupCtx.Err() == nil {
						log.Printf("gateway startup preparation failed: %v", prepErr)
						stack.Gateway.RecordStartupPrepareFailure(startupCtx, prepErr)
					}
				} else {
					stack.Gateway.ClearStartupPrepareFailure()
					stack.Gateway.SetManagedEngineGate(true)
					stack.Gateway.NotifyEngineStatus(true, "")
					stack.Gateway.Inbox().Notify()
					return
				}
				select {
				case <-startupCtx.Done():
					return
				case <-retryTicker.C:
				}
			}
		}

		for {
			select {
			case <-startupCtx.Done():
				return
			case <-readyCh:
			}
			prepMu.Lock()
			gen := engineGen
			isReady := engineReady
			isPrepared := prepared
			prepMu.Unlock()
			if !isReady {
				continue
			}
			if !isPrepared {
				if prepErr := prepareGatewayStartup(startupCtx, stack.Gateway); prepErr != nil {
					if startupCtx.Err() == nil {
						log.Printf("gateway startup preparation failed: %v", prepErr)
						stack.Gateway.RecordStartupPrepareFailure(startupCtx, prepErr)
					}
					continue
				}
				prepMu.Lock()
				stillReady := engineGen == gen && engineReady
				if stillReady {
					prepared = true
				}
				prepMu.Unlock()
				if !stillReady {
					continue
				}
				stack.Gateway.ClearStartupPrepareFailure()
				stack.Gateway.SetManagedEngineGate(true)
				stack.Gateway.NotifyEngineStatus(true, "")
				stack.Gateway.Inbox().Notify()
				continue
			}
			prepMu.Lock()
			stillReady := engineGen == gen && engineReady
			prepMu.Unlock()
			if stillReady {
				stack.Gateway.SetManagedEngineGate(true)
				stack.Gateway.NotifyEngineStatus(true, "")
				stack.Gateway.Inbox().Notify()
			}
		}
	}()

	if attach {
		if err := supervisor.StartServicesAsync(startupCtx, nil, nil); err != nil {
			return err
		}
	} else {
		err = supervisor.StartServicesAsync(startupCtx, specs, func(st daemon.ServiceStatus) {
			if st.Role != "cognee-engine" {
				return
			}
			switch st.State {
			case daemon.StateReady:
				prepMu.Lock()
				engineGen++
				engineReady = true
				prepMu.Unlock()
				notifyPrep()
			case daemon.StateExited, daemon.StateDegraded:
				prepMu.Lock()
				engineGen++
				engineReady = false
				prepMu.Unlock()
				stack.Gateway.SetManagedEngineGate(false)
				msg := strings.TrimSpace(st.LastError)
				if msg == "" {
					msg = fmt.Sprintf("cognee-engine %s", st.State)
				}
				stack.Gateway.RecordStartupEngineFailure(startupCtx, errors.New(msg))
			}
		})
		if err != nil {
			return err
		}
	}

	return stack.Gateway.WaitLoopback(ctx)
}

func prepareGatewayStartup(ctx context.Context, server *gateway.Server) error {
	if err := server.ApplyActiveGeneration(ctx); err != nil {
		server.RecordStartupGenerationFailure(ctx, err)
		log.Printf("generation startup failed (extraction paused until model is verified): %s", err)
	}
	started := time.Now()
	stats, err := server.SyncCatalog(ctx)
	if errors.Is(err, os.ErrNotExist) {
		stats = &engine.SyncStats{}
	} else if err != nil {
		return errors.New("initial catalog projection failed")
	}
	log.Printf("catalog startup: datasets=%d docs=%d sessions=%d duration=%s",
		stats.Datasets, stats.DocsProjected, stats.SessionsProjected, time.Since(started).Round(time.Millisecond))
	return nil
}

// daemonOwnerConfig keeps first-install owners visible to bootstrap across process boundaries.
// Daily/dev stores (first_install == nil) use .daemon-owner.json; first-install stores
// (first_install != nil) use .memlane-dev-owner.json via the unified owners.OwnerCodec.
func daemonOwnerConfig(cfg runtimeConfig, endpoints []string) (daemon.Config, error) {
	config := daemon.Config{StoreRoot: cfg.StoreRoot, ConfigPath: cfg.Path, Endpoints: endpoints, LogDir: filepath.Join(cfg.StoreRoot, "logs")}
	var first struct {
		Installation        *bootstrap.FirstInstallation `json:"first_install"`
		CompletionURL       string                       `json:"completion_url"`
		GenerationMode      string                       `json:"generation_mode"`
		GenerationReference *owners.GenerationReference  `json:"generation_reference"`
	}
	if err := json.Unmarshal(cfg.raw, &first); err != nil {
		return config, err
	}
	if first.Installation == nil {
		return config, nil
	}
	hash, _ := bootstrap.RuntimeHashFromDir(cfg.EngineWorkDir)
	if first.Installation.Setup {
		config.PhaseOnReady = "first_install_setup"
	}
	config.Endpoints = []string{cfg.EngineURL, first.CompletionURL, cfg.GatewayURL}
	config.OwnerMetadata = owners.OwnerMetadata{
		ResourcesDir:        cfg.EngineWorkDir,
		ResourcesHash:       hash,
		RuntimeConfig:       cfg.Path,
		GenerationMode:      first.GenerationMode,
		GenerationReference: first.GenerationReference,
	}.Encode()
	config.LockConfig = owners.FirstInstallLockConfig()
	return config, nil
}

func loopbackAddress(raw string) (string, error) {
	if err := hooks.ValidateGatewayURL(raw); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return "127.0.0.1:" + u.Port(), nil
}

func buildProbeRuntime(cfg runtimeConfig) (map[string]any, error) {
	data := cfg.raw
	if cfg.EngineConfig != "" {
		var err error
		data, err = os.ReadFile(cfg.EngineConfig)
		if err != nil {
			return nil, err
		}
	}
	if len(data) == 0 {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	base := firstNonempty(cfg.EngineConfig, cfg.Path)
	out := map[string]any{}
	for _, key := range []string{"official_source", "official_archive", "verify_cache_path"} {
		field, ok := raw[key]
		if !ok {
			continue
		}
		var path string
		if err := json.Unmarshal(field, &path); err != nil {
			return nil, err
		}
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) && base != "" {
			path = filepath.Join(filepath.Dir(base), path)
		}
		out[key] = path
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func engineService(cfg *runtimeConfig) (daemon.ServiceSpec, string, func() error, error) {
	var spec daemon.ServiceSpec
	cleanup := func() error { return nil }
	var raw map[string]json.RawMessage
	data := cfg.raw
	if cfg.EngineConfig != "" {
		var err error
		data, err = os.ReadFile(cfg.EngineConfig)
		if err != nil {
			return spec, "", cleanup, err
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return spec, "", cleanup, err
	}
	if raw == nil {
		return spec, "", cleanup, errors.New("invalid engine configuration")
	}
	if cfg.EngineTokenFile == "" && cfg.EngineConfig != "" {
		var path string
		if err := json.Unmarshal(raw["internal_token_file"], &path); err != nil {
			return spec, "", cleanup, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(cfg.EngineConfig), path)
		}
		cfg.EngineTokenFile = path
	}
	folder, err := canonicalStoreRuntimeDir(cfg.StoreRoot, "memlane-engine-")
	if err != nil {
		return spec, "", cleanup, err
	}
	cleanup = func() error { return os.RemoveAll(folder) }
	fail := func(err error) (daemon.ServiceSpec, string, func() error, error) {
		return spec, "", cleanup, errors.Join(err, cleanup())
	}
	if cfg.EngineTokenFile == "" {
		cfg.EngineTokenFile = filepath.Join(folder, "internal.token")
		var key [32]byte
		if _, err := rand.Read(key[:]); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(cfg.EngineTokenFile, []byte(hex.EncodeToString(key[:])), 0o600); err != nil {
			return fail(err)
		}
	}
	token, err := hooks.ReadPrivateKey(cfg.EngineTokenFile)
	if err != nil {
		return fail(err)
	}
	// The child takes the pinned source/archive and service environment from the runtime.
	values := map[string]string{"internal_token_file": cfg.EngineTokenFile, "official_data": firstNonempty(cfg.OfficialData, cfg.StoreRoot), "service_url": cfg.EngineURL}
	for _, key := range []string{"official_source", "official_archive", "service_environment_file", "verify_cache_path", "generation_applied_file"} {
		if field, ok := raw[key]; ok {
			var path string
			if err := json.Unmarshal(field, &path); err != nil {
				return fail(err)
			}
			if path != "" && !filepath.IsAbs(path) {
				base := firstNonempty(cfg.EngineConfig, cfg.Path)
				values[key] = filepath.Join(filepath.Dir(base), path)
			}
		}
	}
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fail(err)
		}
		raw[key] = encoded
	}
	delete(raw, "port")
	// The Go store owns startup generation. Applying in the child would reuse
	// the same operation ID before the migrated profile and bridge are available.
	delete(raw, "generation_config")
	out, err := json.Marshal(raw)
	if err != nil {
		return fail(err)
	}
	path := filepath.Join(folder, "runner.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fail(err)
	}
	return daemon.ServiceSpec{
		Role: "cognee-engine", Command: cfg.Python, Args: []string{"-s", "-B", "-m", "engines.cognee.runner", "--config", path},
		Dir: cfg.EngineWorkDir, BindURL: cfg.EngineURL, HealthURL: cfg.EngineURL + "/internal/v1/health",
		HealthHeaders: map[string]string{"Authorization": "Bearer " + token}, LogPath: filepath.Join(cfg.StoreRoot, "logs", "cognee-engine.log"),
		ReadyTimeout: 90 * time.Second, StopGrace: 16 * time.Second, KillWait: 2 * time.Second,
	}, folder, cleanup, nil
}

func importLegacyStore(cfg runtimeConfig, dbPath string) error {
	if _, err := os.Lstat(dbPath); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Legacy daily installations commonly kept control metadata beside runtime.json.
	names := []string{"project-registry.json", "generation.json", "generation-handoff-runtime.json", "user-profiles.json",
		"tool-graph-control-jobs.json", "tool-graph-service-jobs.json", "tool-graph-trace-watermarks.json", "registry-sessions.sqlite3",
		"lesson-provenance.sqlite3", "knowledge-recall.sqlite3", "ui-reading-state.sqlite3", "capture-receipts.sqlite3", "session-drift.sqlite3"}
	roots := []string{cfg.StoreRoot}
	if cfg.Control.RegistryFile != "" {
		legacy := filepath.Dir(cfg.localPath(cfg.Control.RegistryFile))
		if legacy != cfg.StoreRoot {
			roots = append(roots, legacy)
		}
	}
	for _, root := range roots {
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
				_, err = domain.ImportLegacyDailyStore(root, dbPath)
				return err
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

var managedDaemonRunPrefixes = []string{
	"memlane-completion-",
	"memlane-engine-code-",
	"memlane-engine-",
}

func isManagedDaemonRunEntry(name string) bool {
	for _, prefix := range managedDaemonRunPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// openSafeDaemonRunRoot anchors storeRoot -> cache -> daemon-run via fd-backed
// os.OpenRoot handles so directory creation and cleanup are immune to symlink
// swaps and path re-resolution TOCTOU.
func openSafeDaemonRunRoot(storeRoot string, create bool) (*os.Root, string, error) {
	resolvedStore, err := filepath.EvalSymlinks(storeRoot)
	if err != nil {
		return nil, "", err
	}
	canonicalStore, err := filepath.Abs(resolvedStore)
	if err != nil {
		return nil, "", err
	}
	storeInfo, err := os.Stat(canonicalStore)
	if err != nil {
		return nil, "", err
	}
	if !storeInfo.IsDir() {
		return nil, "", errors.New("store root must be a directory")
	}
	storeRootFS, err := os.OpenRoot(canonicalStore)
	if err != nil {
		return nil, "", err
	}
	defer storeRootFS.Close()

	if create {
		if mkErr := storeRootFS.Mkdir("cache", 0o700); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
			return nil, "", mkErr
		}
	}
	cacheInfo, err := storeRootFS.Lstat("cache")
	if err != nil {
		return nil, "", err
	}
	if cacheInfo.Mode()&os.ModeSymlink != 0 || !cacheInfo.IsDir() {
		return nil, "", errors.New("managed runtime cache directory must be a non-symlink directory")
	}
	cacheRoot, err := storeRootFS.OpenRoot("cache")
	if err != nil {
		return nil, "", err
	}
	defer cacheRoot.Close()

	if create {
		if mkErr := cacheRoot.Mkdir("daemon-run", 0o700); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
			return nil, "", mkErr
		}
	}
	runInfo, err := cacheRoot.Lstat("daemon-run")
	if err != nil {
		return nil, "", err
	}
	if runInfo.Mode()&os.ModeSymlink != 0 || !runInfo.IsDir() {
		return nil, "", errors.New("managed runtime daemon-run directory must be a non-symlink directory")
	}
	runRoot, err := cacheRoot.OpenRoot("daemon-run")
	if err != nil {
		return nil, "", err
	}
	return runRoot, filepath.Clean(filepath.Join(canonicalStore, "cache", "daemon-run")), nil
}

// canonicalStoreRuntimeDir creates a symlink-free private directory under
// storeRoot using fd-anchored os.OpenRoot so detached daemons survive ephemeral
// wrapper TMPDIR cleanup without path re-resolution TOCTOU.
func canonicalStoreRuntimeDir(storeRoot, pattern string) (string, error) {
	runRoot, basePath, err := openSafeDaemonRunRoot(storeRoot, true)
	if err != nil {
		return "", err
	}
	defer runRoot.Close()

	for range 8 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name := pattern + hex.EncodeToString(suffix[:])
		if err := runRoot.Mkdir(name, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		return filepath.Join(basePath, name), nil
	}
	return "", errors.New("failed to allocate unique managed runtime directory")
}

// cleanupStaleDaemonRunDirs sweeps leftover managed runtime directories under
// <storeRoot>/cache/daemon-run via fd-anchored os.Root after the exclusive
// .service-owner lock is held, refusing symlinks and preserving active dirs.
func cleanupStaleDaemonRunDirs(storeRoot string, keepDirs ...string) {
	runRoot, basePath, err := openSafeDaemonRunRoot(storeRoot, false)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("daemon-run cleanup skipped: %v", err)
		}
		return
	}
	defer runRoot.Close()

	keep := make(map[string]struct{}, len(keepDirs))
	for _, dir := range keepDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		keep[filepath.Clean(dir)] = struct{}{}
	}
	dirFile, err := runRoot.Open(".")
	if err != nil {
		log.Printf("daemon-run cleanup open dir failed: %v", err)
		return
	}
	entries, err := dirFile.ReadDir(-1)
	_ = dirFile.Close()
	if err != nil {
		log.Printf("daemon-run cleanup read dir failed: %v", err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isManagedDaemonRunEntry(name) {
			continue
		}
		candidate := filepath.Clean(filepath.Join(basePath, name))
		if _, protected := keep[candidate]; protected {
			continue
		}
		info, statErr := runRoot.Lstat(name)
		if statErr != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			log.Printf("daemon-run cleanup skipped non-directory or symlink entry %s", name)
			continue
		}
		if rmErr := runRoot.RemoveAll(name); rmErr != nil {
			log.Printf("daemon-run cleanup remove %s failed: %v", name, rmErr)
		}
	}
}

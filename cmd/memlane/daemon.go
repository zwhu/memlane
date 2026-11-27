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
	DiscoveryOptions                agents.DiscoveryOptions
	OutputLanguage                  string
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
	})
	if err != nil {
		return nil, errors.Join(err, profileService.Close(context.Background()))
	}
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
	} else {
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
	if resources := os.Getenv("MEMLANE_RUNTIME_RESOURCES"); resources != "" {
		if !filepath.IsAbs(resources) {
			return errors.New("runtime resource directory must be absolute")
		}
		if _, ok := bootstrap.RuntimeHashFromDir(resources); !ok {
			return errors.New("runtime resource directory must be a managed generation")
		}
		cfg.EngineWorkDir = resources
	}
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
	}
	var specs []daemon.ServiceSpec
	if !attach {
		if cfg.Python == "" {
			return errors.New("managed Python executable is required")
		}
		spec, cleanup, prepareErr := engineService(&cfg)
		if prepareErr != nil {
			return prepareErr
		}
		defer func() { err = errors.Join(err, cleanup()) }()
		specs = append(specs, spec)
	}
	stackCfg.Engine = cognee.Config{Endpoint: cfg.EngineURL, TokenFile: cfg.EngineTokenFile,
		StorePath: firstNonempty(cfg.OfficialData, cfg.StoreRoot), PythonBin: cfg.Python, WorkDir: cfg.EngineWorkDir}
	endpoints := []string{cfg.GatewayURL}
	if !attach {
		endpoints = append(endpoints, cfg.EngineURL)
	}
	daemonConfig, err := daemonOwnerConfig(cfg, endpoints)
	if err != nil {
		return err
	}
	supervisor := daemon.New(daemonConfig)
	if err := supervisor.Start(ctx, specs); err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = errors.Join(err, supervisor.Stop(stop))
	}()
	stackCfg.DaemonStatus = func(context.Context) map[string]any {
		s := supervisor.Snapshot()
		return map[string]any{"owner_pid": s.OwnerPID, "phase": s.Phase, "state": s.State, "services": s.Services}
	}
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
	if err := prepareGatewayStartup(ctx, stack.Gateway); err != nil {
		return err
	}

	address, err := loopbackAddress(cfg.GatewayURL)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Memlane gateway listening on %s\n", address); err != nil {
		return err
	}
	return stack.Gateway.ListenAndServeLoopback(ctx, address)
}

func prepareGatewayStartup(ctx context.Context, server *gateway.Server) error {
	if err := server.ApplyActiveGeneration(ctx); err != nil {
		log.Printf("generation startup: %s", err)
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
	config.LockConfig = daemon.LockConfig{
		OwnerReceiptFilename: ".memlane-dev-owner.json", RecoveredReceiptPrefix: ".memlane-dev-owner-recovered-",
		UpgradeLockFilename: ".memlane-upgrade.lock", ActivationLockFilename: "first-install-activation.lock",
		Codec: daemon.ReceiptCodec{
			Marshal: func(rec daemon.OwnerRecord) ([]byte, error) {
				return json.Marshal(owners.OwnerRecord{PID: rec.PID, Started: rec.Started, Phase: rec.Phase, Config: rec.ConfigPath,
					OfficialData: rec.StoreRoot, ServiceURL: cfg.EngineURL, CompletionURL: first.CompletionURL, ProjectControlURL: cfg.GatewayURL,
					ResourcesDir: cfg.EngineWorkDir, ResourcesHash: hash, GenerationMode: first.GenerationMode, GenerationReference: first.GenerationReference,
					Children: rec.Children, ServiceChildren: rec.ServiceChildren, ChildStartTimes: rec.ChildStartTimes, ObservedDescendants: rec.ObservedDescendants})
			},
			Unmarshal: func(raw []byte) (daemon.OwnerRecord, error) {
				var rec owners.OwnerRecord
				err := json.Unmarshal(raw, &rec)
				return daemon.OwnerRecord{PID: rec.PID, Started: rec.Started, Phase: rec.Phase, ConfigPath: rec.Config, StoreRoot: rec.OfficialData,
					Endpoints: []string{rec.ServiceURL, rec.CompletionURL, rec.ProjectControlURL},
					Children:  rec.Children, ServiceChildren: rec.ServiceChildren, ChildStartTimes: rec.ChildStartTimes, ObservedDescendants: rec.ObservedDescendants}, err
			},
		},
	}
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

func engineService(cfg *runtimeConfig) (daemon.ServiceSpec, func() error, error) {
	var spec daemon.ServiceSpec
	cleanup := func() error { return nil }
	var raw map[string]json.RawMessage
	data := cfg.raw
	if cfg.EngineConfig != "" {
		var err error
		data, err = os.ReadFile(cfg.EngineConfig)
		if err != nil {
			return spec, cleanup, err
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return spec, cleanup, err
	}
	if raw == nil {
		return spec, cleanup, errors.New("invalid engine configuration")
	}
	if cfg.EngineTokenFile == "" && cfg.EngineConfig != "" {
		var path string
		if err := json.Unmarshal(raw["internal_token_file"], &path); err != nil {
			return spec, cleanup, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(cfg.EngineConfig), path)
		}
		cfg.EngineTokenFile = path
	}
	folder, err := canonicalStoreRuntimeDir(cfg.StoreRoot, "memlane-engine-")
	if err != nil {
		return spec, cleanup, err
	}
	cleanup = func() error { return os.RemoveAll(folder) }
	fail := func(err error) (daemon.ServiceSpec, func() error, error) {
		return spec, cleanup, errors.Join(err, cleanup())
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
	}, cleanup, nil
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

// canonicalStoreRuntimeDir creates a symlink-free private directory under
// storeRoot so detached daemons survive ephemeral wrapper TMPDIR cleanup.
func canonicalStoreRuntimeDir(storeRoot, pattern string) (string, error) {
	base := filepath.Join(storeRoot, "cache", "daemon-run")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(base, 0o700); err != nil {
		return "", err
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(resolvedBase, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", errors.Join(err, os.RemoveAll(dir))
	}
	return dir, nil
}

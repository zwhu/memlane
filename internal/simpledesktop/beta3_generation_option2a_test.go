package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func beta3ControlConfig(t *testing.T, root string) *ProjectControlConfig {
	t.Helper()
	token := filepath.Join(root, "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	return &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
}

func beta3GenerationTester(a *App) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		applyAppearance(a.effectiveDark(c.Theme().Dark))
		ui.Themed(c, homeTheme).Fill().Padding(24).Children(func() { a.generationWorkspace(c) })
	}, 1200, 820)
}

func TestBeta3DiscoveredCLIRequiresExplicitSaveValidationAndNext(t *testing.T) {
	root := t.TempDir()
	profile := GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "codex-model"}
	ref := GenerationRef{ID: "agent-codex", Revision: 1}
	row := GenerationRow{Revision: 1, Profile: profile, Digest: strings.Repeat("a", 64)}
	directory := GenerationDirectory{Revision: 5, AgentBridges: map[string]GenerationProfile{"codex": profile}}
	var saved, validated, selected atomic.Int32
	receipt := GenerationValidation{Digest: row.Digest, RegisteredRevision: 7, Status: "passed", Plain: true, Schema: true, Quality: true}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/generation/profiles/"+ref.ID:
			saved.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"result": ref})
		case r.Method == "POST" && r.URL.Path == "/v1/generation/validations":
			validated.Add(1)
			w.Write([]byte("{}"))
		case r.Method == "PUT" && r.URL.Path == "/v1/generation/next":
			selected.Add(1)
			w.Write([]byte("{}"))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/generation/validations/"):
			json.NewEncoder(w).Encode(receipt)
		case r.Method == "GET" && r.URL.Path == "/v1/generation":
			result := directory
			if saved.Load() > 0 {
				result.Revision = 6
				result.Profiles = map[string][]GenerationRow{ref.ID: {row}}
			}
			if validated.Load() > 0 {
				result.Revision = 7
				result.Validations = map[string]GenerationValidation{"probe": receipt}
			}
			if selected.Load() > 0 {
				result.Revision = 8
				result.Next = &ref
			}
			json.NewEncoder(w).Encode(result)
		case r.Method == "GET" && r.URL.Path == "/v1/connections":
			w.Write([]byte("{}"))
		default:
			t.Errorf("unexpected generation request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.initialSetup, a.environment.RuntimeStarted = true, true
	s := &a.integrations
	s.Loaded, s.Generation = true, directory
	a.newGenerationProfile("agent_bridge")
	tt := beta3GenerationTester(a)
	clickProjectUI(t, tt, "配置模型："+ref.ID)
	if s.ProfileID != ref.ID || s.BridgeKind != "codex" || !s.availableProcessingBridge("codex") || s.connectedProcessingAgent("codex") {
		t.Fatal("CLI was not selectable independently of capture")
	}
	assertInactive := func() {
		t.Helper()
		if s.Generation.Active != nil || a.firstGenerationEnableBlocker() == "" {
			t.Fatal("activation became possible before the explicit prerequisites")
		}
	}
	assertInactive()
	// A stale apply intent must not make save perform the next operation.
	s.GenerationAgentApply = true
	clickProjectUI(t, tt, "保存配置")
	drainReading(t, updates, func() bool { return !s.Busy })
	if saved.Load() != 1 || validated.Load() != 0 || selected.Load() != 0 || !s.cleanSaved() || s.GenerationAgentApply {
		t.Fatal("save did more than persist the draft")
	}
	if a.generationCanUse(&ref) || a.firstGenerationEnableBlocker() != "草稿已保存，请先进行真实验证。" {
		t.Fatal("unvalidated bridge can be used or lacks guidance")
	}
	a.generationAction("next")
	if s.Busy || selected.Load() != 0 {
		t.Fatal("the direct next action accepted an unvalidated bridge")
	}
	assertInactive()
	tt.Frame()
	clickProjectUI(t, tt, "验证（会消耗额度）")
	drainReading(t, updates, func() bool { return s.Validation.Status == "passed" && !s.Loading })
	if validated.Load() != 1 || selected.Load() != 0 || !a.generationCanUse(&ref) {
		t.Fatal("verification did not publish eligibility or submitted an extra action")
	}
	assertInactive()
	tt.Frame()
	clickProjectUI(t, tt, "设为待启用")
	drainReading(t, updates, func() bool { return !s.Busy })
	if selected.Load() != 1 || s.Generation.Next == nil || *s.Generation.Next != ref ||
		s.Generation.Active != nil || a.firstGenerationEnableBlocker() != "" {
		t.Fatal("setting Next did not enable the final action without activating")
	}
}

func TestBeta3EmptyProcessingCLIGuideActions(t *testing.T) {
	root := t.TempDir()
	var refreshes atomic.Int32
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/generation" {
			refreshes.Add(1)
			json.NewEncoder(w).Encode(GenerationDirectory{AgentBridges: map[string]GenerationProfile{
				"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "discovered-model"},
			}})
			return
		}
		w.Write([]byte("{}"))
	})}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.environment.Hosts = []bootstrap.DetectedHost{{Kind: "trae_ide", DisplayName: "TRAE CN", Detected: true}}
	a.chooseGenerationSource("agent_bridge")
	var docs string
	a.OpenExternalURL = func(url string) error { docs = url; return nil }
	tt := beta3GenerationTester(a)
	if tt.HasText("知识") || tt.HasText("未检测到 CLI") || !tt.HasText("未检测到可用于记忆提炼的本地 CLI 工具") {
		t.Fatalf("unexpected empty state: %v", tt.Texts())
	}
	for _, cli := range bootstrap.RecommendedProcessingCLIs() {
		if !tt.HasText(cli.DisplayName) || !tt.HasText(cli.LoginHint) {
			t.Errorf("missing recommendation for %s", cli.DisplayName)
		}
		if cli.InstallCommand != "" {
			if !tt.HasText(cli.InstallCommand) {
				t.Errorf("missing install command for %s", cli.DisplayName)
			}
			clickProjectUI(t, tt, "复制命令 "+cli.DisplayName)
			if tt.Clipboard() != cli.InstallCommand {
				t.Fatalf("incorrect install command for %s", cli.DisplayName)
			}
		}
		if cli.DocsURL != "" {
			clickProjectUI(t, tt, "查看文档 "+cli.DisplayName)
			if docs != cli.DocsURL {
				t.Fatalf("incorrect documentation URL for %s", cli.DisplayName)
			}
		}
	}
	clickProjectUI(t, tt, "复制指令，让 TRAE CN 帮我安装 CLI")
	prompt := tt.Clipboard()
	if prompt != agentTakeoverInstallCLIPrompt("TRAE CN") || !strings.Contains(prompt, "记忆") ||
		strings.Contains(prompt, "知识") || strings.Contains(prompt, "xattr") {
		t.Fatalf("incorrect takeover prompt: %q", prompt)
	}
	a.integrations.Connections.Connections = []AgentConnection{{Kind: "codex", Status: "configured_pending_new_session"}}
	if a.integrations.availableProcessingBridge("codex") || a.integrations.hasAvailableProcessingBridge() {
		t.Fatal("a capture connection was mistaken for a usable processing CLI")
	}
	tt.Frame()
	if !tt.HasText("未检测到可用于记忆提炼的本地 CLI 工具") {
		t.Fatal("an unavailable capture-connected CLI hid the installation guide")
	}
	clickProjectUI(t, tt, "改用模型 API / Ollama")
	if a.integrations.DraftMode != "provider" {
		t.Fatal("provider action did not change the source")
	}
	a.chooseGenerationSource("agent_bridge")
	tt.Frame()
	clickProjectUI(t, tt, "已装好，重新检测")
	drainReading(t, updates, func() bool { return !a.integrations.Loading })
	tt.Frame()
	if refreshes.Load() != 1 || !tt.HasText("Codex CLI") || tt.HasText("已装好，重新检测") {
		t.Fatal("live detection did not replace the guide with the new CLI")
	}
}

func beta3SetupRuntimeConfig(t *testing.T) (Config, func(bool)) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storeID := strings.Repeat("a", 32)
	configPath := filepath.Join(root, "runtime.json")
	generationFile := filepath.Join(root, "generation.json")
	write := func(name string, value any) {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("store-identity.json", bootstrap.StoreReceipt{Schema: 1, StoreID: storeID, SourceCommit: "fixed", OwnerID: "test-owner", KeyID: "test-key", StorageInitialized: true})
	control := beta3ControlConfig(t, root)
	writeConfig := func(setup bool) {
		mode, model := "unconfigured", ""
		if !setup {
			mode, model = "agent_bridge", "codex-model"
			write("generation.json", map[string]any{
				"schema_version": 2,
				"active":         GenerationRef{ID: "agent-codex", Revision: 1},
				"profiles": map[string][]GenerationRow{
					"agent-codex": {{Revision: 1, Profile: GenerationProfile{Mode: mode, Model: model}}},
				},
			})
			write("client-state.json", map[string]any{
				"schema_version": 1, "initialization_completed_at": "2026-11-25T12:34:56Z",
				"first_used_date": "2026-11-25", "first_used_timezone": "UTC",
			})
		}
		write("runtime.json", map[string]any{
			"first_install": bootstrap.FirstInstallation{Schema: 1, StoreID: storeID, Setup: setup},
			"official_data": root, "service_commit": "fixed", "token_file": filepath.Join(root, "owner.token"),
			"service_url": coldStartTestURL, "generation_mode": mode, "model": model,
			"generation_file": generationFile, "project_control": control,
		})
	}
	writeConfig(true)
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return config, writeConfig
}

func TestBeta3SkipAndReopenOverview(t *testing.T) {
	config, _ := beta3SetupRuntimeConfig(t)
	a := New(config, nil)
	a.environment.RuntimeStarted = true
	if !a.initialSetup || a.environment.InitialStep != 3 {
		t.Fatal("test requires a fresh Step 3")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "暂不配置提炼，先进入 Memlane")
	if a.initialSetup || !a.setupDeferredMode || a.page != "总览" {
		t.Fatal("Skip did not open the main Overview")
	}
	if deferred, err := bootstrap.FirstSetupDeferred(config.ConfigFile); err != nil || !deferred {
		t.Fatalf("defer receipt not saved: %v, %v", deferred, err)
	}
	if complete, err := bootstrap.FirstInitializationComplete(config.ConfigFile); err != nil || complete {
		t.Fatalf("Skip changed activation state: %v, %v", complete, err)
	}
	reopened := New(config, nil)
	if reopened.initialSetup || !reopened.setupDeferredMode || reopened.page != "总览" {
		t.Fatal("reopening did not restore deferred Overview")
	}
}

func TestBeta3DeferredSettingsActivatesValidatedNextAndClearsReceipt(t *testing.T) {
	config, writeConfig := beta3SetupRuntimeConfig(t)
	if err := bootstrap.SaveFirstSetupDeferred(config.ConfigFile); err != nil {
		t.Fatal(err)
	}
	a := New(config, nil)
	a.page, a.access.SettingsSection = "设置", "记忆提炼"
	a.environment.RuntimeStarted = true
	a.environment.RuntimePreparation.ConfigPath = config.ConfigFile
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(a.startupDeferred.stopAllTimers)
	ref := GenerationRef{ID: "agent-codex", Revision: 1}
	profile := GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "codex-model"}
	digest := strings.Repeat("b", 64)
	a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
	g := &a.integrations.Generation
	*g = GenerationDirectory{Revision: 12, Next: &ref,
		AgentBridges: map[string]GenerationProfile{"codex": profile},
		Profiles:     map[string][]GenerationRow{ref.ID: {{Revision: 1, Digest: digest, Profile: profile}}},
	}
	var activations atomic.Int32
	a.ActivateFirstRuntimeFn = func(_ context.Context, prepared bootstrap.FirstRuntime, reference bootstrap.FirstGenerationReference, gotDigest string, revision int64) error {
		if prepared.ConfigPath != config.ConfigFile || reference.ID != ref.ID || reference.Revision != ref.Revision || gotDigest != digest || revision != 12 {
			return errors.New("wrong activation request")
		}
		activations.Add(1)
		writeConfig(false)
		return nil
	}
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "启用记忆提炼")
	if a.environment.Busy || activations.Load() != 0 {
		t.Fatal("Settings started activation before validation")
	}
	g.Validations = map[string]GenerationValidation{"probe": {Digest: digest, Status: "passed", Plain: true, Schema: true, Quality: true}}
	tt.Frame()
	clickProjectUI(t, tt, "启用记忆提炼")
	drainReading(t, updates, func() bool { return !a.environment.Busy })
	if activations.Load() != 1 || a.isSetupOnlyRuntime() || a.initialSetup || a.Client.Config.GenerationMode != "agent_bridge" ||
		!a.environment.RuntimeStarted || a.environment.RuntimeStage != "" {
		t.Fatalf("Settings failed to adopt the activated runtime: %s", a.environment.Error)
	}
	if deferred, err := bootstrap.FirstSetupDeferred(config.ConfigFile); err != nil || deferred {
		t.Fatalf("defer receipt remained after verified activation: %v, %v", deferred, err)
	}
	reopened := New(a.Config, nil)
	if reopened.initialSetup || reopened.isSetupOnlyRuntime() {
		t.Fatal("confirmed active runtime reopened in first setup")
	}
	tt.Frame()
	if tt.HasText("尚未配置记忆提炼模型") || tt.HasText("启用记忆提炼") {
		t.Fatal("Settings still shows first-activation UI after completion")
	}
}

func TestBeta3SetupOnlyMonitorAndOverview(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "owner.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{GenerationMode: "unconfigured", ServiceURL: coldStartTestURL, TokenFile: token,
		CompletionURL: "http://127.0.0.1:31337", ProjectControl: beta3ControlConfig(t, root)}, nil)
	var health, monitor, unexpected atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health" && r.URL.Port() != "31337":
			health.Add(1)
		case r.URL.Path == "/v1/runtime/monitor":
			monitor.Add(1)
		default:
			unexpected.Add(1)
		}
		w.Write([]byte("{}"))
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.pollServiceMonitor(true)
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.completion != setupOnlyCompletion || health.Load() != 1 || monitor.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("setup mode probed the completion port or skipped real service checks")
	}
	if tone, label, col := a.trayChannelStatus(); tone != "neutral" || label != "待配置提炼" || col != tok.Text3 || a.trayStatusLevel() != "ok" {
		t.Fatalf("unexpected tray status: %s %s %s", tone, label, a.trayStatusLevel())
	}
	service := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.serviceConnectionOverview(c) })
	}, 1200, 820)
	if !service.HasText("待配置提炼") {
		t.Fatal("service settings did not render the neutral model channel")
	}
	q := &projectQueueState{BacklogError: "old warning"}
	p := AccessProject{ID: "demo"}
	snap := projectQueueSnapshot{setupOnly: true, v2SummaryOK: true, summaryBacklogErr: "not_found"}
	if rec := reconcileProjectQueueState(p, snap); rec.BacklogError != "" {
		t.Fatal("setup mode misreported a missing backlog")
	}
	a.triggerProvenanceBackfillOnce()
	a.refreshBacklog(q, p)
	if q.BacklogLoading || q.BacklogError != "" || unexpected.Load() != 0 {
		t.Fatal("setup mode read the backlog or kept a stale error")
	}
	// The transport records any request from a mistakenly scheduled backfill.
	deadline := time.NewTimer(30 * time.Millisecond)
	defer deadline.Stop()
	<-deadline.C
	if unexpected.Load() != 0 {
		t.Fatal("setup mode made a provenance request")
	}
	a.monitor.ControlReachable = false
	if a.trayStatusLevel() != "danger" {
		t.Fatal("setup mode masked a real control failure")
	}
	a.monitor.ControlReachable = true
	a.monitor.Snapshot.Disk = &DiskMonitorState{Low: true}
	if a.trayStatusLevel() != "warn" {
		t.Fatal("setup mode masked a real low disk warning")
	}
	a.Update = nil
	a.overviewFirstUse.cachedFirstUseKey = firstUseKey(a.Config, time.Now())
	a.overviewFirstUse.cachedFirstUseErr = errors.New("missing first-use receipt")
	a.access.OpenProject = "demo"
	a.settingsOrigin = &readingLocation{}
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("使用时间暂不可用。") || !tt.HasText("尚未配置记忆提炼模型，新会话暂不自动提炼") {
		t.Fatalf("unexpected Overview copy: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "立即配置")
	if a.page != "设置" || a.access.SettingsSection != "记忆提炼" || a.access.OpenProject != "" || a.settingsOrigin != nil ||
		tt.HasText("立即配置") || !tt.HasText("尚未配置记忆提炼模型") {
		t.Fatal("Overview banner did not open the correct Settings tab")
	}
}

func TestBeta3DeferredAndEmptyCLIStayWithinV4Window(t *testing.T) {
	for _, mode := range []string{"light", "dark"} {
		t.Run(mode, func(t *testing.T) {
			a := New(Config{GenerationMode: "unconfigured"}, nil)
			a.appearance = appearanceState{mode: mode, loaded: true}
			a.chooseGenerationSource("agent_bridge")
			for _, fixture := range []struct {
				name   string
				tester *ui.Tester
				labels []string
			}{
				{"empty-cli", beta3GenerationTester(a), []string{"未检测到可用于记忆提炼的本地 CLI 工具", "已装好，重新检测", "改用模型 API / Ollama"}},
				{"overview", ui.NewTester(a.View, 1200, 820), []string{"尚未配置记忆提炼模型，新会话暂不自动提炼", "立即配置"}},
			} {
				fixture.tester.Advance(time.Second)
				for _, label := range fixture.labels {
					r, ok := fixture.tester.Find(label)
					if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1200 || r.Y+r.H > 820 {
						t.Errorf("%s element %q escaped the window: %+v", fixture.name, label, r)
					}
				}
				fixtureImage(t, fixture.tester, "beta3-"+fixture.name+"-"+mode)
			}
		})
	}
}

func TestBeta3SetupOnlyQueueDoesNotOfferBacklogActions(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationSetupOnly: true, ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup, a.setupDeferredMode = false, true
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected queue request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})}
	p := AccessProject{ID: "demo"}
	q := a.projectQueue(p)
	q.Loaded, q.Next = true, time.Now().Add(time.Hour)
	a.Update = func(func()) {}
	tt := ui.NewTester(func(c *ui.Context) { a.projectQueueSection(c, p) }, 1200, 820)
	if !tt.HasText("待配置记忆提炼") || tt.HasText("自动采集已暂停") || tt.HasText("自动采集") {
		t.Fatalf("unexpected setup-only queue: %v", tt.Texts())
	}

	a.changeBacklog(q, p, false)
	a.runProcessingAction(q, p, "/backlog/retry", "", false)
	if q.BacklogBusy || q.BacklogOperation != "" || q.ProcessingActionBusy || requests.Load() != 0 {
		t.Fatal("setup-only runtime scheduled a backlog mutation")
	}
	a.Update = nil // The CTA may refresh the generation directory on entry to Settings.
	clickProjectUI(t, tt, "立即配置")
	if a.page != "设置" || a.access.SettingsSection != "记忆提炼" {
		t.Fatal("queue CTA did not navigate to generation settings")
	}
}

func TestBeta3SetupOnlyGraphTabDoesNotRequestGraph(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationSetupOnly: true, ProjectControl: beta3ControlConfig(t, root),
		Projects: []Project{{ID: "demo", Dataset: "demo"}}}, nil)
	a.initialSetup, a.setupDeferredMode = false, true
	a.page, a.kind, a.project = "知识", "知识", 0
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected graph request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	a.Update = func(func()) {}
	a.graphAttachFn = func(*GraphHost) error {
		t.Error("graph surface was attached in setup-only mode")
		return nil
	}
	a.graphError, a.graphLoading = "HTTP 503", true
	a.loadGraph()
	if a.graphLoading || a.graphError != "" || a.graphAttachRequested {
		t.Fatal("setup-only graph did not clear a pending load and stale error")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.projectTabs(c)
			if a.page == "图谱" {
				a.graphView(c)
			}
		})
	}, 1200, 820)
	clickProjectUI(t, tt, "图谱")
	if a.page != "图谱" || a.graphLoading || a.graphAttachRequested || requests.Load() != 0 ||
		!tt.HasText("配置记忆提炼后可查看图谱") || tt.HasText("HTTP 503") {
		t.Fatalf("unexpected setup-only graph: %v", tt.Texts())
	}
	a.Update = nil
	clickProjectUI(t, tt, "前往配置")
	if a.page != "设置" || a.access.SettingsSection != "记忆提炼" {
		t.Fatal("graph CTA did not navigate to generation settings")
	}
}

func TestBeta3DeferredReopenRetriesOwnerInspectionOnOverviewFocus(t *testing.T) {
	config, _ := beta3SetupRuntimeConfig(t)
	if err := bootstrap.SaveFirstSetupDeferred(config.ConfigFile); err != nil {
		t.Fatal(err)
	}
	// Make startup terminate before it can launch a daemon. This test observes
	// the real startup entry point; activation has a separate validated test.
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", config.ConfigFile)
	a := New(config, nil)
	s := &a.environment
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(a.startupDeferred.stopAllTimers)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	var checks atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		if checks.Add(1) == 1 {
			return bootstrap.OwnerRuntimeStatus{}, errors.New("synthetic first inspection failure")
		}
		return bootstrap.OwnerRuntimeStatus{StaleVersionDetected: false}, nil
	}
	a.Start()
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if a.initialSetup || a.page != "总览" || s.OwnerStatusChecked || s.Busy || checks.Load() != 1 {
		t.Fatal("a failed inspection was recorded as checked or started the daemon")
	}
	a.OnWindowFocus()
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if checks.Load() != 2 || !s.OwnerStatusChecked || !s.Busy || s.RuntimeStage != "启动记忆服务" {
		t.Fatal("successful focus retry did not proceed to start the runtime")
	}
	drainReading(t, updates, func() bool { return !s.Busy })
}

func TestBeta3GenerationRefreshKeepsCLIWhenConnectionsFail(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	s := &a.integrations
	s.Connections.Revision = 4
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/connections" && r.URL.Query().Get("force") == "1":
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			json.NewEncoder(w).Encode(GenerationDirectory{Revision: 12, AgentBridges: map[string]GenerationProfile{
				"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "discovered-model"},
			}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.chooseGenerationSource("agent_bridge")
	a.RefreshIntegrations()
	drainReading(t, updates, func() bool { return !s.Loading })
	if !s.Loaded || s.Generation.Revision != 12 || s.Connections.Revision != 4 || s.Selected == nil ||
		s.ItemMessages == nil || s.Error == "" || !s.availableProcessingBridge("codex") {
		t.Fatalf("partial refresh did not commit generation: %+v", s)
	}
	tt := beta3GenerationTester(a)
	if !tt.HasText("Codex CLI") || tt.HasText("未检测到可用于记忆提炼的本地 CLI 工具") {
		t.Fatalf("available CLI missing from generation settings: %v", tt.Texts())
	}
	connections := ui.NewTester(func(c *ui.Context) { a.agentConnectionSettings(c) }, 1200, 820)
	if !connections.HasText("连接状态暂时不可用") {
		t.Fatal("connection settings did not show its read error")
	}
}

func TestBeta3SetupOnlyToolGraphSettingsAndActions(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationSetupOnly: true, ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup, a.setupDeferredMode = false, true
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected tool graph request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})}
	a.Update = func(func()) {}
	// A stale dialog must neither poll its job nor start a new one.
	a.toolGraph = toolGraphUIState{Open: true, Project: "p1",
		Preview: &toolGraphPreview{Sessions: 1}, Job: &toolGraphJob{Status: "running"}}
	tt := ui.NewTester(func(c *ui.Context) {
		a.projectToolGraphSettings(c, AccessProject{ID: "p1"})
		a.toolGraphDialog(c)
	}, 1200, 820)
	if !tt.HasText("待配置记忆提炼") || !tt.HasText("配置并启用记忆提炼后可创建工具关系图谱") || tt.HasText("创建工具图谱") {
		t.Fatalf("unexpected setup-only tool graph settings: %v", tt.Texts())
	}
	a.previewToolGraph("p1")
	a.toolGraphAction("start")
	if a.toolGraph.Busy || a.toolGraph.Request != 0 || requests.Load() != 0 {
		t.Fatal("setup-only tool graph scheduled a request")
	}
	a.Update = nil // Entering Settings may refresh the generation directory.
	clickProjectUI(t, tt, "前往配置记忆提炼")
	if a.page != "设置" || a.access.SettingsSection != "记忆提炼" {
		t.Fatal("tool graph CTA did not navigate to generation settings")
	}
}

func TestBeta3SetupOnlyQueueDetailsHidePendingCheck(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationSetupOnly: true, ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup, a.setupDeferredMode = false, true
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected queue request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})}
	a.Update = func(func()) {}
	p := AccessProject{ID: "p1"}
	q := &projectQueueState{}
	row := queuedSession{Agent: AccessAgent{ID: "agent"}, Binding: ProjectBinding{HostSessionKey: "session"}, View: &pendingBufferView{}}
	q.Checks = map[string]pendingCheckState{"agent/session": {Status: "unconfirmed"}}
	a.retryPendingCheck(q, p, row)
	if requests.Load() != 0 || q.pendingCheck(row).Status != "unconfirmed" {
		t.Fatal("setup-only queue changed or scheduled a pending check")
	}
	n := messageNotice{Key: "pending", Title: "采集结果待确认", Session: &row}
	tt := ui.NewTester(func(c *ui.Context) { a.renderMessageNoticeDialog(c, q, p, n) }, 1200, 820)
	if !tt.HasText(n.Title) || tt.HasText("重新检查采集结果") {
		t.Fatalf("unexpected setup-only queue details: %v", tt.Texts())
	}
}

func TestBeta3GenerationLoadFailureWithConnectionsReady(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	s := &a.integrations
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/connections":
			json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 5})
		case "GET /v1/generation":
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"generation_unavailable"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.RefreshIntegrations()
	drainReading(t, updates, func() bool { return !s.Loading })
	if !s.Loaded || !s.ConnectionsLoaded || s.Connections.Revision != 5 || s.generationReady() ||
		s.GenerationError == "" || s.Error != s.GenerationError {
		t.Fatalf("connection success hid generation load failure: %+v", s)
	}
	for _, view := range []func(*ui.Context){
		func(c *ui.Context) { a.generationConfigurationFor(c, true, true) },
		a.generationManagementContent,
	} {
		tt := ui.NewTester(view, 1200, 820)
		if !tt.HasText("记忆提炼配置尚未载入") || !tt.HasText(s.GenerationError) || !tt.HasText("读取记忆提炼配置") ||
			tt.HasText("尚未检测到可用于记忆提炼的本机 CLI") {
			t.Fatalf("unexpected first-load generation UI: %v", tt.Texts())
		}
	}
}

func TestBeta3ConnectionRefreshDoesNotLoadGeneration(t *testing.T) {
	root := t.TempDir()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/v1/connections?force=1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 5})
	})}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	s := &a.integrations

	a.RefreshConnections()
	drainReading(t, updates, func() bool { return !s.Loading })
	if !s.ConnectionsLoaded || s.Connections.Revision != 5 || s.GenerationLoaded || s.generationReady() {
		t.Fatalf("connection refresh changed generation readiness: %+v", s)
	}
	if got := a.settingsFirstActivationBlocker(); got != "请先读取记忆提炼配置。" {
		t.Fatalf("connection refresh left the wrong activation blocker: %q", got)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.generationConfigurationFor(c, true, true) }, 1200, 820)
	if !tt.HasText("记忆提炼配置尚未载入") || tt.HasText("尚未检测到可用于记忆提炼的本机 CLI") {
		t.Fatalf("connection refresh rendered the wrong generation state: %v", tt.Texts())
	}
}

func TestBeta3DeferredGlobalToolGraphSettingsCannotMutate(t *testing.T) {
	for _, mode := range []string{"deferred", "first_install"} {
		t.Run(mode, func(t *testing.T) {
			a := New(Config{ProjectControl: beta3ControlConfig(t, t.TempDir())}, nil)
			a.page, a.access.SettingsSection = "设置", "记忆提炼"
			a.access.Loaded = true
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { a.globalSettingsPage(c) })
			}, 1200, 820)
			if !tt.HasText("自动构建关系图谱") {
				t.Fatal("global tool graph setting was absent before entering deferred setup")
			}
			if mode == "deferred" {
				a.setupDeferredMode = true
			} else {
				a.Config.FirstInstall = &bootstrap.FirstInstallation{Setup: true}
			}
			var requests atomic.Int32
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				t.Errorf("unexpected tool graph request %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			})}
			a.Update = func(func()) {}
			tt.Frame()
			if !tt.HasText("记忆提炼") || tt.HasText("自动构建关系图谱") {
				t.Fatalf("unexpected global settings in setup-only runtime: %v", tt.Texts())
			}
			enabled := true
			a.saveToolGraphSetting("", &enabled)
			if a.access.Busy || a.access.Request != 0 || requests.Load() != 0 {
				t.Fatal("setup-only runtime scheduled a tool graph settings mutation")
			}
		})
	}
}

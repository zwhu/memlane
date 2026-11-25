package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func TestBeta3AgentSelectionPersistsEmptyAndMultipleChoices(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", filepath.Join(root, "components"))
	cfg := Config{ConfigFile: filepath.Join(root, "runtime.json"), FirstInstallDefault: true}
	hosts := []bootstrap.DetectedHost{
		{Kind: "trae_ide", DisplayName: "TRAE CN", BetaPrimary: true, Detected: true, Version: "1.2.3", SupportsCapture: true},
		{Kind: "pi", DisplayName: "pi", BetaPrimary: true, SupportsCapture: true, SupportsProcessingCLI: true},
	}
	openWizard := func() (*App, chan func(), *ui.Tester) {
		a := New(cfg, os.ErrNotExist)
		a.DetectLocalHostsFn = func(opts bootstrap.HostDetectOptions) []bootstrap.DetectedHost {
			if !opts.BetaPrimaryOnly {
				t.Error("host discovery did not request beta primary hosts")
			}
			return hosts
		}
		updates := make(chan func(), 8)
		a.Update = func(fn func()) { updates <- fn }
		tt := ui.NewTester(a.View, 1200, 820)
		drainReading(t, updates, func() bool { return a.environment.HostsLoaded })
		tt.Frame()
		return a, updates, tt
	}
	// beta.2 stored environment options before Agent selection existed.
	if err := bootstrap.SaveSelections(bootstrap.EnvironmentOptions{Root: os.Getenv("MEMLANE_BOOTSTRAP_ROOT")}, cfg.ConfigFile); err != nil {
		t.Fatal(err)
	}
	a, updates, tt := openWizard()
	if a.environment.InitialStep != 1 || !slices.Equal(a.environment.SelectedAgents, []string{"trae_ide"}) ||
		a.environment.SelectedAgentsExplicit || !tt.HasText("已安装") || !tt.HasText("未检测到") ||
		tt.HasText("自动采集 · 可用于记忆提炼") || !tt.HasText("自动采集 · 提炼需搭配 CLI 或模型 API") {
		t.Fatalf("detected hosts or default selections missing: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "选择 pi")
	waitSaved := func() {
		drainReading(t, updates, func() bool { return !a.environment.selectedAgentsSaving })
	}
	waitSaved()
	a, updates, tt = openWizard()
	if !tt.HasText("查看推荐 CLI 安装方式") {
		t.Fatal("an undetected selected CLI hid the processing guide")
	}
	if !slices.Equal(a.environment.SelectedAgents, []string{"trae_ide", "pi"}) || !a.environment.SelectedAgentsExplicit {
		t.Fatalf("multiple selections did not reload: %v", a.environment.SelectedAgents)
	}
	clickProjectUI(t, tt, "选择 pi")
	clickProjectUI(t, tt, "选择 TRAE CN")
	if !tt.HasText("稍后再连，先准备环境") {
		t.Fatal("zero selection did not offer a way to continue")
	}
	waitSaved()
	a, updates, tt = openWizard()
	if len(a.environment.SelectedAgents) != 0 || !a.environment.SelectedAgentsExplicit {
		t.Fatalf("zero selection reselected a detected host: %v", a.environment.SelectedAgents)
	}
	// Use a preloaded plan so advancing only exercises local selection persistence.
	a.environment.Loaded = true
	clickProjectUI(t, tt, "继续准备环境")
	waitSaved()
	if a.environment.InitialStep != 2 {
		t.Fatal("zero selection blocked environment preparation")
	}
}

func TestBeta3StepOneCLIGuideAndTakeoverClipboard(t *testing.T) {
	a := New(Config{}, os.ErrNotExist)
	s := &a.environment
	s.HostsLoaded, s.SelectedAgents = true, []string{"trae_ide"}
	s.Hosts = []bootstrap.DetectedHost{
		{Kind: "trae_ide", DisplayName: "TRAE CN", BetaPrimary: true, Detected: true, Application: "/test/Trae CN.app", SupportsCapture: true},
		{Kind: "codex", DisplayName: "Codex", BetaPrimary: true, SupportsProcessingCLI: true},
	}
	var docs, application string
	a.OpenExternalURL = func(url string) error { docs = url; return nil }
	a.OpenExistingAgent = func(path string) error { application = path; return nil }
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("把对话自动整理成记忆需要一个后台提炼工具") || tt.HasText("npm i -g") || tt.HasText("知识") ||
		tt.HasText("自动采集 · 可用于记忆提炼") {
		t.Fatalf("unexpected upfront guidance: %v", tt.Texts())
	}
	tt.Advance(700 * time.Millisecond)
	fixtureImage(t, tt, "beta3-setup-agents")
	clickProjectUI(t, tt, "复制指令，让 TRAE CN 帮我安装 CLI")
	if tt.Clipboard() != agentTakeoverInstallCLIPrompt("TRAE CN") || !strings.Contains(tt.Clipboard(), "记忆") {
		t.Fatal("CLI takeover did not copy the selected host's prompt")
	}
	clickProjectUI(t, tt, "查看推荐 CLI 安装方式")
	for _, cli := range bootstrap.RecommendedProcessingCLIs() {
		if !tt.HasText(cli.LoginHint) {
			t.Errorf("recommendation missing for %s", cli.DisplayName)
		}
	}
	clickProjectUI(t, tt, "复制命令 Codex")
	if tt.Clipboard() != "npm i -g @openai/codex" {
		t.Fatal("command was not copied verbatim")
	}
	clickProjectUI(t, tt, "查看文档 Codex")
	if docs != "https://developers.openai.com/codex/cli" {
		t.Fatal("CLI documentation hook was not called")
	}
	clickProjectUI(t, tt, "收起推荐 CLI")
	clickProjectUI(t, tt, "交给已选 Agent 自动安装")
	const expected = `帮我完成本机 Memlane 的首次安装与配置（无需安装 Skill）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 按 next_actions 顺序推进：所有 requires=explicit_confirmation 的 --apply 操作，先给我看 --plan / --dry-run 预览并等我确认；若返回退出码 20，按 retry_after_ms 用 doctor --operation-id <id> --json 轮询进度直到完成。
3. 严禁读取或输出任何 token/密钥/.local/聊天内容，严禁手动修改 hooks.json/Cognee 源码。若返回退出码 11 或同一问题重试 3 次仍失败，立刻停止并把 human_steps 交给我。
（我计划优先接入的 Agent：TRAE CN）`
	if tt.Clipboard() != expected || !tt.HasText("已复制安装指令（零 Skill）") {
		t.Fatal("fresh-install prompt or feedback differs")
	}
	clickProjectUI(t, tt, "打开 TRAE CN")
	if application != s.Hosts[0].Application {
		t.Fatal("selected detected Agent was not opened")
	}
	if show, names := needsStep1ProcessingCLINotice(s.Hosts, []string{"trae_ide", "codex"}); !show || !slices.Equal(names, []string{"TRAE CN"}) {
		t.Fatal("selecting an undetected CLI hid capture-only guidance")
	}
	s.Hosts[1].Detected = true
	tt.Frame()
	if show, _ := needsStep1ProcessingCLINotice(s.Hosts, []string{"trae_ide", "codex"}); !show ||
		tt.HasText("自动采集 · 可用于记忆提炼") || !tt.HasText("查看推荐 CLI 安装方式") {
		t.Fatal("detected host without an executable was treated as a usable CLI")
	}
	s.Hosts[1].Executable = "/test/bin/codex"
	tt.Frame()
	if tt.HasText("查看推荐 CLI 安装方式") {
		t.Fatal("detected usable CLI did not hide guidance")
	}
	for _, prompt := range []string{expected, agentTakeoverBeta2RescuePrompt(), agentTakeoverConnectionTroubleshootPrompt("trae_ide"), agentTakeoverInstallCLIPrompt("TRAE CN")} {
		if strings.Contains(prompt, "xattr") || strings.Contains(prompt, "知识") {
			t.Fatal("takeover prompt violates terminology")
		}
	}
	if !strings.Contains(agentTakeoverConnectionTroubleshootPrompt("unknown ; argument"), "--agent <kind> --json") {
		t.Fatal("unrecognized argument was interpolated into troubleshooting command")
	}
}

func TestBeta3EnvironmentTechnicalDisclosureAndProgress(t *testing.T) {
	a := setupLayoutFixture()
	s := &a.environment
	s.Plan.Items[4].Requirement.Version = "BAAI/bge-small-zh-v1.5"
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"本地记忆引擎", "本地语义搜索模型", "Agent 接入组件", environmentSafetyPromise} {
		if !tt.HasText(label) {
			t.Errorf("missing capability or safety promise %q", label)
		}
	}
	for _, label := range []string{"uv", "3.12.12", "Cognee", "BAAI/bge-small-zh-v1.5", "知识"} {
		if tt.HasText(label) {
			t.Errorf("plain-language view exposed %q", label)
		}
	}
	tt.Advance(700 * time.Millisecond)
	fixtureImage(t, tt, "beta3-setup-environment")
	clickProjectUI(t, tt, "查看技术明细 (6 项)")
	for _, label := range []string{"uv", "3.12.12", "Cognee", "BAAI/bge-small-zh-v1.5", "LanceDB", "Agent 插件", "SHA-256 fixed", "指定已有环境", "复制安装命令"} {
		if !tt.HasText(label) {
			t.Errorf("missing technical detail %q", label)
		}
	}
	clickProjectUI(t, tt, "收起技术明细")
	clickProjectUI(t, tt, "安装并继续")
	if !tt.HasText("查看技术清单") || !tt.HasText("预计下载 60.0 MiB") || tt.HasText("uv · 0.12.3") || s.Busy {
		t.Fatal("review did not lead with estimates and expandable scope")
	}
	clickProjectUI(t, tt, "查看技术清单")
	if !tt.HasText("uv · 0.12.3") || !tt.HasText("SHA-256 fixed") || s.Busy {
		t.Fatal("expanding the approved scope hid details or started installation")
	}
	clickProjectUI(t, tt, "暂不安装")

	s.Busy = true
	s.Progress = bootstrap.InstallProgress{
		Component: "embedding", Stage: "downloading", BytesDone: 42 << 20, BytesTotal: 96 << 20,
		BytesPerSec: 12.4 * (1 << 20), Mirror: "mirror.example.test", FallbackAttempt: 2, FallbackTotal: 3, Elapsed: 68 * time.Second,
	}
	tt.Frame()
	for _, label := range []string{"正在下载 · 本地语义搜索模型", "进度 44%", "42.0 / 96.0 MiB", "12.4 MiB/s", "来源：mirror.example.test", "已自动切换备用镜像（第 2/3 个）", "已用时 1m 08s"} {
		if !tt.HasText(label) {
			t.Errorf("missing progress metric %q", label)
		}
	}
	fixtureImage(t, tt, "beta3-setup-progress")
	s.Busy, s.ExternalWorkerActive = false, true
	s.ExternalWorker = bootstrap.InstallWorkerState{Stage: "downloading", Component: "agent-plugin", Indeterminate: true, BytesDone: 2 << 20, ElapsedMs: 14000, FallbackAttempt: 2, FallbackTotal: 4}
	tt.Frame()
	if !tt.HasText("进度未定") || !tt.HasText("命令行安装进行中（只读进度）") || !tt.HasText("已下载 2.0 MiB") ||
		!tt.HasText("已用时 14s") || !tt.HasText("已自动切换备用镜像（第 2/4 个）") || tt.HasText("停止安装") {
		t.Fatalf("external indeterminate progress missing: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "安装并继续")
	if s.Review {
		t.Fatal("opened approval while external worker was active")
	}
	a.runEnvironment(true)
	if s.Busy || !strings.Contains(s.Message, "命令行正在后台安装组件") {
		t.Fatal("local start did not guard against an external worker")
	}
}

func TestBeta3InstallPreflightCatchesNewExternalWorker(t *testing.T) {
	a := setupLayoutFixture()
	s := &a.environment
	root := t.TempDir()
	s.Environment = &bootstrap.Environment{Options: bootstrap.EnvironmentOptions{Root: root}}
	s.Review = true
	a.InspectInstallWorkerFn = func(_ context.Context, actualRoot string) (bootstrap.InstallWorkerState, bool, error) {
		if actualRoot != root {
			t.Error("installation preflight checked the wrong component root")
		}
		return bootstrap.InstallWorkerState{Stage: "downloading", OperationID: "cli-install", Component: "python"}, true, nil
	}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.runEnvironment(true)
	drainReading(t, updates, func() bool { return !s.Busy })
	if !s.ExternalWorkerActive || s.ExternalWorker.OperationID != "cli-install" || s.Review ||
		s.Journal.ApprovedDigest != "" || s.Cancel != nil {
		t.Fatal("local consent or installation was recorded after a CLI worker acquired the lock")
	}
}

func TestBeta3LegacyWizardOnlyRestartsStaleOwnerOnClick(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storeID := strings.Repeat("a", 32)
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
	write("runtime.json", map[string]any{
		"first_install": bootstrap.FirstInstallation{Schema: 1, StoreID: storeID, Setup: true},
		"official_data": root, "service_commit": "fixed", "token_file": filepath.Join(root, "owner.token"), "service_url": coldStartTestURL,
	})
	write("first-install-wizard.json", map[string]any{"schema": 1, "store_id": storeID, "step": 2})
	config, err := LoadConfig(filepath.Join(root, "runtime.json"))
	a := New(config, err)
	s := &a.environment
	if !a.initialSetup || s.InitialStep != 3 {
		t.Fatalf("beta.2 step was not migrated: %d, %s", s.InitialStep, s.Error)
	}
	// Simulate a resumed installation with existing verified components but an old owner.
	_, item := environmentFixture()
	item.Observation.Status = bootstrap.Available
	s.InitialStep = 1
	s.HostsLoaded, s.Loaded = true, true
	s.Plan.Items = []bootstrap.Item{item}
	s.RuntimePreparation.ConfigPath = config.ConfigFile
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	var restarts atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		return bootstrap.OwnerRuntimeStatus{StaleVersionDetected: restarts.Load() == 0, OwnerFingerprint: "verified-owner"}, nil
	}
	a.RestartStaleRuntimeOwnerFn = func(_ context.Context, p bootstrap.FirstRuntime, fingerprint string) error {
		if p.ConfigPath != config.ConfigFile || fingerprint != "verified-owner" {
			t.Error("restart lost configuration binding or owner fingerprint")
		}
		restarts.Add(1)
		return nil
	}
	a.inspectOwnerRuntimeStatusAsync()
	drainReading(t, updates, func() bool { return s.OwnerStatusChecked })
	tt := ui.NewTester(a.View, 1200, 820)
	if s.InitialStep != 3 || !s.FastLaneChecked || s.Busy || s.RuntimeStarted || restarts.Load() != 0 ||
		!tt.HasText("检测到旧版后台仍在运行") || !tt.HasText("立即切换至新版服务") {
		t.Fatal("ready legacy installation did not offer explicit recovery on step 3")
	}
	wizard, err := bootstrap.ReadFirstWizardState(config.ConfigFile)
	if err != nil || wizard.Stage != bootstrap.WizardStageConnectAndEnable {
		t.Fatalf("fast-lane progress was not persisted: %+v %v", wizard, err)
	}
	clickProjectUI(t, tt, "复制救援指令交给 Agent")
	if tt.Clipboard() != agentTakeoverBeta2RescuePrompt() || !tt.HasText("已复制救援指令") || restarts.Load() != 0 {
		t.Fatal("rescue copy started a restart or copied a different prompt")
	}
	tt.Advance(700 * time.Millisecond)
	fixtureImage(t, tt, "beta3-setup-stale-owner")
	daily := setupLayoutFixture()
	daily.initialSetup, daily.page, daily.access.SettingsSection = false, "设置", "服务"
	daily.environment.OwnerStatus = s.OwnerStatus
	dailyView := ui.NewTester(daily.View, 1200, 820)
	if !dailyView.HasText("检测到旧版后台仍在运行") {
		t.Fatal("service settings omitted the stale runtime banner")
	}
	daily.environment.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, DeveloperCheckout: true}
	dailyView.Frame()
	if dailyView.HasText("检测到旧版后台仍在运行") {
		t.Fatal("service settings offered to replace a developer checkout")
	}

	clickProjectUI(t, tt, "立即切换至新版服务")
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon })
	tt.Frame()
	if !s.RuntimeStarted || restarts.Load() != 1 || tt.HasText("检测到旧版后台仍在运行") {
		t.Fatalf("restart not confirmed: %q", s.StaleDaemonError)
	}
}

func TestBeta3StepThreePinsAndPreviewsSelectedAgentsInBothThemes(t *testing.T) {
	darkBefore := appliedDark != nil && *appliedDark
	t.Cleanup(func() { applyAppearance(darkBefore) })
	for _, theme := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(theme.name, func(t *testing.T) {
			applyAppearance(theme.dark)
			a := setupLayoutFixture()
			a.Config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t)}
			a.Config.GenerationFile = filepath.Join(t.TempDir(), "generation.json")
			a.Config.AgentHosts = map[string]struct {
				Executable string `json:"executable"`
			}{
				"pi":    {Executable: "/synthetic/pi"},
				"codex": {Executable: "/synthetic/codex"},
			}
			a.environment.RuntimeStarted = true
			a.environment.SelectedAgents = []string{"pi"}
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			var previews, mutations atomic.Int32
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
					t.Errorf("selected-agent preload issued %s %s", r.Method, r.URL.Path)
					return
				}
				switch r.URL.Path {
				case "/v1/connections":
					json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 8, Connections: []AgentConnection{
						{ID: "codex", Kind: "codex", Detected: true, HostVersion: "2.0.0", Status: "not_connected"},
						{ID: "pi", Kind: "pi", Detected: true, HostVersion: "1.0.0", Status: "not_connected"},
					}})
				case "/v1/generation":
					json.NewEncoder(w).Encode(GenerationDirectory{})
				case "/v1/connections/pi/preview":
					previews.Add(1)
					json.NewEncoder(w).Encode(controlplane.ConnectionPreview{
						OK: true, InstanceID: "pi", Kind: "pi", Target: "/test/pi/settings.json", ProposedSHA256: "reviewed", Revision: 8,
					})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			})}
			a.setInitialStep(3)
			drainReading(t, updates, func() bool { return a.integrations.Loaded && !a.integrations.Loading && !a.integrations.Busy })
			tt := ui.NewTester(a.View, 1200, 820)
			tt.SetDark(theme.dark)
			tt.Advance(700 * time.Millisecond)
			pi, piOK := tt.Find("pi 版本：1.0.0")
			codex, codexOK := tt.Find("Codex 版本：2.0.0")
			if !piOK || !codexOK || pi.Y >= codex.Y || !tt.HasText("已选") ||
				!tt.HasText("一键确认接入") || !tt.HasText("Agent 接入") || !tt.HasText("记忆提炼") ||
				!tt.HasText("启用并完成") || previews.Load() != 1 || mutations.Load() != 0 {
				t.Fatalf("invalid step 3 (%s): pi=%+v (%v), Codex=%+v (%v), previews=%d, mutations=%d, texts=%v", theme.name, pi, piOK, codex, codexOK, previews.Load(), mutations.Load(), tt.Texts())
			}
			fixtureImage(t, tt, "beta3-setup-connect-"+theme.name)
		})
	}
}

func TestBeta3InitializedOverviewOnlySwitchesStaleDaemonOnClick(t *testing.T) {
	config, writeConfig := beta3SetupRuntimeConfig(t)
	writeConfig(false)
	config, err := LoadConfig(config.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config, nil)
	s := &a.environment
	s.RuntimePreparation.ConfigPath = config.ConfigFile
	s.OwnerStatusChecked = true
	s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "old-owner"}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	var restarts atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		return bootstrap.OwnerRuntimeStatus{}, nil
	}
	a.RestartStaleRuntimeOwnerFn = func(_ context.Context, _ bootstrap.FirstRuntime, fingerprint string) error {
		if fingerprint != "old-owner" {
			t.Errorf("unexpected daemon fingerprint %q", fingerprint)
		}
		restarts.Add(1)
		return nil
	}
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	tt := ui.NewTester(a.View, 1200, 820)
	if a.initialSetup || a.page != "总览" || !tt.HasText("检测到旧版后台仍在运行") ||
		!tt.HasText("立即切换至新版服务") || s.RestartingStaleDaemon || restarts.Load() != 0 {
		t.Fatalf("returning user was not offered an explicit switch: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "立即切换至新版服务")
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon })
	tt.Frame()
	if restarts.Load() != 1 || !s.RuntimeStarted || tt.HasText("检测到旧版后台仍在运行") {
		t.Fatalf("explicit switch did not clear the banner: %s", s.StaleDaemonError)
	}
}

func TestBeta3IdleStepThreeWakesToRefreshAgentReceipts(t *testing.T) {
	root := t.TempDir()
	a := setupLayoutFixture()
	a.Config.ProjectControl = beta3ControlConfig(t, root)
	a.environment.RuntimeStarted, a.environment.InitialStep = true, 3
	s := &a.integrations
	s.Loaded, s.NextReceiptCheck = true, time.Now().Add(time.Hour)
	s.Connections.Connections = []AgentConnection{{ID: "pi", Kind: "pi", Status: "configured_pending_new_session"}}
	var reads atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/connections" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		reads.Add(1)
		json.NewEncoder(w).Encode(ConnectionsDirectory{})
	})}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	frames := 0
	tt := ui.NewTester(func(c *ui.Context) {
		frames++
		a.initialSetupShell(c)
	}, 1200, 820)
	tt.Advance(3 * time.Second)
	if reads.Load() != 0 || s.ReceiptCheckLoading {
		t.Fatal("a receipt was checked before its deadline")
	}
	s.NextReceiptCheck = time.Now().Add(-time.Second)
	tt.Advance(2 * time.Second)
	if !s.ReceiptCheckLoading {
		t.Fatal("idle Step 3 did not wake to check receipts")
	}
	drainReading(t, updates, func() bool { return !s.ReceiptCheckLoading })
	if reads.Load() != 1 {
		t.Fatalf("idle Step 3 checked receipts %d times", reads.Load())
	}
	s.Connections.Connections = []AgentConnection{{
		ID: "pi", Kind: "pi", Status: "configured_pending_new_session", NewSessions: 1,
		Capture: &AgentCaptureState{State: "healthy"},
	}}
	s.NextReceiptCheck = time.Time{}
	tt.Frame() // redraw when the receipt update is applied
	framesAfterReceipt := frames
	tt.Advance(5 * time.Second)
	if frames != framesAfterReceipt || reads.Load() != 1 || s.ReceiptCheckLoading {
		t.Fatal("Step 3 scheduled another wake after the pending receipt settled")
	}
}

func TestBeta3StaleOwnerUpgradeRequiresExistingRuntimeConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the onboarding environment requires macOS")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", filepath.Join(root, "components"))
	configPath := filepath.Join(root, "runtime.json")
	a := New(Config{ConfigFile: configPath, OfficialData: filepath.Join(root, "store")}, nil)
	s := &a.environment
	s.SelectionLoaded, s.Python, s.UV = true, "/bin/sh", "/bin/sh"
	s.OwnerStatusChecked = true
	s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "verified-owner"}
	a.RestartStaleRuntimeOwnerFn = func(context.Context, bootstrap.FirstRuntime, string) error {
		t.Error("stale owner restart ran without an existing runtime configuration")
		return os.ErrInvalid
	}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.restartStaleRuntimeOwner()
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon })
	if !strings.Contains(s.StaleDaemonError, "open "+configPath+":") {
		t.Fatalf("upgrade preparation did not report the missing runtime configuration: %q", s.StaleDaemonError)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("upgrade preparation created runtime configuration: %v", err)
	}
}

func TestBeta3DailyStartupFailureRequiresExplicitRetry(t *testing.T) {
	for _, mode := range []string{"daily", "deferred"} {
		t.Run(mode, func(t *testing.T) {
			config, writeConfig := beta3SetupRuntimeConfig(t)
			if mode == "deferred" {
				if err := bootstrap.SaveFirstSetupDeferred(config.ConfigFile); err != nil {
					t.Fatal(err)
				}
			} else {
				writeConfig(false)
				var err error
				config, err = LoadConfig(config.ConfigFile)
				if err != nil {
					t.Fatal(err)
				}
			}
			a := New(config, nil)
			s := &a.environment
			if a.initialSetup || a.setupDeferredMode != (mode == "deferred") {
				t.Fatal("fixture did not open the expected runtime")
			}
			updates := make(chan func(), 64)
			a.Update = func(fn func()) { updates <- fn }
			t.Cleanup(a.startupDeferred.stopAllTimers)
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
			a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
				return bootstrap.OwnerRuntimeStatus{}, nil
			}
			var attempts atomic.Int32
			a.PrepareAndStartFirstRuntimeFn = func(context.Context, Config, environmentSetupState) (bootstrap.FirstRuntime, error) {
				attempts.Add(1)
				return bootstrap.FirstRuntime{}, errors.New("synthetic startup failure")
			}
			waitForStartup := func(want int32) {
				t.Helper()
				drainReading(t, updates, func() bool { return !s.ownerInspecting && !s.Busy })
				if got := attempts.Load(); got != want || !s.AutoRuntimeAttempted || s.RuntimeStarted ||
					s.RuntimeStartError != "synthetic startup failure" {
					t.Fatalf("expected %d failed startup attempts, got %d with attempted=%t started=%t error=%q",
						want, got, s.AutoRuntimeAttempted, s.RuntimeStarted, s.RuntimeStartError)
				}
			}

			a.Start()
			waitForStartup(1)
			a.Start()
			a.finishDeferredStartup(false)
			waitForStartup(1)
			a.OnWindowFocus()
			waitForStartup(1)

			tt := ui.NewTester(a.View, 1200, 820)
			if !tt.HasText("记忆服务启动失败") || !tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
				t.Fatal("overview did not show the startup failure")
			}
			a.Config.ServiceEnvironmentFile = filepath.Join(t.TempDir(), "missing-environment.json")
			s.Error = "environment error from previous detection"
			a.page, a.access.SettingsSection = "设置", "服务"
			tt.Frame()
			if !s.Loading || s.Error != "" || s.RuntimeStartError != "synthetic startup failure" ||
				!tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
				t.Fatal("opening service settings hid the runtime recovery action")
			}
			drainReading(t, updates, func() bool { return !s.Loading })
			tt.Frame()
			if !tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
				t.Fatal("completing environment detection hid the runtime recovery action")
			}
			s.Error = "environment error from previous detection"
			a.refreshEnvironment()
			if s.Error != "" || s.RuntimeStartError != "synthetic startup failure" {
				t.Fatal("explicit environment refresh cleared the runtime startup error")
			}
			drainReading(t, updates, func() bool { return !s.Loading })
			tt.SetDark(true)
			if !tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
				t.Fatal("service settings lost the runtime recovery action in dark appearance")
			}
			a.page = "总览"
			tt.Frame()
			if !tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
				t.Fatal("overview lost the runtime recovery action after environment detection")
			}
			tt.SetDark(false)
			clickProjectUI(t, tt, "重试启动")
			waitForStartup(2)
		})
	}
}

func beta3DailyRuntimeApp(t *testing.T) (*App, chan func()) {
	t.Helper()
	config, writeConfig := beta3SetupRuntimeConfig(t)
	writeConfig(false)
	config, err := LoadConfig(config.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config, nil)
	updates := make(chan func(), 64)
	a.Update = func(fn func()) { updates <- fn }
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	t.Cleanup(a.startupDeferred.stopAllTimers)
	return a, updates
}

func TestBeta3FirstDailyOwnerInspectionFailureOffersRecheck(t *testing.T) {
	a, updates := beta3DailyRuntimeApp(t)
	s := &a.environment
	var inspections, startups atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		if inspections.Add(1) <= 2 {
			return bootstrap.OwnerRuntimeStatus{}, errors.New("synthetic inspection failure")
		}
		return bootstrap.OwnerRuntimeStatus{State: "stopped"}, nil
	}
	a.PrepareAndStartFirstRuntimeFn = func(context.Context, Config, environmentSetupState) (bootstrap.FirstRuntime, error) {
		startups.Add(1)
		return bootstrap.FirstRuntime{}, nil
	}
	a.Start()
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if s.OwnerStatusChecked || s.AutoRuntimeAttempted || startups.Load() != 0 || s.RuntimeStartError == "" {
		t.Fatal("first inspection did not leave an explicit recovery state")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("无法核对后台服务状态") || !tt.HasText("重新检查") {
		t.Fatal("overview did not offer owner recheck")
	}
	s.Loaded = true
	a.page, a.access.SettingsSection = "设置", "服务"
	tt.Frame()
	if !tt.HasText("无法核对后台服务状态") || !tt.HasText("重新检查") {
		t.Fatal("service settings did not offer owner recheck")
	}
	clickProjectUI(t, tt, "重新检查")
	drainReading(t, updates, func() bool { return !s.ownerInspecting && !s.Busy })
	tt.Frame()
	if s.OwnerStatusChecked || s.AutoRuntimeAttempted || inspections.Load() != 2 || startups.Load() != 0 ||
		!tt.HasText("重新检查") || tt.HasText("重试启动") {
		t.Fatal("repeated inspection failure did not preserve the recheck action")
	}
	clickProjectUI(t, tt, "重新检查")
	drainReading(t, updates, func() bool { return !s.ownerInspecting && !s.Busy })
	if !s.OwnerStatusChecked || !s.RuntimeStarted || s.RuntimeStartError != "" || inspections.Load() != 3 || startups.Load() != 1 {
		t.Fatal("explicit recheck did not start the runtime exactly once")
	}
}

func TestBeta3WizardFailedStaleOwnerRestartAfterStopRequiresExplicitStartup(t *testing.T) {
	config, _ := beta3SetupRuntimeConfig(t)
	config.ServiceEnvironmentFile = filepath.Join(t.TempDir(), "missing-environment.json")
	a := New(config, nil)
	s := &a.environment
	if !a.initialSetup {
		t.Fatal("fixture did not open the wizard")
	}
	_, item := environmentFixture()
	item.Observation.Status = bootstrap.Available
	s.InitialStep, s.Loaded, s.HostsLoaded, s.FastLaneChecked = 3, true, true, true
	s.Plan.Items = []bootstrap.Item{item}
	s.OwnerStatusChecked = true
	s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "old-owner"}
	s.RuntimePreparation.ConfigPath = config.ConfigFile

	updates := make(chan func(), 64)
	a.Update = func(fn func()) { updates <- fn }
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	a.DetectLocalHostsFn = func(bootstrap.HostDetectOptions) []bootstrap.DetectedHost { return nil }
	a.InspectInstallWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
		return bootstrap.InstallWorkerState{}, false, nil
	}
	const restartError = "synthetic failure after stopping old owner"
	a.RestartStaleRuntimeOwnerFn = func(context.Context, bootstrap.FirstRuntime, string) error {
		return errors.New(restartError)
	}
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		return bootstrap.OwnerRuntimeStatus{State: "stopped"}, nil
	}
	var startups atomic.Int32
	a.PrepareAndStartFirstRuntimeFn = func(context.Context, Config, environmentSetupState) (bootstrap.FirstRuntime, error) {
		startups.Add(1)
		return bootstrap.FirstRuntime{}, nil
	}

	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "立即切换至新版服务")
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon && !s.ownerInspecting })
	tt.Frame()
	if s.InitialStep != 3 || s.staleRuntimeOwner() || !s.OwnerStatusChecked || !s.FastLaneChecked ||
		s.Busy || s.RuntimeStarted || startups.Load() != 0 || s.Error != restartError ||
		!tt.HasText(restartError) || !tt.HasText("重试启动") {
		t.Fatalf("wizard did not remain stopped with explicit recovery: error=%q, busy=%t, startups=%d", s.Error, s.Busy, startups.Load())
	}

	a.OnWindowFocus()
	drainReading(t, updates, func() bool {
		return !s.ownerInspecting && !s.workerInspecting && !s.HostsLoading && !s.Loading
	})
	if s.Busy || s.RuntimeStarted || startups.Load() != 0 {
		t.Fatal("window focus implicitly started the wizard runtime")
	}
	tt.SetDark(true)
	tt.Frame()
	clickProjectUI(t, tt, "重试启动")
	if !s.Busy || s.Error != "" {
		t.Fatal("wizard retry did not start or clear the recovery error")
	}
	drainReading(t, updates, func() bool { return !s.Busy && !a.integrations.Loading })
	if s.InitialStep != 3 || !s.RuntimeStarted || s.AutoRuntimeAttempted || startups.Load() != 1 {
		t.Fatal("explicit retry did not start the wizard runtime exactly once on step 3")
	}
}

func TestBeta3FailedStaleOwnerRestartAfterStopRequiresExplicitStartup(t *testing.T) {
	a, updates := beta3DailyRuntimeApp(t)
	s := &a.environment
	s.OwnerStatusChecked = true
	s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "old-owner"}
	s.RuntimePreparation.ConfigPath = a.Config.ConfigFile
	a.RestartStaleRuntimeOwnerFn = func(context.Context, bootstrap.FirstRuntime, string) error {
		return errors.New("synthetic failure after stopping old owner")
	}
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		return bootstrap.OwnerRuntimeStatus{State: "stopped"}, nil
	}
	var startups atomic.Int32
	a.PrepareAndStartFirstRuntimeFn = func(context.Context, Config, environmentSetupState) (bootstrap.FirstRuntime, error) {
		startups.Add(1)
		return bootstrap.FirstRuntime{}, nil
	}
	a.restartStaleRuntimeOwner()
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon && !s.ownerInspecting })
	if s.staleRuntimeOwner() || s.RuntimeStarted || s.Busy || startups.Load() != 0 || a.startupDeferred.armed ||
		s.RuntimeStartError != "synthetic failure after stopping old owner" {
		t.Fatal("restart failure did not remain stopped with its recovery error")
	}
	a.OnWindowFocus()
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if s.Busy || startups.Load() != 0 || a.startupDeferred.armed {
		t.Fatal("window focus implicitly started the runtime")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText(s.RuntimeStartError) || !tt.HasText("重试启动") {
		t.Fatal("stopped runtime did not show explicit recovery")
	}
	clickProjectUI(t, tt, "重试启动")
	drainReading(t, updates, func() bool { return !s.ownerInspecting && !s.Busy })
	if !s.RuntimeStarted || s.RuntimeStartError != "" || startups.Load() != 1 {
		t.Fatal("explicit retry did not start the runtime exactly once")
	}
}

func TestBeta3FailedStaleOwnerRestartReinspectsFingerprint(t *testing.T) {
	a, updates := beta3DailyRuntimeApp(t)
	s := &a.environment
	s.OwnerStatusChecked = true
	s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "old-owner"}
	s.RuntimePreparation.ConfigPath = a.Config.ConfigFile
	var restarts atomic.Int32
	a.RestartStaleRuntimeOwnerFn = func(_ context.Context, _ bootstrap.FirstRuntime, fingerprint string) error {
		attempt := restarts.Add(1)
		if (attempt == 1 && fingerprint != "old-owner") || (attempt == 2 && fingerprint != "new-owner") {
			t.Errorf("restart used fingerprint %q", fingerprint)
		}
		return errors.New("synthetic owner change during restart")
	}
	var inspections atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		if inspections.Add(1) == 1 {
			return bootstrap.OwnerRuntimeStatus{}, errors.New("synthetic inspection failure")
		}
		return bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "new-owner"}, nil
	}

	a.restartStaleRuntimeOwner()
	nextStartupUpdate(t, updates)()
	if s.RestartingStaleDaemon || s.OwnerStatusChecked || !s.ownerInspecting || s.StaleDaemonError == "" {
		t.Fatal("failed restart did not surface the error and invalidate the old owner check")
	}
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	tt := ui.NewTester(a.View, 1200, 820)
	if s.OwnerStatusChecked || s.RuntimeStartError == "" || !tt.HasText("重新检查") || !tt.HasText("立即切换至新版服务") {
		t.Fatal("failed follow-up inspection did not offer recheck")
	}
	clickProjectUI(t, tt, "立即切换至新版服务")
	if s.RestartingStaleDaemon {
		t.Fatal("switch button accepted an unverified fingerprint")
	}
	a.restartStaleRuntimeOwner()
	if s.RestartingStaleDaemon || restarts.Load() != 1 {
		t.Fatal("unverified fingerprint allowed an owner switch")
	}
	clickProjectUI(t, tt, "重新检查")
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if !s.OwnerStatusChecked || s.OwnerStatus.OwnerFingerprint != "new-owner" {
		t.Fatalf("owner fingerprint was not refreshed: %+v", s.OwnerStatus)
	}
	clickProjectUI(t, tt, "立即切换至新版服务")
	drainReading(t, updates, func() bool { return !s.RestartingStaleDaemon && !s.ownerInspecting })
	if restarts.Load() != 2 {
		t.Fatal("verified fingerprint did not re-enable switching")
	}
}

func TestBeta3StaleOwnerRestartReverifiesReloadedGraphAssets(t *testing.T) {
	config, writeConfig := beta3SetupRuntimeConfig(t)
	writeConfig(false)
	raw, err := os.ReadFile(config.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var runtimeConfig map[string]any
	if err := json.Unmarshal(raw, &runtimeConfig); err != nil {
		t.Fatal(err)
	}
	assetsDir := t.TempDir()
	entries, err := fs.ReadDir(memlane.RuntimeAssets, "web/official-graph/dist")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		data, err := fs.ReadFile(memlane.RuntimeAssets, "web/official-graph/dist/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runtimeConfig["graph_assets"] = assetsDir
	runtimeConfig["projects"] = []Project{{ID: "project", CodeDataset: "code-dataset"}}
	raw, err = json.Marshal(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigFile, raw, 0600); err != nil {
		t.Fatal(err)
	}

	config, err = LoadConfig(config.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.TokenFile, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"asset_error", "attach_error_same_dir", "attached_host_changed_dir"} {
		t.Run(scenario, func(t *testing.T) {
			previous := config
			if scenario != "attach_error_same_dir" {
				previous.GraphAssets = filepath.Join(t.TempDir(), "old-assets")
			}
			a := New(previous, nil)
			s := &a.environment
			s.OwnerStatusChecked = true
			s.OwnerStatus = &bootstrap.OwnerRuntimeStatus{StaleVersionDetected: true, OwnerFingerprint: "old-owner"}
			s.RuntimePreparation.ConfigPath = config.ConfigFile
			var oldHost *GraphHost
			a.applyGraphAssetCheck(map[string][]byte{"old": []byte("old")}, nil)
			switch scenario {
			case "asset_error":
				a.graphAssetsErr = errors.New("old asset error")
				a.graphError = a.graphAssetsErr.Error()
			case "attach_error_same_dir":
				a.graphAttachErr = errors.New("old attach error")
				a.graphError = a.graphAttachErr.Error()
				a.graphAttachRequested = true
			case "attached_host_changed_dir":
				a.syncReadingIdentity()
				oldHost = &GraphHost{}
				a.graph = oldHost
				a.graphError = "old host render error"
				a.graphSelection = "old node"
				a.page, a.project, a.graphMode = "图谱", 0, "代码结构"
			}
			attachRequested := scenario != "asset_error"
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/visualize/json" && r.URL.Query().Get("dataset_id") == "code-dataset" {
					w.Write([]byte(`{"nodes":[{"id":"reloaded"}],"links":[],"color_maps":{}}`))
					return
				}
				w.Write([]byte("{}"))
			})}
			a.graphAttachFn = func(host *GraphHost) error {
				if !attachRequested {
					t.Error("graph attached before it was requested")
				}
				t.Cleanup(host.close)
				return nil
			}
			a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
				return bootstrap.OwnerRuntimeStatus{}, nil
			}
			a.RestartStaleRuntimeOwnerFn = func(context.Context, bootstrap.FirstRuntime, string) error { return nil }

			a.restartStaleRuntimeOwner()
			nextStartupUpdate(t, updates)()
			if !s.RuntimeStarted || a.graphAssetsDir != assetsDir || a.graphAssetsChecked ||
				a.graphAssets != nil || a.graphAssetsErr != nil || a.graphAttachErr != nil ||
				a.graphError != "" || a.graphSelection != "" || a.graphAssetsCheck == nil || a.graph != nil ||
				(oldHost != nil && (!oldHost.closed || !a.graphLoading)) {
				t.Fatalf("restart did not reset graph host and schedule verification for %q: %q", assetsDir, a.graphError)
			}
			drainReading(t, updates, func() bool { return a.graphAssetsChecked && !a.integrations.Loading && !a.graphLoading })
			if a.graphAssetsErr != nil || len(a.graphAssets["app.js"]) == 0 || a.graphAssetsCheck != nil ||
				(a.graph != nil) != attachRequested {
				t.Fatalf("reloaded graph assets were not verified and attached as requested: %v", a.graphAssetsErr)
			}
			if oldHost != nil && (a.graph == oldHost || a.graph.nodes["reloaded"] == nil || a.graphError != "") {
				t.Fatalf("active graph did not load data on the replacement host: %q", a.graphError)
			}
		})
	}
}

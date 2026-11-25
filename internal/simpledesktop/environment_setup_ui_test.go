package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func TestEnvironmentManualSelectionRestoresAcrossAppInstances(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", filepath.Join(root, "components"))
	cfg := Config{ConfigFile: "/synthetic/runtime.json", Python: "/original/python"}
	options := bootstrap.EnvironmentOptions{Root: os.Getenv("MEMLANE_BOOTSTRAP_ROOT"), Python: "/manual/python", ModelCache: "/manual/cache"}
	if err := bootstrap.SaveSelections(options, cfg.ConfigFile); err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil)
	restored, err := loadEnvironmentOptions(a.Config, a.environment)
	if err != nil || restored.Python != "/manual/python" || restored.ModelCache != "/manual/cache" {
		t.Fatal(restored, err)
	}
	b := New(cfg, nil)
	restored, err = loadEnvironmentOptions(b.Config, b.environment)
	if err != nil || restored.Python != "/manual/python" {
		t.Fatal(restored, err)
	}
}

func TestEnvironmentPathDialogCancelPreservesSelectedPaths(t *testing.T) {
	for _, action := range []string{"取消", "关闭路径编辑"} {
		t.Run(action, func(t *testing.T) {
			a, _ := environmentFixture()
			s := &a.environment
			s.Plan.Items[0].Observation.Status = bootstrap.Available
			s.Python, s.ModelCache, s.Source = "/selected/python", "/selected/model", "/selected/source"
			s.Details, s.SelectionLoaded = true, true
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Themed(c, homeTheme).Fill().Children(func() {
					ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) })
				})
			}, 1200, 820)
			clickProjectUI(t, tt, "指定已有路径")
			if !s.PathDialog || s.PathDraft.Python != "/selected/python" {
				t.Fatal("editor did not snapshot the selected paths")
			}
			clickProjectUI(t, tt, "输入Python 路径")
			tt.Type("/draft")
			if s.Python != "/selected/python" || s.PathDraft.Python == s.Python {
				t.Fatalf("typing did not edit only the draft: selected=%q draft=%q", s.Python, s.PathDraft.Python)
			}
			clickProjectUI(t, tt, action)
			if s.PathDialog || s.PathDraft != (environmentPathDraft{}) || s.Python != "/selected/python" || s.ModelCache != "/selected/model" || s.Source != "/selected/source" || s.Generation != 0 {
				t.Fatal("cancel or close applied the draft or started detection")
			}
			clickProjectUI(t, tt, "指定已有路径")
			if s.PathDraft.Python != "/selected/python" || s.PathDraft.ModelCache != "/selected/model" {
				t.Fatal("reopen restored the cancelled draft")
			}
		})
	}
}
func TestEnvironmentDiagnosticExportWithoutServiceUsesNativeSaveChoice(t *testing.T) {
	a, _ := environmentFixture()
	path := filepath.Join(t.TempDir(), "failure.zip")
	a.ChooseDiagnosticPath = func() (string, error) { return path, nil }
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	a.exportEnvironmentDiagnostic()
	drainReading(t, updates, func() bool { return !a.environment.Exporting })
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if a.environment.Message != "诊断包已导出" || a.environment.Environment != nil {
		t.Fatal("diagnostic export depended on installed service")
	}
}

func environmentFixture() (*App, bootstrap.Item) {
	a := New(Config{}, nil)
	a.environment.Loaded = true
	item := bootstrap.Item{Requirement: bootstrap.Requirement{ID: "python", Name: "Python", Purpose: "运行本地记忆引擎", Version: "3.12.12", Source: "https://official.example.test/pinned", Destination: "/synthetic/components/python", Digest: "fixed", DownloadBytes: 10 << 20, InstalledBytes: 30 << 20}, Observation: bootstrap.Observation{Status: bootstrap.Missing}}
	a.environment.Plan = bootstrap.Plan{Items: []bootstrap.Item{item}}
	return a, item
}
func environmentTester(a *App) *ui.Tester {
	a.initialSetup = true
	a.environment.InitialStep = 2
	return ui.NewTester(a.View, 1200, 820)
}

func TestOnboardingEnvironmentRecordsPageConsentForDisplayedPlan(t *testing.T) {
	a, _ := environmentFixture()
	s := &a.environment
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Environment = &bootstrap.Environment{Options: bootstrap.EnvironmentOptions{Root: root}}
	updates := make(chan func(), 24)
	a.Update = func(fn func()) { updates <- fn }
	a.InspectInstallWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
		return bootstrap.InstallWorkerState{}, false, nil
	}
	wantDigest := s.Plan.Digest()
	tt := environmentTester(a)
	for _, label := range []string{"需下载 10.0 MiB", "下载并安装 · 10.0 MiB", "点击即表示同意安装上方「待下载」的组件"} {
		if !tt.HasText(label) {
			t.Fatalf("missing consent scope %q: %v", label, tt.Texts())
		}
	}
	if _, err := s.Environment.Installer().Snapshot(); !os.IsNotExist(err) {
		t.Fatalf("displaying plan recorded consent: %v", err)
	}
	clickProjectUI(t, tt, "下载并安装 · 10.0 MiB")
	if s.Review || tt.HasText("确认安装") || !s.Busy {
		t.Fatal("the page action did not start installation directly")
	}
	drainReading(t, updates, func() bool { return !s.Busy })
	got, err := s.Environment.Installer().Snapshot()
	if err != nil || got.ApprovedDigest != wantDigest {
		t.Fatalf("page consent receipt = %#v, %v, want %s", got, err, wantDigest)
	}
	// The synthetic driver fails at the probe. The consent and redacted
	// failure events are still visible, with the log opened automatically.
	tt.Frame()
	if !s.OnboardingLogOpen || !tt.HasText("重试") || !tt.HasText("复制日志") || !strings.Contains(s.onboardingLogText(), wantDigest) {
		t.Fatalf("failure omitted the live consent and failure log: %v", tt.Texts())
	}
}

func TestAvailableEnvironmentContinuesWithoutInstallAction(t *testing.T) {
	a, _ := environmentFixture()
	a.environment.Plan.Items[0].Observation.Status = bootstrap.Available
	tt := environmentTester(a)
	if !tt.HasText("无需下载") || !tt.HasText("启动服务并继续") || tt.HasText("下载并安装") {
		t.Fatalf("compatible environment requires duplicate installation: %v", tt.Texts())
	}
}

func TestOnboardingEnvironmentStartsAndRetriesAfterOwnerCheck(t *testing.T) {
	config, _ := beta3SetupRuntimeConfig(t)
	a, item := environmentFixture()
	a.Config, a.Client.Config = config, config
	s := &a.environment
	item.Observation.Status = bootstrap.Available
	s.Plan.Items = []bootstrap.Item{item}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	var inspections, startups atomic.Int32
	a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
		inspections.Add(1)
		return bootstrap.OwnerRuntimeStatus{State: "stopped"}, nil
	}
	a.PrepareAndStartFirstRuntimeFn = func(context.Context, Config, environmentSetupState) (bootstrap.FirstRuntime, error) {
		if startups.Add(1) == 1 {
			return bootstrap.FirstRuntime{}, errors.New("启动失败 API_KEY=secret-value")
		}
		return bootstrap.FirstRuntime{ConfigPath: config.ConfigFile}, nil
	}
	tt := environmentTester(a)

	a.inspectOwnerRuntimeStatusAsync()
	drainReading(t, updates, func() bool { return !s.ownerInspecting })
	if s.Busy || startups.Load() != 0 {
		t.Fatal("inspection alone started the service")
	}
	clickProjectUI(t, tt, "启动服务并继续")
	drainReading(t, updates, func() bool { return !s.Busy })
	tt.Frame()
	if !s.OnboardingRuntimeFailed || !s.OnboardingLogOpen || a.onboardingPage() != onboardingEnv ||
		strings.Contains(s.onboardingLogText(), "secret-value") || !tt.HasText("重新启动服务") {
		t.Fatal("startup failure did not remain recoverable with a safe log")
	}

	// Ownership may be unknown when the runtime configuration has only just
	// been written by the first startup attempt. One retry must do both jobs.
	s.OwnerStatusChecked, s.OwnerStatus = false, nil
	clickProjectUI(t, tt, "重新启动服务")
	if !s.ownerInspecting || !s.onboardingStartRequested || s.Busy {
		t.Fatal("retry did not await owner inspection")
	}
	drainReading(t, updates, func() bool { return !s.ownerInspecting && !s.onboardingStartRequested && !s.Busy })
	if inspections.Load() != 2 || startups.Load() != 2 || !s.RuntimeStarted || a.onboardingPage() != onboardingConnect ||
		a.onboarding.toast != "本地记忆服务已就绪" || !strings.Contains(s.onboardingLogText(), "健康检查") {
		t.Fatalf("valid recovery did not start and advance exactly once: inspections=%d starts=%d page=%d error=%q", inspections.Load(), startups.Load(), a.onboardingPage(), s.Error)
	}
}

func TestInstallationFailureCanCopyDiagnosticsWithoutRuntime(t *testing.T) {
	a, item := environmentFixture()
	a.environment.Error = "download failed API_KEY=private-secret"
	a.environment.Journal = bootstrap.Journal{Operation: "first-install", Plan: a.environment.Plan, Status: bootstrap.Failed, Steps: map[string]bootstrap.Step{item.Requirement.ID: {Status: bootstrap.Failed, Attempts: 1, Error: a.environment.Error}}}
	tt := environmentTester(a)
	for _, label := range []string{"准备未完成", "下载并安装 · 10.0 MiB", "改用官方源", "复制诊断，交给 Agent 排查"} {
		if !tt.HasText(label) {
			t.Fatalf("failure action %q missing: %v", label, tt.Texts())
		}
	}
	clickProjectUI(t, tt, "复制诊断，交给 Agent 排查")
	if !strings.Contains(tt.Clipboard(), "[已脱敏]") || strings.Contains(tt.Clipboard(), "private-secret") {
		t.Fatal("copied incomplete or unredacted diagnostics")
	}
	if a.environment.Environment != nil || a.environment.Busy {
		t.Fatal("diagnostics depended on runtime setup")
	}
}

func TestOnboardingEnvironmentUpdatesFiveRowsAndRedactsBoundedLog(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a, item := environmentFixture()
			s := &a.environment
			s.Busy = true
			s.Progress = bootstrap.InstallProgress{Stage: "downloading", Component: item.Requirement.ID, BytesDone: 3 << 20, BytesTotal: 10 << 20}
			s.appendOnboardingLog(makeOnboardingLogLines("[python] 开始下载", false))
			tt := environmentTester(a)
			tt.SetDark(dark)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			for _, label := range []string{"隔离运行环境", "本地记忆引擎", "本地语义搜索模型", "Agent 接入组件", "启动本地记忆服务", "30%", "安装日志（1 行）"} {
				if !tt.HasText(label) {
					t.Fatalf("missing %q while downloading: %v", label, tt.Texts())
				}
			}
			fixtureImage(t, tt, fmt.Sprintf("focused-environment-progress-dark-%t", dark))
			if s.OnboardingLogOpen || tt.HasText("复制日志") {
				t.Fatal("log did not default to collapsed")
			}
			clickProjectUI(t, tt, "安装日志（1 行）")
			s.appendOnboardingLog(makeOnboardingLogLines(strings.Repeat("[python] 已下载\n", onboardingLogMaxLines), false))
			tt.Frame()
			if s.onboardingLogScroll.Y != s.onboardingLogScroll.MaxY || s.onboardingLogScroll.MaxY <= 0 {
				t.Fatal("live log did not follow the end")
			}
			clickProjectUI(t, tt, "安装日志（500 行）")
			s.Busy = false
			s.Error = "下载失败"
			s.Journal = bootstrap.Journal{Plan: s.Plan, Steps: map[string]bootstrap.Step{item.Requirement.ID: {Status: bootstrap.Failed}}}
			s.appendOnboardingLog(makeOnboardingLogLines("[python] 失败：API_KEY=private-secret", true))
			tt.Frame()
			if !s.OnboardingLogOpen || len(s.OnboardingLog) != onboardingLogMaxLines || !s.OnboardingLog[len(s.OnboardingLog)-1].failed {
				t.Fatal("failed log outside bounded open log")
			}
			tt.Scroll(1150, 620, 0, 550)
			clickProjectUI(t, tt, "复制日志")
			if !strings.Contains(tt.Clipboard(), "[已脱敏]") || strings.Contains(tt.Clipboard(), "private-secret") {
				t.Fatal("log copy exposed the original installer text")
			}
			for n, component := range onboardingEnvironmentComponents {
				row := s.onboardingComponentRow(component, s.Progress)
				if failed := row.status == bootstrap.Failed; failed != (n == 0) {
					t.Fatalf("wrong failure status for %s: %s", row.id, row.status)
				}
			}
		})
	}
}

func TestOnboardingWorkerCompletionReturnsToTheMatchingRuntime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override func(*bootstrap.InstallWorkerState)
		wantStep int
	}{
		{"completed startup", func(*bootstrap.InstallWorkerState) {}, 3},
		{"components only", func(w *bootstrap.InstallWorkerState) { w.StartRuntime = false }, 2},
		{"different runtime", func(w *bootstrap.InstallWorkerState) { w.ConfigPath += ".other" }, 2},
		{"failed startup", func(w *bootstrap.InstallWorkerState) { w.State = "failed" }, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := beta3SetupRuntimeConfig(t)
			a, _ := environmentFixture()
			a.Config, a.Client.Config, a.initialSetup = config, config, true
			s := &a.environment
			s.InitialStep, s.ExternalWorkerActive = 2, true
			s.ExternalWorker = bootstrap.InstallWorkerState{State: "running", Stage: "starting_runtime"}
			worker := bootstrap.InstallWorkerState{State: "completed", Stage: "ready", StartRuntime: true, ConfigPath: config.ConfigFile}
			tc.override(&worker)
			a.InspectInstallWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
				return worker, false, nil
			}
			a.InspectOwnerRuntimeStatusFn = func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
				return bootstrap.OwnerRuntimeStatus{}, errors.New("synthetic owner verification hold")
			}
			t.Setenv("MEMLANE_BOOTSTRAP_ROOT", "relative-component-root")
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.inspectInstallWorkerAsync()
			drainReading(t, updates, func() bool { return s.Loading })
			if s.InitialStep != tc.wantStep || s.RuntimeStarted || s.ExternalWorkerActive {
				t.Fatalf("unexpected post-worker state: step=%d started=%t active=%t", s.InitialStep, s.RuntimeStarted, s.ExternalWorkerActive)
			}
			if !strings.Contains(s.onboardingLogText(), "命令行安装") {
				t.Fatal("missing redacted CLI receipt log")
			}
			drainReading(t, updates, func() bool { return !s.Loading && !s.ownerInspecting })
		})
	}
}

func TestOnboardingSwitchToOfficialRedetectsBeforeConsent(t *testing.T) {
	a, _ := environmentFixture()
	s := &a.environment
	// An invalid relative root lets us assert the detection boundary without
	// contacting the network or reading any daily configuration.
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", "relative-component-root")
	s.SelectionLoaded = true
	s.Error = "下载失败"
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	tt := environmentTester(a)
	clickProjectUI(t, tt, "改用官方源")
	if s.DownloadSource != bootstrap.DownloadSourceOfficial || !s.Loading || s.Busy {
		t.Fatal("official retry did not re-detect before installing")
	}
	drainReading(t, updates, func() bool { return !s.Loading })

}

func TestRestartedInstallationReceiptNeverPretendsWorkerIsRunning(t *testing.T) {
	a, item := environmentFixture()
	j := bootstrap.Journal{Plan: a.environment.Plan, Steps: map[string]bootstrap.Step{item.Requirement.ID: {Status: bootstrap.Installing}}}
	if got := environmentRowStatus(item, j, false); got != "unconfirmed" {
		t.Fatal(got)
	}
	if got := environmentRowStatus(item, j, true); got != bootstrap.Installing {
		t.Fatal(got)
	}
	item.Observation.Status = bootstrap.Available
	if got := environmentRowStatus(item, j, false); got != bootstrap.Available {
		t.Fatal(got)
	}
}

func TestExplicitMissingRuntimeDoesNotBecomeAnotherFirstInstallation(t *testing.T) {
	app := New(Config{ConfigFile: "/synthetic/daily/runtime.json"}, os.ErrNotExist)
	if app.initialSetup || app.configError == "" {
		t.Fatal("missing existing configuration silently became a new library")
	}
	fresh := New(Config{ConfigFile: "/synthetic/default/runtime.json", FirstInstallDefault: true}, os.ErrNotExist)
	if !fresh.initialSetup {
		t.Fatal("default first installation unavailable")
	}
}

func TestFirstWizardKeepsEnvironmentAndConnectionActionsSeparate(t *testing.T) {
	app, _ := environmentFixture()
	app.initialSetup = true
	app.environment.InitialStep = 2
	app.environment.RuntimeStarted = true
	app.environment.Plan.Items[0].Observation.Status = bootstrap.Available
	tester := ui.NewTester(app.View, 1200, 820)
	if tester.HasText("验证并启用") || !tester.HasText("本地记忆引擎") {
		t.Fatal("wizard showed generation UI before environment completed")
	}
	clickProjectUI(t, tester, "继续")
	if app.environment.InitialStep != 3 || app.onboardingPage() != onboardingConnect || tester.HasText("验证并启用") {
		t.Fatal("environment did not advance to the separate connection step")
	}
}

func TestDependencyBrandIconsIncludeRuntimeAndDistinctAgentPlugin(t *testing.T) {
	for _, id := range []string{"python", "uv", "runtime", "cognee-source", "embedding", "agent-plugin"} {
		if dependencyBrandIcons[id] == nil {
			t.Fatalf("missing embedded dependency SVG for %q", id)
		}
	}
	if dependencyBrandIcons["agent-plugin"] == dependencyBrandIcons["cognee-source"] {
		t.Fatal("agent-plugin icon must be distinct from cognee-source icon")
	}
	if dependencyBrandIcons["runtime"] == dependencyBrandIcons["cognee-source"] {
		t.Fatal("runtime icon must be distinct from cognee-source icon")
	}
}

func TestIncompatibleAgentPluginExplainsReasonAndDailyScope(t *testing.T) {
	makeApp := func(initial bool, service string) *App {
		a := New(Config{
			ProjectControl: &ProjectControlConfig{
				URL:       "http://127.0.0.1:18777",
				PluginDir: "/synthetic/daily/old-plugin",
			},
		}, nil)
		a.initialSetup = initial
		a.service = service
		a.environment.Loaded = true
		a.environment.Plan = bootstrap.Plan{Items: []bootstrap.Item{{
			Requirement: bootstrap.Requirement{
				ID:             "agent-plugin",
				Name:           "Agent 插件",
				Purpose:        "为已有 Agent 准备官方 Cognee 接入插件及固定权限适配",
				Version:        "1.8.0 · a176248 · edc059dd",
				Source:         "https://official.example.test/plugin.tar.gz",
				Destination:    "/synthetic/components/plugin-integrations",
				DownloadBytes:  20 << 20,
				InstalledBytes: 60 << 20,
			},
			Observation: bootstrap.Observation{
				Status: bootstrap.Incompatible,
				Reason: "插件固定补丁回执不同",
			},
		}}}
		return a
	}

	// Connected daily settings view shows calm "有更新", plain-language copy, and hides jargon unless details expanded.
	daily := makeApp(false, "已连接")
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() {
			ui.Scroll(c).Fill().Children(func() { daily.environmentSetup(c, false) })
		})
	}, 1200, 820)
	wantHint := "当前服务正在使用旧版接入插件，不影响正常使用。点击下方「更新 Agent 插件」确认后，会下载新版并自动切换配置，原插件目录会保留以便回退。"
	if !tt.HasText("有更新") || !tt.HasText(wantHint) {
		t.Fatalf("missing daily update label or plain hint: %v", tt.Texts())
	}
	if tt.HasText("不兼容") || tt.HasText("固定补丁回执") || tt.HasText("安装缺少的依赖") {
		t.Fatalf("daily update leaked scary jargon or wrong button: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "查看详情")
	if !tt.HasText("技术详情：插件固定补丁回执不同") {
		t.Fatalf("missing technical details after expanding details: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "更新 Agent 插件")
	if !tt.HasText("确认更新 Agent 插件") || !tt.HasText("确认更新") || !tt.HasText("暂不更新") {
		t.Fatalf("missing update confirmation modal copy: %v", tt.Texts())
	}
	if !tt.HasText("说明：" + wantHint) {
		t.Fatalf("missing plain explanation inside update modal: %v", tt.Texts())
	}

	// First-install and disconnected service keep strict incompatible status and install action.
	for _, tc := range []struct {
		name    string
		initial bool
		service string
	}{
		{"first_install", true, ""},
		{"disconnected", false, "未连接"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := makeApp(tc.initial, tc.service)
			var tester *ui.Tester
			label := "安装缺少的依赖"
			if tc.initial {
				tester = environmentTester(app)
				clickProjectUI(t, tester, "技术清单（版本、校验值与安装位置）")
				label = "下载并安装 · 20.0 MiB"
			} else {
				tester = ui.NewTester(func(c *ui.Context) {
					ui.Themed(c, homeTheme).Fill().Children(func() { app.environmentSetup(c, false) })
				}, 1200, 820)
			}
			if !tester.HasText("不兼容") || !tester.HasText(label) {
				t.Fatalf("%s did not preserve strict incompatible UI: %v", tc.name, tester.Texts())
			}
		})
	}
}

func TestSettingsEnvironmentUsesV4SkeletonWhileLoading(t *testing.T) {
	a := New(Config{}, nil)
	a.environment.Loading = true
	a.environment.Loaded = false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() {
			ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) })
		})
	}, 1200, 820)
	if !tt.HasText("运行环境") || !tt.HasText("重新检测") || !tt.HasText("查看详情") {
		t.Fatalf("missing V4 group header controls: %v", tt.Texts())
	}
	if tt.HasText("检测中…") {
		t.Fatalf("settings environment still renders legacy plain text loading state: %v", tt.Texts())
	}
}

func TestEnvironmentPolicyUpdateUsesUpdateActionAcrossSetupStates(t *testing.T) {
	for _, tc := range []struct {
		name, service string
		initial       bool
	}{
		{name: "connected", service: "已连接"},
		{name: "disconnected", service: "未连接"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, item := environmentFixture()
			a.initialSetup, a.service = tc.initial, tc.service
			a.Config.ConfigFile = "/synthetic/runtime.json"
			a.Config.ProjectControl = &ProjectControlConfig{PluginDir: "/synthetic/old"}
			item.Requirement.ID, item.Requirement.Name = "agent-plugin", "Agent 插件"
			item.Observation = bootstrap.Observation{Status: bootstrap.Incompatible, Reason: bootstrap.PluginPolicyUpdateReasonPrefix}
			a.environment.Plan.Items = []bootstrap.Item{item}
			var tt *ui.Tester
			if tc.initial {
				tt = environmentTester(a)
				clickProjectUI(t, tt, "查看技术明细 (1 项)")
			} else {
				tt = ui.NewTester(func(c *ui.Context) {
					ui.Themed(c, homeTheme).Fill().Children(func() {
						ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) })
					})
				}, 1200, 820)
			}
			if !tt.HasText("策略有更新") || !tt.HasText(bootstrap.PluginPolicyUpdateReasonPrefix) ||
				!tt.HasText("更新 Agent 插件") || tt.HasText("不兼容") || tt.HasText("安装缺少的依赖") {
				t.Fatalf("wrong plugin update presentation: %v", tt.Texts())
			}
			clickProjectUI(t, tt, "更新 Agent 插件")
			for _, label := range []string{"确认更新 Agent 插件", "确认更新", "暂不更新", "策略有更新"} {
				if !tt.HasText(label) {
					t.Fatalf("missing %q in review: %v", label, tt.Texts())
				}
			}
		})
	}
}

func TestSettingsEnvironmentShowsRedactedErrorsWithDetailsCollapsed(t *testing.T) {
	a, _ := environmentFixture()
	a.environment.Error = "下载失败 API_KEY=private-secret https://user:private-password@example.test/file"
	tt := ui.NewTester(func(c *ui.Context) { a.environmentSetup(c, false) }, 1200, 820)
	for _, expanded := range []bool{false, true} {
		a.environment.Details = expanded
		tt.Frame()
		if !tt.HasText("准备未完成") || !tt.HasText("下载失败") ||
			tt.HasText("private-secret") || tt.HasText("private-password") {
			t.Fatalf("unsafe or hidden error summary: %v", tt.Texts())
		}
	}
}

func TestEnvironmentPolicyUpdateSelectsPersistentConfigTargetWhileDisconnected(t *testing.T) {
	a, item := environmentFixture()
	a.Config.ConfigFile = "/synthetic/runtime.json"
	a.Config.ProjectControl = &ProjectControlConfig{PluginDir: "/synthetic/old"}
	a.service = "未连接"
	item.Requirement.ID = "agent-plugin"
	item.Observation = bootstrap.Observation{Status: bootstrap.Incompatible, Reason: bootstrap.PluginPolicyUpdateReasonPrefix}
	plan := bootstrap.Plan{Items: []bootstrap.Item{item}}
	if destination := a.managedPluginUpdateDestination(plan); destination != item.Requirement.Destination {
		t.Fatalf("disconnected policy upgrade did not select the verified component target: %q", destination)
	}
	plan.Items[0].Observation.Reason = "插件固定补丁回执不同"
	if a.managedPluginUpdateDestination(plan) != "" {
		t.Fatal("disconnected version mismatch should require environment preparation")
	}
	a.service = "已连接"
	if a.managedPluginUpdateDestination(plan) != item.Requirement.Destination {
		t.Fatal("connected daily plugin update lost the persistent config target")
	}
	plan.Items[0].Observation.Reason = bootstrap.PluginPolicyUpdateReasonPrefix
	a.initialSetup = true
	if a.managedPluginUpdateDestination(plan) != "" {
		t.Fatal("first installation attempted to update an existing daily config")
	}
}

func TestSettingsEnvironmentRetainsPluginConfigApplyActionWhenWriteFailed(t *testing.T) {
	a, item := environmentFixture()
	a.Config.ConfigFile = "/synthetic/runtime.json"
	a.Config.ProjectControl = &ProjectControlConfig{PluginDir: "/synthetic/old-plugin"}
	item.Requirement.ID, item.Requirement.Name = "agent-plugin", "Agent 插件"
	item.Observation = bootstrap.Observation{Status: bootstrap.Available, Path: "/synthetic/new-plugin"}
	a.environment.Plan = bootstrap.Plan{Items: []bootstrap.Item{item}}
	a.environment.PendingPluginApplyDir = "/synthetic/new-plugin"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() {
			ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) })
		})
	}, 1200, 820)
	if tt.HasText("已就绪") || !tt.HasText("待写入配置") || !tt.HasText("写入插件配置") {
		t.Fatalf("pending plugin config apply was hidden as ready: %v", tt.Texts())
	}
}

func TestEnvironmentReusesRetainedInstallationOperation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const operation = "retained-cli-install"
	a, _ := environmentFixture()
	s := &a.environment
	s.Environment = &bootstrap.Environment{Options: bootstrap.EnvironmentOptions{Root: root}}
	installer := s.Environment.Installer()
	// An unknown component fails locally after Run has accepted the journal.
	s.Plan = bootstrap.Plan{Items: []bootstrap.Item{{Requirement: bootstrap.Requirement{ID: "synthetic-component"}}}}
	if err := installer.Review(operation, s.Plan, s.Plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
	s.Journal, err = installer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.InspectInstallWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
		return bootstrap.InstallWorkerState{}, false, nil
	}
	a.runEnvironment(false)
	drainReading(t, updates, func() bool { return !s.Busy })
	if s.Journal.Operation != operation || s.Journal.Status != bootstrap.Failed || !strings.Contains(s.Error, "fixed environment plan") {
		t.Fatalf("GUI did not resume retained installation: %s, %+v", s.Error, s.Journal)
	}
	a.declineEnvironment()
	drainReading(t, updates, func() bool { return !s.Busy })
	journal, err := installer.Snapshot()
	if err != nil || journal.Operation != operation || journal.DeclinedDigest != s.Plan.Digest() {
		t.Fatalf("GUI did not decline under retained operation: %+v, %v", journal, err)
	}
	a.runEnvironment(true)
	drainReading(t, updates, func() bool { return !s.Busy })
	journal, err = installer.Snapshot()
	if err != nil || journal.Operation != operation || journal.ApprovedDigest != s.Plan.Digest() || journal.DeclinedDigest != "" {
		t.Fatalf("GUI did not approve under retained operation: %+v, %v", journal, err)
	}
}

func TestOnboardingRetryRequiresCurrentPlanConsent(t *testing.T) {
	for _, scenario := range []string{"diagnostic", "changed digest", "approved install failure"} {
		t.Run(scenario, func(t *testing.T) {
			a, item := environmentFixture()
			s := &a.environment
			s.Environment = &bootstrap.Environment{}
			s.Journal = bootstrap.Journal{Operation: "first-install", ApprovedDigest: s.Plan.Digest(), Plan: bootstrap.Plan{Items: []bootstrap.Item{item}}}
			switch scenario {
			case "diagnostic":
				a.ChooseDiagnosticPath = func() (string, error) { return "", errors.New("无法导出诊断") }
				a.exportEnvironmentDiagnostic()
			default:
				s.Error = "下载失败"
				s.Journal.Status = bootstrap.Failed
				s.onboardingInstallFailureDigest = s.Journal.ApprovedDigest
				s.Journal.ActiveStep = item.Requirement.ID
				s.Journal.Steps = map[string]bootstrap.Step{item.Requirement.ID: {Status: bootstrap.Failed, Error: s.Error}}
				if scenario == "changed digest" {
					s.Plan.Items[0].Requirement.Digest = "new-digest"
				}
			}
			label, _, action := a.onboardingEnvironmentAction()
			if scenario == "approved install failure" {
				if label != "重试" {
					t.Fatalf("approved installation failed, action = %q", label)
				}
				a.Update = func(func()) { t.Error("stale retry tried to start installation") }
				s.Plan.Items[0].Requirement.Digest = "new-digest"
				action()
				if s.Busy {
					t.Fatal("retry from the old frame accepted a changed plan")
				}
			} else if label != "下载并安装 · 10.0 MiB" || a.onboardingEnvironmentHint() != "点击即表示同意安装上方「待下载」的组件" {
				t.Fatalf("page consent missing: %q, %q", label, a.onboardingEnvironmentHint())
			}
		})
	}
}

func TestOnboardingStopInstallationUsesNonFailureMessage(t *testing.T) {
	a, _ := environmentFixture()
	s := &a.environment
	s.Environment = &bootstrap.Environment{Options: bootstrap.EnvironmentOptions{Root: t.TempDir()}}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.InspectInstallWorkerFn = func(ctx context.Context, _ string) (bootstrap.InstallWorkerState, bool, error) {
		<-ctx.Done()
		return bootstrap.InstallWorkerState{}, false, ctx.Err()
	}
	tt := environmentTester(a)
	clickProjectUI(t, tt, "下载并安装 · 10.0 MiB")
	clickProjectUI(t, tt, "停止")
	drainReading(t, updates, func() bool { return !s.Busy })
	tt.Frame()
	label, _, _ := a.onboardingEnvironmentAction()
	if s.Error != "" || tt.HasText("context canceled") || !tt.HasText("已停止，已完成的准备会保留。") || s.OnboardingLogOpen || label != "下载并安装 · 10.0 MiB" {
		t.Fatalf("Stop entered failure state: %v", tt.Texts())
	}
}

func TestOnboardingTerminalWorkerStopsPolling(t *testing.T) {
	config, _ := beta3SetupRuntimeConfig(t)
	a, _ := environmentFixture()
	a.Config, a.Client.Config, a.initialSetup = config, config, true
	t.Setenv("MEMLANE_BOOTSTRAP_ROOT", "relative-component-root")
	a.environment.InitialStep = 1
	a.onboarding.watchWorker = true
	a.InspectInstallWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
		return bootstrap.InstallWorkerState{State: "failed"}, false, nil
	}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.inspectInstallWorkerAsync()
	drainReading(t, updates, func() bool { return !a.environment.workerInspecting })
	if a.onboarding.watchWorker || !strings.Contains(a.environment.onboardingLogText(), "命令行安装失败") {
		t.Fatal("terminal CLI worker still schedules a poll or lost the result")
	}
	drainReading(t, updates, func() bool { return !a.environment.Loading })
	a.onboarding.watchWorker = true
	a.inspectInstallWorkerAsync()
	drainReading(t, updates, func() bool { return !a.environment.workerInspecting })
	if !a.onboarding.watchWorker {
		t.Fatal("an old terminal receipt stopped a new takeover watch")
	}
}

func TestEnvironmentDiagnosticPrefersNewSettingsLog(t *testing.T) {
	a, _ := environmentFixture()
	s := &a.environment
	a.initialSetup = true
	s.OnboardingLog = makeOnboardingLogLines("旧向导日志", false)
	s.Journal = bootstrap.Journal{Operation: "settings-install", ActiveStep: "python", Steps: map[string]bootstrap.Step{"python": {Log: "新设置日志"}}}
	if got := a.environmentDiagnosticInput().InstallerLog; !strings.Contains(got, "旧向导日志") {
		t.Fatalf("first install log = %q", got)
	}
	a.initialSetup = false
	if got := a.environmentDiagnosticInput().InstallerLog; got != "新设置日志" {
		t.Fatalf("settings installation log = %q", got)
	}
}

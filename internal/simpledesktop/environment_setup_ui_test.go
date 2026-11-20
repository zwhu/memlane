package simpledesktop

import (
	"os"
	"path/filepath"
	"strings"
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
	restored, err := a.environmentOptions()
	if err != nil || restored.Python != "/manual/python" || a.environment.ModelCache != "/manual/cache" {
		t.Fatal(restored, err)
	}
	b := New(cfg, nil)
	restored, err = b.environmentOptions()
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
	return ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() { ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, true) }) })
	}, 1200, 820)
}

func TestEnvironmentInstallRequiresReviewWithConcreteScope(t *testing.T) {
	a, _ := environmentFixture()
	tt := environmentTester(a)
	if tt.HasText("确认安装") || tt.HasText("准备完成") {
		t.Fatal("unreviewed missing environment looked complete")
	}
	clickProjectUI(t, tt, "安装缺少的依赖")
	for _, label := range []string{"确认安装缺少的依赖", "运行本地记忆引擎", "下载 10.0 MiB，占用 30.0 MiB", "来源：https://official.example.test/pinned", "位置：/synthetic/components/python", "确认安装", "暂不安装"} {
		if !tt.HasText(label) {
			t.Fatal("review omitted", label)
		}
	}
	if !a.environment.Review || a.environment.Busy {
		t.Fatal("opening review started installation")
	}
}
func TestAvailableEnvironmentContinuesWithoutInstallAction(t *testing.T) {
	a, _ := environmentFixture()
	a.environment.Plan.Items[0].Observation.Status = bootstrap.Available
	tt := environmentTester(a)
	if !tt.HasText("可用") || !tt.HasText("已就绪") || !tt.HasText("继续") || tt.HasText("安装缺少的依赖") || tt.HasText("准备完成") {
		t.Fatal("compatible environment requires duplicate installation")
	}
}
func TestInstallationFailureCanCopyDiagnosticsWithoutRuntime(t *testing.T) {
	a, item := environmentFixture()
	a.environment.Error = "download failed API_KEY=private-secret"
	a.environment.Journal = bootstrap.Journal{Operation: "first-install", Plan: a.environment.Plan, Status: bootstrap.Failed, Steps: map[string]bootstrap.Step{item.Requirement.ID: {Status: bootstrap.Failed, Attempts: 1, Error: a.environment.Error}}}
	tt := environmentTester(a)
	for _, label := range []string{"失败", "重试", "用 Agent 排查", "查看详情"} {
		if !tt.HasText(label) {
			t.Fatal("failure action missing", label)
		}
	}
	clickProjectUI(t, tt, "用 Agent 排查")
	if !strings.Contains(tt.Clipboard(), "你的文字回复不能") || strings.Contains(tt.Clipboard(), "private-secret") {
		t.Fatal("copied incomplete or unredacted diagnostics")
	}
	if a.environment.Environment != nil || a.environment.Busy {
		t.Fatal("diagnostics depended on runtime setup")
	}
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

func TestFirstWizardReusesConnectionsAndGenerationWithoutPrematureCompletion(t *testing.T) {
	app, _ := environmentFixture()
	app.initialSetup = true
	app.environment.InitialStep = 2
	app.environment.RuntimeStarted = true
	app.integrations.Loaded = true
	tester := ui.NewTester(app.View, 1200, 820)
	if !tester.HasText("Agent 连接") || tester.HasText("运行环境") {
		t.Fatal("wizard did not advance to shared Agent component")
	}
	clickProjectUI(t, tester, "继续")
	if app.environment.InitialStep != 3 || !tester.HasText("启用并完成") {
		t.Fatal("generation setup missing")
	}
	clickProjectUI(t, tester, "启用并完成")
	if !app.initialSetup || app.environment.Busy {
		t.Fatal("unvalidated generation completed initialization")
	}
	if tester.HasText("工具图谱") {
		t.Fatal("unrelated global graph settings entered initialization")
	}
}

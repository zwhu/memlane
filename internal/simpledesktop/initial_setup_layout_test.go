package simpledesktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func setupLayoutFixture() *App {
	a, item := environmentFixture()
	a.initialSetup = true
	a.environment.Plan.Items = nil
	for _, d := range []struct{ id, name, version string }{{"uv", "uv", "0.12.3"}, {"python", "Python", "3.12.12"}, {"runtime", "运行库", "LanceDB 0.39.0 · FastEmbed 0.9.1"}, {"cognee-source", "Cognee", "ec6f408"}, {"embedding", "向量模型", "fixed"}, {"agent-plugin", "Agent 插件", "1.8.0 · a176248"}} {
		row := item
		row.Requirement.ID = d.id
		row.Requirement.Name = d.name
		row.Requirement.Version = d.version
		row.Requirement.Source = "https://official.example.test/" + strings.Repeat("fixed-source/", 12) + d.id
		a.environment.Plan.Items = append(a.environment.Plan.Items, row)
	}
	return a
}

func TestFirstUsePlanKeepsApprovalVisibleAndDoesNotMoveBackground(t *testing.T) {
	a := setupLayoutFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	before, ok := tt.Find("准备好你的本地记忆环境")
	if !ok {
		t.Fatal("first-use title missing")
	}
	fixtureImage(t, tt, "first-use-a-environment")
	clickProjectUI(t, tt, "安装缺少的依赖")
	footer, ok := tt.Find("暂不安装")
	if !ok || footer.Y < 0 || footer.Y+footer.H > 820 {
		t.Fatal("approval footer escaped the window", footer)
	}
	if a.environment.Busy {
		t.Fatal("review started installation")
	}
	fixtureImage(t, tt, "first-use-a-plan")
	first, _ := tt.Find("uv · 0.12.3")
	tt.Scroll(600, 400, 0, 500)
	afterScroll, _ := tt.Find("uv · 0.12.3")
	fixed, _ := tt.Find("暂不安装")
	if (afterScroll.H > 0 && afterScroll.Y >= first.Y) || fixed != footer {
		t.Fatal("plan did not scroll independently of approval actions", first, afterScroll, footer, fixed)
	}
	clickProjectUI(t, tt, "暂不安装")
	after, ok := tt.Find("准备好你的本地记忆环境")
	if !ok || before != after || a.environment.Busy || a.environment.Review {
		t.Fatal("declining plan moved the page or started work")
	}
}

func TestFirstUseStepsPreserveRuntimeGateAndSharedShell(t *testing.T) {
	a := setupLayoutFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "连接 Agent")
	clickProjectUI(t, tt, "配置记忆处理")
	if a.environment.InitialStep > 1 {
		t.Fatal("missing runtime allowed future steps")
	}
	a.environment.RuntimeStarted = true
	a.integrations.Loaded = true
	a.integrations.Connections.Connections = []AgentConnection{
		{ID: "codex", Kind: "codex", Detected: true},
		{ID: "pi", Kind: "pi", Detected: true},
		{ID: "traex", Kind: "traex", Detected: true},
	}
	a.integrations.Generation.Profiles = map[string][]GenerationRow{}
	tt.Frame()
	clickProjectUI(t, tt, "连接 Agent")
	fixtureImage(t, tt, "first-use-a-agents")
	clickProjectUI(t, tt, "配置记忆处理")
	if a.environment.InitialStep != 3 || !tt.HasText("选择如何整理记忆") {
		t.Fatal("model configuration bypassed validation/activation")
	}
	clickProjectUI(t, tt, "启用并完成")
	if a.environment.Busy || a.environment.RuntimeStage != "" {
		t.Fatal("unvalidated configuration started activation")
	}
	fixtureImage(t, tt, "first-use-a-processing")
	clickProjectUI(t, tt, "准备环境")
	if !tt.HasText("准备好你的本地记忆环境") || !tt.HasText("运行基础") || !tt.HasText("本地记忆引擎") || !tt.HasText("Agent 接入") {
		t.Fatal("return lost environment grouping")
	}
}

func TestSettingsEnvironmentUsesTheSameBoundedApprovalModal(t *testing.T) {
	a := setupLayoutFixture()
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() { ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) }) })
	}, 1200, 820)
	clickProjectUI(t, tt, "安装缺少的依赖")
	if !tt.HasText("确认安装缺少的依赖") || a.environment.Busy {
		t.Fatal("settings approval diverged from first-use")
	}
	r, ok := tt.Find("确认安装")
	if !ok || r.Y+r.H > 820 {
		t.Fatal("settings approval not bounded", r)
	}
}

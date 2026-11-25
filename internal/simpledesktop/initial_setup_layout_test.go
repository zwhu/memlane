package simpledesktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func setupLayoutFixture() *App {
	a, item := environmentFixture()
	a.initialSetup = true
	a.environment.InitialStep = 2
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

func TestFocusedOnboardingStageMappingAndKeyboard(t *testing.T) {
	a := setupLayoutFixture()
	s := &a.environment
	s.InitialStep, s.HostsLoaded = 1, true
	tt := ui.NewTester(a.View, 1200, 820)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if a.onboardingPage() != onboardingWelcome {
		t.Fatal("fresh startup must show welcome")
	}
	tt.Key(0, ui.KeyEnter)
	if a.onboardingPage() != onboardingAgents || s.InitialStep != 1 {
		t.Fatal("welcome Enter didn't enter step 1")
	}
	tt.Key(0, ui.KeyEnter)
	if a.onboardingPage() != onboardingEnv || s.InitialStep != 2 {
		t.Fatal("step 1 Enter didn't enter step 2")
	}
	a.navigateOnboarding(onboardingConnect)
	if a.onboardingPage() != onboardingEnv {
		t.Fatal("connection allowed before runtime start")
	}
	before, ok := tt.Find("准备本地记忆环境")
	if !ok || before.X != 300 || before.Y < 92 || before.Y > 115 {
		t.Fatalf("environment content alignment: %+v, found=%v", before, ok)
	}
	footer, ok := tt.Find("上一步")
	if !ok || footer.Y < 756 || footer.Y+footer.H > 820 {
		t.Fatalf("footer alignment: %+v, found=%v", footer, ok)
	}
	clickProjectUI(t, tt, "遇到问题")
	clickProjectUI(t, tt, "查看环境详情")
	if !s.Details || !tt.HasText("已检测组件路径") {
		t.Fatal("help menu did not open environment details")
	}
	tt.Key(0, ui.KeyEnter)
	if s.Busy || a.onboardingPage() != onboardingEnv {
		t.Fatal("modal Enter escaped into wizard")
	}
	tt.Key(0, ui.KeyEscape)
	if s.Details {
		t.Fatal("Escape did not close environment details")
	}
	s.RuntimeStarted = true
	a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
	a.navigateOnboarding(onboardingConnect)
	tt.Frame()
	if s.InitialStep != 3 || !tt.HasText("暂不接入也可以") {
		t.Fatalf("zero-agent connect screen missing: %v", tt.Texts())
	}
	a.showOnboardingProcess()
	tt.Frame()
	if s.InitialStep != 3 || !tt.HasText("验证并启用") || tt.HasText("本地语义搜索模型") {
		t.Fatal("processing did not use its own page over persisted step 3")
	}
	clickProjectUI(t, tt, "上一步")
	if a.onboardingPage() != onboardingConnect {
		t.Fatal("back did not return to connect")
	}
}

func TestFocusedOnboardingDoneMotionAndSummary(t *testing.T) {
	for _, options := range []struct {
		name         string
		dark, reduce bool
	}{{"light", false, false}, {"dark-reduced", true, true}} {
		t.Run(options.name, func(t *testing.T) {
			a := setupLayoutFixture()
			a.environment.RuntimeStarted = true
			a.environment.SelectedAgents = []string{"pi"}
			a.integrations.Connections.Connections = []AgentConnection{{ID: "pi", Kind: "pi", Status: "configured_pending_new_session"}}
			a.showOnboardingDone("pi · tested-model")
			show := false
			tt := ui.NewTester(func(c *ui.Context) {
				if show {
					a.View(c)
				}
			}, 1200, 820)
			tt.SetDark(options.dark)
			tt.SetPreferences(ui.Preferences{ReduceMotion: options.reduce})
			show = true
			tt.Frame()
			if options.reduce && tt.FramePending() {
				t.Fatal("done animation requested another frame under ReduceMotion")
			}
			tt.Advance(time.Second)
			for _, title := range []string{"Memlane 已就绪", "本地记忆服务", "Agent 接入", "pi · tested-model", "1 个待新会话", "下一步：重启已接入的 Agent"} {
				if !tt.HasText(title) {
					t.Fatalf("done missing %q: %v", title, tt.Texts())
				}
			}
			if tt.HasText("遇到问题") || tt.HasText("上一步") || tt.FramePending() {
				t.Fatal("done retained navigation or animation after settling")
			}
			fixtureImage(t, tt, "focused-done-"+options.name)
			tt.Key(0, ui.KeyEnter)
			if a.initialSetup || a.page != "总览" {
				t.Fatal("done Enter did not open overview")
			}
		})
	}
}

func TestSettingsEnvironmentUsesTheSameBoundedApprovalModal(t *testing.T) {
	a := setupLayoutFixture()
	a.initialSetup = false
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

func TestSettingsEnvironmentShowsLadybugInRuntimeSummary(t *testing.T) {
	darkBefore := appliedDark != nil && *appliedDark
	t.Cleanup(func() { applyAppearance(darkBefore) })
	a := setupLayoutFixture()
	a.environment.Environment = &bootstrap.Environment{
		Runtime: bootstrap.RuntimeDriver{Lock: []byte("lancedb==0.39.0\nladybug==0.19.0\nfastembed==0.9.1\n")},
	}
	const summary = "LanceDB 0.39.0 · Ladybug 0.19.0 · FastEmbed 0.9.1"
	for _, appearance := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(appearance.name, func(t *testing.T) {
			applyAppearance(appearance.dark)
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Themed(c, homeTheme).Fill().Children(func() {
					ui.Scroll(c).Fill().Children(func() { a.environmentSetup(c, false) })
				})
			}, 1200, 820)
			tt.SetDark(appearance.dark)
			tt.Frame()
			if !tt.HasText(summary) {
				t.Fatalf("runtime summary missing %q", summary)
			}
			fixtureImage(t, tt, "environment-runtime-"+appearance.name)
		})
	}
}

func TestOnboardingMascotPausesWhenWindowLosesFocus(t *testing.T) {
	a := setupLayoutFixture()
	a.environment.InitialStep, a.environment.HostsLoaded = 1, true
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Advance(time.Second)
	if !tt.FramePending() {
		t.Fatal("focused welcome mascot is not animating")
	}
	a.windowInactive = true
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("unfocused welcome mascot still requests animation frames")
	}
	a.windowInactive = false
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("welcome mascot did not resume after focus returned")
	}
}

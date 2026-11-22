package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
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
	clickProjectUI(t, tt, "配置记忆提炼")
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
	clickProjectUI(t, tt, "配置记忆提炼")
	if a.environment.InitialStep != 3 || !tt.HasText("选择如何提炼记忆") {
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

func TestFirstUseValidatesSavedPiBeforeEnablingRuntime(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	ref := GenerationRef{ID: "pi-setup", Revision: 2}
	directory := GenerationDirectory{Revision: 5, Next: &ref, Profiles: map[string][]GenerationRow{
		ref.ID: {{Revision: ref.Revision, Digest: "pi-digest", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "pi", BridgeProvider: "deepseek", Model: "selected-model"}}},
	}}
	validated := make(chan GenerationRef, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/generation/validations":
			var body struct {
				Revision int64         `json:"expected_revision"`
				Profile  GenerationRef `json:"profile"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Revision != directory.Revision {
				t.Error("validation did not use the reviewed generation revision")
			}
			validated <- body.Profile
			w.Write([]byte(`{}`))
		case r.URL.Path == "/v1/generation":
			json.NewEncoder(w).Encode(directory)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := setupLayoutFixture()
	a.Config.GenerationFile = filepath.Join(root, "generation.json")
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	a.Update = func(func()) {}
	a.environment.RuntimeStarted = true
	a.environment.InitialStep = 3
	a.integrations.Loaded = true
	a.integrations.Connections.Connections = []AgentConnection{{Kind: "pi", Status: "configured_pending_new_session"}}
	a.integrations.Generation = directory
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "启用并完成")
	if a.environment.Busy {
		t.Fatal("activation started without a validation receipt")
	}
	clickProjectUI(t, tt, "验证（会消耗额度）")
	select {
	case actual := <-validated:
		if actual != ref {
			t.Fatalf("validated profile %+v, want %+v", actual, ref)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("saved Pi candidate did not submit validation")
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

func TestFirstUseV4StageBannerDetailsDialogAndNoBridgeWording(t *testing.T) {
	a := setupLayoutFixture()
	a.environment.Busy = true
	a.environment.RuntimeStage = ""
	cancelled := false
	a.environment.Cancel = func() { cancelled = true }

	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("正在准备依赖组件…") || !tt.HasText("停止安装") {
		t.Fatalf("stage banner missing while busy: %v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "补全桥") {
			t.Fatalf("unexpected deprecated wording %q", txt)
		}
	}
	clickProjectUI(t, tt, "停止安装")
	if !cancelled {
		t.Fatal("stop install button did not invoke cancel")
	}

	// Open environment details v4Dialog and verify Esc dismisses it.
	a.environment.Busy = false
	tt.Frame()
	clickProjectUI(t, tt, "环境详情")
	if !a.environment.Details || !tt.HasText("已检测组件路径") || !tt.HasText("复制诊断材料") {
		t.Fatalf("environment details v4Dialog did not open: details=%v", a.environment.Details)
	}
	tt.Key(0, ui.KeyEscape)
	if a.environment.Details {
		t.Fatal("Escape did not dismiss environment details v4Dialog")
	}
}

func TestFirstUseSourceSwitchAndManualCommandCopy(t *testing.T) {
	a := setupLayoutFixture()
	a.environment.DownloadSource = "china"
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("国内镜像源") || !tt.HasText("国外官方源") || !tt.HasText("复制安装命令") {
		t.Fatalf("download source toggle or manual copy button missing: %v", tt.Texts())
	}

	clickProjectUI(t, tt, "国外官方源")
	if a.environment.DownloadSource != "official" {
		t.Fatalf("expected official download source after toggle, got %q", a.environment.DownloadSource)
	}
	if !tt.HasText("已切换到国外官方源") {
		t.Fatalf("expected feedback message after source toggle, got %q", a.environment.Message)
	}

	clickProjectUI(t, tt, "复制安装命令")
	if !tt.HasText("已复制终端安装命令，执行后请点击「重新检测」") {
		t.Fatalf("expected manual script copy message, got %q", a.environment.Message)
	}

	// Open approval modal with OfficialSource set and check per-item copy button & labels.
	a.environment.Plan.Items[0].Requirement.OfficialSource = "https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz"
	tt.Frame()
	clickProjectUI(t, tt, "安装缺少的依赖")
	if !tt.HasText("当前来源：") || !tt.HasText("官方原址：") || !tt.HasText("复制全部安装命令") || !tt.HasText("复制此项安装命令") {
		t.Fatalf("review modal missing mirror source labels or copy buttons: %v", tt.Texts())
	}
}

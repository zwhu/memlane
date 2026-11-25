package app

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
)

func TestBeta3SetupOnlyGraphTabDoesNotRequestGraph(t *testing.T) {
	token := p1TestTokenFile(t)
	a := New(Config{GenerationSetupOnly: true, GenerationMode: "unconfigured", ProjectControl: &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token},
		Projects: []Project{{ID: "demo", Dataset: "demo"}}}, nil)
	a.page, a.kind, a.project = "知识", "知识", 0
	var requests atomic.Int32
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected graph request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	a.Update = func(func()) {}
	a.graphController.AttachHost = func(*GraphHost) error {
		t.Error("graph surface was attached in setup-only mode")
		return nil
	}
	a.graphController.Error, a.graphController.Loading = "HTTP 503", true
	a.loadGraph()
	if a.graphController.Loading || a.graphController.Error != "" || a.graphController.AttachRequested {
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
	if a.page != "图谱" || a.graphController.Loading || a.graphController.AttachRequested || requests.Load() != 0 ||
		!tt.HasText("配置记忆提炼后可查看图谱") || tt.HasText("HTTP 503") {
		t.Fatalf("unexpected setup-only graph: %v", tt.Texts())
	}
	a.Update = nil
	clickProjectUI(t, tt, "前往配置")
	if a.page != "设置" || a.access.SettingsSection != "记忆提炼" {
		t.Fatal("graph CTA did not navigate to generation settings")
	}
}

func TestBeta3OwnerSwitchHostReverifiesReloadedGraphAssets(t *testing.T) {
	config := Config{ServiceURL: coldStartTestURL,
		Projects:  []Project{{ID: "project", CodeDataset: "code-dataset"}},
		TokenFile: p1TestTokenFile(t)}
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
	config.GraphAssets = assetsDir
	for _, scenario := range []string{"asset_error", "attach_error_same_dir", "attached_host_changed_dir"} {
		t.Run(scenario, func(t *testing.T) {
			previous := config
			if scenario != "attach_error_same_dir" {
				previous.GraphAssets = filepath.Join(t.TempDir(), "old-assets")
			}
			a := New(previous, nil)
			var oldHost *GraphHost
			a.graphController.Assets = map[string][]byte{"old": []byte("old")}
			a.graphController.AssetsChecked = true
			switch scenario {
			case "asset_error":
				a.graphController.AssetsErr = errors.New("old asset error")
				a.graphController.Error = a.graphController.AssetsErr.Error()
			case "attach_error_same_dir":
				a.graphController.AttachErr = errors.New("old attach error")
				a.graphController.Error = a.graphController.AttachErr.Error()
				a.graphController.AttachRequested = true
			case "attached_host_changed_dir":
				a.syncReadingIdentity()
				oldHost = &GraphHost{}
				a.graphController.Host = oldHost
				a.graphController.Error = "old host render error"
				a.graphController.Selection = "old node"
				a.page, a.project, a.graphController.Mode = "图谱", 0, "代码结构"
			}
			attachRequested := scenario != "asset_error"
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/visualize/json" && r.URL.Query().Get("dataset_id") == "code-dataset" {
					w.Write([]byte(`{"nodes":[{"id":"reloaded"}],"links":[],"color_maps":{}}`))
					return
				}
				if r.URL.Path == "/api/graph" {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte("{}"))
			})}
			a.graphController.AttachHost = func(host *GraphHost) error {
				if !attachRequested {
					t.Error("graph attached before it was requested")
				}
				t.Cleanup(host.Close)
				return nil
			}
			host := a.settingsHost()
			host.ConfigChanged(config)
			host.RuntimeConfigLoaded(config)
			host.ReloadGraphAssets(config.GraphAssets)
			if a.graphController.AssetsDir != assetsDir || a.graphController.AssetsChecked ||
				a.graphController.Assets != nil || a.graphController.AssetsErr != nil || a.graphController.AttachErr != nil ||
				a.graphController.Error != "" || a.graphController.Selection != "" || a.graphController.AssetsCheck == nil || a.graphController.Host != nil ||
				(oldHost != nil && (!oldHost.IsClosed() || !a.graphController.Loading)) {
				t.Fatalf("restart did not reset graph host and schedule verification for %q: %q", assetsDir, a.graphController.Error)
			}
			drainReading(t, updates, func() bool {
				return a.graphController.AssetsChecked && !a.graphController.Loading
			})
			if a.graphController.AssetsErr != nil || len(a.graphController.Assets["app.js"]) == 0 || a.graphController.AssetsCheck != nil ||
				(a.graphController.Host != nil) != attachRequested {
				t.Fatalf("reloaded graph assets were not verified and attached as requested: %v", a.graphController.AssetsErr)
			}
			if oldHost != nil && (a.graphController.Host == oldHost || a.graphController.Host.Node("reloaded") == nil || a.graphController.Error != "") {
				t.Fatalf("active graph did not load data on the replacement host: %q", a.graphController.Error)
			}
		})
	}
}

func TestProjectEmptyBackgroundAndDistinctRecordFilter(t *testing.T) {
	a := projectUIFixture()
	a.state().Selected = nil
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("项目内容") || tt.HasText("选择一项内容开始阅读") || tt.HasText("返回总览") {
		t.Fatal("redundant project labels remain")
	}
	clickProjectUI(t, tt, "原始记录")
	clickProjectUI(t, tt, "会话")
	if a.kind != "会话" {
		t.Fatal("record filter not wired")
	}
	fixtureImage(t, tt, "project-1200x820-empty-background")
}

func TestSettingsServiceDesktopRuntimeSyncWithTray(t *testing.T) {
	a := settingsFixture()
	a.Config.ConfigFile = t.TempDir() + "/runtime.json"
	a.initDesktopPreferences()
	a.SetOpenAtLoginFn = func(enable bool) error { return fmt.Errorf("non-.app binary") }
	a.access.SettingsSection = "服务"
	a.navigate("设置", 0)

	mainTT := ui.NewTester(a.View, 1200, 820)
	if !mainTT.HasText("桌面端运行方式") || !mainTT.HasText("退出时保持后台运行") || !mainTT.HasText("登录时静默启动") {
		t.Fatalf("missing 桌面端运行方式 section in 设置 › 服务; texts=%v", mainTT.Texts())
	}

	if err := mainTT.Click("退出时保持后台运行开关"); err != nil {
		t.Fatalf("failed to click 退出时保持后台运行开关 in 设置 › 服务: %v", err)
	}
	if a.KeepAliveOnQuit() {
		t.Fatal("expected KeepAliveOnQuit=false after toggling in settings")
	}

	// Verify TrayFlyoutView reflects the updated KeepAliveOnQuit state.
	trayTT := ui.NewTester(a.TrayFlyoutView, 300, 480)
	if !trayTT.HasText("退出并停止服务") {
		t.Fatalf("tray flyout did not sync KeepAliveOnQuit=false from settings; texts=%v", trayTT.Texts())
	}

	// Toggling open-at-login in settings records honest error without faking enabled.
	if err := mainTT.Click("登录时静默启动开关"); err != nil {
		t.Fatalf("failed to click 登录时静默启动开关 in 设置 › 服务: %v", err)
	}
	if a.OpenAtLogin() {
		t.Fatal("OpenAtLogin must stay false when OS hook fails")
	}
	if !mainTT.HasText("non-.app binary") {
		t.Fatalf("expected inline LoginError in settings, got %v", mainTT.Texts())
	}
}

func TestSettingsProjectPermissionsCardsAndNavigation(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := settingsFixture()
			original := a.state()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			if tt.HasText("项目设置") {
				t.Fatal("duplicate titlebar 项目设置 button should be removed in favor of 管理 tab")
			}
			clickProjectUI(t, tt, "管理")
			if a.kind != "管理" || a.access.OpenProject != "p" {
				t.Fatal("project identity lost")
			}
			for _, label := range []string{"Agent 访问权限", "只读"} {
				if !tt.HasText(label) {
					t.Fatal("missing", label)
				}
			}
			if tt.HasText("会话 1") || tt.HasText("默认自动采集") {
				t.Fatal("unrelated settings mixed into current project")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-project", wh[0], wh[1]))
			for _, label := range []string{"保存后续会话记录", "自动整理记忆", "自动处理", "已生效", "继承全局默认", "继承默认", "保存此 Agent 的设置", "撤销未保存修改", "仅影响此 Agent"} {
				if tt.HasText(label) {
					t.Fatal("removed settings text remains", label)
				}
			}
			if tt.HasText("待发材料") || tt.HasText("Agent 权限") {
				t.Fatal("removed tab remains")
			}
			clickProjectUI(t, tt, "记忆")
			if a.state() != original || a.page != "知识" || a.kind != "知识" {
				t.Fatal("return lost reading pane")
			}
			clickProjectUI(t, tt, "管理")
			clickProjectUI(t, tt, "设置")
			if a.settingsOrigin != nil || a.access.OpenProject != "" || !tt.HasText("Agent 与模型") {
				t.Fatal("sidebar settings still project scoped")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-global", wh[0], wh[1]))
			for _, text := range tt.Texts() {
				if text == "默认规则" || text == "高级" || text == "后台运行" || text == "配置生效" {
					t.Fatal("removed global policy tab remains")
				}
			}
			a.access.SettingsSection = "高级"
			tt.Frame()
			if a.access.SettingsSection != "记忆提炼" || !tt.HasText("记忆语言") {
				t.Fatal("retired tab left the settings page blank after reload")
			}
			clickProjectUI(t, tt, "记忆提炼")
			if !tt.HasText("记忆语言") || tt.HasText("高级") {
				t.Fatal("retained settings tab lost its content")
			}

		})
	}
}

func TestProjectHeaderSettingsTargetsCurrentProjectAndReturnsToReading(t *testing.T) {
	a := settingsFixture()
	a.navigate("知识", 1)
	original := a.state()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "管理")
	if a.access.OpenProject != "same" || a.project != 1 || a.kind != "管理" {
		t.Fatal("management tab lost current project")
	}
	clickProjectUI(t, tt, "记忆")
	if a.project != 1 || a.state() != original || a.kind != "知识" {
		t.Fatal("management tab lost project reading state")
	}
}

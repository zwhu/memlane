package simpledesktop

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func TestExternalStoreBannerAndCopyAcrossAppearances(t *testing.T) {
	for _, theme := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(theme.name, func(t *testing.T) {
			cfg := Config{ConfigFile: "/synthetic/O'Brien runtime.json", ServiceURL: "http://127.0.0.1:1", Python: "/synthetic path/python"}
			a := New(cfg, nil)
			a.monitor.State = "degraded"
			tt := ui.NewTester(a.View, 1200, 820)
			tt.SetDark(theme.dark)
			tt.Frame()
			for _, label := range []string{"外部托管记忆库服务尚未运行", "缺少 first_install 托管标记",
				"不会自动启动或接管", "scripts/dev.py start --no-ui", "scripts/dev.py recover",
				"重新检查连接", "复制启动命令", "复制恢复命令"} {
				if !tt.HasText(label) {
					t.Fatalf("missing %q: %v", label, tt.Texts())
				}
			}
			for _, copyAction := range []struct{ action, command string }{
				{"启动", "'/synthetic path/python' scripts/dev.py start --no-ui --config '/synthetic/O'\"'\"'Brien runtime.json'"},
				{"恢复", "'/synthetic path/python' scripts/dev.py recover --config '/synthetic/O'\"'\"'Brien runtime.json'"},
			} {
				clickProjectUI(t, tt, "复制"+copyAction.action+"命令")
				if tt.Clipboard() != copyAction.command || !tt.HasText("已复制"+copyAction.action+"命令") {
					t.Fatalf("unexpected command or feedback: %q", tt.Clipboard())
				}
			}
		})
	}
}

func TestExternalStoreBannerRequiresObservedFailureAndUnreachableServices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*App)
		want   bool
	}{
		{"before probe", func(a *App) {}, false},
		{"directory failure", func(a *App) {
			a.Config.ProjectControl = &ProjectControlConfig{}
			a.access.DirectoryError = "unreachable"
		}, true},
		{"exit", func(a *App) { a.monitor.State = "exited" }, true},
		{"connected", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.service = "已连接" }, false},
		{"ready monitor", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.monitor.State = "ready" }, false},
		{"reachable control", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.monitor.ControlReachable = true }, false},
		{"managed", func(a *App) { a.monitor.State = "exited"; a.Config.FirstInstall = &bootstrap.FirstInstallation{} }, false},
		{"setup", func(a *App) { a.monitor.State = "exited"; a.initialSetup = true }, false},
		{"no file", func(a *App) { a.monitor.State = "exited"; a.Config.ConfigFile = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(Config{ConfigFile: "/synthetic/runtime.json", ServiceURL: "http://127.0.0.1:1"}, nil)
			tc.change(a)
			tt := ui.NewTester(func(c *ui.Context) { a.externalStoreStoppedBanner(c) }, 1200, 350)
			if a.isExternalManagedStoreStopped() != tc.want || tt.HasText("外部托管记忆库服务尚未运行") != tc.want {
				t.Fatalf("unexpected stopped store state: %v", tt.Texts())
			}
		})
	}
}

func TestExternalStoreRecheckOnlyReadsAndRestartsPolling(t *testing.T) {
	root := t.TempDir()
	configFile, tokenFile := filepath.Join(root, "runtime.json"), filepath.Join(root, "manager")
	original := []byte(`{"legacy":true}`)
	if err := os.WriteFile(configFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 10)
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
	})
	a := New(Config{ConfigFile: configFile, ServiceURL: "http://127.0.0.1:18777", OfficialData: root,
		ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:18777", ManagementTokenFile: tokenFile}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.monitor.ConsecutiveFailures = 2
	a.monitor.PollEnabled = true
	a.externalStoreNotice = "已复制启动命令"
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if a.monitor.PollEnabled || a.access.Request != 0 || a.monitor.Request != 0 {
		t.Fatal("stopped store retriggered background probes")
	}
	clickProjectUI(t, tt, "重新检查连接")
	if !a.monitor.PollEnabled || a.monitor.Request != 1 || a.access.Request != 1 || a.externalStoreNotice != "" {
		t.Fatal("explicit recheck did not start both checks")
	}
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !seen["/health"] || !seen["/v1/projects"] {
		select {
		case path := <-observed:
			seen[path] = true
		case <-deadline:
			t.Fatalf("missing recheck requests: %v", seen)
		}
	}
	for a.monitor.Polling || a.access.Loading {
		select {
		case update := <-updates:
			update()
		case <-deadline:
			t.Fatal("recheck requests did not complete")
		}
	}
	if a.isExternalManagedStoreStopped() || !a.monitor.PollEnabled {
		t.Fatal("successful recheck did not clear the stopped state")
	}
	if data, err := os.ReadFile(configFile); err != nil || string(data) != string(original) {
		t.Fatalf("recheck modified runtime configuration: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "store-identity.json")); !os.IsNotExist(err) {
		t.Fatalf("recheck created a store identity: %v", err)
	}
}

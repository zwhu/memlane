package app

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
				"不会自动启动或接管", "sh scripts/go.sh run ./cmd/memlane dev start --no-ui",
				"sh scripts/go.sh run ./cmd/memlane dev recover",
				"重新检查连接", "复制启动命令", "复制恢复命令"} {
				if !tt.HasText(label) {
					t.Fatalf("missing %q: %v", label, tt.Texts())
				}
			}
			if tt.HasText("scripts/dev.py") {
				t.Fatalf("retired scripts/dev.py must not appear in banner: %v", tt.Texts())
			}
			for _, copyAction := range []struct{ action, command string }{
				{"启动", "sh scripts/go.sh run ./cmd/memlane dev start --no-ui --config '/synthetic/O'\"'\"'Brien runtime.json'"},
				{"恢复", "sh scripts/go.sh run ./cmd/memlane dev recover --config '/synthetic/O'\"'\"'Brien runtime.json'"},
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
		{"directory auth error alone", func(a *App) {
			a.Config.ProjectControl = &ProjectControlConfig{}
			a.access.DirectoryError = "管理接口鉴权失败（401）"
		}, false},
		{"exit", func(a *App) { a.monitor.State = "exited" }, true},
		{"starting engine", func(a *App) { a.monitor.State = "starting"; a.monitor.EngineStatus = "启动中" }, false},
		{"connected", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.monitor.EngineStatus = "已连接" }, false},
		{"ready monitor", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.monitor.State = "ready" }, false},
		{"reachable control", func(a *App) { a.monitor.ConsecutiveFailures = 1; a.monitor.ControlReachable = true }, false},
		{"reachable control with exited engine", func(a *App) {
			a.monitor.State = "exited"
			a.monitor.ControlChecked = true
			a.monitor.ControlReachable = true
		}, false},
		{"managed", func(a *App) { a.monitor.State = "exited"; a.Config.FirstInstall = &bootstrap.FirstInstallation{} }, false},
		{"setup", func(a *App) { a.monitor.State = "exited"; a.settingsController.SetInitialSetupForHost(true) }, false},
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
	a.monitor.NextPollAt = time.Now().Add(time.Hour)
	a.externalStoreNotice = "已复制启动命令"
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !a.monitor.PollEnabled {
		t.Fatal("stopped external store must never disable service polling")
	}
	if a.access.Request != 0 || a.monitor.Request != 0 {
		t.Fatal("stopped store retriggered premature probes before NextPollAt")
	}
	clickProjectUI(t, tt, "重新检查连接")
	if !a.monitor.PollEnabled || a.monitor.Request != 1 || a.access.Request != 1 || a.externalStoreNotice != "" {
		t.Fatal("explicit recheck did not start both checks")
	}
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !seen["/v1/runtime/monitor"] || !seen["/v1/projects"] {
		select {
		case path := <-observed:
			seen[path] = true
		case <-deadline:
			t.Fatalf("missing recheck requests: %v", seen)
		}
	}
	if seen["/health"] {
		t.Fatalf("gateway-configured monitor must not probe direct /health: %v", seen)
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

func TestExternalStoreNeverDisablesPollingOnFailure(t *testing.T) {
	a := New(Config{ConfigFile: "/synthetic/runtime.json", ServiceURL: "http://127.0.0.1:1"}, nil)
	a.monitor.PollEnabled = true
	a.monitor.NextPollAt = time.Now().Add(time.Hour)
	a.monitor.ConsecutiveFailures = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !a.monitor.PollEnabled || a.externalStorePollPaused {
		t.Fatal("expected service polling to remain enabled even when external store is stopped")
	}
	a.monitor.EngineStatus = "已连接"
	a.monitor.State = "ready"
	a.monitor.ConsecutiveFailures = 0
	tt.Frame()
	if !a.monitor.PollEnabled || a.externalStorePollPaused || a.isExternalManagedStoreStopped() {
		t.Fatal("service did not recover cleanly with polling active")
	}
}

func TestExternalStoreBannerSuppressesAuthAndReceiptErrors(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T) (tokenFile string, transport http.RoundTripper)
		wantKind  string
		wantLevel string
	}{
		{
			name: "401 expired management token",
			setup: func(t *testing.T) (string, http.RoundTripper) {
				tok := filepath.Join(t.TempDir(), "expired.token")
				if err := os.WriteFile(tok, []byte(strings.Repeat("x", 40)), 0600); err != nil {
					t.Fatal(err)
				}
				return tok, queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
				})
			},
			wantKind:  "auth",
			wantLevel: "danger",
		},
		{
			name: "missing management token file",
			setup: func(t *testing.T) (string, http.RoundTripper) {
				return filepath.Join(t.TempDir(), "missing.token"), queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"state":"ready"}`))
				})
			},
			wantKind:  "auth",
			wantLevel: "danger",
		},
		{
			name: "malformed json receipt",
			setup: func(t *testing.T) (string, http.RoundTripper) {
				tok := filepath.Join(t.TempDir(), "valid.token")
				if err := os.WriteFile(tok, []byte(strings.Repeat("y", 40)), 0600); err != nil {
					t.Fatal(err)
				}
				return tok, queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{not-valid-json`))
				})
			},
			wantKind:  "response",
			wantLevel: "danger",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, transport := tc.setup(t)
			a := New(Config{
				ConfigFile:     "/synthetic/runtime.json",
				ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61277", ManagementTokenFile: tok},
			}, nil)
			a.Client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.refreshServiceMonitor()
			drainReading(t, updates, func() bool { return !a.monitor.Polling })

			if a.monitor.ControlErrorKind != tc.wantKind {
				t.Fatalf("ControlErrorKind = %q, want %q (err=%q)", a.monitor.ControlErrorKind, tc.wantKind, a.monitor.ControlError)
			}
			if a.isExternalManagedStoreStopped() {
				t.Fatalf("auth/receipt error (%s) must not trigger external-store-stopped banner", tc.wantKind)
			}
			if level := a.trayStatusLevel(); level != tc.wantLevel {
				t.Fatalf("trayStatusLevel = %q, want %q", level, tc.wantLevel)
			}
			tt := ui.NewTester(func(c *ui.Context) { a.externalStoreStoppedBanner(c) }, 1200, 350)
			if tt.HasText("外部托管记忆库服务尚未运行") {
				t.Fatalf("unexpected stopped banner rendered for %s: %v", tc.name, tt.Texts())
			}
		})
	}
}

func TestExternalStoreBannerSuppressesDirectServiceHTTPAuthAndServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		wantKind string
	}{
		{"direct 401 unauthorized", http.StatusUnauthorized, "auth"},
		{"direct 500 server error", http.StatusInternalServerError, "response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(Config{
				ConfigFile: "/synthetic/runtime.json",
				ServiceURL: "http://127.0.0.1:61278",
			}, nil)
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("error"))
			})}
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.refreshServiceMonitor()
			drainReading(t, updates, func() bool { return !a.monitor.Polling })

			if a.monitor.HealthErrorKind != tc.wantKind {
				t.Fatalf("HealthErrorKind = %q, want %q", a.monitor.HealthErrorKind, tc.wantKind)
			}
			if a.isExternalManagedStoreStopped() {
				t.Fatalf("direct HTTP %d must not trigger external-store-stopped banner", tc.status)
			}
			tt := ui.NewTester(func(c *ui.Context) { a.externalStoreStoppedBanner(c) }, 1200, 350)
			if tt.HasText("外部托管记忆库服务尚未运行") {
				t.Fatalf("unexpected stopped banner rendered for %s: %v", tc.name, tt.Texts())
			}
		})
	}
}

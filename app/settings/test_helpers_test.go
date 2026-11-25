package settings

import (
	"fmt"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type testHost struct {
	*Controller
	page, kind, appearance       string
	project                      int
	authVersion                  uint64
	windowHidden, windowInactive bool
	keepAlive, login             bool
	SetOpenAtLoginFn             func(bool) error
}

var (
	testTokens    = kitui.LightTokens()
	testBrandMark = sync.OnceValue(func() *ui.Bitmap {
		brand, err := fs.ReadFile(runtimeAssetsForTests, "assets/branding/memlane-icon.png")
		if err != nil {
			panic(err)
		}
		mark, err := ui.DecodeBitmap(brand)
		if err != nil {
			panic(err)
		}
		return mark
	})
	homeTheme             = kitui.HomeTheme(testTokens)
	runtimeAssetsForTests = os.DirFS("../..")
)

func applyAppearance(dark bool) {
	testTokens = kitui.TokensFor(dark)
	homeTheme = kitui.HomeTheme(testTokens)
}

func newTestHost(config Config, configErr error) *testHost {
	h := &testHost{page: "总览", kind: "知识", keepAlive: true, appearance: "system"}
	assets, errSub := fs.Sub(runtimeAssetsForTests, "engine")
	if errSub != nil {
		panic(errSub)
	}
	h.Controller = New(config, configErr, Host{
		Router: h, Session: testFence{h}, Theme: h, BootstrapAssets: assets, RuntimeAssets: runtimeAssetsForTests, BrandMark: testBrandMark(),
		WindowHidden: func() bool { return h.windowHidden }, WindowInactive: func() bool { return h.windowInactive },
		InvalidateDataset: func(dataset string) { h.authVersion++; h.ClearProjectQueues(dataset) },
		Appearance:        func() string { return h.appearance }, SetAppearance: func(mode string) { h.appearance = mode },
		KeepAliveOnQuit: func() bool { return h.keepAlive }, SetKeepAliveOnQuit: func(value bool) { h.keepAlive = value },
		OpenAtLogin: func() bool { return h.login },
		SetOpenAtLogin: func(value bool) error {
			if h.SetOpenAtLoginFn != nil {
				if err := h.SetOpenAtLoginFn(value); err != nil {
					return err
				}
			}
			h.login = value
			return nil
		},
		OpenProjectManagement: func(id string) {
			h.NavigateProject(id)
			h.kind = "管理"
			h.access.OpenProject = id
		},
	})
	return h
}

func (h *testHost) Dispatch(fn func())              { fn() }
func (h *testHost) Epoch() uint64                   { return 0 }
func (h *testHost) AuthEpoch() uint64               { return h.authVersion }
func (h *testHost) ReadingIdentity() string         { return h.cachedBaseReadingIdentity() }
func (h *testHost) Tokens() *kitui.Tokens           { return testTokens }
func (h *testHost) ReduceMotion() bool              { return false }
func (h *testHost) NavigateOverview()               { h.navigate("总览", 0) }
func (h *testHost) NavigateCommon()                 { h.navigate("通用知识", 0) }
func (h *testHost) NavigateProfiles()               { h.navigate("用户画像", 0) }
func (h *testHost) OpenItem(shell.ItemTarget) error { return nil }
func (h *testHost) NavigateProject(id string) {
	for i, p := range h.config.Projects {
		if p.ID == id {
			h.navigate("知识", i)
			return
		}
	}
}
func (h *testHost) NavigateSettings(section string) {
	h.SetSection(section)
	h.navigate("设置", 0)
}
func (h *testHost) navigate(page string, index int) {
	h.page, h.project, h.kind = page, index, "知识"
	h.access.OpenProject = ""
}
func (h *testHost) effectiveDark(system bool) bool {
	switch h.appearance {
	case "light":
		return false
	case "dark":
		return true
	default:
		return system
	}
}

func (h *testHost) projectTabs(c *ui.Context) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Button(c, "记忆").OnClick(func() { h.kind = "知识"; h.access.OpenProject = "" })
		ui.Button(c, "管理").OnClick(func() { h.OpenProjectManagement(h.config.Projects[h.project].ID) })
		ui.Button(c, "设置").OnClick(func() { h.NavigateSettings("") })
	})
}

func (h *testHost) View(c *ui.Context) {
	applyAppearance(h.effectiveDark(c.Theme().Dark))
	if h.InitialSetup() {
		if c.Preferences().ReduceMotion {
			c.DisableAnimations()
		}
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { h.ViewOnboarding(c) })
		h.OnboardingShortcuts(c)
		return
	}
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() {
		switch {
		case h.config.GenerationSetupOnly && !h.setupDeferredMode:
			h.ViewFirstGeneration(c)
		case h.page == "设置" && h.access.OpenProject != "":
			h.ViewProject(c, h.access.OpenProject)
		case h.page == "设置":
			h.ViewGlobal(c)
		case h.page == "知识":
			h.projectTabs(c)
			if h.kind == "管理" {
				h.ViewProject(c, h.config.Projects[h.project].ID)
			}
		default:
			h.ViewRuntimeAlerts(c)
			h.ViewDeferredBanner(c)
		}
		h.ViewDeferredToast(c, h.page == "设置")
		h.ViewDialogs(c)
	})
}

func projectUIFixture() *testHost {
	h := newTestHost(Config{Projects: []Project{
		{ID: "p", Name: "memlane", Path: "/synthetic/memlane", Dataset: "ds"},
		{ID: "same", Name: "memlane", Path: "/synthetic/other/memlane", Dataset: "other"},
	}}, nil)
	h.dispatch = func(func()) {}
	h.access.Next = time.Now().Add(time.Hour)
	h.navigate("知识", 0)
	h.monitor.EngineStatus = "已连接"
	return h
}

type queueTestTransport http.HandlerFunc

func (handler queueTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	w := httptest.NewRecorder()
	handler(w, r)
	res := w.Result()
	res.Request = r
	return res, nil
}

func clickProjectUI(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if r, ok := tt.Find(label); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}

func drainReading(t *testing.T, updates chan func(), ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			t.Fatal("reading update deadline")
		}
	}
}

func fixtureImage(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MEMLANE_OVERVIEW_UI_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "fixture-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err = png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func nextStartupUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for UI update")
		return nil
	}
}

func p1TestTokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(p, []byte("synthetic-p1-management-token-0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readingControlConfig(t *testing.T, url string) *ProjectControlConfig {
	t.Helper()
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("0123456789012345678901234567890123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	return &ProjectControlConfig{URL: url, ManagementTokenFile: token}
}

const coldStartTestURL = "http://127.0.0.1:16142"

type testFence struct{ *testHost }

func (f testFence) InvalidateReading() { f.authVersion++ }

type testHTTP struct {
	URL    string
	client *http.Client
}

var testHTTPPort atomic.Int32

func newTestHTTP(handler http.HandlerFunc) testHTTP {
	return testHTTP{
		URL:    fmt.Sprintf("http://127.0.0.1:%d", 20000+testHTTPPort.Add(1)),
		client: &http.Client{Transport: queueTestTransport(handler)},
	}
}

package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const coldStartTestURL = "http://127.0.0.1:16142"

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

func TestColdStartGraphVerificationFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"protocol":1,"upstream":"8853378eae5366addb064e758e560a456146ae1a","files":{"app.js":"bad-hash"}}`
	if err := os.WriteFile(filepath.Join(dir, "assets.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{GraphAssets: dir}, nil)
	if !mygo.Protocol.IsHandled("memlanegraph") || a.graphController.Host != nil || a.graphController.Error != "" || a.graphController.AssetsChecked {
		t.Fatal("New must register the protocol and leave verification pending")
	}
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	a.graphController.AttachHost = func(*GraphHost) error {
		t.Fatal("attach ran with unverified assets")
		return nil
	}
	a.graphController.PendingLoad = &graphLoad{}
	a.graphController.Loading = true
	a.EnsureGraphAttached()
	if a.graphController.AssetsChecked {
		t.Fatal("verification published before the UI callback")
	}
	nextStartupUpdate(t, updates)()
	if !a.graphController.AssetsChecked || a.graphController.AssetsErr == nil || a.graphController.Error != "图组件校验失败" ||
		a.graphController.Host != nil || a.graphController.PendingLoad != nil || a.graphController.Loading {
		t.Fatalf("verification did not fail closed: %q", a.graphController.Error)
	}
	if err := a.AttachGraph(nil); err == nil {
		t.Fatal("synchronous attachment accepted failed verification")
	}
}

func TestColdStartGraphAttachesOnceAndAppliesWaitingHTTPResult(t *testing.T) {
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/graph":
			if r.URL.Query().Get("dataset_id") != "ds" {
				t.Errorf("unexpected graph query %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"ok":true,"dataset_id":"ds","nodes":[{"id":"n"}],"edges":[]}`))
		case "/v1/projects/p/tool-graph/preview", "/v1/generation", "/v1/connections":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected graph request %s", r.URL)
			http.NotFound(w, r)
		}
	})

	for _, scenario := range []string{"attach", "closed", "navigate", "reset", "invalidate", "sync attach"} {
		t.Run(scenario, func(t *testing.T) {
			a := New(Config{ProjectControl: readingControlConfig(t, coldStartTestURL), Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
			a.Client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			attaches := 0
			a.graphController.AttachHost = func(*GraphHost) error { attaches++; return nil }

			a.PrewarmGraph()
			check := a.graphController.AssetsCheck
			a.navigate("图谱", 0)
			if check == nil || a.graphController.AssetsCheck != check || attaches != 0 || a.graphController.Host != nil {
				t.Fatal("graph entry did not share the asynchronous verification")
			}
			publishAssets := nextStartupUpdate(t, updates)
			a.loadGraph()
			drainReading(t, updates, func() bool { return a.graphController.PendingLoad != nil })
			if a.graphController.PendingLoad == nil || !a.graphController.Loading || a.graphController.Host != nil {
				t.Fatal("HTTP result was lost before asset publication")
			}
			switch scenario {
			case "closed":
				a.graphController.WindowClosed = true
				a.cancelGraphLoad()
			case "navigate":
				a.navigate("设置", 0)
			case "reset":
				a.resetGraphView("代码结构")
			case "invalidate":
				a.InvalidateReading("ds")
			case "sync attach":
				if err := a.AttachGraph(nil); err != nil {
					t.Fatal(err)
				}
			}
			publishAssets()
			a.EnsureGraphAttached()
			a.PrewarmGraph()
			if !a.graphController.AssetsChecked || a.graphController.AssetsErr != nil || a.graphController.PendingLoad != nil || a.graphController.Loading {
				t.Fatal("graph did not settle")
			}
			if scenario == "closed" {
				if a.graphController.Host != nil || attaches != 0 {
					t.Fatal("closed window was attached")
				}
				return
			}
			if a.graphController.Host == nil || attaches != 1 || a.graphController.Host.Asset("index.html") == nil {
				t.Fatalf("expected one verified attach, got %d", attaches)
			}
			t.Cleanup(a.graphController.Host.Close)
			if scenario == "attach" || scenario == "sync attach" {
				if a.graphController.Host.Project() != "p" || a.graphController.Host.Dataset() != "ds" || a.graphController.Host.Node("n") == nil {
					t.Fatal("pending graph was not published")
				}
			} else if a.graphController.Host.HasFrame() {
				t.Fatal("superseded graph result was published")
			}
		})
	}
}

func TestColdStartDeferredTasksFollowBootstrap(t *testing.T) {
	origPostFrame := startupPostFirstFrameDelay
	startupPostFirstFrameDelay = 25 * time.Millisecond
	t.Cleanup(func() { startupPostFirstFrameDelay = origPostFrame })

	for _, scenario := range []string{"success", "fail closed", "fallback", "settings", "hidden at login"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "settings" || scenario == "fallback" || scenario == "hidden at login" {
				original := startupDeferredFallbackTimeout
				startupDeferredFallbackTimeout = 150 * time.Millisecond
				t.Cleanup(func() { startupDeferredFallbackTimeout = original })
			}
			bootstrapStarted, releaseBootstrap := make(chan struct{}, 2), make(chan struct{}, 2)
			requests := make(chan string, 64)
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects":
					requests <- r.URL.Path
					_ = json.NewEncoder(w).Encode(AccessDirectory{
						Revision: 1, Projects: []AccessProject{{ID: "p", Name: "Memlane", Dataset: "ds", Status: "verified"}},
					})
				case "/v1/capabilities":
					_ = json.NewEncoder(w).Encode(ControlCapabilities{APIVersions: []string{"v1"}, ReadingContract: readingContract})
				case "/v1/overview/bootstrap":
					bootstrapStarted <- struct{}{}
					select {
					case <-releaseBootstrap:
					case <-r.Context().Done():
						return
					}
					if scenario == "fail closed" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
						Generation: 1, TotalKnowledgeCount: 1,
						DayBuckets:  []v2OverviewDayBucket{{Day: time.Now().Format("2006-01-02"), Count: 1}},
						RecentItems: []v2KnowledgeItem{{DocID: "k", DatasetID: "ds", ProjectID: "p", Title: "首屏真实记忆"}},
					})
				case "/v1/generation", "/v1/connections", "/health", "/v1/runtime/monitor", "/v1/projects/p/message-summary":
					requests <- r.URL.Path
					_, _ = w.Write([]byte(`{"project_id":"p","unread_count":0}`))
				default:
					http.NotFound(w, r)
				}
			})
			defer close(releaseBootstrap)
			a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: &ProjectControlConfig{
				URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
			}}, nil)
			a.Client.HTTP = &http.Client{Transport: transport}
			defer a.cancelOverviewRequests()
			defer a.cancelAndResetPanes()
			updates := make(chan func(), 64)
			a.Update = func(fn func()) { updates <- fn }
			loginReads := atomic.Int32{}
			a.ReadOpenAtLoginFn = func() bool { loginReads.Add(1); return true }
			attaches := 0
			a.graphController.AttachHost = func(*GraphHost) error { attaches++; return nil }
			defer func() {
				if a.graphController.Host != nil {
					a.graphController.Host.Close()
				}
			}()

			hidden := scenario == "hidden at login"
			if hidden {
				a.SetWindowHidden(true)
			}
			a.Start()
			drainReading(t, updates, func() bool { return a.access.Loaded })
			tt := ui.NewTester(a.View, 1200, 820)
			if hidden {
				// No scope-triggered refresh may start from a hidden repaint.
				if len(bootstrapStarted) != 0 || a.overviewState.Loading {
					t.Fatal("hidden repaint started overview bootstrap")
				}
				nextStartupUpdate(t, updates)()
				drainReading(t, updates, func() bool { return a.settingsController.IntegrationState().Loaded && a.OpenAtLogin() })
				if a.monitor.Request != 0 || a.graphController.AssetsCheck != nil || a.graphController.Host != nil || a.overviewFirstScreenReady || a.monitor.PollEnabled {
					t.Fatal("hidden startup started visible monitor, prewarm, or marked first screen ready")
				}
				// First reveal must run overview bootstrap BEFORE releasing monitor/sidebar/prewarm.
				a.SetWindowHidden(false)
				select {
				case <-bootstrapStarted:
				case <-time.After(3 * time.Second):
					t.Fatal("first reveal did not start overview bootstrap")
				}
				if a.monitor.Polling || a.monitor.Request != 0 || a.graphController.AssetsCheck != nil || attaches != 0 || !a.coldStartOverviewPending() {
					t.Fatal("first reveal overlapped monitor/prewarm with initial overview bootstrap")
				}
				releaseBootstrap <- struct{}{}
				drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.monitor.PollEnabled })
				if attaches != 0 {
					t.Fatal("first reveal attached WebView before post-first-frame delay")
				}
				drainReading(t, updates, func() bool { return attaches == 1 })
				return
			}

			select {
			case <-bootstrapStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("first directory render did not start overview bootstrap")
			}
			if !a.coldStartOverviewPending() || a.settingsController.IntegrationState().Request != 0 || a.monitor.Request != 0 ||
				loginReads.Load() != 0 || a.graphController.AssetsCheck != nil || a.existingProjectQueueState(a.access.Directory.Projects[0]) != nil {
				t.Fatal("noncritical startup work began before bootstrap settled")
			}
			if request := <-requests; request != "/v1/projects" || len(requests) != 0 {
				t.Fatalf("premature request after directory: %s (queued: %d)", request, len(requests))
			}

			switch scenario {
			case "fallback":
				nextStartupUpdate(t, updates)()
				if !a.overviewState.Loading {
					t.Fatal("fallback case must still have bootstrap in flight")
				}
			case "settings":
				// Simulate the race where AfterFunc has enqueued a UI update
				// just before the user opens settings.
				staleFallback := nextStartupUpdate(t, updates)
				a.navigate("设置", 0)
				drainReading(t, updates, func() bool { return a.settingsController.IntegrationState().Loaded })
				staleFallback()
			default:
				releaseBootstrap <- struct{}{}
				nextStartupUpdate(t, updates)()
			}
			if !a.startupDeferred.done || a.startupDeferred.timer != nil || !a.overviewFirstScreenReady ||
				!a.monitor.PollEnabled || a.settingsController.IntegrationState().Request != 1 || a.monitor.Request != 1 {
				t.Fatal("settlement did not release deferred work exactly once")
			}
			if scenario == "success" {
				if attaches != 0 || loginReads.Load() != 0 {
					t.Fatalf("main-thread prewarm/login ran before post-first-frame delay: attaches=%d loginReads=%d", attaches, loginReads.Load())
				}
				if !a.overviewState.ColdStartMotion {
					t.Fatal("first payload did not arm cold-start motion")
				}
				assertColdStartHeatmapPainted(t, tt)
			} else if scenario == "fail closed" && (a.overviewState.Error == "" || a.overviewState.Loading) {
				t.Fatal("failed bootstrap did not settle in the error state")
			}

			want := map[string]bool{"/v1/generation": true, "/v1/connections": true, "/v1/runtime/monitor": true}
			if scenario != "settings" {
				want["/v1/projects/p/message-summary"] = true
			}
			deadline := time.After(3 * time.Second)
			for len(want) > 0 {
				select {
				case request := <-requests:
					if !want[request] {
						t.Fatalf("unexpected or duplicated request %s", request)
					}
					delete(want, request)
				case <-deadline:
					t.Fatalf("missing released requests: %v", want)
				}
			}
			drainReading(t, updates, func() bool { return a.OpenAtLogin() && attaches == 1 })
			if loginReads.Load() != 1 {
				t.Fatalf("unexpected login reads: %d", loginReads.Load())
			}
			if scenario == "success" {
				a.navigate("设置", 0)
				a.navigate("总览", 0)
				if a.overviewState.ColdStartMotion {
					t.Fatal("Overview return kept cold-start motion")
				}
			}
		})
	}
}

func assertColdStartHeatmapPainted(t *testing.T, tt *ui.Tester) {
	t.Helper()
	tt.Frame()
	canvas, ok := tt.Find("记忆分布热力图")
	if !ok || !tt.HasText("首屏真实记忆") {
		t.Fatal("real overview content is missing on the first data frame")
	}
	// The last week is still waiting for its spring at time zero. Its cell
	// center must already be painted in a different color from the gutter.
	x := int(canvas.X + canvas.W - 5)
	y := int(canvas.Y) + int(time.Now().Weekday())*14 + 5
	frame := tt.Image()
	for dy := 0; dy <= 6; dy++ {
		if frame.RGBAAt(x, y+dy) != frame.RGBAAt(x+7, y+dy) {
			return
		}
	}
	t.Fatal("today's cell was transparent on the first data frame")
}

func TestColdStartHiddenWindowStopsPollingAndRevalidatesOnFocus(t *testing.T) {
	managementToken := p1TestTokenFile(t)
	readingToken := p1TestTokenFile(t)
	var directoryCalls atomic.Int32
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			directoryCalls.Add(1)
			_ = json.NewEncoder(w).Encode(AccessDirectory{Revision: 1})
			return
		}
		http.NotFound(w, r)
	})
	a := New(Config{ServiceURL: coldStartTestURL, TokenFile: readingToken, ProjectControl: &ProjectControlConfig{
		URL: coldStartTestURL, ManagementTokenFile: managementToken,
	}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	a.Start()
	defer a.cancelAndResetPanes()
	drainReading(t, updates, func() bool { return a.access.Loaded })

	firstRequest := a.access.Request
	a.SetWindowActive(true)
	if a.access.Request != firstRequest || directoryCalls.Load() != 1 {
		t.Fatal("focus ignored startup directory throttle")
	}

	// Visible but blurred window must STILL check credentials on its 2s timer.
	a.SetWindowActive(false)
	identity := a.readingIdentity
	p := a.state()
	p.Loaded = true
	p.Items = []Item{{ID: "sensitive-visible", Body: "private text", BodyLoaded: true}}
	p.Selected = &p.Items[0]
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		a.View(c)
	}, 1200, 820)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := os.Remove(managementToken); err != nil {
		t.Fatal(err)
	}
	builds = 0
	tt.Advance(2 * time.Second)
	drainReading(t, updates, func() bool { return a.readingIdentity != identity })
	if builds == 0 || a.readingIdentity == identity || p.Loaded || p.Selected != nil {
		t.Fatal("visible unfocused window failed to withdraw content after credential removal")
	}

	// Restore token, then hide the window (⌘W): all periodic polling must stop.
	if err := os.WriteFile(managementToken, []byte("rotated-token-value-12345678901234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	a.SetWindowActive(true)
	drainReading(t, updates, func() bool { return a.readingIdentity != "unavailable" && !a.overviewState.Loading })
	identity = a.readingIdentity
	p.Loaded = true
	p.Items = []Item{{ID: "sensitive-hidden", Body: "private text", BodyLoaded: true}}
	p.Selected = &p.Items[0]
	a.SetWindowHidden(true)
	a.access.Next = time.Now().Add(time.Hour) // Still within normal throttle window.
	if err := os.WriteFile(managementToken, []byte("rotated-again-token-0123456789abcdef012345"), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(managementToken, future, future)
	tt.Frame()
	builds = 0
	tt.Advance(20 * time.Second)
	if builds != 0 || tt.FramePending() || a.access.Request != firstRequest ||
		a.readingIdentity != identity || !p.Loaded || a.overviewState.Loading {
		t.Fatalf("hidden view polled or revalidated: builds=%d directory=%d", builds, a.access.Request)
	}
	// Reopening a hidden window must IMMEDIATELY withdraw removed credentials AND
	// bypass access.Next to revalidate server-side authorization.
	a.SetWindowHidden(false)
	bodyVisible := len(p.Items) > 0 && (p.Items[0].BodyLoaded || p.Items[0].Body != "")
	if a.readingIdentity == identity || p.Loaded || p.Selected != nil || bodyVisible {
		t.Fatal("reopening hidden window did not immediately withdraw content after credential removal")
	}
	if a.access.Request != firstRequest+1 {
		t.Fatal("reopening hidden window did not immediately revalidate project directory")
	}
	tt.Frame()
	builds = 0
	tt.Advance(2 * time.Second)
	if builds == 0 {
		t.Fatal("active view failed to re-arm periodic checks")
	}
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if directoryCalls.Load() != 2 {
		t.Fatalf("expected two directory requests, got %d", directoryCalls.Load())
	}
	a.cancelOverviewRequests()
}

func TestColdStartSlowDirectoryResetsBootstrapFallbackDeadline(t *testing.T) {
	origFallback := startupDeferredFallbackTimeout
	origPostFrame := startupPostFirstFrameDelay
	startupDeferredFallbackTimeout = 160 * time.Millisecond
	startupPostFirstFrameDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		startupDeferredFallbackTimeout = origFallback
		startupPostFirstFrameDelay = origPostFrame
	})

	dirRelease := make(chan struct{})
	bootstrapStarted := make(chan struct{}, 1)
	bootstrapRelease := make(chan struct{})
	defer close(bootstrapRelease)
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			<-dirRelease
			_ = json.NewEncoder(w).Encode(AccessDirectory{
				Revision: 1, Projects: []AccessProject{{ID: "p", Name: "Memlane", Dataset: "ds", Status: "verified"}},
			})
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{APIVersions: []string{"v1"}, ReadingContract: readingContract})
		case "/v1/overview/bootstrap":
			bootstrapStarted <- struct{}{}
			select {
			case <-bootstrapRelease:
			case <-r.Context().Done():
				return
			}
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{Generation: 1, TotalKnowledgeCount: 1})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: &ProjectControlConfig{
		URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
	}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	defer a.cancelOverviewRequests()
	defer a.cancelAndResetPanes()
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }

	a.Start()
	// Directory takes 100ms (> half of 160ms fallback).
	time.Sleep(100 * time.Millisecond)
	close(dirRelease)
	drainReading(t, updates, func() bool { return a.access.Loaded })
	// Starting bootstrap must reset the fallback timer to a fresh 160ms window.
	a.loadOverview()
	select {
	case <-bootstrapStarted:
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not start")
	}
	// At t=190ms from Start() (past original 160ms, but only 90ms into bootstrap),
	// deferred startup must NOT have fired yet.
	time.Sleep(90 * time.Millisecond)
	for len(updates) > 0 {
		(<-updates)()
	}
	if a.startupDeferred.done || a.settingsController.IntegrationState().Request != 0 {
		t.Fatal("original directory fallback fired prematurely during fresh bootstrap")
	}
}

func TestColdStartFirstUseLoadsOnceAndRendersAfterPublication(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "client-state.json")
	now := time.Now()
	state := firstUseState{Version: 1, Date: now.AddDate(0, 0, -16).Format("2006-01-02"), Zone: "UTC", Completed: now.Format(time.RFC3339)}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{ClientStateFile: statePath}, nil)
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	tt := ui.NewTester(a.overviewHome, 1200, 820)
	tt.Frame()
	if tt.HasText("使用时间暂不可用。") || tt.HasText("17") || !tt.HasText("—") ||
		a.overviewFirstUse.Request != 1 || a.overviewFirstUse.LoadingKey == (overviewFirstUseKey{}) {
		t.Fatal("first-use count did not stay in the deduplicated pending state")
	}
	nextStartupUpdate(t, updates)()
	tt.Frame()
	if !tt.HasText("17") || tt.HasText("使用时间暂不可用。") {
		t.Fatalf("first-use count did not render: %v", tt.Texts())
	}

	// Two different keys finish out of order. The older publication must
	// leave the newer count intact.
	nextDay := now.AddDate(0, 0, 1)
	a.startOverviewFirstUseDaysLoad(nextDay)
	old := nextStartupUpdate(t, updates)
	newDay := nextDay.AddDate(0, 0, 1)
	a.startOverviewFirstUseDaysLoad(newDay)
	nextStartupUpdate(t, updates)()
	old()
	if days, err, ready := a.overviewFirstUseDays(newDay); !ready || err != nil || days != 19 {
		t.Fatalf("stale first-use result: days=%d err=%v ready=%v", days, err, ready)
	}
	missing := New(Config{}, nil)
	if _, err, ready := missing.overviewFirstUseDays(now); !ready || err == nil || err.Error() != "尚未完成初始化" {
		t.Fatal("unconfigured first-use count did not report initialization state")
	}
}

func TestColdStartLoginReadDoesNotOverwriteToggle(t *testing.T) {
	a := New(Config{}, nil)
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.ReadOpenAtLoginFn = func() bool { return true }
	a.RefreshOpenAtLoginAsync()
	first := nextStartupUpdate(t, updates)
	if a.OpenAtLogin() {
		t.Fatal("login read escaped UI publication")
	}
	first()
	if !a.OpenAtLogin() {
		t.Fatal("async login result was not applied")
	}

	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var reads atomic.Int32
	a.ReadOpenAtLoginFn = func() bool {
		if reads.Add(1) == 1 {
			close(started)
			<-release
			return true
		}
		return false
	}
	a.SetOpenAtLoginFn = func(bool) error { return nil }
	a.RefreshOpenAtLoginAsync()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("login query did not start on worker")
	}
	a.ToggleOpenAtLogin(false)
	if a.OpenAtLogin() {
		t.Fatal("toggle did not publish its OS receipt")
	}
	// Let the older read finish only after ToggleOpenAtLogin returned.
	release <- struct{}{}
	nextStartupUpdate(t, updates)()
	if a.OpenAtLogin() || !a.desktopPrefs.LoginSynced {
		t.Fatal("late login read overwrote the toggle")
	}
}

func TestColdStartWaitsForV2OverviewAndActivityBootstrap(t *testing.T) {
	bootstrapStarted, releaseBootstrap := make(chan struct{}, 1), make(chan struct{})
	a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: readingControlConfig(t, coldStartTestURL)}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			json.NewEncoder(w).Encode(AccessDirectory{Projects: []AccessProject{{ID: "p", Dataset: "ds"}}})
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/overview/bootstrap":
			bootstrapStarted <- struct{}{}
			select {
			case <-releaseBootstrap:
			case <-r.Context().Done():
				return
			}
			json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{Generation: 1})
		case "/api/v1/activity/pipeline-runs", "/api/v1/datasets/ds/data", "/api/v1/sessions":
			t.Error("overview startup requested a legacy route", r.URL)
			http.NotFound(w, r)
		default:
			w.Write([]byte(`{}`))
		}
	})}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	a.Start()
	defer a.cancelOverviewRequests()
	defer a.cancelAndResetPanes()
	drainReading(t, updates, func() bool { return a.access.Loaded })
	a.loadOverview()
	select {
	case <-bootstrapStarted:
	case <-time.After(time.Second):
		t.Fatal("bootstrap request did not start")
	}
	if !a.overviewState.Loading || !a.overviewState.ActivityLoading || a.startupDeferred.done || a.monitor.PollEnabled {
		t.Fatal("overview released startup before the combined response settled")
	}
	close(releaseBootstrap)
	drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.overviewState.ActivityLoaded })
	if !a.overviewState.ColdStartMotion || !a.startupDeferred.done ||
		a.startupDeferred.timer != nil || !a.monitor.PollEnabled {
		t.Fatal("overview did not release deferred startup after both results")
	}
}

func TestColdStartHiddenRevealNeverDeadlocksVisibleStartup(t *testing.T) {
	origFallback := startupDeferredFallbackTimeout
	origPostFrame := startupPostFirstFrameDelay
	startupDeferredFallbackTimeout = 100 * time.Millisecond
	startupPostFirstFrameDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		startupDeferredFallbackTimeout = origFallback
		startupPostFirstFrameDelay = origPostFrame
	})

	t.Run("login hidden with failing directory re-arms fallback on reveal", func(t *testing.T) {
		transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/projects":
				http.Error(w, "directory unavailable", http.StatusServiceUnavailable)
			case "/health", "/v1/runtime/monitor", "/v1/generation", "/v1/connections":
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		})
		a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: &ProjectControlConfig{
			URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
		}}, nil)
		a.Client.HTTP = &http.Client{Transport: transport}
		defer a.cancelOverviewRequests()
		defer a.cancelAndResetPanes()
		updates := make(chan func(), 64)
		a.Update = func(fn func()) { updates <- fn }
		attaches := 0
		a.graphController.AttachHost = func(*GraphHost) error { attaches++; return nil }
		defer func() {
			if a.graphController.Host != nil {
				a.graphController.Host.Close()
			}
		}()

		a.SetWindowHidden(true)
		a.Start()
		// Wait for initial failed /v1/projects and the hidden 100ms fallback.
		drainReading(t, updates, func() bool { return a.startupDeferred.done && !a.access.Loading })
		if a.startupDeferred.visibleDone || a.monitor.PollEnabled || a.startupDeferred.timer != nil {
			t.Fatal("hidden startup should have deferred visible tasks with stopped timer")
		}
		// Reveal while /v1/projects is still failing: must re-arm fallback and release visible startup.
		a.SetWindowHidden(false)
		if a.startupDeferred.timer == nil {
			t.Fatal("reveal with failing directory did not re-arm visible startup fallback")
		}
		drainReading(t, updates, func() bool { return a.startupDeferred.visibleDone && a.monitor.PollEnabled && attaches == 1 })
	})

	t.Run("bootstrap settles while hidden then quick reopen releases visible startup", func(t *testing.T) {
		bootstrapStarted, releaseBootstrap := make(chan struct{}, 1), make(chan struct{}, 1)
		defer close(releaseBootstrap)
		transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/projects":
				_ = json.NewEncoder(w).Encode(AccessDirectory{
					Revision: 1, Projects: []AccessProject{{ID: "p", Name: "Memlane", Dataset: "ds", Status: "verified"}},
				})
			case "/v1/capabilities":
				_ = json.NewEncoder(w).Encode(ControlCapabilities{APIVersions: []string{"v1"}, ReadingContract: readingContract})
			case "/v1/overview/bootstrap":
				bootstrapStarted <- struct{}{}
				<-releaseBootstrap
				_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
					Generation: 1, TotalKnowledgeCount: 1,
					RecentItems: []v2KnowledgeItem{{DocID: "k", DatasetID: "ds", ProjectID: "p", Title: "settled while hidden"}},
				})
			case "/health", "/v1/runtime/monitor", "/v1/generation", "/v1/connections", "/v1/projects/p/message-summary":
				_, _ = w.Write([]byte(`{"project_id":"p","unread_count":0}`))
			default:
				http.NotFound(w, r)
			}
		})
		a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: &ProjectControlConfig{
			URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
		}}, nil)
		a.Client.HTTP = &http.Client{Transport: transport}
		defer a.cancelOverviewRequests()
		defer a.cancelAndResetPanes()
		updates := make(chan func(), 64)
		a.Update = func(fn func()) { updates <- fn }
		attaches := 0
		a.graphController.AttachHost = func(*GraphHost) error { attaches++; return nil }
		defer func() {
			if a.graphController.Host != nil {
				a.graphController.Host.Close()
			}
		}()

		a.Start()
		drainReading(t, updates, func() bool { return a.access.Loaded })
		a.loadOverview()
		<-bootstrapStarted

		// User hits ⌘W while bootstrap is in flight, and bootstrap settles while hidden.
		a.SetWindowHidden(true)
		releaseBootstrap <- struct{}{}
		drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.startupDeferred.done })
		if a.startupDeferred.visibleDone || a.monitor.PollEnabled {
			t.Fatal("hidden settlement should keep visible polling paused until reveal")
		}

		// Quick reopen (within 3s, with search overlay open so overview reload is skipped):
		// must immediately release visible startup tasks & monitor polling.
		a.searchUI.Open = true
		a.SetWindowHidden(false)
		if !a.startupDeferred.visibleDone || !a.monitor.PollEnabled || !a.monitor.Polling {
			t.Fatal("reopening after hidden bootstrap settlement deadlocked visible startup")
		}
		drainReading(t, updates, func() bool { return attaches == 1 })
	})
}

func TestColdStartManagementTokenRemovalInvalidatesReadingOnUnhide(t *testing.T) {
	managementToken := p1TestTokenFile(t)
	readingToken := p1TestTokenFile(t)
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			_ = json.NewEncoder(w).Encode(AccessDirectory{
				Revision: 1, Projects: []AccessProject{{ID: "p", Name: "Memlane", Dataset: "ds", Status: "verified"}},
			})
			return
		}
		http.NotFound(w, r)
	})
	a := New(Config{
		ServiceURL: coldStartTestURL,
		TokenFile:  readingToken,
		ProjectControl: &ProjectControlConfig{
			URL:                 coldStartTestURL,
			ManagementTokenFile: managementToken,
		},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	defer a.cancelAndResetPanes()

	a.RefreshProjectDirectory()
	drainReading(t, updates, func() bool { return a.access.Loaded })

	a.navigate("知识", 0)
	p := a.state()
	p.Loaded = true
	p.Items = []Item{{ID: "k1", Dataset: "ds", Body: "secret management-gated body", BodyLoaded: true}}
	p.Selected = &p.Items[0]

	// Hide window, remove ONLY ManagementTokenFile (official TokenFile stays valid), then unhide.
	a.SetWindowHidden(true)
	if err := os.Remove(managementToken); err != nil {
		t.Fatal(err)
	}
	a.SetWindowHidden(false)
	drainReading(t, updates, func() bool { return !a.access.Loading })

	if a.access.DirectoryError != "项目管理连接凭据不可用" {
		t.Fatalf("unexpected DirectoryError: %q", a.access.DirectoryError)
	}
	bodyVisible := len(p.Items) > 0 && (p.Items[0].BodyLoaded || p.Items[0].Body != "")
	if p.Selected != nil || bodyVisible {
		t.Fatalf("missing ManagementTokenFile failed to withdraw cached reading content: selected=%v item=%+v", p.Selected, p.Items)
	}
}

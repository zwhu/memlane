package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	before := officialGraphDispatcher.host.Load()
	if !mygo.Protocol.IsHandled("memlanegraph") || a.graph != nil || a.graphError != "" || a.graphAssetsChecked {
		t.Fatal("New must register the protocol and leave verification pending")
	}
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	a.graphAttachFn = func(*GraphHost) error {
		t.Fatal("attach ran with unverified assets")
		return nil
	}
	a.pendingGraphLoad = &graphLoad{}
	a.graphLoading = true
	a.EnsureGraphAttached()
	if a.graphAssetsChecked {
		t.Fatal("verification published before the UI callback")
	}
	nextStartupUpdate(t, updates)()
	if !a.graphAssetsChecked || a.graphAssetsErr == nil || a.graphError != "图组件校验失败" ||
		a.graph != nil || a.pendingGraphLoad != nil || a.graphLoading || officialGraphDispatcher.host.Load() != before {
		t.Fatalf("verification did not fail closed: %q", a.graphError)
	}
	reply := httptest.NewRecorder()
	officialGraphDispatcher.serveProtocol(reply, httptest.NewRequest("GET", "memlanegraph://graph/app.js", nil))
	if reply.Code != http.StatusNotFound {
		t.Fatalf("unverified asset request returned %d", reply.Code)
	}
	if err := a.AttachGraph(nil); err == nil {
		t.Fatal("synchronous attachment accepted failed verification")
	}
}

func TestColdStartGraphAttachesOnceAndAppliesWaitingHTTPResult(t *testing.T) {
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/visualize/json" || r.URL.Query().Get("dataset_id") != "ds" {
			t.Errorf("unexpected graph request %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"nodes":[{"id":"n"}],"links":[],"color_maps":{}}`))
	})

	for _, scenario := range []string{"attach", "closed", "navigate", "reset", "invalidate", "sync attach"} {
		t.Run(scenario, func(t *testing.T) {
			a := New(Config{ServiceURL: coldStartTestURL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
			a.Client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 8)
			a.Update = func(fn func()) { updates <- fn }
			attaches := 0
			a.graphAttachFn = func(*GraphHost) error { attaches++; return nil }

			a.PrewarmGraph()
			check := a.graphAssetsCheck
			a.navigate("图谱", 0)
			if check == nil || a.graphAssetsCheck != check || attaches != 0 || a.graph != nil {
				t.Fatal("graph entry did not share the asynchronous verification")
			}
			publishAssets := nextStartupUpdate(t, updates)
			a.loadGraph()
			nextStartupUpdate(t, updates)()
			if a.pendingGraphLoad == nil || !a.graphLoading || a.graph != nil {
				t.Fatal("HTTP result was lost before asset publication")
			}
			switch scenario {
			case "closed":
				a.graphWindowClosed = true
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
			if !a.graphAssetsChecked || a.graphAssetsErr != nil || a.pendingGraphLoad != nil || a.graphLoading {
				t.Fatal("graph did not settle")
			}
			if scenario == "closed" {
				if a.graph != nil || attaches != 0 {
					t.Fatal("closed window was attached")
				}
				return
			}
			if a.graph == nil || attaches != 1 || a.graph.assets["index.html"] == nil {
				t.Fatalf("expected one verified attach, got %d", attaches)
			}
			t.Cleanup(a.graph.close)
			if scenario == "attach" || scenario == "sync attach" {
				if a.graph.project != "p" || a.graph.dataset != "ds" || a.graph.nodes["n"] == nil {
					t.Fatal("pending graph was not published")
				}
			} else if a.graph.frame != nil {
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
			a.graphAttachFn = func(*GraphHost) error { attaches++; return nil }
			defer func() {
				if a.graph != nil {
					a.graph.close()
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
				drainReading(t, updates, func() bool { return a.integrations.Loaded && a.OpenAtLogin() })
				if a.monitor.Request != 0 || a.graphAssetsCheck != nil || a.graph != nil {
					t.Fatal("hidden startup started monitor or prewarm")
				}
				a.SetWindowHidden(false)
				if !a.monitor.Polling || a.graphAssetsCheck == nil || attaches != 0 {
					t.Fatal("first reveal must start monitor and async asset check without synchronous WebView attach")
				}
				drainReading(t, updates, func() bool { return attaches == 1 })
				return
			}

			select {
			case <-bootstrapStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("first directory render did not start overview bootstrap")
			}
			if !a.coldStartOverviewPending() || a.integrations.Request != 0 || a.monitor.Request != 0 ||
				loginReads.Load() != 0 || a.graphAssetsCheck != nil || a.existingProjectQueueState(a.access.Directory.Projects[0]) != nil {
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
				drainReading(t, updates, func() bool { return a.integrations.Loaded })
				staleFallback()
			default:
				releaseBootstrap <- struct{}{}
				nextStartupUpdate(t, updates)()
			}
			if !a.startupDeferred.done || a.startupDeferred.timer != nil || !a.overviewFirstScreenReady ||
				!a.monitor.PollEnabled || a.integrations.Request != 1 || a.monitor.Request != 1 {
				t.Fatal("settlement did not release deferred work exactly once")
			}
			if scenario == "success" {
				if attaches != 0 || loginReads.Load() != 0 {
					t.Fatalf("main-thread prewarm/login ran before post-first-frame delay: attaches=%d loginReads=%d", attaches, loginReads.Load())
				}
				if !a.overviewState.overviewColdStartMotion {
					t.Fatal("first payload did not arm cold-start motion")
				}
				assertColdStartHeatmapPainted(t, tt)
			} else if scenario == "fail closed" && (a.overviewState.Error == "" || a.overviewState.Loading) {
				t.Fatal("failed bootstrap did not settle in the error state")
			}

			want := map[string]bool{"/v1/generation": true, "/v1/connections": true, "/health": true, "/v1/runtime/monitor": true}
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
				if a.overviewState.overviewColdStartMotion {
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
	if err := os.Remove(readingToken); err != nil {
		t.Fatal(err)
	}
	builds = 0
	tt.Advance(2 * time.Second)
	if builds == 0 || a.readingIdentity == identity || p.Loaded || p.Selected != nil {
		t.Fatal("visible unfocused window failed to withdraw content after credential removal")
	}

	// Restore token, then hide the window (⌘W): all periodic polling must stop.
	if err := os.WriteFile(readingToken, []byte("rotated-token-value-12345678901234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	a.SetWindowActive(true)
	identity = a.readingIdentity
	p.Loaded = true
	p.Items = []Item{{ID: "sensitive-hidden", Body: "private text", BodyLoaded: true}}
	p.Selected = &p.Items[0]
	a.SetWindowHidden(true)
	a.access.Next = time.Now().Add(time.Hour) // Still within normal throttle window.
	if err := os.Remove(readingToken); err != nil {
		t.Fatal(err)
	}
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
	if a.startupDeferred.done || a.integrations.Request != 0 {
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
		a.overviewFirstUse.seq != 1 || a.overviewFirstUse.cachedFirstUseLoadingKey == (overviewFirstUseKey{}) {
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

func TestColdStartLegacyWaitsForActivityAndOverview(t *testing.T) {
	activityStarted, releaseActivity := make(chan struct{}), make(chan struct{})
	defer close(releaseActivity)
	a := New(Config{ServiceURL: coldStartTestURL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/activity/pipeline-runs":
			close(activityStarted)
			select {
			case <-releaseActivity:
			case <-r.Context().Done():
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/sessions":
			_, _ = w.Write([]byte(`{"sessions":[]}`))
		case "/api/v1/datasets/ds/data":
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.Start()
	defer a.cancelOverviewRequests()
	defer a.cancelAndResetPanes()
	nextStartupUpdate(t, updates)()
	select {
	case <-activityStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("activity request did not start")
	}
	if !a.overviewState.Loaded || !a.overviewState.ActivityLoading || a.startupDeferred.done || a.monitor.PollEnabled {
		t.Fatal("legacy overview released startup before activity settled")
	}
	releaseActivity <- struct{}{}
	nextStartupUpdate(t, updates)()
	if !a.overviewState.ActivityLoaded || !a.overviewState.overviewColdStartMotion ||
		!a.startupDeferred.done || a.startupDeferred.timer != nil || !a.monitor.PollEnabled {
		t.Fatal("legacy overview did not settle after both results")
	}
}

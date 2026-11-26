package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func receiveTrayUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case fn := <-updates:
		return fn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tray update")
		return nil
	}
}

func TestTrayClickRefreshesMonitorAndRecoversDirectoryDuringHiddenColdStart(t *testing.T) {
	token := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(token, []byte("synthetic-tray-management-token-for-tests"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{
		ServiceURL:     "http://tray-service.test",
		ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61257", ManagementTokenFile: token},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/health":
			fmt.Fprint(w, `{"ok":true}`)
		case "/v1/runtime/monitor":
			fmt.Fprint(w, `{"state":"ready"}`)
		case "/v1/completion/health":
			fmt.Fprint(w, `{"status":"ready"}`)
		case "/v1/projects":
			json.NewEncoder(w).Encode(AccessDirectory{})
		default:
			t.Errorf("unexpected request during tray click: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	a.page = "总览"
	a.windowHidden, a.windowInactive = true, true
	a.startupDeferred.armed = true
	a.monitor.NextPollAt = time.Now().Add(time.Hour)
	a.monitor.Expanded = true
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	if !a.coldStartOverviewPending() {
		t.Fatal("fixture must start before the overview is ready")
	}

	a.RefreshForTrayClick()
	if !a.access.Loading || !a.monitor.Polling {
		t.Fatal("tray click did not bypass the normal refresh guards")
	}
	drainReading(t, updates, func() bool { return !a.access.Loading && !a.monitor.Polling })
	if !a.access.Loaded || a.monitor.State != "ready" || !a.monitor.ControlReachable {
		t.Fatalf("tray click did not recover live status: directory=%t (%s) monitor=%s control=%t (%s)",
			a.access.Loaded, a.access.DirectoryError, a.monitor.State, a.monitor.ControlReachable, a.monitor.ControlError)
	}
	if a.overviewFirstScreenReady || a.overviewState.Loading || a.monitor.PollEnabled || !a.coldStartOverviewPending() {
		t.Fatal("tray click changed cold-start scheduling")
	}
}

func TestTrayBackgroundPollingWaitsForTickAndOnlyRefreshesHiddenMainWindow(t *testing.T) {
	setTrayBackgroundPollInterval(time.Hour)
	t.Cleanup(func() { setTrayBackgroundPollInterval(0) })

	a := New(Config{ServiceURL: "http://tray-service.test"}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected background read: %s", r.URL)
		}
		fmt.Fprint(w, `{"ok":true}`)
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(a.StopTrayBackgroundPolling)
	a.windowHidden = true
	a.StartTrayBackgroundPolling()
	select {
	case <-updates:
		t.Fatal("tray polled before the first tick")
	case <-time.After(20 * time.Millisecond):
	}
	a.StopTrayBackgroundPolling()

	setTrayBackgroundPollInterval(10 * time.Millisecond)
	a.StartTrayBackgroundPolling()
	stop := a.trayPoll.stop
	a.StartTrayBackgroundPolling()
	if stop != a.trayPoll.stop {
		t.Fatal("starting twice replaced the active poller")
	}
	a.windowHidden = false
	receiveTrayUpdate(t, updates)()
	a.windowHidden = true
	a.settingsController.SetInitialSetupForHost(true)
	receiveTrayUpdate(t, updates)()
	a.settingsController.SetInitialSetupForHost(false)
	a.Config.GenerationSetupOnly = true
	receiveTrayUpdate(t, updates)()
	if a.monitor.Request != 0 {
		t.Fatal("background poll ran while visible or during setup")
	}
	a.Config.GenerationSetupOnly = false
	receiveTrayUpdate(t, updates)()
	a.StopTrayBackgroundPolling()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.Request != 1 || a.monitor.State != "ready" || a.monitor.PollEnabled || a.overviewState.Loading {
		t.Fatalf("unexpected background poll outcome: requests=%d state=%s", a.monitor.Request, a.monitor.State)
	}
}

func TestTrayBackgroundPollingStopDoesNotWaitForUISynchronousUpdate(t *testing.T) {
	setTrayBackgroundPollInterval(5 * time.Millisecond)
	t.Cleanup(func() { setTrayBackgroundPollInterval(0) })

	a := New(Config{ServiceURL: "http://tray-service.test"}, nil)
	a.windowHidden = true
	updates := make(chan func())
	release := make(chan struct{})
	done := make(chan struct{})
	a.Update = func(fn func()) {
		updates <- fn
		<-release
		close(done)
	}
	t.Cleanup(a.StopTrayBackgroundPolling)
	a.StartTrayBackgroundPolling()
	fn := receiveTrayUpdate(t, updates)
	a.StopTrayBackgroundPolling()
	a.StopTrayBackgroundPolling()
	fn()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update did not return after UI was released")
	}
	if a.monitor.Request != 0 {
		t.Fatal("a queued poll refreshed after shutdown")
	}
}

func TestTrayClickRefreshesDueQueuesWithBoundedConcurrency(t *testing.T) {
	a := settingsFixture()
	a.Config.ServiceURL, a.Config.ProjectControl = "", nil
	a.Client.Config = a.Config
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	a.access.Directory.Bindings = nil
	a.access.Directory.Projects = nil
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("project-%d", i)
		a.access.Directory.Projects = append(a.access.Directory.Projects, AccessProject{ID: id, Dataset: id})
	}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.windowHidden, a.windowInactive = true, true
	alreadyFresh := a.projectQueueStateForAsync(a.access.Directory.Projects[0])
	alreadyFresh.Loaded, alreadyFresh.Next = true, time.Now().Add(time.Hour)

	a.RefreshForTrayClick()
	a.RefreshForTrayClick()
	loading := func() int {
		count := 0
		for _, q := range a.access.Queues {
			if q.Loading {
				count++
			}
		}
		return count
	}
	if loading() != 3 || alreadyFresh.Loading || a.monitor.Request != 0 {
		t.Fatalf("tray queue fanout=%d, fresh queue loading=%t, monitor requests=%d", loading(), alreadyFresh.Loading, a.monitor.Request)
	}
	drainReading(t, updates, func() bool { return loading() == 0 })
	a.RefreshForTrayClick()
	if loading() != 1 {
		t.Fatalf("tray did not continue from the next due project: fanout=%d", loading())
	}
	drainReading(t, updates, func() bool { return loading() == 0 })
}

func TestForegroundPollingRefreshesActiveInboxAndStopsCleanly(t *testing.T) {
	setForegroundPollInterval(15 * time.Millisecond)
	t.Cleanup(func() { setForegroundPollInterval(0) })

	a := settingsFixture()
	a.page = "设置"
	a.access.OpenProject = "p"
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.monitor.PollEnabled = false

	var projectCalls atomic.Int32
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			fmt.Fprint(w, `{"reading_contract":"catalog-cursor-v1","features":{"async_capture":true}}`)
		case "/api/reading-state":
			fmt.Fprint(w, `{"projects":[{"project_id":"p","dataset_id":"ds","unread_count":1,"state_rev":1}]}`)
		case "/api/projects/p":
			n := projectCalls.Add(1)
			pending := 2
			if n >= 2 {
				pending = 1
			}
			fmt.Fprintf(w, `{"id":"p","dataset_id":"ds","inbox_pending":%d,"inbox_failed":0}`, pending)
		default:
			http.NotFound(w, r)
		}
	})
	a.Config.ProjectControl = readingControlConfig(t, "http://127.0.0.1:16531")
	a.Client.Config = a.Config
	a.Client.HTTP = &http.Client{Transport: transport}
	a.syncSettingsTransport()

	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.InboxPending = 2
	q.Next = time.Now().Add(-time.Second)

	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(a.StopForegroundPolling)

	a.StartForegroundPolling()
	stop := a.foregroundPoll.stop
	done := a.foregroundPoll.done
	a.StartForegroundPolling()
	if stop != a.foregroundPoll.stop {
		t.Fatal("starting foreground polling twice replaced the active poller")
	}

	// Visible & active: next tick must refresh the due project queue and keep 5s cadence.
	receiveTrayUpdate(t, updates)()
	drainReading(t, updates, func() bool { return !q.Loading && projectCalls.Load() >= 1 })
	if got := time.Until(q.Next); got <= 0 || got > 6*time.Second {
		t.Fatalf("active inbox queue Next=%v, want <= 5s cadence", got)
	}

	// Hidden window: completely silent, zero UI wakeups and zero queue requests.
	a.SetWindowHidden(true)
	q.Next = time.Now().Add(-time.Second)
	beforeHidden := projectCalls.Load()
	// Drain any already-enqueued tick before measuring silence.
	for len(updates) > 0 {
		(<-updates)()
	}
	select {
	case fn := <-updates:
		fn()
		t.Fatalf("hidden window woke UI thread for foreground polling")
	case <-time.After(45 * time.Millisecond):
	}
	if projectCalls.Load() != beforeHidden {
		t.Fatalf("hidden window triggered project queue refresh: %d -> %d", beforeHidden, projectCalls.Load())
	}

	// Visible but inactive (blurred): also completely silent.
	a.SetWindowHidden(false)
	a.SetWindowActive(false)
	// Let the unhide directory revalidation finish before asserting silence.
	drainReading(t, updates, func() bool { return !a.access.Loading })
	q.Next = time.Now().Add(-time.Second)
	for len(updates) > 0 {
		(<-updates)()
	}
	beforeInactive := projectCalls.Load()
	select {
	case fn := <-updates:
		fn()
		t.Fatalf("inactive window woke UI thread for foreground polling")
	case <-time.After(45 * time.Millisecond):
	}
	if projectCalls.Load() != beforeInactive {
		t.Fatalf("inactive window triggered project queue refresh: %d -> %d", beforeInactive, projectCalls.Load())
	}

	// StopForegroundPolling must be idempotent and exit the goroutine cleanly.
	a.StopForegroundPolling()
	a.StopForegroundPolling()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("foreground poller goroutine did not exit after StopForegroundPolling")
	}
}

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

package settings

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
)

func TestBacklogUIStartsOneOwnedJobThenOnlyPollsAndShowsIsolatedCounts(t *testing.T) {
	a, q := projectQueueFixture()
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte("private-test-control-credential-1234567890"), 0600)
	posts := 0
	job := &BacklogJob{Operation: "server-operation", Status: "running", Saved: 20, Remaining: 75, Total: 95, Isolated: 20}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-control-credential-1234567890" {
			t.Error("wrong management identity")
		}
		if r.Method == "POST" {
			posts++
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			if len(input["operation_id"]) != 36 {
				t.Error("missing stable operation identity")
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"job": job})
	})
	a.config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
	a.client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 20)
	a.dispatch = func(fn func()) { updates <- fn }
	p := a.access.Directory.Projects[0]
	a.changeBacklog(q, p, false)
	drainReading(t, updates, func() bool { return !q.BacklogBusy })
	if posts != 1 || q.Backlog == nil || !q.Backlog.active() {
		t.Fatal("job not started")
	}
	for n := 0; n < 2; n++ {
		q.BacklogOperation = job.Operation
		a.refreshBacklog(q, p)
		drainReading(t, updates, func() bool { return !q.BacklogLoading })
		if q.BacklogOperation != "" {
			t.Fatal("confirmed original job left a stale start request")
		}
	}
	if posts != 1 {
		t.Fatal("polling restarted a content job")
	}
	q.Next = time.Now().Add(time.Hour)
	q.BacklogNext = time.Now().Add(time.Hour)
	q.InboxPending, q.InboxFailed = 75, 20
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"自动采集", "有 75 条原始记录等待提炼", "采集队列有 20 项失败"} {
		if !tt.HasText(label) {
			t.Fatal("missing inbox status", label)
		}
	}
	if tt.HasText("保存一批") {
		t.Fatal("manual batch controls remain")
	}
	q.Backlog = &BacklogJob{Operation: "server-operation", Status: "completed", Saved: 95, Total: 95}
	q.InboxPending = 0
	tt.Frame()
	if tt.HasText("有 75 条原始记录等待提炼") || !tt.HasText("采集队列有 20 项失败") {
		t.Fatal("completion replaced the separate inbox failure")
	}
	q.Backlog = job
	a.page = "总览"
	if !q.Backlog.active() || posts != 1 {
		t.Fatal("leaving page cancelled or restarted background job")
	}
}

func TestProjectAutoCaptureSwitchWorksWithoutBacklogJob(t *testing.T) {
	a, q := projectQueueFixture()
	q.InboxPending, q.InboxFailed = 0, 0
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte("private-test-control-credential-1234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	var directory AccessDirectory
	payload, _ := json.Marshal(a.access.Directory)
	if err := json.Unmarshal(payload, &directory); err != nil {
		t.Fatal(err)
	}
	maintain := "maintain"
	directory.Projects[0].Overrides["agent"] = ProjectOverride{Access: &maintain}
	writes := make(chan bool, 2)
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/p/auto-capture":
			var body struct {
				Enabled *bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
				t.Error("missing automatic capture setting", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			p := &directory.Projects[0]
			if p.AutoCaptureVersion == 0 {
				p.AutoCaptureVersion = 1
			}
			p.AutoCapture = body.Enabled
			p.AutoCaptureVersion++
			directory.Revision++
			writes <- *body.Enabled
			_ = json.NewEncoder(w).Encode(p)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			_ = json.NewEncoder(w).Encode(directory)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	a.config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
	a.client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	deferPoll := func() {
		q.Next = time.Now().Add(time.Hour)
		q.BacklogNext = q.Next
	}
	deferPoll()
	tt := ui.NewTester(func(c *ui.Context) {
		a.projectQueueSection(c, a.access.Directory.Projects[0])
	}, 1200, 820)
	if !tt.HasText("自动采集已开启") {
		t.Fatal("old directory record did not default to enabled")
	}
	for _, enabled := range []bool{false, true} {
		clickProjectUI(t, tt, "自动采集")
		drainReading(t, updates, func() bool { return !q.AutoCaptureBusy && !a.access.Loading })
		if len(writes) != 1 || <-writes != enabled || q.Backlog != nil {
			t.Fatal("the project switch must write one setting and leave backlog empty")
		}
		stale := !enabled
		q.applyAutoCapture(&stale, q.AutoCaptureVersion-1)
		deferPoll()
		tt.Frame()
		label := "自动采集已关闭"
		if enabled {
			label = "自动采集已开启"
		}
		if !tt.HasText(label) || q.projectAutoCapture(a.access.Directory.Projects[0]) != enabled {
			t.Fatalf("switch did not show confirmed project setting %v", enabled)
		}
		reopened := New(a.CurrentConfig(), nil, Host{
			HTTP:       a.client.HTTP,
			Dispatcher: shell.DispatchFunc(func(fn func()) { updates <- fn }),
		})
		reopened.RefreshProjectDirectory()
		drainReading(t, updates, func() bool { return !reopened.access.Loading })
		if !reopened.access.Loaded {
			t.Fatal("reopened controller did not load the authoritative project directory")
		}
		project := reopened.access.Directory.Projects[0]
		if got := reopened.ProjectQueue(project).projectAutoCapture(project); got != enabled {
			t.Fatalf("reopened controller read automatic capture = %v, want %v", got, enabled)
		}
		if !project.PermissionsReady(reopened.access.Directory.Agents...) ||
			project.Overrides["agent"].Effective(reopened.access.Directory.Defaults).Access != "maintain" {
			t.Fatal("automatic capture changed the project's Effective manual access")
		}
	}
}

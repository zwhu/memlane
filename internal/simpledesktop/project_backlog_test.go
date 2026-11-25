package simpledesktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestBacklogUIStartsOneOwnedJobThenOnlyPollsAndShowsIsolatedCounts(t *testing.T) {
	a, q := projectQueueFixture()
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte("private-test-control-credential-1234567890"), 0600)
	posts := 0
	job := &backlogJob{Operation: "server-operation", Status: "running", Saved: 20, Remaining: 75, Total: 95, Isolated: 20}
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
	a.Config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
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
	q.Sessions[0].View.Status = PendingMaterials{Pending: 75, Signature: "safe-snapshot"}
	q.Sessions[1].View.Status = PendingMaterials{Pending: 20, Unknown: 20, Signature: "unknown-snapshot"}
	maintain := "maintain"
	p.Overrides["agent"] = ProjectOverride{Access: &maintain}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"自动采集", "75 条消息等待采集", "20 条消息未收到采集结果"} {
		if !tt.HasText(label) {
			t.Fatal("missing real job progress", label)
		}
	}
	if tt.HasText("保存一批") {
		t.Fatal("manual batch controls remain")
	}
	if tt.HasText("系统正在查询采集结果，不会重复发送。") {
		t.Fatal("inflight journals were shown as final unknown outcomes")
	}
	fixtureImage(t, tt, "project-backlog-running")
	q.Backlog = &backlogJob{Operation: "server-operation", Status: "completed", Saved: 95, Total: 95, Isolated: 20, ProcessingSubmitted: 1}
	q.Sessions[0].View.Status.Pending = 0
	tt.Frame()
	if tt.HasText("上次采集已完成") || !tt.HasText("20 条消息未收到采集结果") {
		t.Fatal("completion and processing submission were conflated")
	}
	fixtureImage(t, tt, "project-backlog-completed")
	q.Backlog = job
	a.page = "总览"
	if !q.Backlog.active() || posts != 1 {
		t.Fatal("leaving page cancelled or restarted background job")
	}
}

func TestAutomaticBacklogPresentationKeepsExistingUnknownVisible(t *testing.T) {
	a, q := projectQueueFixture()
	q.AutomaticSave = true
	q.Backlog = &backlogJob{Automatic: true, Status: "running", Saved: 8, Remaining: 7}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("自动采集已开启") || !tt.HasText("自动采集") || tt.HasText("保存积压记录") {
		t.Fatal("automatic save still requires a manual start")
	}
	if (!tt.HasText("2 条消息等待采集") || !tt.HasText("1 条消息未收到采集结果")) || tt.HasText("任务已采集 8 条 · 任务内剩余 7 条 · 待核对 0 条") {
		t.Fatal("automatic job hid an older unknown record outside its send range")
	}
}

func TestProjectAutoCaptureSwitchWorksWithoutBacklogJob(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions, q.Documents = nil, nil
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte("private-test-control-credential-1234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	var directory AccessDirectory
	payload, _ := json.Marshal(a.access.Directory)
	if err := json.Unmarshal(payload, &directory); err != nil {
		t.Fatal(err)
	}
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
	a.Config.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
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
	}
}

func TestPausedAutomaticSaveShowsPermissionActionInsteadOfResume(t *testing.T) {
	a, q := projectQueueFixture()
	q.AutomaticSave = true
	q.Backlog = &backlogJob{Automatic: true, Status: "paused", Reason: "policy_unconfirmed", Remaining: 105}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("后台采集已暂停") || !tt.HasText("这是权限状态核对问题，不表示你需要重新授权。") || tt.HasText("继续采集") {
		t.Fatal("paused permission state presented as running or resumable")
	}
	if !tt.HasText("2 条消息等待采集") || !tt.HasText("1 条消息未收到采集结果") {
		t.Fatal("live counts replaced by stale task remaining count")
	}
}

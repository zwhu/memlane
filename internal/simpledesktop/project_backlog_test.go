package simpledesktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBacklogUIStartsOneOwnedJobThenOnlyPollsAndShowsIsolatedCounts(t *testing.T) {
	a, q := projectQueueFixture()
	token := filepath.Join(t.TempDir(), "control.token")
	os.WriteFile(token, []byte("private-test-control-credential-1234567890"), 0600)
	posts := 0
	job := &backlogJob{Operation: "server-operation", Status: "running", Saved: 20, Remaining: 75, Total: 95, Isolated: 20}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer server.Close()
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
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
	for _, label := range []string{"暂停自动保存", "75 条消息等待保存", "20 条消息未收到保存结果"} {
		if !tt.HasText(label) {
			t.Fatal("missing real job progress", label)
		}
	}
	if tt.HasText("保存一批") {
		t.Fatal("manual batch controls remain")
	}
	if tt.HasText("系统正在查询保存结果，不会重复发送。") {
		t.Fatal("inflight journals were shown as final unknown outcomes")
	}
	fixtureImage(t, tt, "project-backlog-running")
	q.Backlog = &backlogJob{Operation: "server-operation", Status: "completed", Saved: 95, Total: 95, Isolated: 20, ProcessingSubmitted: 1}
	q.Sessions[0].View.Status.Pending = 0
	tt.Frame()
	if tt.HasText("上次保存已完成") || !tt.HasText("20 条消息未收到保存结果") {
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
	if !tt.HasText("自动保存已开启") || !tt.HasText("暂停自动保存") || tt.HasText("保存积压记录") {
		t.Fatal("automatic save still requires a manual start")
	}
	if (!tt.HasText("2 条消息等待保存") || !tt.HasText("1 条消息未收到保存结果")) || tt.HasText("任务已保存 8 条 · 任务内剩余 7 条 · 待核对 0 条") {
		t.Fatal("automatic job hid an older unknown record outside its send range")
	}
}

func TestPausedAutomaticSaveShowsPermissionActionInsteadOfResume(t *testing.T) {
	a, q := projectQueueFixture()
	q.AutomaticSave = true
	q.Backlog = &backlogJob{Automatic: true, Status: "paused", Reason: "policy_unconfirmed", Remaining: 105}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("后台保存已暂停") || !tt.HasText("这是权限状态核对问题，不表示你需要重新授权。") || tt.HasText("继续保存") {
		t.Fatal("paused permission state presented as running or resumable")
	}
	if !tt.HasText("2 条消息等待保存") || !tt.HasText("1 条消息未收到保存结果") {
		t.Fatal("live counts replaced by stale task remaining count")
	}
}

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
	for _, label := range []string{"积压保存任务 · 后台运行", "停止后续保存", "本次已保存 20 条 · 其他操作已处理 0 条 · 剩余 75 条 · 隔离待核对 20 条"} {
		if !tt.HasText(label) {
			t.Fatal("missing real job progress", label)
		}
	}
	if tt.HasText("保存一批") {
		t.Fatal("manual batch controls remain")
	}
	if !tt.HasText("含 20 条等待回执或核对") {
		t.Fatal("inflight journals were shown as final unknown outcomes")
	}
	fixtureImage(t, tt, "project-backlog-running")
	q.Backlog = &backlogJob{Operation: "server-operation", Status: "completed", Saved: 95, Total: 95, Isolated: 20, ProcessingSubmitted: 1}
	q.Sessions[0].View.Status.Pending = 0
	tt.Frame()
	if !tt.HasText("本次可保存积压已完成。新增记录由正常采集继续接收。") || !tt.HasText("1 个会话已提交知识整理；完成状态以官方记录为准") {
		t.Fatal("completion and processing submission were conflated")
	}
	fixtureImage(t, tt, "project-backlog-completed")
	q.Backlog = job
	a.page = "总览"
	if !q.Backlog.active() || posts != 1 {
		t.Fatal("leaving page cancelled or restarted background job")
	}
}

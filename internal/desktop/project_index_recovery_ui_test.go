package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProjectIndexRecoveryUIKeepsSameTaskAndUsesNoAgentStart(t *testing.T) {
	requests, release := make(chan records.IndexRebuildStart, 2), make(chan struct{})
	task := records.ProjectTask{ID: "failed-task", Attempt: 3, State: "failed", Error: "project_index_failed", Snapshot: records.ProjectTaskSnapshot{Project: "project"},
		Receipt: &records.ProjectTaskReceipt{Persisted: true, Added: []records.ProjectTaskSaved{{Memory: "saved", Revision: 1}}, IndexFailed: 1}}
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "rebuild_index" {
			requests <- *r.IndexRebuildStart
			<-release
			return serviceipc.Response{OK: true, Result: records.IndexRebuild{ID: r.IndexRebuildStart.OperationID, ProjectTask: task.ID, Attempt: 3, State: "running"}}
		}
		return serviceipc.Response{OK: false, Code: "explicit_fixture_read_failure"}
	})
	a.projectOrganizer.Task = &task
	a.projectOrganizer.SelectedTask = task.ID
	// A disconnected binding must not disable local saved-content indexing.
	a.projectOrganizer.Binding.State = "disconnected"
	tt := ui.NewTester(a.View, 980, 620)
	if !tt.HasText("恢复索引不调用模型，不消耗整理 Token。") {
		t.Fatal("missing local indexing boundary")
	}
	if err := tt.Click("恢复索引"); err != nil {
		t.Fatal(err)
	}
	d := <-requests
	if d.ProjectTask != task.ID || d.Attempt != 3 || d.Scope != "" || d.OperationID == "" {
		t.Fatal(d)
	}
	a.recoverProjectIndexes()
	a.closeProjectOrganizer()
	a.openProjectOrganizer("project", task.ID)
	close(release)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Task.ID != task.ID || a.projectOrganizer.Task.State != "failed" || a.projectOrganizer.Task.IndexRecovery.ID != d.OperationID {
		t.Fatal("recovery changed original task")
	}
	if len(requests) != 0 {
		t.Fatal("reopen or duplicate started another recovery")
	}
	tt.Frame()
	if !tt.HasText("正在恢复索引") || tt.HasText("停止整理") {
		t.Fatal("recovery treated as Agent run")
	}
}

func TestProjectIndexRecoveryUIFailureRequiresReadAndSameIntentRetry(t *testing.T) {
	requests := make(chan records.IndexRebuildStart, 2)
	task := records.ProjectTask{ID: "failed-task", Attempt: 1, State: "failed", Snapshot: records.ProjectTaskSnapshot{Project: "project"}, Receipt: &records.ProjectTaskReceipt{Persisted: true, IndexFailed: 1}}
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "rebuild_index":
			requests <- *r.IndexRebuildStart
			return serviceipc.Response{OK: false, Code: "explicit_lost_reply"}
		case "project_binding":
			return serviceipc.Response{OK: true, Result: records.ProjectBinding{Project: "project", State: "disconnected"}}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		case "project_task":
			return serviceipc.Response{OK: true, Result: task}
		}
		return serviceipc.Response{OK: false, Code: "unexpected"}
	})
	a.projectOrganizer.Task = &task
	a.projectOrganizer.SelectedTask = task.ID
	a.recoverProjectIndexes()
	first := <-requests
	awaitProcessingDispatch(t, dispatched)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Error == "" {
		t.Fatal("read cleared uncertain recovery")
	}
	a.recheckProjectOrganizer()
	awaitProcessingDispatch(t, dispatched)
	a.recoverProjectIndexes()
	second := <-requests
	if first.OperationID != second.OperationID {
		t.Fatal("retry replaced uncertain intent")
	}
	awaitProcessingDispatch(t, dispatched)
	awaitProcessingDispatch(t, dispatched)
}

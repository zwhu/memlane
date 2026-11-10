package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProjectHistoryCheckboxLoadsOnlyExplicitlyAndFreezesIntent(t *testing.T) {
	queries := make(chan records.ProjectHistoryQuery, 2)
	starts := make(chan records.ProjectTaskStart, 1)
	a, dispatch := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_history" {
			if !r.UserConfirmed {
				t.Error("missing history confirmation")
			}
			queries <- *r.ProjectHistory
			return serviceipc.Response{OK: true, Result: records.ProjectHistoryPage{WorkspaceID: "frozen-workspace", Total: 1, Items: []records.ProjectHistoryChoice{{ProjectSessionSelection: records.ProjectSessionSelection{ID: "selected", Digest: "digest"}, State: "available"}}}}
		}
		if r.Op == "project_task_start" {
			starts <- *r.ProjectTaskStart
			return serviceipc.Response{OK: true, Result: records.ProjectTask{ID: "task", State: "organizing", Snapshot: records.ProjectTaskSnapshot{Project: "project", IncludeHistory: true}}}
		}
		return serviceipc.Response{OK: false, Code: "unexpected"}
	})
	tt := ui.NewTester(a.View, 980, 620)
	if a.projectOrganizer.IncludeHistory || len(queries) != 0 {
		t.Fatal("opening page read history")
	}
	if err := tt.Click("同时整理历史会话"); err != nil {
		t.Fatal(err)
	}
	q := <-queries
	if q.Project != "project" || q.BindingRevision != 2 {
		t.Fatal(q)
	}
	awaitProcessingDispatch(t, dispatch)
	tt.Frame()
	if !tt.HasText("已选择 1 个已完成会话 · 历史会话会增加用量。") || len(starts) != 0 {
		t.Fatal("selection started model or lost additional Token hint")
	}
	if err := tt.Click("使用 Codex 整理"); err != nil {
		t.Fatal(err)
	}
	d := <-starts
	if !d.IncludeHistory || len(d.Sessions) != 1 || d.HistoryWorkspaceID != "frozen-workspace" {
		t.Fatal(d)
	}
	awaitProcessingDispatch(t, dispatch)
}

func TestProjectHistoryOldReadDoesNotOverwriteNewReopenedSelection(t *testing.T) {
	requests, release := make(chan struct{}, 2), make(chan struct{})
	a, dispatch := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_history" {
			requests <- struct{}{}
			<-release
			return serviceipc.Response{OK: true, Result: records.ProjectHistoryPage{WorkspaceID: "old", Items: []records.ProjectHistoryChoice{}}}
		}
		return serviceipc.Response{OK: false, Code: "old-refresh-fixture"}
	})
	a.chooseProjectHistory(true)
	<-requests
	old := a.projectOrganizer.HistorySequence
	a.closeProjectOrganizer()
	a.openProjectOrganizer("project", "")
	if a.projectOrganizer.HistorySequence <= old {
		t.Fatal("reopen reused history sequence")
	}
	a.projectOrganizer.Sessions = []records.ProjectSessionSelection{{ID: "new-selection", Digest: "new"}}
	close(release)
	awaitProcessingDispatch(t, dispatch)
	if len(a.projectOrganizer.Sessions) != 1 || a.projectOrganizer.Sessions[0].ID != "new-selection" {
		t.Fatal("old read replaced new scope")
	}
}

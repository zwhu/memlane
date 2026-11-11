package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
	"time"
)

func TestPiProjectCanStartWithoutGrantingHistoricalSessionAccess(t *testing.T) {
	entered := make(chan records.ProjectTaskStart, 1)
	a, _ := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_task_start" {
			entered <- *r.ProjectTaskStart
			return serviceipc.Response{OK: false, Code: "fixture_stop"}
		}
		return serviceipc.Response{OK: false}
	})
	a.projectOrganizer.Binding.Agent = "pi"
	tt := ui.NewTester(a.View, 1200, 820)
	if _, ok := tt.Find("同时整理历史会话"); !ok {
		t.Fatal("history boundary missing")
	}
	_ = tt.Click("同时整理历史会话")
	if a.projectOrganizer.IncludeHistory {
		t.Fatal("pi history was enabled")
	}
	if tt.HasText("当前 Agent 的项目整理暂不可用。") {
		t.Fatal("pi adapter remains blocked")
	}
	if err := tt.Click("使用 pi 整理"); err != nil {
		t.Fatal(err)
	}
	select {
	case draft := <-entered:
		if draft.IncludeHistory || len(draft.Sessions) != 0 || draft.Project != "project" {
			t.Fatal("project or history scope changed", draft)
		}
	case <-time.After(time.Second):
		t.Fatal("pi start not dispatched")
	}
}

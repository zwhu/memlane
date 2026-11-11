package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProjectCreationDoesNotReadAllScopesOrAdoptLateContent(t *testing.T) {
	requests := make(chan serviceipc.Request, 20)
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		requests <- r
		switch r.Op {
		case "scopes":
			return serviceipc.Response{OK: true, Result: []records.Scope{{ID: "alpha", Kind: "project", Name: "Alpha"}}}
		case "list":
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{{Memory: "old", Dataset: "alpha", Title: "旧项目保密正文", Origin: "knowledge"}}, Total: 1}}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
		default:
			return serviceipc.Response{Error: "unexpected " + r.Op}
		}
	})
	a.page, a.scope = "项目", "alpha"
	a.refresh(false)
	var oldReply func()
	select {
	case oldReply = <-updates:
	case <-time.After(5 * time.Second):
		t.Fatal("original content reply absent")
	}
	for len(requests) > 0 {
		<-requests
	}
	a.navigate("项目", "")
	a.detailMode = "project"
	a.projectName, a.projectPath = "新项目", "/synthetic/new"
	a.refresh(false)
	awaitProcessingDispatch(t, updates)
	oldReply()
	close(requests)
	for r := range requests {
		if r.Op != "scopes" {
			t.Fatalf("project creation read content without a scope: %s", r.Op)
		}
	}
	if len(a.items) != 0 || a.selectedID != "" || a.selectedItem != nil || a.listTotal != 0 {
		t.Fatal("late old content entered project creation")
	}
	if len(a.projects) != 1 || a.projectName != "新项目" || a.projectPath != "/synthetic/new" {
		t.Fatal("creation lost project metadata or form draft")
	}
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("旧项目保密正文") || !tt.HasText("填写右侧项目信息，添加后查看本项目的知识与原始记录。") {
		t.Fatal("creation renderer exposed old content or omitted scope explanation")
	}
}

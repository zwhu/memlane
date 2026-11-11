package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestGraphUnavailableDoesNotInventEmptyResultsOrExposeTransport(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		a := graphPresentationApp()
		a.scopes, a.projects = nil, nil
		a.browsing.Error = "dial unix /private/synthetic-secret/service.sock: no such file"
		if stopped {
			a.runtimeStatus.Phase = "stopped"
		}
		tt := ui.NewTester(a.View, 800, 720)
		for _, forbidden := range []string{"0 个", "dial unix /private/synthetic-secret/service.sock: no such file", "请求标识"} {
			if tt.HasText(forbidden) {
				t.Fatalf("unknown/failed material displayed as empty, raw error or cached object: %q", forbidden)
			}
		}
		if !tt.HasText("当前项目（名称待核验） · 当前项目上下文") || !tt.HasText("返回知识") {
			t.Fatal("unread project name removed context or return path")
		}
		if stopped {
			started := false
			a.resumeRuntime = func() { started = true }
			if !tt.HasText("暂不可读") || !tt.HasText("服务已主动停止") {
				t.Fatal("stopped service presented as an ordinary read failure")
			}
			if err := tt.Click("查看服务状态"); err != nil {
				t.Fatal(err)
			}
			if started || a.page != "设置" || a.maintenanceSection != "service" {
				t.Fatal("service action did not preserve explicit startup")
			}
		} else if !tt.HasText("未核验") || !tt.HasText("实体读取失败") {
			t.Fatal("read failure did not retain unknown count")
		}
	}
	a := graphPresentationApp()
	a.browsing.Graph.Entities = nil
	if a.entityCountLabel() != "0 个" {
		t.Fatal("verified actual empty list was replaced by an unknown count")
	}
}

func TestGraphReadReceiptCannotReplaceRestoredLibrary(t *testing.T) {
	project := uuid.NewString()
	a, dispatched := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: records.CogneeGraph{Project: project, State: "empty"}}
	})
	a.page, a.scope = "项目", project
	a.refreshBrowsing()
	a.libraryEpoch++
	a.browsing.Error = "replacement library state"
	a.browsing.Graph = records.CogneeGraph{Project: project, Entities: []records.CogneeEntity{{ID: "replacement"}}}
	awaitProcessingDispatch(t, dispatched)
	if a.browsing.Error != "replacement library state" || len(a.browsing.Graph.Entities) != 1 || a.browsing.Graph.Entities[0].ID != "replacement" {
		t.Fatal("old graph response replaced the restored library state")
	}
}

func TestGraphOriginalMaterialKeepsItsTypeAndReadingRoute(t *testing.T) {
	a := graphPresentationApp()
	a.browsing.Graph.Mentions[0].Origin = "raw"
	a.browsing.Graph.Mentions = append(a.browsing.Graph.Mentions, a.browsing.Graph.Mentions[0])
	a.selectedItem, a.items = nil, nil
	tt := ui.NewTester(a.View, 1280, 820)
	if !tt.HasText("关联知识与原始记录") || !tt.HasText("0 条知识 · 1 条原始记录") || !tt.HasText("阅读原始记录") || tt.HasText("1 条知识") {
		t.Fatal("original material was counted/labeled as reusable knowledge", tt.Texts())
	}
	nodes, _ := a.projectGraphNodes()
	raw := 0
	for _, node := range nodes {
		if node.raw {
			raw++
		}
	}
	if raw != 1 {
		t.Fatal("raw node identity was duplicated or erased", raw)
	}
	a.browseMemory("knowledge") // Synthetic identity retained from the graph fixture.
	if a.recordKind != "raw" || a.selectedID != "knowledge" || a.browsing.View != "memories" || a.browsing.Explorer == nil || a.browsing.Explorer.Revision != 2 {
		t.Fatal("graph source lost its original type/version on navigation")
	}
}

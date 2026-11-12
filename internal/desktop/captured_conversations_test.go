package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestPiMaterialConversationRouteUsesOnlyCapturedReads(t *testing.T) {
	project, cid := uuid.NewString(), uuid.NewString()
	b := records.CogneeBinding{Project: project, Instance: uuid.NewString(), Dataset: uuid.NewString(), ConnectionID: cid, Generation: 1, MaterialSubmission: true}
	row := records.SessionSummary{Session: "saved-pi-session", Dataset: b.Dataset, User: uuid.NewString(), Agent: "pi"}
	reads := 0
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		var value any
		switch r.Op {
		case "cognee_bindings":
			value = []records.CogneeBinding{b}
		case "connections":
			value = []records.Connection{{ID: cid, Agent: "pi", Generation: 1, Enabled: true, AllowedScopes: []string{project}}}
		case "official_sessions", "official_session":
			if r.SessionQuery == nil || !r.SessionQuery.Captured {
				t.Error("pi material route requested history")
			}
			reads++
			if r.Op == "official_sessions" {
				value = records.SessionPage{Sessions: []records.SessionSummary{row}, Limit: r.SessionQuery.Limit, Offset: r.SessionQuery.Offset, Total: 1}
			} else {
				value = records.SessionDetail{SessionSummary: row, QACount: 1, QAs: []records.SessionQA{{ID: "saved", Question: "new captured question", Answer: "actual saved answer"}}}
			}
		default:
			return serviceipc.Response{Code: "unsupported"}
		}
		return serviceipc.Response{OK: true, Result: value}
	})
	a.page, a.scope, a.recordKind, a.detailMode = "项目", project, "raw", "view"
	a.browsing.View = "memories"
	a.openConversations()
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	if reads < 3 || a.conversations.Binding == nil || !a.conversations.Binding.MaterialSubmission || len(a.conversations.Rows) != 1 {
		t.Fatal("captured material route did not reach normal reader", reads)
	}
	view := a.conversations.Views[a.conversations.Selected]
	if view == nil || view.Detail == nil || view.Detail.QAs[0].Question != "new captured question" {
		t.Fatal("captured answer missing")
	}
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		tt := ui.NewTester(a.View, size[0], size[1])
		if !tt.HasText("仅显示已采集并由官方保留的问答与工具记录。") || tt.HasText("仅显示官方已保存的问答与工具记录。当前接口最多返回各自最近 20 条。") {
			t.Fatal("captured route reused historical cache limit")
		}
	}
}

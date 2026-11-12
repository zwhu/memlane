package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestConversationSourceReturnsKnowledgeAndRejectsLateReceipt(t *testing.T) {
	frame := sourceReadingFrame()
	owner := *frame.selectedItem
	owner.Text = strings.Repeat("知识阅读位置保留。\n\n", 40)
	row := records.SessionSummary{Session: "source-session", Dataset: "official-dataset", User: "source-user"}
	result := records.SourceSession{Binding: records.CogneeBinding{Project: owner.Dataset, Instance: "official-instance", Dataset: row.Dataset}, Detail: records.SessionDetail{SessionSummary: row, QACount: 1, QAs: []records.SessionQA{{ID: "qa", Question: "真实关联问题", Answer: "关联回答"}}}, TargetID: "qa", TargetKind: "qa", TargetLoaded: true}
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "official_source_session" {
			return serviceipc.Response{OK: true, Result: result}
		}
		return serviceipc.Response{Code: "service_unavailable", Error: "synthetic"}
	})
	a.page, a.scope, a.recordKind, a.detailMode = "项目", owner.Dataset, "knowledge", "view"
	a.selectedID, a.selectedItem, a.items = owner.ID, &owner, frame.items
	a.browsing.View = "memories"
	tt := ui.NewTester(a.View, 1200, 820)
	a.detailScroll.Y = 80
	tt.Frame()
	if a.detailScroll.Y != 80 {
		t.Fatalf("knowledge fixture is not scrollable: y=%v max=%v", a.detailScroll.Y, a.detailScroll.MaxY)
	}
	a.openConversationSource(owner, records.MergeTarget{Memory: "raw", Revision: 1})
	awaitProcessingDispatch(t, dispatch)
	tt.Frame()
	if !tt.HasText("真实关联问题") || !tt.HasText("返回知识") {
		t.Fatal("source conversation not presented")
	}
	if e := tt.Click("返回知识"); e != nil {
		t.Fatal(e)
	}
	if a.conversationPageVisible() || a.selectedID != owner.ID || a.detailScroll.Y != 80 || a.recordKind != "knowledge" {
		t.Fatalf("knowledge context lost: visible=%v selected=%q kind=%q scroll=%v max=%v", a.conversationPageVisible(), a.selectedID, a.recordKind, a.detailScroll.Y, a.detailScroll.MaxY)
	}
	awaitProcessingDispatch(t, dispatch) // finish the ordinary knowledge-list refresh
	// A queued source refresh must never restore the source route after return.
	a.openConversationSource(owner, records.MergeTarget{Memory: "raw", Revision: 1})
	var late func()
	select {
	case late = <-dispatch:
	case <-time.After(5 * time.Second):
		t.Fatal("source callback absent")
	}
	a.returnFromConversationSource()
	late()
	awaitProcessingDispatch(t, dispatch)
	if a.conversationPageVisible() || a.recordKind != "knowledge" || a.selectedID != owner.ID {
		t.Fatal("late source receipt overwrote knowledge return")
	}
}

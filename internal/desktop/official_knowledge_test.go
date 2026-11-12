package desktop

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOfficialCodingKnowledgeSeparatesBodyFromMetadata(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		d, v := officialKnowledgeTestState(a)
		v.Content.Document.Metadata = json.RawMessage(`{"node_set":["session_learnings","session_learnings:source-session"],"lesson_type":"state","state_key":"inventory-validation","session_id":"source-session","session_date":"2026-11-12","git_commit":"known-commit","git_branch":"main","files":["inventory.py","test_inventory.py"]}`)
		v.Content.SessionReference = "source-session"
		body := "Keep the atomic stock update and affected-row check.\n"
		v.Content.Text = "Lesson-Type: state\nState-Key: inventory-validation\n# Session learning (session source-session)\n\n" + body
		v.Window = newTextWindow(v.Content.Text)
		v.Knowledge.Content, v.Knowledge.Title = *v.Content, "库存校验约定"
		row := v.Content.Document
		d.SelectedRow, d.Rows = &row, []records.OfficialDocument{row}
		tt := ui.NewTester(a.View, width, 620)
		if !tt.HasText("项目状态") || !tt.HasText(strings.TrimSpace(body)) || tt.HasText("Lesson-Type: state") || tt.HasText("inventory-validation") || tt.HasText("known-commit") {
			t.Fatal("default reading mixed raw envelope or metadata with statement")
		}
		if err := tt.Click("复制知识"); err != nil || tt.Clipboard() != body {
			t.Fatal("knowledge copy changed exact statement", err)
		}
		if err := tt.Click("记录信息"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("状态标识") || !tt.HasText("inventory-validation") || !tt.HasText("known-commit") || !tt.HasText("inventory.py\ntest_inventory.py") || !tt.HasText("2026-11-12") || !tt.HasText("阅读对话") {
			t.Fatal("coding metadata or source entry missing")
		}
	}
}

func officialKnowledgeTestState(a *App) (*officialDocumentsUI, *officialDocumentView) {
	row, v := officialDocumentTestState(a)
	a.recordKind = "knowledge"
	d := a.conversations.Documents
	d.Knowledge = true
	d.KnowledgePages = map[int]records.OfficialKnowledgePage{}
	content := *v.Content
	content.SessionReference = "official-knowledge-session"
	content.Text = "# Session learning (session official-knowledge-session)\n\n## Verified guidance\n\n" + strings.Repeat("Preserve atomic updates and affected-row checks.\n\n", 100)
	v.Content = &content
	v.Window = newTextWindow(content.Text)
	v.Knowledge = &records.OfficialKnowledge{Content: content, Title: "Use atomic inventory updates", ProcessingState: "completed"}
	d.KnowledgePages[0] = records.OfficialKnowledgePage{Limit: 20, Items: []records.OfficialKnowledge{*v.Knowledge}}
	d.Rows = []records.OfficialDocument{row}
	return d, v
}

func TestOfficialKnowledgeHeaderRemainsVisibleWhileReading(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		_, view := officialKnowledgeTestState(a)
		view.Knowledge.Title = strings.Repeat("Long official knowledge title ", 8)
		tt := ui.NewTester(a.View, width, 620)
		before, ok := tt.Find("记录信息")
		if !ok {
			t.Fatal("knowledge record information absent")
		}
		view.Scroll.Y = 200
		tt.Frame()
		after, ok := tt.Find("记录信息")
		if !ok || after.Y != before.Y || view.Scroll.Y != 200 {
			t.Fatalf("width=%d knowledge header moved with body: %v -> %v", width, before, after)
		}
	}
}
func TestOfficialKnowledgeNativeShellAndSourceReturn(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var receipt records.OfficialDocumentSession
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op == "official_document_session" {
					return serviceipc.Response{OK: true, Result: receipt}
				}
				return serviceipc.Response{Code: "unsupported"}
			})
			d, v := officialKnowledgeTestState(a)
			receipt = records.OfficialDocumentSession{Document: *d.SelectedRow, Detail: records.SessionDetail{SessionSummary: records.SessionSummary{Session: v.Content.SessionReference, User: "official-user", Dataset: d.SelectedRow.Dataset}, QACount: 1, QAs: []records.SessionQA{{ID: "qa", Question: "Source question", Answer: "Source answer"}}}}
			tt := ui.NewTester(a.View, size[0], size[1])
			if !tt.HasText("知识详情") || !tt.HasText("已处理") || !tt.HasText("Verified guidance") || !tt.HasText("返回项目内容") || tt.HasText("原始文档") {
				t.Fatal("official knowledge shell/body/state missing")
			}
			if err := tt.Click("复制知识"); err != nil || !strings.HasPrefix(tt.Clipboard(), "## Verified guidance") {
				t.Fatal("copy includes wrong envelope/body", err)
			}
			v.Scroll.Y = 120
			d.Query = "atomic"
			tt.Frame()
			if v.Scroll.Y != 120 {
				t.Fatal("fixture not scrollable")
			}
			v.Scroll.Y = 100000
			tt.Frame()
			beforeReturn := v.Scroll.Y
			if beforeReturn <= 0 {
				t.Fatal("source control fixture did not scroll")
			}
			if err := tt.Click("阅读对话"); err != nil {
				t.Fatal(err)
			}
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if !tt.HasText("Source question") || !tt.HasText("返回知识") {
				t.Fatal("source route missing")
			}
			if err := tt.Click("返回知识"); err != nil {
				t.Fatal(err)
			}
			if d.Source != nil || v.Scroll.Y != beforeReturn || d.Query != "atomic" || d.Selected != v.Content.Document.ID {
				t.Fatal("knowledge return lost context")
			}
			v.Knowledge.ProcessingState = "not_completed"
			tt.Frame()
			if !tt.HasText("处理未完成") || tt.HasText("处理失败") {
				t.Fatal("pending falsely reported success/failure")
			}
			if !v.Info {
				v.Scroll.Y = 0
				tt.Frame()
				if err := tt.Click("记录信息"); err != nil {
					t.Fatal(err)
				}
			}
			tt.Frame()
			if !tt.HasText("引擎尚未报告处理完成，当前显示已保存内容") {
				t.Fatal("pending observation boundary absent from record information")
			}
		})
	}
}
func TestOfficialKnowledgeSelectedRefreshAndRevocation(t *testing.T) {
	var response records.OfficialKnowledge
	var code atomic.Value
	code.Store("")
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if failure := code.Load().(string); failure != "" {
			return serviceipc.Response{Code: failure}
		}
		if r.Op != "official_knowledge_text" {
			return serviceipc.Response{Code: "unsupported"}
		}
		return serviceipc.Response{OK: true, Result: response}
	})
	d, v := officialKnowledgeTestState(a)
	response = *v.Knowledge
	window := v.Window
	v.Scroll.Y = 120
	a.refreshSelectedOfficialDocument()
	awaitProcessingDispatch(t, dispatch)
	if v.Window != window || v.Scroll.Y != 120 {
		t.Fatal("unchanged refresh rebuilt knowledge")
	}
	code.Store("timeout")
	a.refreshSelectedOfficialDocument()
	awaitProcessingDispatch(t, dispatch)
	if v.Content == nil || v.Window != window || v.Scroll.Y != 120 {
		t.Fatal("temporary failure dropped valid knowledge")
	}
	code.Store("scope_denied")
	a.refreshSelectedOfficialDocument()
	awaitProcessingDispatch(t, dispatch)
	if len(d.Views) != 0 || len(d.KnowledgePages) != 0 || len(d.Rows) != 0 {
		t.Fatal("revocation retained knowledge cache")
	}
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("Verified guidance") {
		t.Fatal("revoked body displayed")
	}
	_ = tt.Click("复制知识")
	if tt.Clipboard() != "" {
		t.Fatal("revoked body copied")
	}
}

func TestOfficialKnowledgeLateCatalogCannotReviveRevokedContent(t *testing.T) {
	var binding records.CogneeBinding
	var page records.OfficialKnowledgePage
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{binding}}
		case "official_knowledge":
			return serviceipc.Response{OK: true, Result: page}
		default:
			return serviceipc.Response{Code: "scope_denied"}
		}
	})
	d, v := officialKnowledgeTestState(a)
	binding = *a.conversations.Binding
	page = records.OfficialKnowledgePage{Limit: 20, Items: []records.OfficialKnowledge{*v.Knowledge}}
	a.refreshOfficialDocuments(false)
	var late func()
	select {
	case late = <-dispatch:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog did not arrive")
	}
	a.refreshSelectedOfficialDocument()
	awaitProcessingDispatch(t, dispatch)
	late()
	if len(d.Rows) != 0 || len(d.KnowledgePages) != 0 || len(d.Views) != 0 || a.conversations.Binding != nil {
		t.Fatal("late knowledge catalog restored revoked content")
	}
}

func TestOfficialKnowledgeEntryReachableWithEmptyLegacyKnowledge(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		a := New("test", nil)
		conversationTestState(a)
		a.recordKind = "knowledge"
		a.browsing.View = "memories"
		zero := 0
		a.readingCountsRoute = a.page + ":" + a.scope
		a.readingKnowledgeTotal, a.readingRawTotal = &zero, &zero
		if !a.projectKnowledgePreviewVisible() {
			t.Fatal("fixture must use actual empty knowledge guide")
		}
		a.items = nil
		tt := ui.NewTester(a.View, size[0], size[1])
		if !tt.HasText("会话整理结果") {
			t.Fatal("official entry missing from legacy empty/guide shell")
		}
		if err := tt.Click("会话整理结果"); err != nil {
			t.Fatal(err)
		}
		if !a.officialKnowledgeVisible() {
			t.Fatal("entry did not select official knowledge source")
		}
	}
}

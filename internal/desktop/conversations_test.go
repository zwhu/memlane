package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func conversationTestState(a *App) (records.SessionSummary, *conversationReadingState) {
	a.page, a.scope, a.recordKind, a.detailMode = "项目", "synthetic-project", "raw", "view"
	a.browsing.View = "memories"
	a.projects = []ProjectItem{{ID: a.scope, Name: "合成项目"}}
	a.scopes = []records.Scope{{ID: a.scope, Name: "合成项目", Kind: "project"}}
	// Initialize options without starting any IPC.
	life := a.libraryContext
	a.libraryContext = nil
	a.openConversations()
	a.libraryContext = life
	s := &a.conversations
	s.Binding = &records.CogneeBinding{Project: a.scope, Instance: "synthetic-instance", Dataset: "synthetic-dataset"}
	row := records.SessionSummary{Session: "synthetic-session", User: "synthetic-user", Dataset: s.Binding.Dataset}
	s.Loaded, s.Rows = true, []records.SessionSummary{row}
	s.Selected = conversationKey(*s.Binding, row)
	s.SelectedRow = &row
	view := &conversationReadingState{Detail: &records.SessionDetail{SessionSummary: row, QACount: 21, ToolCount: 1,
		QAs:    []records.SessionQA{{ID: "qa", Question: "合成问题", Answer: "**合成回答**\n\n```go\nreturn 42\n```"}},
		Traces: []records.SessionTrace{{ID: "trace", Function: "synthetic tool", Status: "error", Error: "synthetic error"}},
	}}
	s.Views[s.Selected] = view
	return row, view
}

func TestConversationLayoutSeparatesToolsAndLoadedCopyAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		a := New("synthetic", nil)
		_, view := conversationTestState(a)
		tt := ui.NewTester(a.View, size[0], size[1])
		if !tt.HasText("合成问题") || !tt.HasText("合成回答") || !tt.HasText("时间未知") || tt.HasText("synthetic tool") {
			t.Fatal("conversation data or collapsed tools incorrect", size)
		}
		// Search includes loaded answer text; unknown speech times stay unknown.
		a.conversations.Query = "合成回答"
		tt.Frame()
		if !tt.HasText("合成问题") {
			t.Fatal("loaded-answer filter lost matching conversation")
		}
		a.conversations.Query = "absent"
		tt.Frame()
		if !tt.HasText("未找到匹配会话") {
			t.Fatal("search-zero conflated with empty")
		}
		a.conversations.Query = ""
		view.Tools = true
		tt.Frame()
		if !tt.HasText("synthetic tool") || !tt.HasText("失败") {
			t.Fatal("tool outcome missing")
		}
		if e := tt.Click("复制已载入内容"); e != nil {
			t.Fatal(e)
		}
		if tt.Clipboard() != conversationCopy(*view.Detail) {
			t.Fatal("loaded-copy scope incorrect")
		}
	}
}

func TestConversationSingleMessageCopyKeepsOriginalMarkdown(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		_, view := conversationTestState(a)
		tt := ui.NewTester(a.View, width, 620)
		if err := tt.Click("复制用户原文"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != view.Detail.QAs[0].Question {
			t.Fatal("question copy included another message")
		}
		if err := tt.Click("复制Agent原文"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != view.Detail.QAs[0].Answer {
			t.Fatal("answer copy lost Markdown or included another message")
		}
	}
}

func TestConversationShowsOnlyReportedTruncation(t *testing.T) {
	a := New("synthetic", nil)
	_, view := conversationTestState(a)
	view.Tools = true
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("参数 · 已截断") || tt.HasText("问题为已保存的截断片段。") {
		t.Fatal("invented truncation")
	}
	view.Detail.QAs[0].QuestionTruncated = true
	view.Detail.Traces[0].ParamsTruncated = true
	tt.Frame()
	if !tt.HasText("参数 · 已截断") || !tt.HasText("问题为已保存的截断片段。") {
		t.Fatal("reported truncation hidden")
	}
}

func TestConversationReceiptPreservesViewAndWithdrawsRevokedBody(t *testing.T) {
	code := ""
	var result records.SessionDetail
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if code != "" {
			return serviceipc.Response{Code: code, Error: "never show diagnostic"}
		}
		return serviceipc.Response{OK: true, Result: result}
	})
	_, view := conversationTestState(a)
	result = *view.Detail
	view.Tools, view.Metadata, view.Scroll.Y = true, true, 50
	tt := ui.NewTester(a.View, 1200, 820)
	for _, failure := range []string{"", "timeout", "scope_denied"} {
		if failure == "scope_denied" {
			a.conversations.Rows = nil
		} // selected row moved outside the loaded page
		code = failure
		a.refreshSelectedConversation()
		awaitProcessingDispatch(t, dispatch)
		tt.Frame()
		if a.conversations.Views[a.conversations.Selected] != view || !view.Tools || !view.Metadata {
			t.Fatal("receipt replaced reading state")
		}
		if failure == "scope_denied" {
			if view.Detail != nil || tt.HasText("合成回答") {
				t.Fatal("revoked body remains readable")
			}
			_ = tt.Click("复制已载入内容")
			if tt.Clipboard() != "" {
				t.Fatal("revoked content was copied")
			}
		} else if view.Detail == nil || !tt.HasText("合成回答") {
			t.Fatal("valid receipt lost during refresh", failure)
		}
	}
}

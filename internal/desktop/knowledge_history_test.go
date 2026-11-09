package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestKnowledgeHistoryReadsAllPagesAndKeepsCurrentKnowledge(t *testing.T) {
	body := "当前内容\n" + strings.Repeat("当前版本的长正文\n", 120)
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "history" {
			return serviceipc.Response{OK: false, Error: "unexpected page read"}
		}
		if r.BeforeRevision == 0 {
			return serviceipc.Response{OK: true, Result: records.HistoryPage{Items: []records.History{{Revision: 3, Text: "后来保存", Title: "后来版本"}, {Revision: 2, Text: body, Title: "当前标题", Operation: "edit"}}, NextRevision: 2}}
		}
		return serviceipc.Response{OK: true, Result: records.HistoryPage{Items: []records.History{{Revision: 1, Text: "初始内容", Title: "初始标题", Operation: "save"}}}}
	})
	a.selectedItem.Revision, a.selectedItem.Text = 2, body
	a.detailScroll.Y = 120
	a.organization.Selected = "same-running-task"
	selected := a.selectedID
	a.openKnowledgeHistory()
	awaitExperienceDispatch(t, updates)
	if a.knowledgeHistory.Loading || a.knowledgeHistory.Error != "" || len(a.knowledgeHistory.Entries) != 2 || a.knowledgeHistory.Selected != 2 {
		t.Fatal("history page omitted revisions or adopted a later current version", a.knowledgeHistory)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("v1"); err != nil {
		t.Fatal(err)
	}
	if a.knowledgeHistory.Selected != 1 || !tt.HasText("初始内容") || a.selectedID != selected || a.selectedItem.Text != body || a.selectedItem.Revision != 2 || a.detailScroll.Y != 120 || a.organization.Selected != "same-running-task" {
		t.Fatalf("viewing history changed reading: selected=%d current=%d scroll=%v task=%s", a.knowledgeHistory.Selected, a.selectedItem.Revision, a.detailScroll.Y, a.organization.Selected)
	}
	if tt.HasText("确认恢复") || tt.HasText("恢复 v1") {
		t.Fatal("view-only history dialog included a restore action")
	}
	for i := 0; i < 12; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("新增记忆") || tt.Focused("总览") {
			t.Fatal("Tab escaped the dialog")
		}
	}
	tt.Key(0, ui.KeyEscape)
	if a.knowledgeHistory.Open || a.detailScroll.Y != 120 || a.selectedItem.Revision != 2 {
		t.Fatal("Escape did not close while retaining the underlying reading")
	}
}

func TestKnowledgeHistoryRejectsLateReadAndKeepsFailedReadVisible(t *testing.T) {
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: false, Error: "isolated history read failure"}
	})
	a.openKnowledgeHistory()
	awaitExperienceDispatch(t, updates)
	if a.knowledgeHistory.Error == "" || a.knowledgeHistory.Loading {
		t.Fatal("failed read was presented as empty history")
	}
	oldOwner := a.selectedID
	a.loadKnowledgeHistory()
	a.closeKnowledgeHistory()
	a.selectedID = "new-selection"
	awaitExperienceDispatch(t, updates)
	if a.knowledgeHistory.Open || a.selectedID == oldOwner {
		t.Fatal("late read reopened the old knowledge")
	}
}

func TestKnowledgeHistoryLongPreviewClosesByBackdropAndButton(t *testing.T) {
	for _, how := range []string{"button", "backdrop"} {
		t.Run(how, func(t *testing.T) {
			a := New("test", nil)
			a.page = "全部记忆"
			v := MemoryItem{ID: "known", Title: "当前", Revision: 1, Text: "当前正文", Source: "本机用户", State: "active"}
			a.selectedID, a.selectedItem = v.ID, &v
			a.history = []MemoryHistory{{Revision: 1, Text: strings.Repeat("很长的历史正文\n", 200), CreatedAt: time.Now()}}
			tt := ui.NewTester(a.View, 980, 620)
			if err := tt.Click("历史"); err != nil {
				t.Fatal(err)
			}
			if !a.knowledgeHistory.Open {
				t.Fatal("history did not open")
			}
			if how == "button" {
				if err := tt.Click("关闭"); err != nil {
					t.Fatal(err)
				}
			} else {
				tt.ClickAt(10, 10)
			}
			if a.knowledgeHistory.Open || a.selectedItem.Text != "当前正文" || !tt.Focused("历史") {
				t.Fatal("close lost the current text or opening focus")
			}
		})
	}
}

func TestKnowledgeProvenanceNeverTreatsSavedFirstVersionAsOriginal(t *testing.T) {
	a := New("test", nil)
	a.page = "全部记忆"
	v := MemoryItem{ID: "knowledge", Origin: "knowledge", Title: "知识", Text: "当前正文", Original: "最早知识正文不是原文", Source: "用户", State: "active", Revision: 2}
	a.selectedID, a.selectedItem = v.ID, &v
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText(v.Original) || !tt.HasText("这条知识未附完整会话原文。") {
		t.Fatal("saved first version was presented as an original material")
	}
	v.Origin = "raw"
	v.SourceDetail = &records.AgentSource{Native: &records.CodexOriginal{}}
	a.openKnowledgeHistory()
	if a.knowledgeHistory.Open {
		t.Fatal("original bypassed the verified source reader through history")
	}
}

func TestReadingKindsKeepSeparateSelectionsAndProjectQueries(t *testing.T) {
	a := New("test", nil)
	a.page, a.scope, a.recordKind, a.selectedID = "项目", "first", "knowledge", "knowledge-id"
	a.readingSearch, a.readingFilter = "已输入", "已提交"
	a.switchReadingKind("raw")
	if a.selectedID != "" || a.current() != nil {
		t.Fatal("raw tab retained the selected knowledge")
	}
	a.selectedID = "raw-id"
	a.switchReadingKind("knowledge")
	if a.selectedID != "knowledge-id" {
		t.Fatal("returning to knowledge lost its selection")
	}
	a.navigate("项目", "second")
	if a.readingSearch != "" || a.readingFilter != "" {
		t.Fatal("another project inherited the current content filter")
	}
	a.navigate("项目", "first")
	if a.readingSearch != "已输入" || a.readingFilter != "已提交" {
		t.Fatal("returning to a project lost its draft/applied content query")
	}
}

func TestContentFilterReadFailureRetainsPreviousRows(t *testing.T) {
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: false, Error: "isolated content read failure"}
	})
	a.items = []MemoryItem{*a.selectedItem}
	a.visualFixture = false
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("搜索项目内容"); err != nil {
		t.Fatal(err)
	}
	tt.Type("另一条内容")
	tt.Key(0, ui.KeyEnter)
	if len(a.items) != 1 || a.readingFilter != "另一条内容" {
		t.Fatal("pending filter discarded the previous rows")
	}
	awaitExperienceDispatch(t, updates)
	if len(a.items) != 1 || a.libraryError == "" || tt.HasText("没有匹配的内容") {
		t.Fatal("read failure was presented as a successful empty filter")
	}
}

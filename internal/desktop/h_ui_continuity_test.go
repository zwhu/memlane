package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func hConflictFixture() *App {
	a := New("test", nil)
	a.page, a.scope = "项目", "project"
	a.visualFixture = true
	a.browsing.View = "memories"
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "项目"}}
	a.projects = []ProjectItem{{ID: "project", Name: "项目"}}
	item := MemoryItem{ID: "one", Dataset: "project", Title: "原知识", Text: "原正文", Revision: 1, Origin: "knowledge", State: "active"}
	a.items = []MemoryItem{item}
	a.selectedID = item.ID
	a.selectedItem = &item
	a.editing = true
	a.detailMode = "edit"
	a.draftTitle, a.draft, a.draftScope = "我的标题", "草稿独有全文\n"+strings.Repeat("保留我的修改\n", 15), "项目"
	a.drafts[item.ID] = Draft{Revision: 1, Title: a.draftTitle, Text: a.draft, Scope: a.draftScope}
	latest := item
	latest.Revision = 2
	latest.Title = "最新标题"
	latest.Text = "最新正文开头\n" + strings.Repeat("并发写入的新正文\n", 30) + "最新正文尾部不可丢"
	a.conflictCurrent = &latest
	return a
}
func TestH03ComparisonIsReadOnlyUntilExplicitContinue(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 640}} {
		a := hConflictFixture()
		draft := a.draft
		tt := ui.NewTester(a.View, size[0], size[1])
		writeScreenshot(t, tt, "h03-conflict-notice-"+fmtSize(size[0])+".png")
		if err := tt.Click("查看最新正文"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText(a.conflictCurrent.Text) || !tt.HasText(a.draft) || a.drafts["one"].Revision != 1 || a.selectedItem.Revision != 1 || a.draft != draft {
			t.Fatal("comparison truncated latest text or rebound draft")
		}
		if err := tt.Click("返回草稿"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.draft != draft || a.drafts["one"].Revision != 1 || !tt.Focused("查看最新正文") {
			t.Fatal("return lost draft, baseline or focus")
		}
		if err := tt.Click("查看最新正文"); err != nil {
			t.Fatal(err)
		}
		button, ok := tt.Find("确认基于此版本继续编辑")
		if !ok || button.Y+button.H > float32(size[1]) {
			t.Fatal("comparison footer offscreen", button)
		}
		writeScreenshot(t, tt, "h03-comparison-"+fmtSize(size[0])+".png")
		a.conflictReview.Scroll.Y = a.conflictReview.Scroll.MaxY
		tt.Frame()
		copyButton, ok := tt.Find("复制最新全文")
		if !ok || copyButton.Y+copyButton.H > float32(size[1])-40 {
			t.Fatal("latest full text bottom inaccessible", copyButton)
		}
		if err := tt.Click("复制最新全文"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != a.conflictCurrent.Text {
			t.Fatal("latest copy truncated body")
		}
		writeScreenshot(t, tt, "h03-comparison-bottom-"+fmtSize(size[0])+".png")

		if err := tt.Click("确认基于此版本继续编辑"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.Focused("记忆内容") {
			t.Fatal("explicit continue did not return focus to original draft")
		}
		if a.draft != draft || a.drafts["one"].Revision != 2 || a.selectedItem.Revision != 2 || a.conflictCurrent != nil {
			t.Fatal("explicit continue did not preserve draft and change only baseline")
		}
	}
}
func fmtSize(n int) string {
	if n == 800 {
		return "800"
	}
	return "1200"
}
func TestH03ComparisonCannotRebindAfterLibraryOrLatestChanges(t *testing.T) {
	for _, change := range []string{"epoch", "revision", "selection"} {
		a := hConflictFixture()
		a.openConflictReview()
		s := a.conflictReview
		switch change {
		case "epoch":
			a.libraryEpoch++
		case "revision":
			a.conflictCurrent.Revision = 3
		case "selection":
			a.selectedID = "another"
		}
		if a.conflictReviewValid(s) {
			t.Fatal("stale comparison still authorized continue", change)
		}
		tt := ui.NewTester(a.View, 800, 640)
		if tt.HasText("确认基于此版本继续编辑") || a.drafts["one"].Revision != 1 {
			t.Fatal("stale viewer persisted", change)
		}
	}
}
func TestH04IndexFailureIsFirstScreenAndRetryOnlyUsesIndexIPC(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 640}} {
		a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
			if r.Op != "retry_index" {
				t.Errorf("retry performed %s", r.Op)
			}
			return serviceipc.Response{OK: false, Error: "synthetic retry failure"}
		})
		a.visualFixture = true
		a.browsing.View = "memories"
		a.page, a.scope = "项目", "project"
		item := MemoryItem{ID: "saved", Dataset: "project", Title: "已保存标题", Text: strings.Repeat("已保存正文不回滚\n", 80), Revision: 3, Origin: "knowledge", State: "active", IndexState: "failed"}
		a.selectedID = item.ID
		a.selectedItem = &item
		a.items = []MemoryItem{item}
		a.onSave = func() { t.Fatal("index retry called knowledge save") }
		tt := ui.NewTester(a.View, size[0], size[1])
		card, ok := tt.Find("正文已保存，索引失败")
		if !ok || card.Y > 160 || a.readingMetadataOpen {
			t.Fatal("index failure not visible before collapsed metadata", card)
		}
		button, ok := tt.Find("重试索引")
		if !ok || button.Y+button.H > float32(size[1]) {
			t.Fatal("index retry not reachable", button)
		}
		writeScreenshot(t, tt, "h04-index-failed-"+fmtSize(size[0])+".png")
		if err := tt.Click("重试索引"); err != nil {
			t.Fatal(err)
		}
		awaitProcessingDispatch(t, updates)
		tt.Frame()
		if !strings.Contains(a.message, "索引重试失败") || a.selectedItem.Text != item.Text || a.selectedItem.Revision != 3 || a.saving {
			t.Fatal("retry rewrote or lost saved content")
		}
	}
}
func TestH04FailureCardExcludesArchivedRawAndStaleDerived(t *testing.T) {
	base := MemoryItem{Revision: 2, State: "active", Origin: "knowledge", IndexState: "failed"}
	for _, kind := range []string{"raw", "archived", "deleted", "indexed", "stale"} {
		v := base
		switch kind {
		case "raw":
			v.Origin = "raw"
		case "archived", "deleted":
			v.State = kind
		case "indexed":
			v.IndexState = "indexed"
		case "stale":
			v.Derived = &records.DerivedInfo{State: "stale"}
		}
		if hasFailedIndexNotice(v) {
			t.Fatal("failure card applied to unavailable knowledge", kind)
		}
	}
}

func TestH03RealIPCConflictPreviewDoesNotResubmitOrChangeExpectedRevision(t *testing.T) {
	calls := 0
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "apply":
			calls++
			if r.Mutation.ExpectedRevision != 1 {
				t.Error("initial baseline changed")
			}
			return serviceipc.Response{Code: "conflict", Error: "concurrent write"}
		case "get":
			return serviceipc.Response{OK: true, Result: records.Version{Memory: "one", Dataset: "project", Revision: 2, State: "active", Origin: "knowledge", Text: "真实IPC最新正文\n完整尾部"}}
		default:
			return serviceipc.Response{OK: false, Error: "unexpected"}
		}
	})
	frame := hConflictFixture()
	a.visualFixture = true
	a.browsing.View = "memories"
	a.page, a.scope, a.detailMode = frame.page, frame.scope, frame.detailMode
	a.editing = true
	a.items = frame.items
	a.selectedItem = frame.selectedItem
	a.selectedID = frame.selectedID
	a.draft = frame.draft
	a.draftTitle = frame.draftTitle
	a.draftScope = frame.draftScope
	a.drafts = frame.drafts
	a.applyMutation("one", records.Mutation{Kind: "edit", Memory: "one", Dataset: "project", ExpectedRevision: 1, Text: a.draft})
	awaitProcessingDispatch(t, updates)
	a.openConflictReview()
	if a.conflictReview == nil || a.drafts["one"].Revision != 1 || calls != 1 {
		t.Fatal("conflict preview resubmitted or rebound")
	}
	a.conflictReview.Open = false
	if a.conflictCurrent == nil || a.drafts["one"].Revision != 1 || calls != 1 {
		t.Fatal("leaving preview changed expected version")
	}
}

func TestH03LongLatestHasBoundedPagesAndExactFullCopy(t *testing.T) {
	a := hConflictFixture()
	a.draft = "短草稿保留"
	a.conflictCurrent.Text = strings.Repeat("长最新正文包含完整汉字。", 1000) + "精确末尾"
	a.openConflictReview()
	tt := ui.NewTester(a.View, 800, 640)
	key := "conflict-latest-one-2"
	w := a.textWindows[key]
	if w == nil || len(w.Offsets) < 3 {
		t.Fatal("large latest body was not safely paged")
	}
	rebuilt := ""
	for n := 0; n < len(w.Offsets)-1; n++ {
		rebuilt += w.Body[w.Offsets[n]:w.Offsets[n+1]]
	}
	if rebuilt != a.conflictCurrent.Text || a.drafts["one"].Revision != 1 {
		t.Fatal("bounded preview lost bytes or changed baseline")
	}
	a.conflictReview.Scroll.Y = a.conflictReview.Scroll.MaxY
	tt.Frame()
	if err := tt.Click("复制最新全文"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != a.conflictCurrent.Text {
		t.Fatal("large latest copy truncated")
	}
	writeScreenshot(t, tt, "h03-long-latest-800-bottom.png")
}

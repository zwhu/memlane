package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

// The normal detail/history/editor must not bypass the reader's current
// authorization and source-version checks for official original records.
func TestOfficialOriginalRequiresVerifiedReaderInStandardPanes(t *testing.T) {
	for _, detail := range []*records.AgentSource{{Official: &records.CogneeOriginal{}}, {Native: &records.CodexOriginal{}}} {
		for _, mode := range []string{"view", "history", "restore"} {
			a := New("test", nil)
			item := MemoryItem{ID: "synthetic-original", Origin: "raw", Title: "Original", Text: "unverified original body", Revision: 1, SourceDetail: detail}
			a.selectedID, a.selectedItem, a.detailMode = item.ID, &item, mode
			a.history = []MemoryHistory{{Revision: 1, Text: item.Text}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Children(func() { a.memoryRow(c, item, true); a.memoryDetail(c) })
			}, 1200, 820)
			if tt.HasText(item.Text) {
				t.Fatal("unverified original escaped reader checks")
			}
			a.editMemory()
			if a.editing {
				t.Fatal("original body opened in unverified editor")
			}
		}
	}
}

func sourceReadingFrame() *App {
	a := New("test", nil)
	a.page, a.scope, a.browsing.View = "项目", "project", "memories"
	a.projects = []ProjectItem{{ID: "project", Name: "当前项目", Path: "/repository"}}
	owner := MemoryItem{ID: "knowledge", Dataset: "project", Title: "所选知识", Text: strings.Repeat("知识阅读位置保留。\n", 40), Revision: 2, Origin: "knowledge", State: "active", Source: "codex"}
	a.selectedID, a.selectedItem = owner.ID, &owner
	a.items = []MemoryItem{owner, {ID: "other", Dataset: owner.Dataset, Title: "另一条知识", Origin: "knowledge", State: "active"}}
	original := records.Version{Memory: "raw", Dataset: owner.Dataset, Title: "独立原始材料", Text: strings.Repeat("原文内容与知识正文独立。\n", 60), Revision: 1, Origin: "raw", Source: "codex"}
	source := records.OrganizationSource{Memory: "raw", Project: owner.Dataset, Revision: 1, Input: records.OrganizationInput{Memory: "raw", Revision: 1, Title: original.Title, Text: original.Text, Origin: "raw", Source: "codex"}}
	a.organizationSourcesFor, a.organizationSourcesRevision, a.organizationSources = owner.ID, owner.Revision, []records.OrganizationSource{source}
	a.openSourceReader(owner, records.MergeTarget{Memory: source.Memory, Revision: source.Revision})
	a.reader.Result = records.SourceRecord{State: "available", Record: original, QuoteOffset: -1}
	a.reader.Window = newTextWindow(source.Input.Text)
	return a
}

func TestSourceReaderKeepsKnowledgeListAndReturnsPositionAndFocus(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {980, 620}} {
		a := sourceReadingFrame()
		a.detailScroll.Y = 80
		tt := ui.NewTester(a.View, size[0], size[1])
		if !tt.HasText("另一条知识") || !tt.HasText(a.reader.Window.Body) || !tt.HasText("原始出处") {
			t.Fatal("reader replaced knowledge list or omitted independent original")
		}
		if err := tt.Click("返回知识"); err != nil {
			t.Fatal(err)
		}
		if a.reader.Open || a.selectedID != "knowledge" || a.detailScroll.Y != 80 || !tt.Focused("阅读原文") {
			t.Fatal("return lost selection, scroll or source-entry focus")
		}
	}
}

func TestSourceReaderCheckingAndChangedSourcesDoNotExposeOldText(t *testing.T) {
	for _, state := range []string{"checking", "source_changed", "owner_changed"} {
		a := sourceReadingFrame()
		old := a.reader.Window.Body
		if state == "checking" {
			a.reader.Loading = true
		} else {
			a.reader.Result.State = state
		}
		tt := ui.NewTester(a.View, 980, 620)
		if tt.HasText(old) {
			t.Fatal("unverified original retained readable/copyable text", state)
		}
		_ = tt.Click("复制全文")
		if tt.Clipboard() != "" {
			t.Fatal("unverified original retained copy action", state)
		}
		if !tt.HasText("另一条知识") {
			t.Fatal("source failure discarded underlying knowledge list")
		}
	}
}

func TestUnavailableCompleteSessionRequiresExplicitSegmentChoice(t *testing.T) {
	a := sourceReadingFrame()
	segment := a.reader.Window.Body
	a.reader.HadSessionOriginal, a.reader.Full = true, true
	a.reader.Result.OriginalState = "upstream_changed"
	tt := ui.NewTester(a.View, 980, 620)
	if tt.HasText(segment) {
		t.Fatal("missing complete session silently became a segment")
	}
	_ = tt.Click("复制全文")
	if tt.Clipboard() != "" {
		t.Fatal("missing complete session was copyable")
	}
	if err := tt.Click("引用片段"); err != nil {
		t.Fatal(err)
	}
	if a.reader.Full || !tt.HasText(segment) {
		t.Fatal("explicit segment choice did not show verified segment")
	}
}

func TestSourceVerificationKeepsOriginalScrollPosition(t *testing.T) {
	a := sourceReadingFrame()
	a.reader.Scroll.Y = 90
	tt := ui.NewTester(a.View, 1200, 820)
	before := a.reader.Scroll.Y
	a.reader.Loading = true
	tt.Frame()
	if a.reader.Scroll.Y != before {
		t.Fatal("verification status clamped original scroll position")
	}
	a.reader.Loading = false
	tt.Frame()
	if a.reader.Scroll.Y != before {
		t.Fatal("verification return lost original scroll position")
	}
}

func TestSelectingAnotherSourceClearsPreviousSearchLocation(t *testing.T) {
	a := sourceReadingFrame()
	other := records.OrganizationSource{Memory: "raw-other", Project: "project", Revision: 3,
		Input: records.OrganizationInput{Title: "第二份原始材料", Origin: "raw"}}
	a.reader.Sources = append(a.reader.Sources, other)
	a.reader.Search, a.reader.Matches, a.reader.Match = "旧查询", []int{12, 40}, 1
	a.reader.FocusOffset, a.reader.FocusLength, a.reader.Reveal, a.reader.Scroll.Y = 40, 9, true, 90
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click(other.Input.Title); err != nil {
		t.Fatal(err)
	}
	s := &a.reader
	if s.Query.Owner != "knowledge" || s.Query.Memory != other.Memory || s.Query.Revision != other.Revision {
		t.Fatal("source switch lost owner or selected source version")
	}
	if s.Search != "" || len(s.Matches) != 0 || s.Match != 0 || s.FocusOffset != 0 || s.FocusLength != 0 || s.Reveal || s.Scroll.Y != 0 {
		t.Fatal("previous source search, highlight or scroll survived source switch")
	}
}

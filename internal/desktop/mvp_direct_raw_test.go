package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestRawTabReaderReturnRestoresKnowledgeSelectionWithoutChangingOriginal(t *testing.T) {
	a := New("test", nil)
	a.page, a.scope, a.recordKind, a.selectedID = "项目", "project", "raw", "original"
	a.readingSelections = map[string]string{"项目:project:knowledge": "knowledge"}
	a.selectedItem = &MemoryItem{ID: "original", Origin: "raw", Text: "unchanged original", Revision: 2}
	a.reader = SourceReaderUI{Open: true, DirectRaw: true, Query: records.SourceRead{Owner: "original", Memory: "original"}}
	if a.sourceReaderReturnLabel() != "返回知识" {
		t.Fatal("raw tab does not expose the designed return")
	}
	a.returnFromSourceReader()
	if a.reader.Open || a.recordKind != "knowledge" || a.selectedID != "knowledge" {
		t.Fatal("raw reader did not return to the retained knowledge selection")
	}
	a.reader = SourceReaderUI{Query: records.SourceRead{Owner: "original", Memory: "original"}}
	if a.sourceReaderReturnLabel() != "返回原始记录" {
		t.Fatal("standalone original reader return changed")
	}
}

func TestRawTabReaderShowsVerifiedVersionAndCopyFooter(t *testing.T) {
	a := sourceReadingFrame()
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "当前项目"}}
	a.reader.DirectRaw = true
	a.reader.Sources = nil
	a.reader.Result.Record.State = "active"
	tt := ui.NewTester(a.View, 1220, 748)
	if !tt.HasText("当前项目 · codex · v1 · 当前有效") || !tt.HasText("当前项目 · 原始材料") || !tt.HasText("复制正文") {
		t.Fatal("raw tab lost its verified project, source, version or designed footer")
	}
	a.reader.Loading = true
	tt.Frame()
	if !tt.HasText("当前项目 · codex · v1 · 当前有效") {
		t.Fatal("后台刷新期间丢失已核验元数据")
	}
}

package simpledesktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUntitledKnowledgeRendersOnlyOriginalBody(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	d := lessonDocument()
	body := "internal/simpledesktop/tool_graph_ui.go 的创建弹框已精简文案并移除重新核对范围按钮，待处理数量仅在打开弹框时读取，无手动刷新入口。"
	raw := "Lesson-Type: pitfall\nState-Key: k\n# Session learning (session s)\n\n" + body
	i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: body, Document: &d, Body: raw, BodyLoaded: true}
	s.Selected = &i
	tt := ui.NewTester(func(c *ui.Context) { a.projectDetail(c, s) }, 1200, 820)
	if projectDisplayTitle(i) != "" || tt.HasText("展开完整标题") || tt.HasText("原文") {
		t.Fatal("body-derived title still rendered")
	}
	if i.Body != raw || itemPresentation(i).VisibleBody != body {
		t.Fatal("original knowledge content changed")
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "tool_graph_ui.go") {
		t.Fatal("knowledge body disappeared")
	}
	label := "真实独立标题"
	i.Document.Label = &label
	if projectDisplayTitle(i) != label {
		t.Fatal("official title was removed")
	}
}

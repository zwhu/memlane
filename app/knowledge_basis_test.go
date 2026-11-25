package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestKnowledgeBasisRecordedDataOnly(t *testing.T) {
	d := Document{Metadata: map[string]any{"node_set": []any{"session_learnings"}, "lesson_type": "state", "state_key": "test-baseline", "why_learned": "While fixing retry_limit=0."}}
	i := Item{Kind: "知识", Document: &d}
	fields := knowledgeBasisFields(i)
	if len(fields) != 4 || fields[0].Value != "项目状态" || fields[2].Value != "While fixing retry_limit=0." || fields[3].Value != "未记录" {
		t.Fatal(fields)
	}
	delete(d.Metadata, "why_learned")
	d.Metadata["lesson_type"] = "pitfall"
	fields = knowledgeBasisFields(i)
	if len(fields) != 3 || fields[0].Value != "踩坑经验" || fields[1].Value != "未记录" {
		t.Fatal("stale state key or missing context fabricated a reason", fields)
	}
	d.Metadata["lesson_type"] = "future-kind"
	if fields = knowledgeBasisFields(i); fields[0].Value != "未识别分类（future-kind）" {
		t.Fatal(fields)
	}
	i.Kind = "原始记录"
	if knowledgeBasisFields(i) != nil {
		t.Fatal("raw record acquired knowledge classification")
	}
}

func TestKnowledgeBasisReader(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	s.Knowledge.Detail = true
	i := s.Selected
	i.Document.Metadata["node_set"] = []any{"session_learnings", "session_learnings:s"}
	i.Document.Metadata["lesson_type"] = "state"
	i.Document.Metadata["state_key"] = "test-baseline"
	i.Document.Name = ""
	i.Title = "python3 -m unittest -v passes 4 tests with 0 failures"
	i.Body = i.Title
	s.Items = []Item{*i}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, want := range []string{"记忆点", "项目状态", "测试基线：", "学习情境、接受理由未记录", "查看来源会话"} {
		if !tt.HasText(want) {
			t.Fatal("reader missing", want)
		}
	}
	if tt.HasText("测试结果：") || projectDisplayTitle(*i) != "" || i.Body != i.Title {
		t.Fatal("source statement was rewritten")
	}
	fixtureImage(t, tt, "knowledge-basis")
}

package desktop

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestConversationTransientFeedbackPreservesContentGeometry(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		_, view := conversationTestState(a)
		view.Detail.Label = "Synthetic session title"
		view.Detail.QAs[0].Answer = "READING_ANCHOR\n\n" + strings.Repeat("Long reading paragraph.\n\n", 30)
		tt := ui.NewTester(a.View, width, 820)
		view.Scroll.Y = 80
		tt.Frame()
		anchor, ok := tt.Find("READING_ANCHOR")
		if !ok {
			t.Fatal("missing reading anchor")
		}
		row, ok := tt.Find("Synthetic session title")
		if !ok {
			t.Fatal("missing selected row")
		}
		view.ErrorCode = "timeout"
		a.conversations.ErrorCode = "cognee_unavailable"
		tt.Frame()
		changed, ok := tt.Find("READING_ANCHOR")
		if !ok {
			t.Fatal("transient failure withdrew verified body")
		}
		shifted, ok := tt.Find("Synthetic session title")
		if !ok {
			t.Fatal("transient failure withdrew selected row")
		}
		if changed.Y != anchor.Y || shifted.Y != row.Y || view.Scroll.Y != 80 {
			t.Fatalf("width=%d transient feedback moved content: body %v -> %v, row %v -> %v, scroll=%v", width, anchor.Y, changed.Y, row.Y, shifted.Y, view.Scroll.Y)
		}
	}
}

func TestConversationFailureKeepsCountSeparateFromActions(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		_, view := conversationTestState(a)
		tt := ui.NewTester(a.View, width, 620)
		for _, label := range []string{"已载入 1 个会话", "已载入 1 组问答 · 1 条工具记录"} {
			before, ok := tt.Find(label)
			if !ok {
				t.Fatal("missing count", label)
			}
			view.ErrorCode, a.conversations.ErrorCode = "timeout", "timeout"
			tt.Frame()
			after, ok := tt.Find(label)
			if !ok || before.X != after.X || before.Y != after.Y || before.H != after.H ||
				(width < 1000 && before.W != after.W) || (width >= 1000 && after.W < 90) {
				t.Fatalf("width=%d count changed geometry: %v -> %v (%v)", width, before, after, ok)
			}
			view.ErrorCode, a.conversations.ErrorCode = "", ""
			tt.Frame()
		}
	}
}

func TestConversationRevokedScopeDoesNotPointToLegacyMaterials(t *testing.T) {
	a := New("synthetic", nil)
	conversationTestState(a)
	s := &a.conversations
	s.Binding, s.Rows, s.Selected = nil, nil, ""
	s.ErrorCode = "scope_denied"
	tt := ui.NewTester(a.View, 800, 620)
	if !tt.HasText("读取权限已变化") || !tt.HasText("已保存的内容未删除，重新确认授权后可继续查看。") {
		t.Fatal("revocation did not explain authorization and retained content")
	}
	if tt.HasText("已有材料保留在旧材料入口") || tt.HasText("重试会话列表") {
		t.Fatal("revocation misleadingly points to legacy content or network retry")
	}
}

func TestConversationReadingHeaderStaysOutsideMessageScroll(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		_, view := conversationTestState(a)
		view.Detail.Label = "FIXED_READING_TITLE"
		view.Detail.QAs[0].Answer = strings.Repeat("A long paragraph in the conversation.\n\n", 80)
		tt := ui.NewTester(a.View, width, 620)
		before, ok := tt.Find("记录信息")
		if !ok {
			t.Fatal("title absent")
		}
		view.Scroll.Y = 200
		tt.Frame()
		after, ok := tt.Find("记录信息")
		if !ok || after.Y != before.Y || view.Scroll.Y != 200 {
			t.Fatalf("width=%d reading title moved with message scroll: %v -> %v (%v)", width, before, after, ok)
		}
	}
}

func TestConversationToolHeadingStartsAtTheLeft(t *testing.T) {
	for _, width := range []int{800, 1200} {
		a := New("synthetic", nil)
		conversationTestState(a)
		tt := ui.NewTester(a.View, width, 820)
		button, ok := tt.Find("展开本会话工具记录 · 1 条")
		if !ok {
			t.Fatal("toggle absent")
		}
		label, ok := tt.Find("本会话工具记录 · 1 条")
		if !ok || label.X-button.X > 64 || label.X <= button.X {
			t.Fatalf("width=%d tool text centered or clipped: button=%v label=%v", width, button, label)
		}
	}
}

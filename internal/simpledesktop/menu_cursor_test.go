package simpledesktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNavigationEntriesShowPointerCursor(t *testing.T) {
	a := settingsFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"总览", "通用记忆", "设置", "新建项目", "搜索记忆与原始记录"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatal("missing navigation entry", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		if tt.Cursor() != ui.CursorPointer {
			t.Fatalf("%s has cursor %v instead of pointer", label, tt.Cursor())
		}
	}
}

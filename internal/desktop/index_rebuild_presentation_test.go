package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestIndexRecoveryFooterKeepsCancelledVersionsDistinctAndActionsVisible(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "设置"
	a.maintenanceSection = "index"
	a.libraryContext = context.Background()
	a.runtimeStatus.Phase = "ready"
	a.indexRebuild = records.IndexRebuild{ID: "cancelled-rebuild", State: "cancelled", Items: []records.IndexRebuildItem{
		{Memory: "11111111-1111-4111-8111-111111111111", Revision: 1, State: "completed"},
		{Memory: "22222222-2222-4222-8222-222222222222", Revision: 2, State: "failed", Error: "rebuild_cancelled"},
	}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, size := range [][2]int{{1200, 820}, {800, 720}} {
		tt.SetSize(size[0], size[1])
		if !tt.HasText("共 2 条 · 完成 1 · 等待 0 · 失败 0 · 过期 0 · 已取消 1") {
			t.Fatal("cancelled work counted as an indexing failure")
		}
		for _, label := range []string{"重试未完成项", "重建其他范围"} {
			n, ok := tt.Find(label)
			if !ok || n.X < 0 || n.X+n.W > float32(size[0]) || n.Y+n.H > float32(size[1]) || n.Y < float32(size[1]-64) {
				t.Fatalf("footer action outside fixed footer at%v:%s %#v", size, label, n)
			}
		}
	}
	if err := tt.Click("重建其他范围"); err != nil {
		t.Fatal(err)
	}
	if !a.indexRebuildConfirm || a.indexRebuild.ID != "cancelled-rebuild" {
		t.Fatal("new scope discarded previous receipt or failed to require confirmation")
	}
}

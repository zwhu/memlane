package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestUpdateCancellationWaitsForReceiptAndFooterStaysVisible(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "设置"
	a.maintenanceSection = "updates"
	a.SetUpdateStatus("正在下载更新…", true)
	requested := 0
	a.SetCancelUpdate(func() {
		requested++
		a.SetCancelUpdate(nil)
		a.SetUpdateStatus("正在取消更新，等待回执…", true)
	})
	tt := ui.NewTester(a.View, 1200, 820)
	for _, size := range [][2]int{{1200, 820}, {800, 720}} {
		tt.SetSize(size[0], size[1])
		n, ok := tt.Find("取消更新")
		if !ok || n.X < 0 || n.X+n.W > float32(size[0]) || n.Y+n.H > float32(size[1]) || n.Y < float32(size[1]-64) {
			t.Fatalf("update action outside footer %v %#v", size, n)
		}
	}
	if err := tt.Click("取消更新"); err != nil {
		t.Fatal(err)
	}
	if requested != 1 || !a.checking || tt.HasText("更新已取消，当前安装与知识保留。") {
		t.Fatal("request was presented as cancellation receipt")
	}
	a.SetUpdateStatus("正在校验并安装更新…", true)
	tt.SetSize(800, 720)
	if tt.HasText("取消更新") || !tt.HasText("等待更新回执") {
		t.Fatal("installation publication exposed unsupported cancellation")
	}
}

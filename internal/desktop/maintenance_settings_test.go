package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestMaintenanceServiceStopWaitsForConfirmationAndReceipt(t *testing.T) {
	a := New("test", nil)
	a.page, a.maintenanceSection = "设置", "service"
	a.runtimeStatus = RuntimeStatus{Phase: "ready"}
	a.serviceState = "ready"
	calls := 0
	a.stopRuntime = func() {
		calls++
		a.SetRuntimeStatus(RuntimeStatus{Phase: "preparing", Message: "正在停止记忆服务…"})
	}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, width := range []int{1200, 800} {
		tt.SetSize(width, 820)
		node, ok := tt.Find("停止服务")
		if !ok || node.X < 0 || node.X+node.W > float32(width) || node.Y+node.H > 820 {
			t.Fatalf("service action outside maintenance pane at %d: %#v", width, node)
		}
		if node.Y < 736 {
			t.Fatalf("service action left fixed footer at %d: %#v", width, node)
		}
		heading, found := tt.Find("本机后台服务")
		if !found || heading.Y > 126 {
			t.Fatalf("maintenance content vertically centered at %d: %#v", width, heading)
		}
		writeScreenshot(t, tt, fmt.Sprintf("maintenance-service-%d.png", width))
	}
	if err := tt.Click("停止服务"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || !tt.HasText("停止后，记忆读写会暂停") {
		t.Fatal("stop ran before confirmation")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("cancel stopped service")
	}
	if err := tt.Click("停止服务"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("确认停止服务"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || tt.HasText("已主动停止") {
		t.Fatal("service was reported stopped without receipt")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "stopped"})
	tt.SetSize(1200, 820)
	if !tt.HasText("已主动停止") {
		t.Fatal("stop receipt missing")
	}
}

package desktop

// GUI/IPC read fault fixture. Actual plan App/model acceptance is separate.
import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestScheduleUIReportsStoppedLoopWhenDatabaseReadFails(t *testing.T) {
	message := "计划连续触发失败，调度已停止；存储恢复后请明确重新启动服务。"
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "health" {
			return serviceipc.Response{OK: true, Result: map[string]string{"state": "ready", "schedule_error": message}}
		}
		return serviceipc.Response{OK: false, Code: "service_error", Error: "database read fault fixture"}
	})
	a.visualFixture = false
	a.refresh(false)
	awaitExperienceDispatch(t, dispatch)
	if a.schedules.RuntimeError != message || a.libraryError == "" {
		t.Fatal("database read failure hid independently returned plan diagnostic")
	}
	a.page = "整理计划"
	tt := ui.NewTester(a.View, 1200, 900)
	if !tt.HasText(message) {
		t.Fatal("plan stopped-loop diagnostic not rendered")
	}
}

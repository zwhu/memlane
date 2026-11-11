package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func schedulePresentationApp() *App {
	a := New("test", nil)
	a.page = "整理计划"
	a.visualFixture = true
	a.scopes = []records.Scope{{ID: "project", Name: "记忆桌面端", Kind: "project"}}
	a.schedules = ScheduleUI{Selected: "plan", Loaded: true, Destinations: map[string]bool{}, Plans: []records.Schedule{{ID: "plan", Generation: 2, Spec: records.ScheduleSpec{Project: "project", Executor: "processing", Frequency: "daily", LocalTime: "09:00", Timezone: "Asia/Shanghai", MaterialLimit: 8, WindowMinutes: 30}}}}
	return a
}

func TestSchedulePresentationEnableNeedsSeparateConfirmation(t *testing.T) {
	a := schedulePresentationApp()
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("启用计划"); err != nil {
		t.Fatal(err)
	}
	if a.currentSchedule().Enabled || a.schedules.Busy || a.schedules.Requested != nil {
		t.Fatal("opening confirmation enabled or submitted plan")
	}
	if !tt.HasText("确认后才开始计时") || !tt.HasText("首次启用只计算之后的下一次运行") || a.schedules.ConfirmPlan == nil {
		t.Fatal("scope and cost confirmation missing")
	}
	frozen := a.schedules.ConfirmPlan
	a.schedules.Plans[0].Generation = 3
	a.schedules.Plans[0].Spec.MaterialLimit = 16
	tt.Frame()
	if frozen.Generation != 2 || frozen.Spec.MaterialLimit != 8 || !tt.HasText("计划已更新") {
		t.Fatal("confirmation silently adopted changed policy")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.schedules.ConfirmAction != "" || a.schedules.ConfirmPlan != nil || a.currentSchedule().Enabled {
		t.Fatal("cancel changed enabled policy or kept confirmation")
	}
}

func TestSchedulePresentationNewDraftSurvivesReturnAndSelection(t *testing.T) {
	a := schedulePresentationApp()
	a.newSchedule()
	id := a.schedules.Draft.ID
	if a.schedules.Frequency != "" || a.schedules.Window != "" || a.schedules.Limit != "" || a.schedules.Draft.Spec.LocalTime != "" || a.schedules.Draft.Enabled {
		t.Fatal("new plan fabricated timing or material authorization")
	}
	a.schedules.Frequency, a.schedules.ProjectLabel, a.schedules.Window, a.schedules.Limit = "每周", "记忆桌面端", "30", "8"
	a.schedules.Draft.Spec.LocalTime = "09:30"
	a.schedules.Destinations["other"] = true
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("保留草稿返回"); err != nil {
		t.Fatal(err)
	}
	a.selectSchedule("plan")
	a.newSchedule()
	if a.schedules.Draft.ID != id || a.schedules.Frequency != "每周" || a.schedules.Draft.Spec.LocalTime != "09:30" || !a.schedules.Destinations["other"] {
		t.Fatal("return recreated plan or lost draft scope")
	}
	if a.currentSchedule() != nil || a.schedules.Draft.Enabled {
		t.Fatal("draft entry activated another plan")
	}
}

func TestSchedulePresentationControlsFitOrdinaryAndNarrowWindows(t *testing.T) {
	for _, width := range []int{1280, 800} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := schedulePresentationApp()
			tt := ui.NewTester(a.View, width, 720)
			within := func(label string) {
				t.Helper()
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > 721 {
					t.Fatalf("%q outside window: %+v found=%v", label, r, ok)
				}
			}
			within("启用计划")
			writeScreenshot(t, tt, fmt.Sprintf("schedule-%d-detail.png", width))
			if err := tt.Click("启用计划"); err != nil {
				t.Fatal(err)
			}
			within("明确启用计划")
			within("取消")
			writeScreenshot(t, tt, fmt.Sprintf("schedule-%d-enable.png", width))
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			a.newSchedule()
			tt.Frame()
			within("保存计划配置")
			within("保留草稿返回")
			writeScreenshot(t, tt, fmt.Sprintf("schedule-%d-editor.png", width))
			tt.Scroll(float32(width-80), 350, 0, 600)
			within("保存计划配置")
		})
	}
}

func TestSchedulePresentationInvalidTimezoneDoesNotCrash(t *testing.T) {
	if got := scheduleLocalTime(1, "invalid/zone"); got == "" {
		t.Fatal("missing fallback time")
	}
	if got := scheduleFrequencyLabel(records.ScheduleSpec{Frequency: "weekly", Weekday: 99}); got != "每周日期不可用" {
		t.Fatal(got)
	}
}

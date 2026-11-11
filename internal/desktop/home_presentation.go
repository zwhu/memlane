package desktop

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

type homeTask struct{ ID, Kind, State string }

func (a *App) selectHomeDate(date string) {
	if a.homeDate != date {
		a.homeDate = date
		a.recent = nil
		a.homeRecentLoaded = false
	}
	a.refresh(false)
}

func (a *App) homeTotals(c *ui.Context) {
	ui.Row(c).Key("overview-totals").AlignItems(ui.Stretch).Border(1, design.Border).Radius(7).Background(ui.Hex("#fafbfd")).Children(func() {
		for _, metric := range []struct {
			label, unit string
			count       *int
		}{
			{"累计记忆", "条", a.overviewRaw}, {"知识", "条", a.overviewKnowledge}, {"使用天数", "天", nil},
		} {
			ui.Column(c).Key("overview-"+metric.label).Grow(1).MinWidth(0).Padding(11, 18).Gap(4).Children(func() {
				ui.Text(c, metric.label).FontSize(design.Small).TextColor(design.Muted)
				ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
					value := "—"
					if metric.count != nil {
						value = fmt.Sprint(*metric.count)
					}
					ui.Text(c, value).FontSize(26).Bold()
					ui.Text(c, metric.unit).FontSize(10).TextColor(design.Muted)
					if metric.label == "使用天数" {
						ui.Text(c, "首次使用日期未知").FontSize(10).TextColor(design.Muted).SingleLine().MinWidth(0)
					}
				})
			})
		}
	})
}

func (a *App) homePlanFooter(c *ui.Context) {
	ui.Row(c).Key("overview-plan-footer").Height(42).Shrink(0).Padding(0, 30).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Icon(c, icons["history"]).FontSize(11).TextColor(design.Muted)
		label := "整理计划待读取"
		if a.schedules.Loaded {
			label = "尚未启用整理计划"
			var next int64
			for _, plan := range a.schedules.Plans {
				if plan.Enabled && plan.NextRunMS > 0 && (next == 0 || plan.NextRunMS < next) {
					next = plan.NextRunMS
				}
			}
			if next > 0 {
				label = "下次整理：" + time.UnixMilli(next).In(time.Local).Format("01-02 15:04")
			}
		}
		if a.runtimeStatus.Phase == "stopped" {
			label = "服务已停止"
		}
		ui.Text(c, label).FontSize(10).TextColor(design.Muted).SingleLine()
		if a.serviceState == "ready" && time.Since(a.processing.CheckedAt) > 5*time.Second {
			a.refreshProcessing()
		}
		current := a.processing.currentLabel()
		ui.Text(c, "· "+current).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		quietButton(c, "管理计划").Key("schedule-manage").OnClick(func() { a.navigate("整理计划", "") })
	})
}

// Completion is shown once, after the daemon's final receipt for a task that
// this window observed as active. Leaving the active list alone is insufficient.
func (a *App) observeHomeTask(active, terminal homeTask, now time.Time) {
	a.homeActiveTask = active
	if active.ID != "" {
		a.homeLastTask = active
		a.homeCompletedAt = time.Time{}
	} else if terminal.ID != "" && terminal.ID == a.homeLastTask.ID {
		if terminal.State == "completed" {
			a.homeCompletedAt = now
		}
		a.homeLastTask = homeTask{}
	}
}

// Frozen B revision 5: nine stable blocks, 760ms + 22ms stagger hover,
// 1.7s running loop with -140ms phases, 460ms check + 22ms stagger,
// and a 1.4s completion receipt. None of these values advance a task.
var homeBlockOffsets = [9][2]float32{{-5, -3}, {1, -5}, {4, -2}, {-6, 1}, {0, 0}, {5, 2}, {-3, 5}, {1, 6}, {5, 5}}
var homeBlockCheck = [5][2]float32{{5, 11}, {8, 14}, {11, 11}, {14, 8}, {17, 5}}

type homeButtonMotion struct {
	Hover   bool
	HoverAt time.Time
}

func homeBlockPose(i int, elapsed float64, mode string) (dx, dy, scale, alpha float32) {
	scale, alpha = 1, .9
	switch mode {
	case "complete":
		progress := float32(max(0, min(1, (elapsed-float64(i)*.022)/.46)))
		progress = ui.CubicBezier(.2, .7, .2, 1)(progress)
		if i < 5 {
			dx = (homeBlockCheck[i][0] - float32(4+i%3*6)) * progress
			dy = (homeBlockCheck[i][1] - float32(4+i/3*6)) * progress
		} else {
			alpha = .9 * (1 - progress)
		}
	case "hover", "running":
		var t float32
		peak, end := float32(.26), float32(.76)
		amplitude, minScale, minAlpha := float32(1), float32(.82), float32(.45)
		if mode == "running" {
			t = float32(math.Mod((elapsed+float64(i)*.14)/1.7, 1))
			peak, end, amplitude, minScale, minAlpha = .36, .72, .48, .72, .35
		} else {
			t = float32(max(0, min(1, (elapsed-float64(i)*.022)/.76)))
		}
		amount := float32(0)
		if t <= peak {
			amount = t / peak
		} else if t < end {
			amount = 1 - (t-peak)/(end-peak)
		}
		// CSS hover uses the approved cubic curve; running uses EaseInOut.
		if mode == "hover" {
			amount = ui.CubicBezier(.2, .7, .25, 1)(amount)
		} else {
			amount = ui.EaseInOut(amount)
		}
		dx, dy = homeBlockOffsets[i][0]*amplitude*amount, homeBlockOffsets[i][1]*amplitude*amount
		scale, alpha = 1-(1-minScale)*amount, .9-(.9-minAlpha)*amount
	}
	return
}

func (a *App) homeOrganizationButton(c *ui.Context) {
	label := "立即整理"
	task, kind := a.homeActiveTask.ID, a.homeActiveTask.Kind
	moving := a.homeActiveTask.State == "running" || a.homeActiveTask.State == "applying"
	available := a.serviceState == "ready" && a.runtimeStatus.Phase != "stopped" && a.libraryError == ""
	reduced := c.Preferences().ReduceMotion || a.firstLaunch.State.ReducedMotion
	now := time.Now()
	completed := task == "" && !a.homeCompletedAt.IsZero() && now.Sub(a.homeCompletedAt) < 1400*time.Millisecond && available
	if completed {
		c.After(1400*time.Millisecond - now.Sub(a.homeCompletedAt))
	}
	if task != "" {
		label = "正在整理"
	} else if completed {
		label = "整理完成"
	}
	button := ui.Button(c.Key("home-organize"), "").Label(label).Height(36).Padding(6, 14, 6, 11).Gap(9).Border(1, ui.Hex("#b8c7d4")).Radius(7).Background(ui.Hex("#f8fafc")).Disabled(!available)
	hovered := (button.Hovered() || button.FocusVisible()) && available && !reduced
	if hovered && !a.homeMotion.Hover {
		a.homeMotion.HoverAt = now
	}
	a.homeMotion.Hover = hovered
	hoverAt, completedAt := a.homeMotion.HoverAt, a.homeCompletedAt
	if hovered {
		button.Background(ui.Hex("#f0f5fa")).Border(1, ui.Hex("#8fa7ba"))
	}
	if moving && available {
		button.Background(ui.Hex("#eef4f9")).Border(1, ui.Hex("#9fb4c7"))
	}
	if completed {
		button.Background(ui.Hex("#e7f0f7"))
	}
	button.Children(func() {
		ui.Box(c).Key("home-organize-blocks").Size(22, 22).Draw(func(p *ui.Painter, r ui.Rect) {
			mode, elapsed, animate := "", float64(0), false
			if moving && available && !reduced {
				mode, elapsed, animate = "running", float64(p.Now().UnixMilli())/1000, true
			} else if completed {
				mode, elapsed = "complete", p.Now().Sub(completedAt).Seconds()
				if reduced {
					elapsed = 1
				}
				// One bounded frame sequence also removes the 1.4s completion label.
				animate = !reduced && elapsed < 1.4
			} else if hovered && !hoverAt.IsZero() {
				mode, elapsed = "hover", p.Now().Sub(hoverAt).Seconds()
				animate = elapsed < .936
			}
			for i := 0; i < 9; i++ {
				dx, dy, scale, alpha := homeBlockPose(i, elapsed, mode)
				factor := r.W / 24
				size := 4 * scale * factor
				x, y := float32(4+i%3*6)+dx+2*(1-scale), float32(4+i/3*6)+dy+2*(1-scale)
				color := ui.Hex("#687e91")
				color.A = uint8(float32(color.A) * alpha)
				p.Fill(ui.Rect{X: r.X + x*factor, Y: r.Y + y*factor, W: size, H: size}, color, .7*factor)
			}
			if animate {
				p.AnimationFrame()
			}
		})
		ui.Text(c, label).FontSize(design.Small)
	})
	button.OnClick(func() {
		if kind == "organization" {
			a.selectOrganization(task)
		} else if kind == "merge" {
			a.selectMerge(task)
		} else {
			a.beginOrganizationOrMerge()
		}
	})
}

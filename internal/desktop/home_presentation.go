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
	ui.Row(c).Key("overview-totals").AlignItems(ui.Start).Padding(0, 0, 18, 0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		for _, metric := range []struct {
			label, unit string
			count       *int
		}{
			{"累计记忆", "条", a.overviewRaw}, {"知识", "条", a.overviewKnowledge}, {"使用天数", "天", nil},
		} {
			ui.Column(c).Key("overview-" + metric.label).Grow(1).MinWidth(0).Gap(8).Children(func() {
				ui.Text(c, metric.label).FontSize(design.Small).TextColor(design.Muted)
				ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
					value := "—"
					if metric.count != nil {
						value = fmt.Sprint(*metric.count)
					}
					ui.Text(c, value).FontSize(28).Bold()
					ui.Text(c, metric.unit).FontSize(10).TextColor(design.Muted)
				})
				if metric.label == "使用天数" {
					ui.Text(c, "首次使用日期未知").FontSize(10).TextColor(design.Muted)
				}
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
		current := "记忆处理未配置"
		if value := a.processing.Configuration.Current; value.Active && value.Revision != nil {
			current = "当前方式：" + processingSpecLabel(value.Revision.Spec)
		}
		ui.Text(c, "· "+current).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		quietButton(c, "管理计划").Key("schedule-manage").OnClick(func() { a.navigate("整理计划", "") })
	})
}

// The button reads real task summaries. Clicking a running task opens that
// task rather than preparing a second task. The icon never advances business
// state, and stopped/failed/reduced-motion states do not request frames.
func (a *App) homeOrganizationButton(c *ui.Context) {
	label := "立即整理"
	task, kind := a.homeActiveTask.ID, a.homeActiveTask.Kind
	moving := a.homeActiveTask.State == "running" || a.homeActiveTask.State == "applying"
	if task != "" {
		label = "正在整理"
	}
	available := a.serviceState == "ready" && a.runtimeStatus.Phase != "stopped" && a.libraryError == ""
	button := ui.Button(c.Key("home-organize"), "").Label(label).Height(36).Padding(7, 14).Gap(8).Border(1, ui.Hex("#d8e2ed")).Background(ui.Hex("#fafbfd")).Disabled(!available)
	button.Children(func() {
		ui.Box(c).Key("home-organize-blocks").Size(16, 16).Draw(func(p *ui.Painter, r ui.Rect) {
			for i := 0; i < 9; i++ {
				x, y := float32(i%3)*5, float32(i/3)*5
				if moving && available && !c.Preferences().ReduceMotion {
					y += float32(math.Sin(float64(p.Now().UnixMilli())/650 + float64(i)*.65))
					p.AnimationFrame()
				}
				p.Fill(ui.Rect{X: r.X + x, Y: r.Y + y, W: 3.5, H: 3.5}, design.Muted, .5)
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

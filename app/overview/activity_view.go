package overview

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *ControllerOf[T]) RenderActivities(c *ui.Context, now time.Time) {
	tok := a.tokens()
	v := a.State
	events, loaded, loadError := v.Events, v.ActivityLoaded, v.ActivityError
	if v.SearchHistory {
		events, loaded, loadError = v.History, v.HistoryLoaded, v.HistoryError
	}
	events = events[:min(50, len(events))]
	ui.Scroll(c.Key("overview-activity")).Label("近期动态内容").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.ActivityScroll).BorderWidth(0).Background(tok.Bg).Radius(0).Padding(sp1, 0, sp4, 0).Gap(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		bg := tok.Bg
		if v.ActivityScroll.MaxY-v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H - 24, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W - 6, H: 16}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if !loaded && v.ActivityLoading && len(events) == 0 {
			loadingRows(c, tok, "正在读取动态", true)
			return
		}
		if len(events) == 0 {
			title, hint := "暂无动态", "采集和提炼记忆后，动态会显示在这里"
			if v.SearchHistory {
				title, hint = "暂无搜索历史", "在搜索框中输入，记录会显示在这里"
			}
			if v.ActivityLoading && !loaded {
				title, hint = "正在读取动态…", ""
				if v.SearchHistory {
					title = "正在读取搜索历史…"
				}
			} else if loadError != "" {
				title, hint = "暂时无法读取", loadError
			}
			ui.Column(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				kitui.EmptyState(c, tok, "overview-activity-empty", title, hint)
				if loadError != "" {
					ui.Row(c).FillWidth().Justify(ui.Center).Children(func() { kitui.TextButton(c, tok, "重试", a.reloadActivity) })
				}
			})
			return
		}
		ui.Column(c).FillWidth().Shrink(0).Gap(0).Children(func() {
			if !v.SearchHistory && v.RecallError != "" {
				kitui.Text(c, tok, v.RecallError, homeReadableMeta)
				kitui.TextButton(c, tok, "重试", a.reloadActivity).Margin(0, 0, sp2, 0)
			}
			if loadError != "" {
				kitui.Text(c, tok, "读取失败，保留上次结果 · "+loadError, homeReadableMeta).MaxLines(2)
				kitui.TextButton(c, tok, "重试读取动态", a.reloadActivity).Margin(0, 0, sp2, 0)
			}
			if v.GroupedEvents == nil || v.cachedGroupedGen != v.ActivityGeneration || v.cachedGroupedHistory != v.SearchHistory || v.cachedGroupedCount != len(events) {
				v.GroupedEvents = GroupFailures(events)
				v.cachedGroupedGen = v.ActivityGeneration
				v.cachedGroupedHistory = v.SearchHistory
				v.cachedGroupedCount = len(events)
			}
			shown := v.GroupedEvents
			for idx, event := range shown {
				ev := event
				dotColor := tok.Text4
				running := false
				switch ev.State {
				case "已完成":
					dotColor = tok.OK
				case "失败":
					dotColor = tok.Danger
				case "处理中", "已启动":
					dotColor = tok.Accent
					running = true
				}
				var recallGroups []RecallGroup
				if ev.Recall != nil {
					recallGroups = RecallGroups(*ev.Recall, a.projects())
				}
				var row ui.Element
				evKey := fmt.Sprintf("overview-event-%d-%s", v.ActivityRun, ev.Key)
				if ev.Recall != nil && len(recallGroups) > 0 && a.OpenRecalled != nil {
					btn := ui.ButtonBase(c.Key(evKey)).Role(ui.RoleButton).
						Label(fmt.Sprintf("%s · %s · %d 条记忆", ev.Title, ev.Project, len(ev.Recall.Hits))).
						FillWidth().Justify(ui.Start).AlignItems(ui.Stretch).
						Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).BorderWidth(0).
						Cursor(ui.CursorPointer).Tooltip("查看本次召回的记忆")
					btn.Background(kitui.HoverColor(btn, "ev-hover", ui.Transparent, tok.Hover))
					if len(recallGroups) == 1 {
						ds := recallGroups[0].Dataset
						rc := *ev.Recall
						btn.OnClick(func() { a.OpenRecalled(rc, ds) })
					} else {
						rc := *ev.Recall
						groupsCopy := recallGroups
						btn.PopoverMenuButton(func(m *ui.Menu) {
							for _, g := range groupsCopy {
								if m.Item(fmt.Sprintf("%s · %d 条", g.Name, g.Count)).Chosen() {
									a.OpenRecalled(rc, g.Dataset)
								}
							}
						})
					}
					row = btn
				} else {
					row = ui.Row(c).Key(evKey).Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).AlignItems(ui.Stretch)
				}
				interactive := ev.Recall != nil && len(recallGroups) > 0 && a.OpenRecalled != nil
				a.staggerEnter(c, row, "event-"+ev.Key, idx, v.ActivityRun, interactive)
				row.Children(func() {
					ui.Column(c).Width(8).Shrink(0).Padding(6, 0, 0, 0).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(dotColor).Shrink(0)
						if idx < len(shown)-1 {
							ui.Box(c).Width(1).Grow(1).Margin(4, 0, -18, 0).Background(tok.Border)
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
							kitui.Text(c, tok, ev.Title, homeActivityTitle).Grow(1).MinWidth(0).MaxLines(2)
							if running {
								kitui.StageBlocks(c, tok)
							}
							stamp := "时间未知"
							if !ev.At.IsZero() {
								stamp = ev.At.In(now.Location()).Format("15:04")
								if ev.At.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02") {
									stamp = ev.At.In(now.Location()).Format("2006/01/02 15:04")
								}
							}
							kitui.Text(c, tok, stamp, homeReadableMeta).FontFeatures("tnum").Shrink(0)
						})
						if ev.State == "失败" {
							ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
								kitui.Text(c, tok, ev.Project+" ·", homeReadableMeta)
								kitui.Text(c, tok, EventFailureReason(ev), homeReadableMeta).TextColor(tok.Danger).MaxLines(1)
							})
						} else {
							label := ev.Project + " · " + ev.State
							if ev.Recall != nil {
								label = fmt.Sprintf("%s · %d 条记忆", ev.Project, len(ev.Recall.Hits))
							}
							kitui.Text(c, tok, label, homeReadableMeta)
						}
						if ev.Recall != nil && len(recallGroups) == 0 {
							kitui.Text(c, tok, "记忆库暂不可用", homeMeta)
						}
						if running {
							if project, ok := a.ActivityProject(ev); ok {
								kitui.TextButton(c, tok, "查看项目", func() { a.navigateProject(project.ID) }).AlignSelf(ui.Start).Justify(ui.Start)
							}
						}
						if ev.State == "失败" {
							detailBtn := ui.ButtonBase(c).Role(ui.RoleButton).Label("失败操作："+ev.Key).
								Cursor(ui.CursorPointer).Height(24).MinHeight(24).Padding(2, 0).Gap(3).
								Radius(radiusSmall).BorderWidth(0).Background(ui.Transparent).
								AlignItems(ui.Center).AlignSelf(ui.Start)
							fg := tok.Text3
							if detailBtn.Hovered() {
								fg = tok.Text1
							}
							detailBtn.Children(func() {
								ui.Text(c, "查看详情").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(fg).SingleLine()
								ui.Icon(c, kitui.IconChev).FontSize(10).TextColor(fg).Shrink(0)
							}).OnClick(func() {
								a.OpenFailureDetail(ev, now.Location())
							})
						}
					})
				})
			}
		})
	})
}
func (a *ControllerOf[T]) activityTabs(c *ui.Context) {
	tok := a.tokens()
	v := a.State
	selected := 0
	if v.SearchHistory {
		selected = 1
	}
	kitui.Segmented(c, tok, "overview-activity-tabs", "近期动态类型", &selected, []string{"动态", "最近搜索"}, func() {
		a.SetSearchHistory(selected == 1)
	})
}

func (a *ControllerOf[T]) reloadActivity() { a.LoadActivity() }

func (a *ControllerOf[T]) ActivityProject(event Event) (shell.ProjectRef, bool) {
	var match shell.ProjectRef
	found := false
	for _, p := range a.projects() {
		if !p.Common && p.DatasetID != "" && p.DatasetID == event.Record.Dataset {
			if found {
				return shell.ProjectRef{}, false
			}
			match, found = p, true
		}
	}
	return match, found
}

func (a *ControllerOf[T]) navigateProject(id string) {
	if a.NavigateProject != nil {
		a.NavigateProject(id)
	} else if a.Router != nil {
		a.Router.NavigateProject(id)
	}
}

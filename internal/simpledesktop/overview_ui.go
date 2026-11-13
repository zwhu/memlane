package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
)

type overviewViewState struct {
	ExpandedEvent, ExpandedReport, CopiedEvent string
	RawLoaded                                  bool
	Snapshot                                   overviewSnapshot
	Loaded, Loading                            bool
	Error                                      string
	Events                                     []overviewEvent
	ActivityLoaded, ActivityLoading, More      bool
	ActivityError                              string
	ActivityLimit                              int
	Day                                        string
	Offset                                     int
	Scroll                                     ui.ScrollState
	SidebarScroll                              ui.ScrollState
	AllProjects                                bool
	ActivityExpanded                           bool
	RevealProjects                             bool
	scope                                      string
	generation, activityGeneration             uint64
	cancel, activityCancel                     context.CancelFunc
}

func (a *App) overviewScope() string {
	datasets := []string{}
	for _, p := range overviewProjects(a.Config) {
		datasets = append(datasets, p.Dataset)
	}
	sort.Strings(datasets)
	scope, _ := json.Marshal(struct {
		Identity string
		Datasets []string
	}{a.readingIdentity, datasets})
	return string(scope)
}

func (a *App) loadOverview() {
	v := &a.overviewState
	if a.Config.ProjectControl != nil && !a.access.Loaded {
		v.Loading, v.ActivityLoading = true, true
		return
	}
	scope := a.overviewScope()
	if scope != v.scope {
		if v.cancel != nil {
			v.cancel()
		}
		if v.activityCancel != nil {
			v.activityCancel()
		}
		generation, activityGeneration := v.generation+1, v.activityGeneration+1
		// A directory/range refresh invalidates content, not the user's date or
		// reading position. Old diagnostics are dropped with the old scope.
		*v = overviewViewState{scope: scope, generation: generation, activityGeneration: activityGeneration,
			Day: v.Day, Offset: v.Offset, Scroll: v.Scroll, SidebarScroll: v.SidebarScroll, AllProjects: v.AllProjects, ActivityExpanded: v.ActivityExpanded, ActivityLimit: v.ActivityLimit}
	}
	if v.cancel != nil {
		v.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	v.cancel = cancel
	v.generation++
	generation := v.generation
	v.Loading = true
	client := a.Client
	go func() {
		defer cancel()
		snapshot, err := client.overview(ctx)
		a.Update(func() {
			if generation != a.overviewState.generation {
				return
			}
			a.overviewState.Loading = false
			if err != nil {
				a.overviewState.Error = err.Error()
				return
			}
			if snapshot.RawError != "" {
				snapshot.Raw = a.overviewState.Snapshot.Raw
			} else {
				a.overviewState.RawLoaded = true
			}
			ready := map[string]Item{}
			for _, old := range a.overviewState.Snapshot.Knowledge {
				ready[old.key()] = old
			}
			for n, item := range snapshot.Knowledge {
				if old, ok := ready[item.key()]; ok && old.ReadingState == "ready" && item.Document != nil && (item.Document.ContentHash != "" || item.Document.Updated != "") && keyForReading(a.readingIdentity, old, fmt.Sprint(generation)) == keyForReading(a.readingIdentity, item, fmt.Sprint(generation)) {
					snapshot.Knowledge[n].Title, snapshot.Knowledge[n].ReadingState = old.Title, old.ReadingState
				}
			}
			a.overviewState.Snapshot = snapshot
			// A new catalog may have the same IDs as the already preheated old
			// catalog. Let that publication consume cached titles again.
			a.recentScope = ""
			a.overviewState.Loaded = true
			a.overviewState.Error = ""
		})
	}()
	if v.ActivityLimit < 20 {
		v.ActivityLimit = 20
	}
	a.loadOverviewActivity()
}

func (a *App) loadOverviewActivity() {
	v := &a.overviewState
	if v.activityCancel != nil {
		v.activityCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	v.activityCancel = cancel
	v.activityGeneration++
	generation := v.activityGeneration
	v.ActivityLoading = true
	want := v.ActivityLimit
	if want < 20 {
		want = 20
	}
	client := a.Client
	go func() {
		defer cancel()
		events, more, err := client.overviewActivity(ctx, want)
		a.Update(func() {
			if generation != a.overviewState.activityGeneration {
				return
			}
			a.overviewState.ActivityLoading = false
			if err != nil {
				a.overviewState.ActivityError = err.Error()
				return
			}
			a.overviewState.Events = events
			a.overviewState.More = more
			a.overviewState.ActivityError = ""
			a.overviewState.ActivityLoaded = true
		})
	}()
}

func overviewBuckets(snapshot overviewSnapshot, location *time.Location) map[string]int {
	days := map[string]int{}
	for _, item := range snapshot.Knowledge {
		if item.Document != nil {
			if t, ok := officialTime(item.Document.Created); ok {
				days[t.In(location).Format("2006-01-02")]++
			}
		}
	}
	return days
}

func (a *App) openOverviewItem(item Item) {
	for idx, p := range a.Config.Projects {
		if p.Dataset == item.Dataset {
			a.navigate("知识", idx)
			a.selectItem(a.state(), item)
			return
		}
	}
	for idx, p := range a.Config.Common {
		if p.Dataset == item.Dataset {
			a.navigate("通用知识", idx)
			a.selectItem(a.state(), item)
			return
		}
	}
}

func (a *App) overviewHome(c *ui.Context) {
	v := &a.overviewState
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	c.After(midnight.Sub(now))
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).TrackScroll(&v.Scroll).Padding(32, 32, 60, 32).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1320).AlignSelf(ui.Center).Shrink(0).Gap(25).Children(func() {
			usage := "—"
			if n, err := firstUseDays(a.Config, now); err == nil {
				usage = fmt.Sprint(n)
			}
			knowledge, raw := "—", "—"
			if v.Loaded {
				knowledge = fmt.Sprint(len(v.Snapshot.Knowledge))
			}
			if v.RawLoaded {
				raw = fmt.Sprint(v.Snapshot.Raw)
			}
			ui.Row(c).Gap(20).Padding(0, 0, 3, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(9).Children(func() {
					ui.Text(c, "YOUR MEMORY, CONNECTED").FontSize(10).TextColor(homeMuted)
					ui.Text(c, "每一次积累，都能找回。").FontSize(25).Bold().TextColor(homeInk)
					ui.Text(c, "项目中的知识与经验，在这里继续。").FontSize(12).TextColor(homeMuted)
				})
				for idx, metric := range []struct{ label, value, unit string }{{"已使用", usage, "天"}, {"知识", knowledge, "条"}, {"原始记录", raw, "条"}} {
					stat := ui.Column(c).Key("overview-stat-"+metric.label).Padding(0, 20).Gap(6).Shrink(0)
					if idx < 2 {
						stat.BorderWidth(0, 1, 0, 0).BorderColor(homeBorder)
					}
					stat.Children(func() {
						ui.Row(c).Gap(5).Children(func() {
							ui.Text(c, metric.value).FontSize(27).TextColor(homeInk)
							ui.Text(c, metric.unit).FontSize(11).TextColor(homeMuted)
						})
						ui.Text(c, metric.label).FontSize(10).TextColor(homeMuted)
					})
				}
			})
			if _, err := firstUseDays(a.Config, now); err != nil {
				ui.Text(c, "使用天数暂不可用，请通过开发入口完成初始化。").FontSize(11).TextColor(muted)
			}
			if v.Loading {
				ui.Text(c, "正在读取统计…").FontSize(11).TextColor(muted)
			}
			if v.Error != "" {
				ui.Text(c, "统计读取失败，保留上次结果 · "+v.Error).FontSize(11).TextColor(muted)
			}
			if v.Snapshot.RawError != "" {
				ui.Text(c, "原始记录统计暂不可用 · "+v.Snapshot.RawError).FontSize(11).TextColor(muted)
			}
			a.overviewHeatmap(c, now)
			ui.Row(c).Gap(30).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(0).Grow(1.6).MinWidth(0).Gap(15).Children(func() { a.overviewKnowledge(c, now) })
				ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(15).Children(func() { a.overviewActivities(c, now) })
			})
			a.overviewProjectGrid(c)
			if len(a.Config.Projects) == 0 {
				ui.Text(c, "尚未关联项目，请在设置中关联已有官方数据组。").FontSize(12).TextColor(muted)
			}
			ui.Row(c).Children(func() {
				ui.Icon(c, mark).FontSize(12).TextColor(homeMuted)
				ui.Text(c, "Cognee 保存与处理").Margin(0, 6).FontSize(10).TextColor(homeMuted)
				ui.Spacer(c)
				ui.Text(c, now.Format("01月02日")).FontSize(10).TextColor(homeMuted)
			})
		})
	})
}

func (a *App) overviewHeatmap(c *ui.Context, now time.Time) {
	v := &a.overviewState
	buckets := overviewBuckets(v.Snapshot, now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := today.AddDate(0, 0, -364)
	gridStart := start.AddDate(0, 0, -int(start.Weekday()))
	weeks := int(today.Sub(gridStart).Hours()/24)/7 + 1
	// Calendar iteration (rather than hour arithmetic) drives every actual cell.
	if gridStart.AddDate(0, 0, weeks*7-1).Before(today) {
		weeks++
	}
	total := 0
	for day := start; !day.After(today); day = day.AddDate(0, 0, 1) {
		total += buckets[day.Format("2006-01-02")]
	}
	ui.Column(c).Gap(13).Padding(18, 21, 15, 21).Border(1, homeBorder).Radius(11).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, "知识积累").FontSize(13).Bold().TextColor(homeInk)
			ui.Text(c, "过去一年").Margin(0, 8).FontSize(10).TextColor(homeMuted)
			ui.Spacer(c)
			ui.Text(c, start.Format("2006.01.02")+" — "+today.Format("2006.01.02")).FontSize(10).TextColor(muted)
		})
		ui.Column(c).FillWidth().MaxWidth(900).AlignSelf(ui.Center).Gap(8).Children(func() {
			ui.Row(c).Gap(3).Children(func() {
				for week := 0; week < weeks; week++ {
					end := gridStart.AddDate(0, 0, week*7+6)
					label := ""
					if week == 0 || end.Month() != end.AddDate(0, 0, -7).Month() {
						label = fmt.Sprint(int(end.Month()))
					}
					ui.Column(c).Width(0).Grow(1).Children(func() { ui.Text(c, label).FontSize(9).TextColor(homeMuted) })
				}
			})
			ui.Row(c).Gap(3).Children(func() {
				for week := 0; week < weeks; week++ {
					ui.Column(c).Width(0).Grow(1).Gap(3).Children(func() {
						for weekday := 0; weekday < 7; weekday++ {
							day := gridStart.AddDate(0, 0, week*7+weekday)
							date := day.Format("2006-01-02")
							count := buckets[date]
							visible := !day.Before(start) && !day.After(today)
							color := ui.Hex("#f0f3f7")
							if count > 0 {
								color = ui.Hex("#d6e2f0")
							}
							if count >= 3 {
								color = ui.Hex("#adc5e0")
							}
							if count >= 6 {
								color = ui.Hex("#7fa3c9")
							}
							if count >= 10 {
								color = ui.Hex("#567fa9")
							}
							if !visible {
								color = ui.Transparent
							}
							if !visible || count == 0 || !v.Loaded {
								ui.Column(c).Key("overview-empty-" + date).FillWidth().AspectRatio(1).Radius(2).Background(color)
								continue
							}
							label := fmt.Sprintf("%s · 新增 %d 条知识", date, count)
							cell := ui.Button(c, "").Key("overview-day-" + date).Label(label).Tooltip(label).Padding(0).MinHeight(0).MinWidth(0).FillWidth().AspectRatio(1).Radius(2).BorderWidth(0).Background(color)
							if v.Day == date {
								cell.Border(1, ink)
							}
							cell.OnClick(func() { v.Day = date; v.Offset = 0 })
						}
					})
				}
			})
		})
		ui.Row(c).Children(func() {
			text := "读取中…"
			if v.Loaded {
				text = fmt.Sprintf("今天新增 %d 条知识 · 过去一年 %d 条", buckets[today.Format("2006-01-02")], total)
			}
			ui.Text(c, text).FontSize(10).TextColor(muted)
			ui.Spacer(c)
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, "少").FontSize(10).TextColor(homeMuted)
				for _, color := range []string{"#f0f3f7", "#d6e2f0", "#adc5e0", "#7fa3c9"} {
					ui.Column(c).Size(8, 8).Radius(2).Background(ui.Hex(color))
				}
				ui.Text(c, "多").FontSize(10).TextColor(homeMuted)
			})
		})
		if v.Snapshot.MissingDates > 0 {
			ui.Text(c, "部分知识缺少创建日期，未计入热力图。").FontSize(10).TextColor(muted)
		}
	})
}

func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := []Item{}
	for _, item := range v.Snapshot.Knowledge {
		if v.Day != "" {
			if item.Document == nil {
				continue
			}
			t, ok := officialTime(item.Document.Created)
			if !ok || t.In(now.Location()).Format("2006-01-02") != v.Day {
				continue
			}
		}
		rows = append(rows, item)
	}
	ui.Row(c).Children(func() {
		title := "最近知识"
		if v.Day != "" {
			title = v.Day + " 新增知识"
		}
		ui.Text(c, title).FontSize(15).Bold().TextColor(ink)
		ui.Spacer(c)
		if v.Day != "" {
			button(c, "显示最近知识", func() { v.Day = ""; v.Offset = 0 })
		} else {
			homeLink(c, "搜索知识  ›", a.OpenSearch)
		}
	})
	ui.Column(c).Height(1).Background(homeBorder)
	offset := v.Offset
	if offset >= len(rows) {
		offset = 0
	}
	const pageSize = 3
	end := min(offset+pageSize, len(rows))
	a.startRecentTitles(rows[offset:end])
	for _, item := range rows[offset:end] {
		i := item
		ui.Button(c, "").Key("recent-"+i.key()).Label(i.Title).FillWidth().Justify(ui.Start).AlignItems(ui.Start).Padding(17, 0).Gap(13).Background(ui.Transparent).BorderWidth(0, 0, 1, 0).BorderColor(homeBorder).Children(func() {
			ui.Column(c).Size(28, 33).Shrink(0).Border(1, homeBorder).Radius(6).Background(ui.Hex("#f7f9fc")).AlignItems(ui.Center).Justify(ui.Center).Children(func() { ui.Icon(c, homeDocument).FontSize(15).TextColor(homeMuted) })
			ui.Column(c).Gap(6).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, i.Title).FontSize(12).TextColor(homeInk).LineHeight(1.7).MaxLines(2)
				date := "创建日期未知"
				if i.Document != nil {
					if at, ok := officialTime(i.Document.Created); ok {
						date = at.In(now.Location()).Format("01月02日 15:04")
					}
				}
				ui.Text(c, "知识  ·  "+date).FontSize(10).TextColor(homeMuted)
			})
			ui.Text(c, "›").FontSize(17).TextColor(homeMuted).Shrink(0)
		}).OnClick(func() { a.openOverviewItem(i) })
		if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
			button(c, "标题读取失败 · 重试", func() { a.retryRecentTitle(i) })
		}
	}
	if len(rows) == 0 {
		label := "暂无知识"
		if !v.Loaded {
			label = "知识尚未载入"
		}
		ui.Text(c, label).FontSize(12).TextColor(muted)
	}
	if len(rows) > pageSize {
		ui.Row(c).Gap(12).Children(func() {
			ui.Button(c, "上一页").Disabled(offset == 0).OnClick(func() { v.Offset = max(0, offset-pageSize) })
			ui.Text(c, fmt.Sprintf("%d / %d", offset/pageSize+1, (len(rows)+pageSize-1)/pageSize)).FontSize(11).TextColor(muted)
			ui.Button(c, "下一页").Disabled(end == len(rows)).OnClick(func() { v.Offset = end })
		})
	}
}

func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	v := &a.overviewState
	ui.Row(c).Children(func() {
		ui.Text(c, "近期动态").FontSize(15).Bold().TextColor(ink)
		ui.Spacer(c)
		ui.Button(c, "↻").Label("刷新动态").Disabled(v.ActivityLoading).FontSize(17).Background(ui.Transparent).BorderWidth(0).OnClick(a.loadOverviewActivity)
	})
	if v.ActivityLoading {
		ui.Text(c, "正在读取动态…").FontSize(11).TextColor(muted)
	}
	if v.ActivityError != "" {
		ui.Text(c, "动态读取失败，保留上次结果 · "+v.ActivityError).FontSize(11).TextColor(muted)
	}
	if v.ActivityLoaded && len(v.Events) == 0 {
		ui.Text(c, "暂无动态").FontSize(12).TextColor(muted)
	}
	ui.Column(c.Key("overview-activity")).FillWidth().Border(1, homeBorder).Background(ui.Hex("#fafbfd")).Radius(11).Padding(18).Gap(0).Children(func() {
		shown := v.Events
		if !v.ActivityExpanded {
			shown = shown[:min(3, len(shown))]
		}
		for _, event := range shown {
			ui.Row(c).Key("overview-event-"+event.Key).Padding(5, 0, 18, 0).Gap(9).AlignItems(ui.Start).Children(func() {
				mark, color := "✓", ui.Hex("#749082")
				if event.State == "失败" {
					mark, color = "!", ui.Hex("#9f7f66")
				} else if event.State != "已完成" {
					mark, color = "·", ui.Hex("#839bb5")
				}
				ui.Column(c).Width(17).Shrink(0).Padding(2, 0).Children(func() { ui.Text(c, mark).FontSize(10).TextColor(color) })
				ui.Column(c).Grow(1).MinWidth(0).Gap(7).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, event.Title).FontSize(11).TextColor(homeInk).Grow(1).MinWidth(0).MaxLines(2)
						ui.Text(c, event.At.In(now.Location()).Format("15:04")).FontSize(10).TextColor(homeMuted).Shrink(0)
					})
					label := event.Project + " · " + event.State
					if event.State == "失败" {
						label += " · " + eventFailureReason(event)
					}
					ui.Text(c, label).FontSize(10).TextColor(muted)
					if event.State == "失败" {
						ui.Row(c).Gap(8).Children(func() {
							title := "查看失败详情"
							if v.ExpandedEvent == event.Key {
								title = "收起失败详情"
							}
							ui.Button(c, title).Key("failure-detail-"+event.Key).FontSize(10).Height(24).Padding(4, 8).OnClick(func() {
								if v.ExpandedEvent == event.Key {
									v.ExpandedEvent = ""
									return
								}
								v.ExpandedEvent = event.Key
								v.ExpandedReport = failureReport(event, a.Config)
							})
							copyTitle := "复制诊断信息"
							if v.CopiedEvent == event.Key {
								copyTitle = "诊断信息已复制"
							}
							ui.Button(c, copyTitle).Key("failure-copy-"+event.Key).FontSize(10).Height(24).Padding(4, 8).OnClick(func() {
								report := v.ExpandedReport
								if v.ExpandedEvent != event.Key {
									report = failureReport(event, a.Config)
								}
								c.WriteClipboard(report)
								v.CopiedEvent = event.Key
							})
						})
						if v.ExpandedEvent == event.Key {
							ui.Text(c, v.ExpandedReport).FontSize(10).TextColor(ink).LineHeight(1.6)
						}
					}
				})
			})
		}
	})
	if len(v.Events) > 3 {
		title := "查看全部动态"
		if v.ActivityExpanded {
			title = "收起动态"
		}
		homeLink(c, title, func() { v.ActivityExpanded = !v.ActivityExpanded })
	}
	if v.More {
		ui.Button(c, "加载更多动态").Disabled(v.ActivityLoading).OnClick(func() { v.ActivityExpanded = true; v.ActivityLimit += 20; a.loadOverviewActivity() })
	}
}

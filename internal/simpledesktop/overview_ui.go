package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
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
	scope                                      string
	generation, activityGeneration             uint64
	cancel, activityCancel                     context.CancelFunc
}

func (a *App) loadOverview() {
	v := &a.overviewState
	scope, _ := json.Marshal(struct {
		URL, Token string
		Projects   []Project
	}{a.Config.ServiceURL, a.Config.TokenFile, overviewProjects(a.Config)})
	if string(scope) != v.scope {
		if v.cancel != nil {
			v.cancel()
		}
		if v.activityCancel != nil {
			v.activityCancel()
		}
		generation, activityGeneration := v.generation+1, v.activityGeneration+1
		*v = overviewViewState{scope: string(scope), generation: generation, activityGeneration: activityGeneration}
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
			a.overviewState.Snapshot = snapshot
			a.overviewState.Loaded = true
			a.overviewState.Error = ""
		})
	}()
	v.ActivityLimit = 20
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
	ui.Scroll(c.Key("overview-scroll")).Grow(1).TrackScroll(&v.Scroll).Padding(28, 32).Gap(22).Children(func() {
		ui.Row(c).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "继续积累，随时找回").FontSize(25).Bold().TextColor(ink)
				ui.Text(c, "回顾每天的知识积累，以及最近的记忆动态。").FontSize(12).TextColor(muted)
			})
			ui.Spacer(c)
			ui.Button(c, "刷新总览").Key("overview-refresh").Disabled(v.Loading || v.ActivityLoading).OnClick(a.loadOverview)
		})
		usage := "—"
		if n, err := firstUseDays(a.Config, now); err == nil {
			usage = fmt.Sprintf("%d 天", n)
		}
		knowledge, raw := "—", "—"
		if v.Loaded {
			knowledge = fmt.Sprintf("%d 条", len(v.Snapshot.Knowledge))
		}
		if v.RawLoaded {
			raw = fmt.Sprintf("%d 条", v.Snapshot.Raw)
		}
		ui.Row(c).Gap(24).Children(func() {
			for _, metric := range []struct{ label, value string }{{"已使用", usage}, {"知识", knowledge}, {"原始记录", raw}} {
				ui.Column(c).Grow(1).Gap(8).Children(func() {
					ui.Text(c, metric.label).FontSize(11).TextColor(muted)
					ui.Text(c, metric.value).FontSize(27).Bold().TextColor(ink)
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
		ui.Row(c).Gap(24).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(3).MinWidth(0).Gap(12).Children(func() { a.overviewKnowledge(c, now) })
			ui.Column(c).Grow(2).MinWidth(0).Gap(12).Children(func() { a.overviewActivities(c, now) })
		})
		ui.Text(c, "项目").FontSize(15).Bold().TextColor(ink)
		ui.Row(c).Gap(12).Children(func() {
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				index := idx
				ui.Button(c, "").Key("home-project-"+p.ID).Label(labels[idx]+" · 查看项目知识").Grow(1).MinWidth(0).Justify(ui.Start).Padding(16).Height(82).Background(ui.Hex("#ffffff")).Border(1, border).Radius(8).Children(func() {
					ui.Column(c).Gap(9).Children(func() {
						ui.Text(c, labels[idx]).FontSize(14).Bold().TextColor(ink)
						ui.Text(c, "查看项目知识  ›").FontSize(11).TextColor(muted)
					})
				}).OnClick(func() { a.navigate("知识", index) })
			}
		})
		if len(a.Config.Projects) == 0 {
			ui.Text(c, "尚未关联项目，请在设置中关联已有官方数据组。").FontSize(12).TextColor(muted)
		}
		ui.Text(c, "内容服务 · "+a.service+"\nCodex · "+a.completion).FontSize(11).TextColor(muted)
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
	ui.Column(c).Gap(12).Padding(16).Border(1, border).Radius(8).Children(func() {
		ui.Row(c).Children(func() {
			heading := "过去一年新增知识"
			if v.Loaded {
				heading = fmt.Sprintf("过去一年新增 %d 条知识", total)
			}
			ui.Text(c, heading).FontSize(12).Bold().TextColor(ink)
			ui.Spacer(c)
			ui.Text(c, start.Format("2006.01.02")+" — "+today.Format("2006.01.02")).FontSize(10).TextColor(muted)
		})
		ui.Row(c).Gap(3).Children(func() {
			for week := 0; week < weeks; week++ {
				end := gridStart.AddDate(0, 0, week*7+6)
				label := ""
				if week == 0 || end.Month() != end.AddDate(0, 0, -7).Month() {
					label = fmt.Sprint(int(end.Month()))
				}
				ui.Column(c).Width(11).Grow(1).Children(func() { ui.Text(c, label).FontSize(9).TextColor(muted) })
			}
		})
		ui.Row(c).Gap(3).Children(func() {
			for week := 0; week < weeks; week++ {
				ui.Column(c).Width(11).Grow(1).Gap(3).Children(func() {
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
							ui.Column(c).Key("overview-empty-" + date).Width(11).Height(11).Radius(2).Background(color)
							continue
						}
						label := fmt.Sprintf("%s · 新增 %d 条知识", date, count)
						cell := ui.Button(c, "").Key("overview-day-" + date).Label(label).Tooltip(label).Padding(0).Height(11).Width(11).Radius(2).BorderWidth(0).Background(color).Disabled(!visible || !v.Loaded || count == 0)
						if v.Day == date {
							cell.Border(1, ink)
						}
						cell.OnClick(func() { v.Day = date; v.Offset = 0 })
					}
				})
			}
		})
		ui.Row(c).Children(func() {
			text := "读取中…"
			if v.Loaded {
				text = fmt.Sprintf("今天新增 %d 条", buckets[today.Format("2006-01-02")])
			}
			ui.Text(c, text).FontSize(10).TextColor(muted)
			ui.Spacer(c)
			ui.Text(c, "少  ░ ▒ ▓  多").FontSize(10).TextColor(muted)
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
		}
	})
	offset := v.Offset
	if offset >= len(rows) {
		offset = 0
	}
	end := min(offset+8, len(rows))
	a.startRecentTitles(rows[offset:end])
	for _, item := range rows[offset:end] {
		i := item
		ui.Button(c, "").Key("recent-"+i.key()).Label(i.Title).Justify(ui.Start).Padding(12, 0).Background(ui.Transparent).BorderWidth(0, 0, 1, 0).BorderColor(border).Children(func() {
			ui.Column(c).Gap(6).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, i.Title).FontSize(12).Bold().TextColor(ink).SingleLine()
				date := "创建日期未知"
				if i.Document != nil {
					if at, ok := officialTime(i.Document.Created); ok {
						date = at.In(now.Location()).Format("01月02日 15:04")
					}
				}
				ui.Text(c, date).FontSize(10).TextColor(muted)
			})
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
	if len(rows) > 8 {
		ui.Row(c).Gap(12).Children(func() {
			ui.Button(c, "上一页").Disabled(offset == 0).OnClick(func() { v.Offset = max(0, offset-8) })
			ui.Text(c, fmt.Sprintf("%d / %d", offset/8+1, (len(rows)+7)/8)).FontSize(11).TextColor(muted)
			ui.Button(c, "下一页").Disabled(end == len(rows)).OnClick(func() { v.Offset = end })
		})
	}
}

func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	v := &a.overviewState
	ui.Row(c).Children(func() {
		ui.Text(c, "近期动态").FontSize(15).Bold().TextColor(ink)
		ui.Spacer(c)
		ui.Button(c, "刷新动态").Disabled(v.ActivityLoading).OnClick(a.loadOverviewActivity)
	})
	ui.Text(c, "记忆保存、整理和召回的操作记录").FontSize(10).TextColor(muted)
	if v.ActivityLoading {
		ui.Text(c, "正在读取动态…").FontSize(11).TextColor(muted)
	}
	if v.ActivityError != "" {
		ui.Text(c, "动态读取失败，保留上次结果 · "+v.ActivityError).FontSize(11).TextColor(muted)
	}
	if v.ActivityLoaded && len(v.Events) == 0 {
		ui.Text(c, "暂无动态").FontSize(12).TextColor(muted)
	}
	ui.Scroll(c.Key("overview-activity")).Height(210).Border(1, border).Radius(8).Padding(6, 14).Gap(0).Children(func() {
		for _, event := range v.Events {
			ui.Column(c).Key("overview-event-"+event.Key).Padding(10, 0).Gap(7).BorderWidth(0, 0, 1, 0).BorderColor(border).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, event.Title+" · "+event.State).FontSize(12).TextColor(ink)
					ui.Spacer(c)
					ui.Text(c, event.At.In(now.Location()).Format("01月02日 15:04")).FontSize(10).TextColor(muted)
				})
				label := event.Project
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
		}
	})
	if v.More {
		ui.Button(c, "加载更多动态").Disabled(v.ActivityLoading).OnClick(func() { v.ActivityLimit += 20; a.loadOverviewActivity() })
	}
}

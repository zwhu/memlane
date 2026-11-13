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
	ActivityScroll                             ui.ScrollState
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
			Day: v.Day, Offset: v.Offset, Scroll: v.Scroll, SidebarScroll: v.SidebarScroll, ActivityScroll: v.ActivityScroll, AllProjects: v.AllProjects, ActivityExpanded: v.ActivityExpanded, ActivityLimit: v.ActivityLimit}
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
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).TrackScroll(&v.Scroll).Padding(homePageInset, homePageInset, 76, homePageInset).Font("system-ui").FontWeight(400).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(homeMaxWidth).AlignSelf(ui.Center).Shrink(0).Gap(0).Children(func() {
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
			ui.Row(c).Margin(0, 0, 28, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(9).Children(func() {
					homeText(c, "YOUR MEMORY, CONNECTED", homeEyebrow)
					homeText(c, "每一次积累，都能找回。", homeWelcome)
					homeText(c, "项目中的知识与经验，在这里继续。", homeBody)
				})
				for idx, metric := range []struct{ label, value, unit string }{{"已使用", usage, "天"}, {"知识", knowledge, "条"}, {"原始记录", raw, "条"}} {
					left, right := float32(28), float32(28)
					if idx == 0 {
						left = 0
					}
					if idx == 2 {
						right = 0
					}
					stat := ui.Column(c).Key("overview-stat-"+metric.label).Padding(0, right, 0, left).Gap(6).Shrink(0)
					if idx < 2 {
						stat.BorderWidth(0, 1, 0, 0).BorderColor(homeBorder)
					}
					stat.Children(func() {
						ui.Row(c).Gap(5).Children(func() {
							homeText(c, metric.value, homeStatValue)
							homeText(c, metric.unit, homeStatUnit)
						})
						homeText(c, metric.label, homeStatLabel)
					})
				}
			})
			if _, err := firstUseDays(a.Config, now); err != nil {
				homeText(c, "使用天数暂不可用，请通过开发入口完成初始化。", homeAction).Margin(0, 0, 12, 0)
			}
			if v.Loading {
				homeText(c, "正在读取统计…", homeAction).Margin(0, 0, 12, 0)
			}
			if v.Error != "" {
				homeText(c, "统计读取失败，保留上次结果 · "+v.Error, homeAction).Margin(0, 0, 12, 0)
			}
			if v.Snapshot.RawError != "" {
				homeText(c, "原始记录统计暂不可用 · "+v.Snapshot.RawError, homeAction).Margin(0, 0, 12, 0)
			}
			a.overviewHeatmap(c, now)
			ui.Row(c).Key("overview-paired-sections").Gap(homeColumnGap).Margin(0, 0, 23, 0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Key("overview-knowledge-section").Width(0).Grow(1.6).MinWidth(0).Gap(0).Children(func() { a.overviewKnowledge(c, now) })
				// The knowledge column determines the row's natural height. The
				// activity content fills that same box without contributing its
				// unbounded event/diagnostic height to the parent scroll.
				ui.Column(c).Width(0).Grow(1).MinWidth(255).Children(func() {
					ui.Column(c).Key("overview-activity-section").Absolute().Top(0).Bottom(0).Left(0).Right(0).Gap(0).Children(func() { a.overviewActivities(c, now) })
				})
			})
			a.overviewProjectGrid(c)
			if len(a.Config.Projects) == 0 {
				homeText(c, "尚未关联项目，请在设置中关联已有官方数据组。", homeBody)
			}
			ui.Row(c).Margin(20, 0, 0, 0).Children(func() {
				ui.Icon(c, mark).FontSize(12).TextColor(homeMuted)
				homeText(c, "Cognee 保存与处理", homeMeta).Margin(0, 6)
				ui.Spacer(c)
				homeText(c, now.Format("01月02日"), homeMeta)
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
	ui.Column(c).Gap(0).Margin(0, 0, 25, 0).Padding(18, 21, 15, 21).Border(1, homeBorder).Radius(11).Background(ui.Hex("#ffffff")).Children(func() {
		ui.Row(c).Margin(0, 0, 18, 0).Children(func() {
			homeText(c, "知识积累", homePanelHeading)
			homeText(c, "过去一年", homeMeta).Margin(0, 7)
			ui.Spacer(c)
			homeText(c, start.Format("2006.01")+" — "+today.Format("2006.01"), homeMeta)
		})
		ui.Column(c).FillWidth().MaxWidth(900).AlignSelf(ui.Center).Gap(8).Children(func() {
			ui.Row(c).FillWidth().MaxWidth(879).AlignSelf(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, now.Location())
				for !month.After(today) {
					homeText(c, fmt.Sprint(int(month.Month()))+"月", homeMeta).FontSize(9).SingleLine().Shrink(0).TextColor(ui.Hex("#98a3ae"))
					month = month.AddDate(0, 1, 0)
				}
			})
			ui.Row(c).Gap(9).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(9).Shrink(0).Gap(3).Children(func() {
					for weekday := 0; weekday < 7; weekday++ {
						label := ""
						if weekday == 1 {
							label = "一"
						}
						if weekday == 3 {
							label = "三"
						}
						if weekday == 5 {
							label = "五"
						}
						ui.Column(c).Height(0).Grow(1).Justify(ui.Center).Children(func() { homeText(c, label, homeMeta).FontSize(8) })
					}
				})
				ui.Row(c).Width(0).Grow(1).AspectRatio(53.0 / 7).Gap(3).Children(func() {
					for week := 0; week < weeks; week++ {
						ui.Column(c).Width(0).Grow(1).Gap(3).Children(func() {
							for weekday := 0; weekday < 7; weekday++ {
								day := gridStart.AddDate(0, 0, week*7+weekday)
								date := day.Format("2006-01-02")
								count := buckets[date]
								visible := !day.Before(start) && !day.After(today)
								color := ui.Hex("#f0f3f6")
								if count > 0 {
									color = ui.Hex("#edf1f5")
								}
								if count >= 3 {
									color = ui.Hex("#d6e2ee")
								}
								if count >= 6 {
									color = ui.Hex("#b0c6db")
								}
								if count >= 10 {
									color = ui.Hex("#89a8c8")
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
		})
		ui.Row(c).Margin(13, 0, 0, 0).Children(func() {
			text := "读取中…"
			if v.Loaded {
				text = fmt.Sprintf("今天新增 %d 条知识", buckets[today.Format("2006-01-02")])
			}
			homeText(c, text, homeMeta)
			ui.Spacer(c)
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, "少").FontSize(10).TextColor(homeMuted)
				for _, color := range []string{"#edf1f5", "#d6e2ee", "#b0c6db", "#89a8c8"} {
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

func (a *App) overviewKnowledgeRows(now time.Time) []Item {
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
	return rows
}

func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := a.overviewKnowledgeRows(now)
	ui.Row(c).Margin(0, 0, 15, 0).Children(func() {
		title := "最近知识"
		if v.Day != "" {
			title = v.Day + " 新增知识"
		}
		homeText(c, title, homeHeading)
		ui.Spacer(c)
		if v.Day != "" {
			homeLink(c, "显示最近知识", func() { v.Day = ""; v.Offset = 0 })
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
			ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
				homeText(c, i.Title, homeTitle).MaxLines(2)
				date := "创建日期未知"
				if i.Document != nil {
					if at, ok := officialTime(i.Document.Created); ok {
						date = at.In(now.Location()).Format("01月02日 · 15:04")
					}
				}
				ui.Row(c).Margin(9, 0, 0, 0).Gap(8).Children(func() {
					homeText(c, "知识", homeMeta).Padding(2, 6).Radius(4).Background(ui.Hex("#f0f4f8")).TextColor(ui.Hex("#7d93a9"))
					homeText(c, date, homeMeta)
				})
			})
			ui.Icon(c, homeArrow).FontSize(12).TextColor(ui.Hex("#a9b4bf")).Margin(7, 0, 0, 0).Shrink(0)
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
		ui.Column(c).MinHeight(100).Justify(ui.Center).Children(func() { homeText(c, label, homeBody) })
	}
	ui.Row(c).MinHeight(24).Shrink(0).Margin(12, 0, 0, 0).Gap(12).Children(func() {
		if len(rows) > pageSize {
			homeLink(c, "上一页", func() { v.Offset = max(0, offset-pageSize) }).Disabled(offset == 0).MinHeight(24)
			homeText(c, fmt.Sprintf("%d / %d", offset/pageSize+1, (len(rows)+pageSize-1)/pageSize), homeMeta)
			homeLink(c, "下一页", func() { v.Offset = end }).Disabled(end == len(rows)).MinHeight(24)
		}
	})
}

func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := a.overviewKnowledgeRows(now)
	offset := v.Offset
	if offset >= len(rows) {
		offset = 0
	}
	compactLimit := max(1, min(3, len(rows)-offset))
	ui.Row(c).Shrink(0).Margin(0, 0, 15, 0).Children(func() {
		homeText(c, "近期动态", homeHeading)
		ui.Spacer(c)
		homeIconButton(c, "刷新动态", homeRefresh, a.loadOverviewActivity).Disabled(v.ActivityLoading)
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
	ui.Scroll(c.Key("overview-activity")).Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.ActivityScroll).Border(1, ui.Hex("#eaf0f4")).Background(ui.Hex("#fafbfd")).Radius(11).Padding(18, 18, 5, 18).Gap(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		// A soft edge communicates more scrollable content without leaving a
		// hard-cut half line against the rounded panel border. Keep the narrow
		// scrollbar uncovered and remove each edge at that end of the list.
		bg := ui.Hex("#fafbfd")
		if v.ActivityScroll.MaxY-v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X + 1, Y: r.Y + r.H - 25, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 6, H: 18}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		ui.Column(c).FillWidth().Shrink(0).Gap(0).Children(func() {
			shown := v.Events
			if !v.ActivityExpanded {
				shown = shown[:min(compactLimit, len(shown))]
			}
			for idx, event := range shown {
				ui.Row(c).Key("overview-event-"+event.Key).Padding(0, 0, 23, 0).Gap(9).AlignItems(ui.Stretch).Children(func() {
					mark, color := "✓", ui.Hex("#749082")
					border, background := ui.Hex("#b5c8bd"), ui.Hex("#f6faf7")
					if event.State == "失败" {
						mark, color = "!", ui.Hex("#9f7f66")
						border, background = ui.Hex("#cfb5a3"), ui.Hex("#faf7f2")
					} else if event.State != "已完成" {
						mark, color = "·", ui.Hex("#839bb5")
						border, background = ui.Hex("#c1cede"), ui.Hex("#f3f6fb")
					}
					ui.Column(c).Width(17).Shrink(0).Padding(2, 0, 0, 0).AlignItems(ui.Start).Children(func() {
						ui.Column(c).Size(13, 13).Shrink(0).Border(1, border).Radius(7).Background(background).Justify(ui.Center).AlignItems(ui.Center).Children(func() { homeText(c, mark, homeMeta).FontSize(8).TextColor(color) })
						if idx < len(shown)-1 {
							ui.Column(c).Width(1).Grow(1).Margin(2, 0, -21, 6).Background(ui.Hex("#e4eaf0"))
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							homeText(c, event.Title, homeActivityTitle).Grow(1).MinWidth(0).MaxLines(2)
							homeText(c, event.At.In(now.Location()).Format("15:04"), homeMeta).TextColor(ui.Hex("#a1aab4")).Shrink(0)
						})
						label := event.Project + " · " + event.State
						if event.State == "失败" {
							label += " · " + eventFailureReason(event)
						}
						homeText(c, label, homeMeta).TextColor(homeMuted).Margin(5, 0, 0, 0)
						if event.State != "已完成" && event.State != "失败" {
							if index, ok := a.overviewActivityProject(event); ok {
								homeLink(c, "查看项目", func() { a.navigate("知识", index) }).AlignSelf(ui.Start).Justify(ui.Start)
							}
						}
						if event.State == "失败" {
							ui.Row(c).Margin(8, 0, 0, 0).Gap(8).Children(func() {
								title := "查看失败详情"
								if v.ExpandedEvent == event.Key {
									title = "收起失败详情"
								}
								homeLink(c, title, func() {
									if v.ExpandedEvent == event.Key {
										v.ExpandedEvent = ""
										return
									}
									v.ExpandedEvent = event.Key
									v.ExpandedReport = failureReport(event, a.Config)
								}).Key("failure-detail-" + event.Key)
								copyTitle := "复制诊断信息"
								if v.CopiedEvent == event.Key {
									copyTitle = "诊断信息已复制"
								}
								homeLink(c, copyTitle, func() {
									report := v.ExpandedReport
									if v.ExpandedEvent != event.Key {
										report = failureReport(event, a.Config)
									}
									c.WriteClipboard(report)
									v.CopiedEvent = event.Key
								}).Key("failure-copy-" + event.Key)
							})
							if v.ExpandedEvent == event.Key {
								homeText(c, v.ExpandedReport, homeMeta).LineHeight(1.6).Margin(8, 0, 0, 0)
							}
						}
					})
				})
			}
		})
	})
	ui.Row(c).MinHeight(24).Shrink(0).Gap(16).Margin(12, 0, 0, 0).Children(func() {
		if len(v.Events) > compactLimit {
			title := "查看全部动态"
			if v.ActivityExpanded {
				title = "收起动态"
			}
			homeLink(c, title, func() { v.ActivityExpanded = !v.ActivityExpanded; v.ActivityScroll.Y = 0 })
		}
		if v.More && (v.ActivityExpanded || len(v.Events) <= compactLimit) {
			homeLink(c, "加载更多动态", func() { v.ActivityExpanded = true; v.ActivityLimit += 20; a.loadOverviewActivity() }).Disabled(v.ActivityLoading)
		}
	})
}

func (a *App) overviewActivityProject(event overviewEvent) (int, bool) {
	// Activity labels are presentation only. Same-name projects must never
	// route through their label; only an unambiguous official dataset does.
	index := -1
	if event.Record.Dataset != "" {
		for i, p := range a.Config.Projects {
			if p.Dataset == event.Record.Dataset {
				if index >= 0 {
					return 0, false
				}
				index = i
			}
		}
	}
	return index, index >= 0
}

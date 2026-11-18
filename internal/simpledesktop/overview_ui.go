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
	SearchHistory                              bool
	History                                    []overviewEvent
	HistoryLoaded                              bool
	HistoryError                               string
	ActivityLimit                              int
	Day                                        string
	Offset                                     int
	Scroll                                     ui.ScrollState
	SidebarScroll                              ui.ScrollState
	ActivityScroll                             ui.ScrollState
	KnowledgeScroll                            ui.ScrollState
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
	a.overviewRefreshAt = time.Now().Add(overviewRefreshInterval)
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
			Day: v.Day, Offset: v.Offset, Scroll: v.Scroll, SidebarScroll: v.SidebarScroll, ActivityScroll: v.ActivityScroll, KnowledgeScroll: v.KnowledgeScroll, AllProjects: v.AllProjects, ActivityExpanded: v.ActivityExpanded, ActivityLimit: v.ActivityLimit, SearchHistory: v.SearchHistory}
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
			a.loadOverviewRecallStats(generation)
			// A new catalog may have the same IDs as the already preheated old
			// catalog. Let that publication consume cached titles again.
			a.recentScope = ""
			a.overviewState.Loaded = true
			a.overviewState.Error = ""
		})
	}()
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
	want := 50
	client := a.Client
	history := v.SearchHistory
	go func() {
		defer cancel()
		var events []overviewEvent
		var more bool
		var err error
		if history {
			events, err = client.overviewSearchHistory(ctx)
		} else {
			events, more, err = client.overviewActivity(ctx, want)
		}
		a.Update(func() {
			if generation != a.overviewState.activityGeneration {
				return
			}
			a.overviewState.ActivityLoading = false
			if history {
				if err != nil {
					a.overviewState.HistoryError = err.Error()
					return
				}
				a.overviewState.History = events
				a.overviewState.HistoryLoaded = true
				a.overviewState.HistoryError = ""
				return
			}
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
			if day, ok := item.Document.knowledgeDay(location); ok {
				days[day]++
			}
		}
	}
	return days
}

func (a *App) openOverviewItem(item Item) {
	for idx, p := range a.Config.Projects {
		if p.Dataset == item.Dataset {
			a.navigate("知识", idx)
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
	for idx, p := range a.Config.Common {
		if p.Dataset == item.Dataset {
			a.navigate("通用知识", idx)
			a.commonEdit.Category = ""
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
}

func (a *App) overviewHome(c *ui.Context) {
	v := &a.overviewState
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	c.After(midnight.Sub(now))
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).TrackScroll(&v.Scroll).Padding(pageTopInset, homePageInset, pageInset, homePageInset).Font("system-ui").FontWeight(400).Children(func() {
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
			ui.Row(c).Key("overview-welcome-module").Padding(24).Margin(0, 0, sectionGap, 0).Background(ui.Hex("#f0f5fb")).Border(1, ui.Hex("#dce6f2")).Radius(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					homeText(c, "YOUR MEMORY, CONNECTED", homeEyebrow).TextColor(ui.Hex("#7a91ae"))
					homeText(c, "每一次积累，都能找回。", homeWelcome).TextColor(ui.Hex("#365474"))
					homeText(c, "项目中的记忆与经验，在这里继续。", homeBody)
				})
				for idx, metric := range []struct{ label, value, unit string }{{"已使用", usage, "天"}, {"记忆", knowledge, "条"}, {"原始记录", raw, "条"}} {
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
			if v.Loading && !v.Loaded {
				homeText(c, "正在读取统计…", homeAction).Margin(0, 0, 12, 0)
			}
			if v.Error != "" {
				homeText(c, "统计读取失败，保留上次结果 · "+v.Error, homeAction).Margin(0, 0, 6, 0)
				projectLink(c, "重试读取统计", a.loadOverview)
			}
			if v.Snapshot.RawError != "" {
				homeText(c, "原始记录统计暂不可用 · "+v.Snapshot.RawError, homeAction).Margin(0, 0, 6, 0)
				projectLink(c, "重试读取记录统计", a.loadOverview)
			}
			a.overviewHeatmap(c, now)
			ui.Row(c).Key("overview-paired-sections").Gap(homeColumnGap).Margin(sectionGap, 0, sectionGap, 0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Key("overview-knowledge-section").Width(0).Grow(1.6).MinWidth(0).Gap(8).Children(func() {
					v := &a.overviewState
					ui.Row(c).Height(28).Shrink(0).Children(func() {
						title := "最近记忆"
						if v.Day != "" {
							title = v.Day + " 新增记忆"
						}
						homeText(c, title, homeHeading)
						ui.Spacer(c)
						if v.Day != "" {
							homeLink(c, "显示最近记忆", func() { v.Day = ""; v.KnowledgeScroll.Y = 0 })
						}
					})
					listSurface(c, func() { a.overviewKnowledge(c, now) })
				})
				ui.Column(c).Key("overview-activity-section").Width(0).Grow(1).MinWidth(255).Gap(8).Children(func() {
					ui.Row(c).Height(28).Children(func() { homeText(c, "近期动态", homeHeading); ui.Spacer(c); a.overviewActivityTabs(c) })
					listSurface(c, func() { a.overviewActivities(c, now) })
				})
			})
			a.overviewProjectGrid(c)
			if len(a.Config.Projects) == 0 {
				homeText(c, "尚未关联项目，请在设置中关联已有官方数据组。", homeBody)
			}
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
	ui.Column(c).Key("overview-heatmap-module").Gap(0).Padding(16).Border(1, homeBorder).Radius(12).Background(ui.Hex("#ffffff")).Children(func() {
		ui.Row(c).Margin(0, 0, 18, 0).Children(func() {
			ui.Spacer(c)
			homeText(c, start.Format("2006.01")+" — "+today.Format("2006.01"), homeReadableMeta)
		})
		ui.Column(c).FillWidth().MaxWidth(900).AlignSelf(ui.Center).Gap(8).Children(func() {
			ui.Row(c).FillWidth().MaxWidth(879).AlignSelf(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, now.Location())
				for !month.After(today) {
					homeText(c, fmt.Sprint(int(month.Month()))+"月", homeReadableMeta).FontSize(9).SingleLine().Shrink(0).TextColor(ui.Hex("#677383"))
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
						ui.Column(c).Height(0).Grow(1).Justify(ui.Center).Children(func() { homeText(c, label, homeReadableMeta).FontSize(8) })
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
								color := ui.Hex("#f0f2f5")
								if count > 0 {
									color = ui.Hex("#a9c9ee")
								}
								if count >= 3 {
									color = ui.Hex("#74a7e1")
								}
								if count >= 6 {
									color = ui.Hex("#4381cb")
								}
								if count >= 10 {
									color = ui.Hex("#245ba5")
								}
								if !visible {
									color = ui.Transparent
								}
								if !visible || count == 0 || !v.Loaded {
									ui.Column(c).Key("overview-empty-" + date).FillWidth().AspectRatio(1).Radius(2).Background(color)
									continue
								}
								label := fmt.Sprintf("%s · 新增 %d 条记忆", date, count)
								cell := ui.Button(c, "").Key("overview-day-" + date).Label(label).Tooltip(label).Padding(0).MinHeight(0).MinWidth(0).FillWidth().AspectRatio(1).Radius(2).BorderWidth(0).Background(color)
								if v.Day == date {
									cell.Border(1, ink)
								}
								cell.OnClick(func() { v.Day = date; v.KnowledgeScroll.Y = 0 })
							}
						})
					}
				})
			})
		})
		ui.Row(c).Margin(13, 0, 0, 0).Children(func() {
			text := "读取中…"
			if v.Loaded {
				text = fmt.Sprintf("今天新增 %d 条记忆", buckets[today.Format("2006-01-02")])
			}
			homeText(c, text, homeReadableMeta)
			ui.Spacer(c)
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, "少").FontSize(10).TextColor(homeMuted)
				for _, color := range []string{"#a9c9ee", "#74a7e1", "#4381cb", "#245ba5"} {
					ui.Column(c).Size(8, 8).Radius(2).Background(ui.Hex(color))
				}
				ui.Text(c, "多").FontSize(10).TextColor(homeMuted)
			})
		})
		if v.Snapshot.MissingDates > 0 {
			ui.Text(c, "部分记忆缺少来源日期，未计入热力图。").FontSize(10).TextColor(muted)
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
			day, ok := item.Document.knowledgeDay(now.Location())
			if !ok || day != v.Day {
				continue
			}
		}
		rows = append(rows, item)
		if len(rows) == 20 {
			break
		}
	}
	return rows
}

func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := a.overviewKnowledgeRows(now)
	a.startRecentTitles(rows)
	ui.Scroll(c.Key("overview-knowledge")).Label("最近记忆内容").Height(homeOverviewListHeight).Shrink(0).FillWidth().TrackScroll(&v.KnowledgeScroll).BorderWidth(0).Radius(0).Background(ui.Hex("#ffffff")).Padding(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		if v.KnowledgeScroll.MaxY-v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X + 1, Y: r.Y + r.H - 25, W: r.W - 6, H: 24}, ui.LinearGradient{From: ui.Hex("#ffffff").Alpha(0), To: ui.Hex("#ffffff"), Angle: 180}, 0)
		}
		if v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 6, H: 18}, ui.LinearGradient{From: ui.Hex("#ffffff"), To: ui.Hex("#ffffff").Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if !v.Loaded && v.Loading && len(rows) == 0 {
			overviewLoadingRows(c, "正在读取记忆", false)
			return
		}
		for index, item := range rows {
			i := item
			row := ui.Button(c, "").Key("recent-" + i.key()).Label(i.Title).FillWidth().Justify(ui.Start).AlignItems(ui.Center).MinHeight(56).Padding(8, 0).Radius(0).Gap(16).BorderWidth(0)
			if index < len(rows)-1 {
				row.BorderWidth(0, 0, 1, 0).BorderColor(homeBorder)
			}
			hover := ui.Hex("#f7f9fb")
			row.Background(homeHoverColor(row, "knowledge-surface", hover.Alpha(0), hover)).Children(func() {
				ui.Column(c).Size(24, 24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() { agentLogo(c, recentAgentIcon(i), 20) })
				ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
					homeText(c, knowledgePreview(i.Title), homeTitle).MaxLines(1).Tooltip(i.Title)
					date := "来源日期未知"
					if i.Document != nil {
						if day, ok := i.Document.knowledgeDay(now.Location()); ok {
							date = day
						}
					}
					ui.Row(c).Margin(4, 0, 0, 0).Gap(8).Children(func() {
						source := a.overviewSourceName(i)
						homeText(c, middleProjectName(source), homeReadableMeta).Tooltip("所属项目：" + source).MaxLines(1).TextColor(ui.Hex("#7d93a9"))
						recall, tip := knowledgeCardRecall(i)
						homeText(c, recall, homeReadableMeta).Tooltip(tip).Padding(2, 5).Radius(4).Background(ui.Hex("#edf5f1")).TextColor(ui.Hex("#5e8272"))
						if fields := knowledgeBasisFields(i); len(fields) > 0 {
							homeText(c, fields[0].Value, homeReadableMeta).Tooltip("记忆点分类："+fields[0].Value).Padding(2, 5).Radius(4).Background(ui.Hex("#f3eff8")).TextColor(ui.Hex("#847196"))
						}
						homeText(c, date, homeReadableMeta).TextColor(ui.Hex("#68717d"))
					})
				})
				ui.Icon(c, homeArrow).FontSize(12).TextColor(ui.Hex("#a9b4bf")).Margin(7, 0, 0, 0).Shrink(0)
			}).OnClick(func() { a.openOverviewItem(i) })
			if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
				button(c, "标题读取失败 · 重试", func() { a.retryRecentTitle(i) })
			}
		}
		if len(rows) == 0 {
			label := "暂无记忆"
			if !v.Loaded {
				label = "记忆尚未载入"
			}
			ui.Column(c).MinHeight(100).Justify(ui.Center).Children(func() { homeText(c, label, homeBody) })
		}
	})
}

func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	v := &a.overviewState
	events, loaded, loadError := v.Events, v.ActivityLoaded, v.ActivityError
	if v.SearchHistory {
		events, loaded, loadError = v.History, v.HistoryLoaded, v.HistoryError
	}
	events = events[:min(50, len(events))]
	ui.Scroll(c.Key("overview-activity")).Label("近期动态内容").Height(homeOverviewListHeight).Shrink(0).FillWidth().TrackScroll(&v.ActivityScroll).BorderWidth(0).Background(ui.Hex("#ffffff")).Radius(0).Padding(8, 8, 5, 8).Gap(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
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
		if !loaded && v.ActivityLoading && len(events) == 0 {
			overviewLoadingRows(c, "正在读取动态", true)
			return
		}
		if len(events) == 0 {
			ui.Column(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).Padding(20).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
				icon, title, hint := homeDocument, "暂无动态", "保存和整理记忆后，动态会显示在这里"
				if v.SearchHistory {
					icon, title, hint = homeSearch, "暂无搜索历史", "在搜索框中输入，记录会显示在这里"
				}
				if v.ActivityLoading && !loaded {
					title, hint = "正在读取动态…", ""
					if v.SearchHistory {
						title = "正在读取搜索历史…"
					}
				} else if loadError != "" {
					title, hint = "暂时无法读取", loadError
				}
				ui.Icon(c, icon).FontSize(24).TextColor(ui.Hex("#a7b5c4"))
				homeText(c, title, homeBody)
				if hint != "" {
					homeText(c, hint, homeReadableMeta).TextAlign(ui.Center).MaxLines(3)
				}
				if loadError != "" {
					homeLink(c, "重试", a.loadOverviewActivity)
				}
			})
			return
		}
		ui.Column(c).FillWidth().Shrink(0).Gap(0).Children(func() {
			if loadError != "" {
				homeText(c, "读取失败，保留上次结果 · "+loadError, homeReadableMeta).MaxLines(2)
				homeLink(c, "重试读取动态", a.loadOverviewActivity).Margin(0, 0, 12, 0)
			}
			shown := groupOverviewFailures(events)
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
						ui.Column(c).Size(13, 13).Shrink(0).Border(1, border).Radius(7).Background(background).Justify(ui.Center).AlignItems(ui.Center).Children(func() { homeText(c, mark, homeReadableMeta).FontSize(8).TextColor(color) })
						if idx < len(shown)-1 {
							ui.Column(c).Width(1).Grow(1).Margin(2, 0, -21, 6).Background(ui.Hex("#e4eaf0"))
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							homeText(c, event.Title, homeActivityTitle).TextColor(ui.Hex("#3f4d5c")).Grow(1).MinWidth(0).MaxLines(2)
							stamp := "时间未知"
							if !event.At.IsZero() {
								stamp = event.At.In(now.Location()).Format("15:04")
								if event.At.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02") {
									stamp = event.At.In(now.Location()).Format("2006/01/02 15:04")
								}
							}
							homeText(c, stamp, homeReadableMeta).TextColor(ui.Hex("#687483")).Shrink(0)
						})
						label := event.Project + " · " + event.State
						if event.State == "失败" {
							label += " · " + eventFailureReason(event)
						}
						homeText(c, label, homeReadableMeta).TextColor(ui.Hex("#596675")).Margin(5, 0, 0, 0)
						if event.State != "已完成" && event.State != "失败" {
							if index, ok := a.overviewActivityProject(event); ok {
								homeLink(c, "查看项目", func() { a.navigate("知识", index) }).AlignSelf(ui.Start).Justify(ui.Start)
							}
						}
						if event.State == "失败" {
							textButton(c, "更多", nil).Label("失败操作："+event.Key).FontSize(11).Padding(3, 0).BorderWidth(0).Background(ui.Transparent).AlignSelf(ui.Start).PopoverMenuButton(func(m *ui.Menu) {
								title := "查看失败详情"
								if v.ExpandedEvent == event.Key {
									title = "收起失败详情"
								}
								if m.Item(title).Chosen() {
									if v.ExpandedEvent == event.Key {
										v.ExpandedEvent = ""
									} else {
										v.ExpandedEvent = event.Key
										v.ExpandedReport = failureReport(event, a.Config)
									}
								}
								if m.Item("复制诊断信息").Chosen() {
									c.WriteClipboard(failureReport(event, a.Config))
									v.CopiedEvent = event.Key
								}
							})
							if v.ExpandedEvent == event.Key {
								homeText(c, v.ExpandedReport, homeReadableMeta).LineHeight(1.6).Margin(8, 0, 0, 0)
							}
						}
					})
				})
			}
		})
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

func (a *App) overviewActivityTabs(c *ui.Context) {
	v := &a.overviewState
	selected := 0
	if v.SearchHistory {
		selected = 1
	}
	previousTheme := c.Theme()
	compactTheme := *previousTheme
	compactTheme.FontSize, compactTheme.Spacing = 10, 2
	c.SetTheme(&compactTheme)
	ui.Segmented(c, &selected, "动态", "最近搜索").Label("近期动态类型").OnChange(func() {
		v.SearchHistory = selected == 1
		v.ActivityExpanded = false
		v.ActivityScroll.Y = 0
		a.loadOverviewActivity()
	})
	c.SetTheme(previousTheme)
}

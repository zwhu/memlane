package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
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
	RecallError                                string
	SearchHistory                              bool
	History                                    []overviewEvent
	HistoryLoaded                              bool
	HistoryError                               string
	ActivityLimit                              int
	Day                                        string
	ShowAllForDay                              bool
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
	cancel, activityCancel, recallCancel       context.CancelFunc
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
	a.cancelOverviewRecall()
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
	if !v.Loaded {
		a.hydrateOverviewFromCache(scope)
	}
	wasLoaded := v.Loaded
	if v.cancel != nil {
		v.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	v.cancel = cancel
	v.generation++
	generation := v.generation
	v.Loading = true
	client := a.Client
	titles := a.titles
	genStr := fmt.Sprint(generation)
	go func() {
		defer cancel()
		snapshot, err := client.overview(ctx)
		if err == nil && !wasLoaded && len(snapshot.Knowledge) > 0 {
			// On cold start without cache, preheat the first 10 visible titles
			// within a bounded 800ms budget so the first paint swaps atomically
			// from skeleton to real 2-line rich text with zero flicker.
			preCtx, preCancel := context.WithTimeout(ctx, 800*time.Millisecond)
			topN := min(10, len(snapshot.Knowledge))
			var wg sync.WaitGroup
			for idx := 0; idx < topN; idx++ {
				n := idx
				item := snapshot.Knowledge[n]
				if item.Document == nil || readableDocumentTitle(*item.Document) != "" {
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res, e := titles.read(preCtx, client, item, genStr, true, true); e == nil {
						snapshot.Knowledge[n].Title = overviewReadingTitle(res)
						snapshot.Knowledge[n].ReadingState = "ready"
					}
				}()
			}
			wg.Wait()
			preCancel()
		}
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
				if old, ok := ready[item.key()]; ok {
					if snapshot.Knowledge[n].ReadingState != "ready" && old.ReadingState == "ready" && item.Document != nil && (item.Document.ContentHash != "" || item.Document.Updated != "") && keyForReading(a.readingIdentity, old, genStr) == keyForReading(a.readingIdentity, item, genStr) {
						snapshot.Knowledge[n].Title, snapshot.Knowledge[n].ReadingState = old.Title, old.ReadingState
					}
					if snapshot.Knowledge[n].RecallStats == nil && old.RecallStats != nil {
						snapshot.Knowledge[n].RecallStats = old.RecallStats
					}
				}
			}
			a.overviewState.Snapshot = snapshot
			a.loadOverviewRecallStats(generation)
			// A new catalog may have the same IDs as the already preheated old
			// catalog. Let that publication consume cached titles again.
			a.recentScope = ""
			a.overviewState.Loaded = true
			a.overviewState.Error = ""
			if !wasLoaded {
				a.motionRuns.overviewKnowledge++
			}
			a.saveOverviewCache(scope, snapshot, a.overviewState.RawLoaded)
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
	control, hasControl := a.accessClient()
	go func() {
		defer cancel()
		var events []overviewEvent
		var more bool
		var err error
		var recallErrorText string
		if history {
			events, err = client.overviewSearchHistory(ctx)
		} else {
			events, more, err = client.overviewActivity(ctx, want)
			if err == nil && hasControl {
				recalls, recallMore, recallError := control.recallEvents(ctx, want)
				if recallError == nil {
					var mergedMore bool
					events, mergedMore = mergeRecallActivity(events, recalls, overviewProjects(client.Config), want)
					more = more || recallMore || mergedMore
				} else {
					recallErrorText = "召回明细暂不可用"
					for i := range events {
						if events[i].Record.Operation == "recall" && events[i].State == "已完成" {
							events[i].Title = "召回请求完成 · 明细未读取"
						}
					}
				}
			}
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
			a.overviewState.RecallError = recallErrorText
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
	page := ui.Column(c.Key("overview-page")).Fill().Padding(0, sp8).Font("system-ui").FontWeight(400)
	v4PageEnter(page, a.motionRuns.overviewPage)
	page.Children(func() {
		if _, err := firstUseDays(a.Config, now); err != nil {
			homeText(c, "使用时间暂不可用。", homeAction).Margin(0, 0, sp2, 0)
		}
		if v.Error != "" {
			message := "统计读取失败 · " + v.Error
			if v.Loaded {
				message = "未能刷新，显示上次统计 · " + v.Error
			}
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
				homeText(c, message, homeBody)
				projectLink(c, "重试读取统计", a.loadOverview)
			})
		}
		if v.Snapshot.RawError != "" {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
				homeText(c, "原始记录统计暂不可用 · "+v.Snapshot.RawError, homeAction)
				projectLink(c, "重试读取记录统计", a.loadOverview)
			})
		}
		a.overviewHeatmap(c, now)
		// V4: dual columns fill remaining height and scroll independently; no project cards.
		ui.Row(c).Key("overview-paired-sections").Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Key("overview-knowledge-section").Width(0).Grow(1.6).MinWidth(0).MinHeight(0).Padding(sp4, sp6, 0, 0).BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Gap(sp2).Children(func() {
				ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
					title := "最近记忆"
					if v.Day != "" {
						title = fmt.Sprintf("%s · %d 条记忆", v.Day, len(a.overviewKnowledgeRows(now)))
					}
					homeText(c, title, homeHeading)
					ui.Spacer(c)
					if v.Day != "" {
						homeLink(c, "返回最近记忆", func() { a.selectOverviewDay("") })
					}
				})
				a.overviewKnowledge(c, now)
				if v.Loaded && v.Day != "" {
					total := len(a.overviewKnowledgeRows(now))
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Padding(sp2, 0, sp3, 0).Shrink(0).Children(func() {
						homeText(c, fmt.Sprintf("已显示 %d 条，共 %d 条", len(a.overviewDisplayedRows(now)), total), homeReadableMeta)
						ui.Spacer(c)
						if total > 20 && !v.ShowAllForDay {
							homeLink(c, "显示全部", func() { v.ShowAllForDay = true })
						}
					})
				}
			})
			ui.Column(c).Key("overview-activity-section").Width(0).Grow(1).MinWidth(280).MinHeight(0).Padding(sp4, 0, 0, sp6).Gap(sp2).Children(func() {
				ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
					homeText(c, "近期动态", homeHeading)
					ui.Spacer(c)
					a.overviewActivityTabs(c)
				})
				a.overviewActivities(c, now)
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
	weeks := 0
	for day := gridStart; !day.After(today); day = day.AddDate(0, 0, 7) {
		weeks++
	}
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
	// V4: stats merged into heatmap header; no card frame or shadow, separated by hairline divider.
	ui.Column(c).Key("overview-heatmap-module").FillWidth().Shrink(0).Padding(sp2, 0, sp5, 0).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Gap(sp3).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
			homeText(c, "记忆分布", homeHeading).Tooltip("日期优先取自原始记录，不代表整理完成时间。")
			homeText(c, start.Format("2006.01")+" — "+today.Format("2006.01"), homeReadableMeta)
			ui.Spacer(c)
			for idx, metric := range []struct{ value, unit, label string }{{usage, "天", "已使用"}, {knowledge, "条", "记忆"}, {raw, "条", "原始记录"}} {
				stat := ui.Row(c).Key("overview-stat-"+metric.label).AlignItems(ui.Baseline).Gap(4).Shrink(0)
				if idx > 0 {
					stat.Margin(0, 0, 0, sp6).Padding(0, 0, 0, sp6).BorderWidth(0, 0, 0, 1).BorderColor(tok.Border)
				}
				stat.Children(func() {
					homeText(c, metric.value, homeStatValue)
					homeText(c, metric.unit+" "+metric.label, homeStatUnit)
				})
			}
		})
		ui.Row(c).FillWidth().Gap(sp6).AlignItems(ui.End).Children(func() {
			// Left: heatmap grid (11px cells, 3px gap).
			ui.Column(c).Shrink(0).Gap(4).Children(func() {
				ui.Row(c).Padding(0, 0, 0, 18).Gap(3).Height(14).Children(func() {
					for week := 0; week < weeks; week++ {
						label := ""
						if week == 0 {
							nxt := start.AddDate(0, 0, 21)
							if nxt.Month() == start.Month() {
								label = fmt.Sprint(int(start.Month())) + "月"
							}
						} else {
							for weekday := 0; weekday < 7; weekday++ {
								day := gridStart.AddDate(0, 0, week*7+weekday)
								if day.Day() == 1 && !day.After(today) {
									label = fmt.Sprint(int(day.Month())) + "月"
								}
							}
						}
						ui.Column(c).Width(11).Height(14).Children(func() {
							if label != "" {
								text := homeText(c, label, homeReadableMeta).Absolute().Top(0).Width(32).SingleLine()
								if week >= weeks-2 {
									text.Right(0)
								} else {
									text.Left(0)
								}
							}
						})
					}
				})
				ui.Row(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
					ui.Column(c).Width(12).Shrink(0).Gap(3).Children(func() {
						for weekday := 0; weekday < 7; weekday++ {
							label := ""
							switch weekday {
							case 1:
								label = "一"
							case 3:
								label = "三"
							case 5:
								label = "五"
							}
							ui.Column(c).Height(11).Justify(ui.Center).Children(func() {
								ui.Text(c, label).Font("system-ui").FontSize(9).TextColor(tok.Text3)
							})
						}
					})
					ui.Row(c).Gap(3).Children(func() {
						for week := 0; week < weeks; week++ {
							w := week
							col := ui.Column(c).Width(11).Gap(3)
							col.Children(func() {
								for weekday := 0; weekday < 7; weekday++ {
									day := gridStart.AddDate(0, 0, w*7+weekday)
									date := day.Format("2006-01-02")
									count := buckets[date]
									visible := !day.Before(start) && !day.After(today)
									level := 0
									switch {
									case count >= 10:
										level = 4
									case count >= 6:
										level = 3
									case count >= 3:
										level = 2
									case count > 0:
										level = 1
									}
									color := tok.Heat[level]
									if !visible {
										color = ui.Transparent
									}
									if !visible || !v.Loaded {
										ui.Box(c).Key("overview-empty-" + date).Size(11, 11).Radius(2).Background(color)
										continue
									}
									label := fmt.Sprintf("%s · 新增 %d 条记忆", date, count)
									cell := ui.ButtonBase(c.Key("overview-day-"+date)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
										Label(label).Tooltip(label).Size(11, 11).Radius(2).BorderWidth(0).Background(color)
									if v.Day == date {
										cell.Border(1.5, tok.Accent)
									}
									pose := ui.Pose{Scale: 1}
									if cell.Pressed() {
										pose.Scale = 0.72
									} else if cell.Hovered() {
										pose.Scale = 1.32
									}
									cell.AnimatePose(pose, v4Bouncy)
									cell.OnClick(func() {
										if v.Day == date {
											a.selectOverviewDay("")
										} else {
											a.selectOverviewDay(date)
										}
									})
								}
							})
						}
					})
				})
			})
			// Right: today summary + legend.
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.End).Gap(sp2).Padding(0, 0, 2, 0).Children(func() {
				if !v.Loaded {
					homeText(c, "读取中…", homeBody)
				} else {
					n := buckets[today.Format("2006-01-02")]
					ui.Row(c).Label(fmt.Sprintf("今天新增 %d 条", n)).AlignItems(ui.Baseline).Gap(3).Children(func() {
						homeText(c, "今天新增", homeBody)
						ui.Text(c, fmt.Sprint(n)).Font("system-ui").FontSize(fsFootnote).FontWeight(550).FontFeatures("tnum").TextColor(tok.Text1)
						homeText(c, "条", homeBody)
					})
				}
				ui.Row(c).AlignItems(ui.Center).Gap(3).Children(func() {
					ui.Text(c, "少").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
					for level := 1; level <= 4; level++ {
						ui.Box(c).Size(9, 9).Radius(2).Background(tok.Heat[level])
					}
					ui.Text(c, "多").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
				})
			})
		})
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
	}
	return rows
}

func (a *App) selectOverviewDay(day string) {
	v := &a.overviewState
	v.Day, v.ShowAllForDay = day, false
	v.KnowledgeScroll.Y = 0
	a.motionRuns.overviewKnowledge++
}

func (a *App) overviewDisplayedRows(now time.Time) []Item {
	rows := a.overviewKnowledgeRows(now)
	if a.overviewState.Day == "" || !a.overviewState.ShowAllForDay {
		return rows[:min(20, len(rows))]
	}
	return rows
}

func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := a.overviewDisplayedRows(now)
	a.startRecentTitles(rows)
	ui.Scroll(c.Key("overview-knowledge")).Label("最近记忆内容").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.KnowledgeScroll).BorderWidth(0).Radius(0).Background(tok.Bg).Padding(0, 0, sp4, 0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		bg := tok.Bg
		if v.KnowledgeScroll.MaxY-v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H - 24, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W - 6, H: 16}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if !v.Loaded && v.Loading && len(rows) == 0 {
			overviewLoadingRows(c, "正在读取记忆", false)
			return
		}
		prevHovered := false
		for index, item := range rows {
			i := item
			idx := index
			row := ui.ButtonBase(c.Key("recent-"+i.key())).Role(ui.RoleButton).Cursor(ui.CursorPointer).
				Label(i.Title).FillWidth().Justify(ui.Start).AlignItems(ui.Center).
				MinHeight(rowHeight2).Padding(sp2+2, sp2).Radius(radius).Gap(sp3).BorderWidth(0)
			v4StaggerEnter(row, idx, a.motionRuns.overviewKnowledge)
			hovered := row.Hovered()
			hoverBg := tok.BgElevated
			if tok.Dark {
				hoverBg = tok.Fill1
			}
			row.Background(homeHoverColor(row, "knowledge-surface", ui.Transparent, hoverBg))
			if hovered {
				row.Shadow(0, 2, 10, 0, tok.ShadowLift)
			}
			v4RowInteractive(row)
			// Hairline divider between rows starting after the agent icon.
			// Hide both above and below a hovered lifted row (.row:hover + .row::before).
			if idx > 0 && !hovered && !prevHovered {
				row.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X + 42, Y: r.Y, W: max(0, r.W-50), H: 1}, tok.Border, 0)
				})
			}
			prevHovered = hovered
			row.Children(func() {
				ui.Column(c).Size(22, 22).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					agentLogo(c, recentAgentIcon(i), 20)
				})
				ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
					v4InlineMarkdown(c, knowledgePreview(i.Title), fsBody, 500, 1.45, tok.Text1).MaxLines(2).Tooltip(stripInlineBackticks(knowledgePreview(i.Title)))
					date := "来源日期未知"
					if i.Document != nil {
						if day, ok := i.Document.knowledgeDay(now.Location()); ok {
							date = day
						}
					}
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
						if a.itemIsUnread(i) {
							v4UnreadDot(c)
						}
						source := a.overviewSourceName(i)
						homeText(c, middleProjectName(source), homeReadableMeta).Tooltip("所属项目：" + source).MaxLines(1)
						v4RecallChip(c, i)
						if fields := knowledgeBasisFields(i); len(fields) > 0 {
							v4CategoryChip(c, fields[0].Value)
						}
						homeText(c, date, homeReadableMeta)
					})
				})
				chevColor := tok.Text4.Alpha(0)
				if hovered {
					chevColor = tok.Text4
				}
				ui.Icon(c, v4IconChev).FontSize(13).TextColor(row.AnimateColor("chev-col", chevColor, v4Fast)).Shrink(0)
			}).OnClick(func() { a.openOverviewItem(i) })
			if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
				button(c, "标题读取失败 · 重试", func() { a.retryRecentTitle(i) })
			}
		}
		if len(rows) == 0 {
			label := "暂无记忆"
			if !v.Loaded {
				label = "记忆尚未载入"
				if v.Error != "" {
					label = "记忆读取失败"
				}
			} else if v.Day != "" {
				label = "这一天没有记忆"
			}
			v4EmptyState(c, "overview-knowledge-empty", label, "")
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
			overviewLoadingRows(c, "正在读取动态", true)
			return
		}
		if len(events) == 0 {
			title, hint := "暂无动态", "保存和整理记忆后，动态会显示在这里"
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
				v4EmptyState(c, "overview-activity-empty", title, hint)
				if loadError != "" {
					ui.Row(c).FillWidth().Justify(ui.Center).Children(func() { homeLink(c, "重试", a.loadOverviewActivity) })
				}
			})
			return
		}
		ui.Column(c).FillWidth().Shrink(0).Gap(0).Children(func() {
			if !v.SearchHistory && v.RecallError != "" {
				homeText(c, v.RecallError, homeReadableMeta)
				homeLink(c, "重试", a.loadOverviewActivity).Margin(0, 0, sp2, 0)
			}
			if loadError != "" {
				homeText(c, "读取失败，保留上次结果 · "+loadError, homeReadableMeta).MaxLines(2)
				homeLink(c, "重试读取动态", a.loadOverviewActivity).Margin(0, 0, sp2, 0)
			}
			shown := groupOverviewFailures(events)
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
				var recallGroups []recallActivityGroup
				if ev.Recall != nil {
					recallGroups = a.recallActivityGroups(*ev.Recall)
				}
				var row ui.Element
				if ev.Recall != nil && len(recallGroups) > 0 {
					btn := ui.ButtonBase(c.Key("overview-event-"+ev.Key)).Role(ui.RoleButton).
						Label(fmt.Sprintf("%s · %s · %d 条记忆", ev.Title, ev.Project, len(ev.Recall.Hits))).
						FillWidth().Justify(ui.Start).AlignItems(ui.Stretch).
						Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).BorderWidth(0).
						Cursor(ui.CursorPointer).Tooltip("查看本次召回的记忆")
					btn.Background(homeHoverColor(btn, "ev-hover", ui.Transparent, tok.Hover))
					v4ButtonPress(btn, 0.99)
					if len(recallGroups) == 1 {
						ds := recallGroups[0].dataset
						rc := *ev.Recall
						btn.OnClick(func() { a.openRecalledCards(rc, ds) })
					} else {
						rc := *ev.Recall
						groupsCopy := recallGroups
						btn.PopoverMenuButton(func(m *ui.Menu) {
							for _, g := range groupsCopy {
								if m.Item(fmt.Sprintf("%s · %d 条", g.name, g.count)).Chosen() {
									a.openRecalledCards(rc, g.dataset)
								}
							}
						})
					}
					row = btn
				} else {
					row = ui.Row(c).Key("overview-event-"+ev.Key).Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).AlignItems(ui.Stretch)
				}
				v4StaggerEnter(row, idx, a.motionRuns.overviewActivity)
				row.Children(func() {
					ui.Column(c).Width(8).Shrink(0).Padding(6, 0, 0, 0).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(dotColor).Shrink(0)
						if idx < len(shown)-1 {
							ui.Box(c).Width(1).Grow(1).Margin(4, 0, -18, 0).Background(tok.Border)
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
							homeText(c, ev.Title, homeActivityTitle).Grow(1).MinWidth(0).MaxLines(2)
							if running {
								v4StageBlocks(c)
							}
							stamp := "时间未知"
							if !ev.At.IsZero() {
								stamp = ev.At.In(now.Location()).Format("15:04")
								if ev.At.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02") {
									stamp = ev.At.In(now.Location()).Format("2006/01/02 15:04")
								}
							}
							homeText(c, stamp, homeReadableMeta).FontFeatures("tnum").Shrink(0)
						})
						if ev.State == "失败" {
							ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
								homeText(c, ev.Project+" ·", homeReadableMeta)
								homeText(c, eventFailureReason(ev), homeReadableMeta).TextColor(tok.Danger).MaxLines(1)
							})
						} else {
							label := ev.Project + " · " + ev.State
							if ev.Recall != nil {
								label = fmt.Sprintf("%s · %d 条记忆", ev.Project, len(ev.Recall.Hits))
							}
							homeText(c, label, homeReadableMeta)
						}
						if ev.Recall != nil && len(recallGroups) == 0 {
							homeText(c, "记忆库暂不可用", homeMeta)
						}
						if running {
							if index, ok := a.overviewActivityProject(ev); ok {
								homeLink(c, "查看项目", func() { a.navigate("知识", index) }).AlignSelf(ui.Start).Justify(ui.Start)
							}
						}
						if ev.State == "失败" {
							textButton(c, "更多", nil).Label("失败操作："+ev.Key).FontSize(fsCaption).TextColor(tok.Text3).Padding(2, 0).BorderWidth(0).Background(ui.Transparent).AlignSelf(ui.Start).PopoverMenuButton(func(m *ui.Menu) {
								title := "查看失败详情"
								if v.ExpandedEvent == ev.Key {
									title = "收起失败详情"
								}
								if m.Item(title).Chosen() {
									if v.ExpandedEvent == ev.Key {
										v.ExpandedEvent = ""
									} else {
										v.ExpandedEvent = ev.Key
										v.ExpandedReport = failureReport(ev, a.Config)
									}
								}
								if m.Item("复制诊断信息").Chosen() {
									c.WriteClipboard(failureReport(ev, a.Config))
									v.CopiedEvent = ev.Key
								}
							})
							if v.ExpandedEvent == ev.Key {
								homeText(c, v.ExpandedReport, homeReadableMeta).LineHeight(1.6).Margin(sp2, 0, 0, 0)
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
	v4Segmented(c, "overview-activity-tabs", "近期动态类型", &selected, []string{"动态", "最近搜索"}, func() {
		v.SearchHistory = selected == 1
		v.ActivityExpanded = false
		v.ActivityScroll.Y = 0
		a.motionRuns.overviewActivity++
		a.loadOverviewActivity()
	})
}

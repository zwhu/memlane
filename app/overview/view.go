package overview

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *ControllerOf[T]) RenderHome(c *ui.Context) {
	tok := a.tokens()
	v := a.State
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	if !a.windowHidden() {
		c.After(max(midnight.Sub(now), 500*time.Millisecond))
	}
	ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
		pageKey := fmt.Sprintf("overview-page-%d", v.PageRun)
		page := ui.Column(c.Key(pageKey)).Grow(1).MinHeight(0).FillWidth().MaxWidth(1040).Padding(0, sp8).Font("system-ui").FontWeight(400)
		a.pageEnter(c, page, "overview-page", v.PageRun)
		page.Children(func() {
			if a.RenderTopBanners != nil {
				a.RenderTopBanners(c)
			}
			if _, err, ready := a.FirstUseDays(now); ready && err != nil && !a.setupOnly() {
				kitui.Text(c, tok, "使用时间暂不可用。", homeAction).Margin(0, 0, sp2, 0)
			}
			if v.Error != "" {
				message := "统计读取失败 · " + v.Error
				if v.Loaded {
					message = "未能刷新，显示上次统计 · " + v.Error
				}
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
					kitui.Text(c, tok, message, homeBody)
					kitui.TextButton(c, tok, "重试读取统计", func() { a.reload() })
				})
			}
			if v.Snapshot.RawError != "" {
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
					kitui.Text(c, tok, "原始记录统计暂不可用 · "+v.Snapshot.RawError, homeAction)
					kitui.TextButton(c, tok, "重试读取记录统计", func() { a.reload() })
				})
			}
			a.RenderHeatmap(c, now)
			// V4: dual columns fill remaining height and scroll independently; no project cards.
			ui.Row(c).Key("overview-paired-sections").Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Key("overview-knowledge-section").Width(0).Grow(1.6).MinWidth(0).MinHeight(0).Padding(sp4, sp6, 0, 0).BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Gap(sp2).Children(func() {
					ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
						title := "最近记忆"
						if v.Day != "" {
							dayCount := len(a.KnowledgeRows(now))
							if v.V2Mode {
								dayCount = v.DayTotal
								if dayCount == 0 && v.V2DayBuckets[v.Day] > 0 {
									dayCount = v.V2DayBuckets[v.Day]
								}
							}
							title = fmt.Sprintf("%s · %d 条记忆", v.Day, dayCount)
						}
						kitui.Text(c, tok, title, homeHeading)
						ui.Spacer(c)
						if v.Day != "" {
							kitui.TextButton(c, tok, "返回最近记忆", func() { a.SelectDay("") })
						}
					})
					a.RenderKnowledge(c, now)
					if v.Loaded && v.Day != "" {
						displayed := len(a.DisplayedRows(now))
						total := len(a.KnowledgeRows(now))
						if v.V2Mode {
							total = max(v.DayTotal, displayed)
						}
						ui.Row(c).AlignItems(ui.Center).Gap(sp2).Padding(sp2, 0, sp3, 0).Shrink(0).Children(func() {
							kitui.Text(c, tok, fmt.Sprintf("已显示 %d 条，共 %d 条", displayed, total), homeReadableMeta)
							ui.Spacer(c)
							if v.V2Mode {
								if v.DayHasMore && !v.DayLoading {
									kitui.TextButton(c, tok, "加载更多", func() { a.LoadDay(v.Day, true) })
								} else if v.DayLoading {
									kitui.Text(c, tok, "正在加载…", homeMeta)
								}
							} else if total > 20 && !v.ShowAllForDay {
								kitui.TextButton(c, tok, "显示全部", func() { v.ShowAllForDay = true })
							}
						})
					}
				})
				ui.Column(c).Key("overview-activity-section").Width(0).Grow(1).MinWidth(280).MinHeight(0).Padding(sp4, 0, 0, sp6).Gap(sp2).Children(func() {
					ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
						kitui.Text(c, tok, "近期动态", homeHeading)
						ui.Spacer(c)
						a.activityTabs(c)
					})
					a.RenderActivities(c, now)
				})
			})
		})
	})
	a.RenderFailureDialog(c)
}
func (a *ControllerOf[T]) knowledgeRow(c *ui.Context, now time.Time, item T, idx int, prevHovered bool) bool {
	tok := a.tokens()
	v := a.State
	i := a.Describe(item)
	rowKey := fmt.Sprintf("recent-%d-%s", v.KnowledgeRun, i.Key())
	row := ui.ButtonBase(c.Key(rowKey)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label(i.Title).FillWidth().Justify(ui.Start).AlignItems(ui.Center).
		MinHeight(rowHeight2).Padding(sp2+2, sp2).Radius(radius).Gap(sp3).BorderWidth(0)
	hovered := row.Hovered()
	hoverBg := tok.BgElevated
	if tok.Dark {
		hoverBg = tok.Fill1
	}
	row.Background(kitui.HoverColor(row, "knowledge-surface", ui.Transparent, hoverBg))
	if hovered {
		row.Shadow(0, 2, 10, 0, tok.ShadowLift)
	}
	a.staggerEnter(c, row, "recent-"+i.Key(), idx, v.KnowledgeRun, true)
	// Hairline divider between rows starting after the agent icon.
	// Hide both above and below a hovered lifted row (.row:hover + .row::before).
	if idx > 0 && !hovered && !prevHovered {
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 42, Y: r.Y, W: max(0, r.W-50), H: 1}, tok.Border, 0)
		})
	}
	row.Children(func() {
		ui.Column(c).Size(22, 22).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if a.RenderAgentIcon != nil {
				a.RenderAgentIcon(c, item)
			} else {
				ui.Icon(c, kitui.IconDoc).FontSize(20).TextColor(tok.Text3)
			}
		})
		ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
			// Lock a 2-line title slot height so async title hydration never
			// stretches the row downward or shifts rows below it.
			titlePending := i.TitlePending
			ui.Column(c).FillWidth().MinHeight(38).Justify(ui.Center).Children(func() {
				if titlePending {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						ui.Box(c).FillWidth().Height(12).Radius(4).Background(tok.Fill2)
						ui.Box(c).Width(220).Height(12).Radius(4).Background(tok.Fill1)
					})
				} else {
					kitui.InlineMarkdown(c, tok, KnowledgePreview(i.Title), fsBody, 500, 1.45, tok.Text1).MaxLines(2).Tooltip(stripInlineBackticks(KnowledgePreview(i.Title)))
				}
			})
			date, ok := a.date(item, now.Location())
			if !ok {
				date = "来源日期未知"
			}
			ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
				if a.unread(item) {
					kitui.UnreadDot(c, tok, "未读", "未读记忆")
				}
				source := a.sourceName(item)
				kitui.Text(c, tok, MiddleProjectName(source), homeReadableMeta).Tooltip("所属项目：" + source).MaxLines(1)
				if a.RenderItemBadges != nil {
					a.RenderItemBadges(c, item)
				}
				kitui.Text(c, tok, date, homeReadableMeta)
			})
		})
		chevColor := tok.Text4.Alpha(0)
		if hovered {
			chevColor = tok.Text4
		}
		ui.Icon(c, kitui.IconChev).FontSize(13).TextColor(row.AnimateColor("chev-col", chevColor, kitui.Fast)).Shrink(0)
	}).OnClick(func() { a.openItem(item) })
	if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
		kitui.TextButton(c, tok, "标题读取失败 · 重试", func() { a.retryTitle(item) })
	}
	return hovered
}

func (a *ControllerOf[T]) RenderKnowledge(c *ui.Context, now time.Time) {
	tok := a.tokens()
	v := a.State
	rows := a.DisplayedRows(now)
	if !v.V2Mode {
		if a.PrepareTitles != nil {
			a.PrepareTitles(rows)
		}
	}
	if v.V2Mode && v.Day != "" && len(rows) > 0 {
		if v.DayHasMore && !v.DayLoading && v.DayError == "" && v.KnowledgeScroll.MaxY > 0 && v.KnowledgeScroll.Y >= v.KnowledgeScroll.MaxY-160 {
			a.LoadDay(v.Day, true)
		}
		ui.Column(c.Key("overview-knowledge-v2-day")).Grow(1).MinHeight(0).FillWidth().Background(tok.Bg).Children(func() {
			if v.DayError != "" {
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Padding(sp2, sp2).Shrink(0).Children(func() {
					kitui.Text(c, tok, "读取失败 · "+v.DayError, homeMeta).MaxLines(2)
					kitui.TextButton(c, tok, "重试", func() { a.LoadDay(v.Day, false) })
				})
			}
			v.DayList.Key = func(n int) any { return a.Describe(rows[n]).Key() }
			v.DayList.KeepOffset = true
			v.DayList.Label = func(n int) string { return a.Describe(rows[n]).Title }
			ui.List(c.Key("overview-day-list-"+v.Day), &v.DayList, len(rows), func(n int) {
				a.knowledgeRow(c, now, rows[n], n, false)
			}).Label("当日记忆列表").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.KnowledgeScroll).Padding(0, 0, sp4, 0)
		})
		return
	}
	ui.Scroll(c.Key("overview-knowledge")).Label("最近记忆内容").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.KnowledgeScroll).BorderWidth(0).Radius(0).Background(tok.Bg).Padding(0, 0, sp4, 0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		bg := tok.Bg
		if v.KnowledgeScroll.MaxY-v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H - 24, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W - 6, H: 16}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if ((!v.Loaded && v.Loading) || (v.V2Mode && v.Day != "" && v.DayLoading)) && len(rows) == 0 {
			loadingRows(c, tok, "正在读取记忆", false)
			return
		}
		if v.V2Mode && v.Day != "" && v.DayError != "" {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Padding(sp2, sp2).Children(func() {
				kitui.Text(c, tok, "读取失败 · "+v.DayError, homeMeta).MaxLines(2)
				kitui.TextButton(c, tok, "重试", func() { a.LoadDay(v.Day, false) })
			})
		}
		prevHovered := false
		for index, item := range rows {
			prevHovered = a.knowledgeRow(c, now, item, index, prevHovered)
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
			kitui.EmptyState(c, tok, "overview-knowledge-empty", label, "")
		}
	})
}

func (a *ControllerOf[T]) openItem(item T) {
	if a.OpenItem != nil {
		a.OpenItem(item)
	} else if a.Router != nil {
		i := a.Describe(item)
		if err := a.Router.OpenItem(shell.ItemTarget{ProjectID: i.ProjectID, DatasetID: i.Dataset, ItemID: i.ID,
			Kind: i.Kind, SourceDocument: i.SourceDocument, Title: i.Title, Date: i.Date, Preview: i.Preview}); err != nil {
			a.State.Error = a.message(err)
		}
	}
}

func (a *ControllerOf[T]) retryTitle(item T) {
	if a.RetryTitle != nil {
		a.RetryTitle(item)
	}
}

func (a *ControllerOf[T]) unread(item T) bool {
	if a.ItemIsUnread != nil {
		return a.ItemIsUnread(item)
	}
	return a.Describe(item).Unread
}

func (a *ControllerOf[T]) sourceName(item T) string {
	if a.SourceName != nil {
		return a.SourceName(item)
	}
	for _, p := range a.projects() {
		if p.DatasetID == a.Describe(item).Dataset {
			return p.Name
		}
	}
	return "未知项目"
}

func stripInlineBackticks(s string) string { return strings.ReplaceAll(s, "`", "") }

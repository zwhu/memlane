package overview

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *ControllerOf[T]) RenderHeatmap(c *ui.Context, now time.Time) {
	tok := a.tokens()
	v := a.State
	buckets := a.CachedBuckets(now)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := today.AddDate(0, 0, -364)
	gridStart := start.AddDate(0, 0, -int(start.Weekday()))
	weeks := 0
	for day := gridStart; !day.After(today); day = day.AddDate(0, 0, 7) {
		weeks++
	}
	usage := "—"
	if n, err, ready := a.FirstUseDays(now); ready && err == nil {
		usage = fmt.Sprint(n)
	}
	knowledge, raw := "—", "—"
	if v.Loaded {
		if v.V2Mode {
			knowledge = fmt.Sprint(v.V2TotalKnowledge)
		} else {
			knowledge = fmt.Sprint(len(v.Snapshot.Knowledge))
		}
	}
	if v.RawLoaded {
		raw = fmt.Sprint(v.Snapshot.Raw)
	}
	// V4: stats merged into heatmap header; no card frame or shadow, separated by hairline divider.
	ui.Column(c).Key("overview-heatmap-module").FillWidth().Shrink(0).Padding(sp2, 0, sp5, 0).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Gap(sp3).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
			kitui.Text(c, tok, "记忆分布", homeHeading).Tooltip("日期优先取自原始记录，不代表提炼完成时间。")
			kitui.Text(c, tok, start.Format("2006.01")+" — "+today.Format("2006.01"), homeReadableMeta)
			ui.Spacer(c)
			for idx, metric := range []struct{ value, unit, label string }{{usage, "天", "已使用"}, {knowledge, "条", "记忆"}, {raw, "条", "原始记录"}} {
				stat := ui.Row(c).Key("overview-stat-" + metric.label).AlignItems(ui.Baseline).Gap(4).Shrink(0)
				if idx > 0 {
					stat.Margin(0, 0, 0, sp6).Padding(0, 0, 0, sp6).BorderWidth(0, 0, 0, 1).BorderColor(tok.Border)
				}
				stat.Children(func() {
					kitui.Text(c, tok, metric.value, homeStatValue)
					kitui.Text(c, tok, metric.unit+" "+metric.label, homeStatUnit)
				})
			}
		})
		ui.Row(c).FillWidth().Gap(sp6).AlignItems(ui.End).Children(func() {
			// Left: heatmap grid painted in a single canvas node (11px cells, 3px gap)
			// instead of 371 separate ButtonBase widgets so Overview builds in ~1ms.
			gridW := float32(weeks*14 - 3)
			gridH := float32(7*14 - 3)
			ui.Column(c).Shrink(0).Gap(4).Children(func() {
				// Month labels row: only emit actual month ticks (12 texts, not 53 columns).
				ui.Box(c).Width(gridW + 18).Height(14).Children(func() {
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
						if label != "" {
							t := kitui.Text(c, tok, label, homeReadableMeta).Absolute().Top(0).SingleLine()
							if week >= weeks-2 {
								t.Right(0)
							} else {
								t.Left(18 + float32(week*14))
							}
						}
					}
				})
				ui.Row(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
					ui.Box(c).Width(12).Height(gridH).Shrink(0).Children(func() {
						for _, wd := range []struct {
							row   int
							label string
						}{{1, "一"}, {3, "三"}, {5, "五"}} {
							ui.Text(c, wd.label).Font(kitui.FontUI).FontSize(9).TextColor(tok.Text3).
								Absolute().Left(0).Top(float32(wd.row * 14)).SingleLine()
						}
					})
					canvas := ui.Box(c.Key("overview-heatmap-canvas")).Label("记忆分布热力图").Size(gridW, gridH).Shrink(0)
					hoverW, hoverWD := -1, -1
					hoveredDate := ""
					hoveredLabel := ""
					if v.Loaded {
						if px, py, ok := canvas.PointerPosition(); ok && px >= 0 && py >= 0 {
							w := int(px) / 14
							wd := int(py) / 14
							if w >= 0 && w < weeks && wd >= 0 && wd < 7 && px-float32(w*14) <= 12 && py-float32(wd*14) <= 12 {
								day := gridStart.AddDate(0, 0, w*7+wd)
								if !day.Before(start) && !day.After(today) {
									hoverW, hoverWD = w, wd
									hoveredDate = day.Format("2006-01-02")
									hoveredLabel = fmt.Sprintf("%s · 新增 %d 条记忆", hoveredDate, buckets[hoveredDate])
								}
							}
						}
					}
					if hoveredLabel != "" {
						canvas.Cursor(ui.CursorPointer)
					}
					if v.Loaded {
						canvas.OnClick(func() {
							if hoveredDate == "" {
								return
							}
							if v.Day == hoveredDate {
								a.SelectDay("")
							} else {
								a.SelectDay(hoveredDate)
							}
						})
					}
					cells := a.CachedHeatmapCells(now, today, start, gridStart, weeks, buckets)
					selDay := v.Day
					loaded := v.Loaded
					if loaded && v.HeatmapGrowStart.IsZero() && v.PageRun > 0 {
						a.State.HeatmapGrowStart = c.Now()
					}
					if v.heatmapTapPending {
						a.State.heatmapTapPending = false
						a.State.heatmapTapStart = c.Now()
					}
					if v.heatmapRingPending {
						a.State.heatmapRingPending = false
						a.State.heatmapRingStart = c.Now()
					}
					growStart := time.Time{}
					if loaded {
						growStart = a.State.HeatmapGrowStart
					}
					tapDay := v.heatmapTapDay
					tapStart := v.heatmapTapStart
					ringPrevDay := v.heatmapRingPrevDay
					ringStart := v.heatmapRingStart
					coldStart := v.ColdStartMotion
					weekDelay, waveDuration := 7*time.Millisecond, 420*time.Millisecond
					if coldStart {
						weekDelay, waveDuration = 3*time.Millisecond, 320*time.Millisecond
					}
					waveTotal := time.Duration(weeks-1)*weekDelay + waveDuration
					canvas.Draw(func(p *ui.Painter, r ui.Rect) {
						elapsed, waving := a.waveElapsed(c, p, growStart, waveTotal)
						tapElapsed, tapping := a.waveElapsed(c, p, tapStart, 650*time.Millisecond)
						ringElapsed, ringing := a.waveElapsed(c, p, ringStart, 140*time.Millisecond)
						lastW := -1
						var x, scale, alpha float32
						skipWeek := false
						for _, cell := range cells {
							if cell.Week != lastW {
								lastW = cell.Week
								x = r.X + float32(cell.Week*14)
								scale = 1
								alpha = 1
								if waving {
									t := elapsed - float64(cell.Week)*weekDelay.Seconds()
									scale = kitui.BouncyScale(t)
									if coldStart {
										alpha = float32(0.35 + 0.65*max(0, min(t/0.12, 1)))
										scale = 0.72 + 0.28*scale
									} else if t <= 0 {
										alpha = 0
									} else if t < 0.14 {
										alpha = float32(t / 0.14)
									}
								}
								skipWeek = scale <= 0.01 || alpha <= 0.01
							}
							if skipWeek {
								continue
							}
							y := r.Y + float32(cell.Weekday*14)
							date := cell.Date
							color := tok.Heat[cell.Level]
							if alpha < 1 {
								color = color.Alpha(alpha)
							}
							cellScale := scale
							if !waving {
								targetScale := float32(1)
								if cell.Week == hoverW && cell.Weekday == hoverWD {
									targetScale = 1.35
								}
								if tapping && date == tapDay {
									// Cell Tap: Bouncy spring 0.6 -> targetScale (1.35 if still hovered,
									// 1.0 otherwise) so there is zero snap when the spring settles at 650ms.
									cellScale = 0.6 + (targetScale-0.6)*kitui.BouncyScale(tapElapsed)
								} else {
									cellScale = targetScale
								}
							}
							if cellScale != 1 {
								sz := 11 * cellScale
								off := (11 - sz) / 2
								p.Fill(ui.Rect{X: x + off, Y: y + off, W: sz, H: sz}, color, max(1, 2*cellScale))
							} else {
								p.Fill(ui.Rect{X: x, Y: y, W: 11, H: 11}, color, 2)
							}
							// Double-ring halo matching prototype .cell.sel:
							// 1.5px bg gap + 1.5px accent outline with smooth fade in/out.
							if loaded {
								ringAlpha := float32(0)
								if selDay == date {
									ringAlpha = 1
									if ringing {
										ringAlpha = min(1, float32(ringElapsed/0.12))
									}
								} else if ringing && ringPrevDay == date && selDay == "" {
									ringAlpha = max(0, 1-float32(ringElapsed/0.12))
								}
								if ringAlpha > 0.01 {
									sz := 11 * cellScale
									off := (11 - sz) / 2
									p.Stroke(ui.Rect{X: x + off - 0.75, Y: y + off - 0.75, W: sz + 1.5, H: sz + 1.5}, tok.Bg.Alpha(ringAlpha), 2.5, 1.5)
									p.Stroke(ui.Rect{X: x + off - 2.25, Y: y + off - 2.25, W: sz + 4.5, H: sz + 4.5}, tok.Accent.Alpha(ringAlpha), 3.8, 1.5)
								}
							}
						}
					})
					// Expose a single accessible hit-target for today's cell so keyboard
					// and UI tests can locate/click today directly without 371 nodes,
					// plus a floating cell-anchored tooltip pill right above the hovered day.
					if v.Loaded {
						todayDate := today.Format("2006-01-02")
						todayLabel := fmt.Sprintf("%s · 新增 %d 条记忆", todayDate, buckets[todayDate])
						todayW := weeks - 1
						todayWD := int(today.Weekday())
						canvas.Children(func() {
							ui.ButtonBase(c.Key("overview-day-"+todayDate)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
								Label(todayLabel).
								Absolute().Left(float32(todayW*14)).Top(float32(todayWD*14)).Size(11, 11).
								Background(ui.Transparent).BorderWidth(0).OnClick(func() {
								if v.Day == todayDate {
									a.SelectDay("")
								} else {
									a.SelectDay(todayDate)
								}
							})
							if hoverW >= 0 && hoverWD >= 0 && hoveredLabel != "" {
								tip := ui.Row(c.Key("overview-heatmap-tip")).PassThrough().
									Background(tok.TooltipBg).Border(1, tok.Border).Radius(radiusSmall).
									Padding(4, 8).Shadow(0, 4, 14, 0, tok.ShadowLift)
								tipW := tip.Bounds().W
								if tipW <= 0 {
									tipW = 156
								}
								tipX := float32(hoverW*14) + 5.5 - tipW/2
								if tipX < 0 {
									tipX = 0
								}
								if maxX := gridW - tipW; tipX > maxX {
									tipX = maxX
								}
								tipY := float32(hoverWD*14) - 30
								tip.Absolute().Left(tipX).Top(tipY).Children(func() {
									ui.Text(c, hoveredLabel).Font(kitui.FontUI).FontSize(fsCaption).FontWeight(500).
										FontFeatures("tnum").TextColor(tok.TooltipText).SingleLine()
								})
							}
						})
					}
				})
			})
			// Right: today summary + legend.
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.End).Gap(sp2).Padding(0, 0, 4, 0).Children(func() {
				if !v.Loaded {
					kitui.Text(c, tok, "读取中…", homeBody)
				} else {
					n := buckets[today.Format("2006-01-02")]
					ui.Row(c).Label(fmt.Sprintf("今天新增 %d 条", n)).AlignItems(ui.Baseline).Gap(3).Children(func() {
						kitui.Text(c, tok, "今天新增", homeBody)
						ui.Text(c, fmt.Sprint(n)).Font(kitui.FontUI).FontSize(fsFootnote).FontWeight(550).FontFeatures("tnum").TextColor(tok.Text1)
						kitui.Text(c, tok, "条", homeBody)
					})
				}
				ui.Row(c).AlignItems(ui.Center).Gap(3).Children(func() {
					ui.Text(c, "少").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
					for level := 1; level <= 4; level++ {
						ui.Box(c).Size(9, 9).Radius(2).Background(tok.Heat[level])
					}
					ui.Text(c, "多").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
				})
			})
		})
	})
}

func (a *ControllerOf[T]) waveElapsed(c *ui.Context, p *ui.Painter, start time.Time, duration time.Duration) (float64, bool) {
	if a.Theme != nil && a.Theme.ReduceMotion() {
		return 0, false
	}
	return kitui.HeatmapWaveElapsed(c, p, start, duration)
}

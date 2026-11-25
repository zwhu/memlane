package search

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
	"strings"
)

func markBackground(tok *kitui.Tokens) ui.Color {
	if tok.Dark {
		return tok.Accent.Alpha(0.26)
	}
	return tok.Accent.Alpha(0.18)
}

func DangerSoftBg(tok *kitui.Tokens) ui.Color {
	if tok.Dark {
		return tok.Danger.Alpha(0.16)
	}
	return tok.Danger.Alpha(0.10)
}

// highlightInlineMarkdown applies keyword highlighting after parsing Markdown.
func highlightInlineMarkdown(c *ui.Context, tok *kitui.Tokens, raw, query string, size float32, weight int, lineHeight float32, color ui.Color) ui.Element {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return kitui.InlineMarkdown(c, tok, raw, size, weight, lineHeight, color)
	}
	preview := kitui.PreviewMarkdownSource(raw)
	baseSpans := kitui.ParseInlineMarkdown(preview, size, color, tok.Fill2, tok.Accent)
	if len(baseSpans) == 0 {
		baseSpans = []ui.Span{{Text: preview}}
	}
	markBg := markBackground(tok)
	out := make([]ui.Span, 0, len(baseSpans)+4)
	for _, sp := range baseSpans {
		out = append(out, HighlightSpans(sp, terms, markBg)...)
	}
	return ui.RichText(c, out...).Font("system-ui").FontSize(size).FontWeight(weight).LineHeight(lineHeight).TextColor(color)
}

func scopeChip(c *ui.Context, tok *kitui.Tokens, key, label, text string, active bool, onClick func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).Tooltip(text).
		Cursor(ui.CursorPointer).Height(26).MaxWidth(210).Padding(0, kitui.Sp2).Gap(6).
		Radius(kitui.Radius).AlignItems(ui.Center).Shrink(0)
	borderCol := tok.BorderStrong
	bgNormal := ui.Transparent
	fgCol := tok.Text2
	weight := 400
	ico := kitui.IconFolder
	if active {
		borderCol = tok.Accent
		bgNormal = tok.AccentSoft
		fgCol = tok.Accent
		weight = 500
		ico = kitui.SolidSVG(tok.AccentHex, kitui.PathFolder)
	}
	b.Border(1, borderCol).Background(kitui.HoverColor(b, key+"-bg", bgNormal, tok.Hover))
	kitui.ButtonPress(b, 0.97)
	b.Children(func() {
		ui.Icon(c, ico).FontSize(13).TextColor(fgCol).Shrink(0)
		ui.Text(c, text).Font("system-ui").FontSize(kitui.FSFootnote).FontWeight(weight).TextColor(fgCol).SingleLine().MinWidth(0)
	}).OnClick(onClick)
	return b
}

func KeyboardHint(c *ui.Context, tok *kitui.Tokens, keycap, desc string) {
	ui.Row(c).AlignItems(ui.Center).Gap(5).Shrink(0).Children(func() {
		ui.Row(c).MinWidth(18).Height(18).Padding(0, 5).Radius(kitui.RadiusSmall).
			Background(tok.Fill2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, keycap).Font("system-ui").FontSize(10).FontWeight(500).
				TextColor(tok.Text2).SingleLine()
		})
		ui.Text(c, desc).Font("system-ui").FontSize(kitui.FSCaption).TextColor(tok.Text3).SingleLine()
	})
}

func scrimBg(tok *kitui.Tokens) ui.Color {
	if tok.Dark {
		return ui.RGBA(0, 0, 0, 0.52)
	}
	return ui.RGBA(0, 0, 0, 0.28)
}

func (a *ControllerOf[T, L]) RenderDialog(c *ui.Context) {
	tok := a.tokens(c)
	s := a.State
	if !s.Open {
		return
	}
	w, h := c.Size()
	rows := a.VisibleRows()
	signature := a.Signature()
	if len(rows) > 0 && s.Selection >= len(rows) {
		s.Selection = 0
	}
	panelW := min(float32(640), max(0, w-64))
	topInset := float32(120)
	if h < 500 {
		topInset = max(float32(24), (h-360)/2)
	}
	panelH := min(float32(540), max(0, h-topInset-40))
	panel := ui.DialogBase(c.Key("unified-search-modal"), &s.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(scrimBg(tok)).Justify(ui.Start).AlignItems(ui.Center).Padding(topInset, 0, 0, 0)
		panel.Width(panelW).Height(panelH).Padding(0).Gap(0).
			Background(tok.BgElevated).Radius(kitui.RadiusMenu).Border(1, tok.Border).
			Shadow(0, 12, 32, 0, tok.ShadowLift).
			Label("搜索记忆与原始记录弹框").Transition(kitui.DialogTransition()).
			Children(func() {
				// 1. Input bar (52px)
				ui.Row(c).Height(52).Shrink(0).Padding(0, kitui.Sp4).Gap(kitui.Sp3).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					ui.Icon(c, kitui.IconSearch).FontSize(18).TextColor(tok.Text3).Shrink(0)
					input := ui.TextInput(c.Key("unified-query"), &s.Query).
						Label("搜索关键词").Placeholder("搜索记忆与原始记录…").
						Grow(1).MinWidth(0).FontSize(kitui.FSHeading).
						Background(ui.Transparent).BorderWidth(0).
						OnSubmit(a.SubmitOrOpen)
					if input.Changed() {
						s.HistoryDirty = true
						a.CancelChanged()
					}
					if s.Focus {
						input.Focus()
						s.Focus = false
					}
					if s.Query != "" {
						clearBtn := ui.ButtonBase(c.Key("cmdk-clear")).Role(ui.RoleButton).Label("清空关键词").Tooltip("清空关键词").
							Cursor(ui.CursorPointer).Height(22).Padding(0, 7).Radius(kitui.RadiusSmall).BorderWidth(0).
							AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
						clearBtn.Background(kitui.HoverColor(clearBtn, "cmdk-clear-bg", tok.Fill1, tok.Hover))
						kitui.ButtonPress(clearBtn, 0.96)
						clearBtn.Children(func() {
							ui.Text(c, "清空").Font("system-ui").FontSize(kitui.FSCaption).FontWeight(500).TextColor(tok.Text2).SingleLine()
						}).OnClick(func() {
							s.Query = ""
							s.HistoryDirty = false
							s.Focus = true
							a.CancelChanged()
							a.Submit()
						})
					}
					kitui.SmallIconAction(c, tok, "关闭搜索", kitui.IconClose, a.Close)
				})

				// Filters and match statistics.
				ui.Row(c).Shrink(0).Padding(kitui.Sp2, kitui.Sp4).Gap(kitui.Sp2).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					labels := []string{"全部", "知识", "原始记录"}
					displayLabels := []string{"全部", "记忆", "原始记录"}
					selected := 0
					for n, label := range labels {
						if s.Type == label {
							selected = n
						}
					}
					kitui.Segmented(c, tok, "cmdk-scope-seg", "搜索内容类型", &selected, displayLabels, func() {
						a.SetType(labels[selected])
					})
					if proj, ok := a.targetProject(); ok {
						active := s.Scope == proj.ID
						scopeChip(c, tok, "cmdk-proj-chip", "仅当前项目", "仅当前项目 · "+proj.Name, active, func() {
							a.ToggleCurrentProject(proj.ID)
						})
					}
					ui.Spacer(c)
					q := strings.TrimSpace(s.Query)
					switch {
					case s.Loading && len(rows) > 0:
						kitui.Text(c, tok, "正在补充语义与全文结果…", kitui.RoleMeta).FontFeatures("tnum").SingleLine()
					case s.Loading:
						kitui.Text(c, tok, "搜索中…", kitui.RoleMeta).FontFeatures("tnum").SingleLine()
					case q != "" && s.Loaded && s.Submitted == signature:
						keywords := 0
						for _, row := range rows {
							if a.Describe(row).SearchMatch == "关键词匹配" {
								keywords++
							}
						}
						kitui.Text(c, tok, fmt.Sprintf("关键词匹配 %d · 语义相关 %d", keywords, len(rows)-keywords), kitui.RoleMeta).FontFeatures("tnum").SingleLine()
					case q == "" && s.Loaded && s.Submitted == signature:
						kitui.Text(c, tok, fmt.Sprintf("最近 %d 项", len(rows)), kitui.RoleMeta).FontFeatures("tnum").SingleLine()
					}
				})

				// 3. Error bar
				if s.Error != "" {
					ui.Row(c).Shrink(0).Padding(kitui.Sp2, kitui.Sp4).Gap(kitui.Sp3).AlignItems(ui.Center).
						Background(DangerSoftBg(tok)).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
						kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger).MaxLines(2).Grow(1).MinWidth(0)
						kitui.OutlineButton(c, tok, "重试", nil, a.Retry).Disabled(s.Loading)
					})
				}

				// 4. Result list with paint-only spring selection indicator
				var ind ui.Element
				var activeRow ui.Element
				var listCol ui.Element
				hasList := false
				scroll := ui.Scroll(c.Key("unified-results")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(kitui.Sp2)
				scroll.Children(func() {
					if s.Loading && len(rows) == 0 {
						ui.Column(c).FillWidth().Padding(kitui.Sp2).Label("搜索中…").Children(func() {
							kitui.SkeletonGroup(c, tok, 4, 2, true, false)
						})
						return
					}
					if len(rows) == 0 {
						if s.Error != "" {
							return
						}
						q := strings.TrimSpace(s.Query)
						message := "没有找到结果"
						if q != "" && s.Submitted != signature {
							message = "按回车开始搜索"
						} else if q == "" {
							message = "暂无最近记忆或原始记录"
						}
						kitui.EmptyState(c, tok, "cmdk-empty", message, "")
						return
					}
					hasList = true
					listCol = ui.Column(c.Key("cmdk-list-host")).FillWidth().MinWidth(0)
					listCol.Children(func() {
						// Normal-flow 0x0 layer paints strictly behind result rows.
						ui.Box(c.Key("cmdk-sel-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
							ind = ui.Box(c.Key("cmdk-sel-ind")).Width(max(float32(0), panelW-2*kitui.Sp2)).Height(62).
								Radius(kitui.Radius).Background(tok.AccentSoft).PassThrough()
						})
						group := ""
						q := strings.TrimSpace(s.Query)
						for n, row := range rows {
							i, index := row, n
							kind := "原始记录"
							if a.Describe(i).knowledge() {
								kind = "记忆"
							}
							if kind != group {
								title := kind
								if q == "" {
									title = "最近" + kind
								}
								topPad := kitui.Sp2
								if group == "" {
									topPad = kitui.Sp1
								}
								ui.Text(c, title).Font("system-ui").FontSize(kitui.FSCaption).FontWeight(600).
									TextColor(tok.Text3).Padding(topPad, kitui.Sp2, kitui.Sp1, kitui.Sp2)
								group = kind
							}
							selected := s.KeyboardSelection && s.Selection == index
							el := a.cmdkResultRow(c, tok, i, index, q, selected, s.Submitted != signature || a.Describe(i).Unavailable)
							if selected {
								activeRow = el
								if s.Reveal {
									el.ScrollIntoView()
								}
							}
						}
					})
				})
				if hasList {
					hostBox := listCol.Bounds()
					rowBox := activeRow.Bounds()
					hasActive := s.KeyboardSelection && rowBox.H > 0 && hostBox.W > 0
					targetY := s.lastSelIndY
					fade := float32(1)
					if hasActive {
						targetY = rowBox.Y - hostBox.Y
						s.lastSelIndY = targetY
						fade = 0
						ind.Width(rowBox.W).Height(rowBox.H)
					}
					if a.Theme != nil && a.Theme.ReduceMotion() || c.Preferences().ReduceMotion {
						ind.Transform(ui.Translation(0, targetY)).Opacity(1 - fade)
					} else {
						ind.AnimatePose(ui.Pose{Y: targetY, Scale: 1, Fade: fade}, kitui.Snappy)
					}
				}
				s.Reveal = false

				// 5. Footer shortcut hint bar (38px)
				ui.Row(c).Height(38).Shrink(0).Padding(0, kitui.Sp4).Gap(kitui.Sp4).AlignItems(ui.Center).
					BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					KeyboardHint(c, tok, "↑↓", "选择")
					KeyboardHint(c, tok, "↵", "打开")
					KeyboardHint(c, tok, "Esc", "关闭")
					ui.Spacer(c)
					if !s.Loading && len(rows) > 0 {
						kitui.Text(c, tok, fmt.Sprintf("共 %d 项", len(rows)), kitui.RoleMeta).FontFeatures("tnum").SingleLine()
					}
				})
			})
	})

	// Overlay-level shortcuts keep ↑↓ working even after clicking
	// segmented tabs or the project-only scope chip.
	if s.Open {
		if panel.OverlayShortcut(0, ui.KeyDown) && len(rows) > 0 {
			if !s.KeyboardSelection {
				s.Selection = 0
			} else {
				s.Selection = (s.Selection + 1) % len(rows)
			}
			s.KeyboardSelection = true
			s.Reveal = true
		}
		if panel.OverlayShortcut(0, ui.KeyUp) && len(rows) > 0 {
			if !s.KeyboardSelection {
				s.Selection = len(rows) - 1
			} else {
				s.Selection = (s.Selection + len(rows) - 1) % len(rows)
			}
			s.KeyboardSelection = true
			s.Reveal = true
		}
	}
	if !s.Open {
		a.Close()
	}
}

func (a *ControllerOf[T, L]) cmdkResultRow(c *ui.Context, tok *kitui.Tokens, row T, index int, query string, selected, disabled bool) ui.Element {
	i := a.Describe(row)
	title, preview := i.Title, i.Preview
	if a.ItemText != nil {
		title, preview = a.ItemText(row, query)
	} else {
		title, preview = resultText(i, query)
	}

	b := ui.ButtonBase(c.Key("search-hit-"+i.key())).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label(title).FillWidth().MinWidth(0).Justify(ui.Start).AlignItems(ui.Start).
		Padding(9, kitui.Sp3).BorderWidth(0).Radius(kitui.Radius).Gap(kitui.Sp3).Disabled(disabled)
	if selected {
		b.Background(ui.Transparent)
	} else {
		b.Background(kitui.HoverColor(b, "cmdk-row-bg", ui.Transparent, tok.Hover))
	}
	kitui.ButtonPress(b, 0.99)

	tip := title
	if preview != "" {
		tip += "\n\n" + preview
	}
	if v := []rune(tip); len(v) > 800 {
		tip = string(v[:800]) + "…"
	}
	b.Tooltip(tip)

	b.Children(func() {
		ui.Box(c).Margin(2, 0, 0, 0).Shrink(0).Children(func() {
			if a.RenderIcon != nil {
				a.RenderIcon(c, row)
			} else {
				ui.Icon(c, kitui.IconDoc).FontSize(20).TextColor(tok.Text3)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			displayBody := title
			if query != "" && preview != "" {
				displayBody = title + " — " + preview
			}
			highlightInlineMarkdown(c, tok, displayBody, query, kitui.FSBody, 400, 1.5, tok.Text1).MaxLines(2)

			ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(kitui.Sp2).Children(func() {
				if i.knowledge() && i.Unread {
					kitui.UnreadDot(c, tok, "未读标记", "未读")
				}
				if a.ProjectName != nil {
					if name := a.ProjectName(i.DatasetID); name != "" {
						ui.Text(c, name).Font("system-ui").FontSize(kitui.FSCaption).TextColor(tok.Text3).SingleLine().Shrink(0)
					}
				}
				if a.RenderMetadata != nil {
					a.RenderMetadata(c, row)
				} else if i.Category != "" {
					kitui.CategoryChip(c, tok, i.Category, i.Category)
				}
				if i.SearchMatch != "" {
					kitui.Badge(c, tok, i.SearchMatch, "accent")
				}
				ui.Spacer(c)
				ui.Text(c, i.Date).Font("system-ui").FontSize(kitui.FSCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
			})
		})
	}).OnClick(func() {
		a.State.Selection = index
		a.OpenResult(row)
	})
	return b
}

func (a *ControllerOf[T, L]) RenderTrigger(c *ui.Context) {
	tok := a.tokens(c)
	b := ui.ButtonBase(c.Key("unified-search-trigger")).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label("搜索记忆与原始记录").Width(260).Height(28).Padding(0, kitui.Sp2).Gap(kitui.Sp2).
		Background(tok.Fill1).Radius(kitui.Radius).AlignItems(ui.Center).Shrink(0)
	borderCol := ui.Transparent
	if b.Hovered() {
		borderCol = tok.BorderStrong
	}
	b.Border(1, b.AnimateColor("search-border", borderCol, kitui.Fast))
	kitui.ButtonPress(b, 0.98)
	b.Children(func() {
		ui.Icon(c, kitui.IconSearch).FontSize(14).TextColor(tok.Text3).Shrink(0)
		ui.Text(c, "搜索记忆与原始记录").Font("system-ui").FontSize(kitui.FSFootnote).FontWeight(400).TextColor(tok.Text3).SingleLine()
		ui.Spacer(c)
		ui.Text(c, "⌘ K").Font("system-ui").FontSize(kitui.FSCaption).FontWeight(400).TextColor(tok.Text4).SingleLine()
	}).OnClick(func() {
		if a.OnTrigger != nil {
			a.OnTrigger()
			return
		}
		p, ok := a.targetProject()
		a.Open(false, "", p, ok)
	})
}

func (a *ControllerOf[T, L]) targetProject() (shell.ProjectRef, bool) {
	if a.TargetProject == nil {
		return shell.ProjectRef{}, false
	}
	return a.TargetProject()
}

func resultText(i ResultItem, query string) (string, string) {
	title, preview := StripWrapperPrefixes(i.Title), StripWrapperPrefixes(i.Preview)
	if i.SearchMatch == "关键词匹配" {
		preview = HitExcerpt(preview, query)
	}
	if LooksLikeBareUUID(title) || title == "" {
		title = preview
		preview = ""
	}
	if title == "" {
		title = "原始记录"
		if i.knowledge() {
			title = "记忆"
		}
	}
	return title, preview
}

package simpledesktop

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

var v4IconUpload = v4IconSVG(`<path d="M10 13V4.5M6.5 8L10 4.5 13.5 8M4 15.5h12"/>`)

func v4MarkBg() ui.Color {
	if tok.Dark {
		return tok.Accent.Alpha(0.26)
	}
	return tok.Accent.Alpha(0.18)
}

func v4DangerSoftBg() ui.Color {
	if tok.Dark {
		return tok.Danger.Alpha(0.16)
	}
	return tok.Danger.Alpha(0.10)
}

// v4HighlightInlineMarkdown renders inline Markdown with case-insensitive keyword
// highlight spans (using tok.Accent alpha background) when query is non-empty.
func v4HighlightInlineMarkdown(c *ui.Context, raw, query string, size float32, weight int, lineHeight float32, color ui.Color) ui.Element {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return v4InlineMarkdown(c, raw, size, weight, lineHeight, color)
	}
	preview := previewMarkdownSource(raw)
	baseSpans := parseInlineMarkdown(preview, size, color)
	if len(baseSpans) == 0 {
		baseSpans = []ui.Span{{Text: preview}}
	}
	markBg := v4MarkBg()
	out := make([]ui.Span, 0, len(baseSpans)+4)
	for _, sp := range baseSpans {
		out = append(out, highlightSpanByTerms(sp, terms, markBg)...)
	}
	return ui.RichText(c, out...).Font("system-ui").FontSize(size).FontWeight(weight).LineHeight(lineHeight).TextColor(color)
}

func highlightSpanByTerms(sp ui.Span, lowerTerms []string, markBg ui.Color) []ui.Span {
	if sp.Text == "" || len(lowerTerms) == 0 {
		return []ui.Span{sp}
	}
	text := sp.Text
	lower := strings.ToLower(text)
	// Ensure byte lengths match before slicing original text; fall back to rune
	// matching when Unicode case folding changes byte width.
	if len(lower) != len(text) {
		return highlightSpanByRunes(sp, lowerTerms, markBg)
	}
	out := make([]ui.Span, 0, 3)
	pos := 0
	for pos < len(text) {
		bestStart, bestLen := -1, 0
		for _, term := range lowerTerms {
			if term == "" {
				continue
			}
			idx := strings.Index(lower[pos:], term)
			if idx < 0 {
				continue
			}
			abs := pos + idx
			if bestStart < 0 || abs < bestStart || (abs == bestStart && len(term) > bestLen) {
				bestStart = abs
				bestLen = len(term)
			}
		}
		if bestStart < 0 {
			rest := sp
			rest.Text = text[pos:]
			out = append(out, rest)
			break
		}
		if bestStart > pos {
			pre := sp
			pre.Text = text[pos:bestStart]
			out = append(out, pre)
		}
		hit := sp
		hit.Text = text[bestStart : bestStart+bestLen]
		hit.Background = markBg
		if hit.Radius == 0 {
			hit.Radius = 2
		}
		out = append(out, hit)
		pos = bestStart + bestLen
	}
	return out
}

func highlightSpanByRunes(sp ui.Span, lowerTerms []string, markBg ui.Color) []ui.Span {
	runes := []rune(sp.Text)
	lowerRunes := make([]rune, len(runes))
	for i, r := range runes {
		lr := []rune(strings.ToLower(string(r)))
		if len(lr) > 0 {
			lowerRunes[i] = lr[0]
		} else {
			lowerRunes[i] = r
		}
	}
	termRunes := make([][]rune, 0, len(lowerTerms))
	for _, t := range lowerTerms {
		if t != "" {
			termRunes = append(termRunes, []rune(t))
		}
	}
	out := make([]ui.Span, 0, 3)
	pos := 0
	for pos < len(runes) {
		bestStart, bestLen := -1, 0
		for _, tr := range termRunes {
			if len(tr) == 0 || pos+len(tr) > len(lowerRunes) {
				continue
			}
			for s := pos; s+len(tr) <= len(lowerRunes); s++ {
				if bestStart >= 0 && s > bestStart {
					break
				}
				matched := true
				for k := range tr {
					if lowerRunes[s+k] != tr[k] {
						matched = false
						break
					}
				}
				if matched {
					if bestStart < 0 || s < bestStart || (s == bestStart && len(tr) > bestLen) {
						bestStart = s
						bestLen = len(tr)
					}
					break
				}
			}
		}
		if bestStart < 0 {
			rest := sp
			rest.Text = string(runes[pos:])
			out = append(out, rest)
			break
		}
		if bestStart > pos {
			pre := sp
			pre.Text = string(runes[pos:bestStart])
			out = append(out, pre)
		}
		hit := sp
		hit.Text = string(runes[bestStart : bestStart+bestLen])
		hit.Background = markBg
		if hit.Radius == 0 {
			hit.Radius = 2
		}
		out = append(out, hit)
		pos = bestStart + bestLen
	}
	return out
}

func (a *App) searchProjectName(dataset string) string {
	for _, p := range overviewProjects(a.Config) {
		if p.Dataset == dataset {
			return p.Name
		}
	}
	for _, p := range a.Config.Common {
		if p.Dataset == dataset {
			return p.Name
		}
	}
	return ""
}

func (a *App) searchTargetProject() (Project, bool) {
	if (a.page == "知识" || a.page == "图谱") && a.project >= 0 && a.project < len(a.Config.Projects) {
		return a.Config.Projects[a.project], true
	}
	for _, p := range a.Config.Projects {
		if !a.projectIsHidden(p) {
			return p, true
		}
	}
	return Project{}, false
}

func searchItemPrimaryText(i Item, query string) (string, string) {
	if i.Session != nil {
		topic := strings.TrimSpace(i.Title)
		if topic == "" || topic == i.ID || topic == "来源会话" || looksLikeBareUUID(topic) {
			topic = sessionIdentity(i.ID)
		}
		_, preview := searchHitText(i, query)
		if strings.TrimSpace(preview) == strings.TrimSpace(topic) {
			preview = ""
		}
		return topic, preview
	}
	title, preview := searchHitText(i, query)
	if looksLikeBareUUID(title) {
		if preview != "" {
			return firstReadingSentence(preview), preview
		}
		if strings.Contains(i.Kind, "知识") {
			return "记忆", ""
		}
		return "原始记录", ""
	}
	return title, preview
}

func looksLikeBareUUID(s string) bool {
	v := strings.TrimSpace(s)
	if len(v) < 32 || len(v) > 40 || strings.ContainsAny(v, " \t\n") {
		return false
	}
	hexCount := 0
	for _, r := range v {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			hexCount++
		} else if r != '-' {
			return false
		}
	}
	return hexCount >= 32 && utf8.RuneCountInString(v) == len(v)
}

func v4ScopeChip(c *ui.Context, key, label, text string, active bool, onClick func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).Tooltip(text).
		Cursor(ui.CursorPointer).Height(26).MaxWidth(210).Padding(0, sp2).Gap(6).
		Radius(radius).AlignItems(ui.Center).Shrink(0)
	borderCol := tok.BorderStrong
	bgNormal := ui.Transparent
	fgCol := tok.Text2
	weight := 400
	ico := v4IconFolder
	if active {
		borderCol = tok.Accent
		bgNormal = tok.AccentSoft
		fgCol = tok.Accent
		weight = 500
		ico = tok.SolidFolder
	}
	b.Border(1, borderCol).Background(homeHoverColor(b, key+"-bg", bgNormal, tok.Hover))
	v4ButtonPress(b, 0.97)
	b.Children(func() {
		ui.Icon(c, ico).FontSize(13).TextColor(fgCol).Shrink(0)
		ui.Text(c, text).Font("system-ui").FontSize(fsFootnote).FontWeight(weight).TextColor(fgCol).SingleLine().MinWidth(0)
	}).OnClick(onClick)
	return b
}

func v4KbdHint(c *ui.Context, keycap, desc string) {
	ui.Row(c).AlignItems(ui.Center).Gap(5).Shrink(0).Children(func() {
		ui.Text(c, keycap).Font("system-ui").FontSize(10).FontWeight(500).
			MinWidth(18).Height(18).Padding(0, 5).Radius(radiusSmall).
			Background(tok.Fill2).TextColor(tok.Text2).TextAlign(ui.Center).SingleLine()
		ui.Text(c, desc).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
	})
}

func v4ScrimBg() ui.Color {
	if tok.Dark {
		return ui.RGBA(0, 0, 0, 0.52)
	}
	return ui.RGBA(0, 0, 0, 0.28)
}

func (a *App) searchDialog(c *ui.Context) {
	s := &a.searchUI
	if !s.Open {
		return
	}
	w, h := c.Size()
	rows := a.visibleSearchRows()
	if len(rows) > 0 && s.Selection >= len(rows) {
		s.Selection = 0
	}
	panelW := min(float32(640), w-64)
	topInset := float32(120)
	if h < 500 {
		topInset = max(float32(24), (h-360)/2)
	}
	panelH := min(float32(540), h-topInset-40)
	panel := ui.DialogBase(c.Key("unified-search-modal"), &s.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(v4ScrimBg()).Justify(ui.Start).AlignItems(ui.Center).Padding(topInset, 0, 0, 0)
		panel.Width(panelW).Height(panelH).Padding(0).Gap(0).
			Background(tok.BgElevated).Radius(radiusMenu).Border(1, tok.Border).
			Shadow(0, 12, 32, 0, tok.ShadowLift).
			Label("搜索记忆与原始记录弹框").Transition(v4DialogTransition()).
			Children(func() {
				// 1. Input bar (52px)
				ui.Row(c).Height(52).Shrink(0).Padding(0, sp4).Gap(sp3).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					ui.Icon(c, v4IconSearch).FontSize(18).TextColor(tok.Text3).Shrink(0)
					input := ui.TextInput(c.Key("unified-query"), &s.Query).
						Label("搜索关键词").Placeholder("搜索记忆与原始记录…").
						Grow(1).MinWidth(0).FontSize(fsHeading).
						Background(ui.Transparent).BorderWidth(0).
						OnSubmit(a.searchSubmitOrOpen)
					if input.Changed() {
						s.HistoryDirty = true
						a.cancelChangedSearch()
					}
					if s.Focus {
						input.Focus()
						s.Focus = false
					}
					if s.Query != "" {
						clearBtn := ui.ButtonBase(c.Key("cmdk-clear")).Role(ui.RoleButton).Label("清空关键词").Tooltip("清空关键词").
							Cursor(ui.CursorPointer).Height(22).Padding(0, 7).Radius(radiusSmall).BorderWidth(0).
							AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
						clearBtn.Background(homeHoverColor(clearBtn, "cmdk-clear-bg", tok.Fill1, tok.Hover))
						v4ButtonPress(clearBtn, 0.96)
						clearBtn.Children(func() {
							ui.Text(c, "清空").Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text2).SingleLine()
						}).OnClick(func() {
							s.Query = ""
							s.HistoryDirty = false
							s.Focus = true
							a.cancelChangedSearch()
							a.submitUnifiedSearch()
						})
					}
					readingAction(c, "关闭搜索", v4IconClose, a.closeUnifiedSearch)
				})

				// 2. Filter bar: v4Segmented + "仅当前项目" chip + match stats
				ui.Row(c).Shrink(0).Padding(sp2, sp4).Gap(sp2).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					labels := []string{"全部", "知识", "原始记录"}
					displayLabels := []string{"全部", "记忆", "原始记录"}
					selected := 0
					for n, label := range labels {
						if s.Type == label {
							selected = n
						}
					}
					v4Segmented(c, "cmdk-scope-seg", "搜索内容类型", &selected, displayLabels, func() {
						s.Type = labels[selected]
						s.Selection = 0
						s.KeyboardSelection = false
						s.Scroll.Y = 0
						s.Reveal = true
						s.Focus = true
					})
					if proj, ok := a.searchTargetProject(); ok {
						active := s.Scope == proj.ID
						v4ScopeChip(c, "cmdk-proj-chip", "仅当前项目", "仅当前项目 · "+proj.Name, active, func() {
							if s.Scope == proj.ID {
								s.Scope = ""
							} else {
								s.Scope = proj.ID
							}
							s.Selection = 0
							s.KeyboardSelection = false
							s.Scroll.Y = 0
							s.Focus = true
							a.submitUnifiedSearch()
						})
					}
					ui.Spacer(c)
					q := strings.TrimSpace(s.Query)
					switch {
					case s.Loading:
						homeText(c, "搜索中…", homeMeta).FontFeatures("tnum").SingleLine()
					case q != "" && s.Loaded && s.Submitted == a.searchSignature():
						keywords := 0
						for _, row := range rows {
							if row.SearchMatch == "关键词匹配" {
								keywords++
							}
						}
						homeText(c, fmt.Sprintf("关键词匹配 %d · 语义相关 %d", keywords, len(rows)-keywords), homeMeta).FontFeatures("tnum").SingleLine()
					case q == "" && s.Loaded && s.Submitted == a.searchSignature():
						homeText(c, fmt.Sprintf("最近 %d 项", len(rows)), homeMeta).FontFeatures("tnum").SingleLine()
					}
				})

				// 3. Error bar
				if s.Error != "" {
					ui.Row(c).Shrink(0).Padding(sp2, sp4).Gap(sp3).AlignItems(ui.Center).
						Background(v4DangerSoftBg()).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
						homeText(c, s.Error, homeMeta).TextColor(tok.Danger).MaxLines(2).Grow(1).MinWidth(0)
						v4OutlineButton(c, "重试", nil, a.retryUnifiedSearch).Disabled(s.Loading)
					})
				}

				// 4. Result list with paint-only spring selection indicator
				var ind ui.Element
				var activeRow ui.Element
				var listCol ui.Element
				hasList := false
				scroll := ui.Scroll(c.Key("unified-results")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(sp2)
				scroll.Children(func() {
					if s.Loading {
						ui.Column(c).FillWidth().Padding(sp2).Label("搜索中…").Children(func() {
							v4SkeletonGroup(c, 4, 2, true, false)
						})
						return
					}
					if len(rows) == 0 {
						if s.Error != "" {
							return
						}
						q := strings.TrimSpace(s.Query)
						message := "没有找到结果"
						if q != "" && s.Submitted != a.searchSignature() {
							message = "按回车开始搜索"
						} else if q == "" {
							message = "暂无最近记忆或原始记录"
						}
						v4EmptyState(c, "cmdk-empty", message, "")
						return
					}
					hasList = true
					listCol = ui.Column(c.Key("cmdk-list-host")).FillWidth().MinWidth(0)
					listCol.Children(func() {
						// Normal-flow 0x0 layer paints strictly behind result rows.
						ui.Box(c.Key("cmdk-sel-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
							ind = ui.Box(c.Key("cmdk-sel-ind")).Width(max(float32(0), panelW-2*sp2)).Height(62).
								Radius(radius).Background(tok.AccentSoft).PassThrough()
						})
						group := ""
						q := strings.TrimSpace(s.Query)
						for n, row := range rows {
							i, index := row, n
							kind := "原始记录"
							if strings.Contains(i.Kind, "知识") {
								kind = "记忆"
							}
							if kind != group {
								title := kind
								if q == "" {
									title = "最近" + kind
								}
								topPad := sp2
								if group == "" {
									topPad = sp1
								}
								ui.Text(c, title).Font("system-ui").FontSize(fsCaption).FontWeight(600).
									TextColor(tok.Text3).Padding(topPad, sp2, sp1, sp2)
								group = kind
							}
							selected := s.KeyboardSelection && s.Selection == index
							el := a.cmdkResultRow(c, i, index, q, selected, s.Submitted != a.searchSignature() || i.ReadingState == "unavailable")
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
					ind.AnimatePose(ui.Pose{Y: targetY, Scale: 1, Fade: fade}, v4Snappy)
				}
				s.Reveal = false

				// 5. Footer shortcut hint bar (38px)
				ui.Row(c).Height(38).Shrink(0).Padding(0, sp4).Gap(sp4).AlignItems(ui.Center).
					BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					v4KbdHint(c, "↑↓", "选择")
					v4KbdHint(c, "↵", "打开")
					v4KbdHint(c, "⌘↵", "检索结果页")
					v4KbdHint(c, "Esc", "关闭")
					ui.Spacer(c)
					v4SmallButton(c, "查看全部结果", nil, a.openSearchResultsPage)
				})
			})
	})

	// Overlay-level shortcuts keep ↑↓ and ⌘↵ working even after clicking
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
		if panel.OverlayShortcut(ui.Cmd, ui.KeyEnter) {
			a.openSearchResultsPage()
		}
	}
	if !s.Open {
		a.closeUnifiedSearch()
	}
}

func (a *App) cmdkResultRow(c *ui.Context, i Item, index int, query string, selected, disabled bool) ui.Element {
	title, preview := searchItemPrimaryText(i, query)
	isKnowledge := strings.Contains(i.Kind, "知识")

	b := ui.ButtonBase(c.Key("search-hit-"+i.key())).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label(title).FillWidth().MinWidth(0).Justify(ui.Start).AlignItems(ui.Start).
		Padding(9, sp3).BorderWidth(0).Radius(radius).Gap(sp3).Disabled(disabled)
	if selected {
		b.Background(ui.Transparent)
	} else {
		b.Background(homeHoverColor(b, "cmdk-row-bg", ui.Transparent, tok.Hover))
	}
	v4ButtonPress(b, 0.99)

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
			agentLogo(c, recentAgentIcon(i), 20)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			displayBody := title
			if query != "" && preview != "" {
				displayBody = title + " — " + preview
			}
			v4HighlightInlineMarkdown(c, displayBody, query, fsBody, 400, 1.5, tok.Text1).MaxLines(2)

			ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
				if isKnowledge && a.itemIsUnread(i) {
					v4UnreadDot(c)
				}
				if projName := a.searchProjectName(i.Dataset); projName != "" {
					ui.Text(c, projName).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().Shrink(0)
				}
				if isKnowledge {
					v4RecallChip(c, i)
					if fields := knowledgeBasisFields(i); len(fields) > 0 {
						v4CategoryChip(c, fields[0].Value)
					}
				} else {
					rawTag := "会话"
					if i.Session == nil && !strings.Contains(i.Kind, "会话") {
						rawTag = "文档"
					}
					v4CategoryChip(c, rawTag)
					if i.SessionSummary != "" {
						ui.Text(c, i.SessionSummary).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().MinWidth(0)
					}
				}
				if i.SearchMatch != "" {
					ui.Text(c, i.SearchMatch).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine().Shrink(0)
				}
				ui.Spacer(c)
				dateStr := displayDate(i.Date)
				if isKnowledge {
					dateStr = knowledgeDateLabel(i)
				}
				ui.Text(c, dateStr).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
			})
		})
	}).OnClick(func() {
		a.searchUI.Selection = index
		a.openSearchResult(i)
	})
	return b
}

// Keep time import used when needed by animation helpers.
var _ = time.Millisecond

package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *App) sourceContext(c *ui.Context, s *pane) {
	origin := s.History[len(s.History)-1]
	summary := stripInlineBackticks(knowledgePreview(overviewReadingTitle(origin)))
	if summary == "" {
		summary = projectDisplayTitle(origin)
	}
	ui.Column(c).FillWidth().Padding(sp2+2, sp3).Gap(6).Radius(radius).Background(tok.AccentSofter).Border(1, tok.AccentSoft).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			back := ui.ButtonBase(c.Key("source-return-btn")).Role(ui.RoleButton).Label("返回来源记忆").
				Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Gap(4).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
			back.Background(homeHoverColor(back, "src-back-bg", ui.Transparent, tok.AccentSoft))
			v4ButtonPress(back, 0.96)
			back.Children(func() {
				ui.Icon(c, readingBack).FontSize(12).TextColor(tok.Accent).Shrink(0)
				ui.Text(c, "返回来源记忆").Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
			}).OnClick(func() { a.returnFromSource(s) })
			ui.Box(c).Size(1, 12).Background(tok.Border).Shrink(0)
			homeText(c, "来源记忆", homeMeta).Shrink(0)
			ui.Spacer(c)
			if a.page == "知识" && a.kind == "知识" && s.Selected != nil && s.Selected.Session != nil {
				selectedCopy := *s.Selected
				pageCopy := s.SourcePage
				homeLink(c, "在原始记录中打开", func() {
					a.openSourceRecord(s, origin, selectedCopy, pageCopy)
				}).FontSize(fsCaption).Shrink(0)
			}
		})
		if summary != "" {
			ui.Text(c, summary).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text1).MaxLines(2).LineHeight(1.5)
		}
		homeText(c, "会话级出处 · 未提供具体问答或工具记录的位置。", homeMeta)
	})
}

func (a *App) sessionHistoryControls(c *ui.Context, s *pane, item Item) bool {
	page := s.SourcePage
	if page == nil || page.ID != item.ID || page.Dataset != item.Dataset {
		return false
	}
	hasSubs := len(item.Subagents) > 0
	if !hasSubs && s.SessionSubagents {
		s.SessionSubagents = false
	}
	selected := 0
	if hasSubs && s.SessionSubagents {
		selected = 2
	} else if s.SessionTools {
		selected = 1
	}
	labels := []string{"问答", "工具记录"}
	if hasSubs {
		labels = append(labels, fmt.Sprintf("子会话 (%d)", len(item.Subagents)))
	}
	var statusText string
	switch {
	case hasSubs && s.SessionSubagents:
		statusText = fmt.Sprintf("共 %d 个关联子会话", len(item.Subagents))
	case s.SessionTools:
		statusText = fmt.Sprintf("工具记录 · 已加载 %d / %d", len(page.Traces), page.ToolCount)
	default:
		statusText = fmt.Sprintf("问答 · 已加载 %d / %d", len(page.QAs), page.Count)
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp3).Margin(sp1, 0, sp1, 0).Children(func() {
		v4Segmented(c, "session-content-type", "会话内容类型", &selected, labels, func() {
			s.SessionTools = selected == 1
			s.SessionSubagents = hasSubs && selected == 2
			s.SessionLoadMaxY = 0
		})
		homeText(c, statusText, homeMeta).FontFeatures("tnum")
		ui.Spacer(c)
		if !s.SessionSubagents {
			name, more := "问答", page.QAMore
			scrollKey := item.Key()
			if s.SessionTools {
				name, more = "工具记录", page.TraceMore
				scrollKey += "/tools"
			}
			if more {
				if s.Reading {
					homeText(c, "正在加载更多"+name+"…", homeMeta).Shrink(0)
				} else {
					scroll := projectDetailScroll(s, scrollKey)
					projectLink(c, "继续加载"+name, func() {
						s.SessionLoadMaxY = scroll.MaxY
						a.loadMoreSession(s)
					}).AlignSelf(ui.Center).Shrink(0)
				}
			}
		}
		refreshAction(c, "重新读取会话", func() { s.SessionLoadMaxY = 0; a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
	})
	return true
}

func sessionHistoryFor(s *pane, item Item) *SessionHistoryPage {
	page := s.SourcePage
	if page == nil || page.ID != item.ID || page.Dataset != item.Dataset {
		return nil
	}
	return page
}

func (a *App) checkSessionAutoLoad(s *pane, item Item, scroll *ui.ScrollState) {
	p := sessionHistoryFor(s, item)
	if p == nil || s.SessionSubagents || s.Reading || s.Error != "" || scroll == nil {
		return
	}
	more := p.QAMore
	if s.SessionTools {
		more = p.TraceMore
	}
	if more && scroll.MaxY > 0 && scroll.MaxY > s.SessionLoadMaxY && scroll.Y >= scroll.MaxY-120 {
		s.SessionLoadMaxY = scroll.MaxY
		a.loadMoreSession(s)
	}
}

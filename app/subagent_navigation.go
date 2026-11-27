package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) openSubagentSession(s *pane, parent, child Item) {
	if s.Locating {
		if s.cancel != nil {
			s.cancel()
		}
		s.request++
		s.Loading, s.Locating = false, false
	}
	if parent.ID != "" && parent.ID != child.ID {
		parentCopy := parent
		s.SubagentParent = &parentCopy
		s.SubagentParentPage = s.SourcePage
	}
	if child.ParentTitle == "" && parent.ID != "" {
		child.ParentTitle = sessionReadableParentTitle(parent)
	}
	s.SourcePage, s.SessionTools, s.SessionSubagents, s.SessionLoadMaxY = nil, false, false, 0
	if s.readCancel != nil {
		s.readCancel()
	}
	client := a.Client
	control, hasControl := a.accessClient()
	// Support pure in-memory UI fixture tests when no control or Cognee endpoint is configured.
	if !hasControl && client.Config.ServiceURL == "" && child.Detail != nil {
		page := SessionHistoryPage{
			SessionDetail: *child.Detail,
			Paging:        true,
			Limit:         20,
			ParentLabel:   child.ParentTitle,
		}
		result, sessionPage := sessionItemFromHistoryPage(child, page)
		s.Selected, s.SourcePage = &result, sessionPage
		s.SessionTools = subagentDefaultTools(result, sessionPage)
		s.Reading, s.Error = false, ""
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
	s.Reading, s.Error = true, ""
	s.Selected = &child
	revision := a.readingRevisions[child.Key()]
	go func() {
		defer cancel()
		result, sessionPage, err := fetchSessionHistoryPage(ctx, control, hasControl, client, child)
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest || revision != a.readingRevisions[child.Key()] {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				a.handleReadingFailure(child, err)
				return
			}
			if result.ParentTitle == "" && child.ParentTitle != "" {
				result.ParentTitle = child.ParentTitle
			}
			s.Selected = &result
			s.SourcePage = sessionPage
			s.SessionSubagents = false
			s.SessionTools = subagentDefaultTools(result, sessionPage)
		})
	}()
}

func (a *App) returnToParentSession(s *pane) {
	if s == nil {
		return
	}
	if s.SubagentParent != nil {
		if s.readCancel != nil {
			s.readCancel()
		}
		s.readRequest++
		parent := *s.SubagentParent
		page := s.SubagentParentPage
		s.SubagentParent, s.SubagentParentPage = nil, nil
		s.Selected = &parent
		s.SourcePage = page
		s.SessionTools = false
		s.SessionSubagents = len(parent.Subagents) > 0
		s.SessionLoadMaxY = 0
		s.Error = ""
		s.Reading = false
		return
	}
	if s.Selected == nil || s.Selected.Session == nil || s.Selected.Session.ParentSessionID == "" {
		return
	}
	parentID := s.Selected.Session.ParentSessionID
	history := append([]Item(nil), s.History...)
	ret := s.SourceReturn
	for _, item := range s.RawCatalog {
		if item.Session != nil && item.ID == parentID {
			s.RevealItem = item.Key()
			a.selectItem(s, item)
			s.History = history
			s.SourceReturn = ret
			return
		}
	}
	stub := Item{
		ID:      parentID,
		Dataset: s.Selected.Dataset,
		Kind:    "会话",
		Title:   parentID,
		Session: &Session{ID: parentID, Dataset: s.Selected.Dataset},
	}
	a.selectItem(s, stub)
	s.History = history
	s.SourceReturn = ret
}

func (a *App) subagentContext(c *ui.Context, s *pane, i Item) {
	if i.Session == nil || !i.Session.IsSubagent {
		return
	}
	hasParent := s.SubagentParent != nil || i.Session.ParentSessionID != ""
	parentTitle := strings.TrimSpace(i.ParentTitle)
	if parentTitle == "" && s.SubagentParent != nil {
		parentTitle = sessionReadableParentTitle(*s.SubagentParent)
	}
	if parentTitle == "" && i.Session.ParentSessionID != "" {
		parentTitle = sessionIdentity(i.Session.ParentSessionID)
	}
	badge := subagentBadgeLabel(i.Session)
	if i.Session.NestingDepth > 1 {
		badge += fmt.Sprintf(" · L%d", i.Session.NestingDepth)
	}
	ui.Column(c).FillWidth().Padding(sp3, sp3).Gap(8).Radius(radius).Background(tok.AccentSofter).Border(1, tok.AccentSoft).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			if hasParent {
				back := ui.ButtonBase(c.Key("subagent-return-parent-btn")).Role(ui.RoleButton).Label("返回主会话").
					Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Gap(4).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
				back.Background(homeHoverColor(back, "sub-back-bg", ui.Transparent, tok.AccentSoft))
				v4ButtonPress(back, 0.96)
				back.Children(func() {
					ui.Icon(c, readingBack).FontSize(12).TextColor(tok.Accent).Shrink(0)
					ui.Text(c, "返回主会话").Font(fontUI).FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
				}).OnClick(func() { a.returnToParentSession(s) })
				ui.Box(c).Size(1, 12).Background(tok.Border).Shrink(0)
			}
			v4SubagentChip(c, badge, "由主会话派生的子会话，可能包含 Agent 对话与工具记录")
			if parentTitle != "" {
				homeText(c, parentTitle, homeMeta).SingleLine().Grow(1).MinWidth(0)
			} else {
				ui.Spacer(c)
			}
		})
		if !hasParent {
			homeText(c, "未在本项目找到所属主会话（独立展示）", homeMeta)
		}
	})
}

func (a *App) renderSubagentCard(c *ui.Context, s *pane, parent, child Item) {
	childTitle := projectDisplayTitle(child)
	label := fmt.Sprintf("%s：%s", subagentBadgeLabel(child.Session), childTitle)
	card := ui.ButtonBase(c.Key("subagent-card-"+child.Key())).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).FillWidth().Padding(sp3, sp4).Radius(radius).Border(1, tok.Border).
		AlignItems(ui.Stretch).Justify(ui.Start)
	if card.Hovered() {
		card.Background(tok.Hover)
	} else {
		card.Background(tok.BgElevated)
	}
	v4ButtonPress(card, 0.99)
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				agentLogo(c, recentAgentIcon(child), 14)
				v4SubagentChip(c, subagentBadgeLabel(child.Session), sessionIdentity(child.ID))
				if child.Session != nil && child.Session.NestingDepth > 1 {
					v4CategoryChip(c, fmt.Sprintf("L%d", child.Session.NestingDepth))
				}
				ui.Spacer(c)
				ui.Text(c, displayDate(child.Date)).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
			})
			if sessionHasTopic(child) {
				v4InlineMarkdown(c, strings.TrimSpace(child.Title), fsBody, 500, 1.45, tok.Text1).MaxLines(2)
			} else {
				homeText(c, sessionIdentity(child.ID), homeBody).TextColor(tok.Text2).SingleLine()
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				summary := child.SessionSummary
				if summary == "" {
					summary = sessionIdentity(child.ID)
				}
				homeText(c, summary, homeReadableMeta).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, "查看完整记录 ›").Font(fontUI).FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine().Shrink(0)
			})
		})
	}).OnClick(func() { a.openSubagentSession(s, parent, child) })
}

func (a *App) subagentsListView(c *ui.Context, s *pane, parent Item) {
	homeText(c, "以下子会话由当前主会话派生，可能包含 Agent 之间的问答对话与工具调用，点击查看完整记录。", homeMeta)
	for _, childItem := range parent.Subagents {
		a.renderSubagentCard(c, s, parent, childItem)
	}
	if parent.SubagentsHasMore || (parent.SubagentTotalCount > len(parent.Subagents)) {
		total := max(parent.SubagentTotalCount, len(parent.Subagents))
		ui.Row(c.Key("subagents-load-more-row-"+parent.ID)).FillWidth().Padding(sp2, 0, 0, 0).AlignItems(ui.Center).Gap(sp2).Children(func() {
			homeText(c, fmt.Sprintf("已显示 %d / %d 个子会话", len(parent.Subagents), total), homeMeta)
			ui.Spacer(c)
			label := "加载更多子会话"
			if parent.SubagentsLoading {
				label = "正在加载子会话…"
			}
			v4SmallButton(c, label, nil, func() { a.loadMoreSubagents(s, parent) }).Disabled(parent.SubagentsLoading)
		})
	}
}

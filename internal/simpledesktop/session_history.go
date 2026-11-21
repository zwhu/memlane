package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type SessionHistoryPage struct {
	SessionDetail
	Paging      bool              `json:"history_paging"`
	QAOffset    int               `json:"qa_offset"`
	TraceOffset int               `json:"trace_offset"`
	Limit       int               `json:"page_limit"`
	QAMore      bool              `json:"qa_more"`
	TraceMore   bool              `json:"trace_more"`
	ParentLabel string            `json:"parent_label,omitempty"`
	Subagents   []rawSessionEntry `json:"subagents,omitempty"`
}

func sessionPageBody(d SessionDetail) string {
	var body strings.Builder
	for _, q := range d.QAs {
		fmt.Fprintf(&body, "## 用户\n\n%s\n\n## Agent\n\n%s\n\n", q.Question, q.Answer)
	}
	return body.String()
}

func sessionCopyBody(s *pane, item Item) string {
	if s.SessionSubagents && len(item.Subagents) > 0 {
		var body strings.Builder
		for _, child := range item.Subagents {
			fmt.Fprintf(&body, "- [%s] %s (%s) `%s`\n", subagentBadgeLabel(child.Session), projectDisplayTitle(child), child.SessionSummary, child.ID)
		}
		return body.String()
	}
	if s.SessionTools && s.SourcePage != nil && s.SourcePage.ID == item.ID && s.SourcePage.Dataset == item.Dataset && item.Detail != nil {
		body, _ := json.MarshalIndent(item.Detail.Traces, "", "  ")
		return string(body)
	}
	return itemPresentation(item).VisibleBody
}

func validateSessionHistoryPage(page SessionHistoryPage, dataset, sessionID, user string, qa, trace int) error {
	if page.ID != sessionID || page.Dataset != dataset || (user != "" && page.User != user) || !page.Paging ||
		page.QAOffset != qa || page.TraceOffset != trace || page.Limit != 20 ||
		page.Count < qa || page.ToolCount < trace ||
		len(page.QAs) != min(20, page.Count-qa) || len(page.Traces) != min(20, page.ToolCount-trace) ||
		page.QAMore != (qa+len(page.QAs) < page.Count) || page.TraceMore != (trace+len(page.Traces) < page.ToolCount) {
		return fmt.Errorf("来源会话身份或分页结果已变化")
	}
	return nil
}

func mergeSessionHistoryMeta(item *Item, page SessionHistoryPage, refreshChildren bool) {
	prevSess := item.Session
	if page.Session.ID != "" {
		sess := page.Session
		if !sess.IsSubagent && prevSess != nil && prevSess.IsSubagent {
			sess.IsSubagent = true
		}
		if sess.ParentSessionID == "" && prevSess != nil {
			sess.ParentSessionID = prevSess.ParentSessionID
		}
		if sess.SubagentRole == "" && prevSess != nil {
			sess.SubagentRole = prevSess.SubagentRole
		}
		if sess.NestingDepth == 0 && prevSess != nil {
			sess.NestingDepth = prevSess.NestingDepth
		}
		item.Session = &sess
	}
	if strings.TrimSpace(page.ParentLabel) != "" {
		item.ParentTitle = sessionTitle(page.ParentLabel)
	}
	if refreshChildren && len(page.Subagents) > 0 {
		item.Subagents = rawSessionEntriesToItems(item.Dataset, page.Subagents, true)
	}
}

func sessionItemFromHistoryPage(item Item, page SessionHistoryPage) (Item, *SessionHistoryPage) {
	if page.Dataset != "" {
		item.Dataset = page.Dataset
	}
	if page.ID != "" {
		item.ID = page.ID
	}
	if item.Kind == "" {
		item.Kind = "会话"
	}
	if page.Updated != "" {
		item.Date = page.Updated
	}
	mergeSessionHistoryMeta(&item, page, true)
	item.Detail, item.Body = &page.SessionDetail, sessionPageBody(page.SessionDetail)
	item.SessionSummary = sessionCoverage(page.SessionDetail)
	item.ReadingState = "ready"
	if page.Label != "" {
		item.Title = sessionTitle(page.Label)
	} else if len(page.QAs) > 0 {
		item.Title = sessionTitle(page.QAs[0].Question)
	}
	return item, &page
}

func subagentDefaultTools(item Item, page *SessionHistoryPage) bool {
	return item.Session != nil && item.Session.IsSubagent && page != nil && page.Count == 0 && page.ToolCount > 0
}

func fetchSessionHistoryPage(ctx context.Context, control ProjectControlClient, hasControl bool, client Client, item Item) (Item, *SessionHistoryPage, error) {
	if hasControl {
		page, fastErr := control.rawSessionHistory(ctx, item, 0, 0)
		if fastErr == nil {
			result, sessionPage := sessionItemFromHistoryPage(item, page)
			return result, sessionPage, nil
		}
		if !shouldFallbackFromRawControlError(ctx, fastErr) {
			return Item{}, nil, fastErr
		}
	}
	return client.readSessionHistory(ctx, item)
}

func (c Client) sessionHistory(ctx context.Context, item Item, qa, trace int) (SessionHistoryPage, error) {
	var page SessionHistoryPage
	if item.Session == nil || qa < 0 || trace < 0 {
		return page, fmt.Errorf("来源会话身份无效")
	}
	path := fmt.Sprintf("/api/v1/sessions/%s/history?qa_offset=%d&trace_offset=%d&limit=20", url.PathEscape(item.ID), qa, trace)
	if err := c.get(ctx, path, &page); err != nil {
		return page, err
	}
	if err := validateSessionHistoryPage(page, item.Dataset, item.ID, item.Session.User, qa, trace); err != nil {
		return SessionHistoryPage{}, err
	}
	return page, nil
}

func (c Client) sourceWithHistory(ctx context.Context, original Item) (Item, *SessionHistoryPage, error) {
	item, err := c.source(ctx, original)
	if err != nil {
		return item, nil, err
	}
	return c.readSessionHistory(ctx, item)
}

func (c Client) readSessionHistory(ctx context.Context, item Item) (Item, *SessionHistoryPage, error) {
	page, err := c.sessionHistory(ctx, item, 0, 0)
	if err != nil {
		return item, nil, err
	}
	out, pagePtr := sessionItemFromHistoryPage(item, page)
	return out, pagePtr, nil
}

func sourceSession(s *pane) bool {
	return s != nil && len(s.History) > 0 && s.Selected != nil && s.Selected.Session != nil
}

func (a *App) loadSessionPage(s *pane, qa, trace int) { a.readSessionBatch(s, qa, trace, false) }

func (a *App) readSessionBatch(s *pane, qa, trace int, appendRows bool) {
	if s.Selected == nil || s.Selected.Session == nil || s.Reading {
		return
	}
	item := *s.Selected
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch := s.readRequest, a.epoch
	s.Reading, s.Error = true, ""
	client := a.Client
	control, hasControl := a.accessClient()
	go func() {
		defer cancel()
		var page SessionHistoryPage
		var err error
		if hasControl {
			var fastErr error
			page, fastErr = control.rawSessionHistory(ctx, item, qa, trace)
			if fastErr != nil {
				if !shouldFallbackFromRawControlError(ctx, fastErr) {
					err = fastErr
				} else {
					page, err = client.sessionHistory(ctx, item, qa, trace)
				}
			}
		} else {
			page, err = client.sessionHistory(ctx, item, qa, trace)
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest || s.Selected == nil || s.Selected.key() != item.key() || !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = "会话读取失败，请重新读取：" + err.Error()
				if readingUnavailable(err) {
					s.SourcePage = nil
				}
				a.handleReadingFailure(item, err)
				return
			}
			if appendRows && s.SourcePage != nil {
				previous := s.SourcePage
				if qa > 0 {
					if qa != len(previous.QAs) {
						return
					}
					page.QAs = append(append([]QA(nil), previous.QAs...), page.QAs...)
				} else {
					page.QAs = previous.QAs
				}
				if trace > 0 {
					if trace != len(previous.Traces) {
						return
					}
					page.Traces = append(append([]Trace(nil), previous.Traces...), page.Traces...)
				} else {
					page.Traces = previous.Traces
				}
				if qa == 0 {
					page.QAMore = previous.QAMore
				}
				if trace == 0 {
					page.TraceMore = previous.TraceMore
				}
				page.QAOffset, page.TraceOffset = 0, 0
			}
			mergeSessionHistoryMeta(&item, page, qa == 0 && trace == 0)
			item.Detail, item.Body = &page.SessionDetail, sessionPageBody(page.SessionDetail)
			item.SessionSummary = sessionCoverage(page.SessionDetail)
			s.Selected, s.SourcePage = &item, &page
		})
	}()
}

// Keep the knowledge pane intact while showing the source in its actual record tab.
func (a *App) openSourceRecord(origin *pane, knowledge, session Item, page *SessionHistoryPage) {
	location := a.readingLocation()
	a.cancelTitles(origin)
	if origin.cancel != nil {
		origin.cancel()
	}
	origin.Loading = false
	a.epoch++
	a.kind = "原始记录"
	s := a.state()
	if s.readCancel != nil {
		s.readCancel()
	}
	s.readRequest++
	s.SourceReturn = location
	s.History = []Item{knowledge}
	s.SubagentParent, s.SubagentParentPage = nil, nil
	s.Selected, s.SourcePage, s.SessionTools, s.SessionSubagents = &session, page, subagentDefaultTools(session, page), false
	s.Reading, s.Error = false, ""
	if !containsReadingItem(s.Items, session.key()) {
		s.Items = append([]Item{session}, s.Items...)
	}
	s.RevealItem = session.key()
	a.load(s)
}

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
		refreshAction(c, "重新读取会话", func() { s.SessionLoadMaxY = 0; a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
	})
	return true
}

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
	request, epoch := s.readRequest, a.epoch
	s.Reading, s.Error = true, ""
	s.Selected = &child
	revision := a.readingRevisions[child.key()]
	go func() {
		defer cancel()
		result, sessionPage, err := fetchSessionHistoryPage(ctx, control, hasControl, client, child)
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest || revision != a.readingRevisions[child.key()] {
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
			s.RevealItem = item.key()
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
	ui.Column(c).FillWidth().Padding(sp2+2, sp3).Gap(6).Radius(radius).Background(tok.AccentSofter).Border(1, tok.AccentSoft).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			if hasParent {
				back := ui.ButtonBase(c.Key("subagent-return-parent-btn")).Role(ui.RoleButton).Label("返回主会话").
					Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Gap(4).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
				back.Background(homeHoverColor(back, "sub-back-bg", ui.Transparent, tok.AccentSoft))
				v4ButtonPress(back, 0.96)
				back.Children(func() {
					ui.Icon(c, readingBack).FontSize(12).TextColor(tok.Accent).Shrink(0)
					ui.Text(c, "返回主会话").Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
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

func (a *App) subagentsListView(c *ui.Context, s *pane, parent Item) {
	homeText(c, "以下子会话由当前主会话派生，可能包含 Agent 之间的问答对话与工具调用，点击查看完整记录。", homeMeta)
	for _, childItem := range parent.Subagents {
		child := childItem
		childTitle := projectDisplayTitle(child)
		label := fmt.Sprintf("%s：%s", subagentBadgeLabel(child.Session), childTitle)
		card := ui.ButtonBase(c.Key("subagent-card-"+child.key())).Role(ui.RoleButton).Label(label).
			Cursor(ui.CursorPointer).FillWidth().Padding(sp3, sp3+2).Radius(radius).Border(1, tok.Border).
			AlignItems(ui.Stretch).Justify(ui.Start)
		card.Background(homeHoverColor(card, "sub-card-bg-"+child.key(), tok.BgElevated, tok.Hover))
		v4ButtonPress(card, 0.99)
		card.Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					agentLogo(c, recentAgentIcon(child), 14)
					v4SubagentChip(c, subagentBadgeLabel(child.Session), sessionIdentity(child.ID))
					if child.Session != nil && child.Session.NestingDepth > 1 {
						v4CategoryChip(c, fmt.Sprintf("L%d", child.Session.NestingDepth))
					}
					ui.Spacer(c)
					ui.Text(c, displayDate(child.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
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
					ui.Text(c, "查看完整记录 ›").Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Accent).SingleLine().Shrink(0)
				})
			})
		}).OnClick(func() { a.openSubagentSession(s, parent, child) })
	}
}

func (a *App) loadMoreSession(s *pane) {
	p := s.SourcePage
	if p == nil || s.Reading {
		return
	}
	qa, trace := 0, 0
	if s.SessionTools {
		if !p.TraceMore {
			return
		}
		trace = len(p.Traces)
	} else {
		if !p.QAMore {
			return
		}
		qa = len(p.QAs)
	}
	a.readSessionBatch(s, qa, trace, true)
}

func (a *App) sessionLoadMore(c *ui.Context, s *pane, item Item) {
	p := s.SourcePage
	if p == nil {
		return
	}
	name, more := "问答", p.QAMore
	key := item.key()
	if s.SessionTools {
		name, more = "工具记录", p.TraceMore
		key += "/tools"
	}
	if !more {
		homeText(c, "已加载全部"+name, homeMeta)
		return
	}
	scroll := s.DetailScroll[key]
	if !s.Reading && s.Error == "" && scroll != nil && scroll.MaxY > 0 && scroll.MaxY > s.SessionLoadMaxY && scroll.Y >= scroll.MaxY-120 {
		s.SessionLoadMaxY = scroll.MaxY
		a.loadMoreSession(s)
	}
	if s.Reading {
		homeText(c, "正在加载更多"+name+"…", homeMeta)
	} else {
		projectLink(c, "继续加载"+name, func() {
			if scroll != nil {
				s.SessionLoadMaxY = scroll.MaxY
			}
			a.loadMoreSession(s)
		})
	}
}

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
	Paging      bool `json:"history_paging"`
	QAOffset    int  `json:"qa_offset"`
	TraceOffset int  `json:"trace_offset"`
	Limit       int  `json:"page_limit"`
	QAMore      bool `json:"qa_more"`
	TraceMore   bool `json:"trace_more"`
}

func sessionPageBody(d SessionDetail) string {
	var body strings.Builder
	for _, q := range d.QAs {
		fmt.Fprintf(&body, "## 用户\n\n%s\n\n## Agent\n\n%s\n\n", q.Question, q.Answer)
	}
	return body.String()
}

func sessionCopyBody(s *pane, item Item) string {
	if s.SessionTools && s.SourcePage != nil && s.SourcePage.ID == item.ID && s.SourcePage.Dataset == item.Dataset && item.Detail != nil {
		body, _ := json.MarshalIndent(item.Detail.Traces, "", "  ")
		return string(body)
	}
	return itemPresentation(item).VisibleBody
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
	if page.ID != item.ID || page.Dataset != item.Dataset || page.User != item.Session.User || !page.Paging ||
		page.QAOffset != qa || page.TraceOffset != trace || page.Limit != 20 ||
		page.Count < qa || page.ToolCount < trace ||
		len(page.QAs) != min(20, page.Count-qa) || len(page.Traces) != min(20, page.ToolCount-trace) ||
		page.QAMore != (qa+len(page.QAs) < page.Count) || page.TraceMore != (trace+len(page.Traces) < page.ToolCount) {
		return SessionHistoryPage{}, fmt.Errorf("来源会话身份或分页结果已变化")
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
	item.Detail, item.Body = &page.SessionDetail, sessionPageBody(page.SessionDetail)
	item.SessionSummary = sessionCoverage(page.SessionDetail)
	if page.Label != "" {
		item.Title = sessionTitle(page.Label)
	} else if len(page.QAs) > 0 {
		item.Title = sessionTitle(page.QAs[0].Question)
	}
	return item, &page, nil
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
	go func() {
		defer cancel()
		page, err := client.sessionHistory(ctx, item, qa, trace)
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
			item.Detail, item.Body = &page.SessionDetail, sessionPageBody(page.SessionDetail)
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
	s.Selected, s.SourcePage, s.SessionTools = &session, page, false
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
	selected := 0
	if s.SessionTools {
		selected = 1
	}
	name, total, loaded := "问答", page.Count, len(page.QAs)
	if s.SessionTools {
		name, total, loaded = "工具记录", page.ToolCount, len(page.Traces)
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp3).Margin(sp1, 0, sp1, 0).Children(func() {
		v4Segmented(c, "session-content-type", "会话内容类型", &selected, []string{"问答", "工具记录"}, func() {
			s.SessionTools = selected == 1
			s.SessionLoadMaxY = 0
		})
		homeText(c, fmt.Sprintf("%s · 已加载 %d / %d", name, loaded, total), homeMeta).FontFeatures("tnum")
		ui.Spacer(c)
		refreshAction(c, "重新读取会话", func() { s.SessionLoadMaxY = 0; a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
	})
	return true
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

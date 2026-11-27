package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
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
	if page.ID != sessionID || page.Dataset != dataset || (user != "" && page.User != user) || !page.Paging {
		return fmt.Errorf("来源会话身份或分页结果已变化")
	}
	if page.HistoryAvailable != nil && !*page.HistoryAvailable {
		if page.Count != 0 || page.ToolCount != 0 || len(page.QAs) != 0 || len(page.Traces) != 0 || page.QAMore || page.TraceMore {
			return fmt.Errorf("来源会话身份或分页结果已变化")
		}
		return nil
	}
	if page.QAOffset != qa || page.TraceOffset != trace || page.Limit != 20 ||
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
		if sess.Agent == "" && prevSess != nil {
			sess.Agent = prevSess.Agent
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
	if page.HistoryAvailable != nil && !*page.HistoryAvailable {
		item.SessionSummary = "原始会话原文已过期或未留存"
	} else {
		item.SessionSummary = sessionCoverage(page.SessionDetail)
	}
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
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
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
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest || s.Selected == nil || s.Selected.Key() != item.Key() || !a.syncReadingIdentity() {
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
			if page.HistoryAvailable != nil && !*page.HistoryAvailable {
				page.QAs, page.Traces = nil, nil
				page.QAOffset, page.TraceOffset = 0, 0
				page.QAMore, page.TraceMore = false, false
				mergeSessionHistoryMeta(&item, page, qa == 0 && trace == 0)
				item.Detail, item.Body = &page.SessionDetail, ""
				item.SessionSummary = "原始会话原文已过期或未留存"
				s.SessionTools = false
				s.Selected, s.SourcePage = &item, &page
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
	if !containsReadingItem(s.Items, session.Key()) {
		s.Items = append([]Item{session}, s.Items...)
	}
	s.RevealItem = session.Key()
	a.load(s)
}

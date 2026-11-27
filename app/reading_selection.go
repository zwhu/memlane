package app

import (
	"context"
	"time"
)

func (a *App) selectItem(s *pane, i Item) {
	a.selectReadingItem(s, i, true)
}

func (a *App) selectReadingItem(s *pane, i Item, human bool) {
	if human && a.page == "知识" && a.kind == "知识" {
		s.Knowledge.Detail = true
	}
	if s.Locating {
		if s.cancel != nil {
			s.cancel()
		}
		s.request++
		s.Loading, s.Locating = false, false
	}
	if s.HomeAnchor != nil && s.HomeAnchor.Key() != i.Key() {
		s.HomeAnchor = nil
	}
	s.SourcePage, s.SessionTools, s.SessionSubagents, s.SessionLoadMaxY = nil, false, false, 0
	s.SubagentParent, s.SubagentParentPage = nil, nil
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
	generation := a.readingGeneration(s)
	s.Reading = true
	s.History = nil
	s.SourceReturn = nil
	a.resetOverviewFloor(s)
	s.Error = ""
	sameSelected := s.Selected != nil && s.Selected.Key() == i.Key()
	if !sameSelected || keyForReading("", *s.Selected, generation) != keyForReading("", i, generation) {
		s.Selected = &i
	}
	client, titles := a.Client, a.titles
	control, hasControl := a.accessClient()
	revision := a.readingRevisions[i.Key()]
	go func() {
		defer cancel()
		var result Item
		var sessionPage *SessionHistoryPage
		var err error
		if i.Document != nil {
			if i.BodyLoaded && !sameSelected {
				if _, idErr := readingIdentity(client); idErr != nil {
					err = idErr
				} else {
					result = i
					result.ReadingState = "ready"
				}
			} else {
				result, err = titles.read(ctx, client, i, generation, true, false)
			}
		} else if i.Session != nil {
			result, sessionPage, err = fetchSessionHistoryPage(ctx, control, hasControl, client, i)
		} else {
			result, err = client.read(ctx, i)
		}
		if err == nil && result.Kind == "知识" && hasControl {
			statsCtx, statsCancel := context.WithTimeout(ctx, 2*time.Second)
			result.RecallStats, _ = control.knowledgeRecall(statsCtx, result)
			statsCancel()
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest || revision != a.readingRevisions[i.Key()] {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				if i.Session != nil && !readingAuthDenied(err) {
					s.Error = "原文暂时无法读取：" + err.Error()
				} else {
					s.Error = err.Error()
				}
				a.handleReadingFailure(i, err)
				return
			}
			s.Selected = &result
			s.SourcePage = sessionPage
			s.SessionSubagents = false
			if subagentDefaultTools(result, sessionPage) {
				s.SessionTools = true
			}
			if human {
				a.markKnowledgeRead(result)
			}
			a.publishOverviewTitle(result)
			for idx, row := range s.Items {
				if row.Key() == result.Key() {
					s.Items[idx].Title = result.Title
					if result.SessionSummary != "" {
						s.Items[idx].SessionSummary = result.SessionSummary
					}
					if len(result.Subagents) > 0 {
						s.Items[idx].Subagents = result.Subagents
					}
					if result.Session != nil {
						sCopy := *result.Session
						s.Items[idx].Session = &sCopy
					}
					if result.ParentTitle != "" {
						s.Items[idx].ParentTitle = result.ParentTitle
					}
					if result.ReadingState == "ready" {
						s.Items[idx].ReadingState = "ready"
					}
				}
			}
		})
	}()
}
func (a *App) source(s *pane) {
	if s.Selected == nil || s.Selected.Source == "" && s.Selected.SourceDocument == "" {
		return
	}
	original := *s.Selected
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
	s.Reading = true
	s.ReadingSource = true
	client := a.Client
	control, hasControl := a.accessClient()
	titles, generation := a.titles, a.readingGeneration(s)
	go func() {
		defer cancel()
		var i Item
		var sessionPage *SessionHistoryPage
		var err error
		if original.SourceDocument != "" {
			i, err = client.documentInfoByID(ctx, original.Dataset, original.SourceDocument)
			if err == nil {
				i, err = titles.read(ctx, client, i, generation, true, false)
			}
		} else if hasControl {
			target := Item{ID: original.Source, Dataset: original.Dataset, Kind: "会话", Session: &Session{ID: original.Source, Dataset: original.Dataset}}
			page, fastErr := control.rawSessionHistory(ctx, target, 0, 0)
			if fastErr == nil {
				i, sessionPage = sessionItemFromHistoryPage(target, page)
			} else if !shouldFallbackFromRawControlError(ctx, fastErr) {
				err = fastErr
			} else {
				i, sessionPage, err = client.sourceWithHistory(ctx, original)
			}
		} else {
			i, sessionPage, err = client.sourceWithHistory(ctx, original)
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			s.ReadingSource = false
			if err != nil {
				if !readingAuthDenied(err) && original.Source != "" {
					s.Error = "原文暂时无法读取：" + err.Error()
				} else {
					s.Error = err.Error()
				}
				if i.ID != "" {
					a.handleReadingFailure(i, err)
				} else if status, ok := httpStatusCode(err); ok && status == 401 {
					a.InvalidateReading("")
				}
				return
			}
			// Stay in place on the current reading pane so the memory list
			// remains visible and returning is one click. Users can still jump
			// to the Raw Records tab via the action in the source context bar.
			s.History = append(s.History, original)
			s.Selected = &i
			s.SubagentParent, s.SubagentParentPage = nil, nil
			s.SourcePage, s.SessionTools, s.SessionSubagents = sessionPage, subagentDefaultTools(i, sessionPage), false
		})
	}()
}

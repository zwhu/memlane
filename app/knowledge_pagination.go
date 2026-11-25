package app

import (
	"context"
	"time"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) appendKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.More {
		return
	}
	if s.Knowledge.V2Mode {
		if s.Knowledge.Appending || a.Update == nil {
			return
		}
		if s.Knowledge.NextCursor == "" && len(s.Items) == 0 {
			return
		}
		p, ok := a.current()
		control, hasControl := a.accessClient()
		if !ok || !hasControl || p.ID == "" {
			return
		}
		s.Knowledge.Appending = true
		request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
		opts := v2KnowledgePageOpts{Cursor: s.Knowledge.NextCursor, Direction: "next"}
		if opts.Cursor == "" && len(s.Items) > 0 {
			opts.AnchorDocID = s.Items[len(s.Items)-1].ID
		}
		sortParam := "date"
		if s.Knowledge.SortByRecalls {
			sortParam = "recall"
		}
		categoryFilter := s.Knowledge.CategoryFilter
		unreadOnly := s.Knowledge.UnreadOnly
		recallAgentID := s.Knowledge.RecallAgentID
		recallEventID := s.Knowledge.RecallEventID
		readingID, _ := readingIdentity(Client{Config: a.Client.Config})
		recallStartedAt := a.overviewState.V2RecallStartedAt
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := control.listKnowledgePage(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, opts)
			a.Update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Knowledge.Appending = false
				if err != nil {
					if isControlInvalidCursor(err) {
						// Explicitly reset expired cursor to page 1 under the same filter.
						s.Knowledge.NextCursor = ""
						s.Knowledge.PrevCursor = ""
						s.Knowledge.HasPrev = false
						s.More = false
						s.Error = ""
						a.loadKnowledgeFeed(s)
						return
					}
					s.Error = settings.ControlUserError(err)
					if readingUnavailable(err) {
						a.InvalidateReading(p.Dataset)
					}
					return
				}
				mapped := mapKnowledgePage(resp.Items, recallStartedAt)
				a.withdrawChangedReadingBody(s, mapped)
				s.Items = reading.AppendDistinct(s.Items, mapped, Item.Key)
				enforceListSlidingWindowDir(s, true)
				s.Knowledge.NextCursor = resp.NextCursor
				s.More = resp.HasMore
				s.Knowledge.TotalFiltered = resp.TotalFiltered
				a.hydrateKnowledgeCards(s)
			})
		}()
		return
	}
	rows := a.filteredKnowledgeCatalog(s)
	end := min(len(s.Items)+knowledgeBatch, len(rows))
	if end <= len(s.Items) {
		s.More = false
		return
	}
	s.Items = append(s.Items, rows[len(s.Items):end]...)
	s.More = end < len(rows)
	a.hydrateKnowledgeCards(s)
}

func (a *App) prependKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.Knowledge.V2Mode || !s.Knowledge.HasPrev || s.Knowledge.Prepending || a.Update == nil || len(s.Items) == 0 {
		return
	}
	p, ok := a.current()
	control, hasControl := a.accessClient()
	if !ok || !hasControl || p.ID == "" {
		return
	}
	s.Knowledge.Prepending = true
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	opts := v2KnowledgePageOpts{Cursor: s.Knowledge.PrevCursor, Direction: "prev"}
	if opts.Cursor == "" {
		opts.AnchorDocID = s.Items[0].ID
	}
	sortParam := "date"
	if s.Knowledge.SortByRecalls {
		sortParam = "recall"
	}
	categoryFilter := s.Knowledge.CategoryFilter
	unreadOnly := s.Knowledge.UnreadOnly
	recallAgentID := s.Knowledge.RecallAgentID
	recallEventID := s.Knowledge.RecallEventID
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := control.listKnowledgePage(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, opts)
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Knowledge.Prepending = false
			if err != nil {
				if isControlInvalidCursor(err) {
					s.Knowledge.NextCursor = ""
					s.Knowledge.PrevCursor = ""
					s.Knowledge.HasPrev = false
					s.More = false
					s.Error = ""
					a.loadKnowledgeFeed(s)
					return
				}
				s.Error = settings.ControlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
				}
				return
			}
			mapped := mapKnowledgePage(resp.Items, recallStartedAt)
			a.withdrawChangedReadingBody(s, mapped)
			prefix := reading.DistinctPageRows(s.Items, mapped, Item.Key)
			if len(prefix) > 0 {
				s.Items = append(prefix, s.Items...)
				if s.ListSelection >= 0 {
					s.ListSelection += len(prefix)
				}
				enforceListSlidingWindowDir(s, false)
			}
			s.Knowledge.PrevCursor = resp.PrevCursor
			s.Knowledge.HasPrev = resp.HasPrev
			s.Knowledge.TotalFiltered = resp.TotalFiltered
			a.hydrateKnowledgeCards(s)
		})
	}()
}

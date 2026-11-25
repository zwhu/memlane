package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

const (
	knowledgeBatch                = reading.KnowledgeBatchSize
	knowledgeInset        float32 = pageInset
	knowledgeGap          float32 = sectionGap
	knowledgeBodySize     float32 = fsReading
	knowledgeBodyLine     float32 = 22
	knowledgePreviewLines         = 3
)

// Only presentation metadata is retained beyond the displayed batch. Bodies
// remain process-local and use the existing shared, four-slot reading scheduler.
type knowledgeFeedState struct {
	Initialized, Detail  bool
	Catalog              []Item
	Columns              map[string]int
	Measurements         map[string]knowledgeCardMeasurement
	Copied               string
	CopyUntil            time.Time
	RecallPending        int
	RecallRequest        uint64
	RecallChecked        map[string]bool
	RecallNext           time.Time
	SortByRecalls        bool
	CountsLoaded         bool
	RecallOrderPending   bool
	CountsError          string
	RecallFilter         map[string]bool
	RecallAgentID        string
	RecallEventID        string
	CategoryFilter       string
	UnreadOnly           bool
	V2Mode               bool
	NextCursor           string
	PrevCursor           string
	HasPrev              bool
	Prepending           bool
	TotalFiltered        int
	ServerCategoryCounts map[string]int
	ServerBaseTotal      int
	CatalogVersion       string
	CatalogCheck         uint64
	NewCatalogAvailable  bool
	Appending            bool
}

// enforceListSlidingWindow caps active list items to maxActiveListItems (300)
// from the front and releases evicted per-item UI state so deep cursor
// pagination stays bounded in heap and frame time.
func enforceListSlidingWindow(s *pane) {
	enforceListSlidingWindowDir(s, true)
}

func enforceListSlidingWindowDir(s *pane, trimFront bool) {
	if s == nil {
		return
	}
	kept, evicted := reading.EnforceSlidingWindowDir(s.Items, trimFront)
	if len(evicted) == 0 {
		return
	}
	if trimFront {
		drop := len(evicted)
		if s.Knowledge.V2Mode {
			s.Knowledge.HasPrev = true
			s.Knowledge.PrevCursor = ""
		}
		if s.ListSelection >= 0 {
			s.ListSelection -= drop
			if s.ListSelection < 0 {
				s.ListSelection = -1
			}
		}
	} else {
		if s.Knowledge.V2Mode {
			s.More = true
			s.Knowledge.NextCursor = ""
		}
		if s.ListSelection >= maxActiveListItems {
			s.ListSelection = -1
		}
	}
	s.Items = kept
	selectedKey := ""
	if s.Selected != nil {
		selectedKey = s.Selected.Key()
	}
	for _, row := range evicted {
		key := row.Key()
		if key == selectedKey {
			continue
		}
		delete(s.Knowledge.Measurements, key)
		delete(s.Knowledge.Columns, key)
		delete(s.DetailScroll, key)
		delete(s.DetailScroll, key+"/tools")
		delete(s.DetailScroll, key+"/subagents")
		delete(s.Technical, key)
	}
}

type knowledgeCardMeasurement struct {
	Body, State   string
	Width, Height float32
}

var knowledgeQuote = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3 13c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9H3zm12 0c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9h-8z"/></svg>`))
var knowledgeSource = homeSVG(`<path d="m10 13 4-4M8 16l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0" transform="translate(1 0) scale(.9)"/>`)

func (a *App) loadKnowledgeFeed(s *pane) {
	a.syncReadingIdentity()
	p, ok := a.current()
	update := a.Update
	if !ok || update == nil {
		return
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	s.Knowledge.Initialized, s.Loading, s.Error = true, true, ""
	s.Knowledge.Appending = false
	s.Knowledge.Prepending = false
	s.Knowledge.NewCatalogAvailable = false
	s.Knowledge.RecallRequest++
	s.Knowledge.RecallPending = 0
	s.Knowledge.RecallChecked = nil
	client := a.Client
	control, hasControl := a.accessClient()
	sortByRecalls := s.Knowledge.SortByRecalls
	categoryFilter := s.Knowledge.CategoryFilter
	unreadOnly := s.Knowledge.UnreadOnly
	recallAgentID := s.Knowledge.RecallAgentID
	recallEventID := s.Knowledge.RecallEventID
	readingID, _ := readingIdentity(Client{Config: client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	go func() {
		defer cancel()
		failClosed := func(err error) {
			update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Loading = false
				s.Error = settings.ControlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
					s.Knowledge.Catalog = nil
				}
			})
		}
		if !hasControl {
			failClosed(fmt.Errorf("控制面服务不可用，无法加载知识目录"))
			return
		}
		if p.Dataset == "" {
			failClosed(fmt.Errorf("项目正在登记，内容归属尚未确认"))
			return
		}
		sortParam := "date"
		if sortByRecalls {
			sortParam = "recall"
		}
		v2Resp, v2Err := control.listKnowledgePage(ctx, readingID, p.ID, sortParam, categoryFilter, unreadOnly, recallAgentID, recallEventID, knowledgeBatch, v2KnowledgePageOpts{})
		if v2Err == nil {
			mapped := mapKnowledgePage(v2Resp.Items, recallStartedAt)
			baseTotal := v2Resp.TotalFiltered
			if categoryFilter != "" && len(v2Resp.CategoryCounts) > 0 {
				sum := 0
				for _, cnt := range v2Resp.CategoryCounts {
					sum += cnt
				}
				baseTotal = sum
			}
			update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.Loading = false
				s.Error = ""
				s.Knowledge.V2Mode = true
				s.Knowledge.NextCursor = v2Resp.NextCursor
				s.Knowledge.PrevCursor = v2Resp.PrevCursor
				s.Knowledge.HasPrev = v2Resp.HasPrev
				s.Knowledge.Prepending = false
				s.Knowledge.TotalFiltered = v2Resp.TotalFiltered
				s.Knowledge.ServerCategoryCounts = v2Resp.CategoryCounts
				s.Knowledge.ServerBaseTotal = baseTotal
				s.Knowledge.CatalogVersion = v2Resp.CatalogVersion
				s.Knowledge.NewCatalogAvailable = false
				s.Knowledge.CountsLoaded = true
				s.Knowledge.CountsError = ""
				s.Knowledge.Catalog = nil
				a.withdrawChangedReadingBody(s, mapped)
				s.Items = mapped
				s.More = v2Resp.HasMore
				s.Loaded = true
				a.syncV2ProjectUnreadCount(p.ID, p.Dataset, v2Resp.UnreadCount, v2Resp.UnreadStateRev, v2Resp.CatalogVersion)
				a.hydrateKnowledgeCards(s)
				if s.Selected == nil && len(s.Items) > 0 && !s.Locating {
					a.selectReadingItem(s, s.Items[0], false)
				}
			})
			return
		}
		failClosed(v2Err)
	}()
}

func mapKnowledgePage(rows []v2KnowledgeItem, recallStartedAt string) []Item {
	mapped := make([]Item, 0, len(rows))
	for _, row := range rows {
		if row.DocID != "" {
			mapped = append(mapped, row.toItem(recallStartedAt))
		}
	}
	return mapped
}

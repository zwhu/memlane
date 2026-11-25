package app

import (
	"context"
	"strings"
)

func clearUnavailableItem(item *Item) {
	item.Body = ""
	item.BodyLoaded = false
	item.BodyTruncated = false
	item.Detail = nil
	item.Preview = ""
	item.PreviewTruncated = false
	item.Subagents = nil
	item.SubagentsHasMore = false
	item.SubagentsNextCursor = ""
	item.SubagentTotalCount = 0
	item.SubagentsLoading = false
	item.SessionSummary = ""
	item.ParentTitle = ""
	item.RecallStats = nil
	item.ReadingState = "unavailable"
	item.ReadingError = "内容不可用"
	if item.Document != nil {
		doc := *item.Document
		doc.Metadata = nil
		item.Document = &doc
		item.Title = documentTitle(doc)
	} else if item.Session != nil {
		item.Title = ""
	}
}

func (a *App) allReadingPanes() []*pane {
	seen := make(map[*pane]bool, len(a.panes)+len(a.access.Queues))
	out := make([]*pane, 0, len(a.panes)+len(a.access.Queues))
	for _, s := range a.panes {
		if s != nil && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (a *App) clearUserProfileBody(dataset, id string) {
	s := &a.userProfiles
	match := dataset == ""
	if !match && id != "" {
		if strings.HasSuffix(s.BodyKey, "/"+dataset+"/"+id) {
			match = true
		}
		if p, ok := a.selectedUserProfile(); ok && p.Document != nil && p.Document.Dataset == dataset && p.Document.ID == id {
			match = true
		}
	} else if !match {
		if s.BodyKey == "" || strings.Contains(s.BodyKey, "/"+dataset+"/") {
			match = true
		}
		if p, ok := a.selectedUserProfile(); ok && p.Document != nil && p.Document.Dataset == dataset {
			match = true
		}
	}
	if match {
		s.ClearBody()
	}
}

func (a *App) unavailableReadingItem(i Item) {
	a.projectUI.InfoOpen = false
	for n, r := range a.searchUI.Rows {
		if r.Dataset == i.Dataset && (r.ID == i.ID || r.SourceDocument == i.ID) {
			a.searchUI.Rows[n].Body = ""
			a.searchUI.Rows[n].BodyLoaded = false
			a.searchUI.Rows[n].Detail = nil
			a.searchUI.Rows[n].Preview = ""
			a.searchUI.Rows[n].Subagents = nil
			a.searchUI.Rows[n].Title = "内容已失效"
			a.searchUI.Rows[n].ReadingState = "unavailable"
		}
	}
	if a.searchAnchor != nil && a.searchAnchor.Dataset == i.Dataset && (a.searchAnchor.ID == i.ID || a.searchAnchor.SourceDocument == i.ID) {
		a.clearSearchOrigin()
	}
	a.clearUserProfileBody(i.Dataset, i.ID)
	if a.graphController.Host != nil && a.graphController.Host.Dataset() == i.Dataset {
		a.graphController.Host.Clear()
		a.graphController.Selection = ""
		a.graphController.Loading = false
	} else if a.graphController.Selection != "" && (strings.Contains(a.graphController.Selection, i.ID) || (i.Dataset != "" && strings.Contains(a.graphController.Selection, i.Dataset))) {
		a.graphController.Selection = ""
	}
	a.titles.InvalidateObject(i.Dataset, i.ID)
	a.detailChunks.InvalidateObject(i.Dataset, i.ID)
	if a.readingRevisions == nil {
		a.readingRevisions = map[string]uint64{}
	}
	a.readingRevisions[i.Key()]++
	for _, s := range a.allReadingPanes() {
		if s.DetailLists != nil {
			k := i.Key()
			delete(s.DetailLists, k)
			delete(s.DetailLists, k+"/tools")
			delete(s.DetailLists, k+"/subagents")
		}
		if s.SourcePage != nil && s.SourcePage.ID == i.ID && s.SourcePage.Dataset == i.Dataset {
			s.SourcePage = nil
		}
		if s.SubagentParent != nil && s.SubagentParent.Key() == i.Key() {
			s.SubagentParent = nil
			s.SubagentParentPage = nil
		} else if s.SubagentParent != nil {
			for idx := range s.SubagentParent.Subagents {
				if s.SubagentParent.Subagents[idx].Key() == i.Key() {
					clearUnavailableItem(&s.SubagentParent.Subagents[idx])
				}
			}
		}
		if s.SubagentParentPage != nil && s.SubagentParentPage.ID == i.ID && s.SubagentParentPage.Dataset == i.Dataset {
			s.SubagentParentPage = nil
		}
		if s.HomeAnchor != nil && s.HomeAnchor.Key() == i.Key() {
			s.HomeAnchor = nil
		}
		if s.Selected != nil && s.Selected.Key() == i.Key() {
			s.Selected = nil
			s.Reading = false
			s.SessionSubagents = false
		}
		history := s.History[:0]
		for _, h := range s.History {
			if h.Key() != i.Key() {
				history = append(history, h)
			}
		}
		clear(s.History[len(history):])
		s.History = history
		for n, row := range s.Items {
			if row.Key() == i.Key() {
				clearUnavailableItem(&s.Items[n])
			}
		}
		for n, row := range s.RawCatalog {
			if row.Key() == i.Key() {
				clearUnavailableItem(&s.RawCatalog[n])
			}
		}
		for n, row := range s.Knowledge.Catalog {
			if row.Key() == i.Key() {
				clearUnavailableItem(&s.Knowledge.Catalog[n])
			}
		}
		if s.Knowledge.Measurements != nil {
			delete(s.Knowledge.Measurements, i.Key())
		}
	}
	for _, rows := range [][]Item{a.overviewState.Snapshot.Knowledge, a.overviewState.DayItems} {
		for n, row := range rows {
			if row.Key() == i.Key() || (row.Session == nil && i.Session != nil &&
				row.Dataset == i.Dataset && row.Kind == i.Kind && row.ID == i.ID) {
				clearUnavailableItem(&rows[n])
			}
		}
	}
}
func (a *App) retryReadingTitle(s *pane, i Item) {
	// A retry is user initiated and bounded to this object, with no retry loop.
	parent := s.titleCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	for n, row := range s.Items {
		if row.Key() == i.Key() {
			s.Items[n].ReadingState = "pending"
			s.Items[n].ReadingError = ""
		}
	}
	request, epoch, authEpoch, generation := s.titleRequest, a.epoch, a.authEpoch, a.readingGeneration(s)
	client := a.Client
	revision := a.readingRevisions[i.Key()]
	go func() {
		defer cancel()
		result, err := a.titles.read(ctx, client, i, generation, true, false)
		a.Update(func() {
			if request != s.titleRequest || epoch != a.epoch || authEpoch != a.authEpoch || revision != a.readingRevisions[i.Key()] || ctx.Err() != nil || !a.syncReadingIdentity() {
				return
			}
			a.handleReadingFailure(i, err)
			for n, row := range s.Items {
				if row.Key() == i.Key() {
					if err != nil {
						s.Items[n].ReadingState = "failed"
						return
					}
					s.Items[n].Title = result.Title
					s.Items[n].ReadingState = "ready"
					s.Items[n].ReadingError = ""
				}
			}
		})
	}()
}

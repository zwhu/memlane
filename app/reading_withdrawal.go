package app

import (
	"context"
	"strings"
	"time"
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

func spliceDeletedItem(items []Item, key string) ([]Item, bool) {
	if len(items) == 0 {
		return items, false
	}
	kept := items[:0]
	removed := false
	for _, row := range items {
		if row.Key() == key {
			removed = true
			continue
		}
		kept = append(kept, row)
	}
	if !removed {
		return items, false
	}
	clear(items[len(kept):])
	if len(kept) == 0 {
		return nil, true
	}
	return kept, true
}

// applyKnowledgeDocumentDeleted synchronizes panes, unread counts, overview totals/heatmap,
// and advances authEpoch to fence any in-flight reads after a confirmed knowledge deletion.
// When allowOptimisticStats is false (e.g. reconciliation after an unknown delete response)
// or the item was no longer in local snapshots, overview counts are refreshed authoritatively
// instead of optimistically decremented to avoid double-decrement.
func (a *App) applyKnowledgeDocumentDeleted(item Item, allowOptimisticStats bool) {
	var curPane *pane
	curPaneWasLoading := false
	curDataset := ""
	if a.page == "知识" || a.page == "通用知识" {
		if p, ok := a.current(); ok {
			curDataset = p.Dataset
			curPane = a.state()
			curPaneWasLoading = curPane != nil && curPane.Loading
		}
	}
	overviewWasBusy := a.overviewState.Loading || a.overviewState.ActivityLoading
	if overviewWasBusy {
		a.overviewPageController().CancelAll()
		a.overviewState.Loading = false
		a.overviewState.ActivityLoading = false
		a.overviewState.ActivityRun++
	}

	a.authEpoch++
	a.unreadAuthEpoch.Store(a.authEpoch)
	a.unavailableReadingItem(item)

	key := item.Key()
	existedLocally := false
	for _, s := range a.allReadingPanes() {
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		if s.readCancel != nil {
			s.readCancel()
			s.readCancel = nil
		}
		if s.Loading {
			s.Loading = false
			s.request++
		}
		s.Reading = false
		s.Knowledge.Appending = false
		s.Knowledge.Prepending = false
		s.RawAppending = false
		var removedItems, removedCatalog bool
		s.Items, removedItems = spliceDeletedItem(s.Items, key)
		s.Knowledge.Catalog, removedCatalog = spliceDeletedItem(s.Knowledge.Catalog, key)
		if removedItems || removedCatalog {
			existedLocally = true
		}
	}
	a.recent, _ = spliceDeletedItem(a.recent, key)

	if a.access != nil && item.Dataset != "" {
		suffix := "/" + item.Dataset
		for qKey, q := range a.access.Queues {
			if q == nil || !strings.HasSuffix(qKey, suffix) {
				continue
			}
			removedPreview := false
			if len(q.UnreadPreview) > 0 {
				kept := q.UnreadPreview[:0]
				for _, row := range q.UnreadPreview {
					if row.ID == item.ID && row.Dataset == item.Dataset {
						removedPreview = true
						continue
					}
					kept = append(kept, row)
				}
				clear(q.UnreadPreview[len(kept):])
				if len(kept) == 0 {
					q.UnreadPreview = nil
				} else {
					q.UnreadPreview = kept
				}
			}
			// Compute unread status at completion time (not pre-delete) and mark ReadIDs
			// immediately so any concurrent/late body-read callback cannot double-decrement.
			alreadyRead := q.ReadIDs != nil && q.ReadIDs[item.ID]
			isUnreadNow := !alreadyRead && (removedPreview || a.itemIsUnread(item))
			if q.ReadIDs == nil {
				q.ReadIDs = map[string]bool{}
			}
			q.ReadIDs[item.ID] = true
			if q.ServerUnreadKnown && q.ServerUnreadCount > 0 && isUnreadNow {
				q.ServerUnreadCount--
			}
		}
	}

	v := &a.overviewState
	day, ok := overviewItemDate(item, time.Local)
	if !ok && len(item.Date) >= 10 {
		day = item.Date[:10]
	}
	var removedSnap, removedDay bool
	v.Snapshot.Knowledge, removedSnap = spliceDeletedItem(v.Snapshot.Knowledge, key)
	v.DayItems, removedDay = spliceDeletedItem(v.DayItems, key)
	if removedSnap || removedDay {
		existedLocally = true
	}

	overviewReloaded := false
	if allowOptimisticStats && existedLocally {
		if v.V2TotalKnowledge > 0 {
			v.V2TotalKnowledge--
		}
		if day != "" && v.V2DayBuckets != nil {
			if cnt := v.V2DayBuckets[day]; cnt > 1 {
				v.V2DayBuckets[day] = cnt - 1
			} else {
				delete(v.V2DayBuckets, day)
			}
		}
		if (removedDay || (day != "" && v.Day == day)) && v.DayTotal > 0 {
			v.DayTotal--
		}
	} else if v.Loaded && a.Update != nil {
		a.loadOverview(true)
		overviewReloaded = true
	}
	v.SnapshotVersion++
	v.BucketCache = nil
	v.HeatmapCache = nil

	if overviewWasBusy && !overviewReloaded && a.Update != nil {
		a.loadOverview(true)
	}

	// If another visible pane had an in-flight load canceled by authEpoch++, restart it
	// so it never gets stranded in an empty/loading state.
	if curPane != nil && curPaneWasLoading && curDataset != item.Dataset && a.Update != nil {
		a.load(curPane)
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

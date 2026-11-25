package app

import (
	"context"
	"fmt"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

// Find the real page using the same upstream ordering and filters as the list.
// Never treat absence from just one page as deletion of an external selection.
func (a *App) locateOverviewItem(s *pane, item Item) {
	p, ok := a.current()
	if !ok || p.Dataset != item.Dataset {
		return
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	if s.readCancel != nil {
		s.readCancel()
	}
	s.readRequest++ // Fence the cancelled detail callback before locating its page.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	preservePosition := s.HomeAnchor != nil && s.Selected != nil && s.Selected.Key() == item.Key()
	previousOffset := s.Offset
	wasLoaded := s.Loaded
	anchor := item
	s.HomeAnchor, s.HomeOffset = &anchor, s.Offset
	s.HomeCategory = a.commonCategoryFilter()
	common := a.page == "通用知识"
	if !common {
		// Prevent knowledgePage from launching a second competing loadKnowledgeFeed
		// while locateOverviewItem is already fetching the target page.
		s.Knowledge.Initialized = true
	}
	s.Loading, s.Locating, s.Reading, s.Error = true, true, false, ""
	if s.Selected == nil || s.Selected.Key() != item.Key() {
		// Keep any cached body visible immediately on frame 0 so entering from
		// Overview never flashes an empty "正在定位记忆…" placeholder.
		if cached, ok := a.titles.cachedItem(a.Client, item, a.readingGeneration(s)); ok {
			s.Selected = &cached
		} else {
			s.Selected = nil
		}
	}
	client, category := a.Client, a.commonCategoryFilter()
	control, hasControl := a.accessClient()
	readingID, _ := readingIdentity(Client{Config: client.Config})
	recallStartedAt := a.overviewState.V2RecallStartedAt
	go func() {
		defer cancel()
		var rows []Item
		var found Item
		var err error
		var more bool
		var v2NextCursor string
		var v2PrevCursor string
		var v2HasPrev bool
		var v2TotalFiltered int
		var v2CatCounts map[string]int
		var v2CatalogVersion string
		var v2UnreadCount int
		var v2UnreadStateRev int64
		if !hasControl {
			err = fmt.Errorf("控制面服务不可用，无法定位这条记忆")
		} else if !common {
			if err == nil {
				v2Resp, v2Err := control.listKnowledgePage(ctx, readingID, p.ID, "date", "", false, "", "", knowledgeBatch, v2KnowledgePageOpts{
					AnchorDocID: item.ID,
				})
				if v2Err == nil {
					v2NextCursor = v2Resp.NextCursor
					v2PrevCursor = v2Resp.PrevCursor
					v2HasPrev = v2Resp.HasPrev
					v2TotalFiltered = v2Resp.TotalFiltered
					v2CatCounts = v2Resp.CategoryCounts
					v2CatalogVersion = v2Resp.CatalogVersion
					v2UnreadCount = v2Resp.UnreadCount
					v2UnreadStateRev = v2Resp.UnreadStateRev
					more = v2Resp.HasMore
					rows = make([]Item, 0, len(v2Resp.Items))
					for _, r := range v2Resp.Items {
						if r.DocID == "" {
							continue
						}
						mapped := r.toItem(recallStartedAt)
						if mapped.Key() == item.Key() {
							found = mapped
						}
						rows = append(rows, mapped)
					}
					if found.ID == "" {
						err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
					}
				} else {
					if isControlNotFound(v2Err) {
						err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
					} else {
						err = v2Err
					}
				}
			}
		} else {
			v2Resp, v2Err := control.commonPage(ctx, p.Dataset, category, knowledgeBatch, v2KnowledgePageOpts{
				AnchorDocID: item.ID,
			})
			if v2Err == nil {
				v2NextCursor = v2Resp.NextCursor
				v2PrevCursor = v2Resp.PrevCursor
				v2HasPrev = v2Resp.HasPrev
				v2TotalFiltered = v2Resp.TotalFiltered
				v2CatCounts = v2Resp.CategoryCounts
				v2CatalogVersion = v2Resp.CatalogVersion
				more = v2Resp.HasMore
				rows = make([]Item, 0, len(v2Resp.Items))
				for _, r := range v2Resp.Items {
					if r.DocID == "" {
						continue
					}
					mapped := r.toItem(recallStartedAt)
					if mapped.Key() == item.Key() {
						found = mapped
					}
					rows = append(rows, mapped)
				}
				if found.ID == "" {
					err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
				}
			} else {
				if isControlNotFound(v2Err) {
					err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
				} else {
					err = v2Err
				}
			}
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Loading, s.Locating = false, false
			if err != nil {
				s.Error = settings.ControlUserError(err)
				a.handleReadingFailure(item, err)
				return
			}
			oldByKey := map[string]Item{}
			for _, old := range s.Items {
				oldByKey[old.Key()] = old
			}
			for n, row := range rows {
				if old, ok := oldByKey[row.Key()]; ok && old.BodyLoaded &&
					row.Document != nil && (row.Document.ContentHash != "" || row.Document.Updated != "") &&
					keyForReading("", row, "") == keyForReading("", old, "") {
					rows[n].Body, rows[n].BodyLoaded, rows[n].BodyTruncated, rows[n].ReadingState = old.Body, true, old.BodyTruncated, "ready"
				}
				if row.Key() == found.Key() {
					found = rows[n]
				}
			}
			s.Error = ""
			s.Offset, s.HomeOffset, s.Items, s.More, s.Loaded = 0, 0, rows, more, true
			if common {
				s.CommonV2Mode = true
				s.CommonNav = []v2KnowledgePageOpts{{AnchorDocID: found.ID}}
				s.CommonCursors = []string{""}
				s.CommonPageIndex = 0
				s.CommonNextCursor = v2NextCursor
				s.CommonPrevCursor = v2PrevCursor
				s.CommonHasPrev = v2HasPrev
				s.CommonTotalFiltered = v2TotalFiltered
			} else {
				s.Knowledge.V2Mode = true
				s.Knowledge.NextCursor = v2NextCursor
				s.Knowledge.PrevCursor = v2PrevCursor
				s.Knowledge.HasPrev = v2HasPrev
				s.Knowledge.Prepending = false
				s.Knowledge.TotalFiltered = v2TotalFiltered
				s.Knowledge.ServerCategoryCounts = v2CatCounts
				s.Knowledge.ServerBaseTotal = v2TotalFiltered
				s.Knowledge.CatalogVersion = v2CatalogVersion
				s.Knowledge.CountsLoaded = true
				s.Knowledge.CountsError = ""
				s.Knowledge.Catalog = nil
				a.syncV2ProjectUnreadCount(p.ID, p.Dataset, v2UnreadCount, v2UnreadStateRev, v2CatalogVersion)
			}
			s.HomeAnchor = &found
			if !preservePosition || previousOffset != 0 {
				s.RevealItem = found.Key()
			}
			if !wasLoaded {
				a.motionRuns.projectList++
			}
			if !common {
				a.hydrateKnowledgeCards(s)
			}
			a.selectItem(s, found)
		})
	}()
}

func (a *App) publishOverviewTitle(item Item) {
	if item.Document == nil {
		return
	}
	for n, row := range a.overviewState.Snapshot.Knowledge {
		if row.Key() != item.Key() {
			continue
		}
		generation := fmt.Sprint(a.overviewState.Generation)
		if keyForReading(a.readingIdentity, row, generation) == keyForReading(a.readingIdentity, item, generation) {
			a.overviewState.Snapshot.Knowledge[n].Title = overviewReadingTitle(item)
			a.overviewState.Snapshot.Knowledge[n].ReadingState = "ready"
		}
	}
}

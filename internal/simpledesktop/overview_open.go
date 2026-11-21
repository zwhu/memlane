package simpledesktop

import (
	"context"
	"fmt"
	"sync"
	"time"
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
	request, epoch := s.request, a.epoch
	preservePosition := s.HomeAnchor != nil && s.Selected != nil && s.Selected.key() == item.key()
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
	if s.Selected == nil || s.Selected.key() != item.key() {
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
	titles := a.titles
	genStr := a.readingGeneration(s)
	go func() {
		defer cancel()
		var rows []Item
		var found Item
		var err error
		var more bool
		offset := 0
		usedV2 := false
		var v2NextCursor string
		var v2TotalFiltered int
		var v2CatCounts map[string]int
		var v2CatalogVersion string
		var v2UnreadCount int
		if hasControl && !common && control.hasFeature(ctx, v2FeatureKnowledgeCursor) {
			v2Resp, v2Err := control.projectKnowledgeV2(ctx, readingID, p.ID, "date", "", false, "", "", knowledgeBatch, "")
			if v2Err == nil {
				usedV2 = true
				v2NextCursor = v2Resp.NextCursor
				v2TotalFiltered = v2Resp.TotalFiltered
				v2CatCounts = v2Resp.CategoryCounts
				v2CatalogVersion = v2Resp.CatalogVersion
				v2UnreadCount = v2Resp.UnreadCount
				more = v2Resp.HasMore
				rows = make([]Item, 0, len(v2Resp.Items)+1)
				for _, r := range v2Resp.Items {
					mapped := r.toItem(recallStartedAt)
					if mapped.key() == item.key() {
						found = mapped
					}
					rows = append(rows, mapped)
				}
				if found.ID == "" {
					if looked, lookErr := client.documentInfoByID(ctx, item.Dataset, item.ID); lookErr == nil {
						found = looked
						if found.Preview == "" && item.Preview != "" {
							found.Preview = item.Preview
							found.ReadingState = "ready"
						}
						rows = append([]Item{found}, rows...)
					} else if item.Document != nil {
						found = item
						rows = append([]Item{found}, rows...)
					} else {
						err = lookErr
					}
				}
			} else if isControlFailClosedV2(v2Err) {
				usedV2 = true
				err = v2Err
			}
		} else if hasControl && common && control.hasFeature(ctx, v2FeatureCommonKnowledgeCursor) {
			v2Resp, v2Err := control.commonKnowledgeV2(ctx, p.Dataset, category, knowledgeBatch, "")
			if v2Err == nil {
				usedV2 = true
				v2NextCursor = v2Resp.NextCursor
				v2TotalFiltered = v2Resp.TotalFiltered
				v2CatCounts = v2Resp.CategoryCounts
				v2CatalogVersion = v2Resp.CatalogVersion
				more = v2Resp.HasMore
				rows = make([]Item, 0, len(v2Resp.Items)+1)
				for _, r := range v2Resp.Items {
					mapped := r.toItem(recallStartedAt)
					if mapped.key() == item.key() {
						found = mapped
					}
					rows = append(rows, mapped)
				}
				if found.ID == "" {
					if looked, lookErr := client.documentInfoByID(ctx, item.Dataset, item.ID); lookErr == nil {
						found = looked
						rows = append([]Item{found}, rows...)
					} else if item.Document != nil {
						found = item
						rows = append([]Item{found}, rows...)
					} else {
						err = lookErr
					}
				}
			} else if isControlFailClosedV2(v2Err) {
				usedV2 = true
				err = v2Err
			}
		}
		if !usedV2 {
			for ; offset <= 1000000; offset += 30 {
				if common {
					rows, more, err = client.commonDocuments(ctx, p, offset, category)
				} else {
					rows, more, err = client.documents(ctx, p, offset, false)
				}
				if err != nil {
					break
				}
				for _, row := range rows {
					if row.key() == item.key() {
						found = row
						break
					}
				}
				if found.ID != "" || !more {
					break
				}
			}
		}
		if err == nil && found.ID == "" {
			err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
		}
		preheated := map[string]Item{}
		if err == nil && !common && !usedV2 && len(rows) > 0 {
			// Populate cached bodies and preheat the first 10 visible items within
			// a bounded budget so the list renders real 3-line cards in one atomic swap.
			preCtx, preCancel := context.WithTimeout(ctx, 500*time.Millisecond)
			topN := min(10, len(rows))
			var mu sync.Mutex
			var wg sync.WaitGroup
			for idx := 0; idx < topN; idx++ {
				row := rows[idx]
				if cached, ok := titles.cachedItem(client, row, genStr); ok {
					mu.Lock()
					preheated[row.key()] = cached
					mu.Unlock()
					continue
				}
				wg.Add(1)
				go func(it Item) {
					defer wg.Done()
					if res, e := titles.read(preCtx, client, it, genStr, true, true); e == nil {
						mu.Lock()
						preheated[it.key()] = res
						mu.Unlock()
					}
				}(row)
			}
			if _, ok := preheated[found.key()]; !ok {
				if cached, ok := titles.cachedItem(client, found, genStr); ok {
					mu.Lock()
					preheated[found.key()] = cached
					mu.Unlock()
				}
			}
			wg.Wait()
			preCancel()
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Loading, s.Locating = false, false
			if err != nil {
				s.Error = controlUserError(err)
				a.handleReadingFailure(item, err)
				return
			}
			oldByKey := map[string]Item{}
			for _, old := range s.Items {
				oldByKey[old.key()] = old
			}
			if !usedV2 {
				preserveResolvedReadingTitles(rows, s.Items)
			}
			for n, row := range rows {
				if pre, ok := preheated[row.key()]; ok {
					rows[n].Body, rows[n].BodyLoaded, rows[n].ReadingState = pre.Body, true, "ready"
					if rows[n].Title == "" {
						rows[n].Title = pre.Title
					}
				} else if old, ok := oldByKey[row.key()]; ok && old.BodyLoaded {
					rows[n].Body, rows[n].BodyLoaded, rows[n].ReadingState = old.Body, true, "ready"
				}
				if row.key() == found.key() {
					found = rows[n]
				}
			}
			s.Error = ""
			s.Offset, s.HomeOffset, s.Items, s.More, s.Loaded = offset, offset, rows, more, true
			if usedV2 {
				if common {
					s.CommonV2Mode = true
					s.CommonCursors = []string{""}
					s.CommonPageIndex = 0
					s.CommonNextCursor = v2NextCursor
					s.CommonTotalFiltered = v2TotalFiltered
				} else {
					s.Knowledge.V2Mode = true
					s.Knowledge.NextCursor = v2NextCursor
					s.Knowledge.TotalFiltered = v2TotalFiltered
					s.Knowledge.ServerCategoryCounts = v2CatCounts
					s.Knowledge.ServerBaseTotal = v2TotalFiltered
					s.Knowledge.CatalogVersion = v2CatalogVersion
					s.Knowledge.CountsLoaded = true
					s.Knowledge.CountsError = ""
					s.Knowledge.Catalog = nil
					a.syncV2ProjectUnreadCount(p.ID, p.Dataset, v2UnreadCount, v2CatalogVersion)
				}
			} else if !common && len(s.Knowledge.Catalog) == 0 {
				s.Knowledge.Catalog = append([]Item(nil), rows...)
			}
			s.HomeAnchor = &found
			if !preservePosition || previousOffset != offset {
				s.RevealItem = found.key()
			}
			if !wasLoaded {
				a.motionRuns.projectList++
			}
			if common {
				if !usedV2 {
					a.startTitles(s)
				}
			} else {
				a.hydrateKnowledgeCards(s)
			}
			a.selectItem(s, found)
		})
	}()
}

func (a *App) publishOverviewTitle(item Item) {
	a.markKnowledgeRead(item)
	if item.Document == nil {
		return
	}
	for n, row := range a.overviewState.Snapshot.Knowledge {
		if row.key() != item.key() {
			continue
		}
		generation := fmt.Sprint(a.overviewState.generation)
		if keyForReading(a.readingIdentity, row, generation) == keyForReading(a.readingIdentity, item, generation) {
			a.overviewState.Snapshot.Knowledge[n].Title = overviewReadingTitle(item)
			a.overviewState.Snapshot.Knowledge[n].ReadingState = "ready"
		}
	}
}

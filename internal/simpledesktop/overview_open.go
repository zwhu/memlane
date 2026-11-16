package simpledesktop

import (
	"context"
	"fmt"
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
	anchor := item
	s.HomeAnchor, s.HomeOffset = &anchor, s.Offset
	s.HomeCategory = a.commonCategoryFilter()
	s.Loading, s.Locating, s.Reading, s.Error = true, true, false, ""
	if s.Selected == nil || s.Selected.key() != item.key() {
		s.Selected = nil
	}
	client, common, category := a.Client, a.page == "通用知识", a.commonCategoryFilter()
	go func() {
		defer cancel()
		var rows []Item
		var found Item
		var err error
		var more bool
		offset := 0
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
		if err == nil && found.ID == "" {
			err = fmt.Errorf("无法在当前官方目录中定位这条记忆，请重新读取")
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Loading, s.Locating = false, false
			if err != nil {
				s.Error = err.Error()
				a.handleReadingFailure(item, err)
				return
			}
			preserveResolvedReadingTitles(rows, s.Items)
			s.Error = ""
			s.Offset, s.HomeOffset, s.Items, s.More, s.Loaded = offset, offset, rows, more, true
			s.HomeAnchor = &found
			if !preservePosition || previousOffset != offset {
				s.RevealItem = found.key()
			}
			a.startTitles(s)
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

package simpledesktop

import (
	"context"
	"time"
)

// Appending uses server cursor pagination in v2 mode (bounded by the 300-item
// sliding window) or reveals metadata from the last successful legacy catalog.
func (a *App) appendRawRecords(s *pane) {
	if s.Loading || !s.Loaded || !s.More {
		return
	}
	if s.RawV2Mode {
		if s.RawAppending || s.RawNextCursor == "" || a.Update == nil {
			return
		}
		p, ok := a.current()
		control, hasControl := a.accessClient()
		if !ok || !hasControl || p.ID == "" {
			return
		}
		s.RawAppending = true
		request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
		cursor, kind := s.RawNextCursor, a.kind
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := control.rawRecordsV2(ctx, p.ID, kind, 30, cursor)
			var mapped []Item
			if err == nil {
				mapped, err = v2RawRecordsToItems(p.Dataset, resp)
			}
			a.Update(func() {
				if epoch != a.epoch || authEpoch != a.authEpoch || request != s.request || !a.syncReadingIdentity() {
					return
				}
				s.RawAppending = false
				if err != nil {
					if isControlInvalidCursor(err) {
						s.RawNextCursor = ""
						s.More = false
						s.Error = ""
						a.load(s)
						return
					}
					s.Error = controlUserError(err)
					if readingUnavailable(err) {
						a.InvalidateReading(p.Dataset)
					}
					return
				}
				seen := make(map[string]bool, len(s.Items))
				for _, existing := range s.Items {
					seen[existing.key()] = true
				}
				for _, item := range mapped {
					if !seen[item.key()] {
						s.Items = append(s.Items, item)
						seen[item.key()] = true
					}
				}
				enforceListSlidingWindow(s)
				s.RawNextCursor = resp.NextCursor
				s.More = resp.HasMore
				a.startTitles(s)
			})
		}()
		return
	}
	if len(s.RawCatalog) <= len(s.Items) {
		return
	}
	end := min(len(s.Items)+30, len(s.RawCatalog))
	s.Items = append(s.Items, s.RawCatalog[len(s.Items):end]...)
	s.More = end < len(s.RawCatalog)
	a.startTitles(s)
}

func (a *App) rawRecordsAutoLoad(s *pane) {
	if s.SourceReturn == nil && s.Query == "" && s.ListScroll.MaxY > 0 && s.ListScroll.Y >= s.ListScroll.MaxY-160 {
		a.appendRawRecords(s)
	}
}

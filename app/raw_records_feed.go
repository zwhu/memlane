package app

import (
	"context"
	"time"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/app/shell"
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
		request := s.request
		adapter := appShellAdapter{a}
		fence := shell.CaptureFence(adapter)
		cursor, kind := s.RawNextCursor, a.kind
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := control.rawPage(ctx, p.ID, kind, cursor)
			var mapped []Item
			if err == nil {
				mapped, err = v2RawRecordsToItems(p.Dataset, resp)
			}
			a.Update(func() {
				if !fence.Valid(adapter) || request != s.request || !a.syncReadingIdentity() {
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
					s.Error = settings.ControlUserError(err)
					if readingUnavailable(err) {
						a.InvalidateReading(p.Dataset)
					}
					return
				}
				s.Items = reading.AppendDistinct(s.Items, mapped, Item.Key)
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
	end := min(len(s.Items)+reading.RawBatchSize, len(s.RawCatalog))
	s.Items = append(s.Items, s.RawCatalog[len(s.Items):end]...)
	s.More = end < len(s.RawCatalog)
	a.startTitles(s)
}

func (a *App) rawRecordsAutoLoad(s *pane) {
	if s.SourceReturn == nil && s.Query == "" && reading.NearRawListEnd(s.ListScroll.Y, s.ListScroll.MaxY) {
		a.appendRawRecords(s)
	}
}

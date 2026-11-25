package overview

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

const maxDayItems = 300

func (a *ControllerOf[T]) LoadDay(day string, appendMore bool) {
	v := a.State
	if day == "" || day != v.Day || !v.V2Mode || !a.canDispatch() {
		return
	}
	var cursor string
	if appendMore {
		if v.DayLoading || !v.DayHasMore || v.DayNextCursor == "" {
			return
		}
		cursor = v.DayNextCursor
	} else {
		a.cancelDay()
		v.DayItems, v.DayNextCursor, v.DayHasMore = nil, "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	v.DayCancel = cancel
	v.DayRequest++
	req, generation := v.DayRequest, v.Generation
	v.DayLoading, v.DayError = true, ""
	fence, dispatcher := shell.CaptureFence(a.Fence), a.Dispatcher
	fetch := a.fetchDay()
	go func() {
		defer cancel()
		page, err := fetch(ctx, day, cursor)
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		dispatcher.Dispatch(func() {
			if req != v.DayRequest || generation != v.Generation || v.Day != day || !a.valid(fence) {
				return
			}
			v.DayLoading = false
			if err != nil {
				if a.denied(err) {
					a.WithdrawScope()
				} else if appendMore && a.invalidCursor(err) {
					a.LoadDay(day, false)
					return
				}
				v.DayError = a.message(err)
				return
			}
			a.applyDay(page, appendMore)
		})
	}()
}

func (a *ControllerOf[T]) invalidCursor(err error) bool {
	var failure *api.APIError
	return (errors.As(err, &failure) && failure.Status == http.StatusBadRequest && failure.Code == "invalid_cursor") ||
		(a.InvalidCursor != nil && a.InvalidCursor(err))
}

func (a *ControllerOf[T]) applyDay(page DayPage[T], appendMore bool) {
	v := a.State
	v.DayTotal, v.DayNextCursor, v.DayHasMore = page.Total, page.NextCursor, page.HasMore
	if !appendMore {
		v.DayItems = page.Items
		return
	}
	seen := make(map[string]bool, len(v.DayItems))
	for _, item := range v.DayItems {
		seen[a.Describe(item).Key()] = true
	}
	for _, item := range page.Items {
		key := a.Describe(item).Key()
		if !seen[key] {
			v.DayItems = append(v.DayItems, item)
			seen[key] = true
		}
	}
	if drop := len(v.DayItems) - maxDayItems; drop > 0 {
		v.DayItems = append([]T(nil), v.DayItems[drop:]...)
	}
}

func (a *ControllerOf[T]) cancelDay() {
	v := a.State
	if v.DayCancel != nil {
		v.DayCancel()
		v.DayCancel = nil
	}
	v.DayRequest++
	v.DayLoading = false
}

func (a *ControllerOf[T]) SelectDay(day string) {
	v := a.State
	v.heatmapTapDay, v.heatmapRingPrevDay = day, v.Day
	if day == "" {
		v.heatmapTapDay = v.Day
	}
	v.heatmapTapStart, v.heatmapRingStart = time.Time{}, time.Time{}
	v.heatmapTapPending, v.heatmapRingPending = true, true
	v.Day, v.ShowAllForDay, v.KnowledgeScroll.Y = day, false, 0
	v.KnowledgeRun++
	if v.V2Mode {
		a.cancelDay()
		v.DayItems, v.DayTotal, v.DayNextCursor = nil, 0, ""
		v.DayHasMore, v.DayError = false, ""
		a.LoadDay(day, false)
	}
}

func (a *ControllerOf[T]) date(item T, loc *time.Location) (string, bool) {
	if a.ItemDate != nil {
		return a.ItemDate(item, loc)
	}
	day := a.Describe(item).Date
	return day, day != ""
}

func (a *ControllerOf[T]) KnowledgeRows(now time.Time) []T {
	v := a.State
	if v.Day == "" {
		return v.Snapshot.Knowledge
	}
	if v.V2Mode {
		return v.DayItems
	}
	var rows []T
	for _, item := range v.Snapshot.Knowledge {
		if day, ok := a.date(item, now.Location()); ok && day == v.Day {
			rows = append(rows, item)
		}
	}
	return rows
}

func (a *ControllerOf[T]) DisplayedRows(now time.Time) []T {
	rows := a.KnowledgeRows(now)
	v := a.State
	if (v.Day != "" && v.V2Mode) || (v.Day != "" && v.ShowAllForDay) {
		return rows
	}
	return rows[:min(20, len(rows))]
}

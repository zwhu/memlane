package app

import (
	"time"
)

type trayFlyoutCache struct {
	todayGen            uint64
	todayLoaded         bool
	todayKnowledgeCount int
	todayDay            string
	todayCount          int
	todayValid          bool
	todayComputations   uint64
}

func (a *App) trayTodayMemoryCount(now time.Time) int {
	v := &a.overviewState
	if !v.Loaded && len(v.Snapshot.Knowledge) == 0 && len(v.V2DayBuckets) == 0 {
		return 0
	}
	day := now.Format("2006-01-02")
	tc := &a.trayCache
	totalCount := len(v.Snapshot.Knowledge)
	if v.V2Mode {
		totalCount = v.V2TotalKnowledge
	}
	if tc.todayValid && tc.todayGen == v.Generation && tc.todayLoaded == v.Loaded &&
		tc.todayKnowledgeCount == totalCount && tc.todayDay == day {
		return tc.todayCount
	}
	buckets := a.overviewCachedBuckets(now)
	count := buckets[day]
	tc.todayGen = v.Generation
	tc.todayLoaded = v.Loaded
	tc.todayKnowledgeCount = totalCount
	tc.todayDay = day
	tc.todayCount = count
	tc.todayValid = true
	tc.todayComputations++
	return count
}

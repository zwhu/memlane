package overview

import (
	"time"
)

func (a *ControllerOf[T]) CachedBuckets(now time.Time) map[string]int {
	v := a.State
	if v.V2Mode {
		if v.V2DayBuckets == nil {
			return map[string]int{}
		}
		return v.V2DayBuckets
	}
	day := now.Format("2006-01-02")
	firstKey, lastKey := a.knowledgeBounds(v.Snapshot.Knowledge)
	if v.BucketCache != nil && v.cachedBucketsVersion == v.SnapshotVersion && v.cachedBucketsCount == len(v.Snapshot.Knowledge) && v.cachedBucketsFirstKey == firstKey && v.cachedBucketsLastKey == lastKey && v.cachedBucketsDay == day {
		return v.BucketCache
	}
	v.BucketCache = Buckets(v.Snapshot.Knowledge, func(item T) (string, bool) {
		return a.date(item, now.Location())
	})
	v.cachedBucketsVersion = v.SnapshotVersion
	v.cachedBucketsCount = len(v.Snapshot.Knowledge)
	v.cachedBucketsFirstKey = firstKey
	v.cachedBucketsLastKey = lastKey
	v.cachedBucketsDay = day
	return v.BucketCache
}

func (a *ControllerOf[T]) CachedHeatmapCells(now, today, start, gridStart time.Time, weeks int, buckets map[string]int) []HeatmapCell {
	v := a.State
	day := now.Format("2006-01-02")
	firstKey, lastKey := a.knowledgeBounds(v.Snapshot.Knowledge)
	if v.HeatmapCache != nil && v.cachedHeatmapVersion == v.SnapshotVersion && v.cachedHeatmapCount == len(v.Snapshot.Knowledge) && v.cachedHeatmapFirstKey == firstKey && v.cachedHeatmapLastKey == lastKey && v.cachedHeatmapDay == day && v.cachedHeatmapLoaded == v.Loaded && v.cachedHeatmapWeeks == weeks {
		return v.HeatmapCache
	}
	cells := make([]HeatmapCell, 0, weeks*7)
	for w := 0; w < weeks; w++ {
		for wd := 0; wd < 7; wd++ {
			d := gridStart.AddDate(0, 0, w*7+wd)
			if d.Before(start) || d.After(today) {
				continue
			}
			date := d.Format("2006-01-02")
			level := 0
			if v.Loaded {
				count := buckets[date]
				switch {
				case count >= 10:
					level = 4
				case count >= 6:
					level = 3
				case count >= 3:
					level = 2
				case count > 0:
					level = 1
				}
			}
			cells = append(cells, HeatmapCell{Week: w, Weekday: wd, Date: date, Level: level})
		}
	}
	v.HeatmapCache = cells
	v.cachedHeatmapVersion = v.SnapshotVersion
	v.cachedHeatmapCount = len(v.Snapshot.Knowledge)
	v.cachedHeatmapFirstKey = firstKey
	v.cachedHeatmapLastKey = lastKey
	v.cachedHeatmapDay = day
	v.cachedHeatmapLoaded = v.Loaded
	v.cachedHeatmapWeeks = weeks
	return v.HeatmapCache
}

func (a *ControllerOf[T]) knowledgeBounds(items []T) (string, string) {
	if len(items) == 0 {
		return "", ""
	}
	return a.Describe(items[0]).Key(), a.Describe(items[len(items)-1]).Key()
}

func Buckets[T any](items []T, date func(T) (string, bool)) map[string]int {
	days := make(map[string]int)
	for _, item := range items {
		if day, ok := date(item); ok {
			days[day]++
		}
	}
	return days
}

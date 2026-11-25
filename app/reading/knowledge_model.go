package reading

import "slices"

const MaxActiveListItems = 300

func EnforceSlidingWindowDir[T any](rows []T, trimFront bool) (kept, evicted []T) {
	if len(rows) <= MaxActiveListItems {
		return rows, nil
	}
	if trimFront {
		drop := len(rows) - MaxActiveListItems
		return slices.Clone(rows[drop:]), rows[:drop]
	}
	return slices.Clone(rows[:MaxActiveListItems]), rows[MaxActiveListItems:]
}

func ShouldSilentRefreshOnCatalogChange(scrollY float32, readingDetail, busy bool) bool {
	return scrollY <= 0 && !readingDetail && !busy
}

// BodyVersionChanged reports a catalog row with evidence of changed content.
func BodyVersionChanged(oldHash, newHash, oldUpdated, newUpdated string) bool {
	if oldHash != "" && newHash != "" && oldHash != newHash {
		return true
	}
	return oldUpdated != "" && newUpdated != "" && oldUpdated != newUpdated
}

// AppendDistinct de-duplicates a cursor page before adding it to active rows.
func AppendDistinct[T any](rows, extra []T, key func(T) string) []T {
	return append(rows, DistinctPageRows(rows, extra, key)...)
}

func DistinctPageRows[T any](rows, extra []T, key func(T) string) []T {
	seen := make(map[string]bool, len(rows)+len(extra))
	for _, item := range rows {
		seen[key(item)] = true
	}
	var distinct []T
	for _, item := range extra {
		if k := key(item); !seen[k] {
			seen[k] = true
			distinct = append(distinct, item)
		}
	}
	return distinct
}

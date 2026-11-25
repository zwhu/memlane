package reading

import "sort"

type KnowledgeFilter struct {
	CategoryFilter string
	UnreadOnly     bool
	RecallFilter   map[string]bool
}

func SortByRecalls[T any](rows []T, byRecalls bool, key, date func(T) string, recalls func(T) int64) {
	sort.SliceStable(rows, func(i, j int) bool {
		if byRecalls {
			x, y := recalls(rows[i]), recalls(rows[j])
			if x != y {
				return x > y
			}
		}
		x, y := DateOrder(date(rows[i])), DateOrder(date(rows[j]))
		if !x.Equal(y) {
			return x.After(y)
		}
		return key(rows[i]) < key(rows[j])
	})
}

func FilterKnowledge[T any](rows []T, filter KnowledgeFilter, id, category func(T) string, unread func(T) bool) []T {
	if len(filter.RecallFilter) == 0 && !filter.UnreadOnly && filter.CategoryFilter == "" {
		return rows
	}
	filtered := make([]T, 0, len(rows))
	for _, row := range rows {
		if (len(filter.RecallFilter) == 0 || filter.RecallFilter[id(row)]) &&
			(!filter.UnreadOnly || unread(row)) &&
			(filter.CategoryFilter == "" || filter.CategoryFilter == category(row)) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

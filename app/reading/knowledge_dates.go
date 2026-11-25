package reading

import (
	"time"

	"github.com/zwhu/memlane/app/api"
)

func SourceCalendarDate(value string) (string, bool) {
	t, err := time.Parse("2006-01-02", value)
	return t.Format("2006-01-02"), err == nil && len(value) == 10
}

func DateOrder(value string) time.Time {
	if day, ok := SourceCalendarDate(value); ok {
		// Calendar ordinal for sorting only; this is never shown as a timestamp.
		at, _ := time.Parse("2006-01-02", day)
		return at
	}
	at, _ := parseOfficialTime(value)
	return at
}

func parseOfficialTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

func KnowledgeDay(location *time.Location, created string, sourceDates ...string) (string, bool) {
	for _, value := range sourceDates {
		if day, ok := SourceCalendarDate(value); ok {
			return day, true
		}
	}
	if at, ok := parseOfficialTime(created); ok {
		if location == nil {
			location = time.Local
		}
		return at.In(location).Format("2006-01-02"), true
	}
	return "", false
}

func EffectiveDate(item api.CatalogItem, location *time.Location) (string, bool) {
	return KnowledgeDay(location, item.CreatedAt, item.EffectiveDate)
}

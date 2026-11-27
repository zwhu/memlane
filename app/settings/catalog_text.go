package settings

import (
	"regexp"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/api"
)

func calendarDate(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if len(v) != 10 {
		return "", false
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

func knowledgeCatalogDate(effectiveDate, createdAt string) string {
	if day, ok := calendarDate(effectiveDate); ok {
		return day
	}
	if t, ok := api.OfficialTime(strings.TrimSpace(effectiveDate)); ok {
		return t.In(time.Local).Format("2006-01-02")
	}
	if day, ok := calendarDate(createdAt); ok {
		return day
	}
	if t, ok := api.OfficialTime(strings.TrimSpace(createdAt)); ok {
		return t.In(time.Local).Format("2006-01-02")
	}
	return ""
}

func knowledgePlaceholder(date string) string {
	v := strings.TrimSpace(date)
	if day, ok := calendarDate(v); ok {
		return "记忆 · " + day
	}
	if t, ok := api.OfficialTime(v); ok {
		return "记忆 · " + t.In(time.Local).Format("2006-01-02")
	}
	return "记忆"
}

var machineName = regexp.MustCompile(`^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

func readableName(s string) string {
	s = strings.TrimSpace(s)
	if machineName.MatchString(s) {
		return ""
	}
	return s
}

func readableDocumentTitle(d Document) string {
	if d.Label != nil {
		if s := readableName(*d.Label); s != "" {
			return s
		}
	}
	return readableName(d.Name)
}

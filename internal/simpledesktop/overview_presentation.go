package simpledesktop

import (
	"fmt"
	"strings"
	"time"
)

func middleProjectName(name string) string {
	r := []rune(name)
	if len(r) <= 10 {
		return name
	}
	return string(r[:4]) + "…" + string(r[len(r)-5:])
}

func knowledgePreview(title string) string {
	// A source excerpt, not a newly inferred conclusion. Full text stays in
	// the existing reader; no model call or stored-content rewrite is needed.
	return strings.Join(strings.Fields(title), " ")
}

func (a *App) overviewSourceName(item Item) string {
	for _, p := range overviewProjects(a.Config) {
		if p.Dataset == item.Dataset {
			return p.Name
		}
	}
	return "来源未关联"
}

func groupOverviewFailures(events []overviewEvent) []overviewEvent {
	result := make([]overviewEvent, 0, len(events))
	groups, counts := map[string]int{}, map[int]int{}
	for _, event := range events {
		if event.State != "失败" {
			result = append(result, event)
			continue
		}
		if len(event.Failures) == 0 {
			event.Failures = []activityRow{event.Record}
		}
		reason := eventFailureReason(event)
		if reason == "官方未提供详细原因" {
			reason += "/" + event.Title
		}
		key := event.Project + "/" + event.Record.Dataset + "/" + event.At.In(time.Local).Format("2006-01-02") + "/" + reason
		index, found := groups[key]
		if !found {
			groups[key] = len(result)
			counts[len(result)] = 1
			event.Failures = append([]activityRow(nil), event.Failures...)
			if len(event.Failures) == 0 {
				event.Failures = []activityRow{event.Record}
			}
			result = append(result, event)
			continue
		}
		counts[index]++
		result[index].Title = fmt.Sprintf("%s 失败 ×%d", event.Project, counts[index])
		if len(event.Failures) == 0 {
			result[index].Failures = append(result[index].Failures, event.Record)
		} else {
			result[index].Failures = append(result[index].Failures, event.Failures...)
		}
	}
	return result
}

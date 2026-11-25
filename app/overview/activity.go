package overview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

func ActivityName(name string) string             { return api.ActivityName(name) }
func ActivityState(row ActivityRow) string        { return api.ActivityState(row) }
func OfficialTime(value string) (time.Time, bool) { return api.OfficialTime(value) }

func AggregateActivity(rows []ActivityRow, projects []shell.ProjectRef) []Event {
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p.DatasetID] = p.Name
	}
	return api.AggregateActivity(rows, names)
}

// Successful raw pipeline calls do not prove a document was delivered. Once
// receipt history is available, use those exact identifiers for recall events.
func MergeRecallActivity(events []Event, recalls []RecallEvent, projects []shell.ProjectRef, want int) ([]Event, bool) {
	merged := []Event{}
	for _, event := range events {
		if event.Record.Operation == "recall" && event.State == "已完成" {
			continue
		}
		merged = append(merged, event)
	}
	names := map[string]string{}
	for _, p := range projects {
		names[p.DatasetID] = p.Name
	}
	for _, recall := range recalls {
		if len(recall.Hits) == 0 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(recall.AgentName, "本机 "), "本地 ")
		if label, ok := map[string]string{"codex": "Codex", "pi": "pi", "traex": "TRAE CLI", "trae_ide": "TRAE CN", "claude_code": "Claude Code", "cursor": "Cursor", "opencode": "OpenCode"}[recall.AgentKind]; ok {
			name = label
		}
		title := name + " 召回记忆"
		if recall.Channel == "automatic" {
			title = name + " 自动召回"
		}
		at, ok := OfficialTime(recall.Received)
		if !ok {
			continue
		}
		project := "多个记忆库"
		if first := recall.Hits[0].Dataset; first != "" {
			same := true
			for _, hit := range recall.Hits {
				if hit.Dataset != first {
					same = false
				}
			}
			if same && names[first] != "" {
				project = names[first]
			}
		}
		copy := recall
		merged = append(merged, Event{Key: "recall/" + recall.AgentID + "/" + recall.ID, Title: title, State: "已完成", Project: project, At: at, Recall: &copy})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].At.Equal(merged[j].At) {
			return merged[i].Key < merged[j].Key
		}
		return merged[i].At.After(merged[j].At)
	})
	more := len(merged) > want
	return merged[:min(want, len(merged))], more
}
func GroupFailures(events []Event) []Event {
	result := make([]Event, 0, len(events))
	groups, counts := map[string]int{}, map[int]int{}
	for _, event := range events {
		if event.State != "失败" {
			result = append(result, event)
			continue
		}
		if len(event.Failures) == 0 {
			event.Failures = []ActivityRow{event.Record}
		}
		reason := EventFailureReason(event)
		if reason == "官方未提供详细原因" {
			reason += "/" + event.Title
		}
		key := event.Project + "/" + event.Record.Dataset + "/" + event.At.In(time.Local).Format("2006-01-02") + "/" + reason
		index, found := groups[key]
		if !found {
			groups[key] = len(result)
			counts[len(result)] = 1
			event.Failures = append([]ActivityRow(nil), event.Failures...)
			if len(event.Failures) == 0 {
				event.Failures = []ActivityRow{event.Record}
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
func EventDatasets(ev Event) []string {
	seen := map[string]bool{}
	var datasets []string
	add := func(ds string) {
		if ds != "" && !seen[ds] {
			seen[ds] = true
			datasets = append(datasets, ds)
		}
	}
	add(ev.Record.Dataset)
	for _, f := range ev.Failures {
		add(f.Dataset)
	}
	if ev.Recall != nil {
		for _, hit := range ev.Recall.Hits {
			add(hit.Dataset)
		}
	}
	return datasets
}

func RecallGroups(event RecallEvent, projects []shell.ProjectRef) []RecallGroup {
	counts := make(map[string]int)
	for _, hit := range event.Hits {
		counts[hit.Dataset]++
	}
	var groups []RecallGroup
	for _, p := range projects {
		if count := counts[p.DatasetID]; count > 0 {
			groups = append(groups, RecallGroup{p.DatasetID, p.Name, count})
		}
	}
	return groups
}

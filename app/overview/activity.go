package overview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/shell"
)

func ActivityName(name string) string {
	switch name {
	case "add_pipeline", "add", "remember":
		return "采集材料"
	case "cognify_pipeline", "cognify":
		return "提炼记忆"
	case "memify_pipeline", "memify", "improve":
		return "提炼记忆"
	case "search":
		return "搜索记忆"
	case "recall":
		return "召回记忆"
	case "forget", "delete", "prune":
		return "清理内容"
	case "code_graph_pipeline":
		return "更新代码图谱"
	default:
		return "记忆活动"
	}
}
func ActivityState(r ActivityRow) string {
	if r.Outcome == "failed" || strings.Contains(r.Status, "ERRORED") {
		return "失败"
	}
	if r.Status == "DATASET_PROCESSING_COMPLETED" {
		return "已完成"
	}
	if r.Outcome == "noop" {
		return "未执行"
	}
	_, hasEnd := OfficialTime(r.Ended)
	if r.Outcome == "succeeded" && (!r.Background || hasEnd) {
		return "已完成"
	}
	if r.Background {
		return "已启动"
	}
	return "处理中"
}
func AggregateActivity(rows []ActivityRow, projects []shell.ProjectRef) []Event {
	names := map[string]string{}
	for _, p := range projects {
		names[p.DatasetID] = p.Name
	}
	latest := map[string]ActivityRow{}
	for _, r := range rows {
		if r.ID == "" || (r.Dataset != "" && names[r.Dataset] == "") {
			continue
		}
		key := r.Run
		if key == "" {
			key = r.ID
		}
		old, exists := latest[key]
		at, ok := OfficialTime(r.Created)
		prev, _ := OfficialTime(old.Created)
		if ok && (!exists || at.After(prev) || (at.Equal(prev) && r.ID > old.ID)) {
			latest[key] = r
		}
	}
	// Precompute root key for every run in O(N) with memoization to avoid O(N^2) scans.
	rootOf := make(map[string]string, len(latest))
	var resolveRoot func(string) string
	resolveRoot = func(key string) string {
		if r, ok := rootOf[key]; ok {
			return r
		}
		path := []string{}
		seen := map[string]bool{}
		cur := key
		for !seen[cur] {
			if cached, ok := rootOf[cur]; ok {
				cur = cached
				break
			}
			seen[cur] = true
			path = append(path, cur)
			r, ok := latest[cur]
			if !ok || r.Parent == "" {
				break
			}
			if _, ok := latest[r.Parent]; !ok {
				break
			}
			cur = r.Parent
		}
		for _, k := range path {
			rootOf[k] = cur
		}
		return cur
	}
	childFailed := map[string]bool{}
	childMaxAt := map[string]time.Time{}
	failuresByRoot := map[string][]ActivityRow{}
	for key, r := range latest {
		root := resolveRoot(key)
		state := ActivityState(r)
		if state == "失败" {
			failuresByRoot[root] = append(failuresByRoot[root], r)
		}
		if key != root {
			if state == "失败" {
				childFailed[root] = true
			}
			if t, ok := OfficialTime(r.Created); ok && t.After(childMaxAt[root]) {
				childMaxAt[root] = t
			}
		}
	}
	events := make(map[string]Event, len(latest))
	for key, r := range latest {
		root := rootOf[key]
		// A parent operation represents its child runs once its row is available.
		if key != root {
			continue
		}
		name := r.Operation
		if name == "" {
			name = r.Pipeline
		}
		at, _ := OfficialTime(r.Created)
		if t, ok := OfficialTime(r.Ended); ok {
			at = t
		} else if t, ok := OfficialTime(r.Started); ok {
			at = t
		}
		state := ActivityState(r)
		if r.Background && r.Outcome == "succeeded" {
			// Visible child failures are conclusive; a page of completed children
			// cannot prove that every background stage has finished.
			if maxChild := childMaxAt[root]; maxChild.After(at) {
				at = maxChild
			}
			if childFailed[root] {
				state = "失败"
			}
		}
		project := names[r.Dataset]
		if project == "" {
			project = "全局"
		}
		failures := failuresByRoot[root]
		if len(failures) > 1 {
			sort.Slice(failures, func(i, j int) bool { return failures[i].ID < failures[j].ID })
		}
		events[root] = Event{Key: root, Title: ActivityName(name), State: state, Project: project, At: at, Record: r, Failures: failures}
	}
	result := make([]Event, 0, len(events))
	for _, e := range events {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].At.Equal(result[j].At) {
			return result[i].Key < result[j].Key
		}
		return result[i].At.After(result[j].At)
	})
	return result
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

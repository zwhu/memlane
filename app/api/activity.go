package api

import (
	"sort"
	"strings"
	"time"
)

type ActivityRow struct {
	ErrorClass string `json:"error_class"`
	Origin     string `json:"origin"`
	ID         string `json:"id"`
	Run        string `json:"pipeline_run_id"`
	Parent     string `json:"parent_operation_id"`
	Dataset    string `json:"dataset_id"`
	Kind       string `json:"kind"`
	Pipeline   string `json:"pipeline_name"`
	Operation  string `json:"operation_name"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	Background bool   `json:"background"`
	Created    string `json:"created_at"`
	Started    string `json:"started_at"`
	Ended      string `json:"ended_at"`
}
type ActivityEvent struct {
	Key, Title, State, Project string
	At                         time.Time
	Record                     ActivityRow
	Failures                   []ActivityRow
	Recall                     *ActivityRecallEvent
}
type ActivityRecallHit struct {
	Dataset  string `json:"dataset_id"`
	Document string `json:"document_id"`
}
type ActivityRecallEvent struct {
	ID        string              `json:"event_id"`
	AgentID   string              `json:"agent_id"`
	AgentName string              `json:"agent_name"`
	AgentKind string              `json:"agent_kind"`
	Channel   string              `json:"channel"`
	Received  string              `json:"received_at"`
	Hits      []ActivityRecallHit `json:"hits"`
}

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
func AggregateActivity(rows []ActivityRow, names map[string]string) []ActivityEvent {
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
	events := make(map[string]ActivityEvent, len(latest))
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
		events[root] = ActivityEvent{Key: root, Title: ActivityName(name), State: state, Project: project, At: at, Record: r, Failures: failures}
	}
	result := make([]ActivityEvent, 0, len(events))
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

func OfficialTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type overviewSnapshot struct {
	Knowledge    []Item
	Raw          int
	MissingDates int
	RawError     string
}

type firstUseState struct {
	Version   int    `json:"schema_version"`
	Completed string `json:"initialization_completed_at"`
	Date      string `json:"first_used_date"`
	Zone      string `json:"first_used_timezone"`
}

func firstUseDays(c Config, now time.Time) (int, error) {
	path := c.ClientStateFile
	if path == "" {
		path = filepath.Join(filepath.Dir(c.ConfigFile), "client-state.json")
	}
	if c.ConfigFile == "" && c.ClientStateFile == "" {
		return 0, fmt.Errorf("尚未完成初始化")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("初始化记录不可用")
	}
	var state firstUseState
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || state.Zone == "" {
		return 0, fmt.Errorf("初始化记录无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.Completed); err != nil {
		return 0, fmt.Errorf("初始化记录无效")
	}
	first, err := time.Parse("2006-01-02", state.Date)
	if err != nil {
		return 0, fmt.Errorf("初始化日期无效")
	}
	// UTC here is a calendar ordinal, not elapsed local hours across DST.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(first)/(24*time.Hour)) + 1
	if days < 1 {
		days = 1
	}
	return days, nil
}

// Upstream writes datetime.now(timezone.utc); SQLite DTOs may omit its offset.
func officialTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func nodeSets(d Document) []string {
	var result []string
	switch sets := d.Metadata["node_set"].(type) {
	case []any:
		for _, s := range sets {
			if v, ok := s.(string); ok {
				result = append(result, v)
			}
		}
	case []string:
		result = sets
	}
	return result
}

func derivedMaterial(d Document) bool {
	for _, n := range nodeSets(d) {
		if n == "user_sessions_from_cache" || n == "agent_trace_feedbacks" || n == "session_learnings" {
			return true
		}
	}
	return false
}

func overviewProjects(c Config) []Project {
	seen := map[string]bool{}
	var result []Project
	for _, p := range append(append([]Project(nil), c.Projects...), c.Common...) {
		if p.Dataset != "" && !seen[p.Dataset] {
			seen[p.Dataset] = true
			result = append(result, p)
		}
	}
	return result
}

func (c Client) overviewDocuments(ctx context.Context, p Project) ([]Document, error) {
	var all []Document
	seen := map[string]bool{}
	for offset := 0; offset <= 1000000; offset += 100 {
		var page []Document
		if err := c.get(ctx, fmt.Sprintf("/api/v1/datasets/%s/data?limit=100&offset=%d", url.PathEscape(p.Dataset), offset), &page); err != nil {
			return nil, err
		}
		for _, d := range page {
			if d.ID == "" || d.Dataset != p.Dataset {
				return nil, fmt.Errorf("官方内容身份不匹配")
			}
			if !seen[d.ID] {
				seen[d.ID] = true
				all = append(all, d)
			}
		}
		if len(page) < 100 {
			sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
			return all, nil
		}
	}
	return nil, fmt.Errorf("官方分页上限内未读完，统计暂不可用")
}

func (c Client) overviewSessions(ctx context.Context, projects []Project) ([]Session, error) {
	allowed := map[string]bool{}
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	result := []Session{}
	seen := map[string]bool{}
	for offset := 0; offset <= 1000000; offset += 100 {
		var page struct {
			Sessions []Session `json:"sessions"`
			More     bool      `json:"has_more"`
		}
		if err := c.get(ctx, fmt.Sprintf("/api/v1/sessions?range=all&limit=100&offset=%d&order_by=last_activity_at&descending=true", offset), &page); err != nil {
			return nil, err
		}
		for _, s := range page.Sessions {
			key := s.User + "/" + s.ID
			if s.ID == "" {
				return nil, fmt.Errorf("官方会话身份缺失")
			}
			if allowed[s.Dataset] && !seen[key] {
				seen[key] = true
				s.Updated = ""
				result = append(result, s)
			}
		}
		if !page.More {
			sort.Slice(result, func(i, j int) bool { return result[i].User+result[i].ID < result[j].User+result[j].ID })
			return result, nil
		}
		if len(page.Sessions) == 0 {
			return nil, fmt.Errorf("官方会话分页不完整")
		}
	}
	return nil, fmt.Errorf("官方会话分页未读完")
}

func aggregateOverview(projects []Project, docs map[string][]Document, sessions []Session) overviewSnapshot {
	out := overviewSnapshot{}
	raw := map[string]bool{}
	for _, s := range sessions {
		raw["session/"+s.User+"/"+s.ID] = true
	}
	for _, p := range projects {
		for _, d := range docs[p.Dataset] {
			if d.knowledge() {
				copy := d
				out.Knowledge = append(out.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.title(), Date: d.date(), Source: d.session(), Document: &copy})
				if _, ok := d.knowledgeDay(time.Local); !ok {
					out.MissingDates++
				}
			} else if !derivedMaterial(d) {

				raw["document/"+d.Dataset+"/"+d.ID] = true
			}
		}
	}
	out.Raw = len(raw)
	sort.SliceStable(out.Knowledge, func(i, j int) bool {
		a, _ := out.Knowledge[i].Document.knowledgeDay(time.Local)
		b, _ := out.Knowledge[j].Document.knowledgeDay(time.Local)
		if a == b {
			return out.Knowledge[i].key() < out.Knowledge[j].key()
		}
		return a > b
	})
	return out
}

func (c Client) overview(ctx context.Context) (overviewSnapshot, error) {
	projects := overviewProjects(c.Config)
	docs := map[string][]Document{}
	if len(projects) == 0 {
		return overviewSnapshot{}, nil
	}
	for _, p := range projects {
		rows, err := c.overviewDocuments(ctx, p)
		if err != nil {
			return overviewSnapshot{}, err
		}
		docs[p.Dataset] = rows
	}
	// Offset APIs offer no snapshot: publish only when two full enumerations agree.
	for _, p := range projects {
		rows, err := c.overviewDocuments(ctx, p)
		if err != nil {
			return overviewSnapshot{}, err
		}
		if !reflect.DeepEqual(rows, docs[p.Dataset]) {
			return overviewSnapshot{}, fmt.Errorf("内容正在更新，请稍后刷新统计")
		}
	}
	snapshot := aggregateOverview(projects, docs, nil)
	sessions, err := c.overviewSessions(ctx, projects)
	if err != nil {
		snapshot.RawError = err.Error()
		return snapshot, nil
	}
	again, err := c.overviewSessions(ctx, projects)
	if err != nil {
		snapshot.RawError = err.Error()
		return snapshot, nil
	}
	if !reflect.DeepEqual(sessions, again) {
		snapshot.RawError = "会话正在更新，请稍后刷新统计"
		return snapshot, nil
	}
	return aggregateOverview(projects, docs, sessions), nil
}

type activityRow struct {
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
type overviewEvent struct {
	Key, Title, State, Project string
	At                         time.Time
	Record                     activityRow
	Failures                   []activityRow
	Recall                     *RecallEvent
}

func activityName(name string) string {
	switch name {
	case "add_pipeline", "add", "remember":
		return "保存材料"
	case "cognify_pipeline", "cognify":
		return "处理记忆"
	case "memify_pipeline", "memify", "improve":
		return "整理记忆"
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
func activityState(r activityRow) string {
	if r.Outcome == "failed" || strings.Contains(r.Status, "ERRORED") {
		return "失败"
	}
	if r.Status == "DATASET_PROCESSING_COMPLETED" {
		return "已完成"
	}
	if r.Outcome == "noop" {
		return "未执行"
	}
	_, hasEnd := officialTime(r.Ended)
	if r.Outcome == "succeeded" && (!r.Background || hasEnd) {
		return "已完成"
	}
	if r.Background {
		return "已启动"
	}
	return "处理中"
}
func aggregateActivity(rows []activityRow, projects []Project) []overviewEvent {
	names := map[string]string{}
	for _, p := range projects {
		names[p.Dataset] = p.Name
	}
	latest := map[string]activityRow{}
	for _, r := range rows {
		if r.ID == "" || (r.Dataset != "" && names[r.Dataset] == "") {
			continue
		}
		key := r.Run
		if key == "" {
			key = r.ID
		}
		old, exists := latest[key]
		at, ok := officialTime(r.Created)
		prev, _ := officialTime(old.Created)
		if ok && (!exists || at.After(prev) || (at.Equal(prev) && r.ID > old.ID)) {
			latest[key] = r
		}
	}
	rootKey := func(key string) string {
		seen := map[string]bool{}
		for !seen[key] {
			seen[key] = true
			r, ok := latest[key]
			if !ok || r.Parent == "" {
				break
			}
			if _, ok := latest[r.Parent]; !ok {
				break
			}
			key = r.Parent
		}
		return key
	}
	events := map[string]overviewEvent{}
	for key, r := range latest {
		root := rootKey(key)
		// A parent operation represents its child runs once its row is available.
		if key != root {
			continue
		}
		name := r.Operation
		if name == "" {
			name = r.Pipeline
		}
		at, _ := officialTime(r.Created)
		if t, ok := officialTime(r.Ended); ok {
			at = t
		} else if t, ok := officialTime(r.Started); ok {
			at = t
		}
		state := activityState(r)
		if r.Background && r.Outcome == "succeeded" {
			// Visible child failures are conclusive; a page of completed children
			// cannot prove that every background stage has finished.
			failed := false
			for childKey, child := range latest {
				if childKey != key && rootKey(childKey) == root {
					s := activityState(child)
					if s == "失败" {
						failed = true
					}
					if t, ok := officialTime(child.Created); ok && t.After(at) {
						at = t
					}
				}
			}
			if failed {
				state = "失败"

			}
		}
		project := names[r.Dataset]
		if project == "" {
			project = "全局"
		}
		var failures []activityRow
		for childKey, child := range latest {
			if rootKey(childKey) == root && activityState(child) == "失败" {
				failures = append(failures, child)
			}
		}
		sort.Slice(failures, func(i, j int) bool { return failures[i].ID < failures[j].ID })
		events[root] = overviewEvent{Key: root, Title: activityName(name), State: state, Project: project, At: at, Record: r, Failures: failures}
	}
	result := make([]overviewEvent, 0, len(events))
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

// Re-enumerating from the head on load-more avoids carrying shifting OFFSETs
// between refreshes. Events themselves are never persisted outside this process.
func (c Client) overviewActivity(ctx context.Context, want int) ([]overviewEvent, bool, error) {
	projects := overviewProjects(c.Config)
	if len(projects) == 0 {
		return nil, false, nil
	}
	rows := []activityRow{}
	for offset := 0; offset <= 1000000; offset += 100 {
		var page []activityRow
		if err := c.get(ctx, fmt.Sprintf("/api/v1/activity/pipeline-runs?limit=100&offset=%d", offset), &page); err != nil {
			return nil, false, err
		}
		rows = append(rows, page...)
		events := aggregateActivity(rows, projects)
		// Resolve parents on page boundaries before counting independent events.
		unresolved := false
		known := map[string]bool{}
		for _, r := range rows {
			if r.Run != "" {
				known[r.Run] = true
			}
		}
		for _, r := range rows {
			if r.Parent != "" && !known[r.Parent] {
				unresolved = true
			}
		}
		if len(page) < 100 || (len(events) > want && !unresolved) {
			more := len(events) > want || len(page) == 100
			if len(events) > want {
				events = events[:want]
			}
			return events, more, nil
		}
	}
	return nil, false, fmt.Errorf("活动分页未读完")
}

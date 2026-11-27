package app

import (
	"context"
	"fmt"
	"time"

	appoverview "github.com/zwhu/memlane/app/overview"
	"github.com/zwhu/memlane/app/shell"
)

type overviewSnapshot = appoverview.SnapshotOf[Item]
type firstUseState = appoverview.FirstUseReceipt
type activityRow = appoverview.ActivityRow
type overviewEvent = appoverview.Event

func firstUseDays(c Config, now time.Time) (int, error) {
	return appoverview.FirstUseDays(c.ClientStateFile, c.ConfigFile, now)
}

var officialTime = appoverview.OfficialTime
var activityName = appoverview.ActivityName
var activityState = appoverview.ActivityState

func overviewProjectRefs(projects []Project) []shell.ProjectRef {
	result := make([]shell.ProjectRef, 0, len(projects))
	for _, p := range projects {
		result = append(result, projectRef(p))
	}
	return result
}

func aggregateActivity(rows []activityRow, projects []Project) []overviewEvent {
	return appoverview.AggregateActivity(rows, overviewProjectRefs(projects))
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

// Re-enumerating from the head on load-more avoids carrying shifting OFFSETs
// between refreshes. Events themselves are never persisted outside this process.
func (c Client) overviewActivity(ctx context.Context, want int) ([]overviewEvent, bool, error) {
	projects := overviewProjects(c.Config)
	if len(projects) == 0 {
		return nil, false, nil
	}
	control, ok := c.controlClient()
	if !ok {
		return nil, false, fmt.Errorf("本地网关尚未配置")
	}
	pageSize := min(500, max(100, want*5))
	rows := []activityRow{}
	known := map[string]bool{}
	for offset := 0; offset <= 100000; {
		page, err := control.pipelineRunsV2(ctx, "", pageSize, offset)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, page...)
		for _, r := range page {
			if r.Run != "" {
				known[r.Run] = true
			}
		}
		events := aggregateActivity(rows, projects)
		// Resolve parents on page boundaries before counting independent events.
		unresolved := false
		for _, r := range rows {
			if r.Parent != "" && !known[r.Parent] {
				unresolved = true
				break
			}
		}
		if len(page) < pageSize || (len(events) > want && !unresolved) {
			more := len(events) > want || len(page) == pageSize
			if len(events) > want {
				events = events[:want]
			}
			return events, more, nil
		}
		offset += len(page)
	}
	return nil, false, fmt.Errorf("活动分页未读完")
}

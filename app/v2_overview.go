package app

import (
	"context"
	"net/url"
	"strconv"
)

type v2OverviewDayBucket struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type v2OverviewProjectSummary struct {
	ProjectID      string `json:"project_id"`
	DatasetID      string `json:"dataset_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	RawDocCount    int    `json:"raw_doc_count"`
	SessionCount   int    `json:"session_count"`
	UnreadCount    int    `json:"unread_count"`
	UnreadStateRev int64  `json:"unread_state_rev,omitempty"`
	CatalogVersion string `json:"catalog_version"`
}

type v2OverviewCommonSummary struct {
	DatasetID      string `json:"dataset_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	RawDocCount    int    `json:"raw_doc_count"`
	CatalogVersion string `json:"catalog_version"`
}

type v2OverviewBootstrapResponse struct {
	Generation          int64                      `json:"generation"`
	LocalTZName         string                     `json:"local_tz_name"`
	FirstUseDays        int                        `json:"first_use_days"`
	TotalKnowledgeCount int                        `json:"total_knowledge_count"`
	TotalRawCount       *int                       `json:"total_raw_count,omitempty"`
	DayBuckets          []v2OverviewDayBucket      `json:"day_buckets"`
	Projects            []v2OverviewProjectSummary `json:"projects"`
	Common              *v2OverviewCommonSummary   `json:"common"`
	RecentItems         []v2KnowledgeItem          `json:"recent_items"`
	PipelineRuns        []activityRow              `json:"pipeline_runs"`
	PipelineRunsMore    bool                       `json:"pipeline_runs_more"`
	RecallEvents        []RecallEvent              `json:"recall_events"`
	RecallEventsMore    bool                       `json:"recall_events_more"`
	RecallStartedAt     string                     `json:"recall_started_at"`
}

func v2IdentityHeaders(readingID string) map[string]string {
	if readingID == "" {
		return nil
	}
	return map[string]string{v2HeaderReadingIdentity: readingID}
}

func (c ProjectControlClient) overviewBootstrap(ctx context.Context, readingID string, days, recentLimit, activityLimit, recallLimit int) (v2OverviewBootstrapResponse, error) {
	var out v2OverviewBootstrapResponse
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	if recentLimit > 0 {
		q.Set("recent_limit", strconv.Itoa(min(50, recentLimit)))
	}
	if activityLimit > 0 {
		q.Set("activity_limit", strconv.Itoa(min(50, activityLimit)))
	}
	if recallLimit > 0 {
		q.Set("recall_limit", strconv.Itoa(min(50, recallLimit)))
	}
	path := "/v1/overview/bootstrap"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2OverviewDayResponse struct {
	Day        string            `json:"day"`
	Generation int64             `json:"generation"`
	TotalCount int               `json:"total_count"`
	Items      []v2KnowledgeItem `json:"items"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) overviewDayV2(ctx context.Context, readingID, day string, limit int, cursor string) (v2OverviewDayResponse, error) {
	var out v2OverviewDayResponse
	q := url.Values{}
	q.Set("day", day)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v1/overview/day?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

func buildOverviewFromV2Bootstrap(projects []Project, resp v2OverviewBootstrapResponse, wantActivity int) (overviewSnapshot, map[string]int, []overviewEvent, bool) {
	allowed := make(map[string]bool, len(projects))
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	recent := make([]Item, 0, len(resp.RecentItems))
	for _, row := range resp.RecentItems {
		if row.DocID == "" || (len(allowed) > 0 && !allowed[row.DatasetID]) {
			continue
		}
		recent = append(recent, row.toItem(resp.RecallStartedAt))
	}
	allServerDatasetsAllowed := true
	if len(allowed) > 0 {
		for _, proj := range resp.Projects {
			if proj.DatasetID != "" && !allowed[proj.DatasetID] {
				allServerDatasetsAllowed = false
				break
			}
		}
		if allServerDatasetsAllowed && resp.Common != nil && resp.Common.DatasetID != "" && !allowed[resp.Common.DatasetID] {
			allServerDatasetsAllowed = false
		}
	}
	rawTotal := 0
	if resp.TotalRawCount != nil && allServerDatasetsAllowed {
		rawTotal = *resp.TotalRawCount
	} else {
		for _, proj := range resp.Projects {
			if len(allowed) == 0 || allowed[proj.DatasetID] {
				rawTotal += proj.RawDocCount + proj.SessionCount
			}
		}
		if resp.Common != nil && (len(allowed) == 0 || allowed[resp.Common.DatasetID]) {
			rawTotal += resp.Common.RawDocCount
		}
	}
	buckets := make(map[string]int, len(resp.DayBuckets))
	for _, b := range resp.DayBuckets {
		if b.Day != "" && b.Count > 0 {
			buckets[b.Day] = b.Count
		}
	}
	snapshot := overviewSnapshot{
		Knowledge: recent,
		Raw:       rawTotal,
	}
	events := aggregateActivity(resp.PipelineRuns, projects)
	more := len(events) > wantActivity || resp.PipelineRunsMore
	if len(events) > wantActivity {
		events = events[:wantActivity]
	}
	merged, mergedMore := mergeRecallActivity(events, resp.RecallEvents, projects, wantActivity)
	more = more || resp.RecallEventsMore || mergedMore
	return snapshot, buckets, merged, more
}

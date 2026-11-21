package simpledesktop

import (
	"context"
	"fmt"
)

type overviewBootstrapResponse struct {
	Documents        []Document                       `json:"documents"`
	Sessions         []Session                        `json:"sessions"`
	Previews         map[string]string                `json:"previews"`
	PipelineRuns     []activityRow                    `json:"pipeline_runs"`
	PipelineRunsMore bool                             `json:"pipeline_runs_more"`
	RecallEvents     []RecallEvent                    `json:"recall_events"`
	RecallEventsMore bool                             `json:"recall_events_more"`
	RecallStartedAt  string                           `json:"recall_started_at"`
	RecallCounts     map[string]*KnowledgeRecallStats `json:"recall_counts"`
}

func (c ProjectControlClient) overviewBootstrap(ctx context.Context, activityLimit, recallLimit int) (overviewBootstrapResponse, error) {
	var result overviewBootstrapResponse
	path := fmt.Sprintf("/v1/overview/bootstrap?activity_limit=%d&recall_limit=%d", activityLimit, recallLimit)
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

func buildOverviewFromBootstrap(client Client, projects []Project, resp overviewBootstrapResponse, wantActivity int) (overviewSnapshot, []overviewEvent, bool, bool) {
	allowed := make(map[string]bool, len(projects))
	docsByDataset := make(map[string][]Document, len(projects))
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	filtered := make([]Document, 0, len(resp.Documents))
	for _, doc := range resp.Documents {
		if !allowed[doc.Dataset] {
			continue
		}
		filtered = append(filtered, doc)
	}
	_ = client.rememberKnowledgeDays(filtered)
	for _, doc := range filtered {
		docsByDataset[doc.Dataset] = append(docsByDataset[doc.Dataset], doc)
	}

	sessions := make([]Session, 0, len(resp.Sessions))
	seenSess := make(map[string]bool, len(resp.Sessions))
	for _, s := range resp.Sessions {
		if s.ID == "" || !allowed[s.Dataset] {
			continue
		}
		key := s.User + "/" + s.ID
		if !seenSess[key] {
			seenSess[key] = true
			s.Updated = ""
			sessions = append(sessions, s)
		}
	}

	snapshot := aggregateOverview(projects, docsByDataset, sessions)
	hasRecallCounts := resp.RecallStartedAt != ""
	for n := range snapshot.Knowledge {
		item := &snapshot.Knowledge[n]
		docKey := item.Dataset + "/" + item.ID
		if preview := resp.Previews[docKey]; preview != "" && item.Document != nil {
			withBody := *item
			withBody.Body = preview
			withBody.BodyLoaded = true
			item.Title = overviewReadingTitle(withBody)
			item.ReadingState = "ready"
		}
		if hasRecallCounts {
			if stats, ok := resp.RecallCounts[docKey]; ok && stats != nil {
				copyStats := *stats
				if copyStats.StartedAt == "" {
					copyStats.StartedAt = resp.RecallStartedAt
				}
				item.RecallStats = &copyStats
			} else {
				item.RecallStats = &KnowledgeRecallStats{Count: 0, StartedAt: resp.RecallStartedAt}
			}
		}
	}

	events := aggregateActivity(resp.PipelineRuns, projects)
	more := len(events) > wantActivity || resp.PipelineRunsMore
	if len(events) > wantActivity {
		events = events[:wantActivity]
	}
	merged, mergedMore := mergeRecallActivity(events, resp.RecallEvents, projects, wantActivity)
	more = more || resp.RecallEventsMore || mergedMore
	return snapshot, merged, more, hasRecallCounts
}

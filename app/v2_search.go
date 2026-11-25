package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type v2SearchSessionItem struct {
	SessionID      string `json:"session_id"`
	DatasetID      string `json:"dataset_id"`
	ProjectID      string `json:"project_id"`
	UserID         string `json:"user_id"`
	AgentID        string `json:"agent_id"`
	Label          string `json:"label"`
	MsgCount       int    `json:"msg_count"`
	ToolCalls      int    `json:"tool_calls"`
	LastActivityAt string `json:"last_activity_at"`
	StartedAt      string `json:"started_at"`
}

func (row v2SearchSessionItem) toItem() Item {
	sess := Session{
		ID:      row.SessionID,
		Dataset: row.DatasetID,
		User:    row.UserID,
		Agent:   row.AgentID,
		Updated: row.LastActivityAt,
		Started: row.StartedAt,
	}
	title := row.SessionID
	if strings.TrimSpace(row.Label) != "" {
		title = sessionTitle(row.Label)
	}
	summary := sessionCoverage(SessionDetail{Count: row.MsgCount, ToolCount: row.ToolCalls})
	return Item{
		ID:             row.SessionID,
		Dataset:        row.DatasetID,
		Kind:           "会话",
		Title:          title,
		SessionSummary: summary,
		Date:           row.LastActivityAt,
		Session:        &sess,
		ReadingState:   "ready",
	}
}

type v2SearchRecentResponse struct {
	Generation     int64                 `json:"generation"`
	KnowledgeItems []v2KnowledgeItem     `json:"knowledge_items"`
	SessionItems   []v2SearchSessionItem `json:"session_items"`
	RecentItems    json.RawMessage       `json:"recent_items,omitempty"`
}

func (c ProjectControlClient) searchRecentV2(ctx context.Context, readingID string, limit int, projectID, datasetID string) (v2SearchRecentResponse, error) {
	var out v2SearchRecentResponse
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	if datasetID != "" {
		q.Set("dataset_id", datasetID)
	}
	path := "/v1/search/recent?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2SearchMetadataResponse struct {
	Query      string            `json:"query"`
	Generation int64             `json:"generation"`
	Items      []v2KnowledgeItem `json:"items"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) searchMetadataV2(ctx context.Context, readingID, query, filter string, limit int, projectID, datasetID string) (v2SearchMetadataResponse, error) {
	var out v2SearchMetadataResponse
	q := url.Values{}
	q.Set("q", query)
	if filter == "" {
		filter = "all"
	}
	q.Set("filter", filter)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	if datasetID != "" {
		q.Set("dataset_id", datasetID)
	}
	path := "/v1/search/metadata?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

package app

import (
	"context"
	"net/url"
	"strconv"
)

type v2KnowledgePageOpts struct {
	Cursor                       string
	Direction                    string
	AnchorDocID                  string
	RecallAgentID, RecallEventID string
}

type v2ProjectKnowledgeResponse struct {
	ProjectID      string            `json:"project_id"`
	DatasetID      string            `json:"dataset_id"`
	Generation     int64             `json:"generation"`
	CatalogVersion string            `json:"catalog_version"`
	Sort           string            `json:"sort"`
	TotalFiltered  int               `json:"total_filtered"`
	UnreadCount    int               `json:"unread_count"`
	UnreadStateRev int64             `json:"unread_state_rev,omitempty"`
	CategoryCounts map[string]int    `json:"category_counts"`
	Items          []v2KnowledgeItem `json:"items"`
	NextCursor     string            `json:"next_cursor"`
	HasMore        bool              `json:"has_more"`
	PrevCursor     string            `json:"prev_cursor"`
	HasPrev        bool              `json:"has_prev"`
}

func (c ProjectControlClient) projectKnowledgeV2(ctx context.Context, readingID, projectID, sortBy, category string, unreadOnly bool, recallAgentID, recallEventID string, limit int, cursor string) (v2ProjectKnowledgeResponse, error) {
	return c.projectKnowledgePageV2(ctx, readingID, projectID, sortBy, category, unreadOnly, recallAgentID, recallEventID, limit, v2KnowledgePageOpts{Cursor: cursor})
}

func (c ProjectControlClient) projectKnowledgePageV2(ctx context.Context, readingID, projectID, sortBy, category string, unreadOnly bool, recallAgentID, recallEventID string, limit int, opts v2KnowledgePageOpts) (v2ProjectKnowledgeResponse, error) {
	var out v2ProjectKnowledgeResponse
	q := url.Values{}
	if sortBy != "" {
		q.Set("sort", sortBy)
	}
	if category != "" {
		q.Set("category", category)
	}
	if unreadOnly {
		q.Set("unread_only", "1")
	}
	if recallAgentID != "" && recallEventID != "" {
		q.Set("recall_agent_id", recallAgentID)
		q.Set("recall_event_id", recallEventID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(50, limit)))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Direction == "prev" || opts.Direction == "next" {
		q.Set("direction", opts.Direction)
	}
	if opts.AnchorDocID != "" {
		q.Set("anchor_doc_id", opts.AnchorDocID)
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/knowledge"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2CommonKnowledgeResponse struct {
	DatasetID      string            `json:"dataset_id"`
	Generation     int64             `json:"generation"`
	CatalogVersion string            `json:"catalog_version"`
	CommonCategory string            `json:"common_category"`
	TotalFiltered  int               `json:"total_filtered"`
	CategoryCounts map[string]int    `json:"category_counts"`
	Items          []v2KnowledgeItem `json:"items"`
	NextCursor     string            `json:"next_cursor"`
	HasMore        bool              `json:"has_more"`
	PrevCursor     string            `json:"prev_cursor"`
	HasPrev        bool              `json:"has_prev"`
}

func (c ProjectControlClient) commonKnowledgeV2(ctx context.Context, datasetID, commonCategory string, limit int, cursor string) (v2CommonKnowledgeResponse, error) {
	return c.commonKnowledgePageV2(ctx, datasetID, commonCategory, limit, v2KnowledgePageOpts{Cursor: cursor})
}

func (c ProjectControlClient) commonKnowledgePageV2(ctx context.Context, datasetID, commonCategory string, limit int, opts v2KnowledgePageOpts) (v2CommonKnowledgeResponse, error) {
	var out v2CommonKnowledgeResponse
	q := url.Values{}
	if datasetID != "" {
		q.Set("dataset_id", datasetID)
	}
	if commonCategory == "" {
		commonCategory = "all"
	}
	q.Set("common_category", commonCategory)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(50, limit)))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Direction == "prev" || opts.Direction == "next" {
		q.Set("direction", opts.Direction)
	}
	if opts.AnchorDocID != "" {
		q.Set("anchor_doc_id", opts.AnchorDocID)
	}
	if opts.RecallEventID != "" {
		q.Set("recall_agent_id", opts.RecallAgentID)
		q.Set("recall_event_id", opts.RecallEventID)
	}
	path := "/v1/common/knowledge?" + q.Encode()
	err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024)
	return out, err
}

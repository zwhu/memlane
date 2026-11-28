package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

type documentLookupV2Response struct {
	Document
	Kind           string  `json:"kind"`
	Title          string  `json:"title"`
	PreviewText    string  `json:"preview_text"`
	Content        *string `json:"content"`
	EffectiveDate  string  `json:"effective_date"`
	Category       string  `json:"category"`
	CommonCategory string  `json:"common_category"`
	LessonType     string  `json:"lesson_type"`
	StateKey       string  `json:"state_key"`
	SessionID      string  `json:"session_id"`
	AgentID        string  `json:"agent_id,omitempty"`
	RecallCount    int64   `json:"recall_count"`
}

func (c ProjectControlClient) documentLookupV2(ctx context.Context, datasetID, docID string) (documentLookupV2Response, error) {
	var result documentLookupV2Response
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(docID)
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

type sessionLookupV2Response struct {
	Session
	Status string `json:"status"`
}

func (c ProjectControlClient) sessionLookupV2(ctx context.Context, datasetID, sessionID, userID string) (sessionLookupV2Response, error) {
	var result sessionLookupV2Response
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/sessions/" + url.PathEscape(sessionID)
	if userID != "" {
		path += "?user_id=" + url.QueryEscape(userID)
	}
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

type codeActivityV2Response struct {
	ProjectID        string `json:"project_id"`
	CodeDatasetID    string `json:"code_dataset_id"`
	DatasetCreatedAt string `json:"dataset_created_at"`
	Active           bool   `json:"active"`
	Status           string `json:"status"`
	RunID            string `json:"run_id"`
	LatestCreatedAt  string `json:"latest_created_at"`
	LatestStartedAt  string `json:"latest_started_at"`
	LatestEndedAt    string `json:"latest_ended_at"`
}

func (c ProjectControlClient) projectCodeActivityV2(ctx context.Context, projectID string) (codeActivityV2Response, error) {
	var result codeActivityV2Response
	path := "/v1/projects/" + url.PathEscape(projectID) + "/code-activity"
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

type codeDatasetV2Response struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
}

func (c ProjectControlClient) ensureProjectCodeDatasetV2(ctx context.Context, projectID string) (codeDatasetV2Response, error) {
	var result codeDatasetV2Response
	path := "/v1/projects/" + url.PathEscape(projectID) + "/code-dataset"
	err := c.request(ctx, "POST", path, nil, &result)
	return result, err
}

func (c ProjectControlClient) triggerProjectCodeIndexV2(ctx context.Context, projectID, repoPath string) (codeIndexResult, error) {
	var result codeIndexResult
	longClient := c
	if longClient.HTTP != nil {
		cloned := *longClient.HTTP
		cloned.Timeout = 30 * time.Minute
		longClient.HTTP = &cloned
	} else {
		longClient.HTTP = &http.Client{Timeout: 30 * time.Minute, Transport: sharedHTTPTransport, CheckRedirect: noRedirectFunc}
	}
	payload := map[string]any{}
	if trimmed := strings.TrimSpace(repoPath); trimmed != "" {
		payload["repo_path"] = trimmed
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/code-index"
	err := longClient.request(ctx, "POST", path, payload, &result)
	if settings.IsControlErrorCode(err, http.StatusConflict, "code_index_already_active") {
		return codeIndexResult{}, errOfficialCodeActive
	}
	if err != nil && ctx.Err() != nil {
		return codeIndexResult{}, fmt.Errorf("%s: %w", err.Error(), ctx.Err())
	}
	return result, err
}

func (c ProjectControlClient) pipelineRunsV2(ctx context.Context, datasetID string, limit, offset int) ([]activityRow, error) {
	q := url.Values{}
	if trimmed := strings.TrimSpace(datasetID); trimmed != "" {
		q.Set("dataset_id", trimmed)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	path := "/v1/activity/pipeline-runs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var rows []activityRow
	err := c.requestWithLimit(ctx, "GET", path, nil, &rows, 8*1024*1024)
	return rows, err
}

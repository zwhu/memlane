package app

import (
	"context"
	"net/url"
)

type documentLookupV2Response struct {
	Document
	EffectiveDate  string `json:"effective_date"`
	Category       string `json:"category"`
	CommonCategory string `json:"common_category"`
	SessionID      string `json:"session_id"`
	RecallCount    int64  `json:"recall_count"`
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

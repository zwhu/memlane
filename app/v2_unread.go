package app

import (
	"context"
	"errors"
	"net/http"
)

type v2ReadingStateMarkReadRequest struct {
	ReadingIdentity string   `json:"reading_identity"`
	ProjectID       string   `json:"project_id"`
	DatasetID       string   `json:"dataset_id"`
	DocIDs          []string `json:"doc_ids"`
}

type v2ReadingStateMarkReadResponse struct {
	ScopeHash        string `json:"scope_hash"`
	Marked           int    `json:"marked"`
	UnreadCount      int    `json:"unread_count"`
	UnreadCountKnown *bool  `json:"unread_count_known,omitempty"`
	StateRev         int64  `json:"state_rev,omitempty"`
	ReadHorizonAtNS  string `json:"read_horizon_at_ns"`
}

func (r v2ReadingStateMarkReadResponse) unreadCountKnown() bool {
	return r.UnreadCountKnown == nil || *r.UnreadCountKnown
}

func (c ProjectControlClient) readingStateMarkReadV2(ctx context.Context, req v2ReadingStateMarkReadRequest) (v2ReadingStateMarkReadResponse, error) {
	var out v2ReadingStateMarkReadResponse
	err := c.requestWithLimit(ctx, "POST", "/v1/reading-state/mark-read", req, &out, 256*1024)
	return out, err
}

type v2ReadingStateMarkAllReadRequest struct {
	ReadingIdentity string `json:"reading_identity"`
	ProjectID       string `json:"project_id"`
	DatasetID       string `json:"dataset_id"`
}

type v2ReadingStateMarkAllReadResponse struct {
	ScopeHash        string `json:"scope_hash"`
	UnreadCount      int    `json:"unread_count"`
	UnreadCountKnown *bool  `json:"unread_count_known,omitempty"`
	StateRev         int64  `json:"state_rev,omitempty"`
	ReadHorizonAtNS  string `json:"read_horizon_at_ns"`
}

func (r v2ReadingStateMarkAllReadResponse) unreadCountKnown() bool {
	return r.UnreadCountKnown == nil || *r.UnreadCountKnown
}

func (c ProjectControlClient) readingStateMarkAllReadV2(ctx context.Context, req v2ReadingStateMarkAllReadRequest) (v2ReadingStateMarkAllReadResponse, error) {
	var out v2ReadingStateMarkAllReadResponse
	err := c.requestWithLimit(ctx, "POST", "/v1/reading-state/mark-all-read", req, &out, 256*1024)
	return out, err
}

type v2ProvenanceBackfillRequest struct {
	ReadingIdentity string            `json:"reading_identity"`
	Days            map[string]string `json:"days,omitempty"`
}

type v2ProvenanceBackfillResponse struct {
	Status                 string `json:"status"`
	Matched                int    `json:"matched"`
	Imported               int    `json:"imported"`
	Skipped                int    `json:"skipped"`
	Unmapped               int    `json:"unmapped"`
	HasUnconfirmedProjects bool   `json:"has_unconfirmed_projects"`
	BackupPath             string `json:"backup_path"`
}

func (c ProjectControlClient) provenanceBackfillLegacyDatesV2(ctx context.Context, req v2ProvenanceBackfillRequest) (v2ProvenanceBackfillResponse, error) {
	var out v2ProvenanceBackfillResponse
	err := c.requestWithLimit(ctx, "POST", "/v1/provenance/backfill-legacy-dates", req, &out, 512*1024)
	return out, err
}

func isControlNotFound(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == http.StatusNotFound
}

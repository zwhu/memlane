package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

type usageEventsCursor struct {
	Memory, Dataset, Channel, Kind           string
	CurrentRevision                          int64
	Limit                                    int
	SnapshotMS, HighWater, CoverageStartedMS int64
	LastMS                                   int64
	LastID                                   string
	LastRevision                             int64
}

func (s *Store) encodeUsageEventsCursor(c usageEventsCursor) string {
	data, _ := json.Marshal(c)
	sign := hmac.New(sha256.New, []byte(s.usageEventsKey))
	sign.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(sign.Sum(nil))
}
func (s *Store) decodeUsageEventsCursor(raw string) (usageEventsCursor, error) {
	var out usageEventsCursor
	invalid := access("invalid_cursor", "usage event cursor expired or does not match this page")
	parts := strings.Split(raw, ".")
	if len(raw) > 8192 || len(parts) != 2 {
		return out, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return out, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out, invalid
	}
	sign := hmac.New(sha256.New, []byte(s.usageEventsKey))
	sign.Write(data)
	if !hmac.Equal(signature, sign.Sum(nil)) || json.Unmarshal(data, &out) != nil || out.LastID == "" || out.LastRevision < 1 || out.HighWater < 1 || out.SnapshotMS < 1 {
		return out, invalid
	}
	return out, nil
}

// UsageEvents reads only retained event metadata. It follows the existing local
// Usage current-dataset boundary and never expands moved knowledge to its old
// scope, creates observations, changes coverage, or performs retention cleanup.
func (s *Store) UsageEvents(ctx context.Context, q records.UsageEventsQuery) (records.UsageEventsPage, error) {
	page := records.UsageEventsPage{Memory: q.Memory, Events: []records.UsageEvent{}, RetentionDays: 90}
	if s.Schema < 10 || s.usageEventsKey == "" {
		return page, access("usage_unavailable", "usage event collection is unavailable")
	}
	if q.Memory == "" || len(q.Memory) > 128 || len(q.Dataset) > 128 || q.ExpectedRevision < 0 {
		return page, access("invalid_request", "one exact knowledge target is required")
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 50 {
		return page, access("invalid_request", "usage event page limit must be between 1 and 50")
	}
	if q.Channel == "" {
		q.Channel = "daily"
	}
	switch q.Channel {
	case "daily", "app", "verification", "background":
	default:
		return page, access("invalid_request", "unknown usage event channel")
	}
	switch q.Kind {
	case "", "read", "return", "adopt", "browse", "status":
	default:
		return page, access("invalid_request", "unknown usage event kind")
	}
	page.Channel, page.Kind = q.Channel, q.Kind
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT dataset,current_revision FROM memories WHERE id=?`, q.Memory).Scan(&page.Dataset, &page.CurrentRevision); err != nil {
		return page, err
	}
	if q.Dataset != "" && q.Dataset != page.Dataset || q.ExpectedRevision != 0 && q.ExpectedRevision != page.CurrentRevision {
		return page, access("version_changed", "knowledge target version or scope changed")
	}
	if err = tx.QueryRowContext(ctx, `SELECT started_ms FROM usage_coverage WHERE id=1`).Scan(&page.CoverageStartedMS); err != nil {
		return page, err
	}
	cursor := usageEventsCursor{Memory: q.Memory, Dataset: page.Dataset, CurrentRevision: page.CurrentRevision, Limit: q.Limit, Channel: q.Channel, Kind: q.Kind, SnapshotMS: time.Now().UnixMilli(), CoverageStartedMS: page.CoverageStartedMS}
	if q.Cursor != "" {
		cursor, err = s.decodeUsageEventsCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		if cursor.Memory != q.Memory || cursor.Channel != q.Channel || cursor.Kind != q.Kind || cursor.Limit != q.Limit {
			return page, access("invalid_cursor", "usage event cursor belongs to another target or filter")
		}
		if cursor.Dataset != page.Dataset || cursor.CurrentRevision != page.CurrentRevision || cursor.CoverageStartedMS != page.CoverageStartedMS {
			return page, access("version_changed", "knowledge target or usage coverage changed during pagination")
		}
	} else if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(rowid),0) FROM usage_events`).Scan(&cursor.HighWater); err != nil {
		return page, err
	}
	page.SnapshotMS = cursor.SnapshotMS
	query := `SELECT u.id,u.kind,u.channel,u.observed_ms,u.memory,u.revision,u.dataset,u.connection,u.generation,COALESCE(c.agent,''),u.exposure FROM usage_events u LEFT JOIN connections c ON c.id=u.connection WHERE u.memory=? AND u.dataset=? AND u.channel=? AND u.rowid<=? AND u.observed_ms>=? AND u.observed_ms<=?`
	args := []any{q.Memory, page.Dataset, q.Channel, cursor.HighWater, time.Now().Add(-90 * 24 * time.Hour).UnixMilli(), cursor.SnapshotMS}
	if q.Kind != "" {
		query += ` AND u.kind=?`
		args = append(args, q.Kind)
	}
	if q.Cursor != "" {
		query += ` AND (u.observed_ms<? OR (u.observed_ms=? AND (u.id<? OR (u.id=? AND u.revision<?))))`
		args = append(args, cursor.LastMS, cursor.LastMS, cursor.LastID, cursor.LastID, cursor.LastRevision)
	}
	query += ` ORDER BY u.observed_ms DESC,u.id DESC,u.revision DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var event records.UsageEvent
		if err = rows.Scan(&event.ID, &event.Kind, &event.Channel, &event.ObservedMS, &event.Memory, &event.Revision, &event.Dataset, &event.ConnectionID, &event.Generation, &event.Agent, &event.Exposure); err != nil {
			rows.Close()
			return page, err
		}
		page.Events = append(page.Events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Events) > q.Limit {
		page.HasMore = true
		page.Events = page.Events[:q.Limit]
		last := page.Events[len(page.Events)-1]
		cursor.LastMS, cursor.LastID, cursor.LastRevision = last.ObservedMS, last.ID, last.Revision
		page.NextCursor = s.encodeUsageEventsCursor(cursor)
	}
	if err = tx.Commit(); err != nil {
		return page, err
	}
	return page, nil
}

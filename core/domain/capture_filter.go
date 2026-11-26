package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
)

const captureFilterDDL = `
CREATE TABLE IF NOT EXISTS capture_filter_events (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 agent_id TEXT NOT NULL, host_kind TEXT NOT NULL, session_id TEXT NOT NULL,
 tool_name TEXT NOT NULL, rule TEXT NOT NULL, category TEXT NOT NULL, source TEXT NOT NULL,
 reason TEXT NOT NULL, next_action TEXT NOT NULL, occurred_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_capture_filter_project_time ON capture_filter_events(project_id,occurred_at DESC,id);
CREATE INDEX IF NOT EXISTS idx_capture_filter_time ON capture_filter_events(occurred_at DESC,id);
`

const captureFilterColumns = "id,project_id,agent_id,host_kind,session_id,tool_name,rule,category,source,reason,next_action,occurred_at"
const captureFilterRetention = 200

type CaptureFilterEvent struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	AgentID    string `json:"agent_id"`
	HostKind   string `json:"host_kind"`
	SessionID  string `json:"session_id"`
	ToolName   string `json:"tool_name"`
	Rule       string `json:"rule"`
	Category   string `json:"category"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	NextAction string `json:"next_action"`
	OccurredAt string `json:"occurred_at"`
}

func (s *Store) RecordCaptureFilterEvent(ctx context.Context, event CaptureFilterEvent) (CaptureFilterEvent, error) {
	if err := requireIDs(event.ProjectID); err != nil {
		return CaptureFilterEvent{}, err
	}
	event.ID = defaultText(event.ID, rand.Text())
	event.OccurredAt = defaultText(event.OccurredAt, NowUTCMicro())
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := execSQL(ctx, tx, `INSERT INTO capture_filter_events (`+captureFilterColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			event.ID, event.ProjectID, event.AgentID, event.HostKind, event.SessionID, event.ToolName, event.Rule,
			event.Category, event.Source, event.Reason, event.NextAction, event.OccurredAt); err != nil {
			return insertError(err)
		}
		return execSQL(ctx, tx, `DELETE FROM capture_filter_events WHERE id IN (
			SELECT id FROM capture_filter_events WHERE project_id=? ORDER BY occurred_at DESC,id LIMIT -1 OFFSET ?
		)`, event.ProjectID, captureFilterRetention)
	})
	if err != nil {
		return CaptureFilterEvent{}, err
	}
	return event, nil
}

func (s *Store) ListCaptureFilterEvents(ctx context.Context, projectID string, limit int) ([]CaptureFilterEvent, error) {
	return ReadCaptureFilterEvents(ctx, s.db, projectID, limit)
}

func (s *Store) CountCaptureFilterEvents(ctx context.Context, projectID string) (int, error) {
	return ReadCaptureFilterCount(ctx, s.db, projectID)
}

func captureFilterScope(projectID string) (string, []any) {
	if projectID == "" {
		return "", nil
	}
	return " WHERE project_id=?", []any{projectID}
}

// ReadCaptureFilterEvents supports reads inside the caller's snapshot transaction.
func ReadCaptureFilterEvents(ctx context.Context, db sqlReader, projectID string, limit int) ([]CaptureFilterEvent, error) {
	where, args := captureFilterScope(projectID)
	return queryList(ctx, db, func(row scanner) (CaptureFilterEvent, error) {
		var event CaptureFilterEvent
		err := row.Scan(&event.ID, &event.ProjectID, &event.AgentID, &event.HostKind, &event.SessionID, &event.ToolName,
			&event.Rule, &event.Category, &event.Source, &event.Reason, &event.NextAction, &event.OccurredAt)
		return event, err
	}, "SELECT "+captureFilterColumns+" FROM capture_filter_events"+where+" ORDER BY occurred_at DESC,id LIMIT ?", append(args, pageLimit(limit))...)
}

func ReadCaptureFilterCount(ctx context.Context, db sqlReader, projectID string) (int, error) {
	where, args := captureFilterScope(projectID)
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM capture_filter_events"+where, args...).Scan(&count)
	return count, err
}

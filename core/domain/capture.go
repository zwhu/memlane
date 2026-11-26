package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const captureDDL = `
CREATE TABLE IF NOT EXISTS capture_receipts (
 capture_key TEXT PRIMARY KEY, user_id TEXT NOT NULL, dataset_id TEXT NOT NULL, session_id TEXT NOT NULL,
 message_id TEXT NOT NULL, payload_sha256 TEXT NOT NULL, entry_type TEXT NOT NULL, entry_id TEXT DEFAULT '',
 baseline_count INTEGER, status TEXT NOT NULL CHECK(status IN ('pending','saved')), updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_capture_receipts_status_updated ON capture_receipts(status,updated_at);
CREATE TABLE IF NOT EXISTS capture_inbox (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, dataset_id TEXT NOT NULL, agent_id TEXT NOT NULL, session_id TEXT NOT NULL,
 message_id TEXT NOT NULL, capture_key TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, payload_json TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','processing','succeeded','failed')), last_error TEXT NOT NULL DEFAULT '', last_error_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_status_updated ON capture_inbox(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_project_status ON capture_inbox(project_id,status);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_claim ON capture_inbox(status,created_at);
`

type CaptureReceipt struct {
	CaptureKey, UserID, DatasetID, SessionID, MessageID, PayloadSHA256, EntryType, EntryID string
	BaselineCount                                                                          *int
	Status, UpdatedAt                                                                      string
}

type CaptureInboxItem struct {
	ID, ProjectID, DatasetID, AgentID, SessionID, MessageID, CaptureKey, Kind, PayloadJSON string
	Status, LastError, LastErrorAt                                                         string
	Attempts                                                                               int
	CreatedAt, UpdatedAt                                                                   string
}

const receiptColumns = "capture_key,user_id,dataset_id,session_id,message_id,payload_sha256,entry_type,entry_id,baseline_count,status,updated_at"
const inboxColumns = "id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at"

func getCaptureReceipt(ctx context.Context, db sqlReader, key string) (CaptureReceipt, error) {
	var r CaptureReceipt
	var entryID sql.NullString
	err := db.QueryRowContext(ctx, `SELECT `+receiptColumns+` FROM capture_receipts WHERE capture_key=?`, key).
		Scan(&r.CaptureKey, &r.UserID, &r.DatasetID, &r.SessionID, &r.MessageID, &r.PayloadSHA256, &r.EntryType,
			&entryID, &r.BaselineCount, &r.Status, &r.UpdatedAt)
	r.EntryID = entryID.String
	return r, rowError(err)
}

func (s *Store) GetCaptureReceipt(ctx context.Context, captureKey string) (CaptureReceipt, error) {
	return getCaptureReceipt(ctx, s.db, captureKey)
}

func (s *Store) ReserveCaptureReceipt(ctx context.Context, r CaptureReceipt) (string, CaptureReceipt, error) {
	if err := requireIDs(r.CaptureKey, r.UserID, r.DatasetID, r.SessionID, r.MessageID, r.PayloadSHA256, r.EntryType); err != nil {
		return "", CaptureReceipt{}, err
	}
	var state string
	var result CaptureReceipt
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getCaptureReceipt(ctx, tx, r.CaptureKey)
		if err == nil {
			if existing.UserID != r.UserID || existing.DatasetID != r.DatasetID || existing.SessionID != r.SessionID ||
				existing.MessageID != r.MessageID || existing.PayloadSHA256 != r.PayloadSHA256 || existing.EntryType != r.EntryType {
				return fmt.Errorf("capture receipt scope or payload changed: %w", ErrConflict)
			}
			state, result = existing.Status, existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		r.Status, r.EntryID, r.UpdatedAt = "pending", "", NowUTCMicro()
		if err := execSQL(ctx, tx, `INSERT INTO capture_receipts (`+receiptColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			r.CaptureKey, r.UserID, r.DatasetID, r.SessionID, r.MessageID, r.PayloadSHA256, r.EntryType, r.EntryID, r.BaselineCount, r.Status, r.UpdatedAt); err != nil {
			return err
		}
		state, result = "reserved", r
		return nil
	})
	if err != nil {
		return "", CaptureReceipt{}, err
	}
	return state, result, nil
}

func (s *Store) MarkCaptureReceiptSaved(ctx context.Context, captureKey, entryID string) error {
	if err := requireIDs(captureKey, entryID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		r, err := getCaptureReceipt(ctx, tx, captureKey)
		if err != nil {
			return err
		}
		if r.Status == "saved" {
			if r.EntryID != entryID {
				return fmt.Errorf("capture receipt already saved with another entry: %w", ErrConflict)
			}
			return nil
		}
		return execSQL(ctx, tx, `UPDATE capture_receipts SET status='saved',entry_id=?,updated_at=? WHERE capture_key=?`, entryID, NowUTCMicro(), captureKey)
	})
}

func (s *Store) AbortPendingCaptureReceipt(ctx context.Context, captureKey string) error {
	return execSQL(ctx, s.db, `DELETE FROM capture_receipts WHERE capture_key=? AND status='pending'`, captureKey)
}

func scanInboxItem(row scanner) (CaptureInboxItem, error) {
	var item CaptureInboxItem
	err := row.Scan(&item.ID, &item.ProjectID, &item.DatasetID, &item.AgentID, &item.SessionID, &item.MessageID, &item.CaptureKey,
		&item.Kind, &item.PayloadJSON, &item.Status, &item.LastError, &item.LastErrorAt, &item.Attempts, &item.CreatedAt, &item.UpdatedAt)
	return item, rowError(err)
}

func getInboxItem(ctx context.Context, db sqlReader, id string) (CaptureInboxItem, error) {
	return scanInboxItem(db.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM capture_inbox WHERE id=?`, id))
}

func (s *Store) GetInboxItem(ctx context.Context, id string) (CaptureInboxItem, error) {
	return getInboxItem(ctx, s.db, id)
}

func (s *Store) EnqueueInboxItem(ctx context.Context, item CaptureInboxItem) (CaptureInboxItem, error) {
	if err := requireIDs(item.ProjectID, item.DatasetID, item.AgentID, item.SessionID, item.MessageID, item.Kind); err != nil {
		return CaptureInboxItem{}, err
	}
	if !json.Valid([]byte(item.PayloadJSON)) {
		return CaptureInboxItem{}, fmt.Errorf("invalid capture inbox JSON: %w", ErrInvalidArgument)
	}
	if item.ID == "" {
		item.ID = rand.Text()
	}
	var queued CaptureInboxItem
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		if err := execSQL(ctx, tx, `INSERT INTO capture_inbox (`+inboxColumns+`) VALUES(?,?,?,?,?,?,?,?,?,'pending','','',0,?,?)
			ON CONFLICT(id) DO NOTHING`, item.ID, item.ProjectID, item.DatasetID, item.AgentID, item.SessionID,
			item.MessageID, item.CaptureKey, item.Kind, item.PayloadJSON, defaultText(item.CreatedAt, now), now); err != nil {
			return err
		}
		var err error
		queued, err = getInboxItem(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		if queued.ProjectID != item.ProjectID || queued.DatasetID != item.DatasetID || queued.AgentID != item.AgentID ||
			queued.SessionID != item.SessionID || queued.MessageID != item.MessageID || queued.CaptureKey != item.CaptureKey ||
			queued.Kind != item.Kind || queued.PayloadJSON != item.PayloadJSON {
			return fmt.Errorf("capture inbox payload changed: %w", ErrConflict)
		}
		return nil
	})
	if err != nil {
		return CaptureInboxItem{}, err
	}
	return queued, nil
}

func (s *Store) ClaimNextInboxItem(ctx context.Context) (CaptureInboxItem, error) {
	return scanInboxItem(s.db.QueryRowContext(ctx, `UPDATE capture_inbox SET status='processing',attempts=attempts+1,updated_at=?
		WHERE id=(SELECT id FROM capture_inbox WHERE status='pending' ORDER BY created_at,rowid LIMIT 1)
		RETURNING `+inboxColumns, NowUTCMicro()))
}

func (s *Store) transitionInboxItem(ctx context.Context, id, from, to, lastError string) (CaptureInboxItem, error) {
	var item CaptureInboxItem
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now, lastErrorAt := NowUTCMicro(), ""
		if to == "failed" {
			lastErrorAt = now
		} else {
			lastError = ""
		}
		var err error
		item, err = scanInboxItem(tx.QueryRowContext(ctx, `UPDATE capture_inbox SET status=?,last_error=?,last_error_at=?,updated_at=?
			WHERE id=? AND status=? RETURNING `+inboxColumns, to, lastError, lastErrorAt, now, id, from))
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := getInboxItem(ctx, tx, id); err != nil {
			return err
		}
		return fmt.Errorf("capture inbox item is not %s: %w", from, ErrConflict)
	})
	if err != nil {
		return CaptureInboxItem{}, err
	}
	return item, nil
}

func (s *Store) MarkInboxSucceeded(ctx context.Context, id string) error {
	_, err := s.transitionInboxItem(ctx, id, "processing", "succeeded", "")
	return err
}

func (s *Store) MarkInboxFailed(ctx context.Context, id, lastError string) error {
	_, err := s.transitionInboxItem(ctx, id, "processing", "failed", lastError)
	return err
}

func (s *Store) RetryInboxItem(ctx context.Context, id string) (CaptureInboxItem, error) {
	return s.transitionInboxItem(ctx, id, "failed", "pending", "")
}

func (s *Store) ListInboxItems(ctx context.Context, projectID, status string, limit int) ([]CaptureInboxItem, error) {
	query := `SELECT ` + inboxColumns + ` FROM capture_inbox WHERE 1=1`
	var args []any
	if projectID != "" {
		query += " AND project_id=?"
		args = append(args, projectID)
	}
	if status != "" {
		query += " AND status=?"
		args = append(args, status)
	}
	args = append(args, pageLimit(limit))
	return queryList(ctx, s.db, scanInboxItem, query+" ORDER BY created_at DESC,id LIMIT ?", args...)
}

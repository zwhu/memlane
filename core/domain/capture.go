package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const captureInboxV5IndexesDDL = `
CREATE INDEX IF NOT EXISTS idx_capture_inbox_capture_key ON capture_inbox(capture_key);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_dataset_session_status ON capture_inbox(dataset_id,session_id,status);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_dataset_session_agent ON capture_inbox(dataset_id,session_id,updated_at DESC,agent_id);
`

const sessionFlushDDL = `
CREATE TABLE IF NOT EXISTS session_flush_jobs (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, dataset_id TEXT NOT NULL, session_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed')),
 last_error TEXT NOT NULL DEFAULT '', last_error_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, rerun_requested INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(dataset_id,session_id)
);
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_status_updated ON session_flush_jobs(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_project_status ON session_flush_jobs(project_id,status);
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_claim ON session_flush_jobs(status,created_at);
`

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
 status TEXT NOT NULL CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed')), last_error TEXT NOT NULL DEFAULT '', last_error_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_status_updated ON capture_inbox(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_project_status ON capture_inbox(project_id,status);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_claim ON capture_inbox(status,created_at);
` + captureInboxV5IndexesDDL + sessionFlushDDL

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

type SessionFlushJob struct {
	ID, ProjectID, DatasetID, SessionID string
	Status, LastError, LastErrorAt      string
	Attempts                            int
	RerunRequested                      bool
	CreatedAt, UpdatedAt                string
}

type CaptureLimits struct {
	MaxPendingItems int
	MaxPendingBytes int64
}

type EnqueueCaptureInput struct {
	Receipt         CaptureReceipt
	Inbox           CaptureInboxItem
	SessionBinding  *SessionBinding
	SessionRelation *SessionRelation
	Limits          CaptureLimits
}

type EnqueueCaptureResult struct {
	Receipt      CaptureReceipt
	InboxItem    CaptureInboxItem
	HasInboxItem bool
	Deduplicated bool
}

const receiptColumns = "capture_key,user_id,dataset_id,session_id,message_id,payload_sha256,entry_type,entry_id,baseline_count,status,updated_at"
const inboxColumns = "id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at"
const sessionFlushColumns = "id,project_id,dataset_id,session_id,status,last_error,last_error_at,attempts,rerun_requested,created_at,updated_at"

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

func receiptScopeMatches(a, b CaptureReceipt) bool {
	return a.UserID == b.UserID && a.DatasetID == b.DatasetID && a.SessionID == b.SessionID &&
		a.MessageID == b.MessageID && a.PayloadSHA256 == b.PayloadSHA256 && a.EntryType == b.EntryType
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
			if !receiptScopeMatches(existing, r) {
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

func checkInboxCapacityTx(ctx context.Context, tx *sql.Tx, limits CaptureLimits, payloadBytes int) error {
	if limits.MaxPendingItems <= 0 && limits.MaxPendingBytes <= 0 {
		return nil
	}
	var pendingCount int
	var pendingBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(payload_json AS BLOB))),0)
		FROM capture_inbox WHERE status IN ('pending','processing','failed')`).Scan(&pendingCount, &pendingBytes); err != nil {
		return err
	}
	if (limits.MaxPendingItems > 0 && pendingCount >= limits.MaxPendingItems) ||
		(limits.MaxPendingBytes > 0 && pendingBytes+int64(payloadBytes) > limits.MaxPendingBytes) {
		return fmt.Errorf("capture inbox capacity exceeded: %w", ErrInboxCapacityExceeded)
	}
	return nil
}

const projectDatasetMatchSQL = `EXISTS(SELECT 1 FROM projects p WHERE p.id=? AND LOWER(REPLACE(p.dataset_id,'-',''))=LOWER(REPLACE(?,'-','')))`

func projectMatchesDatasetTx(ctx context.Context, tx *sql.Tx, projectID, datasetID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT `+projectDatasetMatchSQL, projectID, datasetID).Scan(&exists)
	return exists, err
}

func (s *Store) ReserveAndEnqueueCapture(ctx context.Context, in EnqueueCaptureInput) (EnqueueCaptureResult, error) {
	r := in.Receipt
	item := in.Inbox
	if err := requireIDs(r.CaptureKey, r.UserID, r.DatasetID, r.SessionID, r.MessageID, r.PayloadSHA256, r.EntryType); err != nil {
		return EnqueueCaptureResult{}, err
	}
	if err := requireIDs(item.ProjectID, item.DatasetID, item.AgentID, item.SessionID, item.MessageID, item.Kind); err != nil {
		return EnqueueCaptureResult{}, err
	}
	if !json.Valid([]byte(item.PayloadJSON)) {
		return EnqueueCaptureResult{}, fmt.Errorf("invalid capture inbox JSON: %w", ErrInvalidArgument)
	}
	if in.SessionRelation != nil && (strings.TrimSpace(in.SessionRelation.ParentSessionID) != "" || strings.TrimSpace(in.SessionRelation.SubagentRole) != "") {
		if strings.TrimSpace(in.SessionRelation.ParentSessionID) == strings.TrimSpace(in.SessionRelation.SessionID) {
			return EnqueueCaptureResult{}, fmt.Errorf("session cannot be its own parent: %w", ErrInvalidArgument)
		}
	}
	if item.ID == "" {
		item.ID = rand.Text()
	}
	var out EnqueueCaptureResult
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		projectExists, err := projectMatchesDatasetTx(ctx, tx, item.ProjectID, item.DatasetID)
		if err != nil {
			return err
		}
		if !projectExists {
			return fmt.Errorf("project %s removed or dataset mismatched: %w", item.ProjectID, ErrNotFound)
		}
		existing, err := getCaptureReceipt(ctx, tx, r.CaptureKey)
		if err == nil {
			if !receiptScopeMatches(existing, r) {
				return fmt.Errorf("capture receipt scope or payload changed: %w", ErrConflict)
			}
			if in.SessionBinding != nil && strings.TrimSpace(in.SessionBinding.SessionKey) != "" {
				if err := upsertSessionBindingTx(ctx, tx, in.SessionBinding.SessionKey, in.SessionBinding.ProjectID, in.SessionBinding.UpdatedAt); err != nil {
					return err
				}
			}
			if in.SessionRelation != nil && (strings.TrimSpace(in.SessionRelation.ParentSessionID) != "" || strings.TrimSpace(in.SessionRelation.SubagentRole) != "") {
				if err := upsertSessionRelationTx(ctx, tx, *in.SessionRelation); err != nil {
					return err
				}
			}
			if found, findErr := getInboxItemByCaptureKey(ctx, tx, r.CaptureKey, item.ID); findErr == nil {
				out = EnqueueCaptureResult{
					Receipt:      existing,
					InboxItem:    found,
					HasInboxItem: true,
					Deduplicated: true,
				}
				return nil
			} else if !errors.Is(findErr, ErrNotFound) {
				return findErr
			}
			if existing.Status == "saved" {
				out = EnqueueCaptureResult{
					Receipt:      existing,
					HasInboxItem: false,
					Deduplicated: true,
				}
				return nil
			}
			// Repair legacy v4 pending receipt that never had a capture_inbox row committed.
			if err := checkInboxCapacityTx(ctx, tx, in.Limits, len(item.PayloadJSON)); err != nil {
				return err
			}
			queued, err := enqueueInboxItemTx(ctx, tx, item, NowUTCMicro())
			if err != nil {
				return err
			}
			out = EnqueueCaptureResult{
				Receipt:      existing,
				InboxItem:    queued,
				HasInboxItem: true,
				Deduplicated: false,
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := checkInboxCapacityTx(ctx, tx, in.Limits, len(item.PayloadJSON)); err != nil {
			return err
		}
		now := NowUTCMicro()
		r.Status, r.EntryID, r.UpdatedAt = "pending", "", now
		if err := execSQL(ctx, tx, `INSERT INTO capture_receipts (`+receiptColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			r.CaptureKey, r.UserID, r.DatasetID, r.SessionID, r.MessageID, r.PayloadSHA256, r.EntryType, r.EntryID, r.BaselineCount, r.Status, r.UpdatedAt); err != nil {
			return err
		}
		if in.SessionBinding != nil && strings.TrimSpace(in.SessionBinding.SessionKey) != "" {
			if err := upsertSessionBindingTx(ctx, tx, in.SessionBinding.SessionKey, in.SessionBinding.ProjectID, in.SessionBinding.UpdatedAt); err != nil {
				return err
			}
		}
		if in.SessionRelation != nil && (strings.TrimSpace(in.SessionRelation.ParentSessionID) != "" || strings.TrimSpace(in.SessionRelation.SubagentRole) != "") {
			if err := upsertSessionRelationTx(ctx, tx, *in.SessionRelation); err != nil {
				return err
			}
		}
		queued, err := enqueueInboxItemTx(ctx, tx, item, now)
		if err != nil {
			return err
		}
		out = EnqueueCaptureResult{
			Receipt:      r,
			InboxItem:    queued,
			HasInboxItem: true,
			Deduplicated: false,
		}
		return nil
	})
	if err != nil {
		return EnqueueCaptureResult{}, err
	}
	return out, nil
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
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `DELETE FROM capture_receipts WHERE capture_key=? AND status='pending'`, captureKey)
	})
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

func getInboxItemByCaptureKey(ctx context.Context, db sqlReader, captureKey, preferredID string) (CaptureInboxItem, error) {
	if preferredID != "" {
		if item, err := getInboxItem(ctx, db, preferredID); err == nil {
			return item, nil
		} else if !errors.Is(err, ErrNotFound) {
			return CaptureInboxItem{}, err
		}
	}
	return scanInboxItem(db.QueryRowContext(ctx,
		`SELECT `+inboxColumns+` FROM capture_inbox WHERE capture_key=? ORDER BY created_at DESC,rowid DESC LIMIT 1`, captureKey))
}

func (s *Store) GetInboxItem(ctx context.Context, id string) (CaptureInboxItem, error) {
	return getInboxItem(ctx, s.db, id)
}

func enqueueInboxItemTx(ctx context.Context, tx *sql.Tx, item CaptureInboxItem, now string) (CaptureInboxItem, error) {
	if err := execSQL(ctx, tx, `INSERT INTO capture_inbox (`+inboxColumns+`) VALUES(?,?,?,?,?,?,?,?,?,'pending','','',0,?,?)
		ON CONFLICT(id) DO NOTHING`, item.ID, item.ProjectID, item.DatasetID, item.AgentID, item.SessionID,
		item.MessageID, item.CaptureKey, item.Kind, item.PayloadJSON, defaultText(item.CreatedAt, now), now); err != nil {
		return CaptureInboxItem{}, err
	}
	queued, err := getInboxItem(ctx, tx, item.ID)
	if err != nil {
		return CaptureInboxItem{}, err
	}
	if queued.ProjectID != item.ProjectID || queued.DatasetID != item.DatasetID || queued.AgentID != item.AgentID ||
		queued.SessionID != item.SessionID || queued.MessageID != item.MessageID || queued.CaptureKey != item.CaptureKey ||
		queued.Kind != item.Kind || queued.PayloadJSON != item.PayloadJSON {
		return CaptureInboxItem{}, fmt.Errorf("capture inbox payload changed: %w", ErrConflict)
	}
	return queued, nil
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
		var err error
		queued, err = enqueueInboxItemTx(ctx, tx, item, NowUTCMicro())
		return err
	})
	if err != nil {
		return CaptureInboxItem{}, err
	}
	return queued, nil
}

func (s *Store) ClaimNextInboxItem(ctx context.Context) (CaptureInboxItem, error) {
	return s.ClaimNextInboxItemExcludingDatasets(ctx, nil)
}

func (s *Store) ClaimNextInboxItemExcludingDatasets(ctx context.Context, excludedDatasets []string) (CaptureInboxItem, error) {
	var item CaptureInboxItem
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		args := make([]any, 0, len(excludedDatasets)+1)
		args = append(args, now)
		excludeClause := ""
		if len(excludedDatasets) > 0 {
			placeholders := make([]string, len(excludedDatasets))
			for i, ds := range excludedDatasets {
				placeholders[i] = "?"
				args = append(args, ds)
			}
			excludeClause = " AND dataset_id NOT IN (" + strings.Join(placeholders, ",") + ")"
		}
		var err error
		item, err = scanInboxItem(tx.QueryRowContext(ctx, `UPDATE capture_inbox SET status='processing',attempts=attempts+1,updated_at=?
			WHERE id=(
				SELECT ci.id FROM capture_inbox ci
				WHERE ci.status='pending'`+excludeClause+`
				  AND EXISTS (
				      SELECT 1 FROM projects p
				      WHERE p.id=ci.project_id AND LOWER(REPLACE(p.dataset_id,'-',''))=LOWER(REPLACE(ci.dataset_id,'-',''))
				  )
				ORDER BY ci.created_at,ci.rowid LIMIT 1
			)
			RETURNING `+inboxColumns, args...))
		return err
	})
	if err != nil {
		return CaptureInboxItem{}, err
	}
	return item, nil
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
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getInboxItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Status != "processing" {
			return fmt.Errorf("capture inbox item is not processing: %w", ErrConflict)
		}
		active, err := projectMatchesDatasetTx(ctx, tx, existing.ProjectID, existing.DatasetID)
		if err != nil {
			return err
		}
		now := NowUTCMicro()
		nextStatus := "failed"
		if !active {
			nextStatus = "cancelled_project_removed"
		}
		return execSQL(ctx, tx, `UPDATE capture_inbox SET status=?,last_error=?,last_error_at=?,updated_at=?
			WHERE id=? AND status='processing'`, nextStatus, lastError, now, now, id)
	})
}

func (s *Store) RequeueInboxItem(ctx context.Context, id string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getInboxItem(ctx, tx, id)
		if errors.Is(err, ErrNotFound) || (err == nil && existing.Status != "processing") {
			return nil
		}
		if err != nil {
			return err
		}
		active, err := projectMatchesDatasetTx(ctx, tx, existing.ProjectID, existing.DatasetID)
		if err != nil {
			return err
		}
		nextStatus := "pending"
		if !active {
			nextStatus = "cancelled_project_removed"
		}
		return execSQL(ctx, tx,
			`UPDATE capture_inbox SET status=?,updated_at=? WHERE id=? AND status='processing'`,
			nextStatus, NowUTCMicro(), id)
	})
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

func (s *Store) PruneSucceededInboxPayloads(ctx context.Context, olderThan string) (int64, error) {
	var pruned int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		var res sql.Result
		var err error
		if strings.TrimSpace(olderThan) == "" {
			res, err = tx.ExecContext(ctx,
				`UPDATE capture_inbox SET payload_json='{}',updated_at=? WHERE status='succeeded' AND payload_json<>'{}'`,
				now)
		} else {
			res, err = tx.ExecContext(ctx,
				`UPDATE capture_inbox SET payload_json='{}',updated_at=? WHERE status='succeeded' AND payload_json<>'{}' AND updated_at<?`,
				now, olderThan)
		}
		if err != nil {
			return err
		}
		pruned, err = res.RowsAffected()
		return err
	})
	return pruned, err
}

func scanSessionFlushJob(row scanner) (SessionFlushJob, error) {
	var job SessionFlushJob
	var rerun int
	err := row.Scan(&job.ID, &job.ProjectID, &job.DatasetID, &job.SessionID, &job.Status,
		&job.LastError, &job.LastErrorAt, &job.Attempts, &rerun, &job.CreatedAt, &job.UpdatedAt)
	job.RerunRequested = intToBool(rerun)
	return job, rowError(err)
}

func getSessionFlushJobByID(ctx context.Context, db sqlReader, id string) (SessionFlushJob, error) {
	return scanSessionFlushJob(db.QueryRowContext(ctx, `SELECT `+sessionFlushColumns+` FROM session_flush_jobs WHERE id=?`, id))
}

func getSessionFlushJobByKey(ctx context.Context, db sqlReader, datasetID, sessionID string) (SessionFlushJob, error) {
	return scanSessionFlushJob(db.QueryRowContext(ctx,
		`SELECT `+sessionFlushColumns+` FROM session_flush_jobs WHERE dataset_id=? AND session_id=?`, datasetID, sessionID))
}

func (s *Store) GetSessionFlushJob(ctx context.Context, datasetID, sessionID string) (SessionFlushJob, error) {
	return getSessionFlushJobByKey(ctx, s.db, datasetID, sessionID)
}

func (s *Store) EnqueueSessionFlushJob(ctx context.Context, projectID, datasetID, sessionID string) (SessionFlushJob, bool, error) {
	projectID = strings.TrimSpace(projectID)
	datasetID = strings.TrimSpace(datasetID)
	sessionID = strings.TrimSpace(sessionID)
	if err := requireIDs(datasetID, sessionID); err != nil {
		return SessionFlushJob{}, false, err
	}
	var out SessionFlushJob
	var queued bool
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if projectID == "" {
			var resolved string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE LOWER(REPLACE(dataset_id,'-',''))=LOWER(REPLACE(?,'-','')) LIMIT 1`, datasetID).Scan(&resolved); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("project for dataset %s not found: %w", datasetID, ErrNotFound)
				}
				return err
			}
			projectID = resolved
		} else {
			projectExists, err := projectMatchesDatasetTx(ctx, tx, projectID, datasetID)
			if err != nil {
				return err
			}
			if !projectExists {
				return fmt.Errorf("project %s removed or dataset mismatched: %w", projectID, ErrNotFound)
			}
		}
		now := NowUTCMicro()
		existing, err := getSessionFlushJobByKey(ctx, tx, datasetID, sessionID)
		if err == nil {
			effectiveProject := projectID
			if effectiveProject == "" {
				effectiveProject = existing.ProjectID
			}
			if existing.Status == "pending" {
				if effectiveProject != existing.ProjectID {
					out, err = scanSessionFlushJob(tx.QueryRowContext(ctx,
						`UPDATE session_flush_jobs SET project_id=?,updated_at=? WHERE id=? RETURNING `+sessionFlushColumns,
						effectiveProject, now, existing.ID))
					if err != nil {
						return err
					}
					queued = false
					return nil
				}
				out, queued = existing, false
				return nil
			}
			if existing.Status == "processing" {
				out, err = scanSessionFlushJob(tx.QueryRowContext(ctx,
					`UPDATE session_flush_jobs SET project_id=?,rerun_requested=1,updated_at=? WHERE id=? RETURNING `+sessionFlushColumns,
					effectiveProject, now, existing.ID))
				if err != nil {
					return err
				}
				queued = false
				return nil
			}
			out, err = scanSessionFlushJob(tx.QueryRowContext(ctx,
				`UPDATE session_flush_jobs SET project_id=?,status='pending',rerun_requested=0,last_error='',last_error_at='',updated_at=?
				 WHERE id=? RETURNING `+sessionFlushColumns, effectiveProject, now, existing.ID))
			if err != nil {
				return err
			}
			queued = true
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		id := "flush:" + rand.Text()
		out, err = scanSessionFlushJob(tx.QueryRowContext(ctx,
			`INSERT INTO session_flush_jobs (`+sessionFlushColumns+`) VALUES(?,?,?,?,'pending','','',0,0,?,?) RETURNING `+sessionFlushColumns,
			id, projectID, datasetID, sessionID, now, now))
		if err != nil {
			return err
		}
		queued = true
		return nil
	})
	if err != nil {
		return SessionFlushJob{}, false, err
	}
	return out, queued, nil
}

func (s *Store) claimSessionFlushWhere(ctx context.Context, extraWhere string, extraArgs []any) (SessionFlushJob, error) {
	var job SessionFlushJob
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		args := make([]any, 0, len(extraArgs)+1)
		args = append(args, now)
		args = append(args, extraArgs...)
		var err error
		job, err = scanSessionFlushJob(tx.QueryRowContext(ctx, `UPDATE session_flush_jobs SET status='processing',attempts=attempts+1,updated_at=?
			WHERE id=(
				SELECT sf.id FROM session_flush_jobs sf
				WHERE sf.status='pending'`+extraWhere+`
				  AND EXISTS (
				      SELECT 1 FROM projects p
				      WHERE p.id=sf.project_id AND LOWER(REPLACE(p.dataset_id,'-',''))=LOWER(REPLACE(sf.dataset_id,'-',''))
				  )
				  AND NOT EXISTS (
				      SELECT 1 FROM capture_inbox ci
				      WHERE ci.dataset_id=sf.dataset_id AND ci.session_id=sf.session_id
				        AND ci.status IN ('pending','processing','failed')
				        AND EXISTS (SELECT 1 FROM projects p2 WHERE p2.id=ci.project_id)
				  )
				ORDER BY sf.updated_at ASC,sf.created_at ASC,sf.id ASC LIMIT 1
			)
			RETURNING `+sessionFlushColumns, args...))
		return err
	})
	if err != nil {
		return SessionFlushJob{}, err
	}
	return job, nil
}

func (s *Store) ClaimNextSessionFlushExcludingDatasets(ctx context.Context, excludedDatasets []string) (SessionFlushJob, error) {
	if len(excludedDatasets) == 0 {
		return s.claimSessionFlushWhere(ctx, "", nil)
	}
	placeholders := make([]string, len(excludedDatasets))
	args := make([]any, len(excludedDatasets))
	for i, ds := range excludedDatasets {
		placeholders[i] = "?"
		args[i] = ds
	}
	return s.claimSessionFlushWhere(ctx, " AND sf.dataset_id NOT IN ("+strings.Join(placeholders, ",")+")", args)
}

func (s *Store) ClaimNextSessionFlushInDatasets(ctx context.Context, datasets []string) (SessionFlushJob, error) {
	if len(datasets) == 0 {
		return SessionFlushJob{}, ErrNotFound
	}
	placeholders := make([]string, len(datasets))
	args := make([]any, len(datasets))
	for i, ds := range datasets {
		placeholders[i] = "?"
		args[i] = ds
	}
	return s.claimSessionFlushWhere(ctx, " AND sf.dataset_id IN ("+strings.Join(placeholders, ",")+")", args)
}

func (s *Store) transitionSessionFlushJob(ctx context.Context, id, from, to, lastError string) (SessionFlushJob, error) {
	var job SessionFlushJob
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now, lastErrorAt := NowUTCMicro(), ""
		if to == "failed" {
			lastErrorAt = now
		} else {
			lastError = ""
		}
		var err error
		job, err = scanSessionFlushJob(tx.QueryRowContext(ctx, `UPDATE session_flush_jobs SET status=?,last_error=?,last_error_at=?,updated_at=?
			WHERE id=? AND status=? RETURNING `+sessionFlushColumns, to, lastError, lastErrorAt, now, id, from))
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := getSessionFlushJobByID(ctx, tx, id); err != nil {
			return err
		}
		return fmt.Errorf("session flush job is not %s: %w", from, ErrConflict)
	})
	if err != nil {
		return SessionFlushJob{}, err
	}
	return job, nil
}

func (s *Store) MarkSessionFlushSucceeded(ctx context.Context, id string) (bool, error) {
	requeued := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getSessionFlushJobByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Status != "processing" {
			return fmt.Errorf("session flush job is not processing: %w", ErrConflict)
		}
		active, err := projectMatchesDatasetTx(ctx, tx, existing.ProjectID, existing.DatasetID)
		if err != nil {
			return err
		}
		now := NowUTCMicro()
		if !active {
			return execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='cancelled_project_removed',rerun_requested=0,last_error='',last_error_at='',updated_at=?
				 WHERE id=? AND status='processing'`, now, id)
		}
		if existing.RerunRequested {
			requeued = true
			return execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='pending',rerun_requested=0,last_error='',last_error_at='',updated_at=?
				 WHERE id=? AND status='processing'`, now, id)
		}
		return execSQL(ctx, tx,
			`UPDATE session_flush_jobs SET status='succeeded',last_error='',last_error_at='',updated_at=?
			 WHERE id=? AND status='processing'`, now, id)
	})
	return requeued, err
}

func (s *Store) MarkSessionFlushFailed(ctx context.Context, id, lastError string) (bool, error) {
	requeued := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getSessionFlushJobByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Status != "processing" {
			return fmt.Errorf("session flush job is not processing: %w", ErrConflict)
		}
		active, err := projectMatchesDatasetTx(ctx, tx, existing.ProjectID, existing.DatasetID)
		if err != nil {
			return err
		}
		now := NowUTCMicro()
		if !active {
			return execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='cancelled_project_removed',rerun_requested=0,last_error=?,last_error_at=?,updated_at=?
				 WHERE id=? AND status='processing'`, lastError, now, now, id)
		}
		if existing.RerunRequested {
			requeued = true
			return execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='pending',rerun_requested=0,last_error=?,last_error_at=?,updated_at=?
				 WHERE id=? AND status='processing'`, lastError, now, now, id)
		}
		return execSQL(ctx, tx,
			`UPDATE session_flush_jobs SET status='failed',last_error=?,last_error_at=?,updated_at=?
			 WHERE id=? AND status='processing'`, lastError, now, now, id)
	})
	return requeued, err
}

func (s *Store) RequeueSessionFlush(ctx context.Context, id string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		existing, err := getSessionFlushJobByID(ctx, tx, id)
		if errors.Is(err, ErrNotFound) || (err == nil && existing.Status != "processing") {
			return nil
		}
		if err != nil {
			return err
		}
		active, err := projectMatchesDatasetTx(ctx, tx, existing.ProjectID, existing.DatasetID)
		if err != nil {
			return err
		}
		if !active {
			return execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='cancelled_project_removed',rerun_requested=0,updated_at=? WHERE id=? AND status='processing'`,
				NowUTCMicro(), id)
		}
		return execSQL(ctx, tx,
			`UPDATE session_flush_jobs SET status='pending',updated_at=? WHERE id=? AND status='processing'`,
			NowUTCMicro(), id)
	})
}

func (s *Store) RetrySessionFlushJob(ctx context.Context, id string) (SessionFlushJob, error) {
	return s.transitionSessionFlushJob(ctx, id, "failed", "pending", "")
}

func (s *Store) ListSessionFlushJobs(ctx context.Context, projectID, status string, limit int) ([]SessionFlushJob, error) {
	query := `SELECT ` + sessionFlushColumns + ` FROM session_flush_jobs WHERE 1=1`
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
	return queryList(ctx, s.db, scanSessionFlushJob, query+" ORDER BY created_at DESC,id LIMIT ?", args...)
}

func (s *Store) HasPendingSessionFlushJobs(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_flush_jobs WHERE status='pending')`).Scan(&exists)
	return exists, err
}

func (s *Store) RecoverStaleProcessing(ctx context.Context) (int, int, error) {
	return s.RecoverStaleProcessingBefore(ctx, "")
}

func (s *Store) RecoverStaleProcessingBefore(ctx context.Context, cutoff string) (int, int, error) {
	cutoff = strings.TrimSpace(cutoff)
	var recoveredInbox, recoveredFlushes int
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		cutoffClause := ""
		args := []any{now}
		if cutoff != "" {
			cutoffClause = " AND updated_at<=?"
			args = append(args, cutoff)
		}
		// Cancel orphaned processing rows whose owning project was removed.
		if err := execSQL(ctx, tx,
			`UPDATE capture_inbox SET status='cancelled_project_removed',updated_at=?
			 WHERE status='processing'`+cutoffClause+`
			   AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id=capture_inbox.project_id AND LOWER(REPLACE(p.dataset_id,'-',''))=LOWER(REPLACE(capture_inbox.dataset_id,'-','')))`,
			args...); err != nil {
			return err
		}
		if err := execSQL(ctx, tx,
			`UPDATE session_flush_jobs SET status='cancelled_project_removed',rerun_requested=0,updated_at=?
			 WHERE status='processing'`+cutoffClause+`
			   AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id=session_flush_jobs.project_id AND LOWER(REPLACE(p.dataset_id,'-',''))=LOWER(REPLACE(session_flush_jobs.dataset_id,'-','')))`,
			args...); err != nil {
			return err
		}
		resInbox, err := tx.ExecContext(ctx, `UPDATE capture_inbox SET status='pending',updated_at=? WHERE status='processing'`+cutoffClause, args...)
		if err != nil {
			return err
		}
		if n, err := resInbox.RowsAffected(); err == nil {
			recoveredInbox = int(n)
		}
		resFlush, err := tx.ExecContext(ctx, `UPDATE session_flush_jobs SET status='pending',updated_at=? WHERE status='processing'`+cutoffClause, args...)
		if err != nil {
			return err
		}
		if n, err := resFlush.RowsAffected(); err == nil {
			recoveredFlushes = int(n)
		}
		return nil
	})
	return recoveredInbox, recoveredFlushes, err
}

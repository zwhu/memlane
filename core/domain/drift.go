package domain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const driftDDL = `
CREATE TABLE IF NOT EXISTS session_project_hits (
 bound_dataset_id TEXT NOT NULL, session_id TEXT NOT NULL, target_project_id TEXT NOT NULL,
 read_count INTEGER NOT NULL DEFAULT 0, write_count INTEGER NOT NULL DEFAULT 0,
 command_count INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL,
 PRIMARY KEY(bound_dataset_id, session_id, target_project_id)
);
CREATE TABLE IF NOT EXISTS session_drift_seen (
 bound_dataset_id TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL,
 recorded_at TEXT NOT NULL, PRIMARY KEY(bound_dataset_id, session_id, message_id)
);
CREATE TABLE IF NOT EXISTS session_drift_disposition (
 bound_dataset_id TEXT NOT NULL, session_id TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('dismissed')),
 target_project_id TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(bound_dataset_id, session_id)
);
`

type DriftHitDelta struct {
	TargetProjectID                     string
	ReadCount, WriteCount, CommandCount int
}

type SessionDriftHit struct {
	BoundDatasetID, SessionID, TargetProjectID string
	ReadCount, WriteCount, CommandCount        int
	UpdatedAt                                  string
}

type SessionDriftDisposition struct {
	BoundDatasetID, SessionID, Status, TargetProjectID, UpdatedAt string
}

func (s *Store) RecordSessionDriftHits(ctx context.Context, boundDatasetID, sessionID, messageID string, hits []DriftHitDelta) (bool, error) {
	if err := requireIDs(boundDatasetID, sessionID); err != nil {
		return false, err
	}
	hasHits := false
	for _, hit := range hits {
		if err := requireIDs(hit.TargetProjectID); err != nil {
			return false, err
		}
		if hit.ReadCount < 0 || hit.WriteCount < 0 || hit.CommandCount < 0 {
			return false, fmt.Errorf("negative drift count: %w", ErrInvalidArgument)
		}
		hasHits = hasHits || hit.ReadCount > 0 || hit.WriteCount > 0 || hit.CommandCount > 0
	}
	if !hasHits {
		return false, nil
	}
	recorded := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		if messageID = strings.TrimSpace(messageID); messageID != "" {
			res, err := tx.ExecContext(ctx, `INSERT INTO session_drift_seen VALUES(?,?,?,?)
				ON CONFLICT(bound_dataset_id,session_id,message_id) DO NOTHING`, boundDatasetID, sessionID, messageID, now)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil || n == 0 {
				return err
			}
		}
		for _, hit := range hits {
			if hit.ReadCount == 0 && hit.WriteCount == 0 && hit.CommandCount == 0 {
				continue
			}
			if err := execSQL(ctx, tx, `INSERT INTO session_project_hits VALUES(?,?,?,?,?,?,?)
				ON CONFLICT(bound_dataset_id,session_id,target_project_id) DO UPDATE SET
				read_count=read_count+excluded.read_count, write_count=write_count+excluded.write_count,
				command_count=command_count+excluded.command_count, updated_at=excluded.updated_at`,
				boundDatasetID, sessionID, hit.TargetProjectID, hit.ReadCount, hit.WriteCount, hit.CommandCount, now); err != nil {
				return err
			}
		}
		recorded = true
		return nil
	})
	return recorded && err == nil, err
}

func scanSessionDriftHit(row scanner) (SessionDriftHit, error) {
	var hit SessionDriftHit
	err := row.Scan(&hit.BoundDatasetID, &hit.SessionID, &hit.TargetProjectID,
		&hit.ReadCount, &hit.WriteCount, &hit.CommandCount, &hit.UpdatedAt)
	return hit, rowError(err)
}

func (s *Store) ListSessionDriftHits(ctx context.Context, boundDatasetID, sessionID string) ([]SessionDriftHit, error) {
	return queryList(ctx, s.db, scanSessionDriftHit, `SELECT bound_dataset_id,session_id,target_project_id,
		read_count,write_count,command_count,updated_at FROM session_project_hits
		WHERE bound_dataset_id=? AND session_id=? ORDER BY target_project_id`, boundDatasetID, sessionID)
}

func (s *Store) DismissSessionDrift(ctx context.Context, boundDatasetID, sessionID, targetProjectID string) error {
	sessionID, targetProjectID = strings.TrimSpace(sessionID), strings.TrimSpace(targetProjectID)
	if err := requireIDs(boundDatasetID, sessionID, targetProjectID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO session_drift_disposition VALUES(?,?,'dismissed',?,?)
		ON CONFLICT(bound_dataset_id,session_id) DO UPDATE SET
		target_project_id=excluded.target_project_id, updated_at=excluded.updated_at`,
		boundDatasetID, sessionID, targetProjectID, NowUTCMicro())
}

func (s *Store) GetSessionDriftDisposition(ctx context.Context, boundDatasetID, sessionID string) (SessionDriftDisposition, error) {
	var d SessionDriftDisposition
	err := s.db.QueryRowContext(ctx, `SELECT bound_dataset_id,session_id,status,target_project_id,updated_at
		FROM session_drift_disposition WHERE bound_dataset_id=? AND session_id=?`, boundDatasetID, sessionID).
		Scan(&d.BoundDatasetID, &d.SessionID, &d.Status, &d.TargetProjectID, &d.UpdatedAt)
	return d, rowError(err)
}

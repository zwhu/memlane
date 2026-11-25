package domain

import (
	"context"
	"fmt"
)

const profilesToolgraphDDL = `
CREATE TABLE IF NOT EXISTS user_profiles (
 instance_id TEXT PRIMARY KEY, kind TEXT NOT NULL DEFAULT '', operation_id TEXT NOT NULL DEFAULT '',
 method TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', dataset_id TEXT NOT NULL DEFAULT '',
 document_dataset_id TEXT NOT NULL DEFAULT '', document_data_id TEXT NOT NULL DEFAULT '',
 previous_dataset_id TEXT NOT NULL DEFAULT '', previous_data_id TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '',
 started_at REAL NOT NULL DEFAULT 0, updated_at REAL NOT NULL DEFAULT 0, progress_json TEXT NOT NULL DEFAULT '{}',
 extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS user_profile_attempts (
 operation_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
 method TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', dataset_id TEXT NOT NULL DEFAULT '',
 document_dataset_id TEXT NOT NULL DEFAULT '', document_data_id TEXT NOT NULL DEFAULT '',
 previous_dataset_id TEXT NOT NULL DEFAULT '', previous_data_id TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '', exit_code INTEGER, failure_reason TEXT NOT NULL DEFAULT '',
 started_at REAL NOT NULL DEFAULT 0, updated_at REAL NOT NULL DEFAULT 0, progress_json TEXT NOT NULL DEFAULT '{}',
 extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_user_profile_attempts_instance ON user_profile_attempts(instance_id,operation_id);
CREATE TABLE IF NOT EXISTS tool_graph_jobs (
 scope TEXT NOT NULL CHECK(scope IN ('control','service')), operation_id TEXT NOT NULL,
 dataset_id TEXT NOT NULL DEFAULT '', user_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '',
 signature TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '',
 started_at TEXT NOT NULL DEFAULT '', ended_at TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY(scope,operation_id)
);
CREATE TABLE IF NOT EXISTS tool_graph_watermarks (
 user_id TEXT NOT NULL, session_id TEXT NOT NULL, count INTEGER NOT NULL DEFAULT 0, min_seq INTEGER NOT NULL DEFAULT 0,
 max_seq INTEGER NOT NULL DEFAULT 0, persisted_max_seq INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(user_id,session_id)
);
`

type DocumentRef struct{ DatasetID, DataID string }

type UserProfile struct {
	InstanceID, Kind, OperationID, Method, Status, DatasetID string
	Document, Previous                                       DocumentRef
	ContentHash                                              string
	StartedAt, UpdatedAt                                     float64
	ProgressJSON, ExtraJSON                                  string
}

type UserProfileAttempt struct {
	OperationID, InstanceID, Kind, Method, Status, DatasetID string
	Document, Previous                                       DocumentRef
	ContentHash, ErrorCode                                   string
	ExitCode                                                 *int
	FailureReason                                            string
	StartedAt, UpdatedAt                                     float64
	ProgressJSON, ExtraJSON                                  string
}

const userProfileColumns = `instance_id, kind, operation_id, method, status, dataset_id, document_dataset_id,
		document_data_id, previous_dataset_id, previous_data_id, content_hash, started_at, updated_at, progress_json,
		extra_json`

func scanUserProfile(row scanner) (UserProfile, error) {
	var p UserProfile
	err := row.Scan(&p.InstanceID, &p.Kind, &p.OperationID, &p.Method, &p.Status, &p.DatasetID, &p.Document.DatasetID,
		&p.Document.DataID, &p.Previous.DatasetID, &p.Previous.DataID, &p.ContentHash, &p.StartedAt, &p.UpdatedAt, &p.ProgressJSON,
		&p.ExtraJSON)
	return p, rowError(err)
}

func (s *Store) UpsertUserProfile(ctx context.Context, p UserProfile) error {
	if err := requireIDs(p.InstanceID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO user_profiles (`+userProfileColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET
		kind=excluded.kind, operation_id=excluded.operation_id, method=excluded.method,
		status=excluded.status, dataset_id=excluded.dataset_id, document_dataset_id=excluded.document_dataset_id,
		document_data_id=excluded.document_data_id, previous_dataset_id=excluded.previous_dataset_id, previous_data_id=excluded.previous_data_id,
		content_hash=excluded.content_hash, started_at=excluded.started_at, updated_at=excluded.updated_at,
		progress_json=excluded.progress_json, extra_json=excluded.extra_json`,
		p.InstanceID, p.Kind, p.OperationID, p.Method, p.Status, p.DatasetID, p.Document.DatasetID,
		p.Document.DataID, p.Previous.DatasetID, p.Previous.DataID, p.ContentHash, p.StartedAt, p.UpdatedAt, defaultText(p.ProgressJSON, "{}"),
		defaultText(p.ExtraJSON, "{}"))
}

func (s *Store) GetUserProfile(ctx context.Context, instanceID string) (UserProfile, error) {
	return scanUserProfile(s.db.QueryRowContext(ctx, `SELECT `+userProfileColumns+` FROM user_profiles`+` WHERE instance_id=?`, instanceID))
}

func (s *Store) ListUserProfiles(ctx context.Context) ([]UserProfile, error) {
	return queryList(ctx, s.db, scanUserProfile, `SELECT `+userProfileColumns+` FROM user_profiles ORDER BY instance_id`)
}

func (s *Store) DeleteUserProfile(ctx context.Context, instanceID string) error {
	return requireAffected(s.db.ExecContext(ctx, `DELETE FROM user_profiles`+` WHERE instance_id=?`, instanceID))
}

const userProfileAttemptColumns = `operation_id, instance_id, kind, method, status, dataset_id, document_dataset_id,
		document_data_id, previous_dataset_id, previous_data_id, content_hash, error_code, exit_code, failure_reason,
		started_at, updated_at, progress_json, extra_json`

func scanUserProfileAttempt(row scanner) (UserProfileAttempt, error) {
	var p UserProfileAttempt
	err := row.Scan(&p.OperationID, &p.InstanceID, &p.Kind, &p.Method, &p.Status, &p.DatasetID, &p.Document.DatasetID,
		&p.Document.DataID, &p.Previous.DatasetID, &p.Previous.DataID, &p.ContentHash, &p.ErrorCode, &p.ExitCode, &p.FailureReason,
		&p.StartedAt, &p.UpdatedAt, &p.ProgressJSON, &p.ExtraJSON)
	return p, rowError(err)
}

func (s *Store) UpsertUserProfileAttempt(ctx context.Context, p UserProfileAttempt) error {
	if err := requireIDs(p.OperationID, p.InstanceID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO user_profile_attempts (`+userProfileAttemptColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO UPDATE SET
		instance_id=excluded.instance_id, kind=excluded.kind, method=excluded.method,
		status=excluded.status, dataset_id=excluded.dataset_id, document_dataset_id=excluded.document_dataset_id,
		document_data_id=excluded.document_data_id, previous_dataset_id=excluded.previous_dataset_id, previous_data_id=excluded.previous_data_id,
		content_hash=excluded.content_hash, error_code=excluded.error_code, exit_code=excluded.exit_code,
		failure_reason=excluded.failure_reason, started_at=excluded.started_at, updated_at=excluded.updated_at,
		progress_json=excluded.progress_json, extra_json=excluded.extra_json`,
		p.OperationID, p.InstanceID, p.Kind, p.Method, p.Status, p.DatasetID, p.Document.DatasetID,
		p.Document.DataID, p.Previous.DatasetID, p.Previous.DataID, p.ContentHash, p.ErrorCode, p.ExitCode, p.FailureReason,
		p.StartedAt, p.UpdatedAt, defaultText(p.ProgressJSON, "{}"), defaultText(p.ExtraJSON, "{}"))
}

func (s *Store) GetUserProfileAttempt(ctx context.Context, operationID string) (UserProfileAttempt, error) {
	return scanUserProfileAttempt(s.db.QueryRowContext(ctx, `SELECT `+userProfileAttemptColumns+` FROM user_profile_attempts`+` WHERE operation_id=?`, operationID))
}

func (s *Store) ListUserProfileAttempts(ctx context.Context, instanceID string) ([]UserProfileAttempt, error) {
	return queryList(ctx, s.db, scanUserProfileAttempt, `SELECT `+userProfileAttemptColumns+` FROM user_profile_attempts WHERE (?='' OR instance_id=?) ORDER BY operation_id`, instanceID, instanceID)
}

func (s *Store) DeleteUserProfileAttempt(ctx context.Context, operationID string) error {
	return requireAffected(s.db.ExecContext(ctx, `DELETE FROM user_profile_attempts`+` WHERE operation_id=?`, operationID))
}

type ToolGraphJob struct {
	Scope       string
	OperationID string
	DatasetID   string
	UserID      string
	Status      string
	Signature   string
	ErrorCode   string
	CreatedAt   string
	StartedAt   string
	EndedAt     string
	PayloadJSON string
}

const toolGraphJobColumns = `scope, operation_id, dataset_id, user_id, status, signature, error_code,
		created_at, started_at, ended_at, payload_json`

func scanToolGraphJob(row scanner) (ToolGraphJob, error) {
	var p ToolGraphJob
	err := row.Scan(&p.Scope, &p.OperationID, &p.DatasetID, &p.UserID, &p.Status, &p.Signature, &p.ErrorCode,
		&p.CreatedAt, &p.StartedAt, &p.EndedAt, &p.PayloadJSON)
	return p, rowError(err)
}

func (s *Store) UpsertToolGraphJob(ctx context.Context, p ToolGraphJob) error {
	if err := requireIDs(p.OperationID); err != nil {
		return err
	}
	if p.Scope != "control" && p.Scope != "service" {
		return fmt.Errorf("tool graph scope: %w", ErrInvalidArgument)
	}
	return execSQL(ctx, s.db, `INSERT INTO tool_graph_jobs (`+toolGraphJobColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(scope,operation_id) DO UPDATE SET
		dataset_id=excluded.dataset_id, user_id=excluded.user_id, status=excluded.status,
		signature=excluded.signature, error_code=excluded.error_code, created_at=excluded.created_at,
		started_at=excluded.started_at, ended_at=excluded.ended_at, payload_json=excluded.payload_json`,
		p.Scope, p.OperationID, p.DatasetID, p.UserID, p.Status, p.Signature, p.ErrorCode,
		p.CreatedAt, p.StartedAt, p.EndedAt, defaultText(p.PayloadJSON, "{}"))
}

func (s *Store) GetToolGraphJob(ctx context.Context, scope string, operationID string) (ToolGraphJob, error) {
	return scanToolGraphJob(s.db.QueryRowContext(ctx, `SELECT `+toolGraphJobColumns+` FROM tool_graph_jobs`+` WHERE scope=? AND operation_id=?`, scope, operationID))
}

func (s *Store) ListToolGraphJobs(ctx context.Context, scope string) ([]ToolGraphJob, error) {
	return queryList(ctx, s.db, scanToolGraphJob, `SELECT `+toolGraphJobColumns+` FROM tool_graph_jobs WHERE (?='' OR scope=?) ORDER BY scope,operation_id`, scope, scope)
}

func (s *Store) DeleteToolGraphJob(ctx context.Context, scope string, operationID string) error {
	return requireAffected(s.db.ExecContext(ctx, `DELETE FROM tool_graph_jobs`+` WHERE scope=? AND operation_id=?`, scope, operationID))
}

type ToolGraphWatermark struct {
	UserID          string
	SessionID       string
	Count           int
	MinSeq          int64
	MaxSeq          int64
	PersistedMaxSeq int64
	UpdatedAt       string
}

const toolGraphWatermarkColumns = `user_id, session_id, count, min_seq, max_seq, persisted_max_seq, updated_at`

func scanToolGraphWatermark(row scanner) (ToolGraphWatermark, error) {
	var p ToolGraphWatermark
	err := row.Scan(&p.UserID, &p.SessionID, &p.Count, &p.MinSeq, &p.MaxSeq, &p.PersistedMaxSeq, &p.UpdatedAt)
	return p, rowError(err)
}

func (s *Store) UpsertToolGraphWatermark(ctx context.Context, p ToolGraphWatermark) error {
	if err := requireIDs(p.UserID, p.SessionID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO tool_graph_watermarks (`+toolGraphWatermarkColumns+`)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,session_id) DO UPDATE SET
		count=excluded.count, min_seq=excluded.min_seq, max_seq=excluded.max_seq,
		persisted_max_seq=excluded.persisted_max_seq, updated_at=excluded.updated_at`,
		p.UserID, p.SessionID, p.Count, p.MinSeq, p.MaxSeq, p.PersistedMaxSeq, p.UpdatedAt)
}

func (s *Store) GetToolGraphWatermark(ctx context.Context, userID string, sessionID string) (ToolGraphWatermark, error) {
	return scanToolGraphWatermark(s.db.QueryRowContext(ctx, `SELECT `+toolGraphWatermarkColumns+` FROM tool_graph_watermarks`+` WHERE user_id=? AND session_id=?`, userID, sessionID))
}

func (s *Store) ListToolGraphWatermarks(ctx context.Context, userID string) ([]ToolGraphWatermark, error) {
	return queryList(ctx, s.db, scanToolGraphWatermark, `SELECT `+toolGraphWatermarkColumns+` FROM tool_graph_watermarks WHERE (?='' OR user_id=?) ORDER BY user_id,session_id`, userID, userID)
}

func (s *Store) DeleteToolGraphWatermark(ctx context.Context, userID string, sessionID string) error {
	return requireAffected(s.db.ExecContext(ctx, `DELETE FROM tool_graph_watermarks`+` WHERE user_id=? AND session_id=?`, userID, sessionID))
}

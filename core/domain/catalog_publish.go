package domain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type CatalogDocKey struct {
	DatasetID string
	DocID     string
}

type CatalogTitleHint struct {
	DocLabel        string
	ContentHash     string
	RawDataLocation string
}

type CatalogPublishDoc struct {
	CatalogDoc
	Provenance *DocumentProvenance
}

type CatalogPublishInput struct {
	DatasetID  string
	Exists     bool
	SnapshotAt string
	Docs       []CatalogPublishDoc
	Sessions   []CatalogSession
}

type CatalogPublishStats struct {
	Datasets             int
	DocsProjected        int
	KnowledgeCount       int
	CommonCount          int
	RawDocCount          int
	PendingRawCount      int
	SessionsProjected    int
	DistinctSessionUsers int
	ProvenanceLocked     int
}

type CatalogPublishState struct {
	DatasetID   string
	ProjectID   string
	Status      string
	LastError   string
	LastErrorAt string
	Attempts    int
	UpdatedAt   string
}

const catalogPublishStateColumns = `dataset_id, project_id, status, last_error, last_error_at, attempts, updated_at`

func scanCatalogPublishState(row scanner) (CatalogPublishState, error) {
	var st CatalogPublishState
	err := row.Scan(&st.DatasetID, &st.ProjectID, &st.Status, &st.LastError, &st.LastErrorAt, &st.Attempts, &st.UpdatedAt)
	return st, rowError(err)
}

func normalizedUUIDHex(raw string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
}

func isDatasetManagedTx(ctx context.Context, tx *sql.Tx, datasetID string) (bool, string, error) {
	norm := normalizedUUIDHex(datasetID)
	if norm == "" {
		return false, "", nil
	}
	var projectID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE LOWER(REPLACE(dataset_id,'-',''))=? OR LOWER(REPLACE(code_dataset_id,'-',''))=? ORDER BY id LIMIT 1`,
		norm, norm).Scan(&projectID)
	if err == nil {
		return true, projectID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	var commonExists int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM common_config WHERE LOWER(REPLACE(dataset_id,'-',''))=? LIMIT 1`,
		norm).Scan(&commonExists)
	if err == nil {
		return true, "", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return false, "", err
}

func deleteCatalogForDatasetTx(ctx context.Context, tx *sql.Tx, datasetID string) error {
	norm := normalizedUUIDHex(datasetID)
	if norm == "" {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM catalog_docs WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
		`DELETE FROM catalog_sessions WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
		`DELETE FROM catalog_sync_state WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
		`DELETE FROM catalog_publish_state WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
		`DELETE FROM catalog_doc_redirects WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
	} {
		if err := execSQL(ctx, tx, stmt, norm); err != nil {
			return err
		}
	}
	return nil
}

func upsertCatalogPublishStateTx(ctx context.Context, tx *sql.Tx, datasetID, projectID, status, lastError string) error {
	datasetID = catalogUUIDText(datasetID)
	if err := requireIDs(datasetID); err != nil {
		return err
	}
	norm := normalizedUUIDHex(datasetID)
	if err := execSQL(ctx, tx,
		`DELETE FROM catalog_publish_state WHERE LOWER(REPLACE(dataset_id,'-',''))=? AND dataset_id<>?`,
		norm, datasetID); err != nil {
		return err
	}
	now := NowUTCMicro()
	switch status {
	case "ok":
		return execSQL(ctx, tx, `INSERT INTO catalog_publish_state(`+catalogPublishStateColumns+`)
			VALUES(?,?,'ok','','',0,?)
			ON CONFLICT(dataset_id) DO UPDATE SET
				project_id=CASE WHEN excluded.project_id<>'' THEN excluded.project_id ELSE catalog_publish_state.project_id END,
				status='ok',
				last_error='',
				last_error_at='',
				updated_at=excluded.updated_at`,
			datasetID, projectID, now)
	case "pending":
		return execSQL(ctx, tx, `INSERT INTO catalog_publish_state(`+catalogPublishStateColumns+`)
			VALUES(?,?,'pending','','',0,?)
			ON CONFLICT(dataset_id) DO UPDATE SET
				project_id=CASE WHEN excluded.project_id<>'' THEN excluded.project_id ELSE catalog_publish_state.project_id END,
				status='pending',
				updated_at=excluded.updated_at`,
			datasetID, projectID, now)
	case "failed":
		return execSQL(ctx, tx, `INSERT INTO catalog_publish_state(`+catalogPublishStateColumns+`)
			VALUES(?,?,'failed',?,?,1,?)
			ON CONFLICT(dataset_id) DO UPDATE SET
				project_id=CASE WHEN excluded.project_id<>'' THEN excluded.project_id ELSE catalog_publish_state.project_id END,
				status='failed',
				last_error=excluded.last_error,
				last_error_at=excluded.last_error_at,
				attempts=catalog_publish_state.attempts+1,
				updated_at=excluded.updated_at`,
			datasetID, projectID, lastError, now, now)
	default:
		return fmt.Errorf("invalid catalog publish status %q: %w", status, ErrInvalidArgument)
	}
}

// markCatalogPublishOKAfterSnapshotTx marks a dataset's publish state 'ok' unless a newer
// 'pending' request arrived after snapshotAt (preventing an older snapshot from clobbering
// a fresh pending publish request).
func markCatalogPublishOKAfterSnapshotTx(ctx context.Context, tx *sql.Tx, datasetID, projectID, snapshotAt string) error {
	datasetID = catalogUUIDText(datasetID)
	norm := normalizedUUIDHex(datasetID)
	if norm == "" {
		return nil
	}
	if strings.TrimSpace(snapshotAt) != "" {
		var newerPending int
		err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM catalog_publish_state WHERE LOWER(REPLACE(dataset_id,'-',''))=? AND status='pending' AND updated_at>?)`,
			norm, snapshotAt).Scan(&newerPending)
		if err != nil {
			return err
		}
		if newerPending != 0 {
			// Normalize any non-canonical pending row without overwriting status='pending'.
			if err := execSQL(ctx, tx,
				`DELETE FROM catalog_publish_state WHERE LOWER(REPLACE(dataset_id,'-',''))=? AND dataset_id<>?`,
				norm, datasetID); err != nil {
				return err
			}
			return nil
		}
	}
	return upsertCatalogPublishStateTx(ctx, tx, datasetID, projectID, "ok", "")
}

func (s *Store) GetCatalogPublishState(ctx context.Context, datasetID string) (CatalogPublishState, error) {
	datasetID = catalogUUIDText(datasetID)
	return scanCatalogPublishState(s.db.QueryRowContext(ctx,
		`SELECT `+catalogPublishStateColumns+` FROM catalog_publish_state WHERE dataset_id=?`, datasetID))
}

func (s *Store) ListFailedCatalogPublishStates(ctx context.Context, projectID string) ([]CatalogPublishState, error) {
	if strings.TrimSpace(projectID) != "" {
		return queryList(ctx, s.db, scanCatalogPublishState,
			`SELECT `+catalogPublishStateColumns+` FROM catalog_publish_state
			 WHERE status IN ('failed','pending') AND (project_id=? OR LOWER(REPLACE(dataset_id,'-','')) IN (SELECT LOWER(REPLACE(dataset_id,'-','')) FROM projects WHERE id=? AND dataset_id<>''))
			 ORDER BY updated_at, dataset_id`, projectID, projectID)
	}
	return queryList(ctx, s.db, scanCatalogPublishState,
		`SELECT `+catalogPublishStateColumns+` FROM catalog_publish_state
		 WHERE status IN ('failed','pending') AND LOWER(REPLACE(dataset_id,'-','')) IN (SELECT LOWER(REPLACE(dataset_id,'-','')) FROM projects WHERE dataset_id<>'')
		 ORDER BY updated_at, dataset_id`)
}

func (s *Store) NextPendingCatalogPublishExcludingDatasets(ctx context.Context, excluded []string) (CatalogPublishState, error) {
	query := `SELECT ` + catalogPublishStateColumns + ` FROM catalog_publish_state
		WHERE status='pending' AND (
			LOWER(REPLACE(dataset_id,'-','')) IN (SELECT LOWER(REPLACE(dataset_id,'-','')) FROM projects WHERE dataset_id<>'')
			OR LOWER(REPLACE(dataset_id,'-','')) IN (SELECT LOWER(REPLACE(code_dataset_id,'-','')) FROM projects WHERE code_dataset_id<>'')
			OR LOWER(REPLACE(dataset_id,'-','')) IN (SELECT LOWER(REPLACE(dataset_id,'-','')) FROM common_config WHERE dataset_id<>'')
		)`
	var args []any
	if len(excluded) > 0 {
		placeholders := strings.Repeat("?,", len(excluded)-1) + "?"
		query += ` AND LOWER(REPLACE(dataset_id,'-','')) NOT IN (` + placeholders + `)`
		for _, ds := range excluded {
			args = append(args, normalizedUUIDHex(ds))
		}
	}
	query += ` ORDER BY updated_at, dataset_id LIMIT 1`
	return scanCatalogPublishState(s.db.QueryRowContext(ctx, query, args...))
}

func (s *Store) MarkCatalogPublishFailed(ctx context.Context, datasetID, projectID, lastError string) error {
	if err := requireIDs(datasetID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, resolvedProject, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		if projectID == "" {
			projectID = resolvedProject
		}
		return upsertCatalogPublishStateTx(ctx, tx, datasetID, projectID, "failed", lastError)
	})
}

func (s *Store) MarkCatalogPublishPending(ctx context.Context, datasetID, projectID string) error {
	if err := requireIDs(datasetID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, resolvedProject, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		if projectID == "" {
			projectID = resolvedProject
		}
		return upsertCatalogPublishStateTx(ctx, tx, datasetID, projectID, "pending", "")
	})
}

func (s *Store) MarkCatalogPublishOK(ctx context.Context, datasetID, projectID string) error {
	return s.MarkCatalogPublishOKAfterSnapshot(ctx, datasetID, projectID, "")
}

func (s *Store) MarkCatalogPublishOKAfterSnapshot(ctx context.Context, datasetID, projectID, snapshotAt string) error {
	datasetID = catalogUUIDText(datasetID)
	if err := requireIDs(datasetID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, resolvedProject, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		if projectID == "" {
			projectID = resolvedProject
		}
		return markCatalogPublishOKAfterSnapshotTx(ctx, tx, datasetID, projectID, snapshotAt)
	})
}

// CompleteInboxWithCatalogPublishState atomically marks a capture_inbox item succeeded and
// records the independent catalog_publish_state ('pending' or 'failed') in a single transaction,
// preventing duplicate LLM extraction on catalog publish failure or shutdown cancellation.
func (s *Store) CompleteInboxWithCatalogPublishState(ctx context.Context, inboxID, datasetID, projectID, status, lastError string) error {
	if err := requireIDs(inboxID, datasetID); err != nil {
		return err
	}
	if status != "pending" && status != "failed" {
		return fmt.Errorf("catalog publish state must be pending or failed: %w", ErrInvalidArgument)
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		if err := requireAffected(tx.ExecContext(ctx,
			`UPDATE capture_inbox SET status='succeeded',last_error='',last_error_at='',updated_at=? WHERE id=?`,
			now, inboxID)); err != nil {
			return err
		}
		managed, resolvedProject, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		if projectID == "" {
			projectID = resolvedProject
		}
		return upsertCatalogPublishStateTx(ctx, tx, datasetID, projectID, status, lastError)
	})
}

// CompleteSessionFlushWithCatalogPublishState atomically completes a session_flush_job
// (respecting rerun_requested) and records the independent catalog_publish_state in one transaction.
func (s *Store) CompleteSessionFlushWithCatalogPublishState(ctx context.Context, jobID, datasetID, projectID, status, lastError string) (bool, error) {
	if err := requireIDs(jobID, datasetID); err != nil {
		return false, err
	}
	if status != "pending" && status != "failed" {
		return false, fmt.Errorf("catalog publish state must be pending or failed: %w", ErrInvalidArgument)
	}
	requeued := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var rerun int
		if err := tx.QueryRowContext(ctx, `SELECT rerun_requested FROM session_flush_jobs WHERE id=?`, jobID).Scan(&rerun); err != nil {
			return rowError(err)
		}
		now := NowUTCMicro()
		if rerun != 0 {
			if err := execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='pending',last_error='',last_error_at='',rerun_requested=0,updated_at=? WHERE id=?`,
				now, jobID); err != nil {
				return err
			}
			requeued = true
		} else {
			if err := execSQL(ctx, tx,
				`UPDATE session_flush_jobs SET status='succeeded',last_error='',last_error_at='',updated_at=? WHERE id=?`,
				now, jobID); err != nil {
				return err
			}
		}
		managed, resolvedProject, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		if projectID == "" {
			projectID = resolvedProject
		}
		return upsertCatalogPublishStateTx(ctx, tx, datasetID, projectID, status, lastError)
	})
	return requeued, err
}

func (s *Store) DeleteCatalogForDataset(ctx context.Context, datasetID string) error {
	if err := requireIDs(datasetID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, _, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if managed {
			return nil
		}
		return deleteCatalogForDatasetTx(ctx, tx, datasetID)
	})
}

func (s *Store) ListCatalogTitleHints(ctx context.Context, datasetIDs []string) (map[CatalogDocKey]CatalogTitleHint, error) {
	hints := make(map[CatalogDocKey]CatalogTitleHint)
	var rows *sql.Rows
	var err error
	if len(datasetIDs) == 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT dataset_id,doc_id,doc_label,content_hash,raw_data_location
			FROM catalog_docs WHERE is_knowledge=1 AND doc_label<>''`)
	} else {
		args := make([]any, len(datasetIDs))
		for i, id := range datasetIDs {
			args[i] = id
		}
		placeholders := strings.Repeat("?,", len(datasetIDs)-1) + "?"
		rows, err = s.db.QueryContext(ctx, `SELECT dataset_id,doc_id,doc_label,content_hash,raw_data_location
			FROM catalog_docs WHERE is_knowledge=1 AND doc_label<>'' AND dataset_id IN (`+placeholders+`)`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key CatalogDocKey
		var hint CatalogTitleHint
		if err := rows.Scan(&key.DatasetID, &key.DocID, &hint.DocLabel, &hint.ContentHash, &hint.RawDataLocation); err != nil {
			return nil, err
		}
		hints[key] = hint
	}
	return hints, rows.Err()
}

func (s *Store) PublishDatasetCatalog(ctx context.Context, input CatalogPublishInput) (*CatalogPublishStats, error) {
	datasetID := strings.TrimSpace(input.DatasetID)
	if datasetID == "" {
		return nil, fmt.Errorf("catalog dataset id: %w", ErrInvalidArgument)
	}
	input.DatasetID = datasetID
	stats := &CatalogPublishStats{}
	users := map[string]struct{}{}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, projectID, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		var maxSeq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(doc_seq),0) FROM catalog_docs`).Scan(&maxSeq); err != nil {
			return err
		}
		maxSeq, err = publishSingleDatasetTx(ctx, tx, input, maxSeq, stats, users)
		_ = maxSeq
		if err != nil {
			return err
		}
		return markCatalogPublishOKAfterSnapshotTx(ctx, tx, datasetID, projectID, input.SnapshotAt)
	})
	if err != nil {
		return nil, err
	}
	stats.DistinctSessionUsers = len(users)
	return stats, nil
}

func (s *Store) PublishFullCatalog(ctx context.Context, inputs []CatalogPublishInput) (*CatalogPublishStats, error) {
	stats := &CatalogPublishStats{}
	users := map[string]struct{}{}
	keep := make(map[string]bool, len(inputs))
	for i := range inputs {
		inputs[i].DatasetID = strings.TrimSpace(inputs[i].DatasetID)
		if inputs[i].DatasetID != "" {
			keep[inputs[i].DatasetID] = true
		}
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var maxSeq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(doc_seq),0) FROM catalog_docs`).Scan(&maxSeq); err != nil {
			return err
		}
		for _, input := range inputs {
			if input.DatasetID == "" {
				continue
			}
			managed, projectID, err := isDatasetManagedTx(ctx, tx, input.DatasetID)
			if err != nil {
				return err
			}
			if !managed {
				delete(keep, input.DatasetID)
				if err := deleteCatalogForDatasetTx(ctx, tx, input.DatasetID); err != nil {
					return err
				}
				continue
			}
			maxSeq, err = publishSingleDatasetTx(ctx, tx, input, maxSeq, stats, users)
			if err != nil {
				return err
			}
			if err := markCatalogPublishOKAfterSnapshotTx(ctx, tx, input.DatasetID, projectID, input.SnapshotAt); err != nil {
				return err
			}
		}
		// Clean up orphan datasets that are no longer managed.
		existingDatasets, err := listCatalogDatasetIDsTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, ds := range existingDatasets {
			if keep[ds] {
				continue
			}
			if err := deleteCatalogForDatasetTx(ctx, tx, ds); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	stats.DistinctSessionUsers = len(users)
	return stats, nil
}

func (s *Store) PublishDatasetSessions(ctx context.Context, datasetID string, sessions []CatalogSession) (*CatalogPublishStats, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, fmt.Errorf("catalog dataset id: %w", ErrInvalidArgument)
	}
	stats := &CatalogPublishStats{}
	users := map[string]struct{}{}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		managed, _, err := isDatasetManagedTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		if !managed {
			return deleteCatalogForDatasetTx(ctx, tx, datasetID)
		}
		cloned := slices.Clone(sessions)
		for i := range cloned {
			cloned[i].DatasetID = datasetID
		}
		relations, err := listSessionRelationsTx(ctx, tx, datasetID)
		if err != nil {
			return err
		}
		applySessionRelations(cloned, relations)
		if err := enrichSessionAgentsTx(ctx, tx, datasetID, cloned); err != nil {
			return err
		}

		if err := execSQL(ctx, tx, `DELETE FROM catalog_sessions WHERE dataset_id=?`, datasetID); err != nil {
			return err
		}
		if err := insertCatalogSessionsTx(ctx, tx, cloned); err != nil {
			return err
		}
		for _, sess := range cloned {
			if sess.UserID != "" {
				users[sess.UserID] = struct{}{}
			}
		}
		stats.SessionsProjected = len(cloned)
		stats.Datasets = 1

		sessionSig := computeSessionsSig(cloned)
		prev, err := scanCatalogSyncState(tx.QueryRowContext(ctx,
			`SELECT `+catalogSyncColumns+` FROM catalog_sync_state WHERE dataset_id=?`, datasetID))
		hasPrev := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		state := prev
		if !hasPrev {
			state = CatalogSyncState{
				DatasetID:  datasetID,
				DatasetHex: catalogUUIDHex(datasetID),
			}
		}
		changed := !hasPrev || prev.SessionCount != len(cloned) || prev.SessionSig != sessionSig
		state.SessionCount = len(cloned)
		state.SessionSig = sessionSig
		state.CatalogVersion = nextCatalogVersion(prev.CatalogVersion, hasPrev, changed)
		state.SyncedAt = NowUTCNS()
		return upsertCatalogSyncStateTx(ctx, tx, state)
	})
	if err != nil {
		return nil, err
	}
	stats.DistinctSessionUsers = len(users)
	return stats, nil
}

type existingCatalogDocInfo struct {
	DocID            string
	DocSeq           int64
	LessonType       string
	StateKey         string
	CreatedAtNS      string
	CreatedAtInvalid bool
}

type keyedLessonKey struct {
	lessonType string
	stateKey   string
}

func recordDocRedirectTx(ctx context.Context, tx *sql.Tx, datasetID, oldDocID, newDocID string) error {
	if datasetID == "" || oldDocID == "" || newDocID == "" || oldDocID == newDocID {
		return nil
	}
	now := NowUTCMicro()
	if err := execSQL(ctx, tx,
		`INSERT INTO catalog_doc_redirects(dataset_id,old_doc_id,new_doc_id,updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(dataset_id,old_doc_id) DO UPDATE SET new_doc_id=excluded.new_doc_id,updated_at=excluded.updated_at`,
		datasetID, oldDocID, newDocID, now); err != nil {
		return err
	}
	// Flatten chained redirects (A -> B -> C becomes A -> C).
	return execSQL(ctx, tx,
		`UPDATE catalog_doc_redirects SET new_doc_id=?,updated_at=? WHERE dataset_id=? AND new_doc_id=?`,
		newDocID, now, datasetID, oldDocID)
}

func resolveDocRedirectTx(ctx context.Context, tx *sql.Tx, datasetID, docID string) (string, string, error) {
	normDS := normalizedUUIDHex(datasetID)
	normDoc := normalizedUUIDHex(docID)
	if normDS == "" || normDoc == "" {
		return datasetID, docID, nil
	}
	var canonDS, newDoc string
	err := tx.QueryRowContext(ctx,
		`SELECT dataset_id,new_doc_id FROM catalog_doc_redirects
		 WHERE LOWER(REPLACE(dataset_id,'-',''))=? AND LOWER(REPLACE(old_doc_id,'-',''))=? LIMIT 1`,
		normDS, normDoc).Scan(&canonDS, &newDoc)
	if errors.Is(err, sql.ErrNoRows) {
		return datasetID, docID, nil
	}
	if err != nil {
		return datasetID, docID, err
	}
	return canonDS, newDoc, nil
}

func bumpCatalogVersionTx(ctx context.Context, tx *sql.Tx, datasetID string) error {
	var prevVersion string
	err := tx.QueryRowContext(ctx, `SELECT catalog_version FROM catalog_sync_state WHERE dataset_id=?`, datasetID).Scan(&prevVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(prevVersion)
	if trimmed != "" {
		if _, parseErr := strconv.ParseInt(trimmed, 10, 64); parseErr != nil {
			// Preserve explicit non-numeric sentinel versions set outside catalog publishing.
			return nil
		}
	}
	next := nextCatalogVersion(prevVersion, true, true)
	return execSQL(ctx, tx, `UPDATE catalog_sync_state SET catalog_version=?,synced_at=? WHERE dataset_id=?`,
		next, NowUTCNS(), datasetID)
}

func publishSingleDatasetTx(ctx context.Context, tx *sql.Tx, input CatalogPublishInput, maxSeq int64, stats *CatalogPublishStats, users map[string]struct{}) (int64, error) {
	datasetID := input.DatasetID
	if !input.Exists && len(input.Docs) == 0 && len(input.Sessions) == 0 {
		for _, stmt := range []string{
			`DELETE FROM catalog_docs WHERE dataset_id=?`,
			`DELETE FROM catalog_sessions WHERE dataset_id=?`,
			`DELETE FROM catalog_sync_state WHERE dataset_id=?`,
		} {
			if err := execSQL(ctx, tx, stmt, datasetID); err != nil {
				return maxSeq, err
			}
		}
		return maxSeq, nil
	}

	existingDocs, err := loadExistingDatasetDocsTx(ctx, tx, datasetID)
	if err != nil {
		return maxSeq, err
	}
	prevState, err := scanCatalogSyncState(tx.QueryRowContext(ctx,
		`SELECT `+catalogSyncColumns+` FROM catalog_sync_state WHERE dataset_id=?`, datasetID))
	hasPrev := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return maxSeq, err
	}

	docs := slices.Clone(input.Docs)
	for i := range docs {
		docs[i].DatasetID = datasetID
	}
	sessions := slices.Clone(input.Sessions)
	for i := range sessions {
		sessions[i].DatasetID = datasetID
	}

	relations, err := listSessionRelationsTx(ctx, tx, datasetID)
	if err != nil {
		return maxSeq, err
	}
	applySessionRelations(sessions, relations)
	if err := enrichSessionAgentsTx(ctx, tx, datasetID, sessions); err != nil {
		return maxSeq, err
	}

	// Gate 1-1: detect auto-replaced state/pitfall keyed docs (old disappeared, new appeared with same key).
	newDocIDs := make(map[string]bool, len(docs))
	for _, d := range docs {
		newDocIDs[d.DocID] = true
	}
	disappearedByKey := make(map[keyedLessonKey][]existingCatalogDocInfo)
	for _, old := range existingDocs {
		if !newDocIDs[old.DocID] && IsAutoReplaceableLessonType(old.LessonType) && old.StateKey != "" {
			k := keyedLessonKey{old.LessonType, old.StateKey}
			disappearedByKey[k] = append(disappearedByKey[k], old)
		}
	}
	if len(disappearedByKey) > 0 {
		migratedKeys := make(map[keyedLessonKey]bool, len(disappearedByKey))
		for _, d := range docs {
			if _, existed := existingDocs[d.DocID]; existed {
				continue
			}
			if !IsAutoReplaceableLessonType(d.LessonType) || d.StateKey == "" {
				continue
			}
			k := keyedLessonKey{d.LessonType, d.StateKey}
			oldList := disappearedByKey[k]
			if len(oldList) == 0 || migratedKeys[k] {
				continue
			}
			migratedKeys[k] = true
			for _, old := range oldList {
				if err := recordDocRedirectTx(ctx, tx, datasetID, old.DocID, d.DocID); err != nil {
					return maxSeq, err
				}
				if err := migrateRecallDocCountTx(ctx, tx, datasetID, old.DocID, d.DocID); err != nil {
					return maxSeq, err
				}
				if err := migrateReadStateTx(ctx, tx, datasetID, old.DocID, d.DocID, old.CreatedAtNS, old.CreatedAtInvalid, d.CreatedAtNS, d.CreatedAtInvalid); err != nil {
					return maxSeq, err
				}
			}
		}
	}

	// Lock provenance inside the same transaction so replacement dates and keyed_provenance stay atomic.
	for i := range docs {
		d := &docs[i]
		if d.Provenance == nil {
			continue
		}
		prov := *d.Provenance
		prov.DatasetID = datasetID
		prov.DocumentID = d.DocID
		locked, changed, err := lockDocumentProvenanceTx(ctx, tx, prov)
		if err != nil {
			return maxSeq, err
		}
		if changed {
			stats.ProvenanceLocked++
		}
		d.Provenance = &locked
		day := locked.FirstSourceDate
		if !IsAutoReplaceableLessonType(locked.LessonType) && locked.LessonType != "" && locked.StateKey != "" {
			keyed, err := getKeyedProvenance(ctx, tx, datasetID, locked.LessonType, locked.StateKey)
			if err == nil && keyed.FirstSourceDate != "" && (day == "" || keyed.FirstSourceDate < day) {
				day = keyed.FirstSourceDate
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return maxSeq, err
			}
		}
		if day != "" {
			d.EffectiveDate = day
			d.EffectiveDateSource = "provenance"
			d.DayDescKey = catalogDayToDescKey(day)
		}
	}

	// Read recall counts after migration so new docs immediately reflect migrated counts.
	counts, err := loadDatasetRecallCountsTx(ctx, tx, datasetID)
	if err != nil {
		return maxSeq, err
	}

	if err := execSQL(ctx, tx, `DELETE FROM catalog_docs WHERE dataset_id=?`, datasetID); err != nil {
		return maxSeq, err
	}
	if err := execSQL(ctx, tx, `DELETE FROM catalog_sessions WHERE dataset_id=?`, datasetID); err != nil {
		return maxSeq, err
	}

	docStmt, err := tx.PrepareContext(ctx, `INSERT INTO catalog_docs(dataset_id,doc_id,cognee_rowid,kind,is_knowledge,
		common_category,lesson_type,state_key,category,session_id,doc_name,doc_label,raw_data_location,content_hash,
		created_at_raw,created_at_ns,created_at_invalid,updated_at_ns,effective_date,effective_date_source,day_desc_key,
		sort_ts_desc_key,recall_count,recall_desc_key,pipeline_status,source_updated_at,doc_seq)
		VALUES(?,?,0,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return maxSeq, err
	}
	defer docStmt.Close()

	state := CatalogSyncState{
		DatasetID:    datasetID,
		DatasetHex:   catalogUUIDHex(datasetID),
		DocCount:     len(docs),
		SessionCount: len(sessions),
		SyncedAt:     NowUTCNS(),
	}
	for i := range docs {
		d := &docs[i]
		if prev, ok := existingDocs[d.DocID]; ok && prev.DocSeq > 0 {
			d.DocSeq = prev.DocSeq
		} else {
			maxSeq++
			d.DocSeq = maxSeq
		}
		d.RecallCount = counts[normalizedUUIDHex(d.DocID)]
		d.RecallDescKey = -int64(d.RecallCount)
		state.MaxUpdatedAt = max(state.MaxUpdatedAt, d.SourceUpdatedAt)
		switch d.Kind {
		case "knowledge":
			state.KnowledgeCount++
			stats.KnowledgeCount++
		case "common":
			state.CommonCount++
			stats.CommonCount++
		case "raw_doc":
			state.RawDocCount++
			stats.RawDocCount++
			if d.PipelineStatus != "DATA_ITEM_PROCESSING_COMPLETED" && d.PipelineStatus != "not_applicable" {
				stats.PendingRawCount++
			}
		}
		if _, err := docStmt.ExecContext(ctx, d.DatasetID, d.DocID, d.Kind, boolToInt(d.IsKnowledge), d.CommonCategory,
			d.LessonType, d.StateKey, d.Category, d.SessionID, d.DocName, d.DocLabel, d.RawDataLocation, d.ContentHash,
			d.CreatedAtRaw, d.CreatedAtNS, boolToInt(d.CreatedAtInvalid), d.UpdatedAtNS, d.EffectiveDate, d.EffectiveDateSource,
			d.DayDescKey, d.SortTSDescKey, d.RecallCount, d.RecallDescKey, d.PipelineStatus, d.SourceUpdatedAt, d.DocSeq); err != nil {
			return maxSeq, err
		}
	}

	if err := insertCatalogSessionsTx(ctx, tx, sessions); err != nil {
		return maxSeq, err
	}
	for _, sess := range sessions {
		if sess.UserID != "" {
			users[sess.UserID] = struct{}{}
		}
	}

	state.DataHash = computeDocsDataHash(docs)
	state.SessionSig = computeSessionsSig(sessions)
	changed := !hasPrev ||
		prevState.DataHash != state.DataHash ||
		prevState.SessionSig != state.SessionSig ||
		prevState.DocCount != state.DocCount ||
		prevState.KnowledgeCount != state.KnowledgeCount ||
		prevState.CommonCount != state.CommonCount ||
		prevState.RawDocCount != state.RawDocCount ||
		prevState.SessionCount != state.SessionCount
	state.CatalogVersion = nextCatalogVersion(prevState.CatalogVersion, hasPrev, changed)

	if err := upsertCatalogSyncStateTx(ctx, tx, state); err != nil {
		return maxSeq, err
	}

	stats.Datasets++
	stats.DocsProjected += len(docs)
	stats.SessionsProjected += len(sessions)
	return maxSeq, nil
}

func loadExistingDatasetDocsTx(ctx context.Context, tx *sql.Tx, datasetID string) (map[string]existingCatalogDocInfo, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT doc_id,doc_seq,lesson_type,state_key,created_at_ns,created_at_invalid FROM catalog_docs WHERE dataset_id=?`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]existingCatalogDocInfo)
	for rows.Next() {
		var info existingCatalogDocInfo
		if err := rows.Scan(&info.DocID, &info.DocSeq, &info.LessonType, &info.StateKey, &info.CreatedAtNS, &info.CreatedAtInvalid); err != nil {
			return nil, err
		}
		out[info.DocID] = info
	}
	return out, rows.Err()
}

func loadDatasetRecallCountsTx(ctx context.Context, tx *sql.Tx, datasetID string) (map[string]int, error) {
	counts := make(map[string]int)
	rows, err := tx.QueryContext(ctx,
		`SELECT document_id,total_hits FROM recall_doc_counts WHERE LOWER(REPLACE(dataset_id,'-',''))=?`,
		normalizedUUIDHex(datasetID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var docID string
		var hits int
		if err := rows.Scan(&docID, &hits); err != nil {
			return nil, err
		}
		counts[normalizedUUIDHex(docID)] += hits
	}
	return counts, rows.Err()
}

func listSessionRelationsTx(ctx context.Context, tx *sql.Tx, datasetID string) ([]SessionRelation, error) {
	return queryList(ctx, tx, scanSessionRelation,
		`SELECT `+sessionRelationColumns+` FROM session_relations WHERE dataset_id=? ORDER BY session_id`, datasetID)
}

// enrichSessionAgentsTx fills empty CatalogSession.AgentID values in bulk during
// catalog sync using capture_inbox (latest non-empty agent_id per dataset+session)
// and unambiguous agents.principal_id matches for session user_id.
func enrichSessionAgentsTx(ctx context.Context, tx *sql.Tx, datasetID string, sessions []CatalogSession) error {
	if len(sessions) == 0 {
		return nil
	}
	missingSeen := make(map[string]struct{})
	missingSessions := make([]string, 0)
	for i := range sessions {
		sessions[i].AgentID = strings.TrimSpace(sessions[i].AgentID)
		if sessions[i].AgentID != "" {
			continue
		}
		sid := strings.TrimSpace(sessions[i].SessionID)
		if sid == "" {
			continue
		}
		if _, exists := missingSeen[sid]; !exists {
			missingSeen[sid] = struct{}{}
			missingSessions = append(missingSessions, sid)
		}
	}
	if len(missingSessions) == 0 {
		return nil
	}

	raw, err := json.Marshal(missingSessions)
	if err != nil {
		return err
	}
	hexDS := catalogUUIDHex(datasetID)
	var query string
	var args []any
	if hexDS != "" && hexDS != datasetID {
		query = `SELECT ref.value, COALESCE(
			(SELECT i.agent_id FROM capture_inbox i
			 WHERE i.dataset_id=?1 AND i.session_id=ref.value AND i.agent_id<>''
			 ORDER BY i.updated_at DESC LIMIT 1),
			(SELECT i.agent_id FROM capture_inbox i
			 WHERE i.dataset_id=?2 AND i.session_id=ref.value AND i.agent_id<>''
			 ORDER BY i.updated_at DESC LIMIT 1),
			''
		) FROM json_each(?3) ref`
		args = []any{datasetID, hexDS, string(raw)}
	} else {
		query = `SELECT ref.value, COALESCE(
			(SELECT i.agent_id FROM capture_inbox i
			 WHERE i.dataset_id=?1 AND i.session_id=ref.value AND i.agent_id<>''
			 ORDER BY i.updated_at DESC LIMIT 1),
			''
		) FROM json_each(?2) ref`
		args = []any{datasetID, string(raw)}
	}

	inboxAgents := make(map[string]string, len(missingSessions))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var sid, agentID string
		if err := rows.Scan(&sid, &agentID); err != nil {
			_ = rows.Close()
			return err
		}
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			inboxAgents[sid] = agentID
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	needPrincipalLookup := false
	for i := range sessions {
		if sessions[i].AgentID != "" {
			continue
		}
		if agentID := inboxAgents[strings.TrimSpace(sessions[i].SessionID)]; agentID != "" {
			sessions[i].AgentID = agentID
			continue
		}
		if strings.TrimSpace(sessions[i].UserID) != "" {
			needPrincipalLookup = true
		}
	}
	if !needPrincipalLookup {
		return nil
	}

	principalAgents, err := loadUniquePrincipalAgentsTx(ctx, tx)
	if err != nil {
		return err
	}
	if len(principalAgents) == 0 {
		return nil
	}
	for i := range sessions {
		if sessions[i].AgentID != "" {
			continue
		}
		uid := catalogUUIDText(sessions[i].UserID)
		if uid == "" {
			continue
		}
		if agentID := principalAgents[uid]; agentID != "" {
			sessions[i].AgentID = agentID
		}
	}
	return nil
}

func loadUniquePrincipalAgentsTx(ctx context.Context, db sqlReader) (map[string]string, error) {
	type bucket struct {
		active   []string
		inactive []string
	}
	byPrincipal := make(map[string]*bucket)
	rows, err := db.QueryContext(ctx, `SELECT id, principal_id, archived, retired_at FROM agents WHERE principal_id<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, principalID, retiredAt string
		var archived int
		if err := rows.Scan(&id, &principalID, &archived, &retiredAt); err != nil {
			return nil, err
		}
		id = strings.TrimSpace(id)
		key := catalogUUIDText(principalID)
		if id == "" || key == "" {
			continue
		}
		b := byPrincipal[key]
		if b == nil {
			b = &bucket{}
			byPrincipal[key] = b
		}
		if archived == 0 && strings.TrimSpace(retiredAt) == "" {
			b.active = append(b.active, id)
		} else {
			b.inactive = append(b.inactive, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(byPrincipal))
	for key, b := range byPrincipal {
		if len(b.active) == 1 {
			out[key] = b.active[0]
		} else if len(b.active) == 0 && len(b.inactive) == 1 {
			out[key] = b.inactive[0]
		}
	}
	return out, nil
}

func insertCatalogSessionsTx(ctx context.Context, tx *sql.Tx, sessions []CatalogSession) error {
	if len(sessions) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO catalog_sessions(dataset_id,user_id,session_id,agent_id,status,
		started_at,last_activity_at,sort_ts_desc_key,parent_session_id,root_session_id,subagent_role,nesting_depth,is_top_level,child_count)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range sessions {
		if _, err := stmt.ExecContext(ctx, s.DatasetID, s.UserID, s.SessionID, s.AgentID, s.Status,
			s.StartedAt, s.LastActivityAt, s.SortTSDescKey, s.ParentSessionID, s.RootSessionID, s.SubagentRole,
			s.NestingDepth, boolToInt(s.IsTopLevel), s.ChildCount); err != nil {
			return err
		}
	}
	return nil
}

func upsertCatalogSyncStateTx(ctx context.Context, tx *sql.Tx, p CatalogSyncState) error {
	return execSQL(ctx, tx, `INSERT INTO catalog_sync_state (`+catalogSyncColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dataset_id) DO UPDATE SET
		dataset_hex=excluded.dataset_hex, doc_count=excluded.doc_count, max_rowid=excluded.max_rowid,
		sum_rowid=excluded.sum_rowid, max_updated_at=excluded.max_updated_at, id_checksum=excluded.id_checksum,
		data_hash=excluded.data_hash, knowledge_count=excluded.knowledge_count, raw_doc_count=excluded.raw_doc_count,
		common_count=excluded.common_count, session_count=excluded.session_count, session_sig=excluded.session_sig,
		catalog_version=excluded.catalog_version, synced_at=excluded.synced_at`,
		p.DatasetID, p.DatasetHex, p.DocCount, p.MaxRowID, p.SumRowID, p.MaxUpdatedAt, p.IDChecksum,
		p.DataHash, p.KnowledgeCount, p.RawDocCount, p.CommonCount, p.SessionCount, p.SessionSig, p.CatalogVersion,
		p.SyncedAt)
}

func listCatalogDatasetIDsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT dataset_id FROM catalog_sync_state
		UNION SELECT DISTINCT dataset_id FROM catalog_docs
		UNION SELECT DISTINCT dataset_id FROM catalog_sessions
		UNION SELECT dataset_id FROM catalog_publish_state
		UNION SELECT DISTINCT dataset_id FROM catalog_doc_redirects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func nextCatalogVersion(prev string, hasPrev, changed bool) string {
	prev = strings.TrimSpace(prev)
	if !hasPrev {
		return "1"
	}
	if !changed {
		if prev == "" {
			return "1"
		}
		return prev
	}
	v, err := strconv.ParseInt(prev, 10, 64)
	if err != nil || v < 1 {
		v = 1
	}
	return strconv.FormatInt(v+1, 10)
}

// computeDocsDataHash excludes RecallCount because RecordRecallEvent bumps CatalogVersion
// directly; including RecallCount here would cause a redundant version bump on the next publish.
func computeDocsDataHash(docs []CatalogPublishDoc) string {
	if len(docs) == 0 {
		return ""
	}
	ordered := slices.Clone(docs)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].DocID < ordered[j].DocID
	})
	h := sha256.New()
	for _, d := range ordered {
		fmt.Fprintf(h, "%s\x00%s\x00%t\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\n",
			d.DocID, d.Kind, d.IsKnowledge, d.CommonCategory, d.LessonType, d.StateKey, d.Category,
			d.SessionID, d.DocName, d.DocLabel, d.ContentHash, d.CreatedAtRaw, d.CreatedAtNS, d.UpdatedAtNS,
			d.EffectiveDate, d.EffectiveDateSource, d.DayDescKey, d.SortTSDescKey,
			d.PipelineStatus, d.SourceUpdatedAt)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func computeSessionsSig(sessions []CatalogSession) string {
	if len(sessions) == 0 {
		return ""
	}
	ordered := slices.Clone(sessions)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].UserID != ordered[j].UserID {
			return ordered[i].UserID < ordered[j].UserID
		}
		return ordered[i].SessionID < ordered[j].SessionID
	})
	h := sha256.New()
	for _, s := range ordered {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s\x00%d\x00%t\x00%d\n",
			s.UserID, s.SessionID, s.AgentID, s.Status, s.StartedAt, s.LastActivityAt,
			s.SortTSDescKey, s.ParentSessionID, s.RootSessionID, s.SubagentRole,
			s.NestingDepth, s.IsTopLevel, s.ChildCount)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type sessionRelKey struct{ dataset, session string }
type catalogSessionKey struct{ userID, sessionID string }

func applySessionRelations(sessions []CatalogSession, relations []SessionRelation) {
	if len(sessions) == 0 {
		return
	}
	relMap := make(map[sessionRelKey]SessionRelation, len(relations))
	for _, rel := range relations {
		ds := catalogUUIDText(rel.DatasetID)
		sid := strings.TrimSpace(rel.SessionID)
		if ds == "" || sid == "" {
			continue
		}
		rel.DatasetID = ds
		rel.SessionID = sid
		rel.ParentSessionID = strings.TrimSpace(rel.ParentSessionID)
		rel.RootSessionID = strings.TrimSpace(rel.RootSessionID)
		rel.SubagentRole = strings.TrimSpace(rel.SubagentRole)
		relMap[sessionRelKey{ds, sid}] = rel
	}

	byDataset := make(map[string][]int)
	for i := range sessions {
		s := &sessions[i]
		s.ParentSessionID = strings.TrimSpace(s.ParentSessionID)
		s.RootSessionID = strings.TrimSpace(s.RootSessionID)
		s.SubagentRole = strings.TrimSpace(s.SubagentRole)
		if rel, ok := relMap[sessionRelKey{catalogUUIDText(s.DatasetID), s.SessionID}]; ok {
			if s.ParentSessionID == "" && rel.ParentSessionID != "" && rel.ParentSessionID != s.SessionID {
				s.ParentSessionID = rel.ParentSessionID
			}
			if s.RootSessionID == "" && rel.RootSessionID != "" {
				s.RootSessionID = rel.RootSessionID
			}
			if s.SubagentRole == "" && rel.SubagentRole != "" {
				s.SubagentRole = rel.SubagentRole
			}
		}
		if s.ParentSessionID == s.SessionID {
			s.ParentSessionID = ""
		}
		s.ChildCount = 0
		byDataset[s.DatasetID] = append(byDataset[s.DatasetID], i)
	}

	for _, idxs := range byDataset {
		byUserSession := make(map[catalogSessionKey]int, len(idxs))
		bySession := make(map[string]int, len(idxs))
		for _, idx := range idxs {
			s := sessions[idx]
			byUserSession[catalogSessionKey{userID: s.UserID, sessionID: s.SessionID}] = idx
			if _, exists := bySession[s.SessionID]; !exists {
				bySession[s.SessionID] = idx
			}
		}
		rawParent := func(idx int) (int, bool) {
			pid := sessions[idx].ParentSessionID
			if pid == "" || pid == sessions[idx].SessionID {
				return -1, false
			}
			if p, ok := byUserSession[catalogSessionKey{userID: sessions[idx].UserID, sessionID: pid}]; ok && p != idx {
				return p, true
			}
			if p, ok := bySession[pid]; ok && p != idx {
				return p, true
			}
			return -1, false
		}
		inCycle := make(map[int]bool)
		for _, idx := range idxs {
			visited := map[int]bool{idx: true}
			curr := idx
			for {
				p, ok := rawParent(curr)
				if !ok {
					break
				}
				if p == idx {
					inCycle[idx] = true
					break
				}
				if visited[p] {
					break
				}
				visited[p] = true
				curr = p
			}
		}
		findParent := func(idx int) (int, bool) {
			if inCycle[idx] {
				return -1, false
			}
			return rawParent(idx)
		}
		for _, idx := range idxs {
			s := &sessions[idx]
			if inCycle[idx] {
				s.ParentSessionID = ""
				s.SubagentRole = ""
				s.IsTopLevel = true
				s.NestingDepth = 0
				if s.RootSessionID == "" {
					s.RootSessionID = s.SessionID
				}
				continue
			}
			directParent, hasParent := findParent(idx)
			if !hasParent {
				s.IsTopLevel = true
				if s.ParentSessionID != "" || s.SubagentRole != "" {
					if s.NestingDepth < 1 {
						s.NestingDepth = 1
					}
					if s.RootSessionID == "" && s.ParentSessionID != "" {
						s.RootSessionID = s.ParentSessionID
					}
				} else {
					s.NestingDepth = 0
				}
				continue
			}
			visited := map[int]bool{idx: true}
			depth := 0
			curr := idx
			for {
				p, ok := findParent(curr)
				if !ok || visited[p] {
					break
				}
				visited[p] = true
				depth++
				curr = p
			}
			s.IsTopLevel = false
			root := sessions[curr]
			if root.ParentSessionID != "" && !inCycle[curr] {
				s.NestingDepth = depth + 1
				if root.RootSessionID != "" {
					s.RootSessionID = root.RootSessionID
				} else {
					s.RootSessionID = root.ParentSessionID
				}
			} else {
				s.NestingDepth = depth
				s.RootSessionID = root.SessionID
			}
			sessions[directParent].ChildCount++
		}
	}
}

func catalogUUIDText(raw string) string {
	raw = strings.TrimSpace(raw)
	if id, err := uuid.Parse(raw); err == nil {
		return id.String()
	}
	return raw
}

func catalogUUIDHex(raw string) string {
	if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil {
		return strings.ReplaceAll(id.String(), "-", "")
	}
	return ""
}

func catalogDayToDescKey(day string) int64 {
	if day == "" {
		return 0
	}
	key, _ := strconv.ParseInt(strings.ReplaceAll(day, "-", ""), 10, 64)
	return -key
}

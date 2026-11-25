package domain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

const readingDDL = `
CREATE TABLE IF NOT EXISTS ui_read_scopes (
 scope_hash TEXT PRIMARY KEY, project_id TEXT NOT NULL, dataset_id TEXT NOT NULL,
 initialized INTEGER NOT NULL CHECK(initialized IN (0,1)), enabled_at_ns TEXT NOT NULL, read_horizon_at_ns TEXT NOT NULL,
 horizon_max_rowid INTEGER NOT NULL DEFAULT 0, state_rev INTEGER NOT NULL DEFAULT 1,
 is_auto_baseline INTEGER NOT NULL DEFAULT 0 CHECK(is_auto_baseline IN (0,1)),
 legacy_file_sha256 TEXT NOT NULL DEFAULT '', legacy_mtime_ns INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ui_read_items (
 scope_hash TEXT NOT NULL REFERENCES ui_read_scopes(scope_hash) ON DELETE CASCADE,
 doc_id TEXT NOT NULL, is_read INTEGER NOT NULL CHECK(is_read IN (0,1)), updated_at TEXT NOT NULL,
 PRIMARY KEY(scope_hash,doc_id)
);
CREATE INDEX IF NOT EXISTS idx_ui_read_items_state ON ui_read_items(scope_hash,is_read,doc_id);
`

type ReadScope struct {
	ScopeHash, ProjectID, DatasetID string
	Initialized                     bool
	EnabledAtNS, ReadHorizonAtNS    string
	HorizonMaxRowID, StateRev       int64
	IsAutoBaseline                  bool
	LegacyFileSHA256                string
	LegacyMtimeNS                   int64
	UpdatedAt                       string
}

type ReadItem struct {
	ScopeHash, DocID string
	IsRead           bool
	UpdatedAt        string
}

func KnowledgeReadScopeHash(readingIdentity, projectID, datasetID string) string {
	hash := sha256.Sum256([]byte(readingIdentity + "/" + projectID + "/" + datasetID))
	return hex.EncodeToString(hash[:])
}

const readScopeColumns = "scope_hash,project_id,dataset_id,initialized,enabled_at_ns,read_horizon_at_ns,horizon_max_rowid,state_rev,is_auto_baseline,legacy_file_sha256,legacy_mtime_ns,updated_at"

func scanReadScope(row scanner) (ReadScope, error) {
	var scope ReadScope
	err := row.Scan(&scope.ScopeHash, &scope.ProjectID, &scope.DatasetID, &scope.Initialized, &scope.EnabledAtNS, &scope.ReadHorizonAtNS,
		&scope.HorizonMaxRowID, &scope.StateRev, &scope.IsAutoBaseline, &scope.LegacyFileSHA256, &scope.LegacyMtimeNS, &scope.UpdatedAt)
	return scope, rowError(err)
}

func (s *Store) GetReadScope(ctx context.Context, scopeHash string) (ReadScope, error) {
	return scanReadScope(s.db.QueryRowContext(ctx, `SELECT `+readScopeColumns+` FROM ui_read_scopes WHERE scope_hash=?`, scopeHash))
}

func (s *Store) EnsureReadScope(ctx context.Context, readingIdentity, projectID, datasetID string) (ReadScope, error) {
	if err := requireIDs(readingIdentity, projectID, datasetID); err != nil {
		return ReadScope{}, err
	}
	hash := KnowledgeReadScopeHash(readingIdentity, projectID, datasetID)
	var scope ReadScope
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCNS()
		var horizon string
		err := tx.QueryRowContext(ctx, `SELECT MAX(?,COALESCE(MAX(created_at_ns),'')) FROM catalog_docs
			WHERE dataset_id=? AND is_knowledge=1 AND created_at_invalid=0`, now, datasetID).Scan(&horizon)
		if err != nil {
			return err
		}
		if err := execSQL(ctx, tx, `INSERT INTO ui_read_scopes (`+readScopeColumns+`) VALUES(?,?,?,1,?,?,0,1,1,'',0,?)
			ON CONFLICT(scope_hash) DO UPDATE SET initialized=1,enabled_at_ns=excluded.enabled_at_ns,
			read_horizon_at_ns=excluded.read_horizon_at_ns,horizon_max_rowid=0,state_rev=state_rev+1,
			is_auto_baseline=1,updated_at=excluded.updated_at WHERE ui_read_scopes.initialized=0`,
			hash, projectID, datasetID, horizon, horizon, NowUTCMicro()); err != nil {
			return err
		}
		scope, err = scanReadScope(tx.QueryRowContext(ctx, `SELECT `+readScopeColumns+` FROM ui_read_scopes WHERE scope_hash=?`, hash))
		if err == nil && (scope.ProjectID != projectID || scope.DatasetID != datasetID) {
			return ErrConflict
		}
		return err
	})
	return scope, err
}

func (s *Store) UpsertReadScope(ctx context.Context, scope ReadScope) error {
	if err := requireIDs(scope.ScopeHash, scope.ProjectID, scope.DatasetID); err != nil {
		return err
	}
	for _, stamp := range []string{scope.EnabledAtNS, scope.ReadHorizonAtNS} {
		if stamp != "" && !IsValidUTCNS(stamp) {
			return fmt.Errorf("invalid read scope timestamp: %w", ErrInvalidArgument)
		}
	}
	return execSQL(ctx, s.db, `INSERT INTO ui_read_scopes (`+readScopeColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(scope_hash) DO UPDATE SET project_id=excluded.project_id,dataset_id=excluded.dataset_id,initialized=excluded.initialized,
		enabled_at_ns=excluded.enabled_at_ns,read_horizon_at_ns=excluded.read_horizon_at_ns,horizon_max_rowid=excluded.horizon_max_rowid,
		state_rev=excluded.state_rev,is_auto_baseline=excluded.is_auto_baseline,legacy_file_sha256=excluded.legacy_file_sha256,
		legacy_mtime_ns=excluded.legacy_mtime_ns,updated_at=excluded.updated_at`,
		scope.ScopeHash, scope.ProjectID, scope.DatasetID, boolToInt(scope.Initialized), scope.EnabledAtNS, scope.ReadHorizonAtNS,
		scope.HorizonMaxRowID, max(1, scope.StateRev), boolToInt(scope.IsAutoBaseline), scope.LegacyFileSHA256, scope.LegacyMtimeNS,
		defaultText(scope.UpdatedAt, NowUTCMicro()))
}

func (s *Store) SetReadItem(ctx context.Context, scopeHash, docID string, isRead bool) error {
	if err := requireIDs(scopeHash, docID); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		if err := requireAffected(tx.ExecContext(ctx, `UPDATE ui_read_scopes SET state_rev=state_rev+1,updated_at=? WHERE scope_hash=?`, now, scopeHash)); err != nil {
			return err
		}
		return execSQL(ctx, tx, `INSERT INTO ui_read_items VALUES(?,?,?,?) ON CONFLICT(scope_hash,doc_id) DO UPDATE SET
			is_read=excluded.is_read,updated_at=excluded.updated_at`, scopeHash, docID, boolToInt(isRead), now)
	})
}

func (s *Store) MarkAllReadInScope(ctx context.Context, scopeHash, horizonNS string, horizonMaxRowID int64) error {
	if !IsValidUTCNS(horizonNS) || horizonMaxRowID < 0 {
		return fmt.Errorf("invalid read horizon: %w", ErrInvalidArgument)
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUTCMicro()
		if err := requireAffected(tx.ExecContext(ctx, `UPDATE ui_read_scopes SET read_horizon_at_ns=MAX(read_horizon_at_ns,?),
			horizon_max_rowid=MAX(horizon_max_rowid,?),state_rev=state_rev+1,is_auto_baseline=0,updated_at=? WHERE scope_hash=?`, horizonNS, horizonMaxRowID, now, scopeHash)); err != nil {
			return err
		}
		if err := execSQL(ctx, tx, `DELETE FROM ui_read_items WHERE scope_hash=? AND is_read=0`, scopeHash); err != nil {
			return err
		}
		return execSQL(ctx, tx, `INSERT INTO ui_read_items(scope_hash,doc_id,is_read,updated_at)
			SELECT s.scope_hash,d.doc_id,1,? FROM ui_read_scopes s JOIN catalog_docs d ON d.dataset_id=s.dataset_id
			WHERE s.scope_hash=? AND d.is_knowledge=1 AND d.created_at_invalid=1
			ON CONFLICT(scope_hash,doc_id) DO UPDATE SET is_read=1,updated_at=excluded.updated_at`, now, scopeHash)
	})
}

func scanReadItem(row scanner) (ReadItem, error) {
	var item ReadItem
	err := row.Scan(&item.ScopeHash, &item.DocID, &item.IsRead, &item.UpdatedAt)
	return item, rowError(err)
}

func (s *Store) GetReadItem(ctx context.Context, scopeHash, docID string) (ReadItem, error) {
	return scanReadItem(s.db.QueryRowContext(ctx, `SELECT scope_hash,doc_id,is_read,updated_at FROM ui_read_items WHERE scope_hash=? AND doc_id=?`, scopeHash, docID))
}

func (s *Store) ListReadItems(ctx context.Context, scopeHash string) ([]ReadItem, error) {
	return queryList(ctx, s.db, scanReadItem, `SELECT scope_hash,doc_id,is_read,updated_at FROM ui_read_items WHERE scope_hash=? ORDER BY doc_id`, scopeHash)
}

func (s *Store) CountUnreadInScope(ctx context.Context, scopeHash string) (int, error) {
	var count int
	// Explicit unread items are counted separately, so the baseline branches require no override.
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM catalog_docs d WHERE d.dataset_id=s.dataset_id AND d.is_knowledge=1
		 AND d.created_at_invalid=0 AND d.created_at_ns>s.read_horizon_at_ns
		 AND NOT EXISTS(SELECT 1 FROM ui_read_items i WHERE i.scope_hash=s.scope_hash AND i.doc_id=d.doc_id))
		+
		(SELECT COUNT(*) FROM catalog_docs d WHERE d.dataset_id=s.dataset_id AND d.is_knowledge=1
		 AND d.created_at_invalid=1
		 AND NOT EXISTS(SELECT 1 FROM ui_read_items i WHERE i.scope_hash=s.scope_hash AND i.doc_id=d.doc_id))
		+
		(SELECT COUNT(*) FROM ui_read_items i JOIN catalog_docs d ON d.dataset_id=s.dataset_id AND d.doc_id=i.doc_id
		 WHERE i.scope_hash=s.scope_hash AND i.is_read=0 AND d.is_knowledge=1)
		FROM ui_read_scopes s WHERE s.scope_hash=?`, scopeHash).Scan(&count)
	return count, rowError(err)
}

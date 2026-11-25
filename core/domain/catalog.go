package domain

import (
	"context"
	"database/sql"
	"fmt"
)

const catalogDDL = `
CREATE TABLE IF NOT EXISTS catalog_docs (
 dataset_id TEXT NOT NULL, doc_id TEXT NOT NULL, cognee_rowid INTEGER NOT NULL DEFAULT 0,
 kind TEXT NOT NULL CHECK(kind IN ('knowledge','common','raw_doc')),
 is_knowledge INTEGER NOT NULL CHECK(is_knowledge IN (0,1)),
 common_category TEXT NOT NULL DEFAULT '' CHECK(common_category IN ('','preference','experience')),
 lesson_type TEXT NOT NULL DEFAULT '', state_key TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '',
 session_id TEXT NOT NULL DEFAULT '', doc_name TEXT NOT NULL DEFAULT '', doc_label TEXT NOT NULL DEFAULT '',
 raw_data_location TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '', created_at_raw TEXT NOT NULL DEFAULT '',
 created_at_ns TEXT NOT NULL DEFAULT '', created_at_invalid INTEGER NOT NULL DEFAULT 0 CHECK(created_at_invalid IN (0,1)),
 updated_at_ns TEXT NOT NULL DEFAULT '', effective_date TEXT NOT NULL DEFAULT '', effective_date_source TEXT NOT NULL DEFAULT '',
 day_desc_key INTEGER NOT NULL DEFAULT 0, sort_ts_desc_key INTEGER NOT NULL DEFAULT 0, recall_count INTEGER NOT NULL DEFAULT 0,
 recall_desc_key INTEGER NOT NULL DEFAULT 0, pipeline_status TEXT NOT NULL DEFAULT '', source_updated_at TEXT NOT NULL DEFAULT '',
 doc_seq INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(dataset_id,doc_id)
);
CREATE INDEX IF NOT EXISTS idx_catalog_docs_knowledge_page ON catalog_docs(dataset_id,kind,day_desc_key,doc_id);
CREATE INDEX IF NOT EXISTS idx_catalog_docs_knowledge_recall ON catalog_docs(dataset_id,kind,recall_desc_key,day_desc_key,doc_id);
CREATE INDEX IF NOT EXISTS idx_catalog_docs_common_page ON catalog_docs(dataset_id,kind,common_category,sort_ts_desc_key,doc_id);
CREATE INDEX IF NOT EXISTS idx_catalog_docs_raw_page ON catalog_docs(dataset_id,kind,sort_ts_desc_key,doc_id);
CREATE INDEX IF NOT EXISTS idx_catalog_docs_unread ON catalog_docs(dataset_id,is_knowledge,created_at_invalid,created_at_ns,doc_id);
CREATE TABLE IF NOT EXISTS catalog_sessions (
 dataset_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL,
 agent_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '',
 last_activity_at TEXT NOT NULL DEFAULT '', sort_ts_desc_key INTEGER NOT NULL DEFAULT 0,
 parent_session_id TEXT NOT NULL DEFAULT '', root_session_id TEXT NOT NULL DEFAULT '', subagent_role TEXT NOT NULL DEFAULT '',
 nesting_depth INTEGER NOT NULL DEFAULT 0, is_top_level INTEGER NOT NULL DEFAULT 1 CHECK(is_top_level IN (0,1)),
 child_count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(dataset_id,user_id,session_id)
);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_page ON catalog_sessions(dataset_id,sort_ts_desc_key,user_id,session_id);
CREATE TABLE IF NOT EXISTS catalog_sync_state (
 dataset_id TEXT PRIMARY KEY, dataset_hex TEXT NOT NULL DEFAULT '', doc_count INTEGER NOT NULL DEFAULT 0,
 max_rowid INTEGER NOT NULL DEFAULT 0, sum_rowid INTEGER NOT NULL DEFAULT 0, max_updated_at TEXT NOT NULL DEFAULT '',
 id_checksum INTEGER NOT NULL DEFAULT 0, data_hash TEXT NOT NULL DEFAULT '', knowledge_count INTEGER NOT NULL DEFAULT 0,
 raw_doc_count INTEGER NOT NULL DEFAULT 0, common_count INTEGER NOT NULL DEFAULT 0, session_count INTEGER NOT NULL DEFAULT 0,
 session_sig TEXT NOT NULL DEFAULT '', catalog_version TEXT NOT NULL DEFAULT '', synced_at TEXT NOT NULL DEFAULT ''
);
`

func (s *Store) ClearCatalogProjection(ctx context.Context) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, "DELETE FROM catalog_docs; DELETE FROM catalog_sessions; DELETE FROM catalog_sync_state;")
	})
}

func (s *Store) ListCatalogDocs(ctx context.Context, datasetID, kind string, limit int) ([]CatalogDoc, error) {
	var order string
	switch kind {
	case "knowledge":
		order = "day_desc_key,doc_id"
	case "common", "raw_doc":
		order = "sort_ts_desc_key,doc_id"
	default:
		return nil, fmt.Errorf("catalog kind: %w", ErrInvalidArgument)
	}
	return queryList(ctx, s.db, scanCatalogDoc, `SELECT `+catalogDocColumns+` FROM catalog_docs
		WHERE dataset_id=? AND kind=? ORDER BY `+order+` LIMIT ?`, datasetID, kind, pageLimit(limit))
}

func (s *Store) ListCatalogSessions(ctx context.Context, datasetID string, limit int) ([]CatalogSession, error) {
	return queryList(ctx, s.db, scanCatalogSession, `SELECT `+catalogSessionColumns+` FROM catalog_sessions
		WHERE dataset_id=? ORDER BY sort_ts_desc_key,user_id,session_id LIMIT ?`, datasetID, pageLimit(limit))
}

func (s *Store) GetCatalogSyncState(ctx context.Context, datasetID string) (CatalogSyncState, error) {
	return scanCatalogSyncState(s.db.QueryRowContext(ctx, `SELECT `+catalogSyncColumns+` FROM catalog_sync_state WHERE dataset_id=?`, datasetID))
}

type CatalogDoc struct {
	DatasetID           string
	DocID               string
	CogneeRowID         int64
	Kind                string
	IsKnowledge         bool
	CommonCategory      string
	LessonType          string
	StateKey            string
	Category            string
	SessionID           string
	DocName             string
	DocLabel            string
	RawDataLocation     string
	ContentHash         string
	CreatedAtRaw        string
	CreatedAtNS         string
	CreatedAtInvalid    bool
	UpdatedAtNS         string
	EffectiveDate       string
	EffectiveDateSource string
	DayDescKey          int64
	SortTSDescKey       int64
	RecallCount         int
	RecallDescKey       int64
	PipelineStatus      string
	SourceUpdatedAt     string
	DocSeq              int64
}

const catalogDocColumns = `dataset_id, doc_id, cognee_rowid, kind, is_knowledge, common_category, lesson_type,
		state_key, category, session_id, doc_name, doc_label, raw_data_location, content_hash,
		created_at_raw, created_at_ns, created_at_invalid, updated_at_ns, effective_date, effective_date_source, day_desc_key,
		sort_ts_desc_key, recall_count, recall_desc_key, pipeline_status, source_updated_at, doc_seq`

func scanCatalogDoc(row scanner) (CatalogDoc, error) {
	var p CatalogDoc
	err := row.Scan(&p.DatasetID, &p.DocID, &p.CogneeRowID, &p.Kind, &p.IsKnowledge, &p.CommonCategory, &p.LessonType,
		&p.StateKey, &p.Category, &p.SessionID, &p.DocName, &p.DocLabel, &p.RawDataLocation, &p.ContentHash,
		&p.CreatedAtRaw, &p.CreatedAtNS, &p.CreatedAtInvalid, &p.UpdatedAtNS, &p.EffectiveDate, &p.EffectiveDateSource, &p.DayDescKey,
		&p.SortTSDescKey, &p.RecallCount, &p.RecallDescKey, &p.PipelineStatus, &p.SourceUpdatedAt, &p.DocSeq)
	return p, rowError(err)
}

func (s *Store) UpsertCatalogDoc(ctx context.Context, p CatalogDoc) error {
	if err := requireIDs(p.DatasetID, p.DocID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO catalog_docs (`+catalogDocColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dataset_id,doc_id) DO UPDATE SET
		cognee_rowid=excluded.cognee_rowid, kind=excluded.kind, is_knowledge=excluded.is_knowledge,
		common_category=excluded.common_category, lesson_type=excluded.lesson_type, state_key=excluded.state_key,
		category=excluded.category, session_id=excluded.session_id, doc_name=excluded.doc_name,
		doc_label=excluded.doc_label, raw_data_location=excluded.raw_data_location, content_hash=excluded.content_hash,
		created_at_raw=excluded.created_at_raw, created_at_ns=excluded.created_at_ns, created_at_invalid=excluded.created_at_invalid,
		updated_at_ns=excluded.updated_at_ns, effective_date=excluded.effective_date, effective_date_source=excluded.effective_date_source,
		day_desc_key=excluded.day_desc_key, sort_ts_desc_key=excluded.sort_ts_desc_key, recall_count=excluded.recall_count,
		recall_desc_key=excluded.recall_desc_key, pipeline_status=excluded.pipeline_status, source_updated_at=excluded.source_updated_at,
		doc_seq=excluded.doc_seq`,
		p.DatasetID, p.DocID, p.CogneeRowID, p.Kind, boolToInt(p.IsKnowledge), p.CommonCategory, p.LessonType,
		p.StateKey, p.Category, p.SessionID, p.DocName, p.DocLabel, p.RawDataLocation, p.ContentHash,
		p.CreatedAtRaw, p.CreatedAtNS, boolToInt(p.CreatedAtInvalid), p.UpdatedAtNS, p.EffectiveDate, p.EffectiveDateSource, p.DayDescKey,
		p.SortTSDescKey, p.RecallCount, p.RecallDescKey, p.PipelineStatus, p.SourceUpdatedAt, p.DocSeq)
}

type CatalogSession struct {
	DatasetID       string
	UserID          string
	SessionID       string
	AgentID         string
	Status          string
	StartedAt       string
	LastActivityAt  string
	SortTSDescKey   int64
	ParentSessionID string
	RootSessionID   string
	SubagentRole    string
	NestingDepth    int
	IsTopLevel      bool
	ChildCount      int
}

const catalogSessionColumns = `dataset_id, user_id, session_id, agent_id, status, started_at, last_activity_at,
		sort_ts_desc_key, parent_session_id, root_session_id, subagent_role, nesting_depth, is_top_level, child_count`

func scanCatalogSession(row scanner) (CatalogSession, error) {
	var p CatalogSession
	err := row.Scan(&p.DatasetID, &p.UserID, &p.SessionID, &p.AgentID, &p.Status, &p.StartedAt, &p.LastActivityAt,
		&p.SortTSDescKey, &p.ParentSessionID, &p.RootSessionID, &p.SubagentRole, &p.NestingDepth, &p.IsTopLevel, &p.ChildCount)
	return p, rowError(err)
}

func (s *Store) UpsertCatalogSession(ctx context.Context, p CatalogSession) error {
	if err := requireIDs(p.DatasetID, p.SessionID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO catalog_sessions (`+catalogSessionColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dataset_id,user_id,session_id) DO UPDATE SET
		agent_id=excluded.agent_id, status=excluded.status, started_at=excluded.started_at,
		last_activity_at=excluded.last_activity_at, sort_ts_desc_key=excluded.sort_ts_desc_key, parent_session_id=excluded.parent_session_id,
		root_session_id=excluded.root_session_id, subagent_role=excluded.subagent_role, nesting_depth=excluded.nesting_depth,
		is_top_level=excluded.is_top_level, child_count=excluded.child_count`,
		p.DatasetID, p.UserID, p.SessionID, p.AgentID, p.Status, p.StartedAt, p.LastActivityAt,
		p.SortTSDescKey, p.ParentSessionID, p.RootSessionID, p.SubagentRole, p.NestingDepth, boolToInt(p.IsTopLevel), p.ChildCount)
}

type CatalogSyncState struct {
	DatasetID      string
	DatasetHex     string
	DocCount       int
	MaxRowID       int64
	SumRowID       int64
	MaxUpdatedAt   string
	IDChecksum     int64
	DataHash       string
	KnowledgeCount int
	RawDocCount    int
	CommonCount    int
	SessionCount   int
	SessionSig     string
	CatalogVersion string
	SyncedAt       string
}

const catalogSyncColumns = `dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
		data_hash, knowledge_count, raw_doc_count, common_count, session_count, session_sig, catalog_version,
		synced_at`

func scanCatalogSyncState(row scanner) (CatalogSyncState, error) {
	var p CatalogSyncState
	err := row.Scan(&p.DatasetID, &p.DatasetHex, &p.DocCount, &p.MaxRowID, &p.SumRowID, &p.MaxUpdatedAt, &p.IDChecksum,
		&p.DataHash, &p.KnowledgeCount, &p.RawDocCount, &p.CommonCount, &p.SessionCount, &p.SessionSig, &p.CatalogVersion,
		&p.SyncedAt)
	return p, rowError(err)
}

func (s *Store) UpsertCatalogSyncState(ctx context.Context, p CatalogSyncState) error {
	if err := requireIDs(p.DatasetID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO catalog_sync_state (`+catalogSyncColumns+`)
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

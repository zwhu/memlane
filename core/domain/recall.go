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
	"strings"
)

const recallDDL = `
CREATE TABLE IF NOT EXISTS recall_events (
 agent_id TEXT NOT NULL, event_id TEXT NOT NULL, channel TEXT NOT NULL CHECK(channel IN ('automatic','explicit')),
 received_at TEXT NOT NULL, payload_hash TEXT NOT NULL, PRIMARY KEY(agent_id,event_id)
);
CREATE INDEX IF NOT EXISTS idx_recall_events_received ON recall_events(received_at DESC,agent_id,event_id);
CREATE TABLE IF NOT EXISTS recall_hits (
 agent_id TEXT NOT NULL, event_id TEXT NOT NULL, dataset_id TEXT NOT NULL, document_id TEXT NOT NULL,
 PRIMARY KEY(agent_id,event_id,dataset_id,document_id),
 FOREIGN KEY(agent_id,event_id) REFERENCES recall_events(agent_id,event_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_recall_hits_document ON recall_hits(dataset_id,document_id);
CREATE INDEX IF NOT EXISTS idx_recall_hits_dataset_event ON recall_hits(dataset_id,agent_id,event_id);
CREATE TABLE IF NOT EXISTS recall_doc_counts (
 dataset_id TEXT NOT NULL, document_id TEXT NOT NULL, total_hits INTEGER NOT NULL DEFAULT 0,
 auto_hits INTEGER NOT NULL DEFAULT 0, explicit_hits INTEGER NOT NULL DEFAULT 0, last_recalled_at TEXT DEFAULT '',
 rev INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(dataset_id,document_id)
);
CREATE INDEX IF NOT EXISTS idx_recall_doc_counts_rev ON recall_doc_counts(rev);
CREATE TABLE IF NOT EXISTS recall_query_stats (
 channel TEXT PRIMARY KEY CHECK(channel IN ('automatic','explicit')), total_queries INTEGER NOT NULL DEFAULT 0,
 hit_queries INTEGER NOT NULL DEFAULT 0, empty_queries INTEGER NOT NULL DEFAULT 0, total_hits INTEGER NOT NULL DEFAULT 0,
 last_query_at TEXT DEFAULT ''
);
`

type RecallHit struct{ DatasetID, DocumentID string }

type RecallEventInput struct {
	AgentID, EventID, Channel, ReceivedAt string
	Hits                                  []RecallHit
}

type RecallDocCount struct {
	DatasetID, DocumentID             string
	TotalHits, AutoHits, ExplicitHits int
	LastRecalledAt                    string
	Rev                               int64
}

type RecallQueryStat struct {
	Channel                                           string
	TotalQueries, HitQueries, EmptyQueries, TotalHits int
	LastQueryAt                                       string
}

func validRecallChannel(channel string) bool {
	return channel == "automatic" || channel == "explicit"
}

func (s *Store) RecordRecallEvent(ctx context.Context, in RecallEventInput) (bool, error) {
	if err := requireIDs(in.AgentID, in.EventID); err != nil {
		return false, err
	}
	if !validRecallChannel(in.Channel) || len(in.Hits) == 0 || len(in.Hits) > 100 {
		return false, fmt.Errorf("invalid recall event: %w", ErrInvalidArgument)
	}
	hits := slices.Clone(in.Hits)
	for _, hit := range hits {
		if err := requireIDs(hit.DatasetID, hit.DocumentID); err != nil {
			return false, err
		}
	}
	slices.SortFunc(hits, func(a, b RecallHit) int {
		if cmp := strings.Compare(a.DatasetID, b.DatasetID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.DocumentID, b.DocumentID)
	})
	hits = slices.Compact(hits)
	hash, err := recallPayloadHash(in.Channel, hits)
	if err != nil {
		return false, err
	}
	recorded := false
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var oldHash string
		err := tx.QueryRowContext(ctx, `SELECT payload_hash FROM recall_events WHERE agent_id=? AND event_id=?`, in.AgentID, in.EventID).Scan(&oldHash)
		if err == nil {
			if oldHash != hash {
				return fmt.Errorf("recall event payload changed: %w", ErrConflict)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		at := defaultText(in.ReceivedAt, NowUTCMicro())
		if err := execSQL(ctx, tx, `INSERT INTO recall_events VALUES(?,?,?,?,?)`, in.AgentID, in.EventID, in.Channel, at, hash); err != nil {
			return err
		}
		var rev int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rev),0)+1 FROM recall_doc_counts`).Scan(&rev); err != nil {
			return err
		}
		for _, hit := range hits {
			if err := execSQL(ctx, tx, `INSERT INTO recall_hits VALUES(?,?,?,?)`, in.AgentID, in.EventID, hit.DatasetID, hit.DocumentID); err != nil {
				return err
			}
			if err := execSQL(ctx, tx, `INSERT INTO recall_doc_counts VALUES(?,?,1,?,?,?,?)
				ON CONFLICT(dataset_id,document_id) DO UPDATE SET total_hits=total_hits+1,auto_hits=auto_hits+excluded.auto_hits,
				explicit_hits=explicit_hits+excluded.explicit_hits,last_recalled_at=MAX(COALESCE(last_recalled_at,''),excluded.last_recalled_at),rev=excluded.rev`,
				hit.DatasetID, hit.DocumentID, boolToInt(in.Channel == "automatic"), boolToInt(in.Channel == "explicit"), at, rev); err != nil {
				return err
			}
		}
		recorded = true
		return nil
	})
	return recorded && err == nil, err
}

func recallPayloadHash(channel string, hits []RecallHit) (string, error) {
	// Match the legacy JSON fingerprint for persisted event identities.
	var payload strings.Builder
	payload.WriteString(`["` + channel + `", [`)
	for i, hit := range hits {
		dataset, err := json.Marshal(hit.DatasetID)
		if err != nil {
			return "", err
		}
		doc, err := json.Marshal(hit.DocumentID)
		if err != nil {
			return "", err
		}
		if i > 0 {
			payload.WriteString(", ")
		}
		payload.WriteByte('[')
		payload.Write(dataset)
		payload.WriteString(", ")
		payload.Write(doc)
		payload.WriteByte(']')
	}
	payload.WriteString("]]")
	digest := sha256.Sum256([]byte(payload.String()))
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) RecordRecallQueryStat(ctx context.Context, channel string, hitCount int, at string) error {
	if !validRecallChannel(channel) || hitCount < 0 {
		return fmt.Errorf("invalid recall query stat: %w", ErrInvalidArgument)
	}
	return execSQL(ctx, s.db, `INSERT INTO recall_query_stats VALUES(?,1,?,?,?,?)
		ON CONFLICT(channel) DO UPDATE SET total_queries=total_queries+1,hit_queries=hit_queries+excluded.hit_queries,
		empty_queries=empty_queries+excluded.empty_queries,total_hits=total_hits+excluded.total_hits,
		last_query_at=MAX(COALESCE(last_query_at,''),excluded.last_query_at)`, channel, boolToInt(hitCount > 0), boolToInt(hitCount == 0), hitCount, defaultText(at, NowUTCMicro()))
}

const recallDocColumns = "dataset_id,document_id,total_hits,auto_hits,explicit_hits,last_recalled_at,rev"

func scanRecallDocCount(row scanner) (RecallDocCount, error) {
	var c RecallDocCount
	var last sql.NullString
	err := row.Scan(&c.DatasetID, &c.DocumentID, &c.TotalHits, &c.AutoHits, &c.ExplicitHits, &last, &c.Rev)
	c.LastRecalledAt = last.String
	return c, rowError(err)
}

func (s *Store) GetRecallDocCount(ctx context.Context, datasetID, documentID string) (RecallDocCount, error) {
	return scanRecallDocCount(s.db.QueryRowContext(ctx, `SELECT `+recallDocColumns+` FROM recall_doc_counts WHERE dataset_id=? AND document_id=?`, datasetID, documentID))
}

func (s *Store) ListRecallDocCounts(ctx context.Context, datasetID string) ([]RecallDocCount, error) {
	return queryList(ctx, s.db, scanRecallDocCount, `SELECT `+recallDocColumns+` FROM recall_doc_counts WHERE dataset_id=? ORDER BY document_id`, datasetID)
}

func (s *Store) GetRecallQueryStats(ctx context.Context) ([]RecallQueryStat, error) {
	return queryList(ctx, s.db, func(row scanner) (RecallQueryStat, error) {
		var stat RecallQueryStat
		var last sql.NullString
		err := row.Scan(&stat.Channel, &stat.TotalQueries, &stat.HitQueries, &stat.EmptyQueries, &stat.TotalHits, &last)
		stat.LastQueryAt = last.String
		return stat, rowError(err)
	}, `SELECT channel,total_queries,hit_queries,empty_queries,total_hits,last_query_at FROM recall_query_stats ORDER BY channel`)
}

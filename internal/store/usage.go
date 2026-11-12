package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func UsageDigest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RecordUsage rechecks connection generation and current scope/version in the
// same short transaction as the observation. No model or network work is here.
func (s *Store) RecordUsage(ctx context.Context, o records.UsageObservation) (records.UsageReceipt, error) {
	receipts, err := s.RecordUsageBatch(ctx, []records.UsageObservation{o})
	if err != nil {
		return records.UsageReceipt{}, err
	}
	return receipts[0], nil
}

// Mixed search channels are one successful return. Any failed target or write
// rolls back the complete batch; search/network work occurs before this call.
func (s *Store) RecordUsageBatch(ctx context.Context, batch []records.UsageObservation) ([]records.UsageReceipt, error) {
	if len(batch) == 0 || len(batch) > 2 {
		return nil, access("invalid_request", "bounded usage batch required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]records.UsageReceipt, 0, len(batch))
	for _, o := range batch {
		receipt, e := s.recordUsageTx(ctx, tx, o)
		if e != nil {
			return nil, e
		}
		out = append(out, receipt)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) recordUsageTx(ctx context.Context, tx *sql.Tx, o records.UsageObservation) (records.UsageReceipt, error) {
	var receipt records.UsageReceipt
	if s.Schema < 10 {
		return receipt, access("usage_unavailable", "usage collection not available in this schema")
	}
	if strings.TrimSpace(o.OperationID) == "" || len(o.OperationID) > 256 || len(o.Digest) != 64 || len(o.Targets) > 100 {
		return receipt, access("invalid_request", "bounded usage ID, digest and targets required")
	}
	switch o.Channel {
	case "daily", "app", "verification", "background":
	default:
		return receipt, access("invalid_request", "unknown usage channel")
	}
	switch o.Kind {
	case "read", "return", "adopt", "browse", "status":
	default:
		return receipt, access("invalid_request", "unknown usage kind")
	}
	if o.Kind == "status" && o.Channel != "verification" || o.Channel == "app" && o.Kind != "browse" && o.Kind != "return" || o.Channel != "app" && o.Kind == "browse" || o.Kind == "adopt" && (o.Channel != "daily" || o.Exposure == "" || o.WorkspaceID == "" || len(o.Targets) != 1) {
		return receipt, access("invalid_request", "invalid usage channel/kind")
	}
	seen := map[string]bool{}
	targets := []records.UsageTarget{}
	for _, v := range o.Targets {
		key := fmt.Sprint(v.Memory, "/", v.Revision)
		if !seen[key] {
			targets = append(targets, v)
			seen[key] = true
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Memory == targets[j].Memory {
			return targets[i].Revision < targets[j].Revision
		}
		return targets[i].Memory < targets[j].Memory
	})
	o.Targets = targets
	digest := UsageDigest(o)
	var err error
	if o.Channel == "daily" || o.Channel == "verification" {
		c, e := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, o.ConnectionID))
		if e != nil {
			return receipt, e
		}
		if !c.Enabled || c.Generation != o.Generation {
			return receipt, access("reload_required", "connection changed; reload Agent")
		}
	}
	// A usage event must still describe the actual current data group.
	for _, v := range targets {
		var dataset string
		if e := tx.QueryRowContext(ctx, `SELECT dataset FROM memories WHERE id=?`, v.Memory).Scan(&dataset); e != nil {
			return receipt, e
		}
		if dataset != v.Dataset {
			return receipt, access("source_changed", "usage target group changed")
		}
	}
	var prior, raw string
	err = tx.QueryRowContext(ctx, `SELECT digest,receipt FROM usage_operations WHERE id=?`, o.OperationID).Scan(&prior, &raw)
	if err == nil {
		if prior != digest {
			return receipt, records.ErrConflict
		}
		err = json.Unmarshal([]byte(raw), &receipt)
		return receipt, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	for _, v := range targets {
		var valid int
		query := `SELECT count(*) FROM memories m WHERE m.id=? AND m.dataset=? AND m.current_revision=?`
		// Explicit reads retain P2's ability to inspect a flagged current record.
		// A successful read is not permission to adopt archived/deleted knowledge.
		query += ` AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')`
		if o.Kind != "read" {
			query += ` AND m.state='active'`
		}
		e := tx.QueryRowContext(ctx, query, v.Memory, v.Dataset, v.Revision).Scan(&valid)
		if e != nil {
			return receipt, e
		}
		if valid != 1 {
			return receipt, access("version_changed", "usage requires the observed current version and permitted state")
		}
	}
	now := time.Now().UnixMilli()
	if o.Kind == "adopt" {
		v := targets[0]
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM usage_events WHERE id=? AND memory=? AND revision=? AND dataset=? AND connection=? AND generation=? AND workspace=? AND channel='daily' AND kind IN ('read','return') AND observed_ms>=?`, o.Exposure, v.Memory, v.Revision, v.Dataset, o.ConnectionID, o.Generation, o.WorkspaceID, now-int64(90*24*time.Hour/time.Millisecond)).Scan(&n)
		if err != nil {
			return receipt, err
		}
		if n != 1 {
			return receipt, access("exposure_required", "adoption needs a matching recent read/search receipt from this connection generation")
		}
	}
	receipt = records.UsageReceipt{ID: o.OperationID, Kind: o.Kind, Channel: o.Channel, ObservedMS: now, Targets: targets, WorkspaceID: o.WorkspaceID}
	encoded, _ := json.Marshal(receipt)
	if _, err = tx.ExecContext(ctx, `INSERT INTO usage_operations(id,digest,receipt) VALUES(?,?,?)`, o.OperationID, digest, string(encoded)); err != nil {
		return receipt, err
	}
	for _, v := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO usage_events(id,kind,channel,connection,generation,workspace,dataset,memory,revision,observed_ms,exposure) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, o.OperationID, o.Kind, o.Channel, o.ConnectionID, o.Generation, o.WorkspaceID, v.Dataset, v.Memory, v.Revision, now, o.Exposure); err != nil {
			return receipt, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM usage_events WHERE observed_ms<?`, now-int64(90*24*time.Hour/time.Millisecond)); err != nil {
		return receipt, err
	}
	if o.ConnectionID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE connections SET last_seen=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND generation=? AND enabled=1`, o.ConnectionID, o.Generation); err != nil {
			return receipt, err
		}
	}
	return receipt, nil
}

func (s *Store) Usage(ctx context.Context, memory string) (records.UsageInfo, error) {
	var dataset string
	if err := s.DB.QueryRowContext(ctx, `SELECT dataset FROM memories WHERE id=?`, memory).Scan(&dataset); err != nil {
		return records.UsageInfo{}, err
	}
	return s.usageForDataset(ctx, memory, dataset)
}

func (s *Store) UsageForVersion(ctx context.Context, v Version) (records.UsageInfo, error) {
	var valid int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE id=? AND dataset=? AND current_revision=?`, v.Memory, v.Dataset, v.Revision).Scan(&valid); err != nil {
		return records.UsageInfo{}, err
	}
	if valid != 1 {
		return records.UsageInfo{}, access("version_changed", "version or scope changed while reading usage")
	}
	return s.usageForDataset(ctx, v.Memory, v.Dataset)
}

func (s *Store) usageForDataset(ctx context.Context, memory, dataset string) (records.UsageInfo, error) {
	var out records.UsageInfo
	out.AdoptionMeaning = "explicit Agent report only; unsupported older tools and unreported use are unknown"
	out.WindowDays = 30
	out.RetentionDays = 90
	if s.Schema < 10 {
		return out, access("usage_unavailable", "usage collection unavailable")
	}

	if err := s.DB.QueryRowContext(ctx, `SELECT started_ms FROM usage_coverage WHERE id=1`).Scan(&out.StartedMS); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT important FROM memory_importance WHERE memory=?),0)`, memory).Scan(&out.Important); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT channel,kind,count(*) FROM usage_events WHERE memory=? AND dataset=? AND observed_ms>=? GROUP BY channel,kind`, memory, dataset, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch, k string
		var n int64
		if err = rows.Scan(&ch, &k, &n); err != nil {
			return out, err
		}
		var c *records.UsageCounts
		switch ch {
		case "daily":
			c = &out.Daily
		case "app":
			c = &out.App
		case "verification":
			c = &out.Verification
		case "background":
			c = &out.Background
		}
		if c == nil {
			continue
		}
		switch k {
		case "read":
			c.Reads = n
		case "return":
			c.Returns = n
		case "adopt":
			c.Adoptions = n
		case "browse":
			c.Browses = n
		}
	}
	return out, rows.Err()
}

func (s *Store) SetImportant(ctx context.Context, r records.ImportantRequest) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE id=? AND current_revision=? AND state='active'`, r.Memory, r.ExpectedRevision).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return records.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_importance(memory,important) VALUES(?,?) ON CONFLICT(memory) DO UPDATE SET important=excluded.important`, r.Memory, r.Important); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneUsage retains compact replay receipts and only removes expired events.
func (s *Store) PruneUsage(ctx context.Context) error {
	if s.Schema < 10 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM usage_events WHERE observed_ms<?`, time.Now().Add(-90*24*time.Hour).UnixMilli())
	return err
}

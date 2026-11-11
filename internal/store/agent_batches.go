package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateAgentConnectionBatches(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE agent_connection_batch_head(singleton INTEGER PRIMARY KEY CHECK(singleton=1),identity TEXT NOT NULL); INSERT INTO agent_connection_batch_head VALUES(1,?); CREATE TABLE agent_connection_batches(id TEXT PRIMARY KEY,operation TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,revision INTEGER NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL); PRAGMA user_version=24;`, uuid.NewString())
	return err
}

func (s *Store) AgentConnectionBatchIdentity(ctx context.Context) (string, error) {
	var identity string
	err := s.DB.QueryRowContext(ctx, `SELECT identity FROM agent_connection_batch_head WHERE singleton=1`).Scan(&identity)
	return identity, err
}

func (s *Store) AgentConnectionBatchByOperation(ctx context.Context, operation string) (records.AgentConnectionBatch, error) {
	return scanAgentConnectionBatch(s.DB.QueryRowContext(ctx, `SELECT payload FROM agent_connection_batches WHERE operation=?`, operation))
}

func scanAgentConnectionBatch(row scanner) (records.AgentConnectionBatch, error) {
	var out records.AgentConnectionBatch
	var data string
	err := row.Scan(&data)
	if err == nil {
		err = json.Unmarshal([]byte(data), &out)
	}
	return out, err
}

func (s *Store) AgentConnectionBatch(ctx context.Context, id string) (records.AgentConnectionBatch, error) {
	if id == "" {
		return scanAgentConnectionBatch(s.DB.QueryRowContext(ctx, `SELECT payload FROM agent_connection_batches ORDER BY CASE WHEN state IN ('running','waiting','cancelling') THEN 0 ELSE 1 END,updated_at DESC,id DESC LIMIT 1`))
	}
	return scanAgentConnectionBatch(s.DB.QueryRowContext(ctx, `SELECT payload FROM agent_connection_batches WHERE id=?`, id))
}

func (s *Store) CreateAgentConnectionBatch(ctx context.Context, d records.AgentConnectionBatchStart) (records.AgentConnectionBatch, bool, error) {
	var out records.AgentConnectionBatch
	invalid := func() (records.AgentConnectionBatch, bool, error) {
		return out, false, access("invalid_request", "bounded installation targets and an operation UUID required")
	}
	if _, err := uuid.Parse(d.OperationID); err != nil || len(d.Targets) == 0 || len(d.Targets) > 4 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, target := range d.Targets {
		if (target.Agent != "pi" && target.Agent != "codex" && target.Agent != "traex" && target.Agent != "trae") || seen[target.Agent] || !filepath.IsAbs(target.Path) || !filepath.IsAbs(target.Directory) || len(target.Path) > 4096 || len(target.Directory) > 4096 {
			return invalid()
		}
		seen[target.Agent] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	digest := records.MergeDigest(d)
	var identity string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM agent_connection_batch_head WHERE singleton=1`).Scan(&identity); err != nil {
		return out, false, err
	}
	if d.Identity != identity {
		return out, false, access("restore_reconnect_required", "connection batch identity changed; reload current settings")
	}
	var priorDigest string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM agent_connection_batches WHERE operation=?`, d.OperationID).Scan(&priorDigest)
	if err == nil {
		if digest != priorDigest {
			return out, false, access("conflict", "batch operation already has different targets")
		}
		out, err = scanAgentConnectionBatch(tx.QueryRowContext(ctx, `SELECT payload FROM agent_connection_batches WHERE operation=?`, d.OperationID))
		return out, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, false, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_connection_batches WHERE state IN ('running','waiting','cancelling')`).Scan(&active); err != nil {
		return out, false, err
	}
	if active != 0 {
		return out, false, access("conflict", "finish or cancel the current connection batch first")
	}
	out = records.AgentConnectionBatch{ID: uuid.NewString(), OperationID: d.OperationID, Revision: 1, State: "running", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Actions: map[string]string{}}
	out.Identity = identity
	out.UpdatedAt = out.CreatedAt
	for _, target := range d.Targets {
		out.Items = append(out.Items, records.AgentConnectionBatchItem{Target: target, ConnectionID: uuid.NewString(), State: "queued", Attempt: 1})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return out, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_connection_batches VALUES(?,?,?,?,?,?,?,?)`, out.ID, out.OperationID, digest, out.Revision, out.State, string(data), out.CreatedAt, out.UpdatedAt)
	if err != nil {
		return out, false, err
	}
	return out, true, tx.Commit()
}

// Only the daemon owner calls this CAS; the UI cannot write batch outcomes.
func (s *Store) SaveAgentConnectionBatch(ctx context.Context, b records.AgentConnectionBatch) (records.AgentConnectionBatch, error) {
	previous := b.Revision
	b.Revision++
	b.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE agent_connection_batches SET revision=?,state=?,payload=?,updated_at=? WHERE id=? AND revision=? AND state!='retired' AND (SELECT identity FROM agent_connection_batch_head WHERE singleton=1)=?`, b.Revision, b.State, string(data), b.UpdatedAt, b.ID, previous, b.Identity)
	if err != nil {
		return b, err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		err = access("conflict", "connection batch changed or retired")
	}
	return b, err
}

func (s *Store) InterruptAgentConnectionBatches(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT payload FROM agent_connection_batches WHERE state IN ('running','cancelling')`)
	if err != nil {
		return err
	}
	var pending []records.AgentConnectionBatch
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return err
		}
		var b records.AgentConnectionBatch
		if err = json.Unmarshal([]byte(data), &b); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, b := range pending {
		b.State = "interrupted"
		for index := range b.Items {
			switch b.Items[index].State {
			case "queued", "installing", "verifying", "cancelling":
				b.Items[index].State = "interrupted"
				b.Items[index].Error = "服务已中断；请明确重试或验证连接。"
			}
		}
		if _, err = s.SaveAgentConnectionBatch(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

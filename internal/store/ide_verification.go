package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// This read-only check uses durable tool receipts, never an IDE answer or a
// checkbox. Generation and version evidence share one SQLite read snapshot.
func (s *Store) CheckIDEVerification(ctx context.Context, d records.IDEVerification) (records.IDEVerificationReceipt, error) {
	out := records.IDEVerificationReceipt{State: "waiting", Memory: d.MemoryID(), Evidence: map[string]string{}}
	id, err := uuid.Parse(d.Nonce)
	if err != nil || id.String() != d.Nonce || d.CreatedMS <= 0 || d.CreatedMS > time.Now().UnixMilli() {
		return out, access("invalid_request", "invalid IDE verification challenge")
	}
	if time.Now().UnixMilli()-d.CreatedMS > int64(24*time.Hour/time.Millisecond) {
		out.State = "expired"
		return out, nil
	}
	c, _, err := s.ResolveAgentScopes(ctx, records.AgentRequest{ConnectionID: d.ConnectionID, Generation: d.Generation, Workspace: d.Workspace, Arguments: records.AgentArguments{Scope: ""}})
	if err != nil {
		return out, err
	}
	if c.Agent != "trae" || c.Project == "" || d.Workspace == "" {
		return out, access("invalid_request", "IDE verification requires a bound workspace")
	}
	canonical, err := filepath.EvalSymlinks(d.Workspace)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	current, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, c.ID))
	if err != nil {
		return out, err
	}
	if !current.Enabled || current.Generation != d.Generation {
		return out, access("reload_required", "connection changed during IDE verification")
	}
	v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, out.Memory))
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !records.IDEVerificationRecord(current, v) || v.Dataset != c.Project || v.State != "active" {
		out.State = "changed"
		return out, nil
	}
	out.Revision = v.Revision
	if v.IndexState != "indexed" {
		out.State = v.IndexState
		return out, nil
	}
	var saved int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE id=? AND memory=? AND revision=1`, "agent:"+out.Memory, out.Memory).Scan(&saved); err != nil {
		return out, err
	}
	if saved != 1 {
		out.State = "changed"
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,id FROM usage_events WHERE connection=? AND generation=? AND workspace=? AND dataset=? AND memory=? AND revision=1 AND channel='verification' AND observed_ms>=? ORDER BY observed_ms,id`, c.ID, c.Generation, UsageDigest([]string{c.Project, canonical}), c.Project, out.Memory, d.CreatedMS)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			return out, err
		}
		if kind == "read" || kind == "return" || kind == "status" {
			out.Evidence[kind] = id
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Evidence) == 3 {
		out.Complete = true
		out.State = "verified"
		out.Evidence["save"] = "agent:" + out.Memory
	}
	return out, nil
}

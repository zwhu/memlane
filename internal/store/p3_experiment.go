package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// OpenP3Experiment is an explicit opt-in for disposable experiment databases.
// Normal Open and the production daemon do not create or use these tables.
// This is not a schema-v3 migration or a public consolidation API.
func OpenP3Experiment(path string) (*Store, error) {
	s, err := open(path, false)
	if err != nil {
		return nil, err
	}
	_, err = s.DB.Exec(`
CREATE TABLE IF NOT EXISTS p3_experiment_tasks(task TEXT PRIMARY KEY, attempt INTEGER NOT NULL, snapshot TEXT NOT NULL, cancelled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS p3_experiment_results(proposal TEXT PRIMARY KEY, task TEXT NOT NULL REFERENCES p3_experiment_tasks(task), attempt INTEGER NOT NULL, payload TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS p3_experiment_receipts(id TEXT PRIMARY KEY, proposal TEXT NOT NULL UNIQUE REFERENCES p3_experiment_results(proposal), memory TEXT NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS p3_experiment_sources(memory TEXT NOT NULL, revision INTEGER NOT NULL, source_memory TEXT NOT NULL, source_revision INTEGER NOT NULL, snapshot TEXT NOT NULL, quote TEXT NOT NULL, PRIMARY KEY(memory,revision,source_memory), FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision), FOREIGN KEY(source_memory,source_revision) REFERENCES versions(memory,revision));
`)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// StartP3Experiment freezes exact current versions and captured P2 metadata.
// Metadata is a capture-time observation, not a claim about legacy provenance.
func (s *Store) StartP3Experiment(ctx context.Context, task string, ids []string) (records.P3Snapshot, error) {
	x := records.P3Snapshot{Schema: records.P3ExperimentSchema, Task: task, Attempt: 1}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return x, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		v, e := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, id))
		if e != nil {
			return x, e
		}
		x.Inputs = append(x.Inputs, v)
		x.Dataset = v.Dataset
	}
	if err = x.Seal(); err != nil {
		return x, err
	}
	b, err := json.Marshal(x)
	if err != nil {
		return x, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO p3_experiment_tasks(task,attempt,snapshot) VALUES(?,?,?)`, task, x.Attempt, string(b))
	if err != nil {
		return x, err
	}
	return x, tx.Commit()
}

func p3Task(ctx context.Context, tx *sql.Tx, task string, attempt int64) (records.P3Snapshot, error) {
	var x records.P3Snapshot
	var raw string
	var fence int64
	var cancelled bool
	if err := tx.QueryRowContext(ctx, `SELECT attempt,snapshot,cancelled FROM p3_experiment_tasks WHERE task=?`, task).Scan(&fence, &raw, &cancelled); err != nil {
		return x, err
	}
	if cancelled || fence != attempt {
		return x, records.ErrP3Cancelled
	}
	return x, json.Unmarshal([]byte(raw), &x)
}

// Stage is a result receipt only: it cannot change knowledge. Host IDs, attempt
// and cancellation are checked inside the same transaction as the receipt.
func (s *Store) StageP3Experiment(ctx context.Context, task string, attempt int64, raw []byte) (records.P3Proposal, error) {
	var p records.P3Proposal
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	x, err := p3Task(ctx, tx, task, attempt)
	if err != nil {
		return p, err
	}
	p, err = x.Decode(raw)
	if err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM p3_experiment_results WHERE proposal=?`, p.ID).Scan(&existing)
	if err == nil {
		if existing != string(b) {
			return p, ErrConflict
		}
		return p, nil
	}
	if err != sql.ErrNoRows {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO p3_experiment_results(proposal,task,attempt,payload) VALUES(?,?,?,?)`, p.ID, task, attempt, string(b))
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func (s *Store) CancelP3Experiment(ctx context.Context, task string, attempt int64) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE p3_experiment_tasks SET cancelled=1 WHERE task=? AND attempt=?`, task, attempt)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return records.ErrP3Cancelled
	}
	return err
}

type P3Receipt struct {
	ID       string `json:"receipt_id"`
	Proposal string `json:"proposal_id"`
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
}

// Apply checks every source revision and active state before making any change.
// It commits a new merge, two archival revisions, immutable source snapshots,
// history, an application receipt and the existing index job atomically.
func (s *Store) ApplyP3Experiment(ctx context.Context, proposal, receipt string, confirmed bool) (P3Receipt, error) {
	out := P3Receipt{}
	if !confirmed || len(receipt) == 0 || len(receipt) > 128 {
		return out, errors.New("explicit experiment confirmation and receipt required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// A retry returns a durable fact even if cancellation happened afterwards.
	var receiptProposal string
	err = tx.QueryRowContext(ctx, `SELECT proposal FROM p3_experiment_receipts WHERE id=?`, receipt).Scan(&receiptProposal)
	if err == nil && receiptProposal != proposal {
		return out, ErrConflict
	}
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,proposal,memory,revision FROM p3_experiment_receipts WHERE proposal=?`, proposal).Scan(&out.ID, &out.Proposal, &out.Memory, &out.Revision)
	if err == nil {
		if out.Proposal != proposal {
			return P3Receipt{}, ErrConflict
		}
		return out, nil
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	var task, raw, state string
	var attempt int64
	if err = tx.QueryRowContext(ctx, `SELECT task,attempt,payload,state FROM p3_experiment_results WHERE proposal=?`, proposal).Scan(&task, &attempt, &raw, &state); err != nil {
		return out, err
	}
	if state == "expired" {
		return out, records.ErrP3Expired
	}
	x, err := p3Task(ctx, tx, task, attempt)
	if err != nil {
		return out, err
	}
	p, err := x.Decode([]byte(raw))
	if err != nil {
		return out, err
	}
	for _, v := range x.Inputs {
		var rev int64
		var dataset, currentState string
		if err = tx.QueryRowContext(ctx, `SELECT current_revision,dataset,state FROM memories WHERE id=?`, v.Memory).Scan(&rev, &dataset, &currentState); err != nil {
			return out, err
		}
		if rev != v.Revision || dataset != x.Dataset || currentState != "active" {
			if _, err = tx.ExecContext(ctx, `UPDATE p3_experiment_results SET state='expired' WHERE proposal=?`, proposal); err != nil {
				return out, err
			}
			if err = tx.Commit(); err != nil {
				return out, err
			}
			return out, records.ErrP3Expired
		}
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("p3-merge:"+proposal)).String()
	v, err := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "p3-merge:" + proposal, Dataset: x.Dataset, Memory: id, Title: p.Title, Text: p.Text, Source: "P3 实验：已确认合并", Origin: "knowledge"})
	if err != nil {
		return out, err
	}
	for i, source := range x.Inputs {
		b, _ := json.Marshal(source)
		if _, err = tx.ExecContext(ctx, `INSERT INTO p3_experiment_sources(memory,revision,source_memory,source_revision,snapshot,quote) VALUES(?,?,?,?,?,?)`, id, v.Revision, source.Memory, source.Revision, string(b), p.Sources[i].Quote); err != nil {
			return out, err
		}
		// Archive retains the original and adds history; it is not deletion and
		// creates no new activity or index job for the inactive version.
		if _, err = tx.ExecContext(ctx, `INSERT INTO versions(memory,revision,text,operation,title) VALUES(?,?,?,'p3_merge_archive',?)`, source.Memory, source.Revision+1, source.Text, source.Title); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memories SET current_revision=?,state='archived',indexed_revision=0,generation='' WHERE id=?`, source.Revision+1, source.Memory); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE memory=? AND state IN ('pending','failed','indexing')`, source.Memory); err != nil {
			return out, err
		}
	}
	out = P3Receipt{ID: receipt, Proposal: proposal, Memory: id, Revision: v.Revision}
	if _, err = tx.ExecContext(ctx, `INSERT INTO p3_experiment_receipts(id,proposal,memory,revision) VALUES(?,?,?,?)`, receipt, proposal, id, v.Revision); err != nil {
		return P3Receipt{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE p3_experiment_results SET state='applied' WHERE proposal=?`, proposal); err != nil {
		return P3Receipt{}, err
	}
	return out, tx.Commit()
}

// Package store owns immutable original versions and current index bindings.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("revision or idempotency conflict")

type Store struct{ DB *sql.DB }

type Mutation = records.Mutation
type Version = records.Version

func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Require an explicit absolute path; never use Cognee's cwd database default.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS memories(id TEXT PRIMARY KEY, dataset TEXT NOT NULL, current_revision INTEGER NOT NULL, indexed_revision INTEGER NOT NULL DEFAULT 0, generation TEXT NOT NULL DEFAULT '', state TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS versions(memory TEXT NOT NULL REFERENCES memories(id), revision INTEGER NOT NULL, text TEXT NOT NULL, operation TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(memory,revision));
CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY, digest TEXT NOT NULL, memory TEXT NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS jobs(memory TEXT NOT NULL, revision INTEGER NOT NULL, state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(memory,revision));
CREATE TABLE IF NOT EXISTS index_bindings(chunk_id TEXT NOT NULL, memory TEXT NOT NULL, revision INTEGER NOT NULL, generation TEXT NOT NULL, PRIMARY KEY(chunk_id,generation));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Current(ctx context.Context, id string) (Version, error) {
	var v Version
	err := s.DB.QueryRowContext(ctx, `SELECT m.id,m.dataset,m.current_revision,v.text,m.state,v.operation FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision WHERE m.id=?`, id).Scan(&v.Memory, &v.Dataset, &v.Revision, &v.Text, &v.State, &v.Operation)
	return v, err
}

func (s *Store) Apply(ctx context.Context, m Mutation) (Version, error) {
	id, err := uuid.Parse(m.Memory)
	if err != nil {
		return Version{}, errors.New("invalid memory UUID")
	}
	datasetID, err := uuid.Parse(m.Dataset)
	if err != nil {
		return Version{}, errors.New("invalid dataset UUID")
	}
	m.Memory, m.Dataset = id.String(), datasetID.String()
	if m.OperationID == "" || m.ExpectedRevision < 0 {
		return Version{}, errors.New("invalid operation")
	}
	switch m.Kind {
	case "save", "edit", "delete", "restore":
	default:
		return Version{}, errors.New("unsupported user mutation")
	}
	if (m.Kind == "save" || m.Kind == "edit") && strings.TrimSpace(m.Text) == "" {
		return Version{}, errors.New("empty text")
	}
	payload, _ := json.Marshal(m)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback()
	var existing string
	var revision int64
	var memory string
	err = tx.QueryRowContext(ctx, `SELECT digest,memory,revision FROM operations WHERE id=?`, m.OperationID).Scan(&existing, &memory, &revision)
	if err == nil {
		if existing != digest {
			return Version{}, ErrConflict
		}
		var v Version
		v.Memory = memory
		v.Revision = revision
		err = tx.QueryRowContext(ctx, `SELECT m.dataset,v.text,v.operation FROM versions v JOIN memories m ON m.id=v.memory WHERE v.memory=? AND v.revision=?`, memory, revision).Scan(&v.Dataset, &v.Text, &v.Operation)
		v.State = "accepted"
		return v, err
	}
	if err != sql.ErrNoRows {
		return Version{}, err
	}
	var dataset, state, text string
	err = tx.QueryRowContext(ctx, `SELECT dataset,current_revision,state FROM memories WHERE id=?`, m.Memory).Scan(&dataset, &revision, &state)
	if err == sql.ErrNoRows {
		if m.Kind != "save" || m.ExpectedRevision != 0 {
			return Version{}, ErrConflict
		}
		revision = 0
		dataset = m.Dataset
		_, err = tx.ExecContext(ctx, `INSERT INTO memories(id,dataset,current_revision,state) VALUES(?,?,0,'active')`, m.Memory, m.Dataset)
	} else if err == nil {
		if m.Kind == "save" || dataset != m.Dataset || revision != m.ExpectedRevision {
			return Version{}, ErrConflict
		}
		if state == "deleted" && m.Kind != "restore" {
			return Version{}, ErrConflict
		}
	}
	if err != nil {
		return Version{}, err
	}
	text = m.Text
	if m.Kind == "delete" {
		err = tx.QueryRowContext(ctx, `SELECT text FROM versions WHERE memory=? AND revision=?`, m.Memory, revision).Scan(&text)
	} else if m.Kind == "restore" {
		if m.RestoreRevision <= 0 || m.RestoreRevision > revision {
			return Version{}, ErrConflict
		}
		err = tx.QueryRowContext(ctx, `SELECT text FROM versions WHERE memory=? AND revision=?`, m.Memory, m.RestoreRevision).Scan(&text)
	}
	if err != nil {
		return Version{}, err
	}
	revision++
	state = "active"
	if m.Kind == "delete" {
		state = "deleted"
	}
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO versions(memory,revision,text,operation) VALUES(?,?,?,?)`, []any{m.Memory, revision, text, m.Kind}},
		{`UPDATE memories SET current_revision=?,state=?,indexed_revision=0,generation='' WHERE id=?`, []any{revision, state, m.Memory}},
		{`INSERT INTO operations(id,digest,memory,revision) VALUES(?,?,?,?)`, []any{m.OperationID, digest, m.Memory, revision}},
		{`UPDATE jobs SET state='superseded' WHERE memory=? AND state IN ('pending','failed')`, []any{m.Memory}},
	}
	if state == "active" {
		queries = append(queries, struct {
			query string
			args  []any
		}{`INSERT INTO jobs(memory,revision,state) VALUES(?,?,'pending')`, []any{m.Memory, revision}})
	}
	for _, q := range queries {
		if _, err = tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return Version{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Version{}, err
	}
	return Version{Record: engine.Record{Dataset: dataset, Memory: m.Memory, Revision: revision, Text: text}, State: state, Operation: m.Kind}, nil
}

func (s *Store) MarkIndexed(ctx context.Context, r engine.Record, receipt engine.Receipt) error {
	if receipt.Generation == "" || len(receipt.ChunkIDs) == 0 {
		return errors.New("invalid index receipt")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE memories SET indexed_revision=?,generation=? WHERE id=? AND dataset=? AND current_revision=? AND state='active'`, r.Revision, receipt.Generation, r.Memory, r.Dataset, r.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	for _, id := range receipt.ChunkIDs {
		if _, err = uuid.Parse(id); err != nil {
			return errors.New("invalid chunk UUID")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO index_bindings(chunk_id,memory,revision,generation) VALUES(?,?,?,?) ON CONFLICT(chunk_id,generation) DO UPDATE SET memory=excluded.memory,revision=excluded.revision`, id, r.Memory, r.Revision, receipt.Generation)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET state='completed',error='' WHERE memory=? AND revision=?`, r.Memory, r.Revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IndexFailed(ctx context.Context, r engine.Record, cause error) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state='failed',error=? WHERE memory=? AND revision=? AND state!='superseded'`, fmt.Sprintf("%T", cause), r.Memory, r.Revision)
	return err
}

// Resolve only candidates whose complete provenance binding is current. The
// business store supplies the original text; engine payload alone is insufficient.
func (s *Store) Resolve(ctx context.Context, c engine.Candidate) (Version, bool, error) {
	var v Version
	err := s.DB.QueryRowContext(ctx, `SELECT m.id,m.dataset,v.revision,v.text,m.state,v.operation FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN index_bindings b ON b.memory=m.id AND b.revision=v.revision WHERE b.chunk_id=? AND b.generation=? AND m.id=? AND m.dataset=? AND v.revision=? AND m.indexed_revision=v.revision AND m.generation=b.generation AND m.state='active'`, c.ChunkID, c.Generation, c.Memory, c.Dataset, c.Revision).Scan(&v.Memory, &v.Dataset, &v.Revision, &v.Text, &v.State, &v.Operation)
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if c.StartByte < 0 || c.EndByte <= c.StartByte || c.EndByte > len(v.Text) || v.Text[c.StartByte:c.EndByte] != c.Text {
		return v, false, nil
	}
	return v, true, nil
}

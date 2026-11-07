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
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	_ "modernc.org/sqlite"
)

var ErrConflict = records.ErrConflict

type Store struct {
	ServiceStopGeneration int64 // fixed for this service epoch, never model data
	DB                    *sql.DB
	ownership             *os.File
	Schema                int
}

type Mutation = records.Mutation
type Version = records.Version

func Open(path string) (*Store, error) { return open(path, true) }

func open(path string, formal bool) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	ownership, err := os.OpenFile(path+".owner.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(ownership.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		ownership.Close()
		return nil, errors.New("database already owned")
	}
	opened := false
	defer func() {
		if !opened {
			ownership.Close()
		}
	}()

	// Require an explicit absolute path; never use Cognee's cwd database default.
	priorFile, statErr := os.Stat(path)
	hadDatabase := statErr == nil && priorFile.Size() > 0
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var prior int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&prior); err != nil {
		db.Close()
		return nil, err
	}
	if prior > SchemaVersion || (!formal && prior > 2) {
		db.Close()
		return nil, errors.New("database schema is newer than this client")
	}
	// VACUUM INTO includes committed WAL pages. The exclusive database owner
	// creates this snapshot before any schema change and aborts on backup failure.
	if formal && prior < SchemaVersion {
		var experiments int
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'p3_experiment_%'`).Scan(&experiments); err != nil || experiments > 0 {
			db.Close()
			return nil, errors.New("experiment database requires explicit experiment entrypoint")
		}
		if hadDatabase {
			backups := filepath.Join(filepath.Dir(path), "backups")
			if err = os.MkdirAll(backups, 0700); err == nil {
				backup := filepath.Join(backups, fmt.Sprintf("schema-v%d-%s.sqlite", prior, time.Now().UTC().Format("20060102T150405.000000000")))
				_, err = db.Exec(`VACUUM INTO ?`, backup)
				if err == nil {
					err = os.Chmod(backup, 0600)
					if err == nil {
						var f *os.File
						f, err = os.Open(backup)
						if err == nil {
							err = f.Sync()
							f.Close()
						}
					}
					if err == nil {
						var f *os.File
						f, err = os.Open(backups)
						if err == nil {
							err = f.Sync()
							f.Close()
						}
					}
				}
			}
			if err != nil {
				db.Close()
				return nil, err
			}
		}
	}
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
	if err := migrate(db, formal); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	opened = true
	var schema int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&schema); err != nil {
		db.Close()
		opened = false
		return nil, err
	}
	return &Store{DB: db, ownership: ownership, Schema: schema}, nil
}

func (s *Store) Close() error {
	err := s.DB.Close()
	if s.ownership != nil {
		s.ownership.Close()
	}
	return err
}

func (s *Store) Current(ctx context.Context, id string) (Version, error) {
	v, err := scanVersion(s.DB.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, id))
	if err == nil {
		err = s.enrichVersions(ctx, []*records.Version{&v})
	}
	if err == nil && s.Schema >= 10 {
		u, e := s.UsageForVersion(ctx, v)
		err = e
		if e == nil {
			v.Usage = &u
		}
	}
	return v, err
}

func (s *Store) Apply(ctx context.Context, m Mutation) (Version, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback()
	v, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return Version{}, err
	}
	if err = tx.Commit(); err != nil {
		return Version{}, err
	}
	if err = s.enrichVersions(ctx, []*records.Version{&v}); err != nil {
		return v, err
	}
	return v, nil
}

// applyTx lets a larger business operation share the existing version, metadata
// and durable-index invariants without opening nested transactions.
func (s *Store) applyTx(ctx context.Context, tx *sql.Tx, m Mutation) (Version, error) {
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
	if m.Origin != "" && m.Origin != "knowledge" && m.Origin != "imported" && m.Origin != "raw" {
		return Version{}, errors.New("invalid memory origin")
	}
	if len(m.Title) > 512 || len(m.Source) > 512 || len(m.OperationID) > 128 {
		return Version{}, errors.New("memory metadata too large")
	}
	if detail, err := json.Marshal(m.SourceDetail); err != nil || len(detail) > 65536 {
		return Version{}, errors.New("source detail too large")
	}
	payload, _ := json.Marshal(m)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
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
		// P0 apply accepted explicit UUID datasets. Adopt unknown ones without
		// changing that contract; only App project registration supplies a path.
		if _, err = tx.ExecContext(ctx, `INSERT INTO scopes(id,kind,name) VALUES(?,'legacy',?) ON CONFLICT(id) DO NOTHING`, m.Dataset, "未登记项目 "+m.Dataset[:8]); err != nil {
			return Version{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO memories(id,dataset,current_revision,state) VALUES(?,?,0,'active')`, m.Memory, m.Dataset)
	} else if err == nil {
		if m.Kind == "save" || dataset != m.Dataset || revision != m.ExpectedRevision {
			return Version{}, ErrConflict
		}
		if (state == "deleted" || state == "archived") && m.Kind != "restore" {
			return Version{}, ErrConflict
		}
	}
	if err != nil {
		return Version{}, err
	}
	text = m.Text
	title := titleFor(m.Title, m.Text)
	if m.Kind == "edit" && m.Title == "" {
		if err = tx.QueryRowContext(ctx, `SELECT title FROM versions WHERE memory=? AND revision=?`, m.Memory, revision).Scan(&title); err != nil {
			return Version{}, err
		}
	}
	if m.Kind == "delete" {
		err = tx.QueryRowContext(ctx, `SELECT text,title FROM versions WHERE memory=? AND revision=?`, m.Memory, revision).Scan(&text, &title)
	} else if m.Kind == "restore" {
		if m.RestoreRevision <= 0 || m.RestoreRevision > revision {
			return Version{}, ErrConflict
		}
		err = tx.QueryRowContext(ctx, `SELECT text,title FROM versions WHERE memory=? AND revision=?`, m.Memory, m.RestoreRevision).Scan(&text, &title)
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
		{`INSERT INTO versions(memory,revision,text,operation,title) VALUES(?,?,?,?,?)`, []any{m.Memory, revision, text, m.Kind, title}},
		{`UPDATE memories SET current_revision=?,state=?,indexed_revision=0,generation='' WHERE id=?`, []any{revision, state, m.Memory}},
		{`INSERT INTO operations(id,digest,memory,revision) VALUES(?,?,?,?)`, []any{m.OperationID, digest, m.Memory, revision}},
		{`UPDATE jobs SET state='superseded' WHERE memory=? AND state IN ('pending','failed','indexing')`, []any{m.Memory}},
	}
	if state == "active" {
		queries = append(queries, struct {
			query string
			args  []any
		}{`INSERT INTO jobs(memory,revision,state) VALUES(?,?,'pending')`, []any{m.Memory, revision}})
	}
	if revision == 1 {
		origin, source := m.Origin, m.Source
		if origin == "" {
			origin = "knowledge"
		}
		if source == "" {
			source = "手动新增"
		}
		detail, _ := json.Marshal(m.SourceDetail)
		if _, err = tx.ExecContext(ctx, `INSERT INTO memory_metadata(memory,source,origin,created_at,source_detail) VALUES(?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'),?)`, m.Memory, source, origin, string(detail)); err != nil {
			return Version{}, err
		}
	}
	for _, q := range queries {
		if _, err = tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return Version{}, err
		}
	}
	if m.Kind == "edit" || m.Kind == "restore" {
		if err = s.followExperienceVersion(ctx, tx, m, revision); err != nil {
			return Version{}, err
		}
		if err = s.followOrganizationVersion(ctx, tx, m.Memory, revision); err != nil {
			return Version{}, err
		}
	}
	saved, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, m.Memory))
	if err != nil {
		return Version{}, err
	}
	return saved, nil
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
	if s.Schema >= 4 {
		var stale int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=? AND revision=? AND state='stale'`, r.Memory, r.Revision).Scan(&stale); err != nil {
			return err
		}
		if stale > 0 {
			return ErrConflict
		}
	}
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
	_, err = tx.ExecContext(ctx, `INSERT INTO activities(memory,kind,occurred_at) SELECT memory,CASE origin WHEN 'imported' THEN 'imported' WHEN 'raw' THEN 'raw' ELSE 'created' END,strftime('%Y-%m-%dT%H:%M:%fZ','now') FROM memory_metadata WHERE memory=? ON CONFLICT(memory) DO NOTHING`, r.Memory)
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
	v, err := scanVersion(s.DB.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+` JOIN index_bindings b ON b.memory=m.id AND b.revision=v.revision WHERE b.chunk_id=? AND b.generation=? AND m.id=? AND m.dataset=? AND v.revision=? AND m.indexed_revision=v.revision AND m.generation=b.generation AND m.state='active'`, c.ChunkID, c.Generation, c.Memory, c.Dataset, c.Revision))
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if c.StartByte < 0 || c.EndByte <= c.StartByte || c.EndByte > len(v.Text) || v.Text[c.StartByte:c.EndByte] != c.Text {
		return v, false, nil
	}
	if err = s.enrichVersions(ctx, []*records.Version{&v}); err != nil {
		return v, false, err
	}
	if v.Derived != nil && v.Derived.State == "stale" {
		return v, false, nil
	}
	return v, true, nil
}

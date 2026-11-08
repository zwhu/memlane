package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	sqlite "modernc.org/sqlite"
)

type snapshotSource interface {
	NewBackup(string) (*sqlite.Backup, error)
}
type snapshotSerializer interface{ Serialize() ([]byte, error) }

func scalar(conn driver.Conn, query string) (driver.Value, error) {
	stmt, err := conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(nil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]driver.Value, len(rows.Columns()))
	if len(values) != 1 {
		return nil, errors.New("unexpected snapshot validation result")
	}
	if err = rows.Next(values); err != nil {
		return nil, err
	}
	return values[0], nil
}

func (s *Store) Backup(ctx context.Context, root, id string) (records.BackupReceipt, error) {
	return (recoveryfiles.Manager{Root: root}).Create(ctx, id, s.snapshot)
}
func (s *Store) snapshot(ctx context.Context) (int, []byte, error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	var pageSize, pageCount int64
	if err = conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, nil, err
	}
	if err = conn.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, nil, err
	}
	if pageSize <= 0 || pageCount > recoveryfiles.MaxSnapshotBytes/pageSize {
		return 0, nil, errors.New("online backup exceeds 64 MiB; use a separately validated offline backup")
	}
	var schema int
	var content []byte
	err = conn.Raw(func(raw any) error {
		source, ok := raw.(snapshotSource)
		if !ok {
			return errors.New("SQLite online backup is unavailable")
		}
		backup, err := source.NewBackup(":memory:")
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, e := backup.Step(64)
			if e != nil {
				return e
			}
			if int64(backup.PageCount()) > recoveryfiles.MaxSnapshotBytes/pageSize {
				return errors.New("online backup exceeds 64 MiB; use a separately validated offline backup")
			}
			if !more {
				break
			}
		}
		target, e := backup.Commit()
		finished = true
		if e != nil {
			return e
		}
		defer target.Close()
		integrity, e := scalar(target, `PRAGMA integrity_check`)
		if e != nil {
			return e
		}
		if integrity != "ok" {
			return errors.New("snapshot integrity check failed")
		}
		stmt, e := target.Prepare(`PRAGMA foreign_key_check`)
		if e != nil {
			return e
		}
		rows, e := stmt.Query(nil)
		if e != nil {
			stmt.Close()
			return e
		}
		e = rows.Next(make([]driver.Value, len(rows.Columns())))
		rows.Close()
		stmt.Close()
		if e != io.EOF {
			return errors.New("snapshot foreign key check failed")
		}
		version, e := scalar(target, `PRAGMA user_version`)
		if e != nil {
			return e
		}
		number, ok := version.(int64)
		if !ok || number < 0 || number > int64(SchemaVersion) {
			return errors.New("snapshot schema unsupported")
		}
		schema = int(number)
		serializer, ok := target.(snapshotSerializer)
		if !ok {
			return errors.New("snapshot serialization unavailable")
		}
		content, e = serializer.Serialize()
		if e != nil {
			return e
		}
		if int64(len(content)) > recoveryfiles.MaxSnapshotBytes {
			return fmt.Errorf("online backup exceeds 64 MiB")
		}
		return ctx.Err()
	})
	return schema, content, err
}

package domain

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateStoreSchema(ctx context.Context, tx *sql.Tx, version int) error {
	if version < 3 {
		for _, table := range []struct {
			name    string
			columns []string
		}{
			{"projects", []string{"auto_capture_reason", "auto_capture_actor"}},
			{"capture_inbox", []string{"last_error_at"}},
		} {
			existing, err := queryList(ctx, tx, func(row scanner) (string, error) {
				var name string
				var cid, required, primaryKey int
				var fieldType string
				var defaultValue sql.NullString
				err := row.Scan(&cid, &name, &fieldType, &required, &defaultValue, &primaryKey)
				return name, err
			}, "PRAGMA table_info("+table.name+")")
			if err != nil {
				return fmt.Errorf("inspect %s columns: %w", table.name, err)
			}
			present := make(map[string]bool, len(existing))
			for _, column := range existing {
				present[column] = true
			}
			for _, column := range table.columns {
				if present[column] {
					continue
				}
				if err := execSQL(ctx, tx, "ALTER TABLE "+table.name+" ADD COLUMN "+column+" TEXT NOT NULL DEFAULT ''"); err != nil {
					return fmt.Errorf("migrate %s.%s: %w", table.name, column, err)
				}
			}
		}
	}
	if version < 4 {
		if err := execSQL(ctx, tx, sessionRelationsDDL); err != nil {
			return fmt.Errorf("migrate session_relations: %w", err)
		}
	}
	if version < 5 {
		if err := execSQL(ctx, tx, sessionFlushDDL); err != nil {
			return fmt.Errorf("migrate session_flush_jobs: %w", err)
		}
		if err := execSQL(ctx, tx, captureInboxV5IndexesDDL); err != nil {
			return fmt.Errorf("migrate capture_inbox indexes: %w", err)
		}
	}
	return nil
}

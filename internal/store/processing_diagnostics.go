package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zwhu/memlane/internal/records"
)

func migrateProcessingDiagnostics(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE processing_validation_diagnostics(validation TEXT PRIMARY KEY REFERENCES processing_validations(id),stage TEXT NOT NULL,resolve_code TEXT NOT NULL DEFAULT '',changed_names TEXT NOT NULL DEFAULT '[]');PRAGMA user_version=26;`)
	return err
}

func validProcessingDiagnostics(d records.ProcessingValidationDiagnostics) bool {
	switch d.Stage {
	case "queue", "preflight", "model", "postflight":
	default:
		return false
	}
	if len(d.ResolveCode) > 128 {
		return false
	}
	for _, r := range d.ResolveCode {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	if len(d.ChangedNames) > 14 {
		return false
	}
	seen := map[string]bool{}
	for _, name := range d.ChangedNames {
		switch name {
		case "adapter_protocol", "processing_configuration", "agent_version", "agent_executable", "agent_configuration_directory", "helper_executable", "extension", "connection_verification", "settings.json", "models.json", "auth.json", "independent_credential", "gateway_model", "config.toml":
		default:
			return false
		}
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}
func saveProcessingDiagnosticsTx(ctx context.Context, tx *sql.Tx, id string, d records.ProcessingValidationDiagnostics) error {
	names, err := json.Marshal(d.ChangedNames)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_validation_diagnostics(validation,stage,resolve_code,changed_names) VALUES(?,?,?,?)`, id, d.Stage, d.ResolveCode, string(names))
	return err
}
func processingDiagnosticsTx(ctx context.Context, tx *sql.Tx, id string) (*records.ProcessingValidationDiagnostics, error) {
	var d records.ProcessingValidationDiagnostics
	var names string
	err := tx.QueryRowContext(ctx, `SELECT stage,resolve_code,changed_names FROM processing_validation_diagnostics WHERE validation=?`, id).Scan(&d.Stage, &d.ResolveCode, &names)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(names), &d.ChangedNames); err != nil {
		return nil, err
	}
	if !validProcessingDiagnostics(d) {
		return nil, records.MergeError("invalid_configuration")
	}
	return &d, nil
}

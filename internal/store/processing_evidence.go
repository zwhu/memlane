package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

func migrateProcessingEvidence(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE processing_validation_evidence(validation TEXT PRIMARY KEY REFERENCES processing_validations(id),evidence TEXT NOT NULL); PRAGMA user_version=31;`)
	return err
}
func validCapabilityEvidence(e records.ProcessingCapabilityEvidence, v records.ProcessingValidation) bool {
	if e.ValidationID != v.ID || e.Revision != v.Revision || e.Attempt != v.Attempt || e.DependencyDigest != v.DependencyDigest {
		return false
	}
	switch e.Agent {
	case "pi":
		if e.IsolationPolicy != "pi_private_no_session_no_tools_v1" {
			return false
		}
	case "codex":
		if e.IsolationPolicy != "codex_private_ephemeral_no_tools_v1" {
			return false
		}
	default:
		return false
	}
	return !e.StructureValid || e.ResponseComplete
}
func saveCapabilityEvidenceTx(ctx context.Context, tx *sql.Tx, e records.ProcessingCapabilityEvidence) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_validation_evidence(validation,evidence) VALUES(?,?)`, e.ValidationID, string(raw))
	return err
}
func capabilityEvidenceTx(ctx context.Context, tx *sql.Tx, v records.ProcessingValidation) (*records.ProcessingCapabilityEvidence, error) {
	// Old backups may not contain the additive table. No evidence is inferred.
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='processing_validation_evidence'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT evidence FROM processing_validation_evidence WHERE validation=?`, v.ID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e records.ProcessingCapabilityEvidence
	if json.Unmarshal([]byte(raw), &e) != nil || !validCapabilityEvidence(e, v) {
		return nil, records.MergeError("invalid_configuration")
	}
	var rawSpec string
	var spec records.ProcessingSpec
	if err = tx.QueryRowContext(ctx, `SELECT spec FROM processing_revisions WHERE id=?`, v.Revision).Scan(&rawSpec); err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(rawSpec), &spec) != nil || spec.Mode != "agent" || spec.Agent != e.Agent {
		return nil, records.MergeError("invalid_configuration")
	}
	return &e, nil
}
func (s *Store) FinishAgentProcessingValidation(ctx context.Context, id string, attempt int64, state, receipt, code string, now time.Time, d records.ProcessingValidationDiagnostics, e *records.ProcessingCapabilityEvidence) error {
	return s.finishProcessingValidation(ctx, id, attempt, state, receipt, code, now, nil, e, d)
}

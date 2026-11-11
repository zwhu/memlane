package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) ProcessingRevision(ctx context.Context, id string) (records.ProcessingRevision, error) {
	var out records.ProcessingRevision
	if !processingUUID(id) || s.Schema < 17 {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = processingRevisionTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) ProcessingValidation(ctx context.Context, id string) (records.ProcessingValidation, error) {
	var out records.ProcessingValidation
	if !processingUUID(id) || s.Schema < 17 {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,revision,attempt,dependency_digest,state,cancel_requested,receipt_digest,error_code,started_ms,finished_ms FROM processing_validations WHERE id=?`, id).Scan(&out.ID, &out.Revision, &out.Attempt, &out.DependencyDigest, &out.State, &out.CancelRequested, &out.ReceiptDigest, &out.ErrorCode, &out.StartedMS, &out.FinishedMS)
	if err != nil {
		return out, err
	}
	if s.Schema >= 26 {
		out.Diagnostics, err = processingDiagnosticsTx(ctx, tx, id)
		if err == nil && s.Schema >= 27 {
			out.HTTP, err = processingHTTPTx(ctx, tx, id)
		}
		if err != nil {
			return out, err
		}
	}
	if s.Schema >= 31 {
		out.Evidence, err = capabilityEvidenceTx(ctx, tx, out)
		if err != nil {
			return out, err
		}
	}
	err = tx.Commit()
	return out, err
}

// Called once by the new exclusive daemon owner, before accepting requests.
// Crash-interrupted probes are never resumed or reported as successful.
func (s *Store) InterruptProcessingValidations(ctx context.Context, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE processing_validations SET state=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE 'failed' END,error_code='owner_interrupted',finished_ms=? WHERE state='validating'`, now.UnixMilli())
	return err
}

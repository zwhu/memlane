package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func processingConnectionTx(ctx context.Context, tx *sql.Tx, spec records.ProcessingSpec) error {
	if spec.Mode != "agent" {
		return nil
	}
	var agent string
	var enabled bool
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT agent,enabled,generation FROM connections WHERE id=?`, spec.Connection).Scan(&agent, &enabled, &generation)
	if err == sql.ErrNoRows || err == nil && (!enabled || agent != spec.Agent || generation != spec.ConnectionGeneration) {
		return records.MergeError("connection_changed")
	}
	return err
}

// BeginProcessingValidation reserves a durable attempt only. A real runner
// must still inspect the dependencies and produce the final sealed receipt.
func (s *Store) BeginProcessingValidation(ctx context.Context, operation, revision, dependency string, now time.Time) (records.ProcessingValidation, error) {
	var out records.ProcessingValidation
	if s.Schema < 17 || !processingUUID(operation) || !processingUUID(revision) || !processingDigest(dependency) {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	digest := "validation:" + records.MergeDigest([]string{revision, dependency})
	if replay, err := processingReplay(ctx, tx, operation, digest, &out); replay || err != nil {
		return out, err
	}
	v, err := processingRevisionTx(ctx, tx, revision)
	if err != nil {
		return out, err
	}
	if err = processingConnectionTx(ctx, tx, v.Spec); err != nil {
		return out, err
	}
	attempt := int64(1)
	if v.Validation != nil {
		attempt = v.Validation.Attempt + 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_validations SET state='superseded',finished_ms=? WHERE revision=? AND state='validating'`, now.UnixMilli(), revision); err != nil {
		return out, err
	}
	// Explicitly revalidating the selected revision retires its current-use
	// authority before starting another probe. A later success cannot silently
	// reactivate it; activation remains a separate, generation-checked action.
	if _, err = tx.ExecContext(ctx, `UPDATE processing_current SET active=0,generation=generation+1,changed_ms=? WHERE id=1 AND active=1 AND revision=?`, now.UnixMilli(), revision); err != nil {
		return out, err
	}
	out = records.ProcessingValidation{ID: uuid.NewString(), Revision: revision, Attempt: attempt, DependencyDigest: dependency, State: "validating", StartedMS: now.UnixMilli()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_validations(id,revision,attempt,dependency_digest,state,started_ms) VALUES(?,?,?,?,?,?)`, out.ID, out.Revision, out.Attempt, out.DependencyDigest, out.State, out.StartedMS); err != nil {
		return out, err
	}
	if err = processingRecord(ctx, tx, operation, digest, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) RequestProcessingValidationCancel(ctx context.Context, id string) error {
	if !processingUUID(id) {
		return records.MergeError("invalid_request")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE processing_validations SET cancel_requested=1 WHERE id=? AND state='validating'`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// FinishProcessingValidation is an internal runner boundary, not an IPC
// action that lets a UI or Agent assert model capability by passing success.
func (s *Store) FinishProcessingValidation(ctx context.Context, id string, attempt int64, state, receipt, code string, now time.Time, diagnostics ...records.ProcessingValidationDiagnostics) error {
	return s.finishProcessingValidation(ctx, id, attempt, state, receipt, code, now, nil, nil, diagnostics...)
}

func (s *Store) FinishHTTPProcessingValidation(ctx context.Context, id string, attempt int64, state, receipt, code string, now time.Time, diagnostic records.ProcessingValidationDiagnostics, http *records.ProcessingHTTPReceipt) error {
	if state == "succeeded" && (http == nil || !http.RequestSent || !http.NativeSchemaRequested || !http.ResponseComplete || !http.SchemaValid || http.LocalCancelled || http.ReportedModel == "") {
		return records.MergeError("invalid_request")
	}
	return s.finishProcessingValidation(ctx, id, attempt, state, receipt, code, now, http, nil, diagnostic)
}

func (s *Store) finishProcessingValidation(ctx context.Context, id string, attempt int64, state, receipt, code string, now time.Time, http *records.ProcessingHTTPReceipt, evidence *records.ProcessingCapabilityEvidence, diagnostics ...records.ProcessingValidationDiagnostics) error {
	if s.Schema < 17 || !processingUUID(id) || attempt < 1 || len(code) > 128 ||
		(state != "succeeded" && state != "failed" && state != "cancelled") ||
		(state == "succeeded" && (!processingDigest(receipt) || code != "")) ||
		(receipt != "" && !processingDigest(receipt)) {
		return records.MergeError("invalid_request")
	}
	if len(diagnostics) > 1 {
		return records.MergeError("invalid_request")
	}
	if len(diagnostics) == 1 && !validProcessingDiagnostics(diagnostics[0]) {
		return records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision string
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM processing_validations WHERE id=?`, id).Scan(&revision); err != nil {
		return err
	}
	latest, err := processingValidationTx(ctx, tx, revision)
	if err != nil {
		return err
	}
	if latest == nil || latest.ID != id || latest.Attempt != attempt || latest.State != "validating" || latest.CancelRequested && state != "cancelled" {
		return ErrConflict
	}
	if evidence != nil {
		if state == "cancelled" || code == "model_timeout" || code == "owner_interrupted" {
			return records.MergeError("invalid_request")
		}
		var rawSpec string
		var spec records.ProcessingSpec
		if err = tx.QueryRowContext(ctx, `SELECT spec FROM processing_revisions WHERE id=?`, revision).Scan(&rawSpec); err != nil {
			return err
		}
		if json.Unmarshal([]byte(rawSpec), &spec) != nil || spec.Mode != "agent" || spec.Agent != evidence.Agent {
			return records.MergeError("invalid_request")
		}
		if s.Schema < 31 || !validCapabilityEvidence(*evidence, *latest) {
			return records.MergeError("invalid_request")
		}
		if state == "succeeded" && (!evidence.ResponseComplete || !evidence.StructureValid || !evidence.Reaped || !evidence.GroupGone) {
			return records.MergeError("invalid_request")
		}
		if err = saveCapabilityEvidenceTx(ctx, tx, *evidence); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_validations SET state=?,receipt_digest=?,error_code=?,finished_ms=? WHERE id=?`, state, receipt, code, now.UnixMilli(), id); err != nil {
		return err
	}
	if http != nil {
		if s.Schema < 27 {
			return records.MergeError("unsupported_schema")
		}
		if err = saveProcessingHTTPTx(ctx, tx, id, *http); err != nil {
			return err
		}
	}
	if s.Schema >= 26 && state != "succeeded" && len(diagnostics) == 1 {
		if err = saveProcessingDiagnosticsTx(ctx, tx, id, diagnostics[0]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func processingVerifiedTx(ctx context.Context, tx *sql.Tx, v records.ProcessingRevision, dependency string) error {
	if v.Validation == nil || v.Validation.State != "succeeded" || v.Validation.CancelRequested || v.Validation.DependencyDigest != dependency || !processingDigest(dependency) {
		return records.MergeError("verification_required")
	}
	return processingConnectionTx(ctx, tx, v.Spec)
}

func (s *Store) ActivateProcessing(ctx context.Context, draft records.ProcessingActivation, dependency string, now time.Time) (records.ProcessingActivationReceipt, error) {
	var out records.ProcessingActivationReceipt
	if s.Schema < 17 || !processingUUID(draft.OperationID) || !processingUUID(draft.Revision) || draft.ExpectedGeneration < 0 || !processingDigest(dependency) {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	digest := "activate:" + records.MergeDigest(draft) + ":" + dependency
	if replay, err := processingReplay(ctx, tx, draft.OperationID, digest, &out); replay || err != nil {
		return out, err
	}
	current, err := processingSelectionTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if current.Generation != draft.ExpectedGeneration {
		return out, ErrConflict
	}
	v, err := processingRevisionTx(ctx, tx, draft.Revision)
	if err != nil {
		return out, err
	}
	var saved sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM processing_saved WHERE mode=?`, v.Spec.Mode).Scan(&saved); err != nil {
		return out, err
	}
	if saved.String != v.ID {
		return out, ErrConflict
	}
	if err = processingVerifiedTx(ctx, tx, v, dependency); err != nil {
		return out, err
	}
	out = records.ProcessingActivationReceipt{Revision: v.ID, Generation: current.Generation, ChangedMS: current.ChangedMS}
	if !current.Active || current.Revision == nil || current.Revision.ID != v.ID {
		out.Generation++
		out.ChangedMS = now.UnixMilli()
		if _, err = tx.ExecContext(ctx, `UPDATE processing_current SET revision=?,active=1,generation=?,changed_ms=? WHERE id=1`, v.ID, out.Generation, out.ChangedMS); err != nil {
			return out, err
		}
	}
	if err = processingRecord(ctx, tx, draft.OperationID, digest, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// freezeProcessingTx is composed with a real task's creation transaction.
// Saving a newer candidate in the same mode does not alter this selection.
func freezeProcessingTx(ctx context.Context, tx *sql.Tx, dependency string, now time.Time) (records.ProcessingSnapshot, error) {
	var out records.ProcessingSnapshot
	current, err := processingSelectionTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if !current.Active || current.Revision == nil {
		return out, records.MergeError("configuration_required")
	}
	if err = processingVerifiedTx(ctx, tx, *current.Revision, dependency); err != nil {
		return out, err
	}
	// JSON copy ensures a caller cannot mutate the shared validation pointer.
	raw, err := json.Marshal(current.Revision)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Revision); err != nil {
		return out, err
	}
	out.SelectionGeneration, out.DependencyDigest, out.FrozenMS = current.Generation, dependency, now.UnixMilli()
	return out, nil
}

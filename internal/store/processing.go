package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func processingUUID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.String() == id
}

func processingDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}

func validateProcessingSpec(spec records.ProcessingSpec) error {
	if len(spec.Provider) > 128 || !utf8.ValidString(spec.Provider) {
		return records.MergeError("invalid_request")
	}
	if strings.TrimSpace(spec.Model) != spec.Model || spec.Model == "" || len(spec.Model) > 256 || !utf8.ValidString(spec.Model) {
		return records.MergeError("invalid_request")
	}
	if spec.Credential == "" && spec.CredentialRevision != 0 || spec.Credential != "" && (!processingUUID(spec.Credential) || spec.CredentialRevision < 1) {
		return records.MergeError("invalid_request")
	}
	if spec.Mode == "agent" {
		if (spec.Agent != "pi" && spec.Agent != "codex") || !processingUUID(spec.Connection) || spec.ConnectionGeneration < 1 || spec.Endpoint != "" || spec.Protocol != "" || spec.Credential != "" {
			return records.MergeError("invalid_request")
		}
		return nil
	}
	if spec.Agent != "" || spec.Connection != "" || spec.ConnectionGeneration != 0 || (spec.Mode != "provider" && spec.Mode != "magpie") {
		return records.MergeError("invalid_request")
	}
	if spec.Protocol != "openai-chat" && spec.Protocol != "openai-responses" && spec.Protocol != "anthropic-messages" {
		return records.MergeError("invalid_request")
	}
	u, err := url.Parse(spec.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(spec.Endpoint) > 2048 {
		return records.MergeError("invalid_request")
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if spec.Mode == "magpie" {
		if !local || u.Scheme != "http" || u.Port() == "" || spec.Credential != "" || spec.Provider != "" {
			return records.MergeError("invalid_request")
		}
	} else if spec.Provider == "" || len(spec.Provider) > 128 || (u.Scheme != "https" && !(u.Scheme == "http" && local)) {
		return records.MergeError("invalid_request")
	}
	return nil
}

func processingReplay(ctx context.Context, tx *sql.Tx, id, digest string, out any) (bool, error) {
	var prior, receipt string
	err := tx.QueryRowContext(ctx, `SELECT digest,receipt FROM processing_operations WHERE id=?`, id).Scan(&prior, &receipt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if prior != digest {
		return true, ErrConflict
	}
	return true, json.Unmarshal([]byte(receipt), out)
}

func processingRecord(ctx context.Context, tx *sql.Tx, id, digest string, receipt any) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_operations(id,digest,receipt) VALUES(?,?,?)`, id, digest, string(raw))
	return err
}

func processingValidationTx(ctx context.Context, tx *sql.Tx, revision string) (*records.ProcessingValidation, error) {
	var v records.ProcessingValidation
	err := tx.QueryRowContext(ctx, `SELECT id,revision,attempt,dependency_digest,state,cancel_requested,receipt_digest,error_code,started_ms,finished_ms FROM processing_validations WHERE revision=? ORDER BY attempt DESC LIMIT 1`, revision).Scan(&v.ID, &v.Revision, &v.Attempt, &v.DependencyDigest, &v.State, &v.CancelRequested, &v.ReceiptDigest, &v.ErrorCode, &v.StartedMS, &v.FinishedMS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &v, err
}

func processingRevisionTx(ctx context.Context, tx *sql.Tx, id string) (records.ProcessingRevision, error) {
	var v records.ProcessingRevision
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,spec,digest,created_ms FROM processing_revisions WHERE id=?`, id).Scan(&v.ID, &raw, &v.Digest, &v.CreatedMS)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v.Spec)
	}
	if err == nil && (validateProcessingSpec(v.Spec) != nil || records.MergeDigest(v.Spec) != v.Digest) {
		err = records.MergeError("invalid_configuration")
	}
	if err == nil {
		v.Validation, err = processingValidationTx(ctx, tx, id)
	}
	return v, err
}

func (s *Store) SaveProcessing(ctx context.Context, draft records.ProcessingSave, now time.Time) (records.ProcessingRevision, error) {
	var out records.ProcessingRevision
	if s.Schema < 17 || !processingUUID(draft.OperationID) || validateProcessingSpec(draft.Spec) != nil {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	digest := "save:" + records.MergeDigest(draft)
	if replay, err := processingReplay(ctx, tx, draft.OperationID, digest, &out); replay || err != nil {
		return out, err
	}
	var previous sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM processing_saved WHERE mode=?`, draft.Spec.Mode).Scan(&previous); err != nil {
		return out, err
	}
	if previous.String != draft.ExpectedSaved {
		return out, ErrConflict
	}
	specDigest := records.MergeDigest(draft.Spec)
	if previous.Valid {
		out, err = processingRevisionTx(ctx, tx, previous.String)
		if err != nil {
			return out, err
		}
	}
	if out.Digest != specDigest {
		out = records.ProcessingRevision{ID: uuid.NewString(), Spec: draft.Spec, Digest: specDigest, CreatedMS: now.UnixMilli()}
		raw, err := json.Marshal(out.Spec)
		if err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO processing_revisions(id,mode,spec,digest,created_ms) VALUES(?,?,?,?,?)`, out.ID, out.Spec.Mode, string(raw), out.Digest, out.CreatedMS); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE processing_saved SET revision=? WHERE mode=?`, out.ID, out.Spec.Mode); err != nil {
			return out, err
		}
	}
	if err = processingRecord(ctx, tx, draft.OperationID, digest, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func processingSelectionTx(ctx context.Context, tx *sql.Tx) (records.ProcessingSelection, error) {
	var out records.ProcessingSelection
	var revision sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT generation,active,revision,changed_ms FROM processing_current WHERE id=1`).Scan(&out.Generation, &out.Active, &revision, &out.ChangedMS)
	if err == nil && revision.Valid {
		v, e := processingRevisionTx(ctx, tx, revision.String)
		err = e
		out.Revision = &v
	}
	return out, err
}

func (s *Store) ProcessingConfiguration(ctx context.Context) (records.ProcessingConfiguration, error) {
	out := records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{}}
	if s.Schema < 17 {
		return out, records.MergeError("unsupported_schema")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, mode := range []string{"provider", "agent", "magpie"} {
		var id sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM processing_saved WHERE mode=?`, mode).Scan(&id); err != nil {
			return out, err
		}
		if id.Valid {
			v, e := processingRevisionTx(ctx, tx, id.String)
			if e != nil {
				return out, e
			}
			out.Saved[mode] = v
		}
	}
	out.Current, err = processingSelectionTx(ctx, tx)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

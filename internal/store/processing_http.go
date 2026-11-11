package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func migrateHTTPProcessing(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE processing_http_receipts(validation TEXT PRIMARY KEY REFERENCES processing_validations(id),payload TEXT NOT NULL);
 CREATE TABLE processing_suppliers(provider TEXT PRIMARY KEY,endpoint TEXT NOT NULL,protocol TEXT NOT NULL,credential TEXT NOT NULL,credential_revision INTEGER NOT NULL,revision INTEGER NOT NULL,updated_ms INTEGER NOT NULL);
 PRAGMA user_version=27;`)
	return err
}
func validHTTPReceipt(r records.ProcessingHTTPReceipt) bool {
	if r.Protocol != "openai-chat" && r.Protocol != "openai-responses" && r.Protocol != "anthropic-messages" {
		return false
	}
	for _, value := range []string{r.RequestedModel, r.ReportedModel, r.GatewayProvider, r.GatewayModel} {
		if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return false
		}
	}
	return r.StartedMS >= 0 && r.FinishedMS >= r.StartedMS && r.Status >= 0 && r.Status <= 599
}
func saveProcessingHTTPTx(ctx context.Context, tx *sql.Tx, id string, r records.ProcessingHTTPReceipt) error {
	if !validHTTPReceipt(r) {
		return records.MergeError("invalid_request")
	}
	var specRaw string
	if err := tx.QueryRowContext(ctx, `SELECT r.spec FROM processing_revisions r JOIN processing_validations v ON v.revision=r.id WHERE v.id=?`, id).Scan(&specRaw); err != nil {
		return err
	}
	var spec records.ProcessingSpec
	if json.Unmarshal([]byte(specRaw), &spec) != nil || spec.Mode != "provider" && spec.Mode != "magpie" || r.Protocol != spec.Protocol || r.RequestedModel != spec.Model {
		return records.MergeError("invalid_request")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_http_receipts(validation,payload) VALUES(?,?)`, id, string(raw))
	return err
}
func processingHTTPTx(ctx context.Context, tx *sql.Tx, id string) (*records.ProcessingHTTPReceipt, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT payload FROM processing_http_receipts WHERE validation=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out records.ProcessingHTTPReceipt
	if json.Unmarshal([]byte(raw), &out) != nil || !validHTTPReceipt(out) {
		return nil, records.MergeError("invalid_configuration")
	}
	return &out, nil
}

func (s *Store) SaveProcessingSupplier(ctx context.Context, d records.ProcessingCredentialWrite) (records.ProcessingSupplier, error) {
	var out records.ProcessingSupplier
	spec := records.ProcessingSpec{Mode: "provider", Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol, Model: "supplier-metadata", Credential: d.Credential, CredentialRevision: d.Revision}
	if s.Schema < 27 || d.ExpectedRevision < 0 || validateProcessingSpec(spec) != nil {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = processingSupplierTx(ctx, tx, d.Provider)
	if err == nil {
		if out.Endpoint == d.Endpoint && out.Protocol == d.Protocol && out.Credential == d.Credential && out.CredentialRevision == d.Revision {
			return out, nil
		}
		if out.Revision != d.ExpectedRevision {
			return records.ProcessingSupplier{}, ErrConflict
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	} else if d.ExpectedRevision != 0 {
		return out, ErrConflict
	}
	out = records.ProcessingSupplier{Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol, Credential: d.Credential, CredentialRevision: d.Revision, Revision: d.ExpectedRevision + 1, UpdatedMS: time.Now().UnixMilli()}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_suppliers(provider,endpoint,protocol,credential,credential_revision,revision,updated_ms) VALUES(?,?,?,?,?,?,?) ON CONFLICT(provider) DO UPDATE SET endpoint=excluded.endpoint,protocol=excluded.protocol,credential=excluded.credential,credential_revision=excluded.credential_revision,revision=excluded.revision,updated_ms=excluded.updated_ms`, out.Provider, out.Endpoint, out.Protocol, out.Credential, out.CredentialRevision, out.Revision, out.UpdatedMS)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func processingSupplierTx(ctx context.Context, tx *sql.Tx, provider string) (records.ProcessingSupplier, error) {
	var out records.ProcessingSupplier
	err := tx.QueryRowContext(ctx, `SELECT provider,endpoint,protocol,credential,credential_revision,revision,updated_ms FROM processing_suppliers WHERE provider=?`, provider).Scan(&out.Provider, &out.Endpoint, &out.Protocol, &out.Credential, &out.CredentialRevision, &out.Revision, &out.UpdatedMS)
	return out, err
}
func (s *Store) ProcessingSuppliers(ctx context.Context) ([]records.ProcessingSupplier, error) {
	out := []records.ProcessingSupplier{}
	rows, err := s.DB.QueryContext(ctx, `SELECT provider,endpoint,protocol,credential,credential_revision,revision,updated_ms FROM processing_suppliers ORDER BY provider`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item records.ProcessingSupplier
		if err = rows.Scan(&item.Provider, &item.Endpoint, &item.Protocol, &item.Credential, &item.CredentialRevision, &item.Revision, &item.UpdatedMS); err != nil {
			return out, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) ProcessingCredentialReferenced(ctx context.Context, id string, revision int64) (bool, error) {
	var used bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processing_suppliers WHERE credential=? AND credential_revision=? UNION ALL SELECT 1 FROM processing_revisions WHERE json_extract(spec,'$.credential')=? AND json_extract(spec,'$.credential_revision')=?)`, id, revision, id, revision).Scan(&used)
	return used, err
}

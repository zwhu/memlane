package store

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateOfficialCaptureReceipts(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE official_capture_receipts(operation TEXT PRIMARY KEY,library TEXT NOT NULL,grant_digest TEXT NOT NULL,instance TEXT NOT NULL,dataset TEXT NOT NULL,session TEXT NOT NULL,kind TEXT NOT NULL,payload_digest TEXT NOT NULL,state TEXT NOT NULL,official_id TEXT NOT NULL DEFAULT '',CHECK(state IN ('dispatching','saved','policy_filtered'))); PRAGMA user_version=33;`)
	return err
}

var officialCaptureHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var officialCaptureSession = regexp.MustCompile(`^pi-[0-9a-f]{48}$`)

func validateOfficialCaptureIdentity(q records.OfficialCaptureIdentity) error {
	for _, value := range []string{q.Library, q.Instance, q.Dataset} {
		if id, err := uuid.Parse(value); err != nil || id.String() != value {
			return access("invalid_request", "canonical official capture identity required")
		}
	}
	for _, value := range []string{q.Operation, q.GrantDigest, q.PayloadDigest} {
		if !officialCaptureHash.MatchString(value) {
			return access("invalid_request", "stable capture digest required")
		}
	}
	if !officialCaptureSession.MatchString(q.Session) || (q.Kind != "qa" && q.Kind != "trace") {
		return access("invalid_request", "bounded typed capture identity required")
	}
	return nil
}

func scanOfficialCapture(row *sql.Row) (records.OfficialCaptureDelivery, error) {
	var out records.OfficialCaptureDelivery
	err := row.Scan(&out.Operation, &out.Library, &out.GrantDigest, &out.Instance, &out.Dataset, &out.Session, &out.Kind, &out.PayloadDigest, &out.State, &out.OfficialID)
	return out, err
}

const officialCaptureColumns = `operation,library,grant_digest,instance,dataset,session,kind,payload_digest,state,official_id`

// ClaimOfficialCapture commits a dispatch intent before any network write.
// Only the winner receives claimed=true. A prior dispatching row is an UNKNOWN
// outcome on replay/restart, never permission to dispatch it again. This method
// does not grant collection consent; the daemon must authorize before calling.
func (s *Store) ClaimOfficialCapture(ctx context.Context, q records.OfficialCaptureIdentity, filtered bool) (out records.OfficialCaptureDelivery, claimed bool, err error) {
	if err = validateOfficialCaptureIdentity(q); err != nil {
		return
	}
	library, err := s.CogneeLibraryIdentity(ctx)
	if err != nil {
		return
	}
	if library != q.Library {
		err = access("delivery_library_changed", "original capture library changed")
		return
	}
	state := "dispatching"
	if filtered {
		state = "policy_filtered"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var currentLibrary string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&currentLibrary); err != nil {
		return
	}
	if currentLibrary != q.Library {
		err = access("delivery_library_changed", "original capture library changed")
		return
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO official_capture_receipts(`+officialCaptureColumns+`) VALUES(?,?,?,?,?,?,?,?,?,'') ON CONFLICT(operation) DO NOTHING`, q.Operation, q.Library, q.GrantDigest, q.Instance, q.Dataset, q.Session, q.Kind, q.PayloadDigest, state)
	if err != nil {
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		return
	}
	out, err = scanOfficialCapture(tx.QueryRowContext(ctx, `SELECT `+officialCaptureColumns+` FROM official_capture_receipts WHERE operation=?`, q.Operation))
	if err != nil {
		return
	}
	if out.OfficialCaptureIdentity != q || (out.State == "policy_filtered") != filtered {
		err = ErrConflict
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	claimed = count == 1 && !filtered
	return
}

// OfficialCaptureReceipt is read-only reconciliation against the local dispatch
// ledger, not a server-side receipt lookup and not authorization to resend.
func (s *Store) OfficialCaptureReceipt(ctx context.Context, q records.OfficialCaptureIdentity) (records.OfficialCaptureDelivery, error) {
	if err := validateOfficialCaptureIdentity(q); err != nil {
		return records.OfficialCaptureDelivery{}, err
	}
	library, err := s.CogneeLibraryIdentity(ctx)
	if err != nil {
		return records.OfficialCaptureDelivery{}, err
	}
	if library != q.Library {
		return records.OfficialCaptureDelivery{}, access("delivery_library_changed", "original capture library changed")
	}
	out, err := scanOfficialCapture(s.DB.QueryRowContext(ctx, `SELECT `+officialCaptureColumns+` FROM official_capture_receipts WHERE operation=?`, q.Operation))
	if err == nil && out.OfficialCaptureIdentity != q {
		return records.OfficialCaptureDelivery{}, ErrConflict
	}
	return out, err
}

// SealOfficialCapture records only a verified official ID. Unknown writes stay
// dispatching across close/reopen/restore. Re-sealing the same ID is idempotent;
// a conflicting receipt can never replace a previously confirmed result.
func (s *Store) SealOfficialCapture(ctx context.Context, q records.OfficialCaptureIdentity, officialID string) error {
	if id, err := uuid.Parse(officialID); err != nil || id.String() != officialID {
		return access("invalid_request", "verified official entry identity required")
	}
	prior, err := s.OfficialCaptureReceipt(ctx, q)
	if err != nil {
		return err
	}
	if prior.State == "saved" && prior.OfficialID == officialID {
		return nil
	}
	if prior.State != "dispatching" {
		return ErrConflict
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE official_capture_receipts SET state='saved',official_id=? WHERE operation=? AND library=? AND grant_digest=? AND instance=? AND dataset=? AND session=? AND kind=? AND payload_digest=? AND state='dispatching' AND official_id='' AND library=(SELECT identity FROM cognee_library_identity WHERE id=1)`, officialID, q.Operation, q.Library, q.GrantDigest, q.Instance, q.Dataset, q.Session, q.Kind, q.PayloadDigest)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		current, readErr := s.OfficialCaptureReceipt(ctx, q)
		if readErr == nil && current.State == "saved" && current.OfficialID == officialID {
			return nil
		}
		return ErrConflict
	}
	return nil
}

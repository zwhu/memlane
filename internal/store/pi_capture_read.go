package store

import (
	"context"
	"encoding/json"
	"github.com/zwhu/memlane/internal/records"
)

// CapturedDeliveries grants no history catalog access. Only saved operations
// from an explicit consent for this still-current connection/workspace count.
// Turning capture off preserves reading of those already saved operations.
func (s *Store) CapturedDeliveries(ctx context.Context, b records.CogneeBinding, policy string) ([]records.OfficialCaptureDelivery, error) {
	current, err := s.piCaptureAuthority(ctx, b)
	if err != nil {
		current, err = s.piCaptureReadAuthority(ctx, b, policy)
		if err != nil {
			return nil, err
		}
	}
	grants, err := s.piCaptureOriginalGrants(ctx, b, current, policy)
	if err != nil {
		return nil, err
	}
	return s.capturedDeliveriesForGrants(ctx, b, current.Library, grants)
}

func (s *Store) piCaptureOriginalGrants(ctx context.Context, b records.CogneeBinding, current records.PiCaptureConsent, policy string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key,payload FROM pi_capture_consents WHERE connection=? AND project=?`, b.ConnectionID, b.Project)
	if err != nil {
		return nil, err
	}
	grants := map[string]bool{}
	for rows.Next() {
		var key, payload string
		var g records.PiCaptureConsent
		if err = rows.Scan(&key, &payload); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(payload), &g); err != nil {
			break
		}
		if g.Key == key && g.Binding == b && g.Library == current.Library && g.WorkspaceDigest == current.WorkspaceDigest && g.WorkspacePhysical == current.WorkspacePhysical && g.PolicyDigest == policy {
			grants[key] = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(grants) == 0 {
		return nil, access("scope_denied", "explicit original capture consent required")
	}
	return grants, nil
}

func (s *Store) capturedDeliveriesForGrants(ctx context.Context, b records.CogneeBinding, library string, grants map[string]bool) ([]records.OfficialCaptureDelivery, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+officialCaptureColumns+` FROM official_capture_receipts WHERE library=? AND instance=? AND dataset=? AND state='saved' ORDER BY rowid DESC LIMIT 10001`, library, b.Instance, b.Dataset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []records.OfficialCaptureDelivery{}
	scanned := 0
	for rows.Next() {
		var r records.OfficialCaptureDelivery
		if err = rows.Scan(&r.Operation, &r.Library, &r.GrantDigest, &r.Instance, &r.Dataset, &r.Session, &r.Kind, &r.PayloadDigest, &r.State, &r.OfficialID); err != nil {
			return nil, err
		}
		scanned++
		if scanned > 10000 {
			return nil, access("invalid_request", "captured receipt read budget exceeded")
		}
		if !grants[r.GrantDigest] {
			continue
		}
		if err = validateOfficialCaptureIdentity(r.OfficialCaptureIdentity); err != nil {
			return nil, err
		}
		out = append(out, r)
		if len(out) > 10000 {
			return nil, access("invalid_request", "captured receipt read budget exceeded")
		}
	}
	return out, rows.Err()
}

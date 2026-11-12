package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// PiCaptureSettings is a trusted App observation, never a consent mutation.
// Return only already-bound projects in the current connection's allowed set.
func (s *Store) PiCaptureSettings(ctx context.Context, q records.PiCaptureSettingsQuery, policy string) ([]records.PiCaptureSetting, error) {
	c, err := s.Connection(ctx, q.Connection)
	if err != nil {
		return nil, err
	}
	if c.Agent != "pi" || !c.Enabled || c.Generation != q.Generation {
		return nil, access("scope_changed", "current enabled pi connection required")
	}
	out := []records.PiCaptureSetting{}
	if len(c.AllowedScopes) > 100 {
		return nil, access("invalid_request", "bounded approved projects required")
	}
	for _, project := range c.AllowedScopes {
		if id, e := uuid.Parse(project); e != nil || id.String() != project {
			continue
		}
		bindings, e := s.CogneeBindings(ctx, project)
		if e != nil {
			return nil, e
		}
		for _, b := range bindings {
			if b.ConnectionID != c.ID || !b.MaterialSubmission {
				continue
			}
			row := records.PiCaptureSetting{Binding: b}
			current, authorityErr := s.piCaptureAuthority(ctx, b)
			row.Ready = authorityErr == nil
			latest, e := scanPiCaptureConsent(s.DB.QueryRowContext(ctx, `SELECT operation,key,payload,enabled FROM pi_capture_consents WHERE connection=? AND project=? ORDER BY rowid DESC LIMIT 1`, c.ID, project))
			if e != nil && e != sql.ErrNoRows {
				return nil, e
			}
			if e == nil {
				row.PreviousKey = latest.Key
				if row.Ready && latest.Binding == b && latest.Library == current.Library && latest.WorkspaceDigest == current.WorkspaceDigest && latest.WorkspacePhysical == current.WorkspacePhysical && latest.PolicyDigest == policy {
					copy := latest
					row.Consent = &copy
				}
			}
			if row.Ready {
				deliveries, e := s.CapturedDeliveries(ctx, b, policy)
				if e == nil {
					row.Saved = len(deliveries)
				} else {
					var denied *records.AccessError
					if !errors.As(e, &denied) || denied.Code != "scope_denied" || row.Consent != nil {
						return nil, e
					}
				}
			}
			out = append(out, row)
		}
	}
	return out, nil
}

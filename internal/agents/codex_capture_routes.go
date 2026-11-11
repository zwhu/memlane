//go:build darwin || linux

package agents

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// The callback holds the same owner as collection and revocation. Daemon
// writes cannot race a completed user revocation. Never hold it across IPC.
func WithCodexCaptureAuthorization(ctx context.Context, root, connection string, fn func(Installation) error) error {
	owner, err := lockCodexCapture(ctx, root, connection)
	if err != nil {
		return err
	}
	defer owner.Close()
	i, err := LoadInstallation(root, connection)
	if err != nil {
		return err
	}
	if i.Connection.Agent != "codex" || !i.Connection.Enabled || !i.Options.ComputerLevel || i.CaptureConsent == nil || !i.CaptureConsent.Enabled {
		return errors.New("capture_not_authorized")
	}
	if err = CheckInstallation(i); err != nil {
		return errors.New("capture_installation_changed")
	}
	return fn(i)
}

// Called by the daemon inside WithCodexCaptureAuthorization after its own
// library/project/repository checks. No tool read/write scope is changed.
func SaveCodexCaptureRoute(root string, i Installation, r records.CaptureRoute) (records.CaptureRoute, error) {
	if i.CaptureConsent == nil || !i.CaptureConsent.Enabled || r.ConnectionID != i.Connection.ID || r.ConsentID != i.CaptureConsent.ID || r.Repository.State != "known" {
		return r, errors.New("capture_route_invalid")
	}
	for _, id := range []string{r.OperationID, r.LibraryID, r.Project} {
		if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
			return r, errors.New("capture_route_invalid")
		}
	}
	for index, prior := range i.CaptureRoutes {
		if prior.OperationID == r.OperationID {
			if prior.LibraryID != r.LibraryID || prior.ConsentID != r.ConsentID || prior.Project != r.Project || !prior.Repository.SameRepository(r.Repository) {
				return r, records.ErrConflict
			}
			return prior, nil
		}
		if prior.LibraryID == r.LibraryID && prior.ConnectionID == r.ConnectionID && prior.ConsentID == r.ConsentID && prior.Repository.SameRepository(r.Repository) {
			if prior.Project != r.Project {
				return r, errors.New("capture_route_project_changed")
			}
			return i.CaptureRoutes[index], nil
		}
	}
	if len(i.CaptureRoutes) >= 100 {
		return r, errors.New("capture_route_capacity")
	}
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	i.CaptureRoutes = append(i.CaptureRoutes, r)
	return r, saveCodexCaptureState(root, i)
}

func CaptureRouteFor(i Installation, origin records.CaptureOrigin, library string) (records.CaptureRoute, error) {
	if i.CaptureConsent == nil || !i.CaptureConsent.Enabled || origin.Consent != i.CaptureConsent.ID {
		return records.CaptureRoute{}, errors.New("capture_authorization_changed")
	}
	for _, r := range i.CaptureRoutes {
		if r.LibraryID == library && r.ConnectionID == i.Connection.ID && r.ConsentID == origin.Consent && r.Repository.SameRepository(origin.Repository) {
			return r, nil
		}
	}
	return records.CaptureRoute{}, errors.New("capture_route_required")
}

func MarkCodexCaptureDelivered(root, connection string, r records.CaptureDeliveryReceipt) error {
	last, err := ReadCodexCaptureStatus(root, connection)
	if err != nil || last == nil {
		return err
	}
	changed := false
	for index, entry := range last.Receipt.Entries {
		if entry.ID == r.EntryID && entry.RequestDigest == r.RequestDigest && entry.ReviewDigest == r.ReviewDigest {
			entry.State, entry.PersistedConfirmed, entry.SavedRevision, entry.Memory = "confirmed", true, r.Revision, r.Memory
			last.Receipt.Entries[index] = entry
			changed = true
		}
	}
	if !changed {
		return nil
	}
	all := len(last.Receipt.Entries) > 0
	for _, entry := range last.Receipt.Entries {
		all = all && entry.PersistedConfirmed
	}
	if all {
		last.Receipt.State, last.Receipt.Code = "delivered", ""
	}
	return saveCodexCaptureStatus(filepath.Join(root, "agent-adapters", connection), *last)
}

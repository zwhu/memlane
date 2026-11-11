package agents

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Collection consent is distinct from project read/write grants and from
// Codex's own hook trust. An enabled consent alone never means hooks loaded.
type CodexCaptureConsent struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	AuthorizedAt string `json:"authorized_at"`
}

// SetCodexCaptureConsent is an explicit App user action. It neither installs a
// hook nor scans sessions, creates projects, or grants knowledge access.
func SetCodexCaptureConsent(root, connection string, generation int64, enabled, confirmed bool) (Installation, error) {
	installationMu.Lock()
	defer installationMu.Unlock()
	var empty Installation
	if !confirmed {
		return empty, errors.New("capture_consent_confirmation_required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner, err := lockCodexCapture(ctx, root, connection)
	if err != nil {
		return empty, err
	}
	defer owner.Close()
	i, err := LoadInstallation(root, connection)
	if err != nil {
		return empty, err
	}
	if i.Connection.ID != connection || i.Connection.Generation != generation || i.Connection.Agent != "codex" || !i.Options.ComputerLevel {
		return empty, errors.New("capture_connection_changed")
	}
	if enabled {
		if _, verified := ConnectionVerification(i, generation); !verified {
			return empty, errors.New("capture_connection_unverified")
		}
		if err = CheckInstallation(i); err != nil {
			return empty, err
		}
		if i.CaptureConsent == nil || !i.CaptureConsent.Enabled {
			i.CaptureConsent = &CodexCaptureConsent{ID: uuid.NewString(), Enabled: true, AuthorizedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		}
	} else if i.CaptureConsent != nil {
		consent := *i.CaptureConsent
		consent.Enabled = false
		i.CaptureConsent = &consent
	}
	if err = saveCodexCaptureState(root, i); err != nil {
		return empty, err
	}
	return i, nil
}

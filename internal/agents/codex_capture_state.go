//go:build darwin || linux

package agents

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

// This sidecar belongs to collection, not to the daemon's installation writer.
// An old daemon can refresh its known metadata without erasing user consent.
type codexCaptureState struct {
	Version    int                    `json:"version"`
	Connection string                 `json:"connection"`
	Directory  string                 `json:"directory"`
	Consent    *CodexCaptureConsent   `json:"consent,omitempty"`
	Hooks      *CodexCaptureHooks     `json:"hooks,omitempty"`
	Routes     []records.CaptureRoute `json:"routes,omitempty"`
}

func codexCaptureStatePath(root, connection string) string {
	return filepath.Join(root, "agent-adapters", connection, "capture-state.json")
}

func loadCodexCaptureState(root, connection string, i *Installation) error {
	i.CaptureConsent, i.CaptureHooks = nil, nil
	i.CaptureRoutes = nil
	path := codexCaptureStatePath(root, connection)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("capture_state_unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("capture_state_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return errors.New("capture_state_unavailable")
	}
	var state codexCaptureState
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Connection != connection || i.Connection.ID != connection || i.Connection.Agent != "codex" || !i.Options.ComputerLevel || !sameConfigDirectory(state.Directory, i.Options.CodexDirectory) {
		return errors.New("capture_state_identity_changed")
	}
	if state.Consent != nil {
		id, e := uuid.Parse(state.Consent.ID)
		if e != nil || id.String() != state.Consent.ID {
			return errors.New("capture_state_corrupt")
		}
		if _, e = time.Parse(time.RFC3339Nano, state.Consent.AuthorizedAt); e != nil {
			return errors.New("capture_state_corrupt")
		}
	}
	i.CaptureConsent, i.CaptureHooks = state.Consent, state.Hooks
	i.CaptureRoutes = state.Routes
	return nil
}

// Callers hold the per-connection capture owner. Rename and directory sync
// finish before the authorization action returns to the App.
func saveCodexCaptureState(root string, i Installation) error {
	state := codexCaptureState{Version: 1, Connection: i.Connection.ID, Directory: i.Options.CodexDirectory, Consent: i.CaptureConsent, Hooks: i.CaptureHooks, Routes: i.CaptureRoutes}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > 65536 {
		return errors.New("capture_state_invalid")
	}
	path := codexCaptureStatePath(root, i.Connection.ID)
	if err = privateWrite(path, raw, 0600); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

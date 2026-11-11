package agents

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// Only the latest observed event, never a claim that every pending material is
// delivered or that a Codex process is still running. Originals stay in events.
type CodexCaptureStatus struct {
	Consent    string              `json:"consent"`
	ObservedAt string              `json:"observed_at"`
	Event      string              `json:"event"`
	Receipt    CodexCaptureReceipt `json:"receipt"`
}

func saveCodexCaptureStatus(directory string, status CodexCaptureStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if len(raw) > 131072 {
		return errors.New("capture_status_budget")
	}
	if err = privateWrite(filepath.Join(directory, "capture-last.json"), raw, 0600); err != nil {
		return err
	}
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func ReadCodexCaptureStatus(root, connection string) (*CodexCaptureStatus, error) {
	id, err := uuid.Parse(connection)
	if err != nil || id.String() != connection || !filepath.IsAbs(root) {
		return nil, errors.New("capture_identity_invalid")
	}
	path := filepath.Join(root, "agent-adapters", connection, "capture-last.json")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("capture_status_unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Uid != uint32(os.Getuid()) {
		return nil, errors.New("capture_status_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, errors.New("capture_status_unavailable")
	}
	var status CodexCaptureStatus
	if json.Unmarshal(raw, &status) != nil || status.Consent == "" || status.ObservedAt == "" || status.Receipt.Event == "" {
		return nil, errors.New("capture_status_unavailable")
	}
	if _, err = time.Parse(time.RFC3339Nano, status.ObservedAt); err != nil {
		return nil, errors.New("capture_status_unavailable")
	}
	return &status, nil
}

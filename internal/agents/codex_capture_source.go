//go:build darwin || linux

package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

// Recheck the exact current-turn prompt stage before authorizing automatic
// attribution. This also fences older queued auxiliary prompts, without
// rewriting/deleting originals or enumerating Codex history. Authorization
// callers must own the connection's capture lock; UI observations confer no grant.
func ValidateCodexCaptureSource(root string, i Installation, a records.AgentArguments) error {
	var meta struct {
		Session, Turn, Workspace string
		Consent                  string `json:"collection_consent"`
	}
	if a.Source == nil || json.Unmarshal([]byte(a.Source.Fragment), &meta) != nil || meta.Session == "" || meta.Turn == "" || i.CaptureConsent == nil || meta.Consent != i.CaptureConsent.ID {
		return errors.New("capture_session_unverified")
	}
	parts, _ := json.Marshal([]string{"codex-hook-v1", meta.Session, meta.Turn, "UserPromptSubmit"})
	hash := sha256.Sum256(parts)
	path := filepath.Join(root, "agent-adapters", i.Connection.ID, "capture-events", hex.EncodeToString(hash[:])+".json")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("capture_session_unverified")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Uid != uint32(os.Getuid()) {
		return errors.New("capture_session_unverified")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	var stage codexCaptureStage
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &stage) != nil || stage.Consent != meta.Consent || stage.Event.Session != meta.Session || stage.Event.Turn != meta.Turn || stage.Event.Workspace != meta.Workspace {
		return errors.New("capture_session_unverified")
	}
	snapshot, err := readCodexCaptureSnapshot(i.Options.CodexDirectory, stage.Event)
	if err != nil {
		return err
	}
	return checkCaptureWorkspace(snapshot, stage.Event)
}

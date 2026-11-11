//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// A bounded iterator over Memlane's own staged events. It never enumerates
// Agent sessions. Keep the directory position between ticks so old successful
// events cannot starve newer failures. Closing/reopening starts a new sweep.
type CodexCaptureRetry struct {
	directory        *os.File
	root, connection string
}

func (r *CodexCaptureRetry) Close() {
	if r.directory != nil {
		_ = r.directory.Close()
		r.directory = nil
	}
}

func retryableCaptureStage(stage codexCaptureStage) bool {
	if stage.Receipt.State == "staged" {
		return true
	}
	if stage.Receipt.State != "retained_error" {
		return false
	}
	switch stage.Receipt.Code {
	case "delivery_busy", "capture_transcript_unavailable", "capture_snapshot_invalid", "capture_status_unavailable", "capture_session_source_excluded":
		return true
	}
	return false
}

func (r *CodexCaptureRetry) Step(ctx context.Context, root, connection string) error {
	if r.root != root || r.connection != connection {
		r.Close()
		r.root, r.connection = root, connection
	}
	var consent string
	err := WithCodexCaptureAuthorization(ctx, root, connection, func(i Installation) error {
		consent = i.CaptureConsent.ID
		if r.directory != nil {
			return nil
		}
		path := filepath.Join(root, "agent-adapters", connection, "capture-events")
		fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&0077 != 0 || st.Uid != uint32(os.Getuid()) {
			unix.Close(fd)
			return errors.New("capture_stage_unavailable")
		}
		r.directory = os.NewFile(uintptr(fd), path)
		return nil
	})
	if err != nil {
		r.Close()
		return err
	}
	entries, err := r.directory.ReadDir(32)
	if err != nil && err != io.EOF {
		r.Close()
		return err
	}
	exhausted := err == io.EOF
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || len(name) != 69 || !strings.HasSuffix(name, ".json") {
			continue
		}
		fd, e := unix.Openat(int(r.directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			continue
		}
		f := os.NewFile(uintptr(fd), name)
		var st unix.Stat_t
		valid := unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&0077 == 0 && st.Uid == uint32(os.Getuid())
		var raw []byte
		if valid {
			raw, e = io.ReadAll(io.LimitReader(f, 2<<20+1))
		}
		_ = f.Close()
		if !valid || e != nil || len(raw) > 2<<20 {
			continue
		}
		var stage codexCaptureStage
		if json.Unmarshal(raw, &stage) != nil || name != stage.Event.ID+".json" || stage.Consent != consent || !retryableCaptureStage(stage) {
			continue
		}
		observed, e := time.Parse(time.RFC3339Nano, stage.Event.ObservedAt)
		if e != nil {
			continue
		}
		event := stage.Event
		payload := map[string]any{"session_id": event.Session, "turn_id": event.Turn, "cwd": event.Workspace, "hook_event_name": event.Event}
		if event.Event == "UserPromptSubmit" {
			payload["prompt"] = event.Text
		} else if event.Event == "Stop" {
			payload["last_assistant_message"] = event.Text
		} else {
			continue
		}
		if event.TranscriptPath != "" {
			payload["transcript_path"] = event.TranscriptPath
		}
		hook, _ := json.Marshal(payload)
		// CaptureCodexHook reacquires the consent owner and checks the frozen
		// event/consent before touching its outbox. No authorization is inferred.
		_, _ = CaptureCodexHook(ctx, root, connection, hook, observed)
	}
	if exhausted {
		r.Close()
	}
	return nil
}

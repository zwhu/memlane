//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

type CodexCaptureReceipt struct {
	Event         string                        `json:"event"`
	LocalRetained bool                          `json:"local_retained"`
	State         string                        `json:"state"`
	Code          string                        `json:"code,omitempty"`
	Entries       []records.AgentDeliveryStatus `json:"entries,omitempty"`
}

type codexCaptureStage struct {
	SessionVerified bool                      `json:"session_verified,omitempty"`
	Repository      records.CaptureRepository `json:"repository"`
	Consent         string                    `json:"consent"`
	Generation      int64                     `json:"generation"`
	Event           CodexCaptureEvent         `json:"event"`
	Receipt         CodexCaptureReceipt       `json:"receipt"`
}

func writeCaptureStage(path string, stage codexCaptureStage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	directory, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("capture_stage_unavailable")
	}
	defer unix.Close(directory)
	var st unix.Stat_t
	if unix.Fstat(directory, &st) != nil || st.Mode&0077 != 0 || st.Uid != uint32(os.Getuid()) {
		return errors.New("capture_stage_unavailable")
	}
	raw, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	if err = privateWrite(path, raw, 0600); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	if !stage.SessionVerified {
		return nil
	}
	last := CodexCaptureStatus{Consent: stage.Consent, ObservedAt: stage.Event.ObservedAt, Event: stage.Event.Event, Receipt: stage.Receipt}
	return saveCodexCaptureStatus(filepath.Dir(filepath.Dir(path)), last)
}

// Read only the current hook's exact transcript under its installed home. This
// never catalogs or imports historical sessions. The parser selects one turn.
func readCodexCaptureSnapshot(directory string, event CodexCaptureEvent) ([]byte, error) {
	if event.TranscriptPath == "" {
		return nil, errors.New("capture_session_unverified")
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, errors.New("capture_home_unavailable")
	}
	path, err := filepath.EvalSymlinks(event.TranscriptPath)
	if err != nil {
		return nil, errors.New("capture_transcript_unavailable")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, errors.New("capture_transcript_outside_home")
	}
	first := strings.Split(filepath.ToSlash(relative), "/")[0]
	if first != "sessions" && first != "archived_sessions" {
		return nil, errors.New("capture_transcript_outside_sessions")
	}
	profile, err := os.Open(root)
	if err != nil {
		return nil, errors.New("capture_home_unavailable")
	}
	defer profile.Close()
	f, err := historyOpen(profile, relative, false)
	if err != nil {
		return nil, errors.New("capture_transcript_unavailable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, codexHistoryLogBytes+1))
	if err != nil || len(raw) > codexHistoryLogBytes {
		return nil, errors.New("capture_transcript_budget")
	}
	return raw, nil
}

// Codex uses source=vscode for both its daemon-backed CLI and local Desktop.
// Match the observed originator as well; source alone also covers unsupported
// clients. This is a compatibility check, not authorization: consent, current
// transcript, session/turn identity and workspace are validated separately.
func codexLocalConversationSource(source, originator string) bool {
	if source != "vscode" {
		return false
	}
	return originator == "codex-tui" || originator == "Codex Desktop"
}

func checkCaptureWorkspace(raw []byte, event CodexCaptureEvent) error {
	var metadataCwd, turnCwd string
	metadataCount := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row codexHistoryRow
		if json.Unmarshal([]byte(line), &row) != nil {
			return errors.New("capture_snapshot_invalid")
		}
		if row.Type == "session_meta" {
			var p codexHistoryMeta
			var source string
			if json.Unmarshal(row.Payload, &p) != nil || json.Unmarshal(p.Source, &source) != nil || source != "cli" && source != "exec" && !codexLocalConversationSource(source, p.Originator) {
				return errors.New("capture_session_source_excluded")
			}
			metadataCount++
			if p.ID != event.Session {
				return errors.New("capture_snapshot_identity_mismatch")
			}
			metadataCwd = p.Cwd
		}
		if row.Type == "turn_context" {
			var p struct {
				Turn string `json:"turn_id"`
				Cwd  string `json:"cwd"`
			}
			if json.Unmarshal(row.Payload, &p) != nil {
				return errors.New("capture_snapshot_invalid")
			}
			if p.Turn == event.Turn {
				turnCwd = p.Cwd
			}
		}
	}
	if metadataCount != 1 {
		return errors.New("capture_snapshot_identity_mismatch")
	}
	if turnCwd == "" {
		turnCwd = metadataCwd
	}
	canonical, err := filepath.EvalSymlinks(turnCwd)
	if err != nil || canonical != event.Workspace {
		return errors.New("capture_snapshot_workspace_mismatch")
	}
	return nil
}

// CaptureCodexHook first durably stages the authorized event, then reuses the
// existing raw outbox. It does not invoke a model, register projects, send an
// ungranted save, or claim that local retention is daemon persistence.
func CaptureCodexHook(ctx context.Context, root, connection string, raw []byte, observed time.Time) (CodexCaptureReceipt, error) {
	var receipt CodexCaptureReceipt
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	owner, err := lockCodexCapture(ctx, root, connection)
	if err != nil {
		return receipt, err
	}
	defer owner.Close()
	i, err := LoadInstallation(root, connection)
	if err != nil {
		return receipt, errors.New("capture_installation_unavailable")
	}
	if i.Connection.Agent != "codex" || i.Connection.ID != connection || !i.Connection.Enabled || !i.Options.ComputerLevel || i.CaptureConsent == nil || !i.CaptureConsent.Enabled {
		return receipt, errors.New("capture_not_authorized")
	}
	if err = CheckInstallation(i); err != nil {
		return receipt, errors.New("capture_installation_changed")
	}
	boundary, err := time.Parse(time.RFC3339Nano, i.CaptureConsent.AuthorizedAt)
	if err != nil || observed.Before(boundary) {
		return receipt, errors.New("capture_before_authorization")
	}
	event, err := DecodeCodexCaptureEvent(raw, i.AgentVersion, observed)
	if err != nil {
		return receipt, err
	}
	workspace, err := filepath.EvalSymlinks(event.Workspace)
	if err != nil {
		workspace = filepath.Clean(event.Workspace) // Preserve the event even if its directory vanished.
	}
	event.Workspace = workspace
	event.ContentDigest = records.MergeDigest([]string{workspace, event.Role, event.Text})
	path := filepath.Join(root, "agent-adapters", connection, "capture-events", event.ID+".json")
	stage := codexCaptureStage{Repository: agentbridge.CaptureRepository(ctx, workspace), Consent: i.CaptureConsent.ID, Generation: i.Connection.Generation, Event: event, Receipt: CodexCaptureReceipt{Event: event.ID, LocalRetained: true, State: "staged"}}
	if old, readErr := os.ReadFile(path); readErr == nil {
		var prior codexCaptureStage
		if json.Unmarshal(old, &prior) != nil || prior.Event.ID != event.ID {
			return receipt, errors.New("capture_stage_corrupt")
		}
		if prior.Event.ContentDigest != event.ContentDigest {
			return prior.Receipt, errors.New("capture_event_conflict")
		}
		if prior.Consent != i.CaptureConsent.ID {
			return prior.Receipt, errors.New("capture_prior_authorization")
		}
		if event.Event == "UserPromptSubmit" && prior.Receipt.State == "retained_pending_assignment" {
			return prior.Receipt, nil
		}
		stage = prior // Replay keeps original observation/source and consent boundary.
		event = stage.Event
	} else if !os.IsNotExist(readErr) {
		return receipt, errors.New("capture_stage_unavailable")
	}
	if err = writeCaptureStage(path, stage); err != nil {
		return receipt, errors.New("capture_stage_unavailable")
	}
	receipt = stage.Receipt
	receipt.Entries = nil
	fail := func(code string) (CodexCaptureReceipt, error) {
		receipt.State, receipt.Code = "retained_error", code
		stage.Receipt = receipt
		if e := writeCaptureStage(path, stage); e != nil {
			return receipt, errors.New("capture_status_unavailable")
		}
		return receipt, errors.New(code)
	}
	messages := []CodexCapturedMessage{{ID: event.ID, Role: "user", Text: event.Text, Timestamp: event.ObservedAt}}
	sessionVersion := ""
	if event.Event == "UserPromptSubmit" {
		snapshot, e := readCodexCaptureSnapshot(i.Options.CodexDirectory, event)
		if e != nil {
			return fail(e.Error())
		}
		if e = checkCaptureWorkspace(snapshot, event); e != nil {
			return fail(e.Error())
		}
		stage.SessionVerified = true
	}
	if event.Event == "Stop" {
		snapshot, e := readCodexCaptureSnapshot(i.Options.CodexDirectory, event)
		if e != nil {
			return fail(e.Error())
		}
		if e = checkCaptureWorkspace(snapshot, event); e != nil {
			return fail(e.Error())
		}
		stage.SessionVerified = true
		for _, line := range strings.Split(string(snapshot), "\n") {
			var row codexHistoryRow
			if json.Unmarshal([]byte(line), &row) == nil && row.Type == "session_meta" {
				var meta codexHistoryMeta
				if json.Unmarshal(row.Payload, &meta) == nil {
					sessionVersion = meta.Version
				}
			}
		}
		all, e := DecodeCodexCaptureMessages(snapshot, event)
		if e != nil {
			return fail(e.Error())
		}
		messages = nil
		for _, message := range all {
			stamp, e := time.Parse(time.RFC3339Nano, message.Timestamp)
			if e != nil || stamp.Before(boundary) {
				return fail("capture_before_authorization")
			}
			if message.Role == "assistant" {
				messages = append(messages, message)
			}
		}
		if len(messages) == 0 {
			return fail("capture_assistant_messages_unavailable")
		}
	}
	for _, message := range messages {
		if err = ctx.Err(); err != nil {
			return fail("capture_cancelled")
		}
		identity := records.MergeDigest([]string{"codex-message-v1", connection, event.Session, event.Turn, message.ID})
		timeKind, timeLabel := "message", "消息时间"
		if message.Role == "user" {
			timeKind, timeLabel = "hook_observed", "采集时间"
		}
		source := map[string]any{"agent": "codex", "session": event.Session, "turn": event.Turn, "message": message.ID, "role": message.Role, "timestamp": message.Timestamp, "timestamp_kind": timeKind, "workspace": event.Workspace, "collection_consent": stage.Consent, "capture_repository": stage.Repository}
		if message.Role == "user" {
			source["observed_at"], source["installation_version"] = stage.Event.ObservedAt, event.AgentVersion
		} else {
			source["session_version"] = sessionVersion
		}
		metadata, _ := json.Marshal(source)
		text := fmt.Sprintf("Codex 原始对话 · %s\n会话：%s\n轮次：%s\n工作目录：%s\n%s：%s\n\n%s", message.Role, event.Session, event.Turn, event.Workspace, timeLabel, message.Timestamp, message.Text)
		status, e := agentbridge.RetainCapturedRaw(agentbridge.Config{Root: i.Options.ServiceRoot, ConnectionID: connection, Generation: stage.Generation, Workspace: event.Workspace}, records.AgentArguments{OperationID: identity, Title: "Codex 对话 · " + message.Role, Text: text, Origin: "raw", Source: &records.AgentSource{Fragment: string(metadata)}})
		if e != nil {
			return fail(e.Error())
		}
		receipt.Entries = append(receipt.Entries, status)
	}
	receipt.State, receipt.Code = "retained_pending_assignment", ""
	allDelivered := len(receipt.Entries) > 0
	for _, entry := range receipt.Entries {
		allDelivered = allDelivered && entry.PersistedConfirmed
	}
	if allDelivered {
		receipt.State = "delivered"
	}
	stage.Receipt = receipt
	if err = writeCaptureStage(path, stage); err != nil {
		return receipt, errors.New("capture_status_unavailable")
	}
	return receipt, nil
}

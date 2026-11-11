//go:build darwin || linux

package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type CodexCapturedMessage struct {
	ID, Role, Text, Timestamp string
}

// DecodeCodexCaptureMessages reads only the hook's selected turn. Native
// completed message events distinguish actual conversation from injected
// developer/environment context and duplicate response_item rows. Tool output
// and reasoning are intentionally outside this conversation adapter.
// It does not claim completion: a Stop-time snapshot may precede the durable
// task_complete row, which the owner must reconcile separately.
func DecodeCodexCaptureMessages(raw []byte, event CodexCaptureEvent) ([]CodexCapturedMessage, error) {
	if len(raw) == 0 || len(raw) > codexHistoryLogBytes || raw[len(raw)-1] != '\n' || !utf8.Valid(raw) || event.Session == "" || event.Turn == "" {
		return nil, errors.New("capture_snapshot_invalid")
	}
	var messages []CodexCapturedMessage
	seen := map[string]bool{}
	metadata := 0
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row codexHistoryRow
		if json.Unmarshal(line, &row) != nil {
			return nil, errors.New("capture_snapshot_invalid")
		}
		if row.Type == "session_meta" {
			var meta codexHistoryMeta
			if json.Unmarshal(row.Payload, &meta) != nil || meta.ID != event.Session {
				return nil, errors.New("capture_snapshot_identity_mismatch")
			}
			metadata++
			continue
		}
		if row.Type != "event_msg" {
			continue
		}
		var p struct {
			Type   string `json:"type"`
			Turn   string `json:"turn_id"`
			Thread string `json:"thread_id"`
			Item   struct {
				Type, ID string
				Content  []struct{ Type, Text string }
			} `json:"item"`
		}
		if json.Unmarshal(row.Payload, &p) != nil {
			return nil, errors.New("capture_snapshot_invalid")
		}
		if p.Type != "item_completed" || p.Turn != event.Turn {
			continue
		}
		role := ""
		switch p.Item.Type {
		case "UserMessage":
			role = "user"
		case "AgentMessage":
			role = "assistant"
		default:
			continue
		}
		if p.Thread != event.Session || p.Item.ID == "" || seen[p.Item.ID] {
			return nil, errors.New("capture_message_identity_invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, row.Timestamp); err != nil {
			return nil, errors.New("capture_message_time_invalid")
		}
		var body strings.Builder
		for _, content := range p.Item.Content {
			if role == "user" && content.Type != "text" || role == "assistant" && content.Type != "Text" {
				return nil, errors.New("capture_content_unsupported")
			}
			body.WriteString(content.Text)
		}
		if body.Len() == 0 || body.Len() > 512<<10 {
			return nil, errors.New("capture_text_unavailable")
		}
		seen[p.Item.ID] = true
		messages = append(messages, CodexCapturedMessage{ID: p.Item.ID, Role: role, Text: body.String(), Timestamp: row.Timestamp})
	}
	if metadata != 1 {
		return nil, errors.New("capture_snapshot_identity_mismatch")
	}
	return messages, nil
}

// CodexCaptureEvent adapts an event, not an entire transcript. In particular,
// Stop's final message does not prove that commentary or interrupted output was
// captured. The owner must durably retain this event before acknowledging it.
type CodexCaptureEvent struct {
	ID, Session, Turn, Event, Workspace, Role, Text, ObservedAt string
	AgentVersion                                                string
	TranscriptPath                                              string
	ContentDigest                                               string
}

// DecodeCodexCaptureEvent performs no filesystem access and never imports
// history. version is diagnostic information from the installed executable,
// never an allowlist. Receipt time is not represented as message time.
func DecodeCodexCaptureEvent(raw []byte, version string, observed time.Time) (CodexCaptureEvent, error) {
	var out CodexCaptureEvent
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) || observed.IsZero() {
		return out, errors.New("capture_event_invalid")
	}
	var p struct {
		Session string  `json:"session_id"`
		Turn    string  `json:"turn_id"`
		Cwd     string  `json:"cwd"`
		Event   string  `json:"hook_event_name"`
		Prompt  *string `json:"prompt"`
		Answer  *string `json:"last_assistant_message"`
		Path    *string `json:"transcript_path"`
		Agent   *string `json:"agent_id"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return out, errors.New("capture_event_invalid")
	}
	id, err := uuid.Parse(p.Session)
	if err != nil || id.String() != p.Session || len(p.Turn) == 0 || len(p.Turn) > 128 || strings.ContainsAny(p.Turn, "\x00\r\n") || !filepath.IsAbs(p.Cwd) || strings.ContainsRune(p.Cwd, '\x00') {
		return out, errors.New("capture_identity_invalid")
	}
	if p.Agent != nil && *p.Agent != "" {
		return out, errors.New("capture_subagent_excluded")
	}
	var text *string
	switch p.Event {
	case "UserPromptSubmit":
		out.Role, text = "user", p.Prompt
	case "Stop":
		out.Role, text = "assistant", p.Answer
		if text == nil {
			empty := ""
			text = &empty
		}
	default:
		return out, errors.New("capture_event_unsupported")
	}
	if text == nil || p.Event == "UserPromptSubmit" && strings.TrimSpace(*text) == "" || len(*text) > 512<<10 {
		return out, errors.New("capture_text_unavailable")
	}
	if p.Path != nil {
		if !filepath.IsAbs(*p.Path) || strings.ContainsRune(*p.Path, '\x00') {
			return CodexCaptureEvent{}, errors.New("capture_transcript_invalid")
		}
		out.TranscriptPath = *p.Path // A hint only; a separate confined reader must validate it.
	}
	out.Session, out.Turn, out.Event, out.Workspace, out.Text = p.Session, p.Turn, p.Event, p.Cwd, *text
	out.AgentVersion = version
	out.ObservedAt = observed.UTC().Format(time.RFC3339Nano)
	identityParts := []string{"codex-hook-v1", p.Session, p.Turn, p.Event}
	if p.Event == "Stop" {
		// A Stop hook may legitimately continue the same turn. Preserve each
		// final-text event, while native message IDs deduplicate its snapshot.
		answer := sha256.Sum256([]byte(out.Text))
		identityParts = append(identityParts, hex.EncodeToString(answer[:]))
	}
	identity, _ := json.Marshal(identityParts)
	digest := sha256.Sum256(identity)
	out.ID = hex.EncodeToString(digest[:])
	content, _ := json.Marshal([]string{p.Cwd, out.Role, out.Text})
	digest = sha256.Sum256(content)
	out.ContentDigest = hex.EncodeToString(digest[:])
	return out, nil
}

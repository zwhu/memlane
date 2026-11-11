//go:build darwin || linux

package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexCaptureMessagesSelectOnlyNativeCurrentTurn(t *testing.T) {
	session := "05e11a40-2d5c-43f9-a242-c51a99b4b540"
	event := CodexCaptureEvent{Session: session, Turn: "new-turn"}
	rows := []map[string]any{
		{"type": "session_meta", "payload": map[string]any{"id": session, "cli_version": "9.0.0"}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "injected environment, not conversation"}}}},
	}
	item := func(turn, id, kind, contentType, text string) map[string]any {
		return map[string]any{"timestamp": "2026-11-11T01:00:00Z", "type": "event_msg", "payload": map[string]any{"type": "item_completed", "turn_id": turn, "thread_id": session, "item": map[string]any{"id": id, "type": kind, "content": []any{map[string]string{"type": contentType, "text": text}}}}}
	}
	rows = append(rows, item("old-turn", "old", "UserMessage", "text", "old history"), item("new-turn", "u", "UserMessage", "text", "  actual user\n"), item("new-turn", "a", "AgentMessage", "Text", "actual assistant"))
	encode := func() []byte {
		var raw []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			raw = append(raw, b...)
			raw = append(raw, '\n')
		}
		return raw
	}
	messages, err := DecodeCodexCaptureMessages(encode(), event)
	if err != nil || len(messages) != 2 || messages[0].Text != "  actual user\n" || messages[1].Text != "actual assistant" {
		t.Fatal("wrong conversation attribution", messages, err)
	}
	rows = append(rows, item("new-turn", "u", "UserMessage", "text", "conflicting duplicate"))
	if _, err = DecodeCodexCaptureMessages(encode(), event); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	rows = rows[:len(rows)-1]
	rows = append(rows, item("new-turn", "image", "UserMessage", "image", ""))
	if _, err = DecodeCodexCaptureMessages(encode(), event); err == nil {
		t.Fatal("unsupported content silently truncated")
	}
}

// Explicit opt-in to this run's synthetic evidence; never opens daily history.
func TestCodexCaptureRealSyntheticStopSnapshot(t *testing.T) {
	directory := os.Getenv("MEMLANE_CODEX_CAPTURE_PROBE")
	if directory == "" {
		t.Skip("explicit synthetic probe evidence required")
	}
	receipt, err := os.ReadFile(filepath.Join(directory, "probe-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Synthetic bool   `json:"synthetic_only"`
		Version   string `json:"version"`
	}
	if json.Unmarshal(receipt, &report) != nil || !report.Synthetic || report.Version != "codex-cli 0.167.1" {
		t.Fatal("not the expected controlled probe")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "probe-events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events map[string]json.RawMessage
	if err = json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	event, err := DecodeCodexCaptureEvent(events["Stop"], "0.167.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(directory, "Stop-snapshot.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	messages, err := DecodeCodexCaptureMessages(raw, event)
	if err != nil || len(messages) != 2 || messages[0].Role != "user" || messages[0].Text != "MEMLANE_SYNTHETIC_CAPTURE_PROMPT: return the fixture reply only." || messages[1].Text != "MEMLANE_SYNTHETIC_CAPTURE_REPLY" {
		t.Fatal("Stop-time conversation did not match synthetic exchange", messages, err)
	}
}

func TestCodexCaptureEventReplayAndConflict(t *testing.T) {
	p := map[string]any{"session_id": "05e11a40-2d5c-43f9-a242-c51a99b4b540", "turn_id": "turn-1", "cwd": "/synthetic/repo", "hook_event_name": "UserPromptSubmit", "prompt": "  测试材料\n保留空白  ", "transcript_path": nil}
	decode := func() CodexCaptureEvent {
		t.Helper()
		raw, _ := json.Marshal(p)
		e, err := DecodeCodexCaptureEvent(raw, "0.167.1", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first, replay := decode(), decode()
	if first.ID != replay.ID || first.ContentDigest != replay.ContentDigest || first.Text != p["prompt"] || first.Role != "user" {
		t.Fatal("event replay or original text changed")
	}
	raw, _ := json.Marshal(p)
	upgraded, err := DecodeCodexCaptureEvent(raw, "9.0.0", time.Now())
	if err != nil || upgraded.ID != first.ID || upgraded.ContentDigest != first.ContentDigest {
		t.Fatal("diagnostic version change duplicated the same event", upgraded, err)
	}
	p["prompt"] = "different original"
	conflict := decode()
	if conflict.ID != first.ID || conflict.ContentDigest == first.ContentDigest {
		t.Fatal("same event content conflict cannot be detected")
	}
	p["hook_event_name"], p["last_assistant_message"] = "Stop", "助手回复"
	answer := decode()
	if answer.ID == first.ID || answer.Role != "assistant" || answer.Text != "助手回复" {
		t.Fatal("assistant event identity mixed with user event")
	}
}

func TestCodexCaptureRejectsIncompleteOrOtherEvents(t *testing.T) {
	base := map[string]any{"session_id": "05e11a40-2d5c-43f9-a242-c51a99b4b540", "turn_id": "turn-1", "cwd": "/synthetic/repo", "hook_event_name": "Stop", "last_assistant_message": "合成回复"}
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"missing-session", "session_id", ""}, {"missing-turn", "turn_id", ""}, {"relative-cwd", "cwd", "repo"},
		{"other-event", "hook_event_name", "SubagentStop"},
		{"subagent", "agent_id", "child"}, {"relative-transcript", "transcript_path", "outside.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{}
			for k, v := range base {
				p[k] = v
			}
			p[tc.key] = tc.value
			raw, _ := json.Marshal(p)
			if _, err := DecodeCodexCaptureEvent(raw, "0.167.1", time.Now()); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	raw, _ := json.Marshal(base)
	if event, err := DecodeCodexCaptureEvent(raw, "9.0.0", time.Now()); err != nil || event.AgentVersion != "9.0.0" {
		t.Fatal("version incorrectly blocks compatible event", event, err)
	}
}

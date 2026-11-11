//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func captureRuntimeFixture(t *testing.T) (Installation, string) {
	t.Helper()
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	bridge := filepath.Join(root, "capture-bridge")
	if err := os.WriteFile(bridge, []byte("#!/bin/sh\nprintf '%s\\n' 'capture-codex -client-root -connection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile, workspace := filepath.Join(root, "profile"), filepath.Join(root, "unknown-repository")
	for _, p := range []string{profile, workspace, filepath.Join(root, "service")} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1, AllowedScopes: []string{}}
	o := InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: bridge, AgentPath: program, CodexDirectory: profile}
	i, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveProtocolVerification(o.ClientRoot, c.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	return i, workspace
}

func TestCodexCaptureRuntimeAuthorizationReplayRevocation(t *testing.T) {
	i, workspace := captureRuntimeFixture(t)
	session := uuid.NewString()
	p := map[string]any{"session_id": session, "turn_id": "turn-1", "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "MEMLANE_SYNTHETIC_ONLY: user prompt", "transcript_path": capturePromptTranscript(t, i, session, workspace)}
	raw, _ := json.Marshal(p)
	ctx := context.Background()
	if _, err := CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, raw, time.Now()); err == nil {
		t.Fatal("unauthorized prompt collected")
	}
	if _, err := os.Stat(filepath.Join(i.Options.ClientRoot, "agent-adapters", i.Connection.ID, "capture-events")); !os.IsNotExist(err) {
		t.Fatal("unauthorized original retained")
	}
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	one, err := CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, raw, time.Now())
	if err != nil || !one.LocalRetained || len(one.Entries) != 1 || one.Entries[0].PersistedConfirmed {
		t.Fatal(one, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	p["cwd"] = alias
	raw, _ = json.Marshal(p)
	two, err := CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, raw, time.Now())
	if err != nil || len(two.Entries) != 1 || two.Entries[0].ID != one.Entries[0].ID {
		t.Fatal("alias replay duplicated original", two, err)
	}
	page, err := agentbridge.ListDeliveries(i.Options.ServiceRoot)
	if err != nil || len(page.Items) != 1 || page.Items[0].Origin != "raw" {
		t.Fatal(page, err)
	}
	read, err := agentbridge.ReviewDelivery(i.Options.ServiceRoot, one.Entries[0].ID)
	if err != nil || !strings.Contains(read.Arguments.Text, "MEMLANE_SYNTHETIC_ONLY") {
		t.Fatal(read, err)
	}
	if _, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, false, true); err != nil {
		t.Fatal(err)
	}
	p["turn_id"] = "turn-2"
	raw, _ = json.Marshal(p)
	if _, err = CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, raw, time.Now()); err == nil {
		t.Fatal("revoked consent collected another prompt")
	}
	page, err = agentbridge.ListDeliveries(i.Options.ServiceRoot)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("revocation deleted or added material", page, err)
	}
}

func TestCodexCaptureRuntimeAssistantSnapshotAndParseFailureRetention(t *testing.T) {
	i, workspace := captureRuntimeFixture(t)
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	snapshot := filepath.Join(i.Options.CodexDirectory, "sessions", "current.jsonl")
	if err = os.MkdirAll(filepath.Dir(snapshot), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Millisecond)
	rows := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": workspace, "source": "cli", "cli_version": "9.0.0"}},
		map[string]any{"type": "event_msg", "timestamp": now.Format(time.RFC3339Nano), "payload": map[string]any{"type": "item_completed", "turn_id": "turn-1", "thread_id": session, "item": map[string]any{"id": "assistant-native-1", "type": "AgentMessage", "content": []any{map[string]string{"type": "Text", "text": "MEMLANE_SYNTHETIC_ASSISTANT"}}}}},
	}
	var contents []byte
	for _, row := range rows {
		line, _ := json.Marshal(row)
		contents = append(contents, line...)
		contents = append(contents, '\n')
	}
	if err = os.WriteFile(snapshot, contents, 0600); err != nil {
		t.Fatal(err)
	}
	p := map[string]any{"session_id": session, "turn_id": "turn-1", "cwd": workspace, "hook_event_name": "Stop", "last_assistant_message": "MEMLANE_SYNTHETIC_ASSISTANT", "transcript_path": snapshot}
	raw, _ := json.Marshal(p)
	one, err := CaptureCodexHook(context.Background(), i.Options.ClientRoot, i.Connection.ID, raw, now.Add(time.Second))
	if err != nil || len(one.Entries) != 1 {
		t.Fatal(one, err)
	}
	// A Stop hook can continue the same turn. Its next snapshot includes old
	// messages plus new ones; retain each native message once, including when
	// the final-text hook event is itself unchanged.
	for index := 2; index <= 3; index++ {
		message := map[string]any{"type": "event_msg", "timestamp": now.Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano), "payload": map[string]any{"type": "item_completed", "turn_id": "turn-1", "thread_id": session, "item": map[string]any{"id": fmt.Sprintf("assistant-native-%d", index), "type": "AgentMessage", "content": []any{map[string]string{"type": "Text", "text": fmt.Sprintf("MEMLANE_SYNTHETIC_CONTINUATION_%d", index)}}}}}
		line, _ := json.Marshal(message)
		contents = append(contents, line...)
		contents = append(contents, '\n')
		if err = os.WriteFile(snapshot, contents, 0600); err != nil {
			t.Fatal(err)
		}
		p["last_assistant_message"] = "same final hook text"
		raw, _ = json.Marshal(p)
		continued, e := CaptureCodexHook(context.Background(), i.Options.ClientRoot, i.Connection.ID, raw, now.Add(time.Second))
		if e != nil || len(continued.Entries) != index {
			t.Fatal("continued Stop lost or duplicated messages", continued, e)
		}
	}
	// Unsupported/incomplete future input remains privately staged, not sent
	// or marked fully captured. Previous raw records remain independently readable.
	p["turn_id"] = "turn-2"
	raw, _ = json.Marshal(p)
	if err = os.WriteFile(snapshot, []byte("incomplete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	two, err := CaptureCodexHook(context.Background(), i.Options.ClientRoot, i.Connection.ID, raw, now.Add(time.Second))
	if err == nil || !two.LocalRetained || two.State != "retained_error" {
		t.Fatal("parse failure silently lost event", two, err)
	}
	stage, err := os.ReadFile(filepath.Join(i.Options.ClientRoot, "agent-adapters", i.Connection.ID, "capture-events", two.Event+".json"))
	if err != nil || !strings.Contains(string(stage), p["last_assistant_message"].(string)) {
		t.Fatal("failed event not durably retained", err)
	}
	page, err := agentbridge.ListDeliveries(i.Options.ServiceRoot)
	if err != nil || len(page.Items) != 3 {
		t.Fatal("parse failure produced misleading raw body", page, err)
	}
}

func TestConcurrentCodexCaptureReplayRetainsOneOriginal(t *testing.T) {
	i, workspace := captureRuntimeFixture(t)
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	raw, _ := json.Marshal(map[string]any{"session_id": session, "turn_id": "one-turn", "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "MEMLANE_SYNTHETIC_CONCURRENT", "transcript_path": capturePromptTranscript(t, i, session, workspace)})
	var group sync.WaitGroup
	results := make(chan error, 8)
	for n := 0; n < 8; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, raw, time.Now())
			results <- err
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := agentbridge.ListDeliveries(i.Options.ServiceRoot)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("concurrent replay duplicated original", page, err)
	}
}

//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func capturePromptTranscript(t *testing.T, i Installation, session, workspace string) string {
	t.Helper()
	path := filepath.Join(i.Options.CodexDirectory, "sessions", session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": workspace, "source": "cli", "cli_version": "synthetic"}})
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCaptureWorkspaceRecognizesDaemonTUIWithoutAllowingIDE(t *testing.T) {
	workspace := t.TempDir()
	workspace, _ = filepath.EvalSymlinks(workspace)
	session := uuid.NewString()
	for _, origin := range []string{"codex-tui", "Codex Desktop", "codex-vscode", "unknown-desktop"} {
		raw, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": workspace, "source": "vscode", "originator": origin}})
		err := checkCaptureWorkspace(raw, CodexCaptureEvent{Session: session, Workspace: workspace})
		if (origin == "codex-tui" || origin == "Codex Desktop") && err != nil {
			t.Fatal("supported local Codex conversation rejected", err)
		}
		if origin != "codex-tui" && origin != "Codex Desktop" && err == nil {
			t.Fatal("unverified client inherited local conversation acceptance")
		}
	}
}

func TestCaptureAuxiliaryPromptWithoutTranscriptRemainsStaged(t *testing.T) {
	i, workspace := captureRuntimeFixture(t)
	_, err := SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"session_id": uuid.NewString(), "turn_id": "auxiliary-title", "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "SYNTHETIC auxiliary title generation"})
	receipt, err := CaptureCodexHook(context.Background(), i.Options.ClientRoot, i.Connection.ID, raw, time.Now())
	if err == nil || !receipt.LocalRetained {
		t.Fatal("unproven auxiliary session treated as user conversation", receipt, err)
	}
	page, err := agentbridge.ListDeliveries(i.Options.ServiceRoot)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("auxiliary prompt entered assignable conversation queue", page, err)
	}
}

func TestPreviouslyQueuedAuxiliarySourceCannotAcquireRoutingProof(t *testing.T) {
	i, workspace := captureRuntimeFixture(t)
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	hook, _ := json.Marshal(map[string]any{"session_id": session, "turn_id": "legacy-auxiliary", "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "SYNTHETIC legacy auxiliary original"})
	event, err := DecodeCodexCaptureEvent(hook, "synthetic", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	event.Workspace, _ = filepath.EvalSymlinks(workspace)
	stage := codexCaptureStage{Consent: i.CaptureConsent.ID, Generation: 1, Event: event, Receipt: CodexCaptureReceipt{Event: event.ID, LocalRetained: true, State: "retained_pending_assignment"}}
	if err = writeCaptureStage(filepath.Join(i.Options.ClientRoot, "agent-adapters", i.Connection.ID, "capture-events", event.ID+".json"), stage); err != nil {
		t.Fatal(err)
	}
	fragment, _ := json.Marshal(map[string]any{"agent": "codex", "session": session, "turn": "legacy-auxiliary", "workspace": event.Workspace, "collection_consent": i.CaptureConsent.ID})
	a := records.AgentArguments{OperationID: "legacy-synthetic-auxiliary", Origin: "raw", Text: "SYNTHETIC legacy auxiliary original", Source: &records.AgentSource{Fragment: string(fragment)}}
	retained, err := agentbridge.RetainCapturedRaw(agentbridge.Config{Root: i.Options.ServiceRoot, ConnectionID: i.Connection.ID, Generation: 1, Workspace: workspace}, a)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateCodexCaptureSource(i.Options.ClientRoot, i, a); err == nil {
		t.Fatal("legacy auxiliary source received automatic routing proof")
	}
	review, err := agentbridge.ReviewDelivery(i.Options.ServiceRoot, retained.ID)
	if err != nil || review.Arguments.Text != a.Text || review.Item.Status.PersistedConfirmed {
		t.Fatal("unproven original lost or reported saved", review, err)
	}
}

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexCaptureHookInstallationPreservesOtherHooksAndRemovesOwnedItems(t *testing.T) {
	i, _ := captureRuntimeFixture(t)
	ctx := context.Background()
	if _, err := InstallCodexCaptureHooks(ctx, i.Options.ClientRoot, i.Connection.ID, true); err == nil {
		t.Fatal("hooks installed without collection consent")
	}
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(i.Options.CodexDirectory, "hooks.json")
	baseline := []byte(`{"description":"unrelated metadata","hooks":{"Stop":[{"matcher":"*","hooks":[{"type":"command","command":"third-party-hook"}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"another-hook"}]}]}}`)
	if err = os.WriteFile(path, baseline, 0600); err != nil {
		t.Fatal(err)
	}
	i, err = InstallCodexCaptureHooks(ctx, i.Options.ClientRoot, i.Connection.ID, true)
	if err != nil || i.CaptureHooks == nil {
		t.Fatal(i, err)
	}
	if backup, err := os.ReadFile(i.CaptureHooks.Backup); err != nil || string(backup) != string(baseline) {
		t.Fatal("original hooks backup unavailable", err)
	}
	i, err = InstallCodexCaptureHooks(ctx, i.Options.ClientRoot, i.Connection.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Description string
		Hooks       map[string][]json.RawMessage
	}
	if err = json.Unmarshal(raw, &doc); err != nil || doc.Description != "unrelated metadata" || len(doc.Hooks["Stop"]) != 2 || len(doc.Hooks["UserPromptSubmit"]) != 1 || len(doc.Hooks["PreToolUse"]) != 1 {
		t.Fatal("installation duplicated or replaced unrelated hooks", string(raw), err)
	}
	if err = Uninstall(ctx, i.Options.ClientRoot, i.Connection.ID); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.Hooks = nil
	if err = json.Unmarshal(raw, &doc); err != nil || len(doc.Hooks["Stop"]) != 1 || len(doc.Hooks["UserPromptSubmit"]) != 0 || len(doc.Hooks["PreToolUse"]) != 1 {
		t.Fatal("disconnect removed foreign hooks", string(raw), err)
	}
}

func TestCodexCaptureModifiedHookIsPreservedAndConsentRevokedBeforeCleanup(t *testing.T) {
	i, _ := captureRuntimeFixture(t)
	ctx := context.Background()
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	i, err = InstallCodexCaptureHooks(ctx, i.Options.ClientRoot, i.Connection.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(i.CaptureHooks.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	group := hooks["Stop"].([]any)[0].(map[string]any)
	group["hooks"].([]any)[0].(map[string]any)["timeout"] = 99
	changed, _ := json.Marshal(doc)
	if err = os.WriteFile(i.CaptureHooks.Path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = Uninstall(ctx, i.Options.ClientRoot, i.Connection.ID); err == nil {
		t.Fatal("externally modified hook removed")
	}
	current, err := os.ReadFile(i.CaptureHooks.Path)
	if err != nil || string(current) != string(changed) {
		t.Fatal("externally modified configuration overwritten", err)
	}
	i, err = LoadInstallation(i.Options.ClientRoot, i.Connection.ID)
	if err != nil || i.CaptureConsent == nil || i.CaptureConsent.Enabled {
		t.Fatal("failed cleanup left collection authorized", err)
	}
}

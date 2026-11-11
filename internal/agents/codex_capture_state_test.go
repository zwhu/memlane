package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureConsentSurvivesLegacyInstallationRewriteWithoutReviving(t *testing.T) {
	i, _ := captureRuntimeFixture(t)
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	i, err = InstallCodexCaptureHooks(context.Background(), i.Options.ClientRoot, i.Connection.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	consent := *i.CaptureConsent
	hooks := *i.CaptureHooks
	path := installationPath(i.Options.ClientRoot, i.Connection.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var oldWriter map[string]json.RawMessage
	if err = json.Unmarshal(raw, &oldWriter); err != nil {
		t.Fatal(err)
	}
	if _, ok := oldWriter["capture_consent"]; ok {
		t.Fatal("collection authority still stored in daemon-owned metadata")
	}
	// A legacy writer knows only its original fields and changes unrelated
	// installation diagnostics. It never reads or rewrites capture-state.json.
	oldWriter["agent_version"] = json.RawMessage(`"codex-cli 9.0.0"`)
	raw, _ = json.Marshal(oldWriter)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	i, err = LoadInstallation(i.Options.ClientRoot, i.Connection.ID)
	if err != nil || i.CaptureConsent == nil || *i.CaptureConsent != consent || i.CaptureHooks == nil || i.CaptureHooks.Path != hooks.Path {
		t.Fatal("legacy rewrite lost independent authorization", err)
	}
	if _, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, false, true); err != nil {
		t.Fatal(err)
	}
	// Even a stale enabled value in installation.json cannot revive consent.
	enabled, _ := json.Marshal(consent)
	oldWriter["capture_consent"] = enabled
	raw, _ = json.Marshal(oldWriter)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	i, err = LoadInstallation(i.Options.ClientRoot, i.Connection.ID)
	if err != nil || i.CaptureConsent == nil || i.CaptureConsent.Enabled {
		t.Fatal("stale metadata revived collection", err)
	}
	// A legacy disconnect can leave a sidecar. Reconnecting the identity must
	// retire it, preserving all original event files rather than reauthorizing.
	config, mode, err := readConfig(i.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = privateWrite(i.ConfigPath, bytes.Replace(config, []byte(i.Block), nil, 1), mode); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	i, err = Install(context.Background(), i.Connection, i.Options)
	if err != nil || i.CaptureConsent != nil {
		t.Fatal("legacy disconnect revived consent on reinstall", err)
	}
}

func TestCaptureStateIsBoundToConfigLocationAndRejectsLinks(t *testing.T) {
	i, _ := captureRuntimeFixture(t)
	var err error
	i, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	path := codexCaptureStatePath(i.Options.ClientRoot, i.Connection.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state codexCaptureState
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	state.Directory = filepath.Join(t.TempDir(), "another-home")
	raw, _ = json.Marshal(state)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadInstallation(i.Options.ClientRoot, i.Connection.ID); err == nil {
		t.Fatal("consent crossed Codex configuration location")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "external.json")
	if err = os.WriteFile(target, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadInstallation(i.Options.ClientRoot, i.Connection.ID); err == nil {
		t.Fatal("linked authorization accepted")
	}
}

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type CodexCaptureHooks struct {
	Path        string          `json:"path"`
	Backup      string          `json:"backup,omitempty"`
	Definition  json.RawMessage `json:"definition"`
	InstalledAt string          `json:"installed_at"`
}

type captureHelpOutput struct{ bytes.Buffer }

func (b *captureHelpOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 32768 {
		return 0, errors.New("capture_bridge_help_budget")
	}
	return b.Buffer.Write(p)
}

func captureShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// Preserve unrelated keys and handlers; remove only an exact owned group.
func editCodexCaptureHooks(raw, previous, replacement []byte) ([]byte, error) {
	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &doc) != nil {
		return nil, errors.New("capture_hooks_invalid")
	}
	if doc == nil {
		return nil, errors.New("capture_hooks_invalid")
	}
	hooks := map[string][]json.RawMessage{}
	if value, ok := doc["hooks"]; ok && json.Unmarshal(value, &hooks) != nil {
		return nil, errors.New("capture_hooks_invalid")
	}
	if hooks == nil {
		return nil, errors.New("capture_hooks_invalid")
	}
	equal := func(a, b []byte) bool {
		var x, y any
		return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
	}
	for _, event := range []string{"UserPromptSubmit", "Stop"} {
		kept := []json.RawMessage{}
		found := false
		for _, group := range hooks[event] {
			if len(previous) > 0 && equal(group, previous) || len(replacement) > 0 && equal(group, replacement) {
				found = true
				continue
			}
			kept = append(kept, group)
		}
		if len(previous) > 0 && !found {
			var owned struct{ Hooks []struct{ Command string } }
			_ = json.Unmarshal(previous, &owned)
			for _, group := range kept {
				var candidate struct{ Hooks []struct{ Command string } }
				if json.Unmarshal(group, &candidate) != nil {
					return nil, errors.New("capture_hooks_invalid")
				}
				for _, a := range owned.Hooks {
					for _, b := range candidate.Hooks {
						if a.Command != "" && a.Command == b.Command {
							return nil, errors.New("capture_hooks_changed_externally")
						}
					}
				}
			}
		}
		if len(replacement) > 0 {
			kept = append(kept, json.RawMessage(replacement))
		}
		if len(kept) > 0 {
			hooks[event] = kept
		} else {
			delete(hooks, event)
		}
	}
	encoded, err := json.Marshal(hooks)
	if err != nil {
		return nil, err
	}
	doc["hooks"] = encoded
	out, err := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n'), err
}

// Definition installation is separate from Codex trust/reload and delivery.
// Never changes trust storage or adds a trust-bypass flag.
func InstallCodexCaptureHooks(ctx context.Context, root, connection string, confirmed bool, captureBridge ...string) (Installation, error) {
	installationMu.Lock()
	defer installationMu.Unlock()
	var empty Installation
	if !confirmed {
		return empty, errors.New("capture_install_confirmation_required")
	}
	owner, err := lockCodexCapture(ctx, root, connection)
	if err != nil {
		return empty, err
	}
	defer owner.Close()
	i, err := LoadInstallation(root, connection)
	if err != nil {
		return empty, err
	}
	if i.Connection.Agent != "codex" || !i.Options.ComputerLevel || !i.Connection.Enabled || i.CaptureConsent == nil || !i.CaptureConsent.Enabled {
		return empty, errors.New("capture_not_authorized")
	}
	if err = CheckInstallation(i); err != nil {
		return empty, err
	}
	bridge := i.Options.Bridge
	if len(captureBridge) > 1 {
		return empty, errors.New("capture_bridge_invalid")
	}
	if len(captureBridge) == 1 {
		bridge = captureBridge[0]
	}
	if !filepath.IsAbs(bridge) {
		return empty, errors.New("capture_bridge_invalid")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, bridge, "capture-codex", "--help")
	var help captureHelpOutput
	probe.Stdout = &help
	probe.Stderr = io.Discard
	probe.WaitDelay = time.Second
	_ = probe.Run() // Flag help may return a nonzero status; inspect capability.
	if probeCtx.Err() != nil || !strings.Contains(help.String(), "client-root") || !strings.Contains(help.String(), "connection") {
		return empty, errors.New("capture_bridge_unavailable")
	}
	command := strings.Join([]string{captureShellQuote(bridge), "capture-codex", "--client-root", captureShellQuote(root), "--connection", captureShellQuote(connection)}, " ")
	definition, _ := json.Marshal(map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}})
	path := filepath.Join(i.Options.CodexDirectory, "hooks.json")
	raw, mode, err := readConfig(path)
	if err != nil {
		return empty, err
	}
	var previous []byte
	if i.CaptureHooks != nil {
		if i.CaptureHooks.Path != path {
			return empty, errors.New("capture_hooks_location_changed")
		}
		previous = i.CaptureHooks.Definition
	}
	updated, err := editCodexCaptureHooks(raw, previous, definition)
	if err != nil {
		return empty, err
	}
	current, _, err := readConfig(path)
	if err != nil || !bytes.Equal(current, raw) {
		return empty, errors.New("capture_hooks_changed_externally")
	}
	backupPath, err := backup(path, raw)
	if err != nil {
		return empty, err
	}
	if err = privateWrite(path, updated, mode); err != nil {
		return empty, err
	}
	i.CaptureHooks = &CodexCaptureHooks{Path: path, Backup: backupPath, Definition: definition, InstalledAt: time.Now().UTC().Format(time.RFC3339Nano)}
	err = saveCodexCaptureState(root, i)
	if err != nil {
		if current, _, e := readConfig(path); e == nil && bytes.Equal(current, updated) {
			_ = privateWrite(path, raw, mode)
		}
		return empty, errors.New("capture_hooks_receipt_unavailable")
	}
	return i, nil
}

func removeCodexCaptureHooks(i Installation) error {
	if i.CaptureHooks == nil {
		return nil
	}
	path := filepath.Join(i.Options.CodexDirectory, "hooks.json")
	if path != i.CaptureHooks.Path {
		return errors.New("capture_hooks_location_changed")
	}
	raw, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	updated, err := editCodexCaptureHooks(raw, i.CaptureHooks.Definition, nil)
	if err != nil {
		return err
	}
	current, _, err := readConfig(path)
	if err != nil || !bytes.Equal(current, raw) {
		return errors.New("capture_hooks_changed_externally")
	}
	if _, err = backup(path, raw); err != nil {
		return err
	}
	return privateWrite(path, updated, mode)
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func invoke(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runMain(context.Background(), args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHeadlessDispatch(t *testing.T) {
	for _, arg := range []string{"--help", "help", "-h"} {
		code, out, notes := invoke(t, "", arg)
		if code != 0 || notes != "" {
			t.Fatalf("%d %q %q", code, out, notes)
		}
		for _, command := range []string{"app", "daemon", "hook", "setup", "dev"} {
			if !strings.Contains(out, command) {
				t.Fatal(out)
			}
		}
	}
	if code, _, _ := invoke(t, "", "daemon", "--help"); code != 0 {
		t.Fatal(code)
	}
	t.Setenv("MEMLANE_PROCESSING_SESSION", "1")
	if code, out, notes := invoke(t, "invalid", "hook", "pi", "recall"); code != 0 || out != "{}\n" || notes != "" {
		t.Fatalf("%d %q %q", code, out, notes)
	}
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	if code, out, notes := invoke(t, `{"prompt":"q"}`, "hook", "codex", "UserPromptSubmit",
		"--gateway-url", "http://127.0.0.1:1", "--key-file", filepath.Join(t.TempDir(), "missing")); code != 0 || out != "{}\n" || notes != "" {
		t.Fatalf("%d %q %q", code, out, notes)
	}
}

func TestSetupDoctorCompatibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEMLANE_DEV_RUNTIME_CONFIG", "")
	for _, prefix := range [][]string{{"doctor"}, {"setup", "doctor"}} {
		begin := time.Now()
		code, out, notes := invoke(t, "", append(prefix, "--json")...)
		elapsed := time.Since(begin)
		var body struct {
			SchemaVersion int `json:"schema_version"`
			Blockers      []struct {
				Code string `json:"code"`
			} `json:"blockers"`
		}
		require(t, json.Unmarshal([]byte(out), &body))
		if code != 10 || notes != "" || elapsed >= 5*time.Second || body.SchemaVersion != 1 {
			t.Fatalf("%v: code=%d elapsed=%v schema=%d blockers=%v stderr=%q", prefix, code, elapsed, body.SchemaVersion, body.Blockers, notes)
		}
	}
}

func TestDevStatusRecoverAndUninstallPreview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := filepath.Join(dir, "runtime.json")
	raw, err := json.Marshal(map[string]string{"store_root": dir, "gateway_url": "http://127.0.0.1:18790"})
	require(t, err)
	require(t, os.WriteFile(cfg, raw, 0o600))
	for _, tc := range []struct {
		args  []string
		field string
		value any
	}{
		{[]string{"dev", "status"}, "status", "stopped"},
		{[]string{"dev", "recover"}, "status", "already_released"},
		{[]string{"setup", "uninstall", "--host", "codex", "--dry-run"}, "applied", false},
	} {
		code, out, notes := invoke(t, "", append(tc.args, "--config", cfg, "--json")...)
		if code != 0 || notes != "" {
			t.Fatalf("%d %q %q", code, out, notes)
		}
		var body map[string]any
		require(t, json.Unmarshal([]byte(out), &body))
		if body[tc.field] != tc.value {
			t.Fatal(body)
		}
	}
}

func TestHookDispatchForwarding(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recall" || r.Header.Get("X-Api-Key") != "agent-test" {
			t.Error("wrong CLI hook forwarding")
		}
		if _, err := io.WriteString(w, `{"hits":[{"title":"hook context"}]}`); err != nil {
			t.Error(err)
		}
	})
	gateway := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	gateway.Start()
	defer gateway.Close()
	key := filepath.Join(t.TempDir(), "key")
	require(t, os.WriteFile(key, []byte("agent-test"), 0o600))
	t.Setenv("MEMLANE_GATEWAY_URL", gateway.URL)
	t.Setenv("MEMLANE_AGENT_KEY_FILE", key)
	code, out, notes := invoke(t, `{"prompt":"help","cwd":"/tmp"}`, "hook", "antigravity", "UserPromptSubmit")
	if code != 0 || notes != "" || !strings.Contains(out, "hook context") {
		t.Fatalf("%d %q %q", code, out, notes)
	}
}

func TestHookSessionStartRuntimeFallback(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	t.Setenv("MEMLANE_GATEWAY_URL", "")
	t.Setenv("MEMLANE_AGENT_KEY_FILE", "")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	requests := make(chan map[string]any, 4)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recall" || r.Header.Get("X-Api-Key") != "runtime-codex-key" {
			t.Errorf("unexpected request %s key=%q", r.URL.Path, r.Header.Get("X-Api-Key"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		if _, err := io.WriteString(w, `{"context":"restored via runtime fallback"}`); err != nil {
			t.Error(err)
		}
	})
	gateway := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	gateway.Start()
	defer gateway.Close()

	dir := t.TempDir()
	key := filepath.Join(dir, "codex.key")
	require(t, os.WriteFile(key, []byte("runtime-codex-key\n"), 0o600))
	cfgPath := filepath.Join(dir, "runtime.json")
	cfgBytes, err := json.Marshal(map[string]any{
		"gateway_url":     gateway.URL,
		"agent_key_files": map[string]string{"codex": key},
	})
	require(t, err)
	require(t, os.WriteFile(cfgPath, cfgBytes, 0o600))
	t.Setenv("MEMLANE_DEV_RUNTIME_CONFIG", cfgPath)

	code, out, notes := invoke(t, `{"source":"startup","cwd":"/tmp","session_id":"s1"}`, "hook", "codex", "SessionStart")
	if code != 0 || out != "{}\n" || notes != "" || len(requests) != 0 {
		t.Fatalf("startup must remain a zero-network no-op: code=%d out=%q notes=%q reqs=%d", code, out, notes, len(requests))
	}

	code, out, notes = invoke(t, `{"source":"compact","cwd":"/tmp","session_id":"s1"}`, "hook", "codex", "SessionStart")
	if code != 0 || notes != "" || !strings.Contains(out, "restored via runtime fallback") {
		t.Fatalf("compact restore failed: code=%d out=%q notes=%q", code, out, notes)
	}
	select {
	case got := <-requests:
		if got["purpose"] != "compact_restore" || got["mode"] != "hybrid" {
			t.Fatalf("unexpected recall body: %+v", got)
		}
	default:
		t.Fatal("expected /recall request on SessionStart compact")
	}
}

func TestDevSyncHooksDryRunApplyAndIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	storeRoot := filepath.Join(t.TempDir(), "official-store")
	codexProfile := filepath.Join(home, ".codex")
	require(t, os.MkdirAll(codexProfile, 0o700))
	require(t, os.MkdirAll(filepath.Join(storeRoot, "agent-keys"), 0o700))

	// Create deterministic existing key for codex (leave traex unconnected).
	sum := sha256.Sum256([]byte(codexProfile))
	codexKey := filepath.Join(storeRoot, "agent-keys", "codex-"+hex.EncodeToString(sum[:12])+".token")
	require(t, os.WriteFile(codexKey, []byte("codex-secret\n"), 0o600))

	fakeBin := filepath.Join(t.TempDir(), "Memlane")
	require(t, os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	cfgPath := filepath.Join(t.TempDir(), "runtime.json")
	cfgRaw, err := json.Marshal(map[string]string{
		"store_root":  storeRoot,
		"gateway_url": "http://127.0.0.1:18777",
	})
	require(t, err)
	require(t, os.WriteFile(cfgPath, cfgRaw, 0o600))

	// Seed an outdated codex hooks.json missing PreCompact plus an unrelated third-party hook.
	hooksPath := filepath.Join(codexProfile, "hooks.json")
	initial := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo third-party"}]}]}}`
	require(t, os.WriteFile(hooksPath, []byte(initial), 0o600))

	// 1. Dry-run previews changes without mutating hooks.json.
	code, out, notes := invoke(t, "", "dev", "sync-hooks", "--config", cfgPath, "--binary", fakeBin, "--dry-run", "--json")
	if code != 0 || notes != "" {
		t.Fatalf("dry-run failed: code=%d out=%q stderr=%q", code, out, notes)
	}
	var dry struct {
		Applied bool `json:"applied"`
		Hosts   []struct {
			Host    string   `json:"host"`
			Changed bool     `json:"changed"`
			Applied bool     `json:"applied"`
			Files   []string `json:"files"`
		} `json:"hosts"`
	}
	require(t, json.Unmarshal([]byte(out), &dry))
	if dry.Applied || len(dry.Hosts) != 1 || dry.Hosts[0].Host != "codex" || !dry.Hosts[0].Changed || dry.Hosts[0].Applied {
		t.Fatalf("unexpected dry-run result: %+v", dry)
	}
	afterDry, err := os.ReadFile(hooksPath)
	require(t, err)
	if string(afterDry) != initial {
		t.Fatalf("dry-run mutated hooks.json: %s", afterDry)
	}

	// 2. Apply writes PreCompact while preserving unrelated third-party hooks and existing key.
	code, out, notes = invoke(t, "", "dev", "sync-hooks", "--config", cfgPath, "--host", "codex", "--binary", fakeBin, "--apply", "--json")
	if code != 0 || notes != "" {
		t.Fatalf("apply failed: code=%d out=%q stderr=%q", code, out, notes)
	}
	var applied struct {
		Applied bool `json:"applied"`
		Hosts   []struct {
			Host    string `json:"host"`
			Changed bool   `json:"changed"`
			Applied bool   `json:"applied"`
		} `json:"hosts"`
	}
	require(t, json.Unmarshal([]byte(out), &applied))
	if !applied.Applied || len(applied.Hosts) != 1 || !applied.Hosts[0].Applied {
		t.Fatalf("unexpected apply result: %+v", applied)
	}
	written, err := os.ReadFile(hooksPath)
	require(t, err)
	if !strings.Contains(string(written), `"PreCompact"`) || !strings.Contains(string(written), "echo third-party") || !strings.Contains(string(written), fakeBin) {
		t.Fatalf("unexpected hooks.json after apply: %s", written)
	}

	// 3. Repeat dry-run is idempotent (changed=false).
	code, out, notes = invoke(t, "", "dev", "sync-hooks", "--config", cfgPath, "--host", "codex", "--binary", fakeBin, "--dry-run", "--json")
	if code != 0 || notes != "" {
		t.Fatalf("repeat dry-run failed: code=%d out=%q stderr=%q", code, out, notes)
	}
	require(t, json.Unmarshal([]byte(out), &dry))
	if len(dry.Hosts) != 1 || dry.Hosts[0].Changed {
		t.Fatalf("expected idempotent sync, got: %+v", dry)
	}

	// 4. Unconnected host explicitly requested returns non-zero without minting a key.
	code, _, _ = invoke(t, "", "dev", "sync-hooks", "--config", cfgPath, "--host", "traex", "--binary", fakeBin, "--dry-run", "--json")
	if code == 0 {
		t.Fatal("expected error for unconnected host without existing key file")
	}
}

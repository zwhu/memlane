package main

import (
	"bytes"
	"context"
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

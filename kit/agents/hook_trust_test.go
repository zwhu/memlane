package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeFakeAppServerScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeHostHookTrustAppServerCodexAndTraeX(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")

	codexProfile := filepath.Join(root, ".codex")
	if err := os.MkdirAll(codexProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	codexHooksPath := filepath.Join(codexProfile, "hooks.json")
	codexHooksContent := `{
  "hooks": {
    "SessionStart": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex SessionStart --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "UserPromptSubmit": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex UserPromptSubmit --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "PostToolUse": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex PostToolUse --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "PostToolUseFailure": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex PostToolUseFailure --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "Stop": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "PreCompact": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex PreCompact --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}],
    "SessionEnd": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex SessionEnd --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]
  }
}`
	if err := os.WriteFile(codexHooksPath, []byte(codexHooksContent), 0o600); err != nil {
		t.Fatal(err)
	}
	codexTOMLPath := filepath.Join(codexProfile, "config.toml")
	codexTOMLContent := "model = \"gpt-5.4\"\n"
	if err := os.WriteFile(codexTOMLPath, []byte(codexTOMLContent), 0o600); err != nil {
		t.Fatal(err)
	}

	codexResponse, err := json.Marshal(map[string]any{
		"id": 2,
		"result": map[string]any{
			"data": []any{
				map[string]any{
					"cwd": "/tmp",
					"hooks": []any{
						// System/third-party hook must be ignored.
						map[string]any{
							"key":         "/Library/CorpLink/resources:pre_tool_use:0:0",
							"eventName":   "preToolUse",
							"command":     "'/Library/CorpLink/resources/corplink-bridge' hook --agent 'codex' --event 'PreToolUse'",
							"sourcePath":  "/Library/CorpLink/resources",
							"enabled":     true,
							"trustStatus": "untrusted",
						},
						// Third-party hook inside the same hooks.json must also be ignored.
						map[string]any{
							"key":         codexHooksPath + ":stop:1:0",
							"eventName":   "stop",
							"command":     "/opt/flux/flux-hook stop",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "untrusted",
						},
						// 1 trusted + 3 untrusted + 2 modified + 1 disabled (trusted but enabled=false).
						map[string]any{
							"key":         codexHooksPath + ":session_start:0:0",
							"eventName":   "sessionStart",
							"command":     "'/bin/memlane' hook codex SessionStart --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "trusted",
						},
						map[string]any{
							"key":         codexHooksPath + ":user_prompt_submit:0:0",
							"eventName":   "userPromptSubmit",
							"command":     "'/bin/memlane' hook codex UserPromptSubmit --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "untrusted",
						},
						map[string]any{
							"key":         codexHooksPath + ":post_tool_use:0:0",
							"eventName":   "postToolUse",
							"command":     "'/bin/memlane' hook codex PostToolUse --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "untrusted",
						},
						map[string]any{
							"key":         codexHooksPath + ":post_tool_use_failure:0:0",
							"eventName":   "postToolUseFailure",
							"command":     "'/bin/memlane' hook codex PostToolUseFailure --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "untrusted",
						},
						map[string]any{
							"key":         codexHooksPath + ":stop:0:0",
							"eventName":   "stop",
							"command":     "'/bin/memlane' hook codex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     true,
							"trustStatus": "modified",
						},
						map[string]any{
							"key":         codexHooksPath + ":pre_compact:0:0",
							"eventName":   "preCompact",
							"command":     "'/bin/memlane' hook codex PreCompact --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     false,
							"trustStatus": "modified",
						},
						map[string]any{
							"key":         codexHooksPath + ":session_end:0:0",
							"eventName":   "sessionEnd",
							"command":     "'/bin/memlane' hook codex SessionEnd --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  codexHooksPath,
							"enabled":     false,
							"trustStatus": "trusted",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	codexRespFile := filepath.Join(root, "codex_resp.json")
	if err := os.WriteFile(codexRespFile, codexResponse, 0o600); err != nil {
		t.Fatal(err)
	}

	codexScript := fmt.Sprintf(`
if [ "$1" != "app-server" ]; then exit 2; fi
if [ "$CODEX_HOME" != %q ]; then exit 3; fi
if [ -n "$TRAE_HOME" ] || [ -n "$TRAECLI_HOME" ]; then exit 4; fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%%s\n' '{"method":"remoteControl/status/changed","params":{}}'
      printf '%%s\n' '{"id":1,"result":{"codexHome":"%s"}}'
      ;;
    *'"method":"hooks/list"'*)
      IFS= read -r resp < %q
      printf '%%s\n' "$resp"
      ;;
  esac
done
`, codexProfile, codexProfile, codexRespFile)
	codexBin := writeFakeAppServerScript(t, binDir, "codex", codexScript)

	report := ProbeHostHookTrust(context.Background(), HostCodex, codexBin, codexProfile, []string{codexHooksPath})
	if !report.Evaluated || report.Source != "app_server" || !report.NeedsTrust() {
		t.Fatalf("unexpected codex trust report: %+v", report)
	}
	if report.TotalManaged != 7 || report.TrustedCount != 1 || report.UntrustedCount != 3 || report.ModifiedCount != 2 || report.DisabledCount != 1 {
		t.Fatalf("unexpected codex counts: total=%d trusted=%d untrusted=%d modified=%d disabled=%d",
			report.TotalManaged, report.TrustedCount, report.UntrustedCount, report.ModifiedCount, report.DisabledCount)
	}
	wantEvents := []string{"UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "Stop", "PreCompact", "SessionEnd"}
	if !slices.Equal(report.UntrustedEvents, wantEvents) {
		t.Fatalf("UntrustedEvents = %v, want %v", report.UntrustedEvents, wantEvents)
	}
	// Strictly read-only check: config files must remain untouched byte-for-byte.
	if got, err := os.ReadFile(codexHooksPath); err != nil || string(got) != codexHooksContent {
		t.Fatalf("hooks.json was mutated during probe")
	}
	if got, err := os.ReadFile(codexTOMLPath); err != nil || string(got) != codexTOMLContent {
		t.Fatalf("config.toml was mutated during probe")
	}

	// Verify TRAE CLI receives TRAE_HOME (and never TRAECLI_HOME) and handles all-trusted cleanly.
	traeProfile := filepath.Join(root, ".trae")
	traeHooksPath := filepath.Join(traeProfile, "cli", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(traeHooksPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traeHooksPath, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	traeResp, _ := json.Marshal(map[string]any{
		"id": 2,
		"result": map[string]any{
			"data": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"key":         traeHooksPath + ":stop:0:0",
							"eventName":   "stop",
							"command":     "'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'",
							"sourcePath":  traeHooksPath,
							"enabled":     true,
							"trustStatus": "trusted",
						},
					},
				},
			},
		},
	})
	traeRespFile := filepath.Join(root, "trae_resp.json")
	if err := os.WriteFile(traeRespFile, traeResp, 0o600); err != nil {
		t.Fatal(err)
	}
	traeScript := fmt.Sprintf(`
if [ "$1" != "app-server" ]; then exit 2; fi
if [ "$TRAE_HOME" != %q ]; then exit 3; fi
if [ -n "$TRAECLI_HOME" ] || [ -n "$CODEX_HOME" ]; then exit 4; fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*) IFS= read -r resp < %q; printf '%%s\n' "$resp" ;;
  esac
done
`, traeProfile, traeRespFile)
	traeBin := writeFakeAppServerScript(t, binDir, "traecli", traeScript)

	traeReport := ProbeHostHookTrust(context.Background(), HostTraeX, traeBin, traeProfile, nil)
	if !traeReport.Evaluated || traeReport.Source != "app_server" || traeReport.NeedsTrust() ||
		traeReport.TotalManaged != 1 || traeReport.TrustedCount != 1 {
		t.Fatalf("unexpected trusted traex report: %+v", traeReport)
	}
}

func TestProbeHostHookTrustAppServerTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profile := filepath.Join(root, ".codex")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(profile, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"/bin/memlane hook codex Stop --gateway-url http://127.0.0.1:18174 --key-file /tmp/k"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "child.pid")
	hangBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex", fmt.Sprintf(`
sleep 30 &
echo $! > %q
wait
`, pidFile))

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	report := ProbeHostHookTrust(ctx, HostCodex, hangBin, profile, []string{hooksPath})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe took %v, exceeded context timeout", elapsed)
	}
	if report.Source != "none" || report.NeedsTrust() {
		t.Fatalf("expected conservative none fallback without [hooks.state], got %+v", report)
	}
	if rawPID, err := os.ReadFile(pidFile); err == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(rawPID))); convErr == nil && pid > 0 {
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child process %d survived app-server probe cleanup", pid)
		}
	}
}

func TestProbeHostHookTrustTOMLFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profile := filepath.Join(root, ".trae")
	hooksPath := filepath.Join(profile, "cli", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
		t.Fatal(err)
	}
	hooksDoc := `{
  "hooks": {
    "SessionStart": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex SessionStart --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'"}]}],
    "UserPromptSubmit": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex UserPromptSubmit --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'"}]}],
    "PostToolUse": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex PostToolUse --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'"}]}],
    "Stop": [{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'"}]}]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(hooksDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	stubBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "traecli", "exit 0")

	// 1. Without [hooks.state], fallback is conservative and returns Source="none".
	tomlPath := filepath.Join(profile, "traecli.toml")
	if err := os.WriteFile(tomlPath, []byte("model = \"doubao-seed-code\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noneReport := ProbeHostHookTrust(context.Background(), HostTraeX, stubBin, profile, []string{hooksPath})
	if noneReport.Evaluated || noneReport.Source != "none" || noneReport.NeedsTrust() {
		t.Fatalf("expected conservative none report without [hooks.state], got %+v", noneReport)
	}

	// 2. With [hooks.state]: 1 trusted, 1 disabled (enabled=false), 1 missing trusted_hash, 1 absent from table.
	canonHooks := canonicalHookPath(hooksPath)
	tomlWithState := fmt.Sprintf(`
[hooks.state.%q]
enabled = true
trusted_hash = "sha256:aaa111"

[hooks.state.%q]
enabled = false
trusted_hash = "sha256:bbb222"

[hooks.state.%q]
enabled = true
`, canonHooks+":session_start:0:0", canonHooks+":user_prompt_submit:0:0", canonHooks+":post_tool_use:0:0")
	if err := os.WriteFile(tomlPath, []byte(tomlWithState), 0o600); err != nil {
		t.Fatal(err)
	}
	fallbackReport := ProbeHostHookTrust(context.Background(), HostTraeX, stubBin, profile, []string{hooksPath})
	if !fallbackReport.Evaluated || fallbackReport.Source != "toml_fallback" || !fallbackReport.NeedsTrust() {
		t.Fatalf("unexpected toml fallback report: %+v", fallbackReport)
	}
	if fallbackReport.TotalManaged != 4 || fallbackReport.TrustedCount != 1 || fallbackReport.UntrustedCount != 2 || fallbackReport.DisabledCount != 1 {
		t.Fatalf("unexpected fallback counts: total=%d trusted=%d untrusted=%d disabled=%d",
			fallbackReport.TotalManaged, fallbackReport.TrustedCount, fallbackReport.UntrustedCount, fallbackReport.DisabledCount)
	}

	// 3. When all 4 entries have trusted_hash and enabled=true, fallback reports all trusted.
	tomlAllTrusted := fmt.Sprintf(`
[hooks.state.%q]
enabled = true
trusted_hash = "sha256:aaa111"

[hooks.state.%q]
enabled = true
trusted_hash = "sha256:bbb222"

[hooks.state.%q]
enabled = true
trusted_hash = "sha256:ccc333"

[hooks.state.%q]
enabled = true
trusted_hash = "sha256:ddd444"
`, canonHooks+":session_start:0:0", canonHooks+":user_prompt_submit:0:0", canonHooks+":post_tool_use:0:0", canonHooks+":stop:0:0")
	if err := os.WriteFile(tomlPath, []byte(tomlAllTrusted), 0o600); err != nil {
		t.Fatal(err)
	}
	trustedReport := ProbeHostHookTrust(context.Background(), HostTraeX, stubBin, profile, []string{hooksPath})
	if !trustedReport.Evaluated || trustedReport.Source != "toml_fallback" || trustedReport.NeedsTrust() || trustedReport.TrustedCount != 4 {
		t.Fatalf("expected all-trusted fallback report, got %+v", trustedReport)
	}
}

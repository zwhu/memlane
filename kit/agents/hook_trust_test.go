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
	"sync"
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

func TestProbeHostHookTrustIgnoresThirdPartyOnlyHooks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profile := filepath.Join(root, ".codex")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(profile, "hooks.json")
	thirdPartyHooks := `{
  "hooks": {
    "Stop": [{"hooks":[{"type":"command","command":"/opt/flux/flux-hook stop"}]}],
    "SessionStart": [{"hooks":[{"type":"command","command":"/usr/local/bin/other-tool start"}]}]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(thirdPartyHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	canonHooks := canonicalHookPath(hooksPath)
	tomlPath := filepath.Join(profile, "config.toml")
	tomlWithUntrustedThirdParty := fmt.Sprintf(`
[hooks.state.%q]
enabled = false

[hooks.state.%q]
enabled = true
`, canonHooks+":stop:0:0", canonHooks+":session_start:0:0")
	if err := os.WriteFile(tomlPath, []byte(tomlWithUntrustedThirdParty), 0o600); err != nil {
		t.Fatal(err)
	}

	if HasManagedHostHooks(HostCodex, profile, []string{hooksPath}) {
		t.Fatalf("HasManagedHostHooks must be false for third-party-only hooks.json")
	}

	// 1. Working app-server returns only third-party untrusted/disabled hooks -> must return
	// Evaluated:true, Source:"app_server", TotalManaged:0, NeedsTrust:false without falling into TOML.
	respRaw, err := json.Marshal(map[string]any{
		"id": 2,
		"result": map[string]any{
			"data": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"key":         hooksPath + ":stop:0:0",
							"eventName":   "stop",
							"command":     "/opt/flux/flux-hook stop",
							"sourcePath":  hooksPath,
							"enabled":     false,
							"trustStatus": "untrusted",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	respFile := filepath.Join(root, "third_party_resp.json")
	if err := os.WriteFile(respFile, respRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	codexBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex", fmt.Sprintf(`
if [ "$1" != "app-server" ]; then exit 2; fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*) IFS= read -r resp < %q; printf '%%s\n' "$resp" ;;
  esac
done
`, respFile))

	appReport := ProbeHostHookTrust(context.Background(), HostCodex, codexBin, profile, []string{hooksPath})
	if !appReport.Evaluated || appReport.Source != "app_server" || appReport.TotalManaged != 0 || appReport.NeedsTrust() {
		t.Fatalf("app-server with zero managed hooks fell back or misclassified third-party hook: %+v", appReport)
	}

	// 2. Unavailable/failing app-server must NOT blind-scan TOML [hooks.state] when zero Memlane hooks exist.
	stubBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex-stub", "exit 0")
	tomlReport := ProbeHostHookTrust(context.Background(), HostCodex, stubBin, profile, []string{hooksPath})
	if tomlReport.Evaluated || tomlReport.Source != "none" || tomlReport.TotalManaged != 0 || tomlReport.NeedsTrust() {
		t.Fatalf("TOML fallback misclassified third-party hooks as Memlane: %+v", tomlReport)
	}
}

func TestProbeHostHookTrustAppServerGracefulExitOnStdinEOF(t *testing.T) {
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
	respRaw, err := json.Marshal(map[string]any{
		"id": 2,
		"result": map[string]any{
			"data": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"key":         hooksPath + ":stop:0:0",
							"eventName":   "stop",
							"command":     "/bin/memlane hook codex Stop --gateway-url http://127.0.0.1:18174 --key-file /tmp/k",
							"sourcePath":  hooksPath,
							"enabled":     true,
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
	respFile := filepath.Join(root, "graceful_resp.json")
	if err := os.WriteFile(respFile, respRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	arg0Dir := filepath.Join(profile, "tmp", "arg0-test")
	cleanMarker := filepath.Join(root, "clean_exit.txt")
	// Simulates codex/traecli creating tmp/arg0*/.lock and cleaning it on stdin EOF.
	// A zero-wait SIGKILL would abort before sleep+cleanup could run.
	gracefulBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex", fmt.Sprintf(`
if [ "$1" != "app-server" ]; then exit 2; fi
mkdir -p %q
touch %q
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*) IFS= read -r resp < %q; printf '%%s\n' "$resp" ;;
  esac
done
sleep 0.05
rm -rf %q
printf 'clean_exit\n' > %q
`, arg0Dir, filepath.Join(arg0Dir, ".lock"), respFile, arg0Dir, cleanMarker))

	report := ProbeHostHookTrust(context.Background(), HostCodex, gracefulBin, profile, []string{hooksPath})
	if !report.Evaluated || report.Source != "app_server" || report.NeedsTrust() || report.TrustedCount != 1 {
		t.Fatalf("unexpected graceful report: %+v", report)
	}
	markerBytes, err := os.ReadFile(cleanMarker)
	if err != nil || strings.TrimSpace(string(markerBytes)) != "clean_exit" {
		t.Fatalf("child did not complete graceful stdin-EOF shutdown (received premature SIGKILL?): err=%v content=%q", err, string(markerBytes))
	}
	if _, err := os.Stat(arg0Dir); !os.IsNotExist(err) {
		t.Fatalf("temporary arg0 directory was leaked: stat err=%v", err)
	}
}

func TestProbeHostHookTrustAppServerKillsProcessIgnoringStdinEOF(t *testing.T) {
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
	respRaw, err := json.Marshal(map[string]any{
		"id": 2,
		"result": map[string]any{
			"data": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"key":         hooksPath + ":stop:0:0",
							"eventName":   "stop",
							"command":     "/bin/memlane hook codex Stop --gateway-url http://127.0.0.1:18174 --key-file /tmp/k",
							"sourcePath":  hooksPath,
							"enabled":     true,
							"trustStatus": "untrusted",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	respFile := filepath.Join(root, "hang_after_reply_resp.json")
	if err := os.WriteFile(respFile, respRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "stuck_child.pid")
	stuckBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex", fmt.Sprintf(`
if [ "$1" != "app-server" ]; then exit 2; fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*)
      IFS= read -r resp < %q
      printf '%%s\n' "$resp"
      sleep 30 &
      echo $! > %q
      wait
      ;;
  esac
done
`, respFile, pidFile))

	start := time.Now()
	report := ProbeHostHookTrust(context.Background(), HostCodex, stuckBin, profile, []string{hooksPath})
	if elapsed := time.Since(start); elapsed >= defaultHookTrustProbeTimeout-500*time.Millisecond {
		t.Fatalf("probe took %v waiting on stuck process after reply (expected graceful kill well before %v)", elapsed, defaultHookTrustProbeTimeout)
	}
	if !report.Evaluated || report.Source != "app_server" || !report.NeedsTrust() {
		t.Fatalf("unexpected report from stuck-after-reply child: %+v", report)
	}
	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("expected child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid child pid %q: %v", string(rawPID), err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("stuck child process %d survived graceful-timeout SIGKILL escalation", pid)
}

func TestProbeHostHookTrustAppServerTimeoutWatchdogRace(t *testing.T) {
	// Intentionally not t.Parallel so concurrent process-spawn stress does not
	// starve other parallel tests' macOS execve latency under -race.
	root := t.TempDir()
	profile := filepath.Join(root, ".codex")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(profile, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"/bin/memlane hook codex Stop --gateway-url http://127.0.0.1:18174 --key-file /tmp/k"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	flakyBin := writeFakeAppServerScript(t, filepath.Join(root, "bin"), "codex", `
sleep 0.03
printf '%s\n' '{"id":1,"result":{}}'
sleep 0.03
`)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()
			timeout := time.Duration(25+(iter%4)*25) * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_ = ProbeHostHookTrust(ctx, HostCodex, flakyBin, profile, []string{hooksPath})
		}(i)
	}
	wg.Wait()
}

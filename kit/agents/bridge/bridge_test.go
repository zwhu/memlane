package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/kit/agents"
)

func requireBridgeError(t *testing.T, err error, code string) {
	t.Helper()
	var result *BridgeError
	if !errors.As(err, &result) || result.Code != code || err.Error() != code {
		t.Fatalf("error %v, want %s", err, code)
	}
}

func testFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func cliOptions(t *testing.T, kind, scenario string) (ExecOptions, string) {
	t.Helper()
	root := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "agent")
	actualHome := filepath.Join(root, "auth")
	ready := filepath.Join(root, "ready")
	defaults := &agents.ModelDefaults{Model: "model-1", Provider: "openai", ReasoningEffort: "high", ServiceTier: "fast"}
	switch kind {
	case "traex":
		defaults.Provider = "trae"
		testFile(t, filepath.Join(actualHome, "cli", "auth.json"), "{}", 0o600)
	case "pi":
		defaults.Provider = "ollama"
		defaults.ThinkingLevel = "high"
		testFile(t, filepath.Join(actualHome, "auth.json"), "{}", 0o600)
	case "antigravity":
		defaults.Provider = "google"
		actualHome = filepath.Join(root, ".gemini", "config")
		if err := os.MkdirAll(actualHome, 0o700); err != nil {
			t.Fatal(err)
		}
		testFile(t, filepath.Join(filepath.Dir(actualHome), "antigravity-cli", "settings.json"), `{"model":"model-1"}`, 0o600)
	default:
		testFile(t, filepath.Join(actualHome, "auth.json"), "{}", 0o600)
	}
	script := "#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexec " + shellQuote(binary) + " -test.run='^TestBridgeCLIHelper$' -- " +
		shellQuote(kind) + " " + shellQuote(scenario) + " " + shellQuote(ready) + " \"$@\"\n"
	testFile(t, exe, script, 0o700)
	return ExecOptions{BridgeKind: kind, Executable: exe, AuthHome: actualHome, Defaults: defaults, Model: "model-1",
		IdleTimeout: 2 * time.Second, RequestID: "test-id", PermissionProfile: "test_profile", SchemaEventPrefix: "test_schema", SchemaName: "test_output"}, ready
}

func makeRequest(t *testing.T, structured bool) *ParsedRequest {
	t.Helper()
	body := `{"model":"model-1","messages":[{"role":"user","content":"sample"}]`
	if structured {
		body += `,"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}}}`
	}
	req, err := ParseChatRequest([]byte(body+"}"), "model-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestCodexAndTraeProtocol(t *testing.T) {
	for _, test := range []struct {
		kind, scenario, code string
		structured, retry    bool
	}{
		{"codex", "retry-after-reply", "", false, true}, {"codex", "retry-before-reply", "", true, true},
		{"codex", "nonretry", "codex_stream_disconnected", false, false}, {"codex", "wrong-turn", "codex_event_identity_invalid", false, false},
		{"codex", "nonretry-before-start", "codex_stream_disconnected", false, false},
		{"codex", "retry-silence", "codex_timeout", false, true}, {"codex", "tool", "codex_unexpected_tool_execution", false, false},
		{"codex", "unknown-feature", "", false, false}, {"traex", "normal", "", true, false}, {"traex", "retry-before-reply", "", false, true},
	} {
		t.Run(test.kind+"/"+test.scenario, func(t *testing.T) {
			opts, _ := cliOptions(t, test.kind, test.scenario)
			if test.scenario == "retry-silence" {
				opts.IdleTimeout = 300 * time.Millisecond
			}
			var traces []TraceEvent
			opts.OnTrace = func(event TraceEvent) { traces = append(traces, event) }
			value, err := Execute(context.Background(), opts, makeRequest(t, test.structured))
			if test.code != "" {
				requireBridgeError(t, err, test.code)
			} else {
				expected := "hello"
				if test.structured {
					expected = `{"ok":true}`
				}
				if err != nil || value != expected {
					t.Fatalf("%q %v", value, err)
				}
			}
			if test.retry || strings.HasPrefix(test.scenario, "nonretry") {
				if len(traces) != 1 || traces[0] != (TraceEvent{"test-id", test.kind, "codex_turn_error", "codex_stream_disconnected", test.retry}) {
					t.Fatalf("trace: %+v", traces)
				}
			} else if len(traces) != 0 {
				t.Fatalf("trace: %+v", traces)
			}
		})
	}
}

func TestPiAndAntigravityProtocol(t *testing.T) {
	for _, test := range []struct {
		kind, scenario, code string
		structured           bool
	}{
		{"pi", "normal", "", true}, {"pi", "normal", "", false}, {"pi", "bad-binding", "pi_native_schema_unconfirmed", false},
		{"pi", "tool", "pi_tool_or_retry_rejected", false}, {"pi", "credential", "pi_command_credential_unsupported", false},
		{"antigravity", "normal", "", true}, {"antigravity", "normal", "", false},
		{"antigravity", "tools-init", "antigravity_tools_not_isolated", false}, {"antigravity", "model-init", "antigravity_model_or_context_changed", false},
		{"antigravity", "schema-init", "antigravity_schema_not_bound", false}, {"antigravity", "tool", "antigravity_tool_rejected", false},
		{"antigravity", "location", "antigravity_account_location_unavailable", false}, {"antigravity", "auth", "antigravity_authentication_required", false},
	} {
		t.Run(test.kind+"/"+test.scenario, func(t *testing.T) {
			opts, _ := cliOptions(t, test.kind, test.scenario)
			if test.scenario == "credential" {
				testFile(t, filepath.Join(opts.AuthHome, "models.json"), `{"providers":{"p":{"apiKey":"!danger"}}}`, 0o600)
			}
			result, err := Execute(context.Background(), opts, makeRequest(t, test.structured))
			if test.code != "" {
				requireBridgeError(t, err, test.code)
				return
			}
			expected := "hello"
			if test.structured {
				expected = `{"ok":true}`
			}
			if result != expected || err != nil {
				t.Fatalf("%q %v", result, err)
			}
		})
	}
}

func TestCancellationReapsProcessGroup(t *testing.T) {
	for _, kind := range []string{"codex", "traex", "pi", "antigravity"} {
		t.Run(kind, func(t *testing.T) {
			opts, ready := cliOptions(t, kind, "cancel")
			opts.IdleTimeout = 8 * time.Second
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			req := makeRequest(t, false)
			go func() { _, err := Execute(ctx, opts, req); done <- err }()
			pid := waitForChild(t, ready, done)
			cancel()
			select {
			case err := <-done:
				expected := kind
				if kind == "traex" {
					expected = "codex"
				}
				requireBridgeError(t, err, expected+"_cancelled")
			case <-time.After(9 * time.Second):
				t.Fatal("bridge did not return after cancellation")
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("grandchild %d still exists: %v", pid, err)
			}
		})
	}
}

func waitForChild(t *testing.T, ready string, done <-chan error) int {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("bridge stopped before ready: %v", err)
		case <-timeout.C:
			t.Fatal("grandchild did not start")
		case <-tick.C:
			data, err := os.ReadFile(ready)
			if err == nil {
				pid, err := strconv.Atoi(string(data))
				if err == nil && pid > 1 {
					return pid
				}
			}
		}
	}
}

type cliHarness struct {
	kind, scenario, ready string
	args                  []string
	input                 *bufio.Scanner
}

func TestBridgeCLIHelper(t *testing.T) {
	if len(os.Args) < 7 || os.Args[2] != "--" {
		return
	}
	h := cliHarness{os.Args[3], os.Args[4], os.Args[5], os.Args[6:], bufio.NewScanner(os.Stdin)}
	h.input.Buffer(make([]byte, 4096), 8<<20)
	switch h.kind {
	case "codex", "traex":
		h.codex()
	case "pi":
		h.pi()
	case "antigravity":
		h.agy()
	default:
		os.Exit(91)
	}
	os.Exit(0)
}

func cliRequire(ok bool) {
	if !ok {
		os.Exit(92)
	}
}

func (h cliHarness) read() map[string]any {
	if !h.input.Scan() {
		os.Exit(93)
	}
	var row map[string]any
	cliRequire(decode(h.input.Bytes(), &row) == nil)
	return row
}

func (h cliHarness) hasArg(name string) string {
	for i, argument := range h.args {
		if argument == name && i+1 < len(h.args) {
			return h.args[i+1]
		}
	}
	return ""
}

func cliEmit(row any) { cliRequire(json.NewEncoder(os.Stdout).Encode(row) == nil) }
func cliNotify(method string, params map[string]any) {
	cliEmit(map[string]any{"method": method, "params": params})
}
func cliReply(request, result map[string]any) {
	cliEmit(map[string]any{"id": request["id"], "result": result})
}

func outputValue(rawSchema any) map[string]any {
	if _, ok := object(object(rawSchema)["properties"])["ok"]; ok {
		return map[string]any{"ok": true}
	}
	return map[string]any{"text": "hello"}
}

func (h cliHarness) spawnGrandchild() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	child := exec.Command("/bin/sleep", "300")
	cliRequire(child.Start() == nil)
	cliRequire(os.WriteFile(h.ready, []byte(strconv.Itoa(child.Process.Pid)), 0o600) == nil)
	// Group signals reach the child directly. The session leader waits for it.
	<-sig
	_ = child.Process.Kill()
	_ = child.Wait()
	os.Exit(0)
}

func (h cliHarness) codex() {
	if h.args[0] == "features" {
		fmt.Println("shell_tool stable true")
		fmt.Println("plugins stable false")
		return
	}
	cliRequire(strings.Join(h.args, " ") == "app-server --strict-config --stdio")
	config := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	provider := "openai"
	if h.kind == "traex" {
		config = filepath.Join(os.Getenv("TRAE_HOME"), "traecli.toml")
		provider = "trae"
	}
	raw, err := os.ReadFile(config)
	cliRequire(err == nil && strings.Contains(string(raw), `service_tier = "fast"`) && strings.Contains(string(raw), `model_reasoning_effort = "high"`))
	if h.scenario == "unknown-feature" && strings.Contains(string(raw), "plugins = false") {
		fmt.Fprintln(os.Stderr, `unknown field "features.plugins"`)
		os.Exit(2)
	}
	init := h.read()
	cliRequire(init["method"] == "initialize")
	cliReply(init, map[string]any{})
	cliRequire(h.read()["method"] == "initialized")
	thread := h.read()
	cliRequire(thread["method"] == "thread/start")
	cliReply(thread, map[string]any{"thread": map[string]any{"id": "thread"}, "model": "model-1", "modelProvider": provider,
		"runtimeWorkspaceRoots": []any{}, "activePermissionProfile": map[string]any{"id": "test_profile"}})
	request := h.read()
	cliRequire(request["method"] == "turn/start")
	params := object(request["params"])
	reply := func() { cliReply(request, map[string]any{"turn": map[string]any{"id": "turn"}}) }
	announce := func() {
		cliNotify("turn/started", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn"}})
	}
	item := func(kind, output string) {
		cliNotify("item/completed", map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"type": kind, "phase": "final_answer", "text": output}})
	}
	turnError := func(id string, retry bool) {
		cliNotify("error", map[string]any{"threadId": "thread", "turnId": id, "willRetry": retry,
			"error": map[string]any{"codexErrorInfo": map[string]any{"responseStreamDisconnected": map[string]any{}}, "message": "Bearer raw-provider-secret"}})
	}
	if h.scenario == "nonretry-before-start" {
		turnError("turn", false)
		return
	}
	before := h.scenario == "retry-before-reply"
	if !before {
		reply()
	}
	announce()
	switch h.scenario {
	case "cancel":
		h.spawnGrandchild()
	case "tool":
		item("commandExecution", "")
		return
	case "retry-before-reply", "retry-after-reply", "wrong-turn", "nonretry", "retry-silence":
		item("agentMessage", `{"text":"partial"}`)
		id := "turn"
		if h.scenario == "wrong-turn" {
			id = "other"
		}
		turnError(id, h.scenario != "nonretry")
		if h.scenario == "retry-silence" {
			cliRequire(h.read()["method"] == "turn/interrupt")
			return
		}
	}
	if before {
		reply()
	}
	item("agentMessage", encodeJSON(outputValue(params["outputSchema"])))
	cliNotify("turn/completed", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "completed", "error": nil}})
}

var extensionValue = regexp.MustCompile(`(?m)^const (digest|prefix) = "([^"]+)";$`)

func (h cliHarness) pi() {
	cliRequire(h.hasArg("--provider") == "ollama" && h.hasArg("--mode") == "rpc")
	ext, err := os.ReadFile(h.hasArg("--extension"))
	cliRequire(err == nil)
	exports := map[string]string{}
	for _, match := range extensionValue.FindAllStringSubmatch(string(ext), -1) {
		exports[match[1]] = match[2]
	}
	cliRequire(exports["prefix"] == "test_schema" && exports["digest"] != "")
	step := func(command string, data map[string]any) {
		row := h.read()
		cliRequire(row["type"] == command)
		if command != "get_state" {
			cliRequire(row["enabled"] == false)
		}
		cliEmit(map[string]any{"type": "response", "command": command, "success": true, "id": row["id"], "data": data})
	}
	state := map[string]any{"model": map[string]any{"provider": "ollama", "id": "model-1"}, "isStreaming": false, "isCompacting": false, "pendingMessageCount": 0}
	step("set_auto_retry", nil)
	step("set_auto_compaction", nil)
	step("get_state", state)
	prompt := h.read()
	cliRequire(prompt["type"] == "prompt")
	cliEmit(map[string]any{"type": "response", "command": "prompt", "id": prompt["id"], "success": true})
	switch h.scenario {
	case "cancel":
		h.spawnGrandchild()
	case "tool":
		cliEmit(map[string]any{"type": "tool_execution_start"})
		return
	}
	message := text(prompt["message"])
	_, schemaRaw, ok := strings.Cut(message, "Return only JSON matching:\n")
	var schema map[string]any
	cliRequire(ok && decode([]byte(schemaRaw), &schema) == nil)
	digest := exports["digest"]
	cliRequire(json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "test_schema_bound", "schema_sha256": digest}) == nil)
	if h.scenario != "bad-binding" {
		cliRequire(json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "test_schema_response", "schema_sha256": digest, "status": 200}) == nil)
	}
	cliEmit(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop",
		"content": []any{map[string]any{"type": "text", "text": encodeJSON(outputValue(schema))}}}})
	cliEmit(map[string]any{"type": "agent_end", "willRetry": false})
	cliEmit(map[string]any{"type": "agent_settled"})
	step("get_state", state)
}

func (h cliHarness) agy() {
	cliRequire(h.hasArg("--input-format") == "stream-json" && h.hasArg("--output-format") == "stream-json")
	raw, err := os.ReadFile(h.hasArg("--json-schema"))
	var schema map[string]any
	cliRequire(err == nil && decode(raw, &schema) == nil)
	cwd, err := os.Getwd()
	cliRequire(err == nil)
	init := map[string]any{"tools": []any{}, "model": "model-1", "agent": h.hasArg("--agent"), "cwd": cwd, "json_schema": schema}
	switch h.scenario {
	case "tools-init":
		init["tools"] = []string{"shell"}
	case "model-init":
		init["model"] = "wrong"
	case "schema-init":
		init["json_schema"] = map[string]any{}
	case "auth", "location":
		reason := "authentication failed: Bearer secret"
		if h.scenario == "location" {
			reason = "not currently available in your location: Bearer secret"
		}
		cliEmit(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR", "error": reason}})
		return
	}
	cliEmit(map[string]any{"event": "init", "conversation_id": "conversation", "init": init})
	if strings.HasSuffix(h.scenario, "-init") {
		return
	}
	cliRequire(h.read()["event"] == "user")
	switch h.scenario {
	case "cancel":
		h.spawnGrandchild()
	case "tool":
		cliEmit(map[string]any{"event": "step_update", "step_update": map[string]any{"conversation_id": "conversation", "step_type": "SUBAGENT"}})
		return
	}
	cliEmit(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "conversation_id": "conversation", "structured_output": outputValue(schema)}})
}

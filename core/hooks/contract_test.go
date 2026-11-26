package hooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/kit/agents"
)

const (
	syntheticGatewayToken = "synthetic-contract-token-0123456789abcdef"
	syntheticBinaryPath   = "/synthetic/bin/memlane"
	syntheticGatewayURL   = "http://127.0.0.1:18799"
)

func readFixtureBytes(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return raw
}

func copyFixture(t *testing.T, rel, dst string) []byte {
	t.Helper()
	raw := readFixtureBytes(t, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", dst, err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatalf("write fixture to %s: %v", dst, err)
	}
	return raw
}

func writePrivateKey(t *testing.T, dir string) string {
	t.Helper()
	keyPath := filepath.Join(dir, "agent.key")
	if err := os.WriteFile(keyPath, []byte(syntheticGatewayToken+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return keyPath
}

func loadPayloadWithBrainRoot(t *testing.T, rel, brainRoot string) []byte {
	t.Helper()
	raw := readFixtureBytes(t, rel)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", rel, err)
	}
	if tp, ok := doc["transcriptPath"].(string); ok && strings.HasPrefix(tp, "/synthetic/brain/") {
		suffix := strings.TrimPrefix(tp, "/synthetic/brain/")
		doc["transcriptPath"] = filepath.Join(brainRoot, filepath.FromSlash(suffix))
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture %s: %v", rel, err)
	}
	return encoded
}

func TestContractAntigravityInstallAndMigration(t *testing.T) {
	t.Run("zero-byte mcp_config and legacy config migration is idempotent", func(t *testing.T) {
		profileDir := t.TempDir()
		keyFile := writePrivateKey(t, t.TempDir())

		emptyBytes := copyFixture(t, "antigravity/empty_mcp_config.json", filepath.Join(profileDir, "mcp_config.json"))
		if len(emptyBytes) != 0 {
			t.Fatalf("expected 0-byte empty_mcp_config.json fixture, got %d bytes", len(emptyBytes))
		}
		copyFixture(t, "antigravity/legacy_config.json", filepath.Join(profileDir, "config.json"))

		legacyPluginDir := filepath.Join(profileDir, "plugins", "memlane-cognee")
		if err := os.MkdirAll(legacyPluginDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacyPluginDir, "plugin.json"), []byte(`{"name":"memlane-cognee"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		opts := hooks.InstallOptions{
			Host:       hooks.HostAntigravity,
			ProfileDir: profileDir,
			BinaryPath: syntheticBinaryPath,
			GatewayURL: syntheticGatewayURL,
			KeyFile:    keyFile,
			Apply:      true,
		}
		res, err := hooks.InstallHostConfig(opts)
		if err != nil || res.Blocked || !res.Applied {
			t.Fatalf("InstallHostConfig failed: res=%+v err=%v", res, err)
		}

		// Native top-level "memlane" hooks.json with exact 4 events and grouped PostToolUse.
		hooksRaw, err := os.ReadFile(filepath.Join(profileDir, "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		hooksDoc, err := agents.ParseJSONC(hooksRaw)
		if err != nil {
			t.Fatal(err)
		}
		if _, hasLegacyWrapper := hooksDoc["hooks"]; hasLegacyWrapper {
			t.Fatalf("antigravity hooks.json must not emit top-level hooks wrapper: %s", hooksRaw)
		}
		memlaneHooks, ok := hooksDoc["memlane"].(map[string]any)
		if !ok || len(memlaneHooks) != 4 {
			t.Fatalf("expected 4 events under top-level memlane hook, got: %s", hooksRaw)
		}
		for _, event := range []string{hooks.SessionStart, hooks.PreInvocation, hooks.Stop} {
			rows, ok := memlaneHooks[event].([]any)
			if !ok || len(rows) != 1 {
				t.Fatalf("expected single flat command handler for %s: %s", event, hooksRaw)
			}
			entry, _ := rows[0].(map[string]any)
			cmd, _ := entry["command"].(string)
			if entry["type"] != "command" || !strings.Contains(cmd, "hook antigravity "+event) {
				t.Fatalf("unexpected %s handler: %+v", event, entry)
			}
		}
		postRows, ok := memlaneHooks[hooks.PostToolUse].([]any)
		if !ok || len(postRows) != 1 {
			t.Fatalf("expected grouped PostToolUse array: %s", hooksRaw)
		}
		postGroup, _ := postRows[0].(map[string]any)
		nested, _ := postGroup["hooks"].([]any)
		if postGroup["matcher"] != "*" || len(nested) != 1 {
			t.Fatalf("expected PostToolUse matcher '*' with 1 nested hook: %+v", postGroup)
		}
		nestedCmd, _ := nested[0].(map[string]any)["command"].(string)
		if !strings.Contains(nestedCmd, "hook antigravity PostToolUse") {
			t.Fatalf("unexpected PostToolUse command: %q", nestedCmd)
		}

		// 0-byte mcp_config.json populated cleanly.
		mcpRaw, err := os.ReadFile(filepath.Join(profileDir, "mcp_config.json"))
		if err != nil {
			t.Fatal(err)
		}
		mcpDoc, err := agents.ParseJSONC(mcpRaw)
		if err != nil {
			t.Fatal(err)
		}
		servers, _ := mcpDoc["mcpServers"].(map[string]any)
		memlaneMCP, _ := servers["memlane"].(map[string]any)
		if memlaneMCP["serverUrl"] != syntheticGatewayURL+"/mcp" {
			t.Fatalf("unexpected mcp_config.json memlane server: %+v", mcpDoc)
		}

		// config.json disables legacy memlane-cognee while keeping swarm untouched.
		cfgRaw, err := os.ReadFile(filepath.Join(profileDir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfgDoc, err := agents.ParseJSONC(cfgRaw)
		if err != nil {
			t.Fatal(err)
		}
		plugins, _ := cfgDoc["plugins"].(map[string]any)
		if plugins["memlane-cognee"].(map[string]any)["enabled"] != false {
			t.Fatalf("expected memlane-cognee disabled: %+v", plugins)
		}
		if plugins["swarm"].(map[string]any)["enabled"] != true || cfgDoc["theme"] != "dark" {
			t.Fatalf("unrelated config fields altered: %+v", cfgDoc)
		}

		// Idempotent reinstall produces zero previews.
		repeat, err := hooks.InstallHostConfig(opts)
		if err != nil || len(repeat.Previews) != 0 {
			t.Fatalf("expected idempotent reinstall with 0 previews, got previews=%d err=%v", len(repeat.Previews), err)
		}
	})

	t.Run("third-party active cognee plugin blocks install without mutating files", func(t *testing.T) {
		profileDir := t.TempDir()
		keyFile := writePrivateKey(t, t.TempDir())
		origCfg := copyFixture(t, "antigravity/third_party_conflict_config.json", filepath.Join(profileDir, "config.json"))

		opts := hooks.InstallOptions{
			Host:       hooks.HostAntigravity,
			ProfileDir: profileDir,
			BinaryPath: syntheticBinaryPath,
			GatewayURL: syntheticGatewayURL,
			KeyFile:    keyFile,
			Apply:      true,
		}
		res, err := hooks.InstallHostConfig(opts)
		if !errors.Is(err, hooks.ErrConflictBlocked) || !res.Blocked || res.Applied {
			t.Fatalf("expected ErrConflictBlocked, got res=%+v err=%v", res, err)
		}
		if res.Conflicts == nil || len(res.Conflicts.ThirdPartyConflicts) != 1 || res.Conflicts.ThirdPartyConflicts[0].EntryName != "cognee" {
			t.Fatalf("expected third-party cognee conflict finding, got: %+v", res.Conflicts)
		}
		afterCfg, err := os.ReadFile(filepath.Join(profileDir, "config.json"))
		if err != nil || string(afterCfg) != string(origCfg) {
			t.Fatalf("blocked install mutated config.json")
		}
		if _, err := os.Stat(filepath.Join(profileDir, "hooks.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("blocked install must not create hooks.json, stat err=%v", err)
		}
	})
}

func TestContractAntigravityRuntimeHooks(t *testing.T) {
	type capturedCall struct {
		Path string
		Body map[string]any
	}
	var (
		mu    sync.Mutex
		calls []capturedCall
	)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != syntheticGatewayToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, capturedCall{Path: r.URL.Path, Body: body})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/recall":
			_, _ = w.Write([]byte(`{"ok":true,"hits":[{"title":"SQLite WAL","snippet":"Use single-writer connection with journal_size_limit."}]}`))
		case "/ingest":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()

	keyFile := writePrivateKey(t, t.TempDir())
	cfg := hooks.Config{GatewayURL: gateway.URL, KeyFile: keyFile}
	ctx := context.Background()

	t.Run("PreInvocation emits injectSteps and deduplicates subturns", func(t *testing.T) {
		mu.Lock()
		calls = nil
		mu.Unlock()

		brainRoot := filepath.Join(t.TempDir(), "brain")
		turn1Raw := loadPayloadWithBrainRoot(t, "antigravity/pre_invocation_turn1.json", brainRoot)
		out, err := hooks.Handle(ctx, hooks.HostAntigravity, hooks.PreInvocation, turn1Raw, cfg)
		if err != nil {
			t.Fatalf("PreInvocation turn1 failed: %v", err)
		}
		var resp struct {
			InjectSteps []struct {
				SystemMessage struct {
					SystemMessage string `json:"systemMessage"`
				} `json:"systemMessage"`
			} `json:"injectSteps"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode injectSteps: %v (%s)", err, out)
		}
		if len(resp.InjectSteps) != 1 || !strings.Contains(resp.InjectSteps[0].SystemMessage.SystemMessage, "SQLite WAL") {
			t.Fatalf("expected injectSteps with recalled memory, got: %s", out)
		}

		mu.Lock()
		if len(calls) != 1 || calls[0].Path != "/recall" {
			mu.Unlock()
			t.Fatalf("expected 1 /recall call, got: %+v", calls)
		}
		recallBody := calls[0].Body
		mu.Unlock()

		if recallBody["query"] != "How should we isolate SQLite WAL locks?" {
			t.Fatalf("expected cleaned query without XML tags, got: %q", recallBody["query"])
		}
		if recallBody["session_id"] != "antigravity-11111111-2222-3333-4444-555555555555" {
			t.Fatalf("unexpected normalized session_id: %v", recallBody["session_id"])
		}
		if recallBody["workspace"] != "/synthetic/workspace/demo-repo" {
			t.Fatalf("unexpected workspace: %v", recallBody["workspace"])
		}

		// Subturn payload (invocationNum > 0) must be deduplicated locally without hitting /recall.
		subturnRaw := loadPayloadWithBrainRoot(t, "antigravity/pre_invocation_subturn.json", brainRoot)
		subOut, err := hooks.Handle(ctx, hooks.HostAntigravity, hooks.PreInvocation, subturnRaw, cfg)
		if err != nil {
			t.Fatalf("PreInvocation subturn failed: %v", err)
		}
		if string(subOut) != "{}" {
			t.Fatalf("expected neutral {} on subturn dedup, got: %s", subOut)
		}
		mu.Lock()
		callCount := len(calls)
		mu.Unlock()
		if callCount != 1 {
			t.Fatalf("subturn must not issue extra /recall call, total calls=%d", callCount)
		}
	})

	t.Run("PostToolUse emits capture_trace with Swarm worker parent and role", func(t *testing.T) {
		mu.Lock()
		calls = nil
		mu.Unlock()

		brainRoot := filepath.Join(t.TempDir(), "brain")
		workerTranscript := filepath.Join(brainRoot, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ".system_generated", "logs", "transcript.jsonl")
		membersPath := filepath.Join(brainRoot, "11111111-2222-3333-4444-555555555555", ".system_generated", "swarm", "members.json")
		copyFixture(t, "antigravity/swarm_worker_transcript.jsonl", workerTranscript)
		copyFixture(t, "antigravity/swarm_members.json", membersPath)

		payload := loadPayloadWithBrainRoot(t, "antigravity/post_tool_use.json", brainRoot)
		out, err := hooks.Handle(ctx, hooks.HostAntigravity, hooks.PostToolUse, payload, cfg)
		if err != nil {
			t.Fatalf("PostToolUse failed: %v", err)
		}
		if string(out) != "{}" {
			t.Fatalf("expected neutral {} from PostToolUse, got: %s", out)
		}

		mu.Lock()
		if len(calls) != 1 || calls[0].Path != "/ingest" {
			mu.Unlock()
			t.Fatalf("expected 1 /ingest call, got: %+v", calls)
		}
		body := calls[0].Body
		mu.Unlock()

		if body["kind"] != "capture_trace" {
			t.Fatalf("expected kind capture_trace, got: %v", body["kind"])
		}
		if body["message_id"] != "tool:call-syn-007" {
			t.Fatalf("expected message_id tool:call-syn-007, got: %v", body["message_id"])
		}
		if body["session_id"] != "antigravity-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			t.Fatalf("unexpected worker session_id: %v", body["session_id"])
		}
		if body["parent_session_id"] != "antigravity-11111111-2222-3333-4444-555555555555" {
			t.Fatalf("expected Swarm parent_session_id, got: %v", body["parent_session_id"])
		}
		if body["subagent_role"] != "verifier" {
			t.Fatalf("expected subagent_role verifier, got: %v", body["subagent_role"])
		}
		content, _ := body["content"].(map[string]any)
		if content["origin_function"] != "run_command" || content["status"] != "success" {
			t.Fatalf("unexpected trace content: %+v", content)
		}

		// Also verify swarm_members.json role lookup when opening omits `for @verifier):`.
		noRoleLine := `{"step_index":0,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-11-26T10:01:00Z","content":"[Message] sender=11111111-2222-3333-4444-555555555555 content=You have just started."}` + "\n"
		if err := os.WriteFile(workerTranscript, []byte(noRoleLine), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := hooks.Handle(ctx, hooks.HostAntigravity, hooks.PostToolUse, payload, cfg); err != nil {
			t.Fatalf("PostToolUse members.json fallback failed: %v", err)
		}
		mu.Lock()
		fallbackBody := calls[len(calls)-1].Body
		mu.Unlock()
		if fallbackBody["parent_session_id"] != "antigravity-11111111-2222-3333-4444-555555555555" || fallbackBody["subagent_role"] != "verifier" {
			t.Fatalf("members.json fallback failed: parent=%v role=%v", fallbackBody["parent_session_id"], fallbackBody["subagent_role"])
		}
	})

	t.Run("Stop strips wrapper XML tags and emits deterministic message_id without parent for coordinator", func(t *testing.T) {
		mu.Lock()
		calls = nil
		mu.Unlock()

		brainRoot := filepath.Join(t.TempDir(), "brain")
		coordTranscript := filepath.Join(brainRoot, "11111111-2222-3333-4444-555555555555", ".system_generated", "logs", "transcript.jsonl")
		copyFixture(t, "antigravity/stop_coordinator_transcript.jsonl", coordTranscript)

		payload := loadPayloadWithBrainRoot(t, "antigravity/stop_coordinator.json", brainRoot)
		for range 2 {
			out, err := hooks.Handle(ctx, hooks.HostAntigravity, hooks.Stop, payload, cfg)
			if err != nil {
				t.Fatalf("Stop failed: %v", err)
			}
			if string(out) != "{}" {
				t.Fatalf("expected neutral {} from Stop, got: %s", out)
			}
		}

		mu.Lock()
		if len(calls) != 2 {
			mu.Unlock()
			t.Fatalf("expected 2 /ingest calls, got %d", len(calls))
		}
		first, second := calls[0].Body, calls[1].Body
		mu.Unlock()

		if first["kind"] != "capture_qa" {
			t.Fatalf("expected kind capture_qa, got: %v", first["kind"])
		}
		if first["message_id"] != "turn:11111111-2222-3333-4444-555555555555:1:4" {
			t.Fatalf("unexpected deterministic message_id: %v", first["message_id"])
		}
		if first["message_id"] != second["message_id"] {
			t.Fatalf("message_id is not deterministic: %v vs %v", first["message_id"], second["message_id"])
		}
		if parent, exists := first["parent_session_id"]; exists && parent != "" {
			t.Fatalf("coordinator must have empty parent_session_id, got: %v", parent)
		}
		content, _ := first["content"].(map[string]any)
		wantQuestion := "Line 1: Audit gateway retries\nLine 2: Keep zero receipt conflict"
		if content["question"] != wantQuestion {
			t.Fatalf("question = %q, want %q", content["question"], wantQuestion)
		}
		for _, forbidden := range []string{"<USER_REQUEST>", "</USER_REQUEST>", "<ADDITIONAL_METADATA>", "<USER_SETTINGS_CHANGE>"} {
			if strings.Contains(content["question"].(string), forbidden) {
				t.Fatalf("question leaked wrapper tag %s: %q", forbidden, content["question"])
			}
		}
		if content["answer"] != "Verified retry path skips duplicate Ingest when receipt is already saved." {
			t.Fatalf("unexpected answer: %q", content["answer"])
		}
	})
}

func TestContractCodexAndTraeIDELegacyMigration(t *testing.T) {
	t.Run("codex migrates legacy plugin and hook while preserving unrelated Flux hooks", func(t *testing.T) {
		profileDir := t.TempDir()
		keyFile := writePrivateKey(t, t.TempDir())
		copyFixture(t, "codex/legacy_config.toml", filepath.Join(profileDir, "config.toml"))
		copyFixture(t, "codex/hooks.json", filepath.Join(profileDir, "hooks.json"))

		opts := hooks.InstallOptions{
			Host:       hooks.HostCodex,
			ProfileDir: profileDir,
			BinaryPath: syntheticBinaryPath,
			GatewayURL: syntheticGatewayURL,
			KeyFile:    keyFile,
			Apply:      true,
		}
		res, err := hooks.InstallHostConfig(opts)
		if err != nil || res.Blocked || !res.Applied {
			t.Fatalf("Codex install failed: res=%+v err=%v", res, err)
		}

		tomlRaw, err := os.ReadFile(filepath.Join(profileDir, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		tomlText := string(tomlRaw)
		if strings.Contains(tomlText, "cognee@memlane-") || strings.Contains(tomlText, "memlane-cognee") {
			t.Fatalf("legacy memlane plugin/server remained in config.toml:\n%s", tomlText)
		}
		tomlDoc, err := agents.ParseTOML(tomlRaw)
		if err != nil {
			t.Fatalf("ParseTOML migrated config.toml: %v\n%s", err, tomlText)
		}
		if tomlDoc["model"] != "gpt-5" {
			t.Fatalf("model setting lost: %+v", tomlDoc)
		}
		features, _ := tomlDoc["features"].(map[string]any)
		if features["hooks"] != true {
			t.Fatalf("expected features.hooks=true: %+v", features)
		}
		servers, _ := tomlDoc["mcp_servers"].(map[string]any)
		userTools, _ := servers["user_tools"].(map[string]any)
		memlaneSrv, _ := servers["memlane"].(map[string]any)
		if userTools["url"] != "http://127.0.0.1:9910/mcp" {
			t.Fatalf("unrelated mcp_servers.user_tools altered: %+v", servers)
		}
		if memlaneSrv["url"] != syntheticGatewayURL+"/mcp" || memlaneSrv["bearer_token_file"] != keyFile {
			t.Fatalf("unexpected mcp_servers.memlane: %+v", memlaneSrv)
		}
		plugins, _ := tomlDoc["plugins"].(map[string]any)
		fluxPlugin, _ := plugins["flux"].(map[string]any)
		if fluxPlugin["enabled"] != true || len(plugins) != 1 {
			t.Fatalf("expected only unrelated plugins.flux preserved, got: %+v", plugins)
		}

		hooksRaw, err := os.ReadFile(filepath.Join(profileDir, "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		hooksText := string(hooksRaw)
		if strings.Contains(hooksText, "/memlane-cognee/") {
			t.Fatalf("legacy memlane-cognee hook remained in hooks.json:\n%s", hooksText)
		}
		for _, keep := range []string{"/synthetic/flux/bin/flux-hook session-start", "/synthetic/flux/bin/flux-hook stop", "hook codex Stop"} {
			if !strings.Contains(hooksText, keep) {
				t.Fatalf("expected %q in migrated hooks.json:\n%s", keep, hooksText)
			}
		}

		repeat, err := hooks.InstallHostConfig(opts)
		if err != nil || len(repeat.Previews) != 0 {
			t.Fatalf("expected idempotent Codex reinstall, got previews=%d err=%v", len(repeat.Previews), err)
		}
	})

	t.Run("trae_ide migrates legacy trae_ide_hook.py while preserving unrelated user hooks", func(t *testing.T) {
		profileDir := t.TempDir()
		keyFile := writePrivateKey(t, t.TempDir())
		copyFixture(t, "trae_ide/legacy_hooks.json", filepath.Join(profileDir, "hooks.json"))

		opts := hooks.InstallOptions{
			Host:       hooks.HostTraeIDE,
			ProfileDir: profileDir,
			BinaryPath: syntheticBinaryPath,
			GatewayURL: syntheticGatewayURL,
			KeyFile:    keyFile,
			Apply:      true,
		}
		res, err := hooks.InstallHostConfig(opts)
		if err != nil || res.Blocked || !res.Applied {
			t.Fatalf("TRAE IDE install failed: res=%+v err=%v", res, err)
		}

		hooksRaw, err := os.ReadFile(filepath.Join(profileDir, "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		hooksText := string(hooksRaw)
		if strings.Contains(hooksText, "trae_ide_hook.py") {
			t.Fatalf("legacy trae_ide_hook.py remained in hooks.json:\n%s", hooksText)
		}
		for _, keep := range []string{"/synthetic/user-tools/check-prompt.sh", "/synthetic/user-tools/on-stop.sh", "hook trae-ide Stop"} {
			if !strings.Contains(hooksText, keep) {
				t.Fatalf("expected %q in migrated trae_ide hooks.json:\n%s", keep, hooksText)
			}
		}
		doc, err := agents.ParseJSONC(hooksRaw)
		if err != nil {
			t.Fatal(err)
		}
		if doc["version"] != float64(1) {
			t.Fatalf("expected hooks version 1 preserved, got: %v", doc["version"])
		}

		repeat, err := hooks.InstallHostConfig(opts)
		if err != nil || len(repeat.Previews) != 0 {
			t.Fatalf("expected idempotent TRAE IDE reinstall, got previews=%d err=%v", len(repeat.Previews), err)
		}
	})
}

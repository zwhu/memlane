package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func stageAntigravity(opts ExecOptions, w workspace, req *ParsedRequest) (string, error) {
	agentsDir := filepath.Join(w.task, ".agents", "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return "", err
	}
	agent := "---\nname: " + opts.AgentName + "\ndescription: Isolated model request\nmainAgent: true\nsubagent: false\nmodel: inherit\n" +
		"tools: []\nmcpServers: []\nskills: []\nplugins: []\n---\n" + req.System + "\nReturn only the object required by the native JSON Schema.\n"
	if err := writePrivate(filepath.Join(agentsDir, opts.AgentName+".md"), []byte(agent), 0o600); err != nil {
		return "", err
	}
	config := filepath.Join(w.home, ".gemini", "config")
	if err := os.MkdirAll(config, 0o700); err != nil {
		return "", err
	}
	for filename, body := range map[string]string{"config.json": `{"plugins":{}}`, "hooks.json": `{}`, "mcp_config.json": `{"mcpServers":{}}`} {
		if err := writePrivate(filepath.Join(config, filename), []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	schema := filepath.Join(w.root, "response-schema.json")
	raw, err := json.Marshal(req.TransportSchema)
	if err != nil {
		return "", err
	}
	return schema, writePrivate(schema, raw, 0o600)
}

func ExecuteAntigravity(ctx context.Context, opts ExecOptions, req *ParsedRequest) (out string, err error) {
	if opts.BridgeKind == "" {
		opts.BridgeKind = "antigravity"
	}
	if opts.BridgeKind != "antigravity" {
		return "", fault("unsupported_generation_bridge", 400)
	}
	opts, err = prepareOptions(opts, req)
	if err != nil {
		return "", err
	}
	if filepath.Base(opts.AuthHome) != "config" || filepath.Base(filepath.Dir(opts.AuthHome)) != ".gemini" {
		return "", fault("antigravity_auth_home_layout_unsupported", 409)
	}
	// The native client searches ancestors for project instructions.
	opts.WorkspaceRoot = os.TempDir()
	if !outsideGitWorkspace(opts.WorkspaceRoot) {
		return "", fault("antigravity_process_unavailable", 503)
	}
	w, e := createWorkspace(opts, "agy")
	if e != nil {
		return "", fault("antigravity_process_unavailable", 503)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.RemoveAll(w.root)
		}
	}()
	schema, e := stageAntigravity(opts, w, req)
	if e != nil {
		return "", fault("antigravity_process_unavailable", 503)
	}
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json", "--model", opts.Model, "--agent", opts.AgentName,
		"--json-schema", schema, "--disable-slash-commands"}
	t, err := startTransport(ctx, opts, w, "antigravity", args, w.environment(opts), MaxBodyBytes, 8*MaxBodyBytes)
	if err != nil {
		return "", err
	}
	defer func() {
		if t.interrupted(err) {
			if e := syscall.Kill(-t.process.PID, syscall.SIGINT); e == nil {
				timer := time.NewTimer(2 * time.Second)
				select {
				case <-timer.C:
				case <-t.process.done:
				}
				timer.Stop()
			}
		}
		remove = t.cleanup(&err)
		if !remove {
			out = ""
		}
	}()
	first, err := t.read(ctx)
	if err != nil {
		return "", err
	}
	id, err := agyInitialized(first, opts, w.task, req.TransportSchema)
	if err != nil {
		return "", err
	}
	if err = t.send(ctx, map[string]any{"event": "user", "message": map[string]any{"content": req.Prompt}}); err != nil {
		return "", err
	}
	for {
		row, e := t.read(ctx)
		if e != nil {
			return "", e
		}
		event := text(row["event"])
		switch event {
		case "init":
			return "", fault("antigravity_duplicate_initialization")
		case "step_update", "result":
		default:
			return "", fault("antigravity_transport_invalid")
		}
		eventData := object(row[event])
		if eventData == nil {
			return "", fault("antigravity_transport_invalid")
		}
		if text(eventData["conversation_id"]) == "" {
			return "", fault("antigravity_conversation_unconfirmed")
		}
		if eventData["conversation_id"] != id {
			return "", fault("antigravity_conversation_changed")
		}
		if event == "step_update" {
			step := strings.ToUpper(text(eventData["step_type"]))
			if truth(eventData["tool_name"]) || truth(eventData["tool_info"]) || truth(eventData["subagent_info"]) ||
				strings.Contains(step, "TOOL") || strings.Contains(step, "SUBAGENT") {
				return "", fault("antigravity_tool_rejected", 409)
			}
			continue
		}
		out, err = agyResult(eventData, req)
		if err != nil {
			return "", err
		}
		if e := t.process.Stdin.Close(); e != nil {
			return "", fault("antigravity_process_failed")
		}
		_, e = withIdle(ctx, t.process.Clock, func() (struct{}, error) { <-t.process.done; return struct{}{}, nil })
		if e != nil {
			return "", t.ioError(e)
		}
		if t.process.waitErr != nil {
			return "", fault("antigravity_process_failed")
		}
		return out, nil
	}
}

func outsideGitWorkspace(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return false
	}
	for {
		if _, err := os.Lstat(filepath.Join(resolved, ".git")); !os.IsNotExist(err) {
			return false
		}
		parent := filepath.Dir(resolved)
		if parent == resolved {
			return true
		}
		resolved = parent
	}
}

func agyInitialized(row map[string]any, opts ExecOptions, work string, transport map[string]any) (string, error) {
	if row["event"] == "result" {
		return "", agyFailure(object(row["result"]))
	}
	init := object(row["init"])
	if row["event"] != "init" || init == nil {
		return "", fault("antigravity_initialization_unconfirmed")
	}
	tools, ok := init["tools"].([]any)
	if !ok || len(tools) != 0 {
		return "", fault("antigravity_tools_not_isolated", 409)
	}
	cwd, err := filepath.EvalSymlinks(text(init["cwd"]))
	resolved, e := filepath.EvalSymlinks(work)
	if err != nil || e != nil || cwd != resolved || init["model"] != opts.Model || init["agent"] != opts.AgentName {
		return "", fault("antigravity_model_or_context_changed", 409)
	}
	schema := init["json_schema"]
	if encoded, ok := schema.(string); ok {
		if decode([]byte(encoded), &schema) != nil {
			return "", fault("antigravity_schema_not_bound")
		}
	}
	var expected any
	if decode([]byte(encodeJSON(transport)), &expected) != nil || !equalJSON(schema, expected) {
		return "", fault("antigravity_schema_not_bound")
	}
	id := text(row["conversation_id"])
	if id == "" {
		return "", fault("antigravity_conversation_unconfirmed")
	}
	return id, nil
}

func agyFailure(result map[string]any) *BridgeError {
	if result == nil {
		return fault("antigravity_terminal_invalid")
	}
	reason := strings.ToLower(text(result["error"]))
	if strings.Contains(reason, "not currently available in your location") {
		return fault("antigravity_account_location_unavailable", 403)
	}
	if strings.Contains(reason, "authentication required") || strings.Contains(reason, "authentication failed") {
		return fault("antigravity_authentication_required", 409)
	}
	if result["status"] == "CANCELED" || result["status"] == "INTERRUPTED" {
		return fault("antigravity_cancelled", 499)
	}
	return fault("antigravity_model_failed")
}

func agyResult(result map[string]any, req *ParsedRequest) (string, error) {
	if result["status"] != "SUCCESS" || truth(result["error"]) {
		return "", agyFailure(result)
	}
	output, ok := result["structured_output"]
	if !ok {
		return "", fault("antigravity_structured_output_missing")
	}
	raw, ok := output.(string)
	if !ok {
		raw = encodeJSON(output)
	}
	out, err := ValidateOutput(raw, req.OriginalSchema, req.TransportSchema, MaxBodyBytes)
	if err != nil {
		return "", fault("antigravity_schema_failed")
	}
	return out, nil
}

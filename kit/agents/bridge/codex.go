package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/kit/agents"
)

type rpc struct {
	*transport
	seq int
}

func (r *rpc) notify(ctx context.Context, method string, params map[string]any) error {
	return r.send(ctx, map[string]any{"method": method, "params": params})
}

func (r *rpc) request(ctx context.Context, method string, params map[string]any) (int, error) {
	r.seq++
	return r.seq, r.send(ctx, map[string]any{"id": r.seq, "method": method, "params": params})
}

func sameRPCID(actual any, expected int) bool {
	n, ok := actual.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i == int64(expected)
}

func (r *rpc) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	id, err := r.request(ctx, method, params)
	if err != nil {
		return nil, err
	}
	for {
		row, err := r.read(ctx)
		if err != nil {
			return nil, err
		}
		if value, ok := row["id"]; ok {
			if !sameRPCID(value, id) || truth(row["method"]) || truth(row["error"]) {
				return nil, fault("codex_rpc_rejected")
			}
			return object(row["result"]), nil
		}
		if row["method"] == "error" {
			return nil, remoteError(object(row["params"])["error"])
		}
		if strings.HasPrefix(text(row["method"]), "item/") {
			return nil, fault("codex_tool_or_rpc_rejected")
		}
	}
}

var codexRemoteCodes = map[string]BridgeError{
	"unauthorized":                  {"codex_authentication_failed", 502},
	"usageLimitExceeded":            {"codex_quota_exceeded", 429},
	"sessionBudgetExceeded":         {"codex_quota_exceeded", 429},
	"rateLimitExceeded":             {"codex_rate_limited", 429},
	"modelNotFound":                 {"codex_model_unavailable", 502},
	"contextWindowExceeded":         {"codex_context_exceeded", 400},
	"badRequest":                    {"codex_request_invalid", 400},
	"sandboxError":                  {"codex_isolation_failed", 502},
	"httpConnectionFailed":          {"codex_connection_failed", 502},
	"responseStreamDisconnected":    {"codex_stream_disconnected", 502},
	"responseTooManyFailedAttempts": {"codex_connection_failed", 502},
	"internalServerError":           {"codex_upstream_error", 502},
}

func remoteError(raw any) *BridgeError {
	row := object(raw)
	info := row["codexErrorInfo"]
	tag := text(info)
	if one := object(info); len(one) == 1 {
		for key := range one {
			tag = key
		}
	}
	if tag == "other" {
		var detail map[string]any
		if decode([]byte(text(row["message"])), &detail) == nil {
			body := object(detail["error"])
			if body["code"] == "invalid_json_schema" {
				return fault("codex_schema_unsupported", 400)
			}
			if detail["status"] == json.Number("400") && body["type"] == "invalid_request_error" {
				return fault("codex_request_invalid", 400)
			}
		}
	}
	if code, ok := codexRemoteCodes[tag]; ok {
		return &code
	}
	return fault("codex_model_failed")
}

func codexConfig(opts ExecOptions, provider, work string, features []string) []byte {
	lines := []string{"model = " + encodeJSON(opts.Model), "model_provider = " + encodeJSON(provider)}
	if value := opts.Defaults.ReasoningEffort; value != "" {
		lines = append(lines, "model_reasoning_effort = "+encodeJSON(value))
	}
	if value := opts.Defaults.ServiceTier; value != "" {
		lines = append(lines, "service_tier = "+encodeJSON(value))
	}
	lines = append(lines, "default_permissions = "+encodeJSON(opts.PermissionProfile), "web_search = \"disabled\"", "project_doc_max_bytes = 0", "[features]")
	for _, name := range features {
		lines = append(lines, name+" = false")
	}
	permissions := "[permissions." + opts.PermissionProfile
	lines = append(lines, "[memories]", "extract_model = "+encodeJSON(opts.Model), "consolidation_model = "+encodeJSON(opts.Model),
		"[skills]", "include_instructions = false", "[skills.bundled]", "enabled = false",
		"[analytics]", "enabled = false", "[feedback]", "enabled = false",
		permissions+".filesystem]", "\"/\" = \"deny\"", "\":minimal\" = \"read\"", encodeJSON(work)+" = \"read\"",
		permissions+".network]", "enabled = false", "")
	return []byte(strings.Join(lines, "\n"))
}

func ExecuteCodexOrTraeX(ctx context.Context, opts ExecOptions, req *ParsedRequest) (out string, err error) {
	if opts.BridgeKind == "" {
		opts.BridgeKind = "codex"
	}
	if opts.BridgeKind != "codex" && opts.BridgeKind != "traex" {
		return "", fault("unsupported_generation_bridge", 400)
	}
	opts, err = prepareOptions(opts, req)
	if err != nil {
		return "", err
	}
	provider, config := "openai", "config.toml"
	if opts.BridgeKind == "traex" {
		provider, config = "trae", "traecli.toml"
	}
	if opts.Defaults.Provider != "" && opts.Defaults.Provider != provider {
		return "", fault("bridge_profile_mismatch", 409)
	}
	key, e := featureIdentity(opts)
	if e != nil {
		return "", fault("codex_process_unavailable", 503)
	}
	supported, e := getFeatures(ctx, opts, key)
	if ctx.Err() != nil {
		return "", fault("codex_cancelled", 499)
	}
	if e != nil {
		return "", e
	}
	w, e := createWorkspace(opts, "codex")
	if e != nil {
		return "", fault("codex_process_unavailable", 503)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.RemoveAll(w.root)
		}
	}()
	if e := stageCodexAuth(opts, w); e != nil {
		return "", fault("codex_authentication_required", 409)
	}
	excluded := map[string]bool{}
	for attempt := 0; attempt < 3; attempt++ {
		features := selectedFeatures(key, supported, excluded)
		if e := writePrivate(filepath.Join(w.private, config), codexConfig(opts, provider, w.task, features), 0o600); e != nil {
			return "", fault("codex_process_unavailable", 503)
		}
		var unsupported string
		out, unsupported, remove, err = codexAttempt(ctx, opts, req, w, provider, features)
		if unsupported == "" {
			return out, err
		}
		excluded[unsupported] = true
		excludeFeature(key, unsupported)
	}
	return "", err
}

func stageCodexAuth(opts ExecOptions, w workspace) error {
	if opts.BridgeKind == "traex" {
		target := filepath.Join(w.private, "cli")
		if err := os.Mkdir(target, 0o700); err != nil {
			return err
		}
		return os.Symlink(filepath.Join(opts.AuthHome, "cli", "auth.json"), filepath.Join(target, "auth.json"))
	}
	auth, err := agents.SafeReadFile(filepath.Join(opts.AuthHome, "auth.json"), 1<<20)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(w.private, "auth.json"), auth, 0o600)
}

func codexAttempt(ctx context.Context, opts ExecOptions, req *ParsedRequest, w workspace, provider string, features []string) (out, unsupported string, clean bool, err error) {
	t, err := startTransport(ctx, opts, w, "codex", []string{"app-server", "--strict-config", "--stdio"}, codexEnvironment(w, opts), 8<<20, 64<<20)
	if err != nil {
		return "", "", true, err
	}
	r := rpc{transport: t}
	initialized := false
	thread := codexTurn{}
	defer func() {
		if r.interrupted(err) && thread.thread != "" && thread.active() != "" {
			r.interrupt(map[string]any{"method": "turn/interrupt", "id": r.seq + 1, "params": map[string]any{"threadId": thread.thread, "turnId": thread.active()}})
		}
		clean = r.cleanup(&err)
		if !clean {
			out = ""
		}
		var failure *BridgeError
		if clean && !initialized && r.process.waitErr != nil && errors.As(err, &failure) && failure.Code == "codex_transport_closed" {
			unsupported = unknownStartupFeature(r.process.diagnostics(), features)
		}
	}()
	_, err = r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": opts.ClientName, "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}})
	if err != nil {
		return
	}
	initialized = true
	if err = r.notify(ctx, "initialized", map[string]any{}); err != nil {
		return
	}
	instructions := "Complete only this isolated model request."
	if provider == "trae" {
		instructions += " Return only the JSON object constrained by outputSchema, without Markdown. For a text envelope put the answer in its text field. Required output schema: " + encodeJSON(req.TransportSchema)
	}
	start, e := r.call(ctx, "thread/start", map[string]any{
		"cwd": w.task, "runtimeWorkspaceRoots": []any{}, "permissions": opts.PermissionProfile,
		"model": opts.Model, "modelProvider": provider, "approvalPolicy": "never", "ephemeral": true,
		"allowProviderModelFallback": false, "dynamicTools": []any{}, "baseInstructions": req.System, "developerInstructions": instructions,
	})
	if e != nil {
		err = e
		return
	}
	thread.thread = text(object(start["thread"])["id"])
	roots, ok := start["runtimeWorkspaceRoots"].([]any)
	if thread.thread == "" || start["model"] != opts.Model || start["modelProvider"] != provider || !ok || len(roots) != 0 ||
		object(start["activePermissionProfile"])["id"] != opts.PermissionProfile {
		err = fault("codex_model_or_permissions_changed")
		return
	}
	id, e := r.request(ctx, "turn/start", map[string]any{
		"threadId": thread.thread, "input": []any{map[string]any{"type": "text", "text": req.Prompt, "text_elements": []any{}}},
		"outputSchema": req.TransportSchema, "permissions": opts.PermissionProfile,
	})
	if e != nil {
		err = e
		return
	}
	for {
		row, e := r.read(ctx)
		if e != nil {
			err = e
			return
		}
		finished, e := thread.consume(row, id, opts)
		if e != nil {
			err = e
			return
		}
		if finished {
			break
		}
	}
	out, err = ValidateOutput(thread.final, req.OriginalSchema, req.TransportSchema, MaxBodyBytes)
	if err != nil {
		err = fault("codex_schema_failed")
	}
	return
}

type codexTurn struct {
	thread, id, announced, final string
	hasFinal                     bool
}

func (t codexTurn) active() string {
	if t.id != "" {
		return t.id
	}
	return t.announced
}

func (t *codexTurn) consume(row map[string]any, id int, opts ExecOptions) (bool, error) {
	identity := fault("codex_event_identity_invalid")
	method := text(row["method"])
	params := object(row["params"])
	if value, ok := row["id"]; ok {
		if method != "" || !sameRPCID(value, id) || truth(row["error"]) || t.id != "" {
			return false, fault("codex_tool_or_rpc_rejected")
		}
		t.id = text(object(object(row["result"])["turn"])["id"])
		if t.id == "" || t.announced != "" && t.id != t.announced {
			return false, identity
		}
		return false, nil
	}
	switch {
	case method == "error":
		remote := remoteError(params["error"])
		retry, _ := params["willRetry"].(bool)
		active := t.active()
		if retry {
			if active == "" || params["threadId"] != t.thread || params["turnId"] != active {
				return false, identity
			}
		} else {
			if thread, present := params["threadId"]; present && thread != t.thread {
				return false, identity
			}
			if turn, present := params["turnId"]; present && active != "" && turn != active {
				return false, identity
			}
		}
		if opts.OnTrace != nil {
			opts.OnTrace(TraceEvent{opts.RequestID, opts.BridgeKind, "codex_turn_error", remote.Code, retry})
		}
		if retry {
			t.final, t.hasFinal = "", false
			return false, nil
		}
		return false, remote
	case method == "turn/started":
		announced := text(object(params["turn"])["id"])
		if announced == "" || params["threadId"] != t.thread || t.active() != "" && t.active() != announced {
			return false, identity
		}
		t.announced = announced
	case strings.HasPrefix(method, "item/"):
		if params["threadId"] != t.thread || t.active() == "" || params["turnId"] != t.active() {
			return false, identity
		}
		if err := t.item(method, object(params["item"])); err != nil {
			return false, err
		}
	case method == "turn/completed":
		turn := object(params["turn"])
		if params["threadId"] != t.thread || t.id == "" || turn["id"] != t.id {
			return false, identity
		}
		if turn["status"] != "completed" || turn["error"] != nil {
			return false, remoteError(turn["error"])
		}
		return true, nil
	}
	return false, nil
}

func (t *codexTurn) item(method string, item map[string]any) error {
	switch method {
	case "item/started", "item/completed":
		switch item["type"] {
		case "agentMessage":
			if method == "item/completed" && item["phase"] != "commentary" {
				if t.hasFinal {
					return fault("codex_multiple_final_answers")
				}
				var ok bool
				t.final, ok = item["text"].(string)
				if !ok {
					return fault("codex_schema_failed")
				}
				t.hasFinal = true
			}
		case "reasoning", "userMessage":
		default:
			return fault("codex_unexpected_tool_execution")
		}
	default:
		if !strings.HasPrefix(method, "item/agentMessage/") && !strings.HasPrefix(method, "item/reasoning/") {
			return fault("codex_unexpected_tool_execution")
		}
	}
	return nil
}

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type object = map[string]any

const neutral = "{}"

func Run(ctx context.Context, host, event string, stdin io.Reader, stdout io.Writer, cfg Config) error {
	result := []byte(neutral)
	if os.Getenv("MEMLANE_PROCESSING_SESSION") != "1" && event != SessionStart {
		raw, err := io.ReadAll(io.LimitReader(stdin, inputLimit(cfg)+1))
		if err != nil {
			err = ErrInput
		} else {
			result, err = Handle(ctx, host, event, raw, cfg)
		}
		if err != nil {
			if cfg.Verbose && cfg.ErrOut != nil {
				if _, noteErr := fmt.Fprintln(cfg.ErrOut, "memlane hook:", err); noteErr != nil {
					// The diagnostic stream is optional. The hook response still goes to stdout.
					_, writeErr := io.WriteString(stdout, neutral+"\n")
					return writeErr
				}
			}
			result = []byte(neutral)
		}
	}
	_, err := stdout.Write(append(result, '\n'))
	return err
}

func inputLimit(cfg Config) int64 {
	if cfg.MaxInputBytes <= 0 {
		return 256 << 10
	}
	return min(cfg.MaxInputBytes, 1<<62)
}

func stringField(in object, keys ...string) string {
	for _, key := range keys {
		if val, ok := in[key].(string); ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func workspacePath(host string, in object) string {
	workspace := stringField(in, "cwd", "workspace")
	if host != HostTraeIDE {
		if host == HostAntigravity {
			if candidate := stringField(in, "cwd"); filepath.IsAbs(candidate) {
				return filepath.Clean(candidate)
			}
			if candidate := firstStringElem(in["workspacePaths"]); filepath.IsAbs(candidate) {
				return filepath.Clean(candidate)
			}
		}
		for _, name := range []string{"cwd", "workspace", "project_path"} {
			if candidate := stringField(in, name); filepath.IsAbs(candidate) {
				return filepath.Clean(candidate)
			}
		}
		return ""
	}
	if !filepath.IsAbs(workspace) {
		return ""
	}
	workspace = filepath.Clean(workspace)
	if host == HostTraeIDE {
		if raw, present := in["workspace_roots"]; present {
			roots, ok := raw.([]any)
			if !ok || len(roots) != 1 {
				return ""
			}
			root, ok := roots[0].(string)
			if !ok || !filepath.IsAbs(root) || resolvedPath(root) != resolvedPath(workspace) {
				return ""
			}
		}
	}
	return workspace
}

func resolvedPath(path string) string {
	path = filepath.Clean(path)
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	return path
}

func Handle(ctx context.Context, host, event string, rawInput []byte, cfg Config) ([]byte, error) {
	if os.Getenv("MEMLANE_PROCESSING_SESSION") == "1" {
		return []byte(neutral), nil
	}
	host = CanonicalHost(host)
	if host == "" {
		return nil, ErrEvent
	}
	if event == SessionStart || host == HostPi && (event == "resolve" || event == "flush") {
		return []byte(neutral), nil
	}
	var in object
	if int64(len(rawInput)) > inputLimit(cfg) || json.Unmarshal(rawInput, &in) != nil || in == nil {
		return nil, ErrInput
	}
	sessionID := stringField(in, "session_id")
	if host == HostAntigravity {
		sessionID = antigravitySessionID(in)
	}
	scope := object{"workspace": workspacePath(host, in), "session_id": sessionID}
	// A rejected IDE workspace must never be replaced by a supplied project ID.
	if host == HostTraeIDE && scope["workspace"] == "" {
		return []byte(neutral), nil
	}
	switch {
	case event == UserPromptSubmit || host == HostAntigravity && event == PreInvocation || host == HostPi && event == "recall":
		prompt := stringField(in, "prompt", "query")
		if host == HostAntigravity {
			prompt = stringField(in, "lastUserInput", "prompt", "query")
		}
		query := []rune(prompt)
		if len(query) == 0 {
			return []byte(neutral), nil
		}
		if host == HostAntigravity && event == PreInvocation {
			skip, err := shouldSkipAntigravityRecall(in, prompt)
			if err != nil {
				return nil, err
			}
			if skip {
				return []byte(neutral), nil
			}
		}
		scope["project_id"], scope["query"] = stringField(in, "project_id"), string(query[:min(len(query), 16000)])
		scope["channel"], scope["limit"] = "automatic", 10
		return recall(ctx, host, scope, cfg)
	case event == PostToolUse || event == PostToolUseFailure || event == Stop || event == SessionEnd || host == HostPi && event == "capture":
		payload := in
		if host == HostAntigravity {
			normalized, ok, err := normalizeAntigravityCapture(in, event)
			if err != nil {
				return nil, err
			}
			if !ok {
				return []byte(neutral), nil
			}
			payload = normalized
		}
		kind, id, content := capture(payload, event)
		if content != nil {
			scope["kind"], scope["message_id"], scope["content"] = kind, id, content
			scope["host"] = host
			if err := forward(ctx, cfg, "/ingest", scope, nil); err != nil {
				return nil, err
			}
		}
		if host == HostPi && (event == "capture" || event == Stop || event == SessionEnd) {
			return []byte(`{"status":"complete"}`), nil
		}
		return []byte(neutral), nil
	}
	return nil, ErrEvent
}

func capture(in object, event string) (kind, id string, content object) {
	fields := in
	if entry, ok := in["entry"].(object); ok {
		fields = maps.Clone(in)
		maps.Copy(fields, entry)
	}
	now := time.Now()
	timestamp := now.UTC().Format(time.RFC3339)
	id = stringField(fields, "turn_id", "event_id", "message_id")
	if event == PostToolUse || event == PostToolUseFailure || event == "capture" && stringField(fields, "type") == "trace" {
		name := stringField(fields, "tool_name", "origin_function")
		if name == "" || strings.HasPrefix(name, "cognee_") || strings.HasPrefix(name, "memlane_") {
			return "", "", nil
		}
		callID := stringField(fields, "tool_use_id", "call_id")
		if callID != "" {
			id = "tool:" + callID
		}
		params, ok := fields["tool_input"]
		if !ok {
			params = fields["method_params"]
		}
		if params == nil {
			params = object{}
		} else if _, ok := params.(object); !ok {
			params = object{"input": params}
		}
		response, ok := fields["tool_response"]
		if !ok {
			response = fields["method_return_value"]
		}
		failure := event == PostToolUseFailure || fields["is_error"] == true || stringField(fields, "status") == "error"
		if detail := fields["error"]; detail != nil && detail != false && detail != "" {
			failure = true
			if response == nil {
				response = detail
			}
		}
		status := "success"
		if failure {
			status = "error"
		}
		return "capture_trace", id, object{"origin_function": name, "method_params": params, "method_return_value": response, "status": status, "time": timestamp}
	}
	question, answer := stringField(fields, "question", "prompt"), stringField(fields, "answer", "last_assistant_message")
	if question == "" || answer == "" {
		return "", "", nil
	}
	var ctxMap object
	switch incoming := fields["context"].(type) {
	case object:
		ctxMap = maps.Clone(incoming)
	case string:
		if err := json.Unmarshal([]byte(incoming), &ctxMap); err != nil {
			// Discard any partially decoded optional context.
			ctxMap = nil
		}
	}
	promptTime := stringField(fields, "prompt_time")
	eventTime := stringField(fields, "time")
	content = object{"question": question, "answer": answer, "time": timestamp}
	if promptTime != "" {
		content["prompt_time"] = promptTime
	}
	if eventTime != "" {
		content["time"] = eventTime
	}
	if stringField(ctxMap, "date") == "" && promptTime == "" && eventTime == "" {
		if ctxMap == nil {
			ctxMap = object{}
		}
		ctxMap["date"] = now.Format("2006-01-02")
	}
	if len(ctxMap) > 0 {
		content["context"] = ctxMap
	}
	return "capture_qa", id, content
}

func recall(ctx context.Context, host string, scope object, cfg Config) ([]byte, error) {
	var out struct {
		Hits []struct{ Title, Snippet string }
	}
	if err := forward(ctx, cfg, "/recall", scope, &out); err != nil {
		return nil, err
	}
	var lines []string
	for _, hit := range out.Hits {
		title, snippet := strings.TrimSpace(hit.Title), strings.TrimSpace(hit.Snippet)
		if title != "" && snippet != "" {
			lines = append(lines, "- "+title+": "+snippet)
		} else if title+snippet != "" {
			lines = append(lines, "- "+title+snippet)
		}
	}
	contextText := strings.Join(lines, "\n")
	if host == HostPi {
		return json.Marshal(object{"status": "complete", "context": contextText})
	}
	if host == HostAntigravity {
		return formatAntigravityRecall(contextText)
	}
	if contextText == "" {
		return []byte(neutral), nil
	}
	return json.Marshal(object{"hookSpecificOutput": object{"hookEventName": UserPromptSubmit, "additionalContext": contextText}})
}

var localTransport = &http.Transport{DialContext: (&net.Dialer{}).DialContext}

const (
	defaultRecallTimeout = 10 * time.Second
	defaultIngestTimeout = 5 * time.Second
)

func defaultTimeout(path string) time.Duration {
	if path == "/recall" {
		return defaultRecallTimeout
	}
	return defaultIngestTimeout
}

func forward(ctx context.Context, cfg Config, path string, payload object, result any) error {
	if err := ValidateGatewayURL(cfg.GatewayURL); err != nil {
		return err
	}
	token, err := ReadPrivateKey(cfg.KeyFile)
	if err != nil {
		return err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout(path)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := http.Client{}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	if client.Transport == nil {
		client.Transport = localTransport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	data, err := json.Marshal(payload)
	if err != nil {
		return ErrInput
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.Replace(strings.TrimRight(cfg.GatewayURL, "/"), "http://localhost:", "http://127.0.0.1:", 1)+path, bytes.NewReader(data))
	if err != nil {
		return ErrInvalidGateway
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", token)
	resp, err := client.Do(req)
	if err != nil {
		return ErrTransport
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HTTPError(resp.StatusCode)
	}
	if result != nil {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, result) != nil {
			return ErrResponse
		}
	}
	return nil
}

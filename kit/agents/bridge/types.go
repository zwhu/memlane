package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/kit/agents"
)

const MaxBodyBytes = 2 << 20

type BridgeError struct {
	Code   string
	Status int
}

func (e *BridgeError) Error() string { return e.Code }
func fault(code string, status ...int) *BridgeError {
	n := 502
	if len(status) > 0 {
		n = status[0]
	}
	return &BridgeError{Code: code, Status: n}
}

type SamplingParams struct {
	Temperature, TopP *float64
	MaxOutputTokens   *int
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type ChatRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	Stream         bool           `json:"stream,omitempty"`
	Tools          []any          `json:"tools,omitempty"`
	Functions      []any          `json:"functions,omitempty"`
	Sampling       SamplingParams `json:"-"`
}
type ParsedRequest struct {
	System, Prompt                  string
	OriginalSchema, TransportSchema map[string]any
	Sampling                        SamplingParams
}
type TraceEvent struct {
	RequestID, BridgeKind, Event, Code string
	WillRetry                          bool
}
type ExecOptions struct {
	RequestID, BridgeKind, Executable, LauncherDir, AuthHome, Model, BridgeProvider string
	Defaults                                                                        *agents.ModelDefaults
	IdleTimeout                                                                     time.Duration
	WorkspaceRoot, ClientName, PermissionProfile, AgentName, ProcessingEnvKey       string
	SchemaEventPrefix, SchemaName, TempPrefix                                       string
	Environ                                                                         func(string) string
	OnTrace                                                                         func(TraceEvent)
}

func decode(raw []byte, dest any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(dest); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected JSON data")
	}
	return nil
}

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(v any) string           { s, _ := v.(string); return s }
func array(v any) []any           { a, _ := v.([]any); return a }
func truth(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case json.Number:
		n, _ := t.Float64()
		return n != 0
	default:
		return true
	}
}

func ParseChatRequest(raw []byte, expectedModel string, maxBodyBytes int64) (*ParsedRequest, error) {
	if maxBodyBytes == 0 {
		maxBodyBytes = MaxBodyBytes
	}
	if int64(len(raw)) > maxBodyBytes {
		return nil, fault("request_body_too_large", 413)
	}
	var body map[string]any
	if decode(raw, &body) != nil {
		return nil, fault("invalid_request", 400)
	}
	if expectedModel == "" || text(body["model"]) != expectedModel {
		return nil, fault("codex_model_mismatch", 400)
	}
	if truth(body["stream"]) || truth(body["tools"]) || truth(body["functions"]) {
		return nil, fault("codex_request_unsupported", 400)
	}
	out := &ParsedRequest{}
	format := object(body["response_format"])
	switch {
	case body["response_format"] == nil || format["type"] == "text":
		out.TransportSchema = map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}, "additionalProperties": false}
	case format["type"] == "json_schema":
		schema := object(object(format["json_schema"])["schema"])
		rawSchema, err := json.Marshal(schema)
		if err != nil || len(rawSchema) > 65536 {
			return nil, fault("codex_schema_invalid", 400)
		}
		out.OriginalSchema = schema
		out.TransportSchema, err = StrictifySchema(schema)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fault("codex_response_format_unsupported", 400)
	}
	var system []string
	var prompt []Message
	for _, item := range array(body["messages"]) {
		row := object(item)
		content, ok := messageText(row["content"])
		if !ok {
			return nil, fault("codex_messages_invalid", 400)
		}
		switch role := text(row["role"]); role {
		case "system", "developer":
			system = append(system, content)
		case "user", "assistant":
			prompt = append(prompt, Message{role, content})
		default:
			return nil, fault("codex_messages_invalid", 400)
		}
	}
	if len(prompt) == 0 {
		return nil, fault("codex_messages_invalid", 400)
	}
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return nil, fault("codex_messages_invalid", 400)
	}
	out.System, out.Prompt = strings.Join(system, "\n\n"), string(encoded)
	if err := decodeSampling(body, &out.Sampling); err != nil {
		return nil, err
	}
	return out, nil
}

func messageText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			row := object(item)
			s, ok := row["text"].(string)
			if row["type"] != "text" || !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, "\n"), true
	default:
		return "", false
	}
}

func decodeSampling(body map[string]any, out *SamplingParams) error {
	invalid := fault("bridge_sampling_invalid", 400)
	for name, dest := range map[string]**float64{"temperature": &out.Temperature, "top_p": &out.TopP} {
		if body[name] == nil {
			continue
		}
		number, ok := body[name].(json.Number)
		n, err := number.Float64()
		if !ok || err != nil {
			return invalid
		}
		*dest = &n
	}
	for _, name := range []string{"max_tokens", "max_output_tokens"} {
		if body[name] == nil {
			continue
		}
		number, ok := body[name].(json.Number)
		n, err := number.Int64()
		if !ok || err != nil || n < 0 || int64(int(n)) != n {
			return invalid
		}
		value := int(n)
		if out.MaxOutputTokens != nil && *out.MaxOutputTokens != value {
			return invalid
		}
		out.MaxOutputTokens = &value
	}
	return nil
}

var optionIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var envIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,79}$`)

func prepareOptions(opts ExecOptions, req *ParsedRequest) (ExecOptions, error) {
	if req == nil || req.TransportSchema == nil {
		return opts, fault("invalid_request", 400)
	}
	if s := req.Sampling; s.Temperature != nil || s.TopP != nil || s.MaxOutputTokens != nil {
		return opts, fault("bridge_sampling_unsupported", 400)
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 90 * time.Second
	}
	if opts.IdleTimeout < 0 {
		return opts, fault("bridge_idle_timeout_invalid", 400)
	}
	for _, setting := range []struct {
		target   *string
		fallback string
	}{
		{&opts.ClientName, "agent_bridge"}, {&opts.PermissionProfile, "bridge_completion"},
		{&opts.AgentName, "isolated-processing"}, {&opts.SchemaEventPrefix, "bridge_schema"},
		{&opts.SchemaName, "bridge_structured_output"}, {&opts.ProcessingEnvKey, "AGENT_BRIDGE_PROCESSING_SESSION"},
		{&opts.TempPrefix, "agent-bridge"},
	} {
		if *setting.target == "" {
			*setting.target = setting.fallback
		}
		if !optionIdentifier.MatchString(*setting.target) {
			return opts, fault("bridge_options_invalid", 400)
		}
	}
	if !envIdentifier.MatchString(opts.ProcessingEnvKey) || slices.Contains([]string{
		"HOME", "TMPDIR", "PATH", "CODEX_HOME", "TRAE_HOME", "TRAECLI_HOME",
		"PI_CODING_AGENT_DIR", "PI_TELEMETRY", "PI_OFFLINE", "LANG", "LC_ALL",
		"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY",
	}, opts.ProcessingEnvKey) {
		return opts, fault("bridge_options_invalid", 400)
	}
	if opts.Environ == nil {
		opts.Environ = os.Getenv
	}
	runtime, err := agents.CheckBridgeRuntime(opts.BridgeKind, opts.Executable, opts.AuthHome, agents.ModelDefaultsOptions{Environ: opts.Environ})
	if err != nil {
		return opts, err
	}
	if opts.Defaults == nil {
		defaults, err := agents.ReadModelDefaults(opts.BridgeKind, runtime.Home, agents.ModelDefaultsOptions{Environ: opts.Environ})
		if err != nil {
			return opts, err
		}
		opts.Defaults = &defaults
	} else {
		defaults := *opts.Defaults
		opts.Defaults = &defaults
	}
	if opts.Defaults.Model == "" || opts.Model != "" && opts.Model != opts.Defaults.Model {
		return opts, fault("bridge_profile_mismatch", 409)
	}
	opts.Model = opts.Defaults.Model
	if opts.BridgeKind == "pi" {
		if opts.BridgeProvider != "" && opts.BridgeProvider != opts.Defaults.Provider {
			return opts, fault("bridge_profile_mismatch", 409)
		}
		opts.BridgeProvider = opts.Defaults.Provider
	}
	if opts.LauncherDir == "" {
		opts.LauncherDir = runtime.LauncherDir
	}
	opts.Executable, opts.AuthHome = runtime.Executable, runtime.Home
	return opts, nil
}

func Execute(ctx context.Context, opts ExecOptions, req *ParsedRequest) (string, error) {
	switch opts.BridgeKind {
	case "codex", "traex":
		return ExecuteCodexOrTraeX(ctx, opts, req)
	case "pi":
		return ExecutePi(ctx, opts, req)
	case "antigravity":
		return ExecuteAntigravity(ctx, opts, req)
	default:
		return "", fault("unsupported_generation_bridge", 400)
	}
}

func number(value any) *big.Rat {
	switch v := value.(type) {
	case json.Number:
		raw := string(v)
		if len(raw) > 1024 {
			return nil
		}
		if i := strings.IndexAny(raw, "eE"); i >= 0 {
			exponent, err := json.Number(strings.TrimPrefix(raw[i+1:], "+")).Int64()
			if err != nil || exponent > 4096 || exponent < -4096 {
				return nil
			}
		}
		n, _ := new(big.Rat).SetString(raw)
		return n
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil
		}
		n, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'g', -1, 64))
		return n
	case int:
		return new(big.Rat).SetInt64(int64(v))
	case int64:
		return new(big.Rat).SetInt64(v)
	default:
		return nil
	}
}

func equalJSON(a, b any) bool {
	if n := number(a); n != nil {
		other := number(b)
		return other != nil && n.Cmp(other) == 0
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, v := range x {
			w, ok := y[key]
			if !ok || !equalJSON(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, equalJSON)
	default:
		return reflect.DeepEqual(a, b)
	}
}

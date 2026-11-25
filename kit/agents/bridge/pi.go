package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zwhu/memlane/kit/agents"
)

const piExtension = `
import { openAIResponsesApi } from "@earendil-works/pi-ai";
const schema = __SCHEMA__;
const digest = __DIGEST__;
const provider = __PROVIDER__;
const prefix = __PREFIX__;
const name = __NAME__;

export default function (pi) {
  if (provider === "deepseek") {
    const responses = openAIResponsesApi();
    pi.registerProvider(provider, {
      api: "openai-completions",
      streamSimple(model, context, options) {
        return responses.streamSimple({...model, api: "openai-responses"}, context, options);
      }
    });
  }
  const evidence = (event, fields = {}) => {
    process.stderr.write(JSON.stringify({type: prefix + event, schema_sha256: digest, ...fields}) + "\n");
  };
  pi.on("before_provider_request", (event, ctx) => {
    if (ctx.model?.provider !== provider || ctx.model?.api !== "openai-completions") {
      throw new Error("Unexpected provider or schema transport");
    }
    const payload = event.payload;
    if (!payload || typeof payload !== "object" || Array.isArray(payload) ||
        (Array.isArray(payload.tools) && payload.tools.length !== 0)) {
      throw new Error("Unexpected tools or provider payload");
    }
    if (provider === "deepseek") {
      if (!Array.isArray(payload.input) || "messages" in payload) {
        throw new Error("Unexpected Responses payload");
      }
      evidence("_bound");
      return {...payload, text: {format: {type: "json_schema", name, schema}}};
    }
    evidence("_bound");
    return {...payload, response_format: {type: "json_schema", json_schema: {name, strict: true, schema}}};
  });
  pi.on("after_provider_response", (event) => evidence("_response", {status: event.status}));
};
`

// PiExtensionSource identifies the trusted adapter template for runtime validation.
func PiExtensionSource() []byte { return []byte(piExtension) }

func renderPiExtension(opts ExecOptions, schema map[string]any) ([]byte, string, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	script := strings.NewReplacer("__SCHEMA__", string(raw), "__DIGEST__", encodeJSON(digest),
		"__PROVIDER__", encodeJSON(opts.BridgeProvider), "__PREFIX__", encodeJSON(opts.SchemaEventPrefix),
		"__NAME__", encodeJSON(opts.SchemaName)).Replace(piExtension)
	return []byte(script), digest, nil
}

type piRPC struct {
	*transport
	seq int
}

func (p *piRPC) request(ctx context.Context, command string, fields map[string]any) (string, error) {
	p.seq++
	id := "bridge-" + strconv.Itoa(p.seq)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["id"], fields["type"] = id, command
	return id, p.send(ctx, fields)
}
func (p *piRPC) call(ctx context.Context, command string, fields map[string]any) (map[string]any, error) {
	id, err := p.request(ctx, command, fields)
	if err != nil {
		return nil, err
	}
	row, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	switch row["type"] {
	case "response":
		if row["id"] != id || row["command"] != command || row["success"] != true {
			return nil, fault("pi_rpc_rejected")
		}
		return object(row["data"]), nil
	case "extension_ui_request":
		return nil, fault("pi_interactive_request_rejected")
	default:
		return nil, fault("pi_unexpected_startup_event")
	}
}

func (p *piRPC) checkState(ctx context.Context, opts ExecOptions) error {
	row, err := p.call(ctx, "get_state", nil)
	if err != nil {
		return err
	}
	model := object(row["model"])
	pending, exists := row["pendingMessageCount"]
	if model["provider"] != opts.BridgeProvider || model["id"] != opts.Model ||
		row["isStreaming"] != false || row["isCompacting"] != false || exists && !equalJSON(pending, 0) {
		return fault("pi_model_or_state_changed")
	}
	return nil
}

func stagePi(opts ExecOptions, w workspace, req *ParsedRequest) (string, string, error) {
	models, err := agents.ReadPiModels(opts.AuthHome)
	if err != nil {
		return "", "", err
	}
	if models != nil {
		if err := writePrivate(filepath.Join(w.private, "models.json"), models, 0o400); err != nil {
			return "", "", err
		}
	}
	if err := os.Symlink(filepath.Join(opts.AuthHome, "auth.json"), filepath.Join(w.private, "auth.json")); err != nil {
		return "", "", err
	}
	settings := map[string]any{"retry": map[string]any{"enabled": false}, "compaction": map[string]any{"enabled": false}}
	if value := opts.Defaults.ThinkingLevel; value != "" {
		settings["defaultThinkingLevel"] = value
	}
	if err := writePrivate(filepath.Join(w.private, "settings.json"), []byte(encodeJSON(settings)), 0o600); err != nil {
		return "", "", err
	}
	extension, digest, err := renderPiExtension(opts, req.TransportSchema)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(w.root, "schema-extension.ts")
	return path, digest, writePrivate(path, extension, 0o400)
}

func ExecutePi(ctx context.Context, opts ExecOptions, req *ParsedRequest) (out string, err error) {
	if opts.BridgeKind == "" {
		opts.BridgeKind = "pi"
	}
	if opts.BridgeKind != "pi" {
		return "", fault("unsupported_generation_bridge", 400)
	}
	opts, err = prepareOptions(opts, req)
	if err != nil {
		return "", err
	}
	if opts.BridgeProvider != "deepseek" && opts.BridgeProvider != "ollama" {
		return "", fault("pi_provider_schema_unsupported", 409)
	}
	w, e := createWorkspace(opts, "pi")
	if e != nil {
		return "", fault("pi_process_unavailable", 503)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.RemoveAll(w.root)
		}
	}()
	extension, digest, e := stagePi(opts, w, req)
	if e != nil {
		if agentErr, ok := e.(*agents.AgentError); ok {
			return "", fault(agentErr.Code, agentErr.Status)
		}
		return "", fault("pi_process_unavailable", 503)
	}
	args := []string{"--mode", "rpc", "--no-session", "--offline", "--no-extensions", "--no-context-files", "--no-skills",
		"--no-prompt-templates", "--no-themes", "--no-builtin-tools", "--no-tools", "--provider", opts.BridgeProvider, "--model", opts.Model,
		"--system-prompt", req.System, "--extension", extension}
	t, err := startTransport(ctx, opts, w, "pi", args, w.environment(opts, "PI_CODING_AGENT_DIR="+w.private, "PI_TELEMETRY=0", "PI_OFFLINE=1"), 1<<20, 8<<20)
	if err != nil {
		return "", err
	}
	p := piRPC{transport: t}
	defer func() {
		if p.interrupted(err) {
			p.interrupt(map[string]any{"id": "bridge-abort", "type": "abort"})
		}
		remove = p.cleanup(&err)
		if !remove {
			out = ""
		}
		if err == nil && !piSchemaEvidence(p.process.diagnostics(), opts.SchemaEventPrefix, digest) {
			out, err = "", fault("pi_native_schema_unconfirmed")
		}
	}()
	if _, err = p.call(ctx, "set_auto_retry", map[string]any{"enabled": false}); err != nil {
		return "", err
	}
	if _, err = p.call(ctx, "set_auto_compaction", map[string]any{"enabled": false}); err != nil {
		return "", err
	}
	if err = p.checkState(ctx, opts); err != nil {
		return "", err
	}
	id, err := p.request(ctx, "prompt", map[string]any{"message": req.Prompt + "\nReturn only JSON matching:\n" + encodeJSON(req.TransportSchema)})
	if err != nil {
		return "", err
	}
	final, err := p.finish(ctx, id)
	if err != nil {
		return "", err
	}
	if err = p.checkState(ctx, opts); err != nil {
		return "", err
	}
	out, err = ValidateOutput(final, req.OriginalSchema, req.TransportSchema, MaxBodyBytes)
	if err != nil {
		return "", fault("pi_schema_failed")
	}
	return out, nil
}

func (p *piRPC) finish(ctx context.Context, id string) (string, error) {
	accepted, ended, hasFinal := false, false, false
	var final string
	for {
		row, err := p.read(ctx)
		if err != nil {
			return "", err
		}
		kind := text(row["type"])
		switch kind {
		case "response":
			if accepted || row["id"] != id || row["command"] != "prompt" || row["success"] != true {
				return "", fault("pi_prompt_rejected")
			}
			accepted = true
		case "message_end":
			msg := object(row["message"])
			if msg["role"] != "assistant" {
				continue
			}
			if hasFinal || ended || msg["stopReason"] != "stop" {
				return "", fault("pi_model_failed")
			}
			var body strings.Builder
			for _, item := range array(msg["content"]) {
				part := object(item)
				switch part["type"] {
				case "text":
					value, ok := part["text"].(string)
					if !ok {
						return "", fault("pi_model_failed")
					}
					body.WriteString(value)
				case "thinking":
				default:
					return "", fault("pi_tool_rejected")
				}
			}
			final, hasFinal = body.String(), true
		case "agent_end":
			if ended || row["willRetry"] != false {
				return "", fault("pi_retry_or_terminal_invalid")
			}
			ended = true
		case "agent_settled":
			if !accepted || !ended || final == "" {
				return "", fault("pi_terminal_incomplete")
			}
			return final, nil
		case "agent_start", "turn_start", "turn_end", "message_start", "message_update":
		default:
			if kind == "extension_ui_request" || kind == "error" || strings.HasPrefix(kind, "tool_") ||
				strings.HasPrefix(kind, "auto_") || strings.HasPrefix(kind, "summarization_") {
				return "", fault("pi_tool_or_retry_rejected")
			}
			return "", fault("pi_event_unrecognized")
		}
	}
}

func piSchemaEvidence(stderr []byte, prefix, digest string) bool {
	bound, responses := 0, 0
	for _, line := range strings.Split(string(stderr), "\n") {
		var entry map[string]any
		if decode([]byte(line), &entry) != nil {
			continue
		}
		switch entry["type"] {
		case prefix + "_bound":
			if entry["schema_sha256"] != digest {
				return false
			}
			bound++
		case prefix + "_response":
			n := number(entry["status"])
			if entry["schema_sha256"] != digest || n == nil || !n.IsInt() || n.Cmp(number(200)) < 0 || n.Cmp(number(300)) >= 0 {
				return false
			}
			responses++
		}
	}
	return bound == 1 && responses == 1
}

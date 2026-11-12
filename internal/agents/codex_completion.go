//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/pelletier/go-toml/v2"
)

type CodexCompletionReceipt struct {
	Thread               string `json:"thread"`
	Turn                 string `json:"turn"`
	PermissionProfile    string `json:"permission_profile"`
	SchemaDigest         string `json:"schema_digest"`
	SourceSchemaDigest   string `json:"source_schema_digest,omitempty"`
	OutputSchemaAccepted bool   `json:"output_schema_accepted"`
	FinalSchemaValid     bool   `json:"final_schema_valid"`
	ToolFree             bool   `json:"tool_free"`
}

// Configuration values and credentials never cross the process-owner boundary
// as JSON. The processing ledger holds only this plan's dependency digest.
type CodexCompletionBinding struct {
	Version, Provider, Model, ConfigurationDigest                            string
	executable, authHome, guard, configDigest, authDigest, environmentDigest string
	selected                                                                 map[string]any
}
type CodexCompletionOptions struct {
	Executable, AuthHome, Provider, Model, GuardExecutable string
	SystemPrompt, Prompt                                   string
	OutputSchema                                           json.RawMessage
	Binding                                                *CodexCompletionBinding
	OnStart                                                func(int) error
	OnEvent                                                func(string)
}
type codexCompletionError struct {
	code  string
	cause error
}

func (e *codexCompletionError) Error() string { return e.code }
func (e *codexCompletionError) Unwrap() error { return e.cause }
func CodexCompletionErrorCode(err error) string {
	var e *codexCompletionError
	if errors.As(err, &e) {
		return e.code
	}
	return "codex_completion_failed"
}
func codexFailure(code string, cause error) error {
	return &codexCompletionError{code: code, cause: cause}
}

func codexCompletionFile(path string, optional bool) ([]byte, string, error) {
	data, _, err := readConfig(path)
	if err != nil {
		return nil, "", codexFailure("codex_configuration_unavailable", nil)
	}
	if data == nil {
		if !optional {
			return nil, "", codexFailure("codex_configuration_required", nil)
		}
		return nil, "absent", nil
	}
	return data, hash(data), nil
}
func codexCompletionEnvironment(selected map[string]any) map[string]string {
	names := map[string]bool{}
	provider, _ := selected["model_provider"].(string)
	providers, _ := selected["model_providers"].(map[string]any)
	definition, _ := providers[provider].(map[string]any)
	if name, ok := definition["env_key"].(string); ok {
		names[name] = true
	}
	if headers, ok := definition["env_http_headers"].(map[string]any); ok {
		for _, value := range headers {
			if name, ok := value.(string); ok {
				names[name] = true
			}
		}
	}
	values := map[string]string{}
	for name := range names {
		if name != "" && !strings.ContainsAny(name, "=\x00") {
			values[name] = os.Getenv(name)
		}
	}
	return values
}

func safeCodexCredentialEnvironment(values map[string]string) bool {
	for name := range values {
		upper := strings.ToUpper(name)
		switch upper {
		case "HOME", "CODEX_HOME", "PATH", "TMPDIR", "TMP", "TEMP", "NODE_OPTIONS", "NODE_PATH", "BASH_ENV", "ENV", "SHELL", "ZDOTDIR", "PYTHONHOME", "PYTHONPATH":
			return false
		}
		for _, prefix := range []string{"MEMLANE_", "PI_", "TRAE_", "LD_", "DYLD_"} {
			if strings.HasPrefix(upper, prefix) {
				return false
			}
		}
	}
	return true
}

func probeCodexCompletion(ctx context.Context, executable string) Discovery {
	d := Discovery{Agent: "codex", Name: "Codex", Path: executable, Protocol: "app-server"}
	root, err := os.MkdirTemp("", "memlane-codex-model-probe-")
	if err != nil {
		d.Error = "Codex version probe unavailable"
		d.ProbeFailureCode = "agent_probe_version_unavailable"
		return d
	}
	defer os.RemoveAll(root)
	run, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(run, executable, "--version")
	command.Dir = root
	command.Env = []string{"HOME=" + root, "CODEX_HOME=" + filepath.Join(root, "codex"), "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin", "TMPDIR=" + root}
	command.WaitDelay = time.Second
	var output diagnosticOutput
	command.Stdout = &output
	if command.Run() != nil {
		d.Error = "Codex version probe failed"
		d.ProbeFailureCode = "agent_probe_version_failed"
		if errors.Is(run.Err(), context.DeadlineExceeded) {
			d.ProbeFailureCode = "agent_probe_version_timeout"
		}
		return d
	}
	d.Version = strings.TrimSpace(output.text.String())
	return d
}

func ResolveCodexCompletion(ctx context.Context, o CodexCompletionOptions) (plan CodexCompletionBinding, resultErr error) {
	return resolveCodexCompletion(ctx, o, false)
}

type CodexModelSelection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Version  string `json:"version"`
}

// Read-only metadata uses the exact production resolver. A native/default
// provider is resolved through a private no-turn handshake, never guessed.
func CodexCompletionSelection(ctx context.Context, i Installation, guard string) (CodexModelSelection, error) {
	if _, ok := ConnectionVerification(i, i.Connection.Generation); !ok || i.Connection.Agent != "codex" || CheckInstallation(i) != nil {
		return CodexModelSelection{}, codexFailure("connection_verification_required", nil)
	}
	plan, err := resolveCodexCompletion(ctx, CodexCompletionOptions{Executable: i.Options.AgentPath, AuthHome: i.Options.CodexDirectory, GuardExecutable: guard}, true)
	return CodexModelSelection{Provider: plan.Provider, Model: plan.Model, Version: plan.Version}, err
}

func resolveCodexCompletion(ctx context.Context, o CodexCompletionOptions, selectionOnly bool) (plan CodexCompletionBinding, resultErr error) {
	if !filepath.IsAbs(o.Executable) || !filepath.IsAbs(o.AuthHome) || !selectionOnly && (o.Provider == "" || o.Model == "") {
		return plan, codexFailure("codex_configuration_required", nil)
	}
	probe := probeCodexCompletion(ctx, o.Executable)
	if probe.ConnectionError() != nil || ComputerConnectionError(probe) != nil {
		if probe.ProbeFailureCode != "" {
			return plan, codexFailure(probe.ProbeFailureCode, nil)
		}
		return plan, codexFailure("unsupported_agent_version", nil)
	}
	data, configDigest, err := codexCompletionFile(filepath.Join(o.AuthHome, "config.toml"), false)
	if err != nil {
		return plan, err
	}
	var configuration map[string]any
	if toml.Unmarshal(data, &configuration) != nil {
		return plan, codexFailure("codex_configuration_invalid", nil)
	}
	if profile, ok := configuration["profile"].(string); ok && profile != "" {
		return plan, codexFailure("codex_profile_unsupported", nil)
	}
	model, _ := configuration["model"].(string)
	provider, _ := configuration["model_provider"].(string)
	if model != "" && !selectionOnly && model != o.Model {
		return plan, codexFailure("codex_model_changed", nil)
	}
	selected := map[string]any{}
	if model != "" {
		selected["model"] = model
	}
	for _, name := range []string{"model_reasoning_effort", "model_reasoning_summary", "model_verbosity", "service_tier"} {
		if value := configuration[name]; value != nil {
			selected[name] = value
		}
	}
	if provider != "" {
		selected["model_provider"] = provider
	}
	if definitions, ok := configuration["model_providers"].(map[string]any); ok {
		if provider == "" {
			selected["model_providers"] = definitions
		} else if definition := definitions[provider]; definition != nil {
			selected["model_providers"] = map[string]any{provider: definition}
		}
	}
	if provider == "" || model == "" {
		inspect, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		root, e := os.MkdirTemp("", "memlane-codex-provider-")
		if e != nil {
			return plan, codexFailure("codex_configuration_unavailable", e)
		}
		defer os.RemoveAll(root)
		actual, e := resolveCodexCompletionNativeProvider(inspect, o.Executable, o.AuthHome, o.GuardExecutable, root, selected)
		err = e
		if err != nil {
			return plan, codexFailure("codex_provider_unconfirmed", err)
		}
		if model != "" && model != actual.Model || provider != "" && provider != actual.Provider {
			return plan, codexFailure("codex_model_changed", nil)
		}
		provider, model = actual.Provider, actual.Model
		selected["model"] = model
		selected["model_provider"] = provider
		if definitions, ok := selected["model_providers"].(map[string]any); ok {
			if definition := definitions[provider]; definition != nil {
				selected["model_providers"] = map[string]any{provider: definition}
			} else {
				delete(selected, "model_providers")
			}
		}
	}
	if !selectionOnly && (provider != o.Provider || model != o.Model) {
		return plan, codexFailure("codex_model_changed", nil)
	}
	_, authDigest, err := codexCompletionFile(filepath.Join(o.AuthHome, "auth.json"), true)
	if err != nil {
		return plan, err
	}
	credentialEnvironment := codexCompletionEnvironment(selected)
	if !safeCodexCredentialEnvironment(credentialEnvironment) {
		return plan, codexFailure("codex_credential_environment_unsupported", nil)
	}
	envDigest := hash(mustJSON(credentialEnvironment))
	plan = CodexCompletionBinding{Version: probe.Version, Provider: provider, Model: model, executable: o.Executable, authHome: o.AuthHome, guard: o.GuardExecutable, selected: selected, configDigest: configDigest, authDigest: authDigest, environmentDigest: envDigest}
	plan.ConfigurationDigest = hash(mustJSON([]any{CodexCompletionProtocol, probe.Version, selected, configDigest, authDigest, envDigest}))
	return plan, nil
}

func resolveCodexCompletionNativeProvider(ctx context.Context, executable, authHome, guard, root string, selected map[string]any) (selection CodexModelSelection, resultErr error) {
	private, work, home := filepath.Join(root, "codex"), filepath.Join(root, "task"), filepath.Join(root, "home")
	for _, dir := range []string{private, work, home} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return selection, err
		}
	}
	configuration := map[string]any{}
	for name, value := range selected {
		configuration[name] = value
	}
	configuration["features"] = map[string]bool{"hooks": false, "plugins": false, "apps": false, "memories": false, "shell_tool": false, "unified_exec": false, "multi_agent": false, "multi_agent_v2": false}
	configuration["web_search"], configuration["project_doc_max_bytes"] = "disabled", 0
	data, err := toml.Marshal(configuration)
	if err != nil {
		return selection, err
	}
	if err = os.WriteFile(filepath.Join(private, "config.toml"), data, 0600); err != nil {
		return selection, err
	}
	auth, _, err := codexCompletionFile(filepath.Join(authHome, "auth.json"), true)
	if err != nil {
		return selection, err
	}
	if auth != nil {
		err = os.WriteFile(filepath.Join(private, "auth.json"), auth, 0600)
		clear(auth)
		if err != nil {
			return selection, err
		}
	}
	rpc, err := startCodexRPC(ctx, executable, guard, []string{"app-server", "--strict-config", "--stdio"}, []string{"HOME=" + home, "CODEX_HOME=" + private, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin", "TMPDIR=" + root}, work, nil)
	if err != nil {
		return selection, err
	}
	defer func() { resultErr = errors.Join(resultErr, rpc.close(nil)) }()
	if err = rpc.initialize(ctx); err != nil {
		return selection, err
	}
	var reply struct {
		Model    string `json:"model"`
		Provider string `json:"modelProvider"`
	}
	if err = rpc.call(ctx, "thread/start", map[string]any{"cwd": work, "runtimeWorkspaceRoots": []string{}, "model": selected["model"], "approvalPolicy": "never", "ephemeral": true, "allowProviderModelFallback": false, "dynamicTools": []any{}}, &reply); err != nil {
		return selection, err
	}
	if expected, ok := selected["model"].(string); ok && reply.Model != expected || strings.TrimSpace(reply.Provider) == "" || strings.TrimSpace(reply.Model) == "" {
		return selection, errors.New("Codex native model selection changed")
	}
	return CodexModelSelection{Provider: reply.Provider, Model: reply.Model}, nil
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func (b CodexCompletionBinding) unchanged() error {
	_, config, err := codexCompletionFile(filepath.Join(b.authHome, "config.toml"), false)
	if err != nil {
		return err
	}
	_, auth, err := codexCompletionFile(filepath.Join(b.authHome, "auth.json"), true)
	if err != nil {
		return err
	}
	if config != b.configDigest || auth != b.authDigest || hash(mustJSON(codexCompletionEnvironment(b.selected))) != b.environmentDigest {
		return codexFailure("codex_dependency_changed", nil)
	}
	return nil
}

// RunCodexCompletion has one fresh ephemeral thread/turn and no registered or
// built-in tools. Strict transport and original schemas are both checked locally.
// The shared carrier's Pi HTTP status remains zero; Codex has its own receipt.
func RunCodexCompletion(ctx context.Context, o CodexCompletionOptions) (out PiCompletionExecution, resultErr error) {
	out.Events, out.Items = map[string]int{}, map[string]int{}
	out.ExitCode = -1
	stage := "schema"
	defer func() {
		if resultErr != nil {
			out.Final = nil
			out.FailureStage = stage
			out.FailureCode = CodexCompletionErrorCode(resultErr)
			if errors.Is(resultErr, context.Canceled) {
				out.FailureCode = "codex_cancelled"
			}
			if errors.Is(resultErr, context.DeadlineExceeded) {
				out.FailureCode = "codex_timeout"
			}
			out.Failure = out.FailureCode
		}
	}()
	if o.SystemPrompt == "" || o.Prompt == "" || len(o.Prompt) > 1<<20 || len(o.OutputSchema) > 65536 || !json.Valid(o.OutputSchema) {
		return out, codexFailure("codex_schema_invalid", nil)
	}
	var schema jsonschema.Schema
	if json.Unmarshal(o.OutputSchema, &schema) != nil {
		return out, codexFailure("codex_schema_invalid", nil)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return out, codexFailure("codex_schema_invalid", nil)
	}
	transportSchema, err := codexCompletionSchema(o.OutputSchema)
	if err != nil || len(transportSchema) > 65536 {
		return out, codexFailure("codex_schema_unsupported", nil)
	}
	var strictSchema jsonschema.Schema
	if json.Unmarshal(transportSchema, &strictSchema) != nil {
		return out, codexFailure("codex_schema_invalid", nil)
	}
	strictResolved, err := strictSchema.Resolve(nil)
	if err != nil {
		return out, codexFailure("codex_schema_invalid", nil)
	}
	stage = "configuration"
	var plan CodexCompletionBinding
	if o.Binding != nil {
		plan = *o.Binding
	} else {
		plan, err = ResolveCodexCompletion(ctx, o)
		if err != nil {
			return out, err
		}
	}
	if plan.selected == nil || plan.Provider != o.Provider || plan.Model != o.Model {
		return out, codexFailure("codex_model_changed", nil)
	}
	if err = plan.unchanged(); err != nil {
		return out, err
	}
	probe := probeCodexCompletion(ctx, plan.executable)
	if probe.ConnectionError() != nil || probe.Version != plan.Version {
		return out, codexFailure("unsupported_agent_version", nil)
	}
	out.AgentVersion, out.SelectedProvider, out.SelectedModel = plan.Version, plan.Provider, plan.Model
	root, err := os.MkdirTemp("", "memlane-codex-completion-")
	if err != nil {
		return out, codexFailure("codex_private_directory_failed", err)
	}
	defer os.RemoveAll(root)
	home, private, work := filepath.Join(root, "home"), filepath.Join(root, "codex"), filepath.Join(root, "task")
	for _, dir := range []string{home, private, work} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return out, codexFailure("codex_private_directory_failed", err)
		}
	}
	configuration := map[string]any{}
	for name, value := range plan.selected {
		configuration[name] = value
	}
	configuration["default_permissions"] = "memlane_completion"
	configuration["permissions"] = map[string]any{"memlane_completion": map[string]any{"filesystem": map[string]string{"/": "deny", ":minimal": "read", work: "read"}, "network": map[string]bool{"enabled": false}}}
	features := map[string]bool{}
	for _, name := range []string{"shell_tool", "unified_exec", "apps", "plugins", "hooks", "memories", "multi_agent", "multi_agent_v2", "browser_use", "browser_use_external", "computer_use", "image_generation", "skill_search", "skill_mcp_dependency_install", "workspace_dependencies", "code_mode_host", "view_image", "remote_plugin"} {
		features[name] = false
	}
	configuration["features"], configuration["web_search"], configuration["project_doc_max_bytes"] = features, "disabled", 0
	configuration["skills"] = map[string]any{"include_instructions": false, "bundled": map[string]bool{"enabled": false}}
	configuration["analytics"], configuration["feedback"] = map[string]bool{"enabled": false}, map[string]bool{"enabled": false}
	data, err := toml.Marshal(configuration)
	if err != nil {
		return out, codexFailure("codex_private_configuration_invalid", nil)
	}
	if err = os.WriteFile(filepath.Join(private, "config.toml"), data, 0600); err != nil {
		return out, codexFailure("codex_private_directory_failed", err)
	}
	// This is a bounded credential snapshot in Codex's own private home, never
	// a Cognee setting or ledger payload. Token refresh cannot rewrite the source.
	auth, authDigest, err := codexCompletionFile(filepath.Join(plan.authHome, "auth.json"), true)
	if err != nil {
		return out, err
	}
	if authDigest != plan.authDigest {
		return out, codexFailure("codex_dependency_changed", nil)
	}
	if auth != nil {
		err = os.WriteFile(filepath.Join(private, "auth.json"), auth, 0600)
		clear(auth)
		if err != nil {
			return out, codexFailure("codex_private_directory_failed", err)
		}
	}
	env := []string{"HOME=" + home, "CODEX_HOME=" + private, "PATH=" + filepath.Dir(plan.executable) + ":/usr/bin:/bin", "TMPDIR=" + root}
	for name, value := range codexCompletionEnvironment(plan.selected) {
		env = append(env, name+"="+value)
	}
	for _, name := range []string{"LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	stage = "process"
	run, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rpc, err := startCodexRPC(run, plan.executable, plan.guard, []string{"app-server", "--strict-config", "--stdio"}, env, work, o.OnStart)
	if err != nil {
		return out, codexFailure("codex_process_failed", err)
	}
	defer func() {
		closeErr := rpc.close(&out.ProposalProcess)
		if closeErr != nil {
			resultErr = errors.Join(resultErr, codexFailure("codex_process_cleanup_failed", closeErr))
		}
	}()
	if err = rpc.initialize(run); err != nil {
		return out, codexFailure("codex_initialize_failed", err)
	}
	stage = "thread"
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model    string   `json:"model"`
		Provider string   `json:"modelProvider"`
		Roots    []string `json:"runtimeWorkspaceRoots"`
		Profile  *struct {
			ID string `json:"id"`
		} `json:"activePermissionProfile"`
	}
	if err = rpc.call(run, "thread/start", map[string]any{"cwd": work, "runtimeWorkspaceRoots": []string{}, "permissions": "memlane_completion", "model": plan.Model, "modelProvider": plan.Provider, "approvalPolicy": "never", "ephemeral": true, "allowProviderModelFallback": false, "dynamicTools": []any{}, "baseInstructions": o.SystemPrompt, "developerInstructions": "Only complete this isolated processing request. No tools, files, memory collection or external sessions are authorized."}, &started); err != nil {
		return out, codexFailure("codex_thread_failed", err)
	}
	if started.Thread.ID == "" || started.Model != plan.Model || started.Provider != plan.Provider || len(started.Roots) != 0 || started.Profile == nil || started.Profile.ID != "memlane_completion" {
		return out, codexFailure("codex_model_or_permissions_changed", nil)
	}
	out.Observation = &CompletionObservation{IsolationConfigured: true, IsolationPolicy: "codex_private_ephemeral_no_tools_v1"}
	out.ResolvedProvider, out.ResolvedModel = started.Provider, started.Model
	out.Codex = &CodexCompletionReceipt{Thread: started.Thread.ID, PermissionProfile: started.Profile.ID, SchemaDigest: hash(transportSchema), SourceSchemaDigest: hash(o.OutputSchema), ToolFree: true}
	stage = "turn"
	rpc.nextID++
	request := rpc.nextID
	if err = rpc.send(run, map[string]any{"id": request, "method": "turn/start", "params": map[string]any{"threadId": started.Thread.ID, "input": []any{map[string]any{"type": "text", "text": o.Prompt, "text_elements": []any{}}}, "outputSchema": transportSchema, "permissions": "memlane_completion"}}); err != nil {
		return out, codexFailure("codex_turn_failed", err)
	}
	var final string
	var announcedTurn string
	for {
		message, e := rpc.next(run)
		if e != nil {
			if run.Err() != nil {
				out.Cancelled = true
				codexInterruptCompletion(rpc, &out)
				return out, codexFailure("codex_completion_interrupted", run.Err())
			}
			return out, codexFailure("codex_transport_closed", e)
		}
		if message.Method != "" {
			out.Events[message.Method]++
		}
		if len(message.ID) > 0 && message.Method == "" {
			var id int
			var accepted struct {
				Turn struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if json.Unmarshal(message.ID, &id) != nil || id != request || len(message.Error) > 0 || json.Unmarshal(message.Result, &accepted) != nil || accepted.Turn.ID == "" {
				return out, codexFailure("codex_turn_rejected", nil)
			}
			if announcedTurn != "" && announcedTurn != accepted.Turn.ID {
				return out, codexFailure("codex_event_identity_invalid", nil)
			}
			out.Accepted = true
			out.Codex.Turn = accepted.Turn.ID
			out.NativeSchemaRequested = true
			out.Codex.OutputSchemaAccepted = true
			if o.OnEvent != nil {
				o.OnEvent("model_request_sent")
			}
			continue
		}
		if len(message.ID) > 0 {
			out.Codex.ToolFree = false
			return out, codexFailure("codex_tool_or_approval_requested", nil)
		}
		if message.Method == "error" {
			var event struct {
				Error json.RawMessage `json:"error"`
			}
			_ = json.Unmarshal(message.Params, &event)
			return out, codexFailure(codexCompletionRemoteCode(event.Error), nil)
		}
		if message.Method == "turn/started" {
			var event struct {
				Thread string `json:"threadId"`
				Turn   struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if json.Unmarshal(message.Params, &event) != nil || event.Thread != out.Codex.Thread || event.Turn.ID == "" || out.Accepted && event.Turn.ID != out.Codex.Turn || announcedTurn != "" && announcedTurn != event.Turn.ID {
				return out, codexFailure("codex_event_identity_invalid", nil)
			}
			announcedTurn = event.Turn.ID
		}
		if strings.HasPrefix(message.Method, "item/") && message.Method != "item/started" && message.Method != "item/completed" && !strings.HasPrefix(message.Method, "item/agentMessage/") && !strings.HasPrefix(message.Method, "item/reasoning/") {
			out.Codex.ToolFree = false
			return out, codexFailure("codex_unexpected_tool_execution", nil)
		}
		if message.Method == "item/started" || message.Method == "item/completed" {
			var item struct {
				Thread string                             `json:"threadId"`
				Turn   string                             `json:"turnId"`
				Item   struct{ Type, Text, Phase string } `json:"item"`
			}
			if json.Unmarshal(message.Params, &item) != nil || !out.Accepted || item.Thread != out.Codex.Thread || item.Turn != out.Codex.Turn {
				return out, codexFailure("codex_event_identity_invalid", nil)
			}
			if item.Item.Type != "agentMessage" && item.Item.Type != "reasoning" && item.Item.Type != "userMessage" {
				out.Codex.ToolFree = false
				return out, codexFailure("codex_unexpected_tool_execution", nil)
			}
			if message.Method == "item/completed" {
				out.Items[item.Item.Type]++
				if item.Item.Type == "agentMessage" && item.Item.Phase != "commentary" {
					if final != "" {
						return out, codexFailure("codex_multiple_final_answers", nil)
					}
					final = item.Item.Text
				}
			}
		}
		if strings.HasPrefix(message.Method, "item/agentMessage/") || strings.HasPrefix(message.Method, "item/reasoning/") {
			var event struct {
				Thread string `json:"threadId"`
				Turn   string `json:"turnId"`
			}
			if json.Unmarshal(message.Params, &event) != nil || !out.Accepted || event.Thread != out.Codex.Thread || event.Turn != out.Codex.Turn {
				return out, codexFailure("codex_event_identity_invalid", nil)
			}
			if o.OnEvent != nil {
				o.OnEvent("model_update")
			}
		}
		if message.Method == "turn/completed" {
			var terminal struct {
				Thread string `json:"threadId"`
				Turn   struct {
					ID, Status string
					Error      json.RawMessage
				} `json:"turn"`
			}
			if json.Unmarshal(message.Params, &terminal) != nil || !out.Accepted || terminal.Thread != out.Codex.Thread || terminal.Turn.ID != out.Codex.Turn {
				return out, codexFailure("codex_terminal_identity_invalid", nil)
			}
			out.Terminal = true
			if terminal.Turn.Status != "completed" || len(terminal.Turn.Error) > 0 && string(terminal.Turn.Error) != "null" {
				return out, codexFailure(codexCompletionRemoteCode(terminal.Turn.Error), nil)
			}
			break
		}
	}
	stage = "result"
	if err = run.Err(); err != nil {
		return out, codexFailure("codex_completion_interrupted", err)
	}
	if len(final) == 0 || len(final) > 2<<20 {
		return out, codexFailure("codex_result_invalid", nil)
	}
	out.Observation.ResponseComplete = true
	var value any
	if json.Unmarshal([]byte(final), &value) != nil || resolved.Validate(value) != nil || strictResolved.Validate(value) != nil {
		return out, codexFailure("codex_schema_failed", nil)
	}
	if err = plan.unchanged(); err != nil {
		return out, err
	}
	out.Final = json.RawMessage(final)
	out.FinalFormat = "codex-app-server-json-schema-v1"
	out.Codex.FinalSchemaValid = true
	out.Observation.LocalSchemaValid = true
	return out, nil
}

// Only the versioned app-server machine error shape is interpreted. Upstream
// prose, provider bodies and credentials are never returned or persisted.
func codexCompletionRemoteCode(raw json.RawMessage) string {
	var failure struct {
		Info json.RawMessage `json:"codexErrorInfo"`
	}
	if len(raw) > 65536 || json.Unmarshal(raw, &failure) != nil {
		return "codex_model_failed"
	}
	var kind string
	if json.Unmarshal(failure.Info, &kind) == nil {
		switch kind {
		case "unauthorized":
			return "codex_authentication_failed"
		case "usageLimitExceeded", "sessionBudgetExceeded":
			return "codex_quota_exceeded"
		case "rateLimitExceeded":
			return "codex_rate_limited"
		case "modelNotFound":
			return "codex_model_unavailable"
		case "contextWindowExceeded":
			return "codex_context_exceeded"
		case "badRequest":
			return "codex_request_invalid"
		case "sandboxError":
			return "codex_isolation_failed"
		case "internalServerError", "serverOverloaded", "flexUnavailable", "policyCapacityUnavailable":
			return "codex_provider_unavailable"
		}
		return "codex_model_failed"
	}
	var variants map[string]struct {
		Status *int `json:"httpStatusCode"`
	}
	if json.Unmarshal(failure.Info, &variants) != nil {
		return "codex_model_failed"
	}
	for _, name := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"} {
		value, ok := variants[name]
		if !ok {
			continue
		}
		if value.Status != nil {
			switch status := *value.Status; {
			case status == 401 || status == 403:
				return "codex_authentication_failed"
			case status == 429:
				return "codex_rate_limited"
			case status == 408 || status == 504:
				return "codex_timeout"
			case status >= 500:
				return "codex_provider_unavailable"
			case status >= 400:
				return "codex_request_invalid"
			}
		}
		return "codex_transport_closed"
	}
	return "codex_model_failed"
}

func codexInterruptCompletion(rpc *codexRPC, out *PiCompletionExecution) {
	if out.Codex == nil || out.Codex.Turn == "" {
		return
	}
	stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rpc.nextID++
	id := rpc.nextID
	out.AbortSent = rpc.send(stop, map[string]any{"id": id, "method": "turn/interrupt", "params": map[string]string{"threadId": out.Codex.Thread, "turnId": out.Codex.Turn}}) == nil
	for out.AbortSent {
		message, err := rpc.next(stop)
		if err != nil {
			return
		}
		if len(message.ID) > 0 && message.Method == "" {
			var got int
			if json.Unmarshal(message.ID, &got) == nil && got == id && len(message.Error) == 0 {
				out.AbortAcknowledged = true
			}
		}
		if message.Method == "turn/completed" {
			var terminal struct {
				Thread string              `json:"threadId"`
				Turn   struct{ ID string } `json:"turn"`
			}
			if json.Unmarshal(message.Params, &terminal) == nil && terminal.Thread == out.Codex.Thread && terminal.Turn.ID == out.Codex.Turn {
				out.Terminal = true
				return
			}
		}
	}
}

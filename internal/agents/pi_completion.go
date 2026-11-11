package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// PiCompletionOptions is a model-only task. It grants no memory or file tools.
type PiCompletionOptions struct {
	Executable, AuthHome, Provider, Model string
	SystemPrompt, Prompt                  string
	OutputSchema                          json.RawMessage
	GuardExecutable                       string
	OnStart                               func(int) error
	OnEvent                               func(string)
}
type PiCompletionExecution struct {
	Observation      *CompletionObservation  `json:"observation,omitempty"`
	Codex            *CodexCompletionReceipt `json:"codex,omitempty"`
	AgentVersion     string                  `json:"agent_version,omitempty"`
	SelectedProvider string                  `json:"selected_provider,omitempty"`
	SelectedModel    string                  `json:"selected_model,omitempty"`
	PiRPCProcess
}

// RunPiCompletion reuses the verified RPC lifecycle and isolated Agent config.
func RunPiCompletion(ctx context.Context, o PiCompletionOptions) (PiCompletionExecution, error) {
	return runPiCompletion(ctx, o, nil)
}

func runPiCompletion(ctx context.Context, o PiCompletionOptions, project *piProjectTools) (out PiCompletionExecution, resultErr error) {
	stage, code := "schema", "pi_schema_invalid"
	completionStarted := time.Now()
	defer func() {
		d := out.Diagnostics
		if d == nil {
			d = &CompletionDiagnostics{Stage: "preflight", ParentContext: CompletionContextState(ctx.Err())}
		}
		d.ElapsedMS = time.Since(completionStarted).Milliseconds()
		if resultErr != nil && (d.FailureCode == "" || stage == "output_schema") {
			d.Stage, d.FailureCode = "preflight", "preflight_failed"
			if stage == "output_schema" {
				d.Stage, d.FailureCode = "output_schema", "output_schema_invalid"
			}
			if errors.Is(resultErr, context.DeadlineExceeded) {
				d.FailureCode = "deadline_exceeded"
			} else if errors.Is(resultErr, context.Canceled) {
				d.FailureCode = "cancelled"
			}
		}
		if resultErr == nil {
			d.Stage, d.FailureCode = "completed", ""
		}
		out.Diagnostics = SafeCompletionDiagnostics(d)
		if resultErr == nil {
			return
		}
		out.FailureStage, out.FailureCode = stage, code
		switch {
		case errors.Is(resultErr, context.Canceled):
			out.FailureCode = "pi_cancelled"
		case errors.Is(resultErr, context.DeadlineExceeded):
			out.FailureCode = "pi_timeout"
		case errors.Is(resultErr, syscall.ENOSPC):
			out.FailureCode = "pi_storage_full"
		case errors.Is(resultErr, os.ErrPermission):
			out.FailureCode = "pi_permission_denied"
		case errors.Is(resultErr, os.ErrNotExist):
			out.FailureCode = "pi_file_unavailable"
		}
	}()
	if o.SystemPrompt == "" || o.Prompt == "" || len(o.Prompt) > 1024*1024 || len(o.OutputSchema) > 65536 || !json.Valid(o.OutputSchema) {
		return out, errors.New("bounded prompt and JSON schema required")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(o.OutputSchema, &schema); err != nil {
		return out, errors.New("invalid completion schema")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return out, errors.New("invalid completion schema")
	}
	stage, code = "auth_directory", "pi_auth_directory_invalid"
	home, err := os.UserHomeDir()
	if err != nil {
		return out, err
	}
	if o.AuthHome == "" {
		o.AuthHome = os.Getenv("PI_CODING_AGENT_DIR")
		if o.AuthHome == "" {
			o.AuthHome = filepath.Join(home, ".pi/agent")
		}
	}
	if project != nil && project.config != nil {
		if err := project.config.verify(o.AuthHome); err != nil {
			return out, err
		}
	}
	if !filepath.IsAbs(o.AuthHome) {
		return out, errors.New("absolute pi configuration directory required")
	}
	if o.Executable == "" {
		for _, found := range Discover(ctx, home, nil) {
			if found.Agent == "pi" {
				o.Executable = found.Path
				break
			}
		}
	}
	stage, code = "agent_probe", "pi_agent_probe_failed"
	probe := Probe(ctx, "pi", o.Executable)
	out.AgentVersion = probe.Version
	if probe.ConnectionError() != nil {
		if probe.ProbeFailureCode != "" {
			code = "pi_" + probe.ProbeFailureCode
		}
		return out, errors.New("pi RPC is only adapted for the verified installed 0.84.1 protocol")
	}
	if probe.Version != "0.84.1" {
		code = "pi_agent_version_unsupported"
		return out, errors.New("pi RPC is only adapted for the verified installed 0.84.1 protocol")
	}
	stage, code = "model_settings", "pi_model_settings_invalid"
	var settings struct {
		Provider string `json:"defaultProvider"`
		Model    string `json:"defaultModel"`
	}
	if b, e := os.ReadFile(filepath.Join(o.AuthHome, "settings.json")); e == nil {
		if project != nil && project.config != nil && hash(b) != project.config.settingsHash {
			return out, errors.New("bound pi settings changed")
		}
		if len(b) > 1024*1024 || json.Unmarshal(b, &settings) != nil {
			return out, errors.New("pi model settings invalid; original preserved")
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
	if o.Provider == "" {
		o.Provider = settings.Provider
	}
	if o.Model == "" {
		o.Model = settings.Model
	}
	stage, code = "model_selection", "pi_model_selection_required"
	if o.Provider == "" || o.Model == "" {
		return out, errors.New("select pi provider/model before running a background task")
	}
	if o.Provider != "ollama" && o.Provider != "deepseek" {
		code = "pi_schema_binding_unsupported"
		return out, errors.New("pi provider has no adapted full JSON schema request binding")
	}
	out.SelectedProvider, out.SelectedModel = o.Provider, o.Model
	stage, code = "private_directory", "pi_private_directory_failed"
	root, err := os.MkdirTemp("", "memlane-pi-rpc-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(root)
	private, work, privateHome := filepath.Join(root, "agent"), filepath.Join(root, "task"), filepath.Join(root, "home")
	for _, dir := range []string{private, work, privateHome} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return out, err
		}
	}
	// models.json is pi-owned provider configuration, not model knowledge.
	// Command-valued configuration could execute a shell outside the tool list;
	// reject it rather than weakening background isolation.
	stage, code = "provider_configuration", "pi_provider_configuration_invalid"
	expectedModels := ""
	if project != nil && project.config != nil {
		expectedModels = project.config.modelsHash
	}
	if err = copyPiModelDefinitions(o.AuthHome, private, expectedModels); err != nil {
		if errors.Is(err, errPiCommandConfiguration) {
			code = "pi_command_configuration_unsupported"
		}
		return out, err
	}

	if project != nil && project.config != nil {
		if err := project.config.verify(o.AuthHome); err != nil {
			return out, err
		}
	}
	stage, code = "auth_link", "pi_auth_link_failed"
	for _, name := range []string{"auth.json"} {
		source := filepath.Join(o.AuthHome, name)
		if info, e := os.Stat(source); e == nil {
			if !info.Mode().IsRegular() {
				return out, errors.New("pi auth/model source must be a regular file")
			}
			if err = os.Symlink(source, filepath.Join(private, name)); err != nil {
				return out, err
			}
		} else if !os.IsNotExist(e) {
			return out, e
		}
	}
	env := []string{"HOME=" + privateHome, "PI_CODING_AGENT_DIR=" + private, "PI_TELEMETRY=0", "PI_OFFLINE=1", "PATH=" + filepath.Dir(o.Executable) + ":" + os.Getenv("PATH")}
	for _, key := range []string{"TMPDIR", "LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	args := []string{"--mode", "rpc", "--no-session", "--offline", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-builtin-tools", "--no-tools", "--provider", o.Provider, "--model", o.Model}
	instructions := o.SystemPrompt
	// Replace the coding assistant's default system context for this isolated
	// pure-suggestion task. No user settings or model selection is rewritten.
	args = append(args, "--system-prompt", instructions)
	sum := sha256.Sum256(o.OutputSchema)
	schemaDigest := hex.EncodeToString(sum[:])
	extension := strings.ReplaceAll(piStructuredOutput, "__MEMLANE_OUTPUT_SCHEMA__", string(o.OutputSchema))
	extension = strings.ReplaceAll(extension, "__MEMLANE_SCHEMA_DIGEST__", schemaDigest)
	extension = strings.ReplaceAll(extension, "__MEMLANE_SCHEMA_PROVIDER__", o.Provider)
	if project != nil {
		extension = project.extension(extension)
	}
	path := filepath.Join(root, "schema-extension.ts")
	stage, code = "schema_extension", "pi_schema_extension_failed"
	if err = os.WriteFile(path, []byte(extension), 0400); err != nil {
		return out, err
	}
	args = append(args, "--extension", path)
	if project != nil {
		for index, argument := range args {
			if argument == "--no-tools" {
				args = append(args[:index], args[index+1:]...)
				break
			}
		}
		args = append(args, "--tools", "memlane_project_tree,memlane_project_source,memlane_project_knowledge,memlane_project_finish")
	}
	prompt := o.Prompt + "\nReturn ONLY JSON matching this schema:\n" + string(o.OutputSchema)
	stage, code = "rpc", "pi_rpc_failed"
	process, err := runPiRPCProcessWithProject(ctx, o.Executable, args, env, work, prompt, o.GuardExecutable, o.OnStart, o.OnEvent, func(provider, model string) error {
		if provider != o.Provider || model != o.Model {
			return errors.New("pi resolved a different model; choose its exact ID explicitly")
		}
		return nil
	}, schemaDigest, project)
	out.PiRPCProcess = process
	if project == nil {
		out.Observation = &CompletionObservation{ResponseComplete: process.Accepted && process.Terminal && len(process.Final) > 0, IsolationConfigured: process.Started, IsolationPolicy: "pi_private_no_session_no_tools_v1"}
	}
	if err != nil {
		return out, err
	}
	if process.ResolvedProvider != o.Provider || process.ResolvedModel != o.Model {
		code = "pi_resolved_model_changed"
		return out, errors.New("pi resolved a different model; choose its exact ID explicitly")
	}
	var value any
	stage, code = "output_schema", "pi_output_schema_invalid"
	if json.Unmarshal(process.Final, &value) != nil || resolved.Validate(value) != nil {
		out.Final = nil
		return out, errors.New("pi completion did not match the requested JSON schema")
	}
	if out.Observation != nil {
		out.Observation.LocalSchemaValid = true
	}
	return out, nil
}

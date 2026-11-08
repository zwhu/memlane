package agents

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

//go:embed pi-structured-output.ts
var piStructuredOutput string

type PiOrganizationRunOptions struct {
	Executable, AuthHome, Provider, Model string
	GuardExecutable                       string
	OnStart                               func(int) error
	OnEvent                               func(string)
	Snapshot                              records.OrganizationSnapshot
}

type PiOrganizationExecution struct {
	AgentVersion     string                      `json:"agent_version,omitempty"`
	Result           *records.OrganizationResult `json:"result,omitempty"`
	SelectedProvider string                      `json:"selected_provider,omitempty"`
	SelectedModel    string                      `json:"selected_model,omitempty"`
	PiRPCProcess
}

// Pi is an explicitly selected alternate runner. traex remains the default.
// The Agent reads its own auth/model files through links; no tokens are copied
// to the trusted memory service, component worker or result evidence.
func RunPiOrganization(ctx context.Context, o PiOrganizationRunOptions) (out PiOrganizationExecution, resultErr error) {
	checked := o.Snapshot
	if checked.Seal() != nil || checked.Digest != o.Snapshot.Digest {
		return out, records.MergeError("invalid_proposal")
	}
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
	probe := Probe(ctx, "pi", o.Executable)
	if probe.ConnectionError() != nil || probe.Version != "0.84.1" {
		return out, errors.New("pi RPC is only adapted for the verified installed 0.84.1 protocol")
	}
	out.AgentVersion = probe.Version
	var settings struct {
		Provider string `json:"defaultProvider"`
		Model    string `json:"defaultModel"`
	}
	if b, e := os.ReadFile(filepath.Join(o.AuthHome, "settings.json")); e == nil {
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
	if o.Provider == "" || o.Model == "" {
		return out, errors.New("select pi provider/model before running a background task")
	}
	out.SelectedProvider, out.SelectedModel = o.Provider, o.Model
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
	if b, e := os.ReadFile(filepath.Join(o.AuthHome, "models.json")); e == nil {
		var configuration any
		if len(b) > 1024*1024 || json.Unmarshal(b, &configuration) != nil {
			return out, errors.New("pi provider configuration invalid")
		}
		var commandValue func(any) bool
		commandValue = func(v any) bool {
			switch x := v.(type) {
			case string:
				return strings.HasPrefix(x, "!")
			case []any:
				for _, value := range x {
					if commandValue(value) {
						return true
					}
				}
			case map[string]any:
				for _, value := range x {
					if commandValue(value) {
						return true
					}
				}
			}
			return false
		}
		if commandValue(configuration) {
			return out, errors.New("pi command-valued provider configuration is not supported by the isolated runner")
		}
		// The checked bytes are the bytes pi consumes. An original config edit
		// after validation must not introduce command-valued credentials.
		if err = os.WriteFile(filepath.Join(private, "models.json"), b, 0400); err != nil {
			return out, err
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
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
	instructions := organizationPrompt
	if o.Snapshot.Schema == records.OrganizationReviewSchema {
		instructions = organizationReviewPrompt
	}
	if o.Snapshot.Schema == records.OrganizationGlobalSchema {
		instructions = organizationReviewPrompt + organizationGlobalPrompt
	}
	// Replace the coding assistant's default system context for this isolated
	// pure-suggestion task. No user settings or model selection is rewritten.
	args = append(args, "--system-prompt", instructions)
	schemaDigest := ""
	if o.Provider == "ollama" {
		schema := o.Snapshot.OutputSchema()
		sum := sha256.Sum256(schema)
		schemaDigest = hex.EncodeToString(sum[:])
		extension := strings.ReplaceAll(piStructuredOutput, "__MEMLANE_OUTPUT_SCHEMA__", string(schema))
		extension = strings.ReplaceAll(extension, "__MEMLANE_SCHEMA_DIGEST__", schemaDigest)
		path := filepath.Join(root, "schema-extension.ts")
		if err = os.WriteFile(path, []byte(extension), 0400); err != nil {
			return out, err
		}
		args = append(args, "--extension", path)
	}
	snapshot, _ := json.Marshal(o.Snapshot)
	prompt := instructions + "\n宿主指定result_id=" + o.Snapshot.ProposalID() + "\n严格输出schema：\n" + string(o.Snapshot.OutputSchema()) + "\n冻结授权材料：\n" + string(snapshot)
	process, err := runPiRPCProcessOwned(ctx, o.Executable, args, env, work, prompt, o.GuardExecutable, o.OnStart, o.OnEvent, func(provider, model string) error {
		if provider != o.Provider || model != o.Model {
			return errors.New("pi resolved a different model; choose its exact ID explicitly")
		}
		return nil
	}, schemaDigest)
	out.PiRPCProcess = process
	if err != nil {
		return out, err
	}
	if process.ResolvedProvider != o.Provider || process.ResolvedModel != o.Model {
		return out, errors.New("pi resolved a different model; choose its exact ID explicitly")
	}
	decoded, err := o.Snapshot.Decode(process.Final)
	if err != nil {
		return out, err
	}
	out.Result = &decoded
	return out, nil
}

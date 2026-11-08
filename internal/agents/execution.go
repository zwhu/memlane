package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/agentbridge"
)

type ToolCall struct {
	CallID    string             `json:"call_id,omitempty"`
	Tool      string             `json:"tool"`
	Arguments any                `json:"arguments,omitempty"`
	Response  agentbridge.Result `json:"response"`
}
type Execution struct {
	Channel          string           `json:"channel,omitempty"`
	SelectedModel    string           `json:"selected_model,omitempty"`
	PID              int              `json:"pid,omitempty"`
	ProcessGroupGone bool             `json:"process_group_gone"`
	Inspection       json.RawMessage  `json:"inspection,omitempty"`
	FinalText        string           `json:"final_text,omitempty"`
	ItemTypes        map[string]int   `json:"item_types,omitempty"`
	ToolEvents       []map[string]any `json:"memory_tool_events,omitempty"`
	Provider         string           `json:"provider,omitempty"`
	Model            string           `json:"model,omitempty"`
	Agent            string           `json:"agent"`
	Version          string           `json:"version"`
	ExitCode         int              `json:"exit_code"`
	Calls            []ToolCall       `json:"calls"`
	Failure          string           `json:"failure,omitempty"`
	Events           map[string]int   `json:"event_types,omitempty"`
}
type RunOptions struct {
	InspectInput    bool   // no-model context check; debug launch semantics differ from exec
	Initialization  bool   // only approved source-query/status and structured import tools
	UsageChannel    string // explicit daily replay acceptance; default is connection verification
	Workspace       string // explicit trusted session root for isolated verification
	Prompt          string
	Provider        string
	Model           string
	CodexAuthHome   string
	TraeAuthHome    string
	PiAuthDirectory string
}

type diagnosticOutput struct{ text strings.Builder }

func (b *diagnosticOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remain := 4096 - b.text.Len(); remain > 0 {
		b.text.Write(p[:min(remain, n)])
	}
	return n, nil
}
func safeReason(message string) string {
	message = regexp.MustCompile(`(?i)(Bearer\s+[^\s]+|sk-[a-zA-Z0-9_-]+|api[_-]?key[=:]\s*[^\s]+)`).ReplaceAllString(message, "[redacted]")
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}

func wrapped(value any) (agentbridge.Result, bool) {
	switch v := value.(type) {
	case map[string]any:
		if v["protocol_version"] == float64(1) {
			if _, ok := v["ok"].(bool); ok {
				data, _ := json.Marshal(v)
				var r agentbridge.Result
				if json.Unmarshal(data, &r) == nil {
					return r, true
				}
			}
		}
		for _, key := range []string{"result", "structuredContent", "structured_content", "details", "content", "text", "output"} {
			if r, ok := wrapped(v[key]); ok {
				return r, true
			}
		}
	case []any:
		for _, item := range v {
			if r, ok := wrapped(item); ok {
				return r, true
			}
		}
	case string:
		var nested any
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(v, "Error: "))), &nested) == nil {
			return wrapped(nested)
		}
	}
	return agentbridge.Result{}, false
}

// Run invokes the actual installed Agent using isolated settings. The Agent
// itself reads stored login through an auth-file link; we never extract tokens.
// Only actual memory tool events with structured receipts are kept as evidence.
func Run(ctx context.Context, d Discovery, i Installation, o RunOptions) (Execution, error) {
	out := Execution{Agent: d.Agent, Version: d.Version, Channel: d.Channel, Calls: []ToolCall{}, Events: map[string]int{}, ItemTypes: map[string]int{}}
	if d.Path == "" || d.Error != "" || !filepath.IsAbs(d.Path) {
		return out, errors.New("Agent executable unavailable")
	}
	workspace := i.Options.Workspace
	if o.Workspace != "" {
		workspace = o.Workspace
	}
	if !filepath.IsAbs(workspace) {
		return out, errors.New("select a workspace before verifying Agent")
	}
	isolated, err := os.MkdirTemp("", "memlane-agent-run-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(isolated)
	if err = os.Chmod(isolated, 0700); err != nil {
		return out, err
	}
	channel := o.UsageChannel
	if channel == "" {
		channel = "verification"
	}
	if channel != "daily" && channel != "verification" {
		return out, errors.New("invalid usage channel")
	}
	env := append(os.Environ(), "PATH="+filepath.Dir(d.Path)+":"+os.Getenv("PATH"), "PI_TELEMETRY=0", "MEMLANE_USAGE_CHANNEL="+channel)
	mode := ""
	if o.Initialization {
		mode = "initialization"
	}
	env = append(env, "MEMLANE_TOOL_MODE="+mode)
	var arguments []string
	if projectMCPAgent(d.Agent) {
		authHome := o.CodexAuthHome
		if authHome == "" {
			authHome = os.Getenv("CODEX_HOME")
			if authHome == "" {
				home, _ := os.UserHomeDir()
				authHome = filepath.Join(home, ".codex")
			}
		}
		authTarget := isolated
		if d.Agent == "traex" {
			authHome = o.TraeAuthHome
			if authHome == "" {
				authHome = os.Getenv("TRAECLI_HOME")
			}
			if authHome == "" {
				shared := os.Getenv("TRAE_HOME")
				if shared == "" {
					home, _ := os.UserHomeDir()
					shared = filepath.Join(home, ".trae")
				}
				authHome = filepath.Join(shared, "cli")
			}
			authTarget = filepath.Join(isolated, "cli")
			if err = os.Mkdir(authTarget, 0700); err != nil {
				return out, err
			}
			if o.Model == "" {
				selected, e := selectedTraexModel(workspace)
				if e != nil {
					return out, e
				}
				o.Model = selected
			}
		}
		if info, e := os.Stat(filepath.Join(authHome, "auth.json")); e == nil && !info.IsDir() {
			if err = os.Symlink(filepath.Join(authHome, "auth.json"), filepath.Join(authTarget, "auth.json")); err != nil {
				return out, err
			}
		}
		if d.Agent == "traex" {
			env = []string{}
			for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
				if v, ok := os.LookupEnv(key); ok {
					env = append(env, key+"="+v)
				}
			}
			env = append(env, "TRAE_HOME="+isolated, "TRAECLI_HOME="+authTarget, "MEMLANE_USAGE_CHANNEL="+channel, "MEMLANE_TOOL_MODE="+mode)
		} else {
			env = append(env, "CODEX_HOME="+isolated)
		}
		command, _ := json.Marshal(i.Options.Bridge)
		argv, _ := json.Marshal([]string{"mcp", "--root", i.Options.ServiceRoot, "--connection", i.Connection.ID, "--generation", fmt.Sprint(i.Connection.Generation), "--workspace", workspace, "--usage-channel", channel, "--tool-mode", mode})
		arguments = []string{"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--json", "--sandbox", "read-only", "--cd", workspace, "-c", `approval_policy="never"`, "-c", "mcp_servers.memlane.command=" + string(command), "-c", "mcp_servers.memlane.args=" + string(argv), "-c", "features.shell_tool=false"}
		arguments = append(arguments, "-c", `mcp_servers.memlane.tools.memory_save.approval_mode="approve"`)
		if o.Initialization {
			arguments = append(arguments, "-c", `mcp_servers.memlane.tools.memory_initialize.approval_mode="approve"`)
		}
		if d.Agent == "traex" {
			arguments = append(arguments, "--strict-config", "-c", "project_doc_max_bytes=0", "-c", `web_search="disabled"`, "-c", `model_provider="trae"`, "-c", "resource_dirs=[]", "-c", "skills.include_instructions=false", "-c", "skills.bundled.enabled=false", "-c", "memories.generate_memories=false", "-c", "memories.use_memories=false", "--enable", "code_mode_host", "--enable", "skip_host_skill_discovery")
			for _, flag := range []string{"shell_tool", "unified_exec", "apps", "plugins", "hooks", "memories", "multi_agent", "multi_agent_v2", "browser_use", "browser_use_external", "computer_use", "image_generation", "skill_search", "skill_mcp_dependency_install", "workspace_dependencies", "goals", "memory_self_evolution", "skill_evolution", "team_memory", "daemon_auto_start", "prevent_idle_sleep"} {
				arguments = append(arguments, "--disable", flag)
			}
			if o.Model != "" {
				key, _ := json.Marshal(o.Model)
				arguments = append(arguments, "-c", "model_tool_modes={"+string(key)+`="code_mode_only"}`)
			}
		}
		if o.Model != "" {
			arguments = append(arguments, "--model", o.Model)
		}
		arguments = append(arguments, o.Prompt)
	} else if d.Agent == "pi" {
		authDir := o.PiAuthDirectory
		if authDir == "" {
			authDir = i.Options.PiDirectory
		}
		if info, e := os.Stat(filepath.Join(authDir, "auth.json")); e == nil && !info.IsDir() {
			if err = os.Symlink(filepath.Join(authDir, "auth.json"), filepath.Join(isolated, "auth.json")); err != nil {
				return out, err
			}
		}
		env = append(env, "PI_CODING_AGENT_DIR="+isolated)
		arguments = []string{"--mode", "json", "--print", "--no-session", "--offline", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-builtin-tools", "--extension", i.ExtensionPath, "--tools", "memory_scopes,memory_search,memory_get,memory_save,memory_status,memory_adopt,memory_initialization_get,memory_initialization_status,memory_initialize"}
		if o.Initialization {
			for n := range arguments {
				if n > 0 && arguments[n-1] == "--tools" {
					arguments[n] = "memory_initialization_get,memory_initialization_status,memory_initialize"
				}
			}
		}
		if o.Provider != "" {
			arguments = append(arguments, "--provider", o.Provider)
		}
		if o.Model != "" {
			arguments = append(arguments, "--model", o.Model)
		}
		arguments = append(arguments, o.Prompt)
	} else {
		return out, errors.New("unsupported Agent")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.Path, arguments...)
	cmd.Dir = workspace
	cmd.Env = env
	out.SelectedModel = o.Model
	if o.InspectInput {
		if d.Agent != "traex" {
			return out, errors.New("prompt inspection is only available for TRAE CLI")
		}
		debug := []string{"debug", "prompt-input", "-c", `sandbox_mode="read-only"`}
		for n := 1; n < len(arguments); n++ {
			switch arguments[n] {
			case "-c", "--enable", "--disable":
				debug = append(debug, arguments[n], arguments[n+1])
				n++
			case "--model":
				key, _ := json.Marshal(arguments[n+1])
				debug = append(debug, "-c", "model="+string(key))
				n++
			}
		}
		debug = append(debug, o.Prompt)
		inspect := exec.CommandContext(ctx, d.Path, debug...)
		inspect.Dir = workspace
		inspect.Env = env
		b, e := inspect.Output()
		if len(b) > 2*1024*1024 {
			return out, errors.New("prompt inspection too large")
		}
		out.Inspection = json.RawMessage(b)
		return out, e
	}
	configureAgentProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, err
	}
	var diagnostic diagnosticOutput
	cmd.Stderr = &diagnostic
	if err = cmd.Start(); err != nil {
		return out, errors.New("Agent process could not start")
	}
	out.PID = cmd.Process.Pid
	defer finishAgentProcess(cmd)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	type piStart struct {
		name string
		args any
	}
	piCalls := map[string]piStart{}
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		type_, _ := event["type"].(string)
		out.Events[type_]++
		if d.Agent == "pi" && type_ == "tool_execution_start" {
			id, _ := event["toolCallId"].(string)
			name, _ := event["toolName"].(string)
			if id != "" && strings.HasPrefix(name, "memory_") && len(piCalls) < 128 {
				piCalls[id] = piStart{name: name, args: event["args"]}
			}
		}
		if message, ok := event["message"].(map[string]any); ok {
			if provider, ok := message["provider"].(string); ok {
				out.Provider = provider
			}
			if model, ok := message["model"].(string); ok {
				out.Model = model
			}
		}
		var item map[string]any
		if projectMCPAgent(d.Agent) && type_ == "item.completed" {
			item, _ = event["item"].(map[string]any)
			kind, _ := item["type"].(string)
			out.ItemTypes[kind]++
			if kind == "agent_message" {
				if message, ok := item["text"].(string); ok {
					out.FinalText = safeReason(message)
				}
			}
			if strings.Contains(kind, "mcp") {
				out.ToolEvents = append(out.ToolEvents, item)
				if failure, ok := item["error"].(map[string]any); ok {
					if message, ok := failure["message"].(string); ok {
						out.Failure = safeReason(message)
					}
				}
			}
			if item["type"] != "mcp_tool_call" {
				continue
			}
		} else if d.Agent == "pi" && type_ == "tool_execution_end" {
			item = event
		} else {
			if type_ == "turn.failed" || type_ == "error" {
				message, _ := event["message"].(string)
				if terminal, ok := event["error"].(map[string]any); ok {
					if text, ok := terminal["message"].(string); ok {
						message = text
					}
				}
				// Keep the actual terminal reason, never authorization headers or
				// common token/key strings. No credentials are inspected directly.
				message = safeReason(message)
				if message != "" {
					out.Failure = message
				} else {
					out.Failure = "Agent run failed; check model authentication and network"
				}
			}
			continue
		}
		name, _ := item["tool"].(string)
		if name == "" {
			name, _ = item["toolName"].(string)
		}
		if !strings.HasPrefix(name, "memory_") {
			continue
		}
		if response, ok := wrapped(item); ok {
			callID, _ := item["toolCallId"].(string)
			args := item["arguments"]
			if args == nil {
				args = item["args"]
			}
			if d.Agent == "pi" {
				if started, exists := piCalls[callID]; exists && started.name == name && args == nil {
					args = started.args
				}
				delete(piCalls, callID)
			}
			out.Calls = append(out.Calls, ToolCall{CallID: callID, Tool: name, Arguments: args, Response: response})
		}
	}
	scanErr := scanner.Err()
	err = cmd.Wait()
	out.ProcessGroupGone = finishAgentProcess(cmd)
	out.ExitCode = cmd.ProcessState.ExitCode()
	if ctx.Err() != nil {
		out.Failure = "Agent verification timed out or was cancelled"
		return out, ctx.Err()
	}
	if scanErr != nil {
		return out, scanErr
	}
	if err != nil {
		if out.Failure == "" {
			out.Failure = safeReason(strings.TrimSpace(diagnostic.text.String()))
			if out.Failure == "" {
				out.Failure = "Agent exited without a successful verification; check authentication/model/network"
			}
		}
		return out, errors.New(out.Failure)
	}
	if !out.ProcessGroupGone {
		return out, errors.New("Agent process group did not exit")
	}
	if len(out.Calls) == 0 {
		if diagnostic.text.Len() > 0 {
			out.Failure = safeReason(strings.TrimSpace(diagnostic.text.String()))
		}
		return out, errors.New("Agent did not invoke a Memlane memory tool: " + out.Failure + " " + out.FinalText)
	}
	return out, nil
}

//go:build darwin || linux

package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

type P3RunOptions struct {
	Agent      string // codex (default) or traex; experimental adapters only
	Model      string // preserve the selected traex model without loading user config
	Executable string
	AuthHome   string
	Snapshot   records.P3Snapshot
	// Called with event type only; no reasoning or private model trace is saved.
	OnEvent func(string)
}

type P3Execution struct {
	Proposal   *records.P3Proposal `json:"proposal,omitempty"`
	Events     map[string]int      `json:"events"`
	Items      map[string]int      `json:"items"`
	Terminal   bool                `json:"terminal_completed"`
	Started    bool                `json:"process_started"`
	Reaped     bool                `json:"process_reaped"`
	GroupGone  bool                `json:"process_group_gone"`
	PID        int                 `json:"pid"`
	ExitCode   int                 `json:"exit_code"`
	Cancelled  bool                `json:"cancelled"`
	Failure    string              `json:"failure,omitempty"`
	ItemErrors []string            `json:"item_errors,omitempty"`
}

// RunP3Experiment runs the installed Codex or TRAE CLI exec protocol. There is no
// memory tool, database handle, user-config edit or resume operation here.
func RunP3Experiment(ctx context.Context, o P3RunOptions) (P3Execution, error) {
	x := o.Snapshot
	agent := o.Agent
	if agent == "" {
		agent = "codex"
	}
	if agent != "codex" && agent != "traex" {
		return P3Execution{}, errors.New("unsupported experimental Agent")
	}
	check := x
	if check.Seal() != nil || check.Digest != x.Digest || !filepath.IsAbs(o.Executable) {
		return P3Execution{}, records.ErrP3Invalid
	}
	root, err := os.MkdirTemp("", "memlane-p3-run-")
	if err != nil {
		return P3Execution{}, err
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "agent-home")
	work := filepath.Join(root, "task")
	for _, dir := range []string{config, work} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return P3Execution{}, err
		}
	}
	auth := o.AuthHome
	if auth == "" {
		if agent == "traex" {
			auth = os.Getenv("TRAECLI_HOME")
		} else {
			auth = os.Getenv("CODEX_HOME")
		}
		if auth == "" {
			home, _ := os.UserHomeDir()
			if agent == "traex" {
				shared := os.Getenv("TRAE_HOME")
				if shared == "" {
					shared = filepath.Join(home, ".trae")
				}
				auth = filepath.Join(shared, "cli")
			} else {
				auth = filepath.Join(home, ".codex")
			}
		}
	}
	private := config
	if agent == "traex" {
		private = filepath.Join(config, "cli")
		if err = os.Mkdir(private, 0700); err != nil {
			return P3Execution{}, err
		}
	}
	if _, err = os.Stat(filepath.Join(auth, "auth.json")); err == nil {
		if err = os.Symlink(filepath.Join(auth, "auth.json"), filepath.Join(private, "auth.json")); err != nil {
			return P3Execution{}, err
		}
	}
	schema := filepath.Join(root, "output-schema.json")
	if err = os.WriteFile(schema, x.OutputSchema(), 0600); err != nil {
		return P3Execution{}, err
	}
	args := []string{"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--cd", work, "--output-schema", schema, "-c", `approval_policy="never"`, "-c", "project_doc_max_bytes=0", "-c", `web_search="disabled"`}
	args = append(args, "--strict-config", "-c", "skills.include_instructions=false", "-c", "skills.bundled.enabled=false")
	for _, flag := range []string{"shell_tool", "unified_exec", "apps", "plugins", "hooks", "memories", "multi_agent", "multi_agent_v2", "browser_use", "browser_use_external", "computer_use", "image_generation", "skill_search", "skill_mcp_dependency_install", "workspace_dependencies"} {
		args = append(args, "--disable", flag)
	}
	if agent == "codex" {
		args = append(args, "--disable", "code_mode_host", "--disable", "view_image", "--disable", "remote_plugin")
	} else {
		args = append(args, "-c", `model_provider="trae"`, "-c", "resource_dirs=[]", "-c", "memories.generate_memories=false", "-c", "memories.use_memories=false", "--enable", "code_mode_host", "--enable", "skip_host_skill_discovery", "--disable", "goals", "--disable", "memory_self_evolution", "--disable", "skill_evolution", "--disable", "team_memory", "--disable", "daemon_auto_start", "--disable", "prevent_idle_sleep", "--disallowed-tool", "*", "--disallowed-tool", "exec")
		if o.Model != "" {
			args = append(args, "--model", o.Model)
			modelKey, _ := json.Marshal(o.Model)
			args = append(args, "-c", "model_tool_modes={"+string(modelKey)+`="code_mode_only"}`)
		}
	}
	// Global .agents skills may live outside CODEX_HOME. Disable known entries
	// by path without reading their contents; no installed skills are loaded.
	home, _ := os.UserHomeDir()
	entries, _ := os.ReadDir(filepath.Join(home, ".agents", "skills"))
	var skillConfig []map[string]any
	for _, e := range entries {
		skillConfig = append(skillConfig, map[string]any{"path": filepath.Join(home, ".agents", "skills", e.Name(), "SKILL.md"), "enabled": false})
	}
	if len(skillConfig) > 0 {
		// TOML inline tables (paths encoded with JSON string escaping).
		var values []string
		for _, v := range skillConfig {
			b, _ := json.Marshal(v["path"])
			values = append(values, "{path="+string(b)+",enabled=false}")
		}
		args = append(args, "-c", "skills.config=["+strings.Join(values, ",")+"]")
	}
	args = append(args, "-")
	b, _ := json.Marshal(x)
	prompt := p3ProposalPrompt + "\n宿主指定 proposal_id=" + x.ProposalID() + "\n冻结输入快照：\n" + string(b)
	env := []string{}
	// Do not forward production service roots, model credentials or tool config.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	if agent == "traex" {
		env = append(env, "TRAE_HOME="+config, "TRAECLI_HOME="+private)
	} else {
		env = append(env, "CODEX_HOME="+config)
	}
	return runP3Process(ctx, o.Executable, args, env, work, prompt, x, o.OnEvent)
}

// runP3Process is shared by the real run and explicitly labelled protocol
// fixtures. A successful exit alone never releases a proposal.
func runP3Process(ctx context.Context, executable string, args, env []string, dir, prompt string, x records.P3Snapshot, onEvent func(string)) (P3Execution, error) {
	out := P3Execution{Events: map[string]int{}, Items: map[string]int{}, ExitCode: -1}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, err
	}
	var diagnostic diagnosticOutput
	cmd.Stderr = &diagnostic
	if err = cmd.Start(); err != nil {
		return out, err
	}
	out.Started = true
	out.PID = cmd.Process.Pid
	// Keep draining via Scanner, bounded per event. Cancellation kills the group
	// even while a grandchild holds stdout; WaitDelay also bounds inherited pipes.
	stream := &io.LimitedReader{R: stdout, N: 8*1024*1024 + 1}
	scan := bufio.NewScanner(stream)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	var final string
	malformed, tool := false, false
	for scan.Scan() {
		var event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   *struct {
				Message string `json:"message"`
			} `json:"error"`
			Item *struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Message string `json:"message"`
			} `json:"item"`
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil || event.Type == "" {
			malformed = true
			continue
		}
		out.Events[event.Type]++
		switch event.Type {
		case "thread.started":
			if out.Events["turn.started"] > 0 {
				malformed = true
			}
		case "turn.started":
			if out.Events["thread.started"] != 1 {
				malformed = true
			}
		case "item.started", "item.updated", "item.completed":
			if out.Events["turn.started"] != 1 || out.Terminal {
				malformed = true
			}
		case "turn.completed":
			if out.Items["agent_message"] != 1 {
				malformed = true
			}
		case "turn.failed", "error":
		default:
			malformed = true
		}
		if event.Item != nil {
			if event.Item.Type == "error" {
				message := event.Item.Message
				if message == "" {
					message = event.Item.Text
				}
				out.ItemErrors = append(out.ItemErrors, safeReason(message))
				if message != "" {
					out.Failure = safeReason(message)
				}
			}
			if event.Type == "item.completed" {
				out.Items[event.Item.Type]++
			}
			if event.Item.Type != "agent_message" && event.Item.Type != "reasoning" {
				tool = true
			}
			if event.Type == "item.completed" && event.Item.Type == "agent_message" {
				final = event.Item.Text
			}
		}
		if event.Type == "turn.completed" {
			out.Terminal = true
		}
		if event.Type == "turn.failed" || event.Type == "error" {
			out.Failure = safeReason(event.Message)
			if event.Error != nil {
				out.Failure = safeReason(event.Error.Message)
			}
		}
		if onEvent != nil {
			onEvent(event.Type)
		}
	}
	if stream.N == 0 {
		malformed = true
	}
	// A protocol error or oversized stream must also close the process tree.
	if scan.Err() != nil || malformed {
		_ = syscall.Kill(-out.PID, syscall.SIGKILL)
	}
	err = cmd.Wait()
	out.Reaped = cmd.ProcessState != nil
	if out.Reaped {
		out.ExitCode = cmd.ProcessState.ExitCode()
	}
	// Codex wrappers can outlive their leader; always close remaining group.
	_ = syscall.Kill(-out.PID, syscall.SIGKILL)
	// Reparented children can take a moment to be reaped by the OS. Bound the
	// observation; a surviving process group is always a failed cancellation.
	deadline := time.Now().Add(time.Second)
	for {
		out.GroupGone = syscall.Kill(-out.PID, 0) == syscall.ESRCH
		if out.GroupGone || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		out.Cancelled = true
		out.Failure = ctx.Err().Error()
		return out, ctx.Err()
	}
	if err != nil || scan.Err() != nil || malformed || tool || !out.GroupGone || !out.Terminal || out.Events["turn.completed"] != 1 || out.Events["turn.started"] != 1 || out.Events["thread.started"] != 1 || out.Events["turn.failed"] > 0 || out.Events["error"] > 0 || out.Items["agent_message"] != 1 {
		if out.Failure == "" {
			out.Failure = safeReason(strings.TrimSpace(diagnostic.text.String()))
			if out.Failure == "" {
				out.Failure = "invalid terminal/process/tool protocol"
			}
		}
		return out, errors.New(out.Failure)
	}
	p, err := x.Decode([]byte(final))
	if err != nil {
		out.Failure = err.Error()
		return out, err
	}
	out.Proposal = &p
	return out, nil
}

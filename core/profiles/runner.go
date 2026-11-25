package profiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zwhu/memlane/kit/agents"
)

const outputLimit = 4 * MaxDocumentBytes
const pipeGrace = 5 * time.Second

type CLIRunner struct {
	Discovery agents.DiscoveryOptions
}

type commandSpec struct {
	args       []string
	env        map[string]string
	outputFile string
}

func isolatedEnv(environ []string) map[string]string {
	env := make(map[string]string)
	prefixes := []string{"COGNEE_", "MEMLANE_PROJECT_", "LLM_", "OPENAI_", "ANTHROPIC_", "GEMINI_", "OLLAMA_", "FALLBACK_"}
	for _, entry := range environ {
		key, value, _ := strings.Cut(entry, "=")
		upperKey := strings.ToUpper(key)
		blocked := false
		for _, prefix := range prefixes {
			blocked = blocked || strings.HasPrefix(upperKey, prefix)
		}
		if !blocked {
			env[key] = value
		}
	}
	env["MEMLANE_PROCESSING_SESSION"] = "1"
	return env
}

func (r CLIRunner) command(req RunRequest, work string) (commandSpec, error) {
	host := agents.ResolveHostRuntime(req.Source, r.Discovery)
	if host.Executable == "" || host.ProfileDir == "" {
		return commandSpec{}, ErrImportUnavailable
	}
	spec := commandSpec{args: []string{host.Executable}, env: isolatedEnv(os.Environ())}
	prompt := req.Prompt
	if req.Source == "claude_code" {
		spec.env["CLAUDE_CONFIG_DIR"] = host.ProfileDir
		delete(spec.env, "CLAUDECODE")
		spec.args = append(spec.args, "--print", "--no-session-persistence", "--output-format", "json",
			"--permission-mode", "dontAsk", "--tools", "Read,Glob,Grep", "--strict-mcp-config",
			"--mcp-config", `{"mcpServers":{}}`, "--settings", `{"disableAllHooks":true}`, "--", prompt)
		return spec, nil
	}
	defaults, err := agents.ReadModelDefaults(req.Source, host.ProfileDir, agents.ModelDefaultsOptions{Environ: os.Getenv})
	if err != nil {
		return commandSpec{}, errors.Join(ErrImportUnavailable, err)
	}
	switch req.Source {
	case "codex", "traex":
		configName := "config.toml"
		if req.Source == "codex" {
			spec.env["CODEX_HOME"] = host.ProfileDir
		} else {
			delete(spec.env, "CODEX_HOME")
			spec.env["TRAE_HOME"] = host.ProfileDir
			spec.env["TRAECLI_HOME"] = filepath.Join(host.ProfileDir, "cli")
			configName = "traecli.toml"
		}
		spec.outputFile = filepath.Join(work, "answer.md")
		spec.args = append(spec.args, "exec", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check",
			"-s", "read-only", "-c", "features.hooks=false", "-c", "features.plugins=false",
			"-c", "memories.generate_memories=false", "-o", spec.outputFile)
		for _, pair := range [][2]string{{"model", defaults.Model}, {"model_provider", defaults.Provider},
			{"memories.extract_model", defaults.Model}, {"memories.consolidation_model", defaults.Model},
			{"model_reasoning_effort", defaults.ReasoningEffort}, {"service_tier", defaults.ServiceTier}} {
			if pair[1] != "" {
				value, _ := json.Marshal(pair[1])
				spec.args = append(spec.args, "-c", pair[0]+"="+string(value))
			}
		}
		spec.args = append(spec.args, memoryFlags(filepath.Join(host.ProfileDir, configName))...)
		spec.args = append(spec.args, "--", prompt)
	case "pi":
		spec.env["PI_CODING_AGENT_DIR"] = host.ProfileDir
		spec.args = append(spec.args, "--print", "--no-session", "--no-extensions", "--no-skills",
			"--provider", defaults.Provider, "--model", defaults.Model, "--tools", "read,grep,find,ls", prompt)
	case "antigravity":
		parent := filepath.Dir(host.ProfileDir)
		if filepath.Base(host.ProfileDir) != "config" || filepath.Base(parent) != ".gemini" {
			return commandSpec{}, ErrImportUnavailable
		}
		spec.env["HOME"] = filepath.Dir(parent)
		if err := isolateAntigravity(work); err != nil {
			return commandSpec{}, err
		}
		spec.args = append(spec.args, "--output-format", "json", "--mode", "plan", "--model", defaults.Model,
			"--disable-slash-commands", "--sandbox", "--print="+prompt)
	default:
		return commandSpec{}, ErrImportUnavailable
	}
	return spec, nil
}

func memoryFlags(path string) []string {
	raw, err := agents.SafeReadFile(path, 1024*1024)
	if err != nil {
		return nil
	}
	doc, err := agents.ParseTOML(raw)
	if err != nil {
		return nil
	}
	profiles, _ := doc["profiles"].(map[string]any)
	name, _ := doc["profile"].(string)
	selected, _ := profiles[name].(map[string]any)
	var flags []string
	for _, field := range [][2]string{{"features", "memories"}, {"memories", "use_memories"}} {
		base, _ := doc[field[0]].(map[string]any)
		override, _ := selected[field[0]].(map[string]any)
		value := base[field[1]]
		if v, ok := override[field[1]]; ok {
			value = v
		}
		if enabled, ok := value.(bool); ok {
			flags = append(flags, "-c", field[0]+"."+field[1]+"="+strconv.FormatBool(enabled))
		}
	}
	return flags
}

func isolateAntigravity(work string) error {
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(work, ".agents"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte("Export from your own stored context. Do not use tools, run commands, or read external files.\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(work, ".agents", "hooks.json"), []byte(`{"memlane-profile-export":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"printf '%s\\n' '{\"decision\":\"deny\",\"reason\":\"Tools disabled during profile export\"}'","timeout":5}]}]}}`), 0o600)
}

func (r CLIRunner) Run(ctx context.Context, req RunRequest, report func(Progress)) (string, error) {
	work, err := os.MkdirTemp("", "memlane-profile-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	spec, err := r.command(req, work)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(spec.args[0], spec.args[1:]...)
	cmd.Dir = work
	for key, value := range spec.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdout, stderr, err := runProcess(ctx, cmd, req.IdleThreshold, pipeGrace, report)
	if err != nil {
		return "", err
	}
	if spec.outputFile != "" {
		stdout, err = agents.SafeReadFile(spec.outputFile, MaxDocumentBytes)
		if err != nil {
			return "", errors.Join(ErrInvalidDocument, err)
		}
	}
	return exportBody(req.Source, stdout, stderr)
}

func exportBody(source string, stdout, stderr []byte) (string, error) {
	switch source {
	case "claude_code", "antigravity":
		var result struct {
			IsError                                  bool `json:"is_error"`
			Subtype, Result, Status, Response, Error string
		}
		if !json.Valid(stdout) || json.Unmarshal(stdout, &result) != nil {
			return "", agentFailure(nil, stderr)
		}
		if source == "claude_code" && !result.IsError && result.Subtype == "success" {
			return result.Result, nil
		}
		if source == "antigravity" && strings.EqualFold(result.Status, "success") && result.Error == "" {
			return result.Response, nil
		}
		if result.Error != "" && len(stderr) == 0 {
			stderr = []byte(result.Error)
		}
		return "", agentFailure(nil, stderr)
	default:
		return string(stdout), nil
	}
}

type capturedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *capturedOutput) drain(reader io.Reader, signal func()) error {
	chunk := make([]byte, 32*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			size := min(n, outputLimit-b.Len())
			b.Write(chunk[:size])
			b.overflow = b.overflow || size < n
			signal()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func runProcess(ctx context.Context, cmd *exec.Cmd, idle, grace time.Duration, report func(Progress)) ([]byte, []byte, error) {
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	defer outRead.Close()
	defer outWrite.Close()
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	defer errRead.Close()
	defer errWrite.Close()
	cmd.Stdout, cmd.Stderr = outWrite, errWrite
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, nil, agentFailure(nil, []byte(err.Error()))
	}
	outWrite.Close()
	errWrite.Close()

	var last atomic.Int64
	activity := make(chan struct{}, 1)
	signal := func() {
		last.Store(time.Now().UnixNano())
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	var stdout, stderr capturedOutput
	var outErr, stderrErr error
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); outErr = stdout.drain(outRead, signal) }()
	go func() { defer readers.Done(); stderrErr = stderr.drain(errRead, signal) }()
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	stopReading := func() {
		outRead.Close()
		errRead.Close()
		<-drained
	}
	if idle <= 0 {
		idle = 5 * time.Second
	}
	ticker := time.NewTicker(idle)
	defer ticker.Stop()
	var reported int64
	var waitErr error
	publish := func(state string) {
		now := time.Now()
		ns := last.Load()
		p := Progress{State: state, ElapsedSeconds: now.Sub(start).Seconds(), IdleSeconds: now.Sub(start).Seconds()}
		if ns != 0 {
			p.LastActivityAt = float64(ns) / float64(time.Second)
			p.IdleSeconds = max(0, now.Sub(time.Unix(0, ns)).Seconds())
		}
		if state == "exited" {
			p.ExitCode = exitCode(waitErr)
		}
		reported = ns
		report(p)
	}
	publish("waiting")
running:
	for {
		select {
		case <-ctx.Done():
			kill()
			<-exited
			stopReading()
			return nil, nil, ctx.Err()
		case <-activity:
			if last.Load() != reported {
				publish("activity")
			}
		case <-ticker.C:
			ns := last.Load()
			if ns == 0 || time.Since(time.Unix(0, ns)) >= idle {
				publish("waiting")
			}
		case waitErr = <-exited:
			break running
		}
	}
	if last.Load() != reported {
		publish("activity")
	}
	publish("exited")
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		kill()
		stopReading()
		return nil, nil, ctx.Err()
	case <-timer.C:
		kill()
		stopReading()
		return nil, nil, &AgentFailure{Reason: ErrOutputPipeOpen.Error(), ExitCode: exitCode(waitErr)}
	case <-drained:
	}
	if waitErr != nil {
		return nil, nil, agentFailure(exitCode(waitErr), stderr.Bytes())
	}
	if stdout.overflow || outErr != nil || stderrErr != nil {
		return nil, nil, ErrInvalidDocument
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func exitCode(err error) *int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		n := exit.ExitCode()
		return &n
	}
	if err == nil {
		n := 0
		return &n
	}
	return nil
}

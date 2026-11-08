package official

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type AgentScope struct {
	Project       string `json:"project"`
	Workspace     string `json:"workspace"`
	Executable    string `json:"executable"`
	AuthDirectory string `json:"auth_directory"`
	DatasetName   string `json:"dataset_name"`
	Version       string `json:"version"`
	Model         string `json:"model"`
	Home          string `json:"home"`
	Plugin        string `json:"plugin"`
}
type AgentResult struct {
	Session  string `json:"session"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func (c *Controller) agentRoot(project string) (string, error) {
	id, err := uuid.Parse(project)
	if err != nil || id.String() != project {
		return "", errors.New("approved project UUID required")
	}
	return filepath.Join(c.Root, "agents", project), nil
}
func (c *Controller) projectToken(ctx context.Context, endpoint, project string) (string, error) {
	root, err := c.agentRoot(project)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(root, "principal.json")
	var principal struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	err = privateJSON(path, &principal)
	if os.IsNotExist(err) {
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return "", err
		}
		principal.Email = "memlane-project-" + project + "@example.com"
		principal.Password = hex.EncodeToString(random)
		body, _ := json.Marshal(map[string]string{"email": principal.Email, "password": principal.Password})
		if err = Request(ctx, endpoint, "/api/v1/auth/register", "POST", "", "application/json", body, nil); err != nil {
			return "", err
		}
		if err = serviceipc.WriteJSON(path, principal); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	var auth struct {
		Token string `json:"access_token"`
	}
	form := url.Values{"username": {principal.Email}, "password": {principal.Password}}
	if err = Request(ctx, endpoint, "/api/v1/auth/login", "POST", "", "application/x-www-form-urlencoded", []byte(form.Encode()), &auth); err != nil {
		return "", err
	}
	// API key minting is a real official write and part of approved setup.
	// The reusable key never enters the App state, log or Memlane database.
	tokenPath := filepath.Join(root, "cognee-access.token")
	if data, e := os.ReadFile(tokenPath); e == nil {
		token := strings.TrimSpace(string(data))
		var identity map[string]any
		if e = Request(ctx, endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &identity); e == nil {
			return token, nil
		}
	}
	var key struct {
		Key string `json:"key"`
	}
	if err = Request(ctx, endpoint, "/api/v1/auth/api-keys", "POST", auth.Token, "application/json", []byte(`{"name":"Memlane isolated project"}`), &key); err != nil {
		return "", err
	}
	if key.Key == "" {
		return "", errors.New("official key mint returned no key")
	}
	if err = os.WriteFile(tokenPath, []byte(key.Key), 0600); err != nil {
		return "", err
	}
	return key.Key, nil
}
func (c *Controller) PrepareAgent(ctx context.Context, d AgentScope) (AgentScope, error) {
	root, err := c.agentRoot(d.Project)
	if err != nil {
		return d, err
	}
	for _, path := range []string{d.Executable, d.AuthDirectory, d.Workspace} {
		if !filepath.IsAbs(path) {
			return d, errors.New("Agent executable, login directory and project must be explicit absolute paths")
		}
	}
	workspace, err := filepath.EvalSymlinks(d.Workspace)
	if err != nil {
		return d, err
	}
	top, err := exec.CommandContext(ctx, "/usr/bin/git", "-C", workspace, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return d, err
	}
	if strings.TrimSpace(string(top)) != workspace {
		return d, errors.New("approved workspace must be the actual Git project root")
	}
	version, err := exec.CommandContext(ctx, d.Executable, "--version").Output()
	if err != nil {
		return d, err
	}
	d.Version = strings.TrimSpace(string(version))
	if !strings.Contains(d.Version, "0.167.0-alpha.3.2") {
		return d, errors.New("当前官方插件路径仅实际核验 Codex 0.167.0-alpha.3.2；不会升级 Agent")
	}
	python, err := exec.CommandContext(ctx, "/usr/bin/python3", "-c", "import sys; assert sys.version_info >= (3, 9); print(sys.version.split()[0])").Output()
	if err != nil || len(python) == 0 {
		return d, errors.New("官方 hook 需要可用 Python 3.9 或以上；当前本机依赖未验证")
	}
	data, err := os.ReadFile(filepath.Join(d.AuthDirectory, "config.toml"))
	if err != nil {
		return d, err
	}
	if len(data) > 1024*1024 {
		return d, errors.New("Agent config exceeds bounded read")
	}
	var current map[string]any
	if err = toml.Unmarshal(data, &current); err != nil {
		return d, err
	}
	selected := map[string]any{"features": map[string]bool{"hooks": true}}
	for _, key := range []string{"model", "model_provider", "model_reasoning_effort", "model_providers"} {
		if value, ok := current[key]; ok {
			selected[key] = value
		}
	}
	model, _ := selected["model"].(string)
	if model == "" {
		return d, errors.New("Agent 当前模型未在该配置中明确；请提供当前登录与配置目录")
	}
	d.Model = model
	d.Home = filepath.Join(root, "home")
	d.DatasetName = "memlane_" + strings.ReplaceAll(d.Project, "-", "")
	codexHome := filepath.Join(d.Home, ".codex")
	if err = os.MkdirAll(codexHome, 0700); err != nil {
		return d, err
	}
	// Never copy session history, other plugins, MCP configuration or hooks.
	// Authentication is copied opaquely only while a task runs (below).
	configPath := filepath.Join(codexHome, "config.toml")
	if _, e := os.Stat(configPath); os.IsNotExist(e) {
		encoded, e := toml.Marshal(selected)
		if e != nil {
			return d, e
		}
		if e = os.WriteFile(configPath, encoded, 0600); e != nil {
			return d, e
		}
	}
	state, err := c.ReadState()
	if err != nil {
		return d, err
	}
	token, err := c.projectToken(ctx, state.Endpoint, d.Project)
	if err != nil {
		return d, err
	}
	envDir := filepath.Join(d.Home, ".cognee")
	if err = os.MkdirAll(envDir, 0700); err != nil {
		return d, err
	}
	variables := map[string]string{"COGNEE_BASE_URL": state.Endpoint, "COGNEE_API_KEY": token, "COGNEE_BACKEND": "cloud", "COGNEE_MANAGED_ENDPOINT": "true", "COGNEE_PLUGIN_DATASET": d.DatasetName, "COGNEE_SHARED_AGENT_MEMORY": "false", "COGNEE_PLUGIN_IDENTITY": "true"}
	var config strings.Builder
	for _, key := range []string{"COGNEE_BASE_URL", "COGNEE_API_KEY", "COGNEE_BACKEND", "COGNEE_MANAGED_ENDPOINT", "COGNEE_PLUGIN_DATASET", "COGNEE_SHARED_AGENT_MEMORY", "COGNEE_PLUGIN_IDENTITY"} {
		value, _ := json.Marshal(variables[key])
		fmt.Fprintf(&config, "%s=%s\n", key, value)
	}
	if err = os.WriteFile(filepath.Join(envDir, ".env"), []byte(config.String()), 0600); err != nil {
		return d, err
	}
	manager, feed, err := c.Manager(ctx)
	if err != nil {
		return d, err
	}
	var marketplace string
	for _, descriptor := range feed.Components {
		if descriptor.ID == "cognee-codex-plugin" {
			installed, e := manager.Inspect(ctx, descriptor)
			if e != nil {
				return d, e
			}
			marketplace = installed.Directory
		}
	}
	if marketplace == "" {
		return d, errors.New("官方插件组件尚未准备")
	}
	for _, arguments := range [][]string{{"plugin", "marketplace", "add", marketplace, "--json"}, {"plugin", "add", "cognee@cognee", "--json"}} {
		command := exec.CommandContext(ctx, d.Executable, arguments...)
		command.Dir = d.Home
		command.Env = agentEnvironment(d)
		output, e := command.Output()
		if e != nil {
			return d, fmt.Errorf("官方插件安装命令未通过：%w", e)
		}
		if arguments[1] == "add" {
			var installed struct {
				Version string `json:"version"`
				Path    string `json:"installedPath"`
			}
			if e = json.Unmarshal(output, &installed); e != nil {
				return d, e
			}
			if installed.Version != "1.7.7" {
				return d, errors.New("官方插件实际安装版本不匹配")
			}
			d.Plugin = installed.Path
		}
	}
	if err = serviceipc.WriteJSON(filepath.Join(root, "scope.json"), d); err != nil {
		return d, err
	}
	return d, nil
}
func agentEnvironment(d AgentScope) []string {
	return []string{"HOME=" + d.Home, "CODEX_HOME=" + filepath.Join(d.Home, ".codex"), "PATH=/usr/bin:/bin", "COGNEE_ENV_FILE=" + filepath.Join(d.Home, ".cognee/.env")}
}
func (c *Controller) LoadAgent(project string) (AgentScope, error) {
	var d AgentScope
	root, err := c.agentRoot(project)
	if err != nil {
		return d, err
	}
	err = privateJSON(filepath.Join(root, "scope.json"), &d)
	if err == nil && (d.Project != project || d.Home != filepath.Join(root, "home")) {
		err = errors.New("persisted Agent scope identity differs")
	}
	return d, err
}
func (c *Controller) Dataset(ctx context.Context, d AgentScope) (string, string, error) {
	state, err := c.ReadState()
	if err != nil {
		return "", "", err
	}
	token, err := c.projectToken(ctx, state.Endpoint, d.Project)
	if err != nil {
		return "", "", err
	}
	var datasets []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err = Request(ctx, state.Endpoint, "/api/v1/datasets", "GET", token, "", nil, &datasets); err != nil {
		return "", "", err
	}
	for _, dataset := range datasets {
		if dataset.Name == d.DatasetName {
			return dataset.ID, token, nil
		}
	}
	return "", "", errors.New("官方任务尚未创建此项目的真实 dataset")
}
func (c *Controller) RunAgent(ctx context.Context, d AgentScope, prompt string, notify func(string)) (AgentResult, error) {
	out := AgentResult{ExitCode: -1}
	if len(prompt) == 0 || len(prompt) > 65536 {
		return out, errors.New("explicit bounded task required")
	}
	scope, err := c.LoadAgent(d.Project)
	if err != nil {
		return out, err
	}
	if scope != d {
		return out, errors.New("approved Agent scope changed")
	}
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.tasks == nil {
		c.tasks = map[string]context.CancelFunc{}
	}
	if _, busy := c.tasks[d.Project]; busy {
		c.mu.Unlock()
		cancel()
		return out, errors.New("this project's official task is already running")
	}
	c.tasks[d.Project] = cancel
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); delete(c.tasks, d.Project); c.mu.Unlock() }()
	auth, err := os.ReadFile(filepath.Join(d.AuthDirectory, "auth.json"))
	if err != nil {
		return out, err
	}
	if len(auth) > 1024*1024 {
		return out, errors.New("opaque Agent login exceeds bound")
	}
	target := filepath.Join(d.Home, ".codex/auth.json")
	if err = os.WriteFile(target, auth, 0600); err != nil {
		return out, err
	}
	auth = nil
	defer os.Remove(target)
	// A fresh normal task. No resume, history, prewritten answer or model override.
	command := exec.Command(d.Executable, "--dangerously-bypass-hook-trust", "--disable", "shell_snapshot", "-c", "sandbox_workspace_write.network_access=true", "-a", "never", "exec", "--json", "-C", d.Workspace, "--sandbox", "workspace-write", prompt)
	command.Dir = d.Workspace
	command.Env = agentEnvironment(d)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return out, err
	}
	root, _ := c.agentRoot(d.Project)
	log, err := os.OpenFile(filepath.Join(root, "task-stderr.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return out, err
	}
	defer log.Close()
	command.Stderr = log
	if err = command.Start(); err != nil {
		return out, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// Codex's normal interrupt cancels its active tool requests. A
			// terminal SIGTERM alone can leave a tool executor running.
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGINT)
			interrupt := time.NewTimer(12 * time.Second)
			defer interrupt.Stop()
			select {
			case <-done:
				return
			case <-interrupt.C:
			}
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
			terminate := time.NewTimer(5 * time.Second)
			defer terminate.Stop()
			select {
			case <-done:
				return
			case <-terminate.C:
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				_ = stdout.Close()
			}
		case <-done:
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 65536), 2*1024*1024)
	var transcript strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if transcript.Len()+len(line) < 8*1024*1024 {
			transcript.WriteString(line + "\n")
		}
		var event struct {
			Type   string `json:"type"`
			Thread string `json:"thread_id"`
			Item   struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &event) == nil {
			if event.Type == "thread.started" {
				out.Session = "codex_" + event.Thread
			}
			if event.Item.Type == "agent_message" && notify != nil {
				notify(event.Item.Text)
			}
		}
	}
	err = command.Wait()
	out.Output = transcript.String()
	if command.ProcessState != nil {
		out.ExitCode = command.ProcessState.ExitCode()
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return out, scanErr
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}

// SyncSession uses the official plugin's supported end/manual-sync operation.
// It does not inject QA, manufacture learning files or alter official outputs.
func (c *Controller) SyncSession(ctx context.Context, d AgentScope, session string) error {
	if !strings.HasPrefix(session, "codex_") || strings.ContainsAny(session, "/\\ \t\r\n") {
		return errors.New("actual official session required")
	}
	script := filepath.Join(d.Plugin, "scripts/sync-session-to-graph.py")
	command := exec.CommandContext(ctx, "/usr/bin/python3", script, "--strict")
	command.Env = append(agentEnvironment(d), "COGNEE_SYNC_TIMEOUT=180", "COGNEE_SYNC_SESSION_ID="+session, "COGNEE_SYNC_DATASET="+d.DatasetName)
	command.Dir = d.Workspace
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("官方结束同步未通过：%w", err)
	}
	root, _ := c.agentRoot(d.Project)
	return os.WriteFile(filepath.Join(root, "official-sync-result.txt"), output, 0600)
}

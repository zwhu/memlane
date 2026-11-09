// Package official prepares an independently owned Cognee service. It does
// not inspect Agent tokens, install Agents or infer a project from model text.
package official

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/modelbridge"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type Config struct {
	Source               string `json:"source"`
	PublicKey            string `json:"public_key"`
	AllowLocalTestSource bool   `json:"allow_local_test_source,omitempty"`
}
type Settings struct {
	Root                 string              `json:"root"`
	Environment          map[string]string   `json:"environment"`
	ModelBridge          *modelbridge.Config `json:"model_bridge,omitempty"`
	ModelRestartRequired bool                `json:"model_restart_required,omitempty"`
}
type State struct {
	PID      int    `json:"pid"`
	Endpoint string `json:"endpoint"`
	Root     string `json:"root"`
	Version  string `json:"version"`
	Phase    string `json:"phase"`
}
type Status struct {
	Phase, Message, Endpoint, Instance, Model string
	Downloaded, Total                         int64
	EngineBytes, ModelBytes                   int64
	EmbeddingDimensions                       int
}
type Controller struct {
	Root   string
	Config Config
	gate   sync.Mutex
	mu     sync.Mutex
	cancel context.CancelFunc
	tasks  map[string]context.CancelFunc
}

func privateJSON(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return errors.New("private bounded settings required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func (c *Controller) Settings() (Settings, error) {
	var s Settings
	err := privateJSON(filepath.Join(c.Root, "cognee-config.json"), &s)
	if err == nil && (s.Root != filepath.Join(c.Root, "cognee") || s.Environment == nil) {
		err = errors.New("Cognee 私有服务配置无效")
	}
	return s, err
}
func (c *Controller) SaveSettings(s Settings) error {
	if !filepath.IsAbs(c.Root) {
		return errors.New("absolute private root required")
	}
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		return err
	}
	s.Root = filepath.Join(c.Root, "cognee")
	return serviceipc.WriteJSON(filepath.Join(c.Root, "cognee-config.json"), s)
}
func (c *Controller) Manager(ctx context.Context) (*components.Manager, components.Feed, error) {
	key, err := base64.StdEncoding.DecodeString(c.Config.PublicKey)
	if err != nil {
		return nil, components.Feed{}, err
	}
	m, err := components.New(filepath.Join(c.Root, "runtime"), ed25519.PublicKey(key), 1, c.Config.AllowLocalTestSource)
	if err != nil {
		return nil, components.Feed{}, err
	}
	f, err := m.Feed(ctx, c.Config.Source)
	if err != nil && ctx.Err() == nil {
		var transport *url.Error
		if errors.As(err, &transport) {
			f, err = m.CachedFeed(c.Config.Source)
		}
	}
	return m, f, err
}
func (c *Controller) Cancel() {
	c.cancelModelRequests()
	c.mu.Lock()
	cancel := c.cancel
	tasks := make([]context.CancelFunc, 0, len(c.tasks))
	for _, stop := range c.tasks {
		tasks = append(tasks, stop)
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, stop := range tasks {
		stop()
	}
}
func (c *Controller) ReadState() (State, error) {
	var state State
	err := privateJSON(filepath.Join(c.Root, "cognee-service-state.json"), &state)
	if err == nil && (state.Root != filepath.Join(c.Root, "cognee") || state.Version != "1.6.7") {
		err = errors.New("service state identity differs")
	}
	return state, err
}
func Request(ctx context.Context, endpoint, path, method, token, contentType string, body []byte, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("explicit loopback Cognee endpoint required")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if err = SetCredential(req.Header, token); err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Cognee redirects are not permitted") }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Cognee %s HTTP %d", path, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 8*1024*1024 {
		return errors.New("Cognee response exceeds limit")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Cognee 1.6.7 has separate official transports: API keys use X-Api-Key;
// the three-part access_token returned by login uses the Bearer transport.
func SetCredential(header http.Header, token string) error {
	if token == "" {
		return nil
	}
	if len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return errors.New("invalid bounded Cognee credential")
	}
	if strings.Count(token, ".") == 2 {
		header.Set("Authorization", "Bearer "+token)
	} else {
		header.Set("X-Api-Key", token)
	}
	return nil
}
func (c *Controller) OwnerToken(ctx context.Context, endpoint string) (string, error) {
	s, err := c.Settings()
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"access_token"`
	}
	form := url.Values{"username": {s.Environment["DEFAULT_USER_EMAIL"]}, "password": {s.Environment["DEFAULT_USER_PASSWORD"]}}
	err = Request(ctx, endpoint, "/api/v1/auth/login", "POST", "", "application/x-www-form-urlencoded", []byte(form.Encode()), &response)
	return response.Token, err
}
func running(state State, executable, config string) bool {
	if state.PID <= 1 || state.Phase == "stopped" {
		return false
	}
	output, err := exec.Command("/bin/ps", "-p", strconv.Itoa(state.PID), "-o", "command=").Output()
	return err == nil && strings.TrimSpace(string(output)) == executable+" --config "+config
}
func (c *Controller) Prepare(ctx context.Context, notify func(Status)) (resultErr error) {
	return c.prepare(ctx, notify, true)
}
func (c *Controller) PrepareEnvironment(ctx context.Context, notify func(Status)) error {
	return c.prepare(ctx, notify, false)
}
func (c *Controller) prepare(ctx context.Context, notify func(Status), verifyModel bool) (resultErr error) {
	c.gate.Lock()
	defer c.gate.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); c.cancel = nil; c.mu.Unlock() }()
	if notify == nil {
		notify = func(Status) {}
	}
	settings, err := c.Settings()
	if os.IsNotExist(err) && !verifyModel {
		password := make([]byte, 32)
		if _, err = rand.Read(password); err != nil {
			return err
		}
		settings = Settings{Environment: map[string]string{"DEFAULT_USER_EMAIL": "memlane-local@example.com", "DEFAULT_USER_PASSWORD": hex.EncodeToString(password), "EMBEDDING_PROVIDER": "fastembed", "EMBEDDING_MODEL": "BAAI/bge-small-zh-v1.5", "EMBEDDING_DIMENSIONS": "512", "EMBEDDING_MAX_TOKENS": "512"}}
		err = c.SaveSettings(settings)
		settings.Root = filepath.Join(c.Root, "cognee")
	}
	if err != nil {
		return err
	}
	if verifyModel && settings.ModelBridge == nil && (settings.Environment["LLM_API_KEY"] == "" || settings.Environment["LLM_MODEL"] == "") {
		return errors.New("请选择并验证提炼 Agent")
	}
	notify(Status{Phase: "checking", Message: "核对真实 Cognee 服务、模型与官方插件组件…"})
	m, feed, err := c.Manager(ctx)
	if err != nil {
		return err
	}
	installed := map[string]components.Installed{}
	var total, done, engineBytes, modelBytes int64
	for _, id := range []string{"cognee-service", "cognee-bge-zh", "cognee-codex-plugin"} {
		var selected *components.Descriptor
		for i := range feed.Components {
			if feed.Components[i].ID == id {
				selected = &feed.Components[i]
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("组件清单缺少 %s", id)
		}
		total += selected.Bytes
		if id == "cognee-bge-zh" {
			modelBytes += selected.Bytes
		} else {
			engineBytes += selected.Bytes
		}
	}
	report := notify
	notify = func(status Status) { status.EngineBytes, status.ModelBytes = engineBytes, modelBytes; report(status) }
	for _, id := range []string{"cognee-service", "cognee-bge-zh", "cognee-codex-plugin"} {
		for _, d := range feed.Components {
			if d.ID != id {
				continue
			}
			item, e := m.Prepare(ctx, d, func(p components.Progress) {
				notify(Status{Phase: p.Phase, Message: "正在准备 " + id, Downloaded: done + p.Downloaded, Total: total})
			})
			if e != nil {
				return e
			}
			installed[id] = item
			done += d.Bytes
		}
	}
	settings.Environment["HF_HOME"] = installed["cognee-bge-zh"].Directory
	settings.Environment["HF_HUB_OFFLINE"] = "1"
	if verifyModel && settings.ModelBridge != nil && installed["cognee-service"].Metadata.Version != "1.6.7-memlane.5" {
		return errors.New("当前 Cognee 服务组件尚未适配 pi 完整 Schema；请准备已验证的 1.6.7-memlane.5 组件后重试")
	}
	if err = c.SaveSettings(settings); err != nil {
		return err
	}
	executable := filepath.Join(installed["cognee-service"].Directory, installed["cognee-service"].Metadata.Executable)
	if err = c.restartModelProcess(ctx, executable, &settings); err != nil {
		return err
	}
	if err = c.startModelBridge(ctx, &settings); err != nil {
		return err
	}
	config := filepath.Join(c.Root, "cognee-config.json")
	state, stateErr := c.ReadState()
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return stateErr
	}
	spawned := false
	if !running(state, executable, config) {
		if state.PID > 1 && state.Phase != "stopped" && syscall.Kill(state.PID, 0) == nil {
			return errors.New("该数据根已由另一进程占用，请先停止它")
		}
		log, err := os.OpenFile(filepath.Join(c.Root, "service-console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		command := exec.Command(executable, "--config", config)
		command.Dir = c.Root
		command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.Root, "PYTHON_DOTENV_DISABLED=1"}
		command.Stdout = log
		command.Stderr = log
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = command.Start(); err != nil {
			log.Close()
			return err
		}
		spawned = true
		go func() { _ = command.Wait(); log.Close() }()
		// Remember the exact process we own even before its public state exists.
		defer func() {
			if resultErr != nil || ctx.Err() != nil {
				_ = command.Process.Signal(syscall.SIGTERM)
			}
		}()
	}
	notify(Status{Phase: "verifying", Message: "验证独立服务、模型授权与实际嵌入…", Total: total, Downloaded: done})
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	var token string
	for {
		state, e := c.ReadState()
		if e == nil && state.Phase != "stopped" {
			probe, stop := context.WithTimeout(ctx, 2*time.Second)
			token, e = c.OwnerToken(probe, state.Endpoint)
			stop()
			if e == nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("独立服务启动/登录验证超时；新启动=%t，请查看该数据根的服务诊断", spawned)
		case <-tick.C:
		}
	}
	state, err = c.ReadState()
	if err != nil {
		return err
	}
	var identity struct {
		Instance string `json:"instance"`
		Version  string `json:"cognee_version"`
		Adapter  string `json:"adapter"`
	}
	if err = Request(ctx, state.Endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &identity); err != nil {
		return err
	}
	if identity.Version != "1.6.7" || identity.Adapter != "memlane-material-graph-v1" {
		return errors.New("Cognee 实际版本或衔接接口不匹配")
	}
	var health struct {
		Status  string `json:"status"`
		Health  string `json:"health"`
		Version string `json:"version"`
	}
	// The official basic health route probes all four critical local stores.
	// Its detailed variant also performs LLM inference, which must wait for the
	// user's explicit model verification below.
	if err = Request(ctx, state.Endpoint, "/health", "GET", token, "", nil, &health); err != nil {
		return fmt.Errorf("Cognee 数据服务尚未验证可用（图/向量子进程）：%w", err)
	}
	if health.Status != "ready" || health.Health != "healthy" || health.Version != "1.6.7" {
		return errors.New("Cognee 图/向量/关系库完整健康验证未通过")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if settings.ModelRestartRequired {
		settings.ModelRestartRequired = false
		if err = c.SaveSettings(settings); err != nil {
			return err
		}
	}
	if !verifyModel {
		notify(Status{Phase: "service_ready", Message: "Cognee 本地服务与依赖已验证；提炼 Agent 在首次任务或设置中选择", Endpoint: state.Endpoint, Instance: identity.Instance, Total: total, Downloaded: done})
		return nil
	}
	var probe struct {
		State         string `json:"state"`
		Model         string `json:"model"`
		Dimensions    int    `json:"embedding_dimensions"`
		MemoryWritten bool   `json:"memory_written"`
	}
	if err = Request(ctx, state.Endpoint, "/api/v1/memlane/verify-environment", "POST", token, "application/json", []byte("{}"), &probe); err != nil {
		return err
	}
	if probe.State != "verified" || probe.Dimensions <= 0 || probe.MemoryWritten {
		return errors.New("模型可用探测未通过")
	}
	notify(Status{Phase: "ready", Message: "Cognee 服务与模型已实际验证；官方 Agent 接入范围仍需确认", Endpoint: state.Endpoint, Instance: identity.Instance, Model: probe.Model, EmbeddingDimensions: probe.Dimensions, Total: total, Downloaded: done})
	return nil
}

func (c *Controller) Stop(ctx context.Context) (resultErr error) {
	c.Cancel()
	c.gate.Lock()
	defer c.gate.Unlock()
	defer func() { resultErr = errors.Join(resultErr, c.stopModelBridge(ctx)) }()
	for {
		c.mu.Lock()
		active := len(c.tasks)
		c.mu.Unlock()
		if active == 0 {
			break
		}
		pause := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			pause.Stop()
			return ctx.Err()
		case <-pause.C:
		}
	}
	state, err := c.ReadState()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Phase == "stopped" || state.PID <= 1 {
		return nil
	}
	command, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(state.PID), "-o", "command=").Output()
	if err != nil {
		return nil
	}
	// Only this private config and its prepared executable can be terminated.
	expectedSuffix := " --config " + filepath.Join(c.Root, "cognee-config.json")
	text := strings.TrimSpace(string(command))
	prefix := filepath.Join(c.Root, "runtime/components/cognee-service/")
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, expectedSuffix) {
		return errors.New("service PID is not owned by this prepared scope")
	}
	if err = syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		return err
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for syscall.Kill(state.PID, 0) == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

package official

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/modelbridge"
)

func (c *Controller) SelectPiBridge(configuration modelbridge.Config) error {
	s, err := c.Settings()
	if os.IsNotExist(err) {
		password := make([]byte, 32)
		if _, err = rand.Read(password); err != nil {
			return err
		}
		s = Settings{Environment: map[string]string{"DEFAULT_USER_EMAIL": "memlane-local@example.com", "DEFAULT_USER_PASSWORD": hex.EncodeToString(password), "EMBEDDING_PROVIDER": "fastembed", "EMBEDDING_MODEL": "BAAI/bge-small-zh-v1.5", "EMBEDDING_DIMENSIONS": "512", "EMBEDDING_MAX_TOKENS": "512"}}
	} else if err != nil {
		return err
	}
	configuration.Root = filepath.Join(c.Root, "model-bridge")
	configuration.OwnerState = filepath.Join(c.Root, "cognee-service-state.json")
	if err = modelbridge.SaveConfig(configuration); err != nil {
		return err
	}
	s.ModelBridge = &configuration
	s.ModelRestartRequired = true
	// The real local access token is installed only when the bridge is started.
	for _, key := range []string{"LLM_API_KEY", "LLM_ENDPOINT", "LLM_MODEL", "LLM_PROVIDER"} {
		delete(s.Environment, key)
	}
	return c.SaveSettings(s)
}

func bridgeRunning(state modelbridge.State, cfg modelbridge.Config) bool {
	if state.Root != cfg.Root || state.PID <= 1 || state.Phase != "ready" {
		return false
	}
	command, err := exec.Command("/bin/ps", "-p", strconv.Itoa(state.PID), "-o", "command=").Output()
	return err == nil && strings.TrimSpace(string(command)) == cfg.Helper+" model-bridge --config "+filepath.Join(cfg.Root, "config.json")
}
func (c *Controller) restartModelProcess(ctx context.Context, executable string, s *Settings) error {
	if !s.ModelRestartRequired {
		return nil
	}
	previous, err := c.ReadState()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if running(previous, executable, filepath.Join(c.Root, "cognee-config.json")) {
		if err = syscall.Kill(previous.PID, syscall.SIGTERM); err != nil {
			return err
		}
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for syscall.Kill(previous.PID, 0) == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("old Cognee model process did not stop")
			case <-tick.C:
			}
		}
	}
	return c.stopModelBridge(ctx)
}
func (c *Controller) startModelBridge(ctx context.Context, s *Settings) error {
	if s.ModelBridge == nil {
		return nil
	}
	cfg := *s.ModelBridge
	if cfg.Root != filepath.Join(c.Root, "model-bridge") || cfg.OwnerState != filepath.Join(c.Root, "cognee-service-state.json") {
		return errors.New("model bridge identity differs")
	}
	if err := modelbridge.SaveConfig(cfg); err != nil {
		return err
	}
	token, err := modelbridge.Token(cfg.Root)
	if err != nil {
		return err
	}
	var state modelbridge.State
	err = modelbridge.ReadPrivate(filepath.Join(cfg.Root, "state.json"), &state)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !bridgeRunning(state, cfg) {
		if state.PID > 1 && state.Phase != "stopped" && syscall.Kill(state.PID, 0) == nil {
			return errors.New("model bridge root already occupied")
		}
		log, err := os.OpenFile(filepath.Join(cfg.Root, "console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		command := exec.Command(cfg.Helper, "model-bridge", "--config", filepath.Join(cfg.Root, "config.json"))
		command.Dir = cfg.Root
		command.Env = []string{"HOME=" + cfg.Root, "PATH=" + filepath.Dir(cfg.Executable) + ":/usr/bin:/bin"}
		for _, key := range []string{"TMPDIR", "LANG", "LC_ALL", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
			if value, ok := os.LookupEnv(key); ok {
				command.Env = append(command.Env, key+"="+value)
			}
		}
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = command.Start(); err != nil {
			log.Close()
			return err
		}
		go func() { _ = command.Wait(); _ = log.Close() }()
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if modelbridge.ReadPrivate(filepath.Join(cfg.Root, "state.json"), &state) == nil && bridgeRunning(state, cfg) {
			var response map[string]any
			probe, cancel := context.WithTimeout(ctx, time.Second)
			err = modelbridge.Call(probe, state.Endpoint, "/health", "GET", token, "", nil, &response)
			cancel()
			if err == nil && response["state"] == "ready" {
				if err = modelbridge.Call(ctx, state.Endpoint, "/v1/resume", "POST", token, "application/json", []byte("{}"), nil); err != nil {
					return err
				}
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("pi model bridge startup failed; see private service diagnostics")
		case <-tick.C:
		}
	}
	s.Environment["LLM_PROVIDER"] = "custom"
	s.Environment["LLM_MODEL"] = "openai/" + modelbridge.ModelAlias
	s.Environment["LLM_ENDPOINT"] = state.Endpoint + "/v1"
	s.Environment["LLM_API_KEY"] = token
	// The packaged service registers this private alias's actual schema
	// capability. Its native adapter then forwards the complete schema.
	delete(s.Environment, "STRUCTURED_OUTPUT_FRAMEWORK")
	delete(s.Environment, "LLM_INSTRUCTOR_MODE")
	return c.SaveSettings(*s)
}
func (c *Controller) cancelModelRequests() {
	s, err := c.Settings()
	if err != nil || s.ModelBridge == nil {
		return
	}
	var state modelbridge.State
	if modelbridge.ReadPrivate(filepath.Join(s.ModelBridge.Root, "state.json"), &state) != nil || !bridgeRunning(state, *s.ModelBridge) {
		return
	}
	token, err := modelbridge.Token(s.ModelBridge.Root)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = modelbridge.Call(ctx, state.Endpoint, "/v1/cancel", "POST", token, "application/json", []byte("{}"), nil)
	s.ModelRestartRequired = true
	_ = c.SaveSettings(s)
}
func (c *Controller) stopModelBridge(ctx context.Context) error {
	s, err := c.Settings()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.ModelBridge == nil {
		return nil
	}
	var state modelbridge.State
	err = modelbridge.ReadPrivate(filepath.Join(s.ModelBridge.Root, "state.json"), &state)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Phase == "stopped" || state.PID <= 1 {
		return nil
	}
	if !bridgeRunning(state, *s.ModelBridge) {
		if syscall.Kill(state.PID, 0) != nil {
			return nil
		}
		return errors.New("bridge PID identity differs")
	}
	token, err := modelbridge.Token(s.ModelBridge.Root)
	if err != nil {
		return err
	}
	_ = modelbridge.Call(ctx, state.Endpoint, "/shutdown", "POST", token, "application/json", []byte("{}"), nil)
	_ = syscall.Kill(state.PID, syscall.SIGTERM)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for syscall.Kill(state.PID, 0) == nil {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(state.PID, syscall.SIGKILL)
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

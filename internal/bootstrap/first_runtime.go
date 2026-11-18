package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type FirstInstallation struct {
	Schema  int    `json:"schema"`
	StoreID string `json:"store_id"`
	Setup   bool   `json:"setup"`
}

type FirstRuntime struct {
	ConfigPath, StoreRoot, Resources, Python, HostHome string
	Plan                                               Plan
	ActivationFile                                     string
}

type firstRuntimeConfig struct {
	First          *FirstInstallation `json:"first_install"`
	Root           string             `json:"official_data"`
	Commit         string             `json:"service_commit"`
	Python         string             `json:"python"`
	ServiceURL     string             `json:"service_url"`
	TokenFile      string             `json:"token_file"`
	GenerationFile string             `json:"generation_file"`
	GenerationMode string             `json:"generation_mode"`
	Model          string             `json:"model"`
	Control        struct {
		URL       string `json:"url"`
		TokenFile string `json:"management_token_file"`
	} `json:"project_control"`
}

func DefaultRuntimeConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library/Application Support/Memlane/runtime.json"), nil
}

func loadFirstRuntimeConfig(path string) (firstRuntimeConfig, error) {
	var config firstRuntimeConfig
	b, err := privateRead(path)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return config, err
	}
	if config.First == nil || config.First.Schema != 1 || len(config.First.StoreID) != 32 || !filepath.IsAbs(config.Root) {
		return config, errors.New("已有配置不属于首次安装，保留原配置与内容库")
	}
	b, err = privateRead(filepath.Join(config.Root, "store-identity.json"))
	if err != nil {
		return config, err
	}
	var receipt StoreReceipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		return config, err
	}
	if receipt.Schema != 1 || !receipt.StorageInitialized || receipt.OwnerID == "" || receipt.KeyID == "" || receipt.StoreID != config.First.StoreID || receipt.SourceCommit != config.Commit || config.TokenFile != filepath.Join(config.Root, "owner.token") {
		return config, errors.New("首次运行配置与官方库身份不同，保留原配置")
	}
	return config, nil
}

// PrepareFirstRuntime never treats a broken existing daily configuration as a
// fresh install. The only SDK initialization path is the explicitly new store.
func (e *Environment) PrepareFirstRuntime(ctx context.Context, configPath string, assets fs.FS) (FirstRuntime, error) {
	var prepared FirstRuntime
	if !filepath.IsAbs(configPath) {
		return prepared, errors.New("首次运行配置需要绝对路径")
	}
	root := filepath.Join(filepath.Dir(configPath), "library")
	_, statErr := os.Lstat(configPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return prepared, statErr
	}
	config, err := loadFirstRuntimeConfig(configPath)
	existing := err == nil
	if err != nil && (statErr == nil || !errors.Is(err, os.ErrNotExist)) {
		return prepared, err
	}
	if existing {
		root = config.Root
		if e.Options.ProtectedStore != root || config.Commit != e.Source.Managed.Spec.Commit {
			return prepared, errors.New("已有内容库绑定不同，不能切换或新建另一套库")
		}
	} else if e.Options.ProtectedStore != "" {
		return prepared, errors.New("已有内容库缺少原运行配置，请恢复配置后继续")
	}
	plan, err := e.Detect(ctx)
	prepared.Plan = plan
	if err != nil {
		return prepared, err
	}
	if !plan.Complete() {
		return prepared, errors.New("环境组件尚未全部准备完成")
	}
	python, err := e.runtimePython(ctx)
	if err != nil {
		return prepared, err
	}
	pluginRequirement, err := e.Requirement("agent-plugin")
	if err != nil {
		return prepared, err
	}
	plugin, err := e.Plugin.Probe(ctx, pluginRequirement)
	if err != nil {
		return prepared, err
	}
	if plugin.Status != Available {
		return prepared, errors.New("固定 Agent 插件尚未准备完成")
	}
	if !existing {
		engineAssets, err := fs.Sub(assets, "engine")
		if err != nil {
			return prepared, err
		}
		if _, err := e.InitializeStore(ctx, root, engineAssets); err != nil {
			return prepared, err
		}
	}
	resources, err := PrepareRuntimeResources(ctx, e.Installer().Root, assets)
	if err != nil {
		return prepared, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return prepared, err
	}
	err = runManaged(ctx, root, python, "-I", "-B", filepath.Join(resources, "engine/python/first_install_runtime.py"),
		"--store-root", root, "--runtime-config", configPath, "--plugin", plugin.Path, "--host-home", home, "--python-executable", python, "--prepare-only")
	if err != nil {
		return prepared, err
	}
	return FirstRuntime{ConfigPath: configPath, StoreRoot: root, Resources: resources, Python: python, HostHome: home, Plan: plan}, nil
}

func loopbackJSON(ctx context.Context, origin, endpoint, tokenFile, header, prefix string) (map[string]any, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("首次服务地址必须为明确的本机端口")
	}
	token, err := privateRead(tokenFile)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(origin, "/")+endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(header, prefix+strings.TrimSpace(string(token)))
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("首次服务身份核对返回 %d", response.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, errors.New("首次服务回执超出限制")
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func FirstRuntimeReady(ctx context.Context, configPath string) (bool, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return false, err
	}
	b, err := privateRead(filepath.Join(config.Root, ".memlane-dev-owner.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
		Config  string `json:"config"`
		Root    string `json:"official_data"`
		Phase   string `json:"phase"`
	}
	if err := json.Unmarshal(b, &owner); err != nil {
		return false, err
	}
	if owner.PID < 1 || owner.Config != configPath || owner.Root != config.Root {
		return false, errors.New("首次服务所有权记录不同，保留现场")
	}
	started, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(owner.PID), "-o", "lstart=").Output()
	if err != nil || strings.TrimSpace(string(started)) != owner.Started {
		return false, errors.New("首次服务所有者已变化，保留原记录等待核对")
	}
	if owner.Phase != "first_install_setup" && owner.Phase != "ready" {
		return false, nil
	}
	var receipt StoreReceipt
	b, err = privateRead(filepath.Join(config.Root, "store-identity.json"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		return false, err
	}
	me, err := loopbackJSON(ctx, config.ServiceURL, "/api/v1/users/me", config.TokenFile, "X-Api-Key", "")
	if err != nil {
		return false, err
	}
	if me["id"] != receipt.OwnerID {
		return false, errors.New("首次官方服务 owner 身份不同")
	}
	projects, err := loopbackJSON(ctx, config.Control.URL, "/v1/projects", config.Control.TokenFile, "Authorization", "Bearer ")
	if err != nil {
		return false, err
	}
	if _, ok := projects["revision"].(float64); !ok {
		return false, errors.New("首次管理服务回执不完整")
	}
	return true, nil
}

// StartFirstRuntime keeps the dev owner's existing process and recovery rules.
// A timeout does not kill or replace an owner whose startup is still in flight.
func StartFirstRuntime(ctx context.Context, prepared FirstRuntime) error {
	for _, path := range []string{prepared.ConfigPath, prepared.StoreRoot, prepared.Resources, prepared.Python, prepared.HostHome} {
		if !filepath.IsAbs(path) {
			return errors.New("首次运行参数必须为明确的绝对路径")
		}
	}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		return err
	}
	if config.Root != prepared.StoreRoot || config.Python != prepared.Python {
		return errors.New("首次运行参数与原配置不同，未启动")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = privateRead(filepath.Join(prepared.StoreRoot, ".memlane-dev-owner.json"))
	exited := make(chan error, 1)
	if prepared.ActivationFile != "" && prepared.ActivationFile != filepath.Join(prepared.StoreRoot, "first-install-activation.json") {
		return errors.New("首次启用回执位置不同")
	}
	if prepared.ActivationFile != "" && err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) || prepared.ActivationFile != "" {
		logs := filepath.Join(filepath.Dir(prepared.ConfigPath), "runtime-logs")
		if err := os.MkdirAll(logs, 0700); err != nil {
			return err
		}
		logDirectory, err := os.Lstat(logs)
		if err != nil {
			return err
		}
		if !logDirectory.IsDir() || logDirectory.Mode()&os.ModeSymlink != 0 || logDirectory.Mode().Perm()&0077 != 0 {
			return errors.New("首次运行日志目录归属不明确，保留原目录")
		}
		log, err := os.OpenFile(filepath.Join(logs, "launcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
		info, err := log.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("首次运行日志必须为私有普通文件")
		}
		arguments := []string{filepath.Join(prepared.Resources, "scripts/dev.py"), "start", "--config", prepared.ConfigPath, "--no-ui"}
		if prepared.ActivationFile != "" {
			var fields struct {
				Control struct {
					Plugin string `json:"plugin_dir"`
				} `json:"project_control"`
			}
			b, err := privateRead(prepared.ConfigPath)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &fields); err != nil {
				return err
			}
			arguments = []string{"-I", "-B", filepath.Join(prepared.Resources, "engine/python/first_install_runtime.py"), "--store-root", prepared.StoreRoot, "--runtime-config", prepared.ConfigPath, "--plugin", fields.Control.Plugin, "--host-home", prepared.HostHome, "--activate-file", prepared.ActivationFile}
		}
		command := exec.Command(prepared.Python, arguments...)
		command.Dir = prepared.Resources
		path := os.Getenv("PATH")
		if path == "" {
			path = "/usr/bin:/bin:/usr/sbin:/sbin"
		}
		command.Env = []string{"HOME=" + prepared.HostHome, "PATH=" + filepath.Join(prepared.HostHome, ".local/bin") + ":" + path, "LANG=en_US.UTF-8", "PYTHONDONTWRITEBYTECODE=1"}
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		command.Stdout, command.Stderr = log, log
		if err := command.Start(); err != nil {
			return err
		}
		go func() { exited <- command.Wait() }()
	} else if err != nil {
		return err
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := FirstRuntimeReady(ctx, prepared.ConfigPath)
		if err != nil && prepared.ActivationFile == "" {
			return err
		}
		if prepared.ActivationFile != "" {
			complete, stateError := FirstInitializationComplete(prepared.ConfigPath)
			if stateError != nil {
				return stateError
			}
			ready = ready && complete
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("首次服务就绪尚未确认，进度及所有权记录已保留")
		case err := <-exited:
			if err == nil && prepared.ActivationFile != "" {
				exited = nil
				continue
			}
			return fmt.Errorf("首次运行入口退出，保留日志与原库：%v", err)
		case <-ticker.C:
		}
	}
}

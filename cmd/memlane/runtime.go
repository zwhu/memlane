package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/internal/bootstrap"
)

type runtimeConfig struct {
	Path                string            `json:"-"`
	StoreRoot           string            `json:"store_root"`
	OfficialData        string            `json:"official_data"`
	GatewayURL          string            `json:"gateway_url"`
	ControlURL          string            `json:"control_url"`
	ManagementTokenFile string            `json:"management_token_file"`
	EngineURL           string            `json:"engine_url"`
	ServiceURL          string            `json:"service_url"`
	EngineTokenFile     string            `json:"engine_token_file"`
	InternalTokenFile   string            `json:"internal_token_file"`
	EngineConfig        string            `json:"engine_config"`
	EngineWorkDir       string            `json:"engine_work_dir"`
	CompletionTokenFile string            `json:"completion_token_file"`
	Python              string            `json:"python"`
	OutputLanguage      string            `json:"output_language"`
	AgentKeyFiles       map[string]string `json:"agent_key_files"`
	Control             struct {
		URL                 string            `json:"url"`
		ManagementTokenFile string            `json:"management_token_file"`
		AgentKeyFiles       map[string]string `json:"agent_control_token_files"`
		RegistryFile        string            `json:"registry_file"`
	} `json:"project_control"`
	raw []byte
}

func readRuntime(path string) (runtimeConfig, error) {
	cfg := runtimeConfig{}
	path = firstNonempty(path, os.Getenv("MEMLANE_DEV_RUNTIME_CONFIG"))
	if path == "" {
		const daily = ".local/daily/runtime.json"
		if _, err := os.Stat(daily); err == nil {
			path = daily
		} else if !errors.Is(err, os.ErrNotExist) {
			return cfg, err
		}
	}
	if path == "" {
		var err error
		path, err = bootstrap.DefaultRuntimeConfig()
		if err != nil {
			return cfg, err
		}
	}
	var err error
	cfg.Path, err = filepath.Abs(path)
	if err != nil {
		return cfg, err
	}
	cfg.raw, err = os.ReadFile(cfg.Path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(cfg.raw, &cfg); err != nil {
		return cfg, err
	}
	cfg.StoreRoot = cfg.localPath(firstNonempty(cfg.StoreRoot, cfg.OfficialData))
	cfg.OfficialData = cfg.localPath(cfg.OfficialData)
	cfg.GatewayURL = strings.TrimRight(firstNonempty(cfg.GatewayURL, cfg.ControlURL, cfg.Control.URL, "http://127.0.0.1:18790"), "/")
	cfg.ManagementTokenFile = cfg.localPath(firstNonempty(cfg.ManagementTokenFile, cfg.Control.ManagementTokenFile))
	cfg.EngineURL = strings.TrimRight(firstNonempty(cfg.EngineURL, cfg.ServiceURL), "/")
	cfg.EngineTokenFile = cfg.localPath(firstNonempty(cfg.EngineTokenFile, cfg.InternalTokenFile))
	cfg.EngineConfig = cfg.localPath(cfg.EngineConfig)
	cfg.EngineWorkDir = cfg.localPath(cfg.EngineWorkDir)
	cfg.CompletionTokenFile = cfg.localPath(cfg.CompletionTokenFile)
	cfg.Python = cfg.localPath(cfg.Python)
	return cfg, nil
}

func (cfg runtimeConfig) localPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(cfg.Path), path)
}

func (cfg runtimeConfig) agentKeyFile(host string) string {
	legacy := strings.ReplaceAll(host, "-", "_")
	return cfg.localPath(firstNonempty(cfg.AgentKeyFiles[host], cfg.AgentKeyFiles[legacy], cfg.Control.AgentKeyFiles[legacy], cfg.Control.AgentKeyFiles[host]))
}

func (cfg runtimeConfig) validateStore() error {
	if !filepath.IsAbs(cfg.StoreRoot) {
		return errors.New("runtime store root is required")
	}
	return hooks.ValidateGatewayURL(cfg.GatewayURL)
}

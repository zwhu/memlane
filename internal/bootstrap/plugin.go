package bootstrap

import (
	"errors"
	"strings"
)

// PluginDriver retains the fields consumed by the desktop compatibility adapter.
type PluginDriver struct {
	ExistingPath string
	Manifest     []byte
}

const PluginPolicyUpdateReasonPrefix = "官方插件版本未变，仅 Memlane 策略补丁有更新"

func IsPluginPolicyUpdate(obs Observation) bool {
	return obs.Status == Incompatible && strings.HasPrefix(obs.Reason, PluginPolicyUpdateReasonPrefix)
}

func ApplyManagedPluginUpdate(configFile, officialData, newPluginDir string, manifest []byte) (string, error) {
	return "", errors.New("Agent 接入已迁移至内置 memlane hook，无需更新旧插件")
}

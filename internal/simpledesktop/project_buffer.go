package simpledesktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type PendingMaterials struct {
	Pending   int    `json:"pending"`
	Signature string `json:"signature"`
	Unknown   int    `json:"unknown_outcomes"`
	Blocked   *struct {
		Reason string `json:"reason"`
	} `json:"blocked"`
	Drained   int    `json:"drained"`
	Discarded int    `json:"discarded"`
	Error     string `json:"error"`
}

type boundedBufferOutput struct{ buffer bytes.Buffer }

func (b *boundedBufferOutput) Len() int      { return b.buffer.Len() }
func (b *boundedBufferOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedBufferOutput) Write(value []byte) (int, error) {
	if b.Len()+len(value) > 64*1024 {
		return 0, io.ErrShortWrite
	}
	return b.buffer.Write(value)
}

func verifyInstallation(installation AgentInstallation) error {
	root := installation.PluginDir
	if !filepath.IsAbs(root) || !filepath.IsAbs(installation.EnvFile) || !filepath.IsAbs(installation.Home) {
		return fmt.Errorf("受管插件配置尚未确认")
	}
	receipt, err := os.ReadFile(filepath.Join(root, ".memlane-project-access.json"))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(receipt)) != installation.ManifestSHA {
		return fmt.Errorf("受管插件输入发生变化，请重新核对")
	}
	var input struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(receipt, &input) != nil || input.Files["scripts/_memlane_policy.py"] != installation.HelperSHA {
		return fmt.Errorf("受管插件输入未确认")
	}
	for name, expected := range input.Files {
		if filepath.IsAbs(name) || strings.HasPrefix(filepath.Clean(name), "..") {
			return fmt.Errorf("受管插件文件路径无效")
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("受管插件文件未确认")
		}
		body, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(body)) != expected {
			return fmt.Errorf("受管插件文件发生变化，请重新核对")
		}
	}
	return nil
}

func runPendingMaterials(ctx context.Context, python string, installation AgentInstallation, binding ProjectBinding, dataset, action, signature string) (PendingMaterials, error) {
	var result PendingMaterials
	if !filepath.IsAbs(python) || binding.HostSessionKey == "" || dataset == "" {
		return result, fmt.Errorf("受管会话尚未确认")
	}
	if action != "show" && action != "replay" && action != "discard" {
		return result, fmt.Errorf("待发材料操作无效")
	}
	if err := verifyInstallation(installation); err != nil {
		return result, err
	}
	arguments := []string{filepath.Join(installation.PluginDir, "scripts/_memlane_policy.py"), "--session-key", binding.HostSessionKey, "--dataset", dataset, "--action", action}
	if action != "show" {
		arguments = append(arguments, "--expected-signature", signature)
	}
	command := exec.CommandContext(ctx, python, arguments...)
	command.Dir = installation.Home
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key != "HOME" && key != "COGNEE_ENV_FILE" && key != "COGNEE_SESSION_KEY" && key != "COGNEE_PLUGIN_IN_VENV" {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "HOME="+installation.Home, "COGNEE_ENV_FILE="+installation.EnvFile, "COGNEE_SESSION_KEY="+binding.HostSessionKey, "COGNEE_PLUGIN_IN_VENV=1")
	var output boundedBufferOutput
	command.Stdout = &output
	err := command.Run()
	if output.Len() > 64*1024 || json.Unmarshal(output.Bytes(), &result) != nil {
		return result, fmt.Errorf("待发材料结果未确认，保留原材料；不会自动重试")
	}
	if result.Error != "" || err != nil {
		message := "待发材料操作未确认，保留原材料"
		switch result.Error {
		case "pending_materials_changed_review_again":
			message = "待发材料已变化，请重新核对后选择"
		case "buffer_send_in_progress":
			message = "仍有发送正在进行，本次未更改材料"
		case "original_write_result_still_unknown":
			message = "原写入结果仍未知，不重复补交"
		case "official_session_unconfirmed":
			message = "官方会话尚未确认"
		case "当前 Agent 没有此项目的写入权限":
			message = result.Error
		}
		return result, fmt.Errorf("%s", message)
	}
	return result, nil
}

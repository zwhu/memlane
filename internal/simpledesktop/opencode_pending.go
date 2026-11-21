package simpledesktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type PendingOperatorConfig struct {
	TokenFile  string `json:"token_file"`
	ControlURL string `json:"control_url"`
	ServiceURL string `json:"service_url"`
}

func pendingOperatorConfig(config Config) PendingOperatorConfig {
	result := PendingOperatorConfig{ServiceURL: config.ServiceURL}
	if config.ProjectControl != nil {
		result.TokenFile, result.ControlURL = config.ProjectControl.ManagementTokenFile, config.ProjectControl.URL
	}
	return result
}

func pendingFileHash(path string, limit int64, private bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("unconfirmed path")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", fmt.Errorf("unconfirmed path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit || (private && info.Mode().Perm()&0077 != 0) {
		return "", fmt.Errorf("unconfirmed file")
	}
	stream, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, stream); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func verifyOpenCodePending(installation AgentInstallation) error {
	root := installation.PluginDir
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || canonical != root {
		return fmt.Errorf("unconfirmed component")
	}
	path := filepath.Join(root, ".memlane-opencode-artifact.json")
	digest, err := pendingFileHash(path, 8*1024*1024, false)
	if err != nil || digest != installation.ManifestSHA {
		return fmt.Errorf("unconfirmed component")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest struct {
		Protocol int               `json:"protocol"`
		Files    map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Protocol != 1 || manifest.Files["dist/src/managed-pending-cli.js"] == "" || manifest.Files["dist/src/managed-pending-cli.js"] != installation.HelperSHA {
		return fmt.Errorf("unconfirmed worker")
	}
	found := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("component symlink")
		}
		if entry.IsDir() || path == filepath.Join(root, ".memlane-opencode-artifact.json") {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		expected, ok := manifest.Files[name]
		if !ok {
			return fmt.Errorf("extra component file")
		}
		actual, err := pendingFileHash(path, 64*1024*1024, false)
		if err != nil || actual != expected {
			return fmt.Errorf("changed component file")
		}
		found[name] = true
		return nil
	})
	if err != nil || len(found) != len(manifest.Files) {
		return fmt.Errorf("unconfirmed component tree")
	}
	digest, err = pendingFileHash(installation.BindingFile, 65536, true)
	if err != nil || digest != installation.BindingSHA {
		return fmt.Errorf("unconfirmed binding")
	}
	digest, err = pendingFileHash(installation.NodeExecutable, 512*1024*1024, false)
	if err != nil || digest != installation.NodeSHA {
		return fmt.Errorf("unconfirmed runtime")
	}
	return nil
}

func runOpenCodePending(ctx context.Context, installation AgentInstallation, binding ProjectBinding, dataset, action, signature string, operator PendingOperatorConfig) (PendingMaterials, error) {
	var result PendingMaterials
	if action != "show" && action != "check" && action != "replay" && action != "discard" {
		return result, fmt.Errorf("待处理记录操作无效")
	}
	if !filepath.IsAbs(operator.TokenFile) || operator.ControlURL == "" || operator.ServiceURL == "" {
		return result, fmt.Errorf("管理连接尚未确认，未更改记录")
	}
	if err := verifyOpenCodePending(installation); err != nil {
		return result, fmt.Errorf("连接组件需要重新核对，保留原记录")
	}
	request := map[string]any{
		"agent_id": binding.AgentID,
		"installation": map[string]string{"plugin_dir": installation.PluginDir, "manifest_sha256": installation.ManifestSHA,
			"binding_file": installation.BindingFile, "binding_sha256": installation.BindingSHA,
			"node_executable": installation.NodeExecutable, "node_sha256": installation.NodeSHA},
		"operator": operator,
		"command": map[string]string{"project_id": binding.ProjectID, "dataset_id": dataset,
			"host_session_key": binding.HostSessionKey, "action": action, "signature": signature},
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > 65536 {
		return result, fmt.Errorf("原操作尚未确认")
	}
	command := exec.CommandContext(ctx, installation.NodeExecutable, filepath.Join(installation.PluginDir, "dist/src/managed-pending-cli.js"))
	command.Dir = filepath.Dir(installation.BindingFile)
	command.Stdin = bytes.NewReader(raw)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "NODE_") || strings.HasPrefix(upper, "COGNEE_") || strings.HasPrefix(upper, "MEMLANE_") || strings.HasPrefix(upper, "OPENCODE_") || strings.HasPrefix(upper, "OPENAI_") || strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "GEMINI_") || strings.HasPrefix(upper, "GOOGLE_") || strings.HasSuffix(upper, "_PROXY") {
			continue
		}
		command.Env = append(command.Env, item)
	}
	var output, diagnostic boundedBufferOutput
	command.Stdout, command.Stderr = &output, &diagnostic
	err = command.Run()
	if output.Len() > 65536 || json.Unmarshal(output.Bytes(), &result) != nil {
		return result, fmt.Errorf("原操作结果未确认，保留原记录；不会自动重试")
	}
	if err != nil || result.Error != "" {
		message := "原操作结果未确认，保留原记录；不会自动重试"
		switch result.Error {
		case "pending_manager_auth_required", "pending_manager_unavailable":
			message = "管理连接尚未确认，未更改记录"
		case "pending_materials_changed_review_again":
			message = "待处理记录已变化，请重新核对后选择"
		case "original_write_result_still_unknown":
			message = "原采集结果仍未知，不重复补交"
		case "buffer_send_in_progress":
			message = "仍有发送正在进行，本次未更改记录"
		case "project_write_denied", "project_capture_disabled":
			message = "当前 Agent 没有此项目的采集权限"
		case "project_read_denied":
			message = "当前 Agent 无法核对原采集结果"
		}
		return result, fmt.Errorf("%s", message)
	}
	if result.Signature == "" || result.Pending < 0 || result.Unknown < 0 || result.Unknown > result.Pending {
		return result, fmt.Errorf("采集状态未确认，保留原记录")
	}
	return result, nil
}

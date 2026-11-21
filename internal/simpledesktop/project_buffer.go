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
	"sync"
)

type pendingMessageDetail struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Unknown bool   `json:"unknown"`
}

type PendingMaterials struct {
	Details          []pendingMessageDetail `json:"details,omitempty"`
	Pending          int                    `json:"pending"`
	UnknownEntryIDs  []string               `json:"unknown_entry_ids"`
	Signature        string                 `json:"signature"`
	Unknown          int                    `json:"unknown_outcomes"`
	RetryableUnknown int                    `json:"retryable_unknown"`
	Blocked          *struct {
		Reason string `json:"reason"`
	} `json:"blocked"`
	Drained   int    `json:"drained"`
	Discarded int    `json:"discarded"`
	Error     string `json:"error"`
	Checked         bool              `json:"checked"`
	Matched         int               `json:"matched_unknown"`
	SessionID       string            `json:"session_id"`
	SessionInactive bool              `json:"session_inactive,omitempty"`
	ColdWatch       *coldPendingWatch `json:"cold_watch,omitempty"`
}

type coldPendingWatch struct {
	MapPath       string `json:"map_path"`
	BridgePath    string `json:"bridge_path"`
	BridgeExists  bool   `json:"bridge_exists"`
	BridgeSize    int64  `json:"bridge_size"`
	BridgeMtimeNS int64  `json:"bridge_mtime_ns"`
}

type coldPendingKey struct {
	AgentID        string
	HostSessionKey string
	Dataset        string
	PluginDir      string
	ManifestSHA    string
	HelperSHA      string
	EnvFile        string
	Home           string
}

type coldPendingEntry struct {
	Seq             uint64
	Result          PendingMaterials
	Watch           coldPendingWatch
	ManifestPath    string
	ManifestSize    int64
	ManifestMtimeNS int64
}

const coldPendingCacheMaxEntries = 2048

var coldPendingCache = struct {
	mu      sync.Mutex
	nextSeq uint64
	entries map[coldPendingKey]coldPendingEntry
}{
	entries: map[coldPendingKey]coldPendingEntry{},
}

func clonePendingMaterials(in PendingMaterials) PendingMaterials {
	out := in
	out.ColdWatch = nil
	if in.Details != nil {
		out.Details = append([]pendingMessageDetail(nil), in.Details...)
	}
	if in.UnknownEntryIDs != nil {
		out.UnknownEntryIDs = append([]string(nil), in.UnknownEntryIDs...)
	}
	if in.Blocked != nil {
		b := *in.Blocked
		out.Blocked = &b
	}
	return out
}

func coldPendingWatchValid(w *coldPendingWatch) bool {
	return w != nil && filepath.IsAbs(w.MapPath) && filepath.IsAbs(w.BridgePath)
}

func coldPendingEntryUnchanged(entry coldPendingEntry) bool {
	minfo, err := os.Lstat(entry.ManifestPath)
	if err != nil || !minfo.Mode().IsRegular() || minfo.Size() != entry.ManifestSize || minfo.ModTime().UnixNano() != entry.ManifestMtimeNS {
		return false
	}
	if _, err := os.Lstat(entry.Watch.MapPath); !os.IsNotExist(err) {
		return false
	}
	binfo, err := os.Lstat(entry.Watch.BridgePath)
	if !entry.Watch.BridgeExists {
		return os.IsNotExist(err)
	}
	if err != nil || !binfo.Mode().IsRegular() || binfo.Size() != entry.Watch.BridgeSize || binfo.ModTime().UnixNano() != entry.Watch.BridgeMtimeNS {
		return false
	}
	return true
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
	receiptPath := filepath.Join(root, ".memlane-project-access.json")
	receiptInfo, err := os.Lstat(receiptPath)
	if err != nil || !receiptInfo.Mode().IsRegular() {
		return fmt.Errorf("受管插件输入发生变化，请重新核对")
	}
	receipt, err := os.ReadFile(receiptPath)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(receipt)) != installation.ManifestSHA {
		return fmt.Errorf("受管插件输入发生变化，请重新核对")
	}
	var input struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(receipt, &input) != nil || input.Files["scripts/_memlane_policy.py"] == "" || input.Files["scripts/_plugin_common.py"] == "" {
		return fmt.Errorf("受管插件输入未确认")
	}
	if installation.HelperSHA != "" && input.Files["scripts/_memlane_policy.py"] != installation.HelperSHA {
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

func runPendingMaterials(ctx context.Context, python string, installation AgentInstallation, binding ProjectBinding, dataset, action, signature string, operator ...PendingOperatorConfig) (PendingMaterials, error) {
	if installation.HostKind == "opencode" {
		if len(operator) != 1 {
			return PendingMaterials{}, fmt.Errorf("管理连接尚未确认，未更改记录")
		}
		return runOpenCodePending(ctx, installation, binding, dataset, action, signature, operator[0])
	}
	var result PendingMaterials
	if !filepath.IsAbs(python) || binding.HostSessionKey == "" || dataset == "" {
		return result, fmt.Errorf("受管会话尚未确认")
	}
	if action != "show" && action != "check" && action != "replay" && action != "discard" && action != "details" {
		return result, fmt.Errorf("待发材料操作无效")
	}
	cacheKey := coldPendingKey{
		AgentID:        binding.AgentID,
		HostSessionKey: binding.HostSessionKey,
		Dataset:        dataset,
		PluginDir:      installation.PluginDir,
		ManifestSHA:    installation.ManifestSHA,
		HelperSHA:      installation.HelperSHA,
		EnvFile:        installation.EnvFile,
		Home:           installation.Home,
	}
	if action == "show" {
		coldPendingCache.mu.Lock()
		entry, ok := coldPendingCache.entries[cacheKey]
		coldPendingCache.mu.Unlock()
		if ok {
			if coldPendingEntryUnchanged(entry) {
				return clonePendingMaterials(entry.Result), nil
			}
			coldPendingCache.mu.Lock()
			if cur, exists := coldPendingCache.entries[cacheKey]; exists && cur.Seq == entry.Seq {
				delete(coldPendingCache.entries, cacheKey)
			}
			coldPendingCache.mu.Unlock()
		}
	} else {
		coldPendingCache.mu.Lock()
		delete(coldPendingCache.entries, cacheKey)
		coldPendingCache.mu.Unlock()
	}
	if err := verifyInstallation(installation); err != nil {
		return result, err
	}
	arguments := []string{filepath.Join(installation.PluginDir, "scripts/_memlane_policy.py"), "--session-key", binding.HostSessionKey, "--dataset", dataset, "--action", action}
	if action == "show" || action == "details" {
		arguments = []string{"-c", pendingSnapshotScript, filepath.Join(installation.PluginDir, "scripts"), binding.HostSessionKey, dataset}
		if action == "details" {
			arguments = append(arguments, "details")
		}
	}
	if action == "check" {
		arguments = []string{"-c", pendingReviewScript, filepath.Join(installation.PluginDir, "scripts"), binding.HostSessionKey, dataset}
	}
	if action == "replay" || action == "discard" {
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
		coldPendingCache.mu.Lock()
		delete(coldPendingCache.entries, cacheKey)
		coldPendingCache.mu.Unlock()
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
		case "official_review_unavailable":
			message = "服务端采集结果暂时无法读取；保留原记录，未重新发送"
		case "当前 Agent 没有此项目的写入权限":
			message = result.Error
		}
		return result, fmt.Errorf("%s", message)
	}
	watch := result.ColdWatch
	result.ColdWatch = nil
	if action == "show" && result.SessionInactive && coldPendingWatchValid(watch) {
		manifestPath := filepath.Join(installation.PluginDir, ".memlane-project-access.json")
		if minfo, statErr := os.Lstat(manifestPath); statErr == nil && minfo.Mode().IsRegular() {
			stored := clonePendingMaterials(result)
			coldPendingCache.mu.Lock()
			if _, exists := coldPendingCache.entries[cacheKey]; !exists && len(coldPendingCache.entries) >= coldPendingCacheMaxEntries {
				for k := range coldPendingCache.entries {
					delete(coldPendingCache.entries, k)
					break
				}
			}
			coldPendingCache.nextSeq++
			coldPendingCache.entries[cacheKey] = coldPendingEntry{
				Seq:             coldPendingCache.nextSeq,
				Result:          stored,
				Watch:           *watch,
				ManifestPath:    manifestPath,
				ManifestSize:    minfo.Size(),
				ManifestMtimeNS: minfo.ModTime().UnixNano(),
			}
			coldPendingCache.mu.Unlock()
		} else {
			coldPendingCache.mu.Lock()
			delete(coldPendingCache.entries, cacheKey)
			coldPendingCache.mu.Unlock()
		}
	} else {
		coldPendingCache.mu.Lock()
		delete(coldPendingCache.entries, cacheKey)
		coldPendingCache.mu.Unlock()
	}
	return result, nil
}

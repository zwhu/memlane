package simpledesktop

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

type openCodeVerifyKey struct {
	PluginDir      string
	ManifestSHA    string
	HelperSHA      string
	BindingFile    string
	BindingSHA     string
	NodeExecutable string
	NodeSHA        string
}

type openCodeVerifyEntry struct {
	Dirs        []verifiedFileWatch
	Files       []verifiedFileWatch
	BindingPath string
	BindingSig  fileStatSig
	NodePath    string
	NodeSig     fileStatSig
	OutboxDir   string
	BindingsDir string
}

var openCodeVerifyCache = struct {
	mu          sync.Mutex
	entries     map[openCodeVerifyKey]openCodeVerifyEntry
	hashCount   uint64
	inflightMu  sync.Mutex
	inflightMap map[openCodeVerifyKey]*openCodeVerifyCall
}{
	entries:     map[openCodeVerifyKey]openCodeVerifyEntry{},
	inflightMap: map[openCodeVerifyKey]*openCodeVerifyCall{},
}

type openCodeVerifyCall struct {
	done  chan struct{}
	entry openCodeVerifyEntry
	err   error
}

type openCodeColdKey struct {
	AgentID        string
	ProjectID      string
	HostSessionKey string
	Dataset        string
	PluginDir      string
	ManifestSHA    string
	HelperSHA      string
	BindingFile    string
	BindingSHA     string
	NodeExecutable string
	NodeSHA        string
	TokenFile      string
	ControlURL     string
	ServiceURL     string
}

type openCodePathWatch struct {
	Path   string
	Exists bool
	Sig    fileStatSig
}

type openCodeColdEntry struct {
	Seq           uint64
	Result        PendingMaterials
	TokenWatch    openCodePathWatch
	OutboxDir     openCodePathWatch
	OutboxEntries []openCodePathWatch
	SessionWatch  openCodePathWatch
}

type openCodeColdNode struct {
	Key   openCodeColdKey
	Entry openCodeColdEntry
}

var openCodeColdCache = struct {
	mu      sync.Mutex
	nextSeq uint64
	order   *list.List
	entries map[openCodeColdKey]*list.Element
}{
	order:   list.New(),
	entries: map[openCodeColdKey]*list.Element{},
}

func openCodeColdGet(key openCodeColdKey) (openCodeColdEntry, bool) {
	openCodeColdCache.mu.Lock()
	defer openCodeColdCache.mu.Unlock()
	elem, ok := openCodeColdCache.entries[key]
	if !ok {
		return openCodeColdEntry{}, false
	}
	openCodeColdCache.order.MoveToFront(elem)
	return elem.Value.(*openCodeColdNode).Entry, true
}

func openCodeColdPut(key openCodeColdKey, entry openCodeColdEntry) {
	openCodeColdCache.mu.Lock()
	defer openCodeColdCache.mu.Unlock()
	openCodeColdCache.nextSeq++
	entry.Seq = openCodeColdCache.nextSeq
	if elem, ok := openCodeColdCache.entries[key]; ok {
		elem.Value.(*openCodeColdNode).Entry = entry
		openCodeColdCache.order.MoveToFront(elem)
		return
	}
	for len(openCodeColdCache.entries) >= coldPendingCacheMaxEntries {
		back := openCodeColdCache.order.Back()
		if back == nil {
			break
		}
		openCodeColdCache.order.Remove(back)
		delete(openCodeColdCache.entries, back.Value.(*openCodeColdNode).Key)
	}
	elem := openCodeColdCache.order.PushFront(&openCodeColdNode{Key: key, Entry: entry})
	openCodeColdCache.entries[key] = elem
}

func openCodeColdDelete(key openCodeColdKey) {
	openCodeColdCache.mu.Lock()
	defer openCodeColdCache.mu.Unlock()
	if elem, ok := openCodeColdCache.entries[key]; ok {
		openCodeColdCache.order.Remove(elem)
		delete(openCodeColdCache.entries, key)
	}
}

func openCodeColdDeleteIfSeq(key openCodeColdKey, seq uint64) {
	openCodeColdCache.mu.Lock()
	defer openCodeColdCache.mu.Unlock()
	if elem, ok := openCodeColdCache.entries[key]; ok && elem.Value.(*openCodeColdNode).Entry.Seq == seq {
		openCodeColdCache.order.Remove(elem)
		delete(openCodeColdCache.entries, key)
	}
}

func openCodeWatchPath(path string, dirAllowed bool) (openCodePathWatch, bool) {
	if !filepath.IsAbs(path) {
		return openCodePathWatch{}, false
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return openCodePathWatch{Path: path, Exists: false}, true
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return openCodePathWatch{}, false
	}
	if !dirAllowed && !info.Mode().IsRegular() {
		return openCodePathWatch{}, false
	}
	if dirAllowed && !info.IsDir() {
		return openCodePathWatch{}, false
	}
	return openCodePathWatch{Path: path, Exists: true, Sig: statSigFromInfo(info)}, true
}

func openCodeWatchUnchanged(w openCodePathWatch, dirAllowed bool) bool {
	if w.Path == "" {
		return true
	}
	info, err := os.Lstat(w.Path)
	if !w.Exists {
		return os.IsNotExist(err)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if dirAllowed && !info.IsDir() {
		return false
	}
	if !dirAllowed && !info.Mode().IsRegular() {
		return false
	}
	return statSigFromInfo(info) == w.Sig
}

func captureOpenCodeColdWatch(entry openCodeVerifyEntry, tokenFile, hostSessionKey string) (openCodeColdEntry, bool) {
	if entry.OutboxDir == "" || !filepath.IsAbs(entry.OutboxDir) {
		return openCodeColdEntry{}, false
	}
	tokenW, ok := openCodeWatchPath(tokenFile, false)
	if !ok || !tokenW.Exists || tokenW.Sig.Mode.Perm()&0077 != 0 {
		return openCodeColdEntry{}, false
	}
	outboxW, ok := openCodeWatchPath(entry.OutboxDir, true)
	if !ok {
		return openCodeColdEntry{}, false
	}
	var outboxEntries []openCodePathWatch
	if outboxW.Exists {
		items, err := os.ReadDir(entry.OutboxDir)
		if err != nil || len(items) > 128 {
			return openCodeColdEntry{}, false
		}
		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			full := filepath.Join(entry.OutboxDir, name)
			info, err := os.Lstat(full)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return openCodeColdEntry{}, false
			}
			outboxEntries = append(outboxEntries, openCodePathWatch{Path: full, Exists: true, Sig: statSigFromInfo(info)})
		}
	}
	var sessionW openCodePathWatch
	if entry.BindingsDir != "" && filepath.IsAbs(entry.BindingsDir) && hostSessionKey != "" {
		sw, swOK := openCodeWatchPath(filepath.Join(entry.BindingsDir, hostSessionKey+".json"), false)
		if !swOK {
			return openCodeColdEntry{}, false
		}
		sessionW = sw
	}
	return openCodeColdEntry{
		TokenWatch:    tokenW,
		OutboxDir:     outboxW,
		OutboxEntries: outboxEntries,
		SessionWatch:  sessionW,
	}, true
}

func openCodeColdEntryUnchanged(cached openCodeColdEntry) bool {
	if !openCodeWatchUnchanged(cached.TokenWatch, false) {
		return false
	}
	if !openCodeWatchUnchanged(cached.OutboxDir, true) {
		return false
	}
	if cached.OutboxDir.Exists {
		items, err := os.ReadDir(cached.OutboxDir.Path)
		if err != nil || len(items) != len(cached.OutboxEntries) {
			return false
		}
		for _, ew := range cached.OutboxEntries {
			info, err := os.Lstat(ew.Path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || statSigFromInfo(info) != ew.Sig {
				return false
			}
		}
	}
	if cached.SessionWatch.Path != "" && !openCodeWatchUnchanged(cached.SessionWatch, false) {
		return false
	}
	return true
}

func openCodeVerifyEntryUnchanged(entry openCodeVerifyEntry) bool {
	if len(entry.Dirs) == 0 || len(entry.Files) == 0 {
		return false
	}
	for _, d := range entry.Dirs {
		sig, ok := lstatEntrySig(d.Path)
		if !ok || !sig.Mode.IsDir() || sig != d.Sig {
			return false
		}
	}
	for _, f := range entry.Files {
		sig, ok := lstatRegularSig(f.Path)
		if !ok || sig != f.Sig {
			return false
		}
	}
	bsig, ok := lstatRegularSig(entry.BindingPath)
	if !ok || bsig != entry.BindingSig || bsig.Mode.Perm()&0077 != 0 {
		return false
	}
	nsig, ok := lstatRegularSig(entry.NodePath)
	if !ok || nsig != entry.NodeSig {
		return false
	}
	return true
}

func verifyOpenCodePending(installation AgentInstallation) error {
	_, err := verifyOpenCodePendingMode(installation, false)
	return err
}

func verifyOpenCodePendingMode(installation AgentInstallation, forceFull bool) (openCodeVerifyEntry, error) {
	key := openCodeVerifyKey{
		PluginDir:      installation.PluginDir,
		ManifestSHA:    installation.ManifestSHA,
		HelperSHA:      installation.HelperSHA,
		BindingFile:    installation.BindingFile,
		BindingSHA:     installation.BindingSHA,
		NodeExecutable: installation.NodeExecutable,
		NodeSHA:        installation.NodeSHA,
	}
	if !forceFull {
		openCodeVerifyCache.mu.Lock()
		entry, ok := openCodeVerifyCache.entries[key]
		openCodeVerifyCache.mu.Unlock()
		if ok {
			if openCodeVerifyEntryUnchanged(entry) {
				return entry, nil
			}
			openCodeVerifyCache.mu.Lock()
			delete(openCodeVerifyCache.entries, key)
			openCodeVerifyCache.mu.Unlock()
		}
	}
	openCodeVerifyCache.inflightMu.Lock()
	if !forceFull {
		if call, ok := openCodeVerifyCache.inflightMap[key]; ok {
			openCodeVerifyCache.inflightMu.Unlock()
			<-call.done
			return call.entry, call.err
		}
	}
	call := &openCodeVerifyCall{done: make(chan struct{})}
	if !forceFull {
		openCodeVerifyCache.inflightMap[key] = call
	}
	openCodeVerifyCache.inflightMu.Unlock()

	entry, err := verifyOpenCodePendingFull(installation)
	if err == nil {
		openCodeVerifyCache.mu.Lock()
		if len(openCodeVerifyCache.entries) >= verifiedInstallCacheMax {
			clear(openCodeVerifyCache.entries)
		}
		openCodeVerifyCache.entries[key] = entry
		openCodeVerifyCache.mu.Unlock()
	} else {
		openCodeVerifyCache.mu.Lock()
		delete(openCodeVerifyCache.entries, key)
		openCodeVerifyCache.mu.Unlock()
	}
	call.entry, call.err = entry, err
	close(call.done)
	if !forceFull {
		openCodeVerifyCache.inflightMu.Lock()
		if openCodeVerifyCache.inflightMap[key] == call {
			delete(openCodeVerifyCache.inflightMap, key)
		}
		openCodeVerifyCache.inflightMu.Unlock()
	}
	return entry, err
}

func verifyOpenCodePendingFull(installation AgentInstallation) (openCodeVerifyEntry, error) {
	var entry openCodeVerifyEntry
	root := installation.PluginDir
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || canonical != root {
		return entry, fmt.Errorf("unconfirmed component")
	}
	path := filepath.Join(root, ".memlane-opencode-artifact.json")
	digest, err := pendingFileHash(path, 8*1024*1024, false)
	if err != nil || digest != installation.ManifestSHA {
		return entry, fmt.Errorf("unconfirmed component")
	}
	manifestSig, ok := lstatRegularSig(path)
	if !ok {
		return entry, fmt.Errorf("unconfirmed component")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return entry, err
	}
	var manifest struct {
		Protocol int               `json:"protocol"`
		Files    map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Protocol != 1 || manifest.Files["dist/src/managed-pending-cli.js"] == "" || manifest.Files["dist/src/managed-pending-cli.js"] != installation.HelperSHA {
		return entry, fmt.Errorf("unconfirmed worker")
	}
	entry.Files = append(entry.Files, verifiedFileWatch{Path: path, Sig: manifestSig})
	found := map[string]bool{}
	err = filepath.WalkDir(root, func(p string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("component symlink")
		}
		if dirEntry.IsDir() {
			dsig, dok := lstatEntrySig(p)
			if !dok {
				return fmt.Errorf("component dir unconfirmed")
			}
			entry.Dirs = append(entry.Dirs, verifiedFileWatch{Path: p, Sig: dsig})
			return nil
		}
		if p == path {
			return nil
		}
		name, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		expected, ok := manifest.Files[name]
		if !ok {
			return fmt.Errorf("extra component file")
		}
		actual, err := pendingFileHash(p, 64*1024*1024, false)
		if err != nil || actual != expected {
			return fmt.Errorf("changed component file")
		}
		fsig, fok := lstatRegularSig(p)
		if !fok {
			return fmt.Errorf("changed component file")
		}
		entry.Files = append(entry.Files, verifiedFileWatch{Path: p, Sig: fsig})
		found[name] = true
		return nil
	})
	if err != nil || len(found) != len(manifest.Files) {
		return entry, fmt.Errorf("unconfirmed component tree")
	}
	digest, err = pendingFileHash(installation.BindingFile, 65536, true)
	if err != nil || digest != installation.BindingSHA {
		return entry, fmt.Errorf("unconfirmed binding")
	}
	bindingSig, ok := lstatRegularSig(installation.BindingFile)
	if !ok {
		return entry, fmt.Errorf("unconfirmed binding")
	}
	digest, err = pendingFileHash(installation.NodeExecutable, 512*1024*1024, false)
	if err != nil || digest != installation.NodeSHA {
		return entry, fmt.Errorf("unconfirmed runtime")
	}
	nodeSig, ok := lstatRegularSig(installation.NodeExecutable)
	if !ok {
		return entry, fmt.Errorf("unconfirmed runtime")
	}
	entry.BindingPath, entry.BindingSig = installation.BindingFile, bindingSig
	entry.NodePath, entry.NodeSig = installation.NodeExecutable, nodeSig
	if rawBinding, bErr := os.ReadFile(installation.BindingFile); bErr == nil {
		var parsed struct {
			Memlane struct {
				StateDir string `json:"stateDir"`
			} `json:"memlane"`
			Cognee struct {
				StateDir string `json:"stateDir"`
			} `json:"cognee"`
		}
		if json.Unmarshal(rawBinding, &parsed) == nil {
			if filepath.IsAbs(parsed.Cognee.StateDir) {
				entry.OutboxDir = parsed.Cognee.StateDir
			}
			if filepath.IsAbs(parsed.Memlane.StateDir) {
				entry.BindingsDir = parsed.Memlane.StateDir
			}
		}
	}
	openCodeVerifyCache.mu.Lock()
	openCodeVerifyCache.hashCount++
	openCodeVerifyCache.mu.Unlock()
	return entry, nil
}

func runOpenCodePending(ctx context.Context, installation AgentInstallation, binding ProjectBinding, dataset, action, signature string, operator PendingOperatorConfig) (PendingMaterials, error) {
	var result PendingMaterials
	if action != "show" && action != "check" && action != "replay" && action != "discard" {
		return result, fmt.Errorf("待处理记录操作无效")
	}
	if !filepath.IsAbs(operator.TokenFile) || operator.ControlURL == "" || operator.ServiceURL == "" {
		return result, fmt.Errorf("管理连接尚未确认，未更改记录")
	}
	forceVerify := action != "show"
	verifiedEntry, err := verifyOpenCodePendingMode(installation, forceVerify)
	if err != nil {
		return result, fmt.Errorf("连接组件需要重新核对，保留原记录")
	}
	coldKey := openCodeColdKey{
		AgentID:        binding.AgentID,
		ProjectID:      binding.ProjectID,
		HostSessionKey: binding.HostSessionKey,
		Dataset:        dataset,
		PluginDir:      installation.PluginDir,
		ManifestSHA:    installation.ManifestSHA,
		HelperSHA:      installation.HelperSHA,
		BindingFile:    installation.BindingFile,
		BindingSHA:     installation.BindingSHA,
		NodeExecutable: installation.NodeExecutable,
		NodeSHA:        installation.NodeSHA,
		TokenFile:      operator.TokenFile,
		ControlURL:     operator.ControlURL,
		ServiceURL:     operator.ServiceURL,
	}
	if action == "show" {
		if cached, ok := openCodeColdGet(coldKey); ok {
			if openCodeColdEntryUnchanged(cached) {
				return clonePendingMaterials(cached.Result), nil
			}
			openCodeColdDeleteIfSeq(coldKey, cached.Seq)
		}
	} else {
		openCodeColdDelete(coldKey)
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
		openCodeColdDelete(coldKey)
		return result, fmt.Errorf("原操作结果未确认，保留原记录；不会自动重试")
	}
	if err != nil || result.Error != "" {
		openCodeColdDelete(coldKey)
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
		openCodeColdDelete(coldKey)
		return result, fmt.Errorf("采集状态未确认，保留原记录")
	}
	if action == "show" && result.Pending == 0 && result.Unknown == 0 {
		if watchEntry, ok := captureOpenCodeColdWatch(verifiedEntry, operator.TokenFile, binding.HostSessionKey); ok {
			watchEntry.Result = clonePendingMaterials(result)
			openCodeColdPut(coldKey, watchEntry)
		}
	} else {
		openCodeColdDelete(coldKey)
	}
	return result, nil
}

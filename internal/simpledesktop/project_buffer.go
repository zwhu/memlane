package simpledesktop

import (
	"bytes"
	"container/list"
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
	"syscall"
	"time"
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
	ManifestIno     uint64
	BridgeIno       uint64
}

type coldPendingNode struct {
	Key   coldPendingKey
	Entry coldPendingEntry
}

const coldPendingCacheMaxEntries = 65536

var coldPendingCache = struct {
	mu      sync.Mutex
	nextSeq uint64
	order   *list.List
	entries map[coldPendingKey]*list.Element
}{
	order:   list.New(),
	entries: map[coldPendingKey]*list.Element{},
}

func coldPendingCacheGet(key coldPendingKey) (coldPendingEntry, bool) {
	coldPendingCache.mu.Lock()
	defer coldPendingCache.mu.Unlock()
	elem, ok := coldPendingCache.entries[key]
	if !ok {
		return coldPendingEntry{}, false
	}
	coldPendingCache.order.MoveToFront(elem)
	return elem.Value.(*coldPendingNode).Entry, true
}

func coldPendingCachePut(key coldPendingKey, entry coldPendingEntry) {
	coldPendingCache.mu.Lock()
	coldPendingCache.nextSeq++
	entry.Seq = coldPendingCache.nextSeq
	if elem, exists := coldPendingCache.entries[key]; exists {
		elem.Value.(*coldPendingNode).Entry = entry
		coldPendingCache.order.MoveToFront(elem)
		coldPendingCache.mu.Unlock()
		if isPersistableCleanColdResult(entry.Result) && entry.Result.SessionInactive {
			scheduleColdPendingFlush()
		}
		return
	}
	for len(coldPendingCache.entries) >= coldPendingCacheMaxEntries {
		back := coldPendingCache.order.Back()
		if back == nil {
			break
		}
		coldPendingCache.order.Remove(back)
		delete(coldPendingCache.entries, back.Value.(*coldPendingNode).Key)
	}
	elem := coldPendingCache.order.PushFront(&coldPendingNode{Key: key, Entry: entry})
	coldPendingCache.entries[key] = elem
	coldPendingCache.mu.Unlock()
	if isPersistableCleanColdResult(entry.Result) && entry.Result.SessionInactive {
		scheduleColdPendingFlush()
	}
}

func coldPendingCacheDelete(key coldPendingKey) {
	coldPendingCache.mu.Lock()
	deleted := false
	if elem, ok := coldPendingCache.entries[key]; ok {
		coldPendingCache.order.Remove(elem)
		delete(coldPendingCache.entries, key)
		deleted = true
	}
	coldPendingCache.mu.Unlock()
	if deleted {
		scheduleColdPendingFlush()
	}
}

func coldPendingCacheDeleteIfSeq(key coldPendingKey, seq uint64) {
	coldPendingCache.mu.Lock()
	deleted := false
	if elem, ok := coldPendingCache.entries[key]; ok && elem.Value.(*coldPendingNode).Entry.Seq == seq {
		coldPendingCache.order.Remove(elem)
		delete(coldPendingCache.entries, key)
		deleted = true
	}
	coldPendingCache.mu.Unlock()
	if deleted {
		scheduleColdPendingFlush()
	}
}

type fileStatSig struct {
	Mode    os.FileMode
	Size    int64
	MtimeNS int64
	Ino     uint64
	Dev     int64
}

func statSigFromInfo(info os.FileInfo) fileStatSig {
	sig := fileStatSig{
		Mode:    info.Mode(),
		Size:    info.Size(),
		MtimeNS: info.ModTime().UnixNano(),
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		sig.Ino = st.Ino
		sig.Dev = int64(st.Dev)
	}
	return sig
}

func lstatRegularSig(path string) (fileStatSig, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fileStatSig{}, false
	}
	return statSigFromInfo(info), true
}

func lstatEntrySig(path string) (fileStatSig, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return fileStatSig{}, false
	}
	return statSigFromInfo(info), true
}

type verifiedInstallKey struct {
	PluginDir   string
	EnvFile     string
	Home        string
	ManifestSHA string
	HelperSHA   string
}

type verifiedFileWatch struct {
	Path string
	Sig  fileStatSig
}

type verifiedInstallEntry struct {
	Files []verifiedFileWatch
}

const verifiedInstallCacheMax = 256

var verifiedInstallCache = struct {
	mu          sync.Mutex
	entries     map[verifiedInstallKey]verifiedInstallEntry
	hashCount   uint64
	inflightMu  sync.Mutex
	inflightMap map[verifiedInstallKey]*verifyCall
}{
	entries:     map[verifiedInstallKey]verifiedInstallEntry{},
	inflightMap: map[verifiedInstallKey]*verifyCall{},
}

type verifyCall struct {
	done chan struct{}
	err  error
}

func verifiedInstallFilesUnchanged(watches []verifiedFileWatch) bool {
	if len(watches) == 0 {
		return false
	}
	for _, w := range watches {
		sig, ok := lstatRegularSig(w.Path)
		if !ok || sig != w.Sig {
			return false
		}
	}
	return true
}

func installKeyFor(installation AgentInstallation) verifiedInstallKey {
	return verifiedInstallKey{
		PluginDir:   installation.PluginDir,
		EnvFile:     installation.EnvFile,
		Home:        installation.Home,
		ManifestSHA: installation.ManifestSHA,
		HelperSHA:   installation.HelperSHA,
	}
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
	if entry.ManifestIno != 0 && statSigFromInfo(minfo).Ino != entry.ManifestIno {
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
	if entry.BridgeIno != 0 && statSigFromInfo(binfo).Ino != entry.BridgeIno {
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
	return verifyInstallationMode(installation, false)
}

func verifyInstallationMode(installation AgentInstallation, forceFull bool) error {
	root := installation.PluginDir
	if !filepath.IsAbs(root) || !filepath.IsAbs(installation.EnvFile) || !filepath.IsAbs(installation.Home) {
		return fmt.Errorf("受管插件配置尚未确认")
	}
	key := installKeyFor(installation)
	if !forceFull {
		verifiedInstallCache.mu.Lock()
		entry, ok := verifiedInstallCache.entries[key]
		verifiedInstallCache.mu.Unlock()
		if ok {
			if verifiedInstallFilesUnchanged(entry.Files) {
				return nil
			}
			verifiedInstallCache.mu.Lock()
			delete(verifiedInstallCache.entries, key)
			verifiedInstallCache.mu.Unlock()
		}
	}
	// Coalesce concurrent verification calls for the same Agent installation so
	// parallel queue workers compute SHA256 at most once per Agent.
	verifiedInstallCache.inflightMu.Lock()
	if !forceFull {
		if call, ok := verifiedInstallCache.inflightMap[key]; ok {
			verifiedInstallCache.inflightMu.Unlock()
			<-call.done
			return call.err
		}
	}
	call := &verifyCall{done: make(chan struct{})}
	if !forceFull {
		verifiedInstallCache.inflightMap[key] = call
	}
	verifiedInstallCache.inflightMu.Unlock()

	watches, err := verifyInstallationFull(installation)
	if err == nil {
		verifiedInstallCache.mu.Lock()
		if len(verifiedInstallCache.entries) >= verifiedInstallCacheMax {
			clear(verifiedInstallCache.entries)
		}
		verifiedInstallCache.entries[key] = verifiedInstallEntry{Files: watches}
		verifiedInstallCache.mu.Unlock()
	} else {
		verifiedInstallCache.mu.Lock()
		delete(verifiedInstallCache.entries, key)
		verifiedInstallCache.mu.Unlock()
	}

	call.err = err
	close(call.done)
	if !forceFull {
		verifiedInstallCache.inflightMu.Lock()
		if verifiedInstallCache.inflightMap[key] == call {
			delete(verifiedInstallCache.inflightMap, key)
		}
		verifiedInstallCache.inflightMu.Unlock()
	}
	return err
}

func verifyInstallationFull(installation AgentInstallation) ([]verifiedFileWatch, error) {
	root := installation.PluginDir
	receiptPath := filepath.Join(root, ".memlane-project-access.json")
	receiptInfo, err := os.Lstat(receiptPath)
	if err != nil || !receiptInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("受管插件输入发生变化，请重新核对")
	}
	receipt, err := os.ReadFile(receiptPath)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(receipt)) != installation.ManifestSHA {
		return nil, fmt.Errorf("受管插件输入发生变化，请重新核对")
	}
	var input struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(receipt, &input) != nil || input.Files["scripts/_memlane_policy.py"] == "" || input.Files["scripts/_plugin_common.py"] == "" {
		return nil, fmt.Errorf("受管插件输入未确认")
	}
	if installation.HelperSHA != "" && input.Files["scripts/_memlane_policy.py"] != installation.HelperSHA {
		return nil, fmt.Errorf("受管插件输入未确认")
	}
	watches := make([]verifiedFileWatch, 0, len(input.Files)+1)
	watches = append(watches, verifiedFileWatch{Path: receiptPath, Sig: statSigFromInfo(receiptInfo)})
	for name, expected := range input.Files {
		if filepath.IsAbs(name) || strings.HasPrefix(filepath.Clean(name), "..") {
			return nil, fmt.Errorf("受管插件文件路径无效")
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("受管插件文件未确认")
		}
		body, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(body)) != expected {
			return nil, fmt.Errorf("受管插件文件发生变化，请重新核对")
		}
		watches = append(watches, verifiedFileWatch{Path: path, Sig: statSigFromInfo(info)})
	}
	verifiedInstallCache.mu.Lock()
	verifiedInstallCache.hashCount++
	verifiedInstallCache.mu.Unlock()
	return watches, nil
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
	// Always verify the installation integrity BEFORE consulting the cold cache
	// (deduplicated per Agent via verifiedInstallCache) so tampered/revoked
	// installations never serve cached results.
	forceVerify := action == "replay" || action == "discard"
	if err := verifyInstallationMode(installation, forceVerify); err != nil {
		coldPendingCacheDelete(cacheKey)
		return result, err
	}
	if action == "show" {
		waitColdPendingStoreLoad(200 * time.Millisecond)
		if entry, ok := coldPendingCacheGet(cacheKey); ok {
			if coldPendingEntryUnchanged(entry) {
				return clonePendingMaterials(entry.Result), nil
			}
			coldPendingCacheDeleteIfSeq(cacheKey, entry.Seq)
		}
	} else {
		coldPendingCacheDelete(cacheKey)
	}
	manifestPath := filepath.Join(installation.PluginDir, ".memlane-project-access.json")
	preManifestSig, preManifestOK := lstatRegularSig(manifestPath)
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
		coldPendingCacheDelete(cacheKey)
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
	if action == "show" && result.SessionInactive && coldPendingWatchValid(watch) && preManifestOK {
		if minfo, statErr := os.Lstat(manifestPath); statErr == nil && minfo.Mode().IsRegular() && statSigFromInfo(minfo) == preManifestSig {
			var bridgeIno uint64
			bridgeStable := false
			if _, mapErr := os.Lstat(watch.MapPath); os.IsNotExist(mapErr) {
				if !watch.BridgeExists {
					_, bErr := os.Lstat(watch.BridgePath)
					bridgeStable = os.IsNotExist(bErr)
				} else if binfo, bErr := os.Lstat(watch.BridgePath); bErr == nil && binfo.Mode().IsRegular() &&
					binfo.Size() == watch.BridgeSize && binfo.ModTime().UnixNano() == watch.BridgeMtimeNS {
					bridgeIno = statSigFromInfo(binfo).Ino
					bridgeStable = true
				}
			}
			if bridgeStable {
				stored := clonePendingMaterials(result)
				coldPendingCachePut(cacheKey, coldPendingEntry{
					Result:          stored,
					Watch:           *watch,
					ManifestPath:    manifestPath,
					ManifestSize:    minfo.Size(),
					ManifestMtimeNS: minfo.ModTime().UnixNano(),
					ManifestIno:     statSigFromInfo(minfo).Ino,
					BridgeIno:       bridgeIno,
				})
			} else {
				coldPendingCacheDelete(cacheKey)
			}
		} else {
			coldPendingCacheDelete(cacheKey)
		}
	} else {
		coldPendingCacheDelete(cacheKey)
	}
	return result, nil
}

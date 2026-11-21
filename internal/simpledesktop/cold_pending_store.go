package simpledesktop

import (
	"container/list"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	coldPendingWatchSchemaVersion = 1
	coldPendingWatchMaxBytes      = 32 * 1024 * 1024
)

// persistedColdPendingRecord stores ONLY stat/watch metadata and clean empty-queue
// status signatures for cold inactive sessions. Never persists message bodies,
// details, summaries, unknown entry IDs, or tokens.
type persistedColdPendingRecord struct {
	Key             coldPendingKey   `json:"key"`
	Signature       string           `json:"signature"`
	SessionID       string           `json:"session_id,omitempty"`
	SessionInactive bool             `json:"session_inactive"`
	Watch           coldPendingWatch `json:"watch"`
	ManifestPath    string           `json:"manifest_path"`
	ManifestSize    int64            `json:"manifest_size"`
	ManifestMtimeNS int64            `json:"manifest_mtime_ns"`
	ManifestIno     uint64           `json:"manifest_ino,omitempty"`
	BridgeIno       uint64           `json:"bridge_ino,omitempty"`
}

type persistedOpenCodeColdRecord struct {
	Key           openCodeColdKey     `json:"key"`
	Signature     string              `json:"signature"`
	SessionID     string              `json:"session_id,omitempty"`
	TokenWatch    openCodePathWatch   `json:"token_watch"`
	OutboxDir     openCodePathWatch   `json:"outbox_dir"`
	OutboxEntries []openCodePathWatch `json:"outbox_entries,omitempty"`
	SessionWatch  openCodePathWatch   `json:"session_watch"`
}

type persistedColdPendingSnapshot struct {
	SchemaVersion int                             `json:"schema_version"`
	Standard      []persistedColdPendingRecord    `json:"standard,omitempty"`
	OpenCode      []persistedOpenCodeColdRecord   `json:"opencode,omitempty"`
}

type coldPendingDiskStore struct {
	mu         sync.Mutex
	fileMu     sync.Mutex
	cond       *sync.Cond
	path       string
	loadedPath string
	debounce   time.Duration
	dirty      bool
	timer      *time.Timer
	flushing   bool
	writeCount uint64
}

var coldPendingStore = func() *coldPendingDiskStore {
	s := &coldPendingDiskStore{
		debounce: 500 * time.Millisecond,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}()

func coldPendingWatchPathForConfig(cfg Config) string {
	base := cfg.ClientStateFile
	if base == "" {
		base = cfg.ConfigFile
	}
	if base == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(base), "cold-pending-watch.json")
}

func (a *App) configureColdPendingStoreFromConfig() {
	path := coldPendingWatchPathForConfig(a.Config)
	if path != "" {
		configureColdPendingStore(path)
	}
}

func configureColdPendingStore(path string) {
	coldPendingStore.mu.Lock()
	if coldPendingStore.path == path && coldPendingStore.loadedPath == path {
		coldPendingStore.mu.Unlock()
		return
	}
	if coldPendingStore.timer != nil {
		coldPendingStore.timer.Stop()
		coldPendingStore.timer = nil
	}
	coldPendingStore.path = path
	coldPendingStore.dirty = false
	coldPendingStore.loadedPath = ""
	coldPendingStore.mu.Unlock()

	if path != "" {
		loadColdPendingStore(path)
	}
}

func resetColdPendingCachesForTest() {
	coldPendingCache.mu.Lock()
	coldPendingCache.order.Init()
	coldPendingCache.entries = map[coldPendingKey]*list.Element{}
	coldPendingCache.nextSeq = 0
	coldPendingCache.mu.Unlock()

	openCodeColdCache.mu.Lock()
	openCodeColdCache.order.Init()
	openCodeColdCache.entries = map[openCodeColdKey]*list.Element{}
	openCodeColdCache.nextSeq = 0
	openCodeColdCache.mu.Unlock()

	coldPendingStore.mu.Lock()
	coldPendingStore.loadedPath = ""
	coldPendingStore.mu.Unlock()
}

func isPersistableCleanColdResult(r PendingMaterials) bool {
	return r.Pending == 0 && r.Unknown == 0 && r.RetryableUnknown == 0 &&
		r.Blocked == nil && len(r.Details) == 0 && len(r.UnknownEntryIDs) == 0 &&
		r.Error == "" && r.Signature != ""
}

func loadColdPendingStore(path string) {
	coldPendingStore.mu.Lock()
	if path == "" || coldPendingStore.loadedPath == path {
		coldPendingStore.mu.Unlock()
		return
	}
	coldPendingStore.loadedPath = path
	coldPendingStore.mu.Unlock()

	coldPendingStore.fileMu.Lock()
	defer coldPendingStore.fileMu.Unlock()

	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > coldPendingWatchMaxBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snap persistedColdPendingSnapshot
	if json.Unmarshal(data, &snap) != nil || snap.SchemaVersion != coldPendingWatchSchemaVersion {
		return
	}

	// Load in reverse MRU order so MRU items end up at the front of the LRU list.
	if len(snap.Standard) > 0 {
		limit := min(len(snap.Standard), coldPendingCacheMaxEntries)
		coldPendingCache.mu.Lock()
		for i := limit - 1; i >= 0; i-- {
			rec := snap.Standard[i]
			if rec.Signature == "" || !rec.SessionInactive || !coldPendingWatchValid(&rec.Watch) || !filepath.IsAbs(rec.ManifestPath) {
				continue
			}
			if _, exists := coldPendingCache.entries[rec.Key]; exists {
				continue
			}
			if len(coldPendingCache.entries) >= coldPendingCacheMaxEntries {
				continue
			}
			coldPendingCache.nextSeq++
			entry := coldPendingEntry{
				Seq: coldPendingCache.nextSeq,
				Result: PendingMaterials{
					Pending:         0,
					Unknown:         0,
					Signature:       rec.Signature,
					SessionID:       rec.SessionID,
					SessionInactive: true,
				},
				Watch:           rec.Watch,
				ManifestPath:    rec.ManifestPath,
				ManifestSize:    rec.ManifestSize,
				ManifestMtimeNS: rec.ManifestMtimeNS,
				ManifestIno:     rec.ManifestIno,
				BridgeIno:       rec.BridgeIno,
			}
			elem := coldPendingCache.order.PushFront(&coldPendingNode{Key: rec.Key, Entry: entry})
			coldPendingCache.entries[rec.Key] = elem
		}
		coldPendingCache.mu.Unlock()
	}

	if len(snap.OpenCode) > 0 {
		limit := min(len(snap.OpenCode), coldPendingCacheMaxEntries)
		openCodeColdCache.mu.Lock()
		for i := limit - 1; i >= 0; i-- {
			rec := snap.OpenCode[i]
			if rec.Signature == "" || !filepath.IsAbs(rec.TokenWatch.Path) || !filepath.IsAbs(rec.OutboxDir.Path) {
				continue
			}
			if _, exists := openCodeColdCache.entries[rec.Key]; exists {
				continue
			}
			if len(openCodeColdCache.entries) >= coldPendingCacheMaxEntries {
				continue
			}
			openCodeColdCache.nextSeq++
			entry := openCodeColdEntry{
				Seq: openCodeColdCache.nextSeq,
				Result: PendingMaterials{
					Pending:   0,
					Unknown:   0,
					Signature: rec.Signature,
					SessionID: rec.SessionID,
				},
				TokenWatch:    rec.TokenWatch,
				OutboxDir:     rec.OutboxDir,
				OutboxEntries: rec.OutboxEntries,
				SessionWatch:  rec.SessionWatch,
			}
			elem := openCodeColdCache.order.PushFront(&openCodeColdNode{Key: rec.Key, Entry: entry})
			openCodeColdCache.entries[rec.Key] = elem
		}
		openCodeColdCache.mu.Unlock()
	}
}

func scheduleColdPendingFlush() {
	coldPendingStore.mu.Lock()
	if coldPendingStore.path == "" {
		coldPendingStore.mu.Unlock()
		return
	}
	coldPendingStore.dirty = true
	if coldPendingStore.timer != nil {
		coldPendingStore.timer.Stop()
	}
	delay := coldPendingStore.debounce
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	coldPendingStore.timer = time.AfterFunc(delay, func() {
		flushColdPendingStoreNow()
	})
	coldPendingStore.mu.Unlock()
}

func waitColdPendingStoreFlush() {
	coldPendingStore.mu.Lock()
	for coldPendingStore.dirty || coldPendingStore.flushing || coldPendingStore.timer != nil {
		coldPendingStore.cond.Wait()
	}
	coldPendingStore.mu.Unlock()
}

func flushColdPendingStoreNow() {
	coldPendingStore.mu.Lock()
	if coldPendingStore.timer != nil {
		coldPendingStore.timer.Stop()
		coldPendingStore.timer = nil
	}
	path := coldPendingStore.path
	if path == "" || !coldPendingStore.dirty {
		coldPendingStore.dirty = false
		coldPendingStore.cond.Broadcast()
		coldPendingStore.mu.Unlock()
		return
	}
	if coldPendingStore.flushing {
		coldPendingStore.mu.Unlock()
		return
	}
	coldPendingStore.dirty = false
	coldPendingStore.flushing = true
	coldPendingStore.mu.Unlock()

	for {
		snap := buildColdPendingSnapshot()
		_ = writeColdPendingSnapshotAtomic(path, snap)

		coldPendingStore.mu.Lock()
		coldPendingStore.writeCount++
		if !coldPendingStore.dirty || coldPendingStore.path != path {
			coldPendingStore.flushing = false
			coldPendingStore.cond.Broadcast()
			coldPendingStore.mu.Unlock()
			return
		}
		coldPendingStore.dirty = false
		coldPendingStore.mu.Unlock()
	}
}

func buildColdPendingSnapshot() persistedColdPendingSnapshot {
	snap := persistedColdPendingSnapshot{SchemaVersion: coldPendingWatchSchemaVersion}

	coldPendingCache.mu.Lock()
	for elem := coldPendingCache.order.Front(); elem != nil && len(snap.Standard) < coldPendingCacheMaxEntries; elem = elem.Next() {
		node := elem.Value.(*coldPendingNode)
		if !node.Entry.Result.SessionInactive || !isPersistableCleanColdResult(node.Entry.Result) || !coldPendingWatchValid(&node.Entry.Watch) {
			continue
		}
		snap.Standard = append(snap.Standard, persistedColdPendingRecord{
			Key:             node.Key,
			Signature:       node.Entry.Result.Signature,
			SessionID:       node.Entry.Result.SessionID,
			SessionInactive: true,
			Watch:           node.Entry.Watch,
			ManifestPath:    node.Entry.ManifestPath,
			ManifestSize:    node.Entry.ManifestSize,
			ManifestMtimeNS: node.Entry.ManifestMtimeNS,
			ManifestIno:     node.Entry.ManifestIno,
			BridgeIno:       node.Entry.BridgeIno,
		})
	}
	coldPendingCache.mu.Unlock()

	openCodeColdCache.mu.Lock()
	for elem := openCodeColdCache.order.Front(); elem != nil && len(snap.OpenCode) < coldPendingCacheMaxEntries; elem = elem.Next() {
		node := elem.Value.(*openCodeColdNode)
		if !isPersistableCleanColdResult(node.Entry.Result) || !filepath.IsAbs(node.Entry.TokenWatch.Path) || !filepath.IsAbs(node.Entry.OutboxDir.Path) {
			continue
		}
		entriesCopy := append([]openCodePathWatch(nil), node.Entry.OutboxEntries...)
		snap.OpenCode = append(snap.OpenCode, persistedOpenCodeColdRecord{
			Key:           node.Key,
			Signature:     node.Entry.Result.Signature,
			SessionID:     node.Entry.Result.SessionID,
			TokenWatch:    node.Entry.TokenWatch,
			OutboxDir:     node.Entry.OutboxDir,
			OutboxEntries: entriesCopy,
			SessionWatch:  node.Entry.SessionWatch,
		})
	}
	openCodeColdCache.mu.Unlock()

	return snap
}

func writeColdPendingSnapshotAtomic(path string, snap persistedColdPendingSnapshot) error {
	coldPendingStore.fileMu.Lock()
	defer coldPendingStore.fileMu.Unlock()

	data, err := json.Marshal(snap)
	if err != nil || len(data) > coldPendingWatchMaxBytes {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".cold-pending-watch-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

package simpledesktop

import (
	"container/list"
	"encoding/json"
	"fmt"
	"io"
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
	mu           sync.Mutex
	fileMu       sync.Mutex
	cond         *sync.Cond
	path         string
	loadedPath   string
	loadDone     chan struct{}
	debounce     time.Duration
	maxDelay     time.Duration
	dirty        bool
	firstDirtyAt time.Time
	timer        *time.Timer
	flushing     bool
	writeCount   uint64
	lastWriteErr error
}

var coldPendingStore = func() *coldPendingDiskStore {
	s := &coldPendingDiskStore{
		debounce: 500 * time.Millisecond,
		maxDelay: 5 * time.Second,
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
		configureColdPendingStoreAsync(path)
	}
}

func configureColdPendingStoreAsync(path string) chan struct{} {
	coldPendingStore.mu.Lock()
	if coldPendingStore.path == path && coldPendingStore.loadedPath == path {
		done := coldPendingStore.loadDone
		coldPendingStore.mu.Unlock()
		return done
	}
	if coldPendingStore.timer != nil {
		coldPendingStore.timer.Stop()
		coldPendingStore.timer = nil
	}
	coldPendingStore.path = path
	coldPendingStore.dirty = false
	coldPendingStore.firstDirtyAt = time.Time{}
	coldPendingStore.loadedPath = ""
	if path == "" {
		coldPendingStore.loadDone = nil
		coldPendingStore.cond.Broadcast()
		coldPendingStore.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	coldPendingStore.loadDone = done
	coldPendingStore.mu.Unlock()

	go func() {
		defer close(done)
		loadColdPendingStore(path)
	}()
	return done
}

func waitColdPendingStoreLoad(timeout time.Duration) {
	coldPendingStore.mu.Lock()
	done := coldPendingStore.loadDone
	coldPendingStore.mu.Unlock()
	if done == nil {
		return
	}
	if timeout <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func configureColdPendingStore(path string) {
	done := configureColdPendingStoreAsync(path)
	if done != nil {
		<-done
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
	coldPendingStore.loadDone = nil
	coldPendingStore.lastWriteErr = nil
	coldPendingStore.firstDirtyAt = time.Time{}
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

	linfo, err := os.Lstat(path)
	if err != nil {
		return
	}
	if !linfo.Mode().IsRegular() || linfo.Mode()&os.ModeSymlink != 0 || linfo.Mode().Perm()&0077 != 0 || linfo.Size() <= 0 || linfo.Size() > coldPendingWatchMaxBytes {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	finfo, err := f.Stat()
	if err != nil || !os.SameFile(linfo, finfo) || !finfo.Mode().IsRegular() || finfo.Mode().Perm()&0077 != 0 || finfo.Size() <= 0 || finfo.Size() > coldPendingWatchMaxBytes {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, coldPendingWatchMaxBytes+1))
	if err != nil || int64(len(data)) > coldPendingWatchMaxBytes {
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
	now := time.Now()
	coldPendingStore.mu.Lock()
	if coldPendingStore.path == "" {
		coldPendingStore.mu.Unlock()
		return
	}
	if !coldPendingStore.dirty || coldPendingStore.firstDirtyAt.IsZero() {
		coldPendingStore.firstDirtyAt = now
	}
	coldPendingStore.dirty = true
	if coldPendingStore.timer != nil {
		coldPendingStore.timer.Stop()
		coldPendingStore.timer = nil
	}
	delay := coldPendingStore.debounce
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	maxDelay := coldPendingStore.maxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	remaining := maxDelay - now.Sub(coldPendingStore.firstDirtyAt)
	if remaining <= 0 {
		coldPendingStore.mu.Unlock()
		go flushColdPendingStoreNow()
		return
	}
	if remaining < delay {
		delay = remaining
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

func flushColdPendingStoreWithTimeout(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		flushColdPendingStoreNow()
		waitColdPendingStoreFlush()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
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
		coldPendingStore.firstDirtyAt = time.Time{}
		coldPendingStore.cond.Broadcast()
		coldPendingStore.mu.Unlock()
		return
	}
	if coldPendingStore.flushing {
		coldPendingStore.mu.Unlock()
		return
	}
	coldPendingStore.dirty = false
	coldPendingStore.firstDirtyAt = time.Time{}
	coldPendingStore.flushing = true
	coldPendingStore.mu.Unlock()

	for {
		snap := buildColdPendingSnapshot()
		writeErr := writeColdPendingSnapshotAtomic(path, snap)

		coldPendingStore.mu.Lock()
		coldPendingStore.lastWriteErr = writeErr
		if writeErr == nil {
			coldPendingStore.writeCount++
		}
		if !coldPendingStore.dirty || coldPendingStore.path != path {
			coldPendingStore.flushing = false
			coldPendingStore.firstDirtyAt = time.Time{}
			coldPendingStore.cond.Broadcast()
			coldPendingStore.mu.Unlock()
			return
		}
		coldPendingStore.dirty = false
		coldPendingStore.firstDirtyAt = time.Time{}
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

func marshalBoundedColdPendingSnapshot(snap persistedColdPendingSnapshot, maxBytes int) ([]byte, error) {
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	if len(data) <= maxBytes {
		return data, nil
	}
	// Trim from the LRU tail (end of slices) via binary search over total entries
	// so the largest valid MRU prefix <= maxBytes is always persisted.
	total := len(snap.Standard) + len(snap.OpenCode)
	sliceFor := func(keep int) persistedColdPendingSnapshot {
		if keep <= 0 {
			return persistedColdPendingSnapshot{SchemaVersion: snap.SchemaVersion}
		}
		stdKeep := 0
		ocKeep := 0
		if total > 0 {
			stdKeep = (len(snap.Standard) * keep) / total
			ocKeep = keep - stdKeep
			if ocKeep > len(snap.OpenCode) {
				extra := ocKeep - len(snap.OpenCode)
				ocKeep = len(snap.OpenCode)
				stdKeep = min(len(snap.Standard), stdKeep+extra)
			}
			if stdKeep > len(snap.Standard) {
				stdKeep = len(snap.Standard)
			}
		}
		return persistedColdPendingSnapshot{
			SchemaVersion: snap.SchemaVersion,
			Standard:      snap.Standard[:stdKeep],
			OpenCode:      snap.OpenCode[:ocKeep],
		}
	}
	lo, hi := 0, total
	var best []byte
	for lo <= hi {
		mid := lo + (hi-lo)/2
		b, mErr := json.Marshal(sliceFor(mid))
		if mErr != nil {
			return nil, mErr
		}
		if len(b) <= maxBytes {
			best = b
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if len(best) == 0 {
		return nil, fmt.Errorf("冷缓存快照超过大小上限")
	}
	return best, nil
}

func writeColdPendingSnapshotAtomic(path string, snap persistedColdPendingSnapshot) error {
	coldPendingStore.fileMu.Lock()
	defer coldPendingStore.fileMu.Unlock()

	data, err := marshalBoundedColdPendingSnapshot(snap, coldPendingWatchMaxBytes)
	if err != nil {
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

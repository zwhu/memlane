package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// Client presentation state only. True means read; false means newly discovered.
// No text, source records, embeddings or Cognee writes are stored here.
type knowledgeReadState struct {
	Version     int             `json:"version"`
	Initialized bool            `json:"initialized"`
	StartedAt   time.Time       `json:"enabled_at"`
	Read        map[string]bool `json:"knowledge_ids"`
}

func (a *App) readingStateDir() string {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return ""
	}
	return filepath.Dir(base)
}

func knowledgeReadPathForDir(stateDir, readingID string, p AccessProject) (string, error) {
	if stateDir == "" {
		return "", fmt.Errorf("客户端状态位置不可用")
	}
	if readingID == "" {
		return "", fmt.Errorf("客户端阅读身份不可用")
	}
	return filepath.Join(stateDir, fmt.Sprintf("knowledge-read-%x.json", sha256.Sum256([]byte(readingID+"/"+p.ID+"/"+p.Dataset)))), nil
}

func (a *App) knowledgeReadPath(p AccessProject) (string, error) {
	stateDir := a.readingStateDir()
	if stateDir == "" {
		return "", fmt.Errorf("客户端状态位置不可用")
	}
	identity, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil {
		return "", err
	}
	return knowledgeReadPathForDir(stateDir, identity, p)
}
func readKnowledgeState(path string) (knowledgeReadState, error) {
	s := knowledgeReadState{Version: 1, Read: map[string]bool{}}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return s, fmt.Errorf("已读状态文件不可用")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Read == nil {
		return knowledgeReadState{}, fmt.Errorf("已读状态读取失败")
	}
	if s.Initialized && s.StartedAt.IsZero() {
		info, err := os.Stat(path)
		if err != nil {
			return s, err
		}
		s.StartedAt = info.ModTime().UTC()
	}
	return s, nil
}
func writeKnowledgeState(path string, s knowledgeReadState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".knowledge-read-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

const (
	maxMarkReadBatch       = 200
	maxFailedMarkReadQueue = 4096
)

type knowledgeReadMarkJob struct {
	key             string
	path            string
	stateDir        string
	docIDs          []string
	markAll         bool
	queue           *projectQueueState
	update          func(func())
	control         *ProjectControlClient
	readingIdentity string
	projectID       string
	datasetID       string
	app             *App
	authEpoch       uint64
	done            chan error
	onResult        func(v2ReadingStateMarkAllReadResponse, error)
}

// knowledgeReadWriter serializes all background atomic writes of
// knowledge-read-*.json, /v2/reading-state/mark-read (chunked <=200), and
// /v2/reading-state/mark-all-read off the UI thread, coalescing rapid
// mark-read bursts while keeping mark-all-read strictly ordered per scope.
type knowledgeReadWriter struct {
	mu            sync.Mutex
	fileMu        sync.Mutex
	cond          *sync.Cond
	jobs          []*knowledgeReadMarkJob
	failedPending map[string][]string
	failedMeta    map[string]*knowledgeReadMarkJob
	running       bool
	inFlight      bool
	inFlightJob   *knowledgeReadMarkJob
	writeFn       func(path string, s knowledgeReadState) error
}

func (a *App) ensureKnowledgeWriter() *knowledgeReadWriter {
	if a.knowledgeWriter == nil {
		w := &knowledgeReadWriter{
			failedPending: map[string][]string{},
			failedMeta:    map[string]*knowledgeReadMarkJob{},
			writeFn:       writeKnowledgeState,
		}
		w.cond = sync.NewCond(&w.mu)
		a.knowledgeWriter = w
	}
	return a.knowledgeWriter
}

func (a *App) knowledgeWriterHasPending(projectID, datasetID string) bool {
	if a.knowledgeWriter == nil {
		return false
	}
	w := a.knowledgeWriter
	w.mu.Lock()
	defer w.mu.Unlock()
	matches := func(j *knowledgeReadMarkJob) bool {
		if j == nil {
			return false
		}
		return (projectID != "" && j.projectID == projectID) || (datasetID != "" && j.datasetID == datasetID)
	}
	if matches(w.inFlightJob) {
		return true
	}
	for _, j := range w.jobs {
		if matches(j) {
			return true
		}
	}
	return false
}

func appendUniqueDocIDs(dst []string, ids ...string) []string {
	if len(ids) == 0 {
		return dst
	}
	seen := make(map[string]bool, len(dst)+len(ids))
	for _, existing := range dst {
		seen[existing] = true
	}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		dst = append(dst, id)
	}
	return dst
}

func (a *App) enqueueKnowledgeReadMark(path, docID string, q *projectQueueState, control *ProjectControlClient, readingID, projectID, datasetID string) {
	w := a.ensureKnowledgeWriter()
	updateFn := a.Update
	authEpoch := a.authEpoch
	stateDir := a.readingStateDir()
	key := path
	if key == "" {
		key = readingID + "/" + projectID + "/" + datasetID
	}
	w.mu.Lock()
	var target *knowledgeReadMarkJob
	for idx := len(w.jobs) - 1; idx >= 0; idx-- {
		if w.jobs[idx].key == key {
			if !w.jobs[idx].markAll {
				target = w.jobs[idx]
			}
			break
		}
	}
	retryIDs := w.failedPending[key]
	if len(retryIDs) > 0 {
		delete(w.failedPending, key)
		delete(w.failedMeta, key)
	}
	if target == nil {
		target = &knowledgeReadMarkJob{
			key:             key,
			path:            path,
			stateDir:        stateDir,
			queue:           q,
			update:          updateFn,
			control:         control,
			readingIdentity: readingID,
			projectID:       projectID,
			datasetID:       datasetID,
			app:             a,
			authEpoch:       authEpoch,
		}
		w.jobs = append(w.jobs, target)
	} else {
		target.stateDir = stateDir
		target.queue = q
		target.update = updateFn
		target.control = control
		target.readingIdentity = readingID
		target.projectID = projectID
		target.datasetID = datasetID
		target.app = a
		target.authEpoch = authEpoch
	}
	target.docIDs = appendUniqueDocIDs(target.docIDs, retryIDs...)
	target.docIDs = appendUniqueDocIDs(target.docIDs, docID)
	if !w.running {
		w.running = true
		go w.drainLoop()
	}
	w.mu.Unlock()
}

func (a *App) enqueueKnowledgeMarkAllReadV2(path string, q *projectQueueState, control ProjectControlClient, readingID, projectID, datasetID string, syncWait bool) (v2ReadingStateMarkAllReadResponse, error) {
	return a.enqueueKnowledgeMarkAllReadV2WithEpoch(path, a.readingStateDir(), q, control, readingID, projectID, datasetID, a.authEpoch, syncWait)
}

func (a *App) enqueueKnowledgeMarkAllReadV2WithEpoch(path, stateDir string, q *projectQueueState, control ProjectControlClient, readingID, projectID, datasetID string, authEpoch uint64, syncWait bool) (v2ReadingStateMarkAllReadResponse, error) {
	w := a.ensureKnowledgeWriter()
	key := path
	if key == "" {
		key = readingID + "/" + projectID + "/" + datasetID
	}
	ctrlCopy := control
	done := make(chan error, 1)
	var outResp v2ReadingStateMarkAllReadResponse
	job := &knowledgeReadMarkJob{
		key:             key,
		path:            path,
		stateDir:        stateDir,
		markAll:         true,
		queue:           q,
		control:         &ctrlCopy,
		readingIdentity: readingID,
		projectID:       projectID,
		datasetID:       datasetID,
		app:             a,
		authEpoch:       authEpoch,
		done:            done,
		onResult: func(resp v2ReadingStateMarkAllReadResponse, _ error) {
			outResp = resp
		},
	}
	w.mu.Lock()
	delete(w.failedPending, key)
	delete(w.failedMeta, key)
	w.jobs = append(w.jobs, job)
	if !w.running {
		w.running = true
		go w.drainLoop()
	}
	w.mu.Unlock()
	if syncWait {
		err := <-done
		return outResp, err
	}
	return v2ReadingStateMarkAllReadResponse{}, nil
}

func (w *knowledgeReadWriter) drainLoop() {
	for {
		w.mu.Lock()
		if len(w.jobs) == 0 {
			w.running = false
			w.inFlight = false
			w.inFlightJob = nil
			w.cond.Broadcast()
			w.mu.Unlock()
			return
		}
		job := w.jobs[0]
		w.jobs = w.jobs[1:]
		w.inFlight = true
		w.inFlightJob = job
		writeFn := w.writeFn
		if writeFn == nil {
			writeFn = writeKnowledgeState
		}
		w.mu.Unlock()

		err := w.executeMarkJob(job, writeFn)
		if job.done != nil {
			job.done <- err
		}

		w.mu.Lock()
		w.inFlight = false
		w.inFlightJob = nil
		if len(w.jobs) == 0 {
			w.cond.Broadcast()
		}
		w.mu.Unlock()
	}
}

func (w *knowledgeReadWriter) requeueFailedIDs(job *knowledgeReadMarkJob, ids []string) {
	if job == nil || len(ids) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failedMeta == nil {
		w.failedMeta = map[string]*knowledgeReadMarkJob{}
	}
	merged := appendUniqueDocIDs(w.failedPending[job.key], ids...)
	if len(merged) > maxFailedMarkReadQueue {
		merged = merged[len(merged)-maxFailedMarkReadQueue:]
	}
	w.failedPending[job.key] = merged
	metaCopy := *job
	metaCopy.docIDs = nil
	metaCopy.done = nil
	metaCopy.onResult = nil
	w.failedMeta[job.key] = &metaCopy
}

func (w *knowledgeReadWriter) executeMarkJob(job *knowledgeReadMarkJob, writeFn func(string, knowledgeReadState) error) error {
	jobStillValid := func() bool {
		return job.app == nil || job.authEpoch == job.app.authEpoch
	}
	reportErr := func() {
		if job.update != nil && job.queue != nil {
			q := job.queue
			job.update(func() {
				if !jobStillValid() {
					return
				}
				q.KnowledgeReadError = "已读状态未保存"
				q.StateVersion++
			})
		}
	}

	if job.control != nil && job.projectID != "" && job.datasetID != "" && job.readingIdentity != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		hasV2, probeErr := job.control.probeFeature(ctx, v2FeatureReadingState)
		if probeErr != nil {
			cancel()
			if !job.markAll {
				w.requeueFailedIDs(job, job.docIDs)
			}
			if job.onResult != nil {
				job.onResult(v2ReadingStateMarkAllReadResponse{}, probeErr)
			}
			reportErr()
			return probeErr
		}
		if hasV2 {
			// Guard against flipping is_auto_baseline before any pending local legacy file is migrated.
			if job.app != nil {
				if migErr := job.app.migrateReadingBeforeWrite(ctx, job); migErr != nil {
					cancel()
					if !job.markAll {
						w.requeueFailedIDs(job, job.docIDs)
					}
					if job.onResult != nil {
						job.onResult(v2ReadingStateMarkAllReadResponse{}, migErr)
					}
					reportErr()
					return migErr
				}
			}
			if job.markAll {
				resp, err := job.control.readingStateMarkAllReadV2(ctx, v2ReadingStateMarkAllReadRequest{
					ReadingIdentity: job.readingIdentity,
					ProjectID:       job.projectID,
					DatasetID:       job.datasetID,
				})
				cancel()
				if job.onResult != nil {
					job.onResult(resp, err)
				}
				if err != nil {
					reportErr()
					return err
				}
				return nil
			}
			var lastResp v2ReadingStateMarkReadResponse
			var hasResp bool
			for start := 0; start < len(job.docIDs); start += maxMarkReadBatch {
				end := min(start+maxMarkReadBatch, len(job.docIDs))
				chunk := job.docIDs[start:end]
				var resp v2ReadingStateMarkReadResponse
				var err error
				for attempt := 0; attempt < 3; attempt++ {
					resp, err = job.control.readingStateMarkReadV2(ctx, v2ReadingStateMarkReadRequest{
						ReadingIdentity: job.readingIdentity,
						ProjectID:       job.projectID,
						DatasetID:       job.datasetID,
						DocIDs:          chunk,
					})
					if err == nil || isControlAuthDenied(err) || isControlNotFound(err) || ctx.Err() != nil {
						break
					}
					select {
					case <-ctx.Done():
					case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
					}
				}
				if err != nil {
					cancel()
					w.requeueFailedIDs(job, job.docIDs[start:])
					reportErr()
					return err
				}
				lastResp = resp
				hasResp = true
			}
			cancel()
			if hasResp && job.update != nil && job.queue != nil {
				q := job.queue
				resp := lastResp
				job.update(func() {
					if !jobStillValid() {
						return
					}
					if resp.StateRev > 0 && resp.StateRev < q.ServerUnreadStateRev {
						// Stale out-of-order response: never regress unread count.
						return
					}
					if resp.StateRev > q.ServerUnreadStateRev {
						q.ServerUnreadStateRev = resp.StateRev
					}
					if resp.unreadCountKnown() {
						q.ServerUnreadKnown = true
						q.ServerUnreadCount = resp.UnreadCount
					}
					if q.KnowledgeReadError == "已读状态未保存" {
						q.KnowledgeReadError = ""
					}
					q.StateVersion++
				})
			}
			return nil
		}
		cancel()
	}

	if job.markAll {
		return fmt.Errorf("未读状态服务端不可用")
	}
	if job.path == "" {
		return nil
	}
	w.fileMu.Lock()
	defer w.fileMu.Unlock()

	s, err := readKnowledgeState(job.path)
	if err != nil {
		// Never overwrite a corrupt or unreadable read-state file with an invented baseline.
		w.requeueFailedIDs(job, job.docIDs)
		reportErr()
		return err
	}
	changed := false
	for _, id := range job.docIDs {
		if read, ok := s.Read[id]; !ok || !read {
			s.Read[id] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := writeFn(job.path, s); err != nil {
		w.requeueFailedIDs(job, job.docIDs)
		reportErr()
		return err
	}
	if job.update != nil && job.queue != nil {
		q := job.queue
		job.update(func() {
			if !jobStillValid() {
				return
			}
			if q.KnowledgeReadError == "已读状态未保存" {
				q.KnowledgeReadError = ""
				q.StateVersion++
			}
		})
	}
	return nil
}

// waitKnowledgeReadFlush blocks until all queued background mark-read writes
// have finished flushing to disk or server.
func (a *App) waitKnowledgeReadFlush() {
	if a.knowledgeWriter == nil {
		return
	}
	w := a.knowledgeWriter
	w.mu.Lock()
	for len(w.jobs) > 0 || w.inFlight || w.running {
		w.cond.Wait()
	}
	w.mu.Unlock()
}

// flushKnowledgeReadWithTimeout re-enqueues any previously failed mark-read items
// for a final bounded attempt before exit and returns an error if items remain unsaved.
func (a *App) flushKnowledgeReadWithTimeout(timeout time.Duration) error {
	if a.knowledgeWriter == nil {
		return nil
	}
	w := a.knowledgeWriter
	w.mu.Lock()
	if len(w.failedPending) > 0 {
		for key, ids := range w.failedPending {
			meta := w.failedMeta[key]
			if meta == nil || len(ids) == 0 {
				continue
			}
			retryJob := *meta
			retryJob.docIDs = append([]string(nil), ids...)
			delete(w.failedPending, key)
			delete(w.failedMeta, key)
			w.jobs = append(w.jobs, &retryJob)
		}
		if len(w.jobs) > 0 && !w.running {
			w.running = true
			go w.drainLoop()
		}
	}
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		a.waitKnowledgeReadFlush()
		close(done)
	}()
	if timeout <= 0 {
		<-done
	} else {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			return fmt.Errorf("已读状态刷新超时")
		}
	}
	w.mu.Lock()
	unsaved := 0
	for _, ids := range w.failedPending {
		unsaved += len(ids)
	}
	w.mu.Unlock()
	if unsaved > 0 {
		return fmt.Errorf("仍有 %d 条已读状态未能保存", unsaved)
	}
	return nil
}

func isMigrationEligibleProject(p AccessProject) bool {
	if p.ID == "" || p.Dataset == "" {
		return false
	}
	return p.Status == "verified"
}

func (a *App) hasPendingLocalReadingMigration(controlURL, stateDir, readingID string, projects []AccessProject) bool {
	if stateDir == "" || readingID == "" {
		return false
	}
	a.readingMigrateMu.Lock()
	defer a.readingMigrateMu.Unlock()
	for _, p := range projects {
		if !isMigrationEligibleProject(p) {
			continue
		}
		if a.pendingLocalReadingMigrationLocked(controlURL, stateDir, readingID, p) {
			return true
		}
	}
	return false
}

func (a *App) pendingLocalReadingMigrationLocked(controlURL, stateDir, readingID string, p AccessProject) bool {
	if controlURL != "" && a.readingMigrated[controlURL+"|"+readingID+"|"+p.ID+"|"+p.Dataset] {
		return false
	}
	path, err := knowledgeReadPathForDir(stateDir, readingID, p)
	if err != nil {
		return false
	}
	_, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return false
	}
	if statErr != nil {
		return true
	}
	localState, readErr := readKnowledgeState(path)
	return readErr != nil || localState.Initialized
}

func (a *App) migrateReadingBeforeWrite(ctx context.Context, job *knowledgeReadMarkJob) error {
	if job.stateDir == "" {
		return nil
	}
	scope := AccessProject{ID: job.projectID, Dataset: job.datasetID}
	a.readingMigrateMu.Lock()
	pending := a.pendingLocalReadingMigrationLocked(job.control.Config.URL, job.stateDir, job.readingIdentity, scope)
	a.readingMigrateMu.Unlock()
	if !pending {
		return nil
	}
	directory, err := job.control.directory(ctx)
	if err != nil {
		return err
	}
	for _, p := range directory.Projects {
		if p.ID == scope.ID && p.Dataset == scope.Dataset && isMigrationEligibleProject(p) {
			return a.migrateReadingStateV2Snapshot(ctx, *job.control, job.readingIdentity, job.stateDir, []AccessProject{p})
		}
	}
	return fmt.Errorf("项目尚未确认，已读状态未保存")
}

// migrateReadingStateV2 runs once per (controlURL, readingIdentity, projectID, datasetID)
// scope with singleflight coalescing so cold-start reads wait for migration to settle.
func (a *App) migrateReadingStateV2(ctx context.Context, control ProjectControlClient, projects []AccessProject) error {
	readingID, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil || readingID == "" {
		return err
	}
	return a.migrateReadingStateV2Snapshot(ctx, control, readingID, a.readingStateDir(), projects)
}

func (a *App) migrateReadingStateV2Snapshot(ctx context.Context, control ProjectControlClient, readingID, stateDir string, projects []AccessProject) error {
	if readingID == "" {
		return fmt.Errorf("客户端阅读身份不可用")
	}
	hasV2ReadState, probeErr := control.probeFeature(ctx, v2FeatureReadingState)
	if probeErr != nil {
		return probeErr
	}
	if !hasV2ReadState {
		return nil
	}
	scopeKeyFor := func(p AccessProject) string {
		return control.Config.URL + "|" + readingID + "|" + p.ID + "|" + p.Dataset
	}

	var err error
	for {
		var pending []AccessProject
		var waitChs []chan struct{}
		a.readingMigrateMu.Lock()
		if a.readingMigrated == nil {
			a.readingMigrated = map[string]bool{}
		}
		if a.readingMigrating == nil {
			a.readingMigrating = map[string]chan struct{}{}
		}
		for _, p := range projects {
			if !isMigrationEligibleProject(p) {
				continue
			}
			sk := scopeKeyFor(p)
			if a.readingMigrated[sk] {
				continue
			}
			if ch, busy := a.readingMigrating[sk]; busy {
				waitChs = append(waitChs, ch)
				continue
			}
			pending = append(pending, p)
		}
		if len(pending) == 0 {
			a.readingMigrateMu.Unlock()
			if len(waitChs) == 0 {
				return nil
			}
			for _, ch := range waitChs {
				select {
				case <-ch:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue
		}
		batchDone := make(chan struct{})
		claimed := make([]string, 0, len(pending))
		for _, p := range pending {
			sk := scopeKeyFor(p)
			a.readingMigrating[sk] = batchDone
			claimed = append(claimed, sk)
		}
		a.readingMigrateMu.Unlock()

		migratedOK := map[string]bool{}
		func() {
			defer func() {
				a.readingMigrateMu.Lock()
				for _, sk := range claimed {
					delete(a.readingMigrating, sk)
					if migratedOK[sk] {
						a.readingMigrated[sk] = true
					}
				}
				close(batchDone)
				a.readingMigrateMu.Unlock()
			}()

			refs := make([]v2ReadingStateProjectRef, 0, len(pending))
			for _, p := range pending {
				refs = append(refs, v2ReadingStateProjectRef{ProjectID: p.ID, DatasetID: p.Dataset})
			}
			resp, reqErr := control.readingStateMigrateLocalV2(ctx, v2ReadingStateMigrateRequest{
				ReadingIdentity: readingID,
				Projects:        refs,
			})
			if reqErr != nil {
				err = reqErr
				return
			}
			migratedStatus := make(map[string]v2ReadingStateMigratedScope, len(resp.Migrated))
			for _, row := range resp.Migrated {
				migratedStatus[row.ProjectID+"/"+row.DatasetID] = row
			}
			for _, p := range pending {
				sk := scopeKeyFor(p)
				row, reported := migratedStatus[p.ID+"/"+p.Dataset]
				if !reported {
					err = fmt.Errorf("未读状态迁移回执缺少项目 %s", p.ID)
					return
				}
				status := strings.ToLower(strings.TrimSpace(row.Status))
				switch status {
				case "migrated", "already_migrated":
					migratedOK[sk] = true
					continue
				case "no_legacy_file", "no_local_file", "not_found":
					if stateDir == "" {
						migratedOK[sk] = true
						continue
					}
					path, pathErr := knowledgeReadPathForDir(stateDir, readingID, p)
					if pathErr != nil {
						err = pathErr
						return
					}
					if _, statErr := os.Stat(path); statErr != nil {
						if os.IsNotExist(statErr) {
							migratedOK[sk] = true
							continue
						}
						// Non-ENOENT stat error: never mark migrated!
						err = statErr
						return
					}
					localState, readErr := readKnowledgeState(path)
					if readErr != nil {
						// Corrupt/unreadable local file: never mark migrated!
						err = readErr
						return
					}
					if !localState.Initialized {
						migratedOK[sk] = true
						continue
					}
					if _, importErr := importReadingStateStagingV2(ctx, control, readingID, p.ID, p.Dataset, localState); importErr != nil {
						err = importErr
						return
					}
					migratedOK[sk] = true
				default:
					err = fmt.Errorf("未读状态迁移状态未确认：%s", row.Status)
					return
				}
			}
		}()
		if err != nil {
			return err
		}
		if len(waitChs) == 0 {
			return nil
		}
		for _, ch := range waitChs {
			select {
			case <-ch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func importReadingStateStagingV2(ctx context.Context, control ProjectControlClient, readingID, projectID, datasetID string, local knowledgeReadState) (v2ReadingStateImportCommitResponse, error) {
	enabledAt := ""
	if !local.StartedAt.IsZero() {
		enabledAt = local.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	beginResp, err := control.readingStateImportBeginV2(ctx, v2ReadingStateImportBeginRequest{
		ReadingIdentity: readingID,
		ProjectID:       projectID,
		DatasetID:       datasetID,
		Initialized:     local.Initialized,
		EnabledAt:       enabledAt,
	})
	if err != nil {
		return v2ReadingStateImportCommitResponse{}, err
	}
	keys := make([]string, 0, len(local.Read))
	for id := range local.Read {
		if id != "" {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	const chunkSize = 500
	for start := 0; start < len(keys); start += chunkSize {
		end := min(start+chunkSize, len(keys))
		chunk := make([]v2ReadingStateImportChunkItem, 0, end-start)
		for _, id := range keys[start:end] {
			chunk = append(chunk, v2ReadingStateImportChunkItem{
				DocID:  id,
				IsRead: local.Read[id],
			})
		}
		if _, err := control.readingStateImportChunkV2(ctx, v2ReadingStateImportChunkRequest{
			BatchID: beginResp.BatchID,
			Items:   chunk,
		}); err != nil {
			return v2ReadingStateImportCommitResponse{}, err
		}
	}
	expected := len(keys)
	return control.readingStateImportCommitV2(ctx, v2ReadingStateImportCommitRequest{
		BatchID:       beginResp.BatchID,
		ExpectedItems: &expected,
	})
}

func applyServerUnreadSnapshot(q *projectQueueState, unreadCount int, stateRev int64, writerPending bool) bool {
	if q == nil {
		return false
	}
	if stateRev > 0 {
		if stateRev < q.ServerUnreadStateRev {
			return false
		}
		if writerPending && stateRev <= q.ServerUnreadStateRev {
			return false
		}
		q.ServerUnreadStateRev = stateRev
		q.ServerUnreadKnown = true
		q.ServerUnreadCount = unreadCount
		q.ServerUnreadSnapshot++
		return true
	}
	if writerPending || q.ServerUnreadStateRev > 0 {
		return false
	}
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = unreadCount
	q.ServerUnreadSnapshot++
	return true
}

func (a *App) syncV2ProjectUnreadCount(projectID, datasetID string, unreadCount int, stateRev int64, catalogVersion string) {
	for _, p := range a.access.Directory.Projects {
		if (projectID != "" && p.ID == projectID) || (datasetID != "" && p.Dataset == datasetID) {
			q := a.projectQueueStateFor(p)
			applyServerUnreadSnapshot(q, unreadCount, stateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset))
			if catalogVersion != "" {
				q.KnowledgeCatalogVersion = catalogVersion
			}
			q.StateVersion++
			return
		}
	}
}

func (a *App) clearDatasetServerUnread(datasetID string) {
	if datasetID == "" {
		return
	}
	for _, s := range a.panes {
		for idx := range s.Items {
			if s.Items[idx].Dataset == datasetID && s.Items[idx].ServerUnread {
				s.Items[idx].ServerUnread = false
			}
		}
		if s.Selected != nil && s.Selected.Dataset == datasetID && s.Selected.ServerUnread {
			s.Selected.ServerUnread = false
		}
	}
	for idx := range a.overviewState.Snapshot.Knowledge {
		if a.overviewState.Snapshot.Knowledge[idx].Dataset == datasetID {
			a.overviewState.Snapshot.Knowledge[idx].ServerUnread = false
		}
	}
	for idx := range a.overviewState.DayItems {
		if a.overviewState.DayItems[idx].Dataset == datasetID {
			a.overviewState.DayItems[idx].ServerUnread = false
		}
	}
}

// handleKnowledgeCatalogVersionChanged implements C-P1-07 non-disruptive refresh:
// if the user is at the top of the project's knowledge list and not deep-reading,
// silently refresh page 1; otherwise keep the viewport fixed and show the top banner.
func (a *App) handleKnowledgeCatalogVersionChanged(projectID, datasetID, newVersion string) {
	if a.page != "知识" || a.kind != "知识" {
		return
	}
	cur, ok := a.current()
	if !ok || (cur.ID != projectID && cur.Dataset != datasetID) {
		return
	}
	s := a.state()
	if s == nil || !s.Loaded {
		return
	}
	if s.Knowledge.CatalogVersion != "" && s.Knowledge.CatalogVersion == newVersion {
		return
	}
	detailScrolled := false
	if s.Selected != nil {
		if sc := s.DetailScroll[s.Selected.key()]; sc != nil && sc.Y > 2 {
			detailScrolled = true
		}
	}
	atTop := s.ListScroll.Y <= 0 && !detailScrolled && !s.Knowledge.Detail
	if atTop && !s.Loading && !s.Knowledge.Appending {
		a.loadKnowledgeFeed(s)
		return
	}
	s.Knowledge.NewCatalogAvailable = true
}

type computedKnowledgeCatalog struct {
	State  knowledgeReadState
	Unread []Item
	ErrMsg string
}

// computeKnowledgeCatalogOffUI runs file locking and atomic JSON persistence off
// the UI thread so observeKnowledgeCatalog never stalls the main UI loop.
func computeKnowledgeCatalogOffUI(w *knowledgeReadWriter, path string, p AccessProject, docs []Document, prevStartedAt time.Time) computedKnowledgeCatalog {
	w.fileMu.Lock()
	defer w.fileMu.Unlock()

	s, err := readKnowledgeState(path)
	if err != nil {
		return computedKnowledgeCatalog{ErrMsg: "未读状态暂时无法读取"}
	}
	next := map[string]bool{}
	for _, d := range docs {
		if d.Dataset != p.Dataset || d.ID == "" {
			return computedKnowledgeCatalog{ErrMsg: "未读范围尚未确认"}
		}
		if !d.knowledge() {
			continue
		}
		read, exists := s.Read[d.ID]
		if !s.Initialized && !exists {
			read = true
		} // Existing catalog is the first-use baseline.
		if !exists && s.Initialized {
			if created, ok := officialTime(d.Created); ok && !created.After(s.StartedAt) {
				read = true
			}
		}
		next[d.ID] = read
	}
	if !s.Initialized {
		s.StartedAt = time.Now().UTC()
	}
	changed := !s.Initialized || !reflect.DeepEqual(s.Read, next) || prevStartedAt.IsZero()
	s.Read = next
	s.Initialized = true
	if changed {
		if err = writeKnowledgeState(path, s); err != nil {
			return computedKnowledgeCatalog{ErrMsg: "未读状态未保存"}
		}
	}
	var unread []Item
	for _, d := range docs {
		if d.knowledge() && !s.Read[d.ID] {
			copy := d
			unread = append(unread, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &copy, Date: d.date(), Title: d.title()})
		}
	}
	sort.Slice(unread, func(i, j int) bool {
		return unread[i].Document.Created > unread[j].Document.Created
	})
	return computedKnowledgeCatalog{State: s, Unread: unread}
}

func applyComputedKnowledgeCatalog(q *projectQueueState, res computedKnowledgeCatalog) {
	if res.ErrMsg != "" {
		q.KnowledgeReadError = res.ErrMsg
		q.StateVersion++
		return
	}
	q.KnowledgeRead = res.State
	q.UnreadKnowledge = res.Unread
	q.KnowledgeReadError = ""
	q.StateVersion++
}

func (a *App) observeKnowledgeCatalog(p AccessProject, q *projectQueueState, docs []Document) {
	path, err := a.knowledgeReadPath(p)
	if err != nil {
		return
	}
	// Serialize with any in-flight background markKnowledgeRead writes so disk
	// state never loses a pending read transition.
	a.waitKnowledgeReadFlush()
	res := computeKnowledgeCatalogOffUI(a.ensureKnowledgeWriter(), path, p, docs, q.KnowledgeRead.StartedAt)
	applyComputedKnowledgeCatalog(q, res)
}

func (a *App) markKnowledgeRead(i Item) {
	if i.Kind != "知识" || i.Document == nil || !i.BodyLoaded || i.BodyTruncated || !i.Document.knowledge() {
		return
	}
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	var controlPtr *ProjectControlClient
	if ctrl, ok := a.accessClient(); ok {
		c := ctrl
		controlPtr = &c
	}
	for _, p := range a.access.Directory.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		path, pathErr := a.knowledgeReadPath(p)
		if pathErr != nil && controlPtr == nil {
			return
		}
		q := a.projectQueueStateFor(p)
		if q.KnowledgeRead.Read == nil {
			q.KnowledgeRead.Read = map[string]bool{}
		}
		wasRead, exists := q.KnowledgeRead.Read[i.ID]
		wasServerUnread := i.ServerUnreadKnown && i.ServerUnread
		if exists && wasRead && !wasServerUnread && q.KnowledgeReadError == "" {
			return
		}
		// Instant in-memory state update; atomic JSON serialization or
		// /v2/reading-state/mark-read runs serially on the background writer so
		// 10k+ maps never stall the UI thread.
		q.KnowledgeRead.Read[i.ID] = true
		if q.ServerUnreadKnown && q.ServerUnreadCount > 0 && (wasServerUnread || (!exists || !wasRead)) {
			q.ServerUnreadCount--
		}
		if len(q.UnreadKnowledge) > 0 {
			filtered := q.UnreadKnowledge[:0]
			for _, row := range q.UnreadKnowledge {
				if row.ID != i.ID {
					filtered = append(filtered, row)
				}
			}
			q.UnreadKnowledge = filtered
		}
		// Clear ServerUnread flag on in-memory items so unread dots disappear immediately.
		for _, s := range a.panes {
			for idx := range s.Items {
				if s.Items[idx].Dataset == i.Dataset && s.Items[idx].ID == i.ID {
					s.Items[idx].ServerUnread = false
				}
			}
			if s.Selected != nil && s.Selected.Dataset == i.Dataset && s.Selected.ID == i.ID {
				s.Selected.ServerUnread = false
			}
		}
		for idx := range a.overviewState.Snapshot.Knowledge {
			if a.overviewState.Snapshot.Knowledge[idx].Dataset == i.Dataset && a.overviewState.Snapshot.Knowledge[idx].ID == i.ID {
				a.overviewState.Snapshot.Knowledge[idx].ServerUnread = false
			}
		}
		for idx := range a.overviewState.DayItems {
			if a.overviewState.DayItems[idx].Dataset == i.Dataset && a.overviewState.DayItems[idx].ID == i.ID {
				a.overviewState.DayItems[idx].ServerUnread = false
			}
		}
		q.StateVersion++
		a.enqueueKnowledgeReadMark(path, i.ID, q, controlPtr, readingID, p.ID, p.Dataset)
		return
	}
}

func (q *projectQueueState) unreadCount() int {
	if q.KnowledgeReadError != "" {
		return 0
	}
	if q.ServerUnreadKnown {
		return max(0, q.ServerUnreadCount)
	}
	n := 0
	for _, read := range q.KnowledgeRead.Read {
		if !read {
			n++
		}
	}
	return n
}

func (a *App) projectUnread(p Project) int {
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	if q == nil {
		return 0
	}
	return q.unreadCount()
}

func (a *App) itemIsUnread(i Item) bool {
	if i.Kind != "知识" {
		return false
	}
	for _, p := range a.Config.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		identity := a.cachedBaseReadingIdentity()
		q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
		if q != nil && q.KnowledgeReadError != "" {
			return false
		}
		if q != nil {
			if read, exists := q.KnowledgeRead.Read[i.ID]; exists {
				return !read
			}
		}
		if i.ServerUnreadKnown {
			return i.ServerUnread
		}
		return false
	}
	return false
}

func (a *App) openProjectUnreadFeed(index int) {
	a.projectUI.UnreadPopoverOpen = false
	a.projectUI.UnreadPopoverProject = ""
	a.navigate("知识", index)
	s := a.state()
	s.Knowledge.UnreadOnly = true
	s.Knowledge.CategoryFilter = ""
	s.Knowledge.RecallFilter = nil
	s.Knowledge.RecallAgentID = ""
	s.Knowledge.RecallEventID = ""
	if s.Knowledge.V2Mode || s.Loading || !s.Loaded {
		a.loadKnowledgeFeed(s)
		return
	}
	a.reorderKnowledge(s)
	if len(s.Items) > 0 {
		a.selectItem(s, s.Items[0])
	}
}

func (a *App) unreadProjectBadge(c *ui.Context, p Project, index int, active bool) {
	count := a.projectUnread(p)
	if count == 0 {
		return
	}
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	usePopover := q != nil && len(q.UnreadKnowledge) > 0 && (count > 10 || q.UnreadHasMore)

	label := formatBadgeCount(count)
	bg, fg := tok.Fill2, tok.Text2
	if active {
		bg, fg = tok.Accent, tok.OnAccent
	}
	btn := ui.ButtonBase(c.Key("unread-badge-"+p.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label("未读记忆："+p.Name).Tooltip(fmt.Sprintf("%d 条未读记忆，点击查看", count)).
		MinWidth(20).Height(18).Padding(0, 6).Radius(9).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	hoverBg := tok.Accent
	btn.Background(homeHoverColor(btn, "badge-bg", bg, hoverBg))
	if btn.Hovered() {
		fg = tok.OnAccent
	}
	v4ButtonPress(btn, 0.92)
	btn.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
	}).OnClick(func() {
		if usePopover {
			if a.projectUI.UnreadPopoverOpen && a.projectUI.UnreadPopoverProject == p.ID {
				a.projectUI.UnreadPopoverOpen = false
				a.projectUI.UnreadPopoverProject = ""
			} else {
				a.projectUI.UnreadPopoverProject = p.ID
				a.projectUI.UnreadPopoverOpen = true
			}
			return
		}
		a.openProjectUnreadFeed(index)
	})

	if usePopover && a.projectUI.UnreadPopoverOpen && a.projectUI.UnreadPopoverProject == p.ID {
		open := true
		ui.Popover(c.Key("unread-popover-"+p.ID), btn, &open, func() {
			ui.Column(c).Width(304).Padding(sp2, sp3).Gap(sp2).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					ui.Text(c, fmt.Sprintf("未读记忆 · %d 条", count)).Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1).SingleLine()
					ui.Spacer(c)
				})
				previewLimit := min(10, len(q.UnreadKnowledge))
				for idx := 0; idx < previewLimit; idx++ {
					item := q.UnreadKnowledge[idx]
					row := ui.ButtonBase(c.Key("unread-preview-"+item.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
						Label(item.Title).FillWidth().Padding(5, 6).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start).Gap(sp2)
					row.Background(homeHoverColor(row, "unread-prev-bg", ui.Transparent, tok.Hover))
					row.Children(func() {
						v4UnreadDot(c)
						ui.Text(c, item.Title).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, displayDate(item.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
					}).OnClick(func() {
						a.projectUI.UnreadPopoverOpen = false
						a.projectUI.UnreadPopoverProject = ""
						a.openOverviewItem(item)
					})
				}
				if count > 10 || q.UnreadHasMore {
					ui.Row(c).FillWidth().Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
						viewAllLabel := fmt.Sprintf("查看全部 %d 条未读记忆", count)
						allBtn := ui.ButtonBase(c.Key("unread-view-all-"+p.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
							Label(viewAllLabel).FillWidth().Height(28).Padding(0, sp2).Radius(radius).BorderWidth(0).
							AlignItems(ui.Center).Justify(ui.SpaceBetween)
						allBtn.Background(homeHoverColor(allBtn, "unread-all-bg", ui.Transparent, tok.AccentSofter))
						allBtn.Children(func() {
							ui.Text(c, viewAllLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
							ui.Icon(c, v4IconChev).FontSize(14).TextColor(tok.Accent).Shrink(0)
						}).OnClick(func() {
							a.openProjectUnreadFeed(index)
						})
					})
				}
			})
		})
		if !open {
			a.projectUI.UnreadPopoverOpen = false
			a.projectUI.UnreadPopoverProject = ""
		}
	}
}

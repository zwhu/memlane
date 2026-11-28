package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

var errExtractionPausedModelUnverified = errors.New("extraction paused: active model unverified")

type catalogPublishError struct {
	datasetID string
	projectID string
	err       error
}

func (e *catalogPublishError) Error() string {
	return fmt.Sprintf("publish catalog: %v", e.err)
}

func (e *catalogPublishError) Unwrap() error {
	return e.err
}

type FlushScheduleStatus string

const (
	FlushScheduled    FlushScheduleStatus = "queued"
	FlushDeduplicated FlushScheduleStatus = "deduplicated"
	FlushStopping     FlushScheduleStatus = "stopping"
	FlushInvalid      FlushScheduleStatus = "invalid"
)

const maxDirectSessionQABatch = 32

type InboxWorker struct {
	cfg                    Config
	server                 *Server
	now                    func() time.Time
	wake                   chan struct{}
	mu                     sync.Mutex
	claimMu                sync.Mutex
	preferFlush            map[string]bool
	run                    *inboxRun
	activeDatasets         map[string]bool
	datasetExtractCooldown map[string]time.Time
	startedAt              string
	needsRecovery          atomic.Bool
	completedSincePrune    atomic.Int64
	extractBackoffMu       sync.Mutex
	extractBackoffNext     time.Time
	extractBackoffDur      time.Duration
}

func (w *InboxWorker) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

type inboxRun struct {
	cancel   context.CancelFunc
	drain    chan struct{}
	done     chan struct{}
	stopping bool
}

type InboxSummary struct {
	Running        bool           `json:"running"`
	Pending        int            `json:"pending"`
	Processing     int            `json:"processing"`
	Succeeded      int            `json:"succeeded"`
	Failed         int            `json:"failed"`
	RecentFailures []InboxFailure `json:"recent_failures"`

	FilteredCount  int                      `json:"filtered_count,omitempty"`
	RecentFiltered []CaptureFilterEventView `json:"recent_filtered,omitempty"`

	CatalogPublishPending int    `json:"catalog_publish_pending,omitempty"`
	CatalogPublishFailed  int    `json:"catalog_publish_failed,omitempty"`
	CatalogPublishError   string `json:"catalog_publish_error,omitempty"`
	CatalogPublishErrorAt string `json:"catalog_publish_error_at,omitempty"`
}
type InboxFailure struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	LastError string `json:"last_error"`
	Attempts  int    `json:"attempts"`
	UpdatedAt string `json:"updated_at"`

	LastErrorAt string `json:"last_error_at,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Summary     string `json:"summary,omitempty"`
	NextAction  string `json:"next_action,omitempty"`
}

func (w *InboxWorker) Notify() {
	for range w.cfg.InboxWorkers {
		select {
		case w.wake <- struct{}{}:
		default:
			return
		}
	}
}

func (w *InboxWorker) isStopping() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run == nil || !w.run.stopping {
		return false
	}
	select {
	case <-w.run.done:
		return false
	default:
		return true
	}
}

func (w *InboxWorker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.run != nil {
		select {
		case <-w.run.done:
		default:
			w.mu.Unlock()
			return
		}
	}
	if w.activeDatasets == nil {
		w.activeDatasets = make(map[string]bool)
	}
	startedAt := domain.NowUTCMicro()
	w.startedAt = startedAt
	w.mu.Unlock()

	if inboxRec, flushRec, err := w.cfg.Store.RecoverStaleProcessingBefore(context.Background(), startedAt); err != nil {
		w.needsRecovery.Store(true)
		log.Printf("gateway inbox recover stale processing: %s", sanitizeError(err))
	} else {
		w.needsRecovery.Store(false)
		if inboxRec > 0 || flushRec > 0 {
			log.Printf("gateway inbox recovered stale processing items: inbox=%d session_flush=%d", inboxRec, flushRec)
		}
	}
	if pruned, err := w.PruneSucceededPayloads(context.Background(), w.pruneRetention()); err != nil {
		log.Printf("gateway inbox prune succeeded payloads: %s", sanitizeError(err))
	} else if pruned > 0 {
		log.Printf("gateway inbox pruned succeeded payloads: count=%d", pruned)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run != nil {
		select {
		case <-w.run.done:
		default:
			return
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &inboxRun{cancel: cancel, drain: make(chan struct{}), done: make(chan struct{})}
	w.run = run
	go func() {
		defer close(run.done)
		defer cancel()
		var group sync.WaitGroup
		for range w.cfg.InboxWorkers {
			group.Go(func() { w.runLoop(ctx, run.drain) })
		}
		group.Wait()
	}()
}

func (w *InboxWorker) runLoop(ctx context.Context, drain <-chan struct{}) {
	timer := time.NewTicker(w.cfg.InboxPollInterval)
	defer timer.Stop()
	for ctx.Err() == nil {
		worked, err := w.ProcessOnce(ctx)
		if err != nil {
			log.Printf("gateway inbox: %s", sanitizeError(err))
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-drain:
			return
		case <-timer.C:
		case <-w.wake:
		}
	}
}

func (w *InboxWorker) Stop(ctx context.Context) error {
	w.mu.Lock()
	run := w.run
	if run != nil && !run.stopping {
		run.stopping = true
		close(run.drain)
	}
	w.mu.Unlock()
	if run == nil {
		return nil
	}
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		run.cancel()
		grace := time.NewTimer(2 * time.Second)
		defer grace.Stop()
		select {
		case <-run.done:
		case <-grace.C:
		}
		return ctx.Err()
	}
}

func (w *InboxWorker) ScheduleSessionFlushJob(ctx context.Context, projectID, datasetID, sessionID string) (FlushScheduleStatus, error) {
	datasetID = strings.TrimSpace(datasetID)
	sessionID = strings.TrimSpace(sessionID)
	if datasetID == "" || sessionID == "" {
		return FlushInvalid, nil
	}
	if w.isStopping() {
		return FlushStopping, nil
	}
	_, queued, err := w.cfg.Store.EnqueueSessionFlushJob(ctx, projectID, datasetID, sessionID)
	if err != nil {
		return "", err
	}
	if !queued {
		return FlushDeduplicated, nil
	}
	w.Notify()
	return FlushScheduled, nil
}

func (w *InboxWorker) ScheduleSessionFlush(datasetID, sessionID string) bool {
	status, err := w.ScheduleSessionFlushJob(context.Background(), "", datasetID, sessionID)
	return err == nil && status == FlushScheduled
}

func (w *InboxWorker) hasQueuedSessionFlushes() bool {
	has, err := w.cfg.Store.HasReadySessionFlushJobs(context.Background(), w.clock())
	return err == nil && has
}

func (w *InboxWorker) recordDatasetExtract(datasetID string) {
	if w.cfg.DatasetExtractCooldown <= 0 || datasetID == "" {
		return
	}
	w.mu.Lock()
	if w.datasetExtractCooldown == nil {
		w.datasetExtractCooldown = make(map[string]time.Time)
	}
	w.datasetExtractCooldown[datasetID] = w.clock().Add(w.cfg.DatasetExtractCooldown)
	w.mu.Unlock()
}

func (w *InboxWorker) clearDatasetExtractCooldown(datasetID string) {
	w.mu.Lock()
	if datasetID == "" {
		clear(w.datasetExtractCooldown)
	} else {
		delete(w.datasetExtractCooldown, datasetID)
	}
	w.mu.Unlock()
}

func (w *InboxWorker) coolingDatasetsSnapshot() []string {
	if w.cfg.DatasetExtractCooldown <= 0 {
		return nil
	}
	now := w.clock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.datasetExtractCooldown) == 0 {
		return nil
	}
	var out []string
	for ds, until := range w.datasetExtractCooldown {
		if now.Before(until) {
			out = append(out, ds)
		} else {
			delete(w.datasetExtractCooldown, ds)
		}
	}
	slices.Sort(out)
	return out
}

func mergeDatasetSets(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func (w *InboxWorker) activeDatasetsSnapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.activeDatasets) == 0 {
		return nil
	}
	out := make([]string, 0, len(w.activeDatasets))
	for ds := range w.activeDatasets {
		out = append(out, ds)
	}
	slices.Sort(out)
	return out
}

func (w *InboxWorker) markDatasetActive(datasetID string) {
	w.mu.Lock()
	if w.activeDatasets == nil {
		w.activeDatasets = make(map[string]bool)
	}
	w.activeDatasets[datasetID] = true
	w.mu.Unlock()
}

func (w *InboxWorker) releaseDataset(datasetID string) {
	w.mu.Lock()
	delete(w.activeDatasets, datasetID)
	stopping := w.run != nil && w.run.stopping
	w.mu.Unlock()
	if !stopping {
		w.Notify()
	}
}

func (w *InboxWorker) sessionHasInFlightInbox(ctx context.Context, datasetID, sessionID string) (bool, error) {
	var exists bool
	err := w.cfg.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM capture_inbox WHERE dataset_id=? AND session_id=? AND status IN ('pending','processing','failed'))`,
		datasetID, sessionID).Scan(&exists)
	return exists, err
}

func (w *InboxWorker) sessionNeedsConsolidation(ctx context.Context, datasetID, sessionID string) (bool, error) {
	var exists bool
	err := w.cfg.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM capture_receipts WHERE dataset_id=? AND session_id=?
			UNION ALL
			SELECT 1 FROM capture_inbox WHERE dataset_id=? AND session_id=? AND status IN ('pending','processing','succeeded')
		)`,
		datasetID, sessionID, datasetID, sessionID).Scan(&exists)
	return exists, err
}

func (w *InboxWorker) flushSession(ctx context.Context, job domain.SessionFlushJob) error {
	needs, err := w.sessionNeedsConsolidation(ctx, job.DatasetID, job.SessionID)
	if err != nil {
		return fmt.Errorf("check session consolidation: %w", err)
	}
	if !needs {
		return nil
	}
	model, err := w.expectedDigest(ctx)
	if err != nil {
		return err
	}
	extracted, err := w.cfg.Engine.Extract(ctx, engine.ExtractRequest{
		Scope:               engine.Scope{DatasetID: job.DatasetID},
		DocIDs:              []string{job.SessionID},
		ExpectedModelDigest: model,
		OutputLanguage:      w.cfg.OutputLanguage,
		Mode:                engine.ExtractModeDefault,
	})
	if err != nil {
		if isRetryableEnginePause(err) {
			w.advanceExtractBackoff()
			return fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
		}
		return fmt.Errorf("extract session end: %w", err)
	}
	if !extracted.OK {
		return errors.New("engine did not complete session-end extraction")
	}
	w.resetExtractBackoff()
	w.recordDatasetExtract(job.DatasetID)
	if err := w.syncDatasetCatalog(ctx, job.DatasetID); err != nil {
		return &catalogPublishError{datasetID: job.DatasetID, projectID: job.ProjectID, err: err}
	}
	return nil
}

func (w *InboxWorker) syncDatasetCatalog(ctx context.Context, datasetID string) error {
	if w.cfg.Projector == nil {
		return nil
	}
	if w.server != nil {
		_, err := w.server.SyncDatasetCatalog(ctx, datasetID)
		return err
	}
	return nil
}

func (w *InboxWorker) syncDatasetSessions(ctx context.Context, datasetID string) error {
	if w.cfg.Projector == nil {
		return nil
	}
	if w.server != nil {
		_, err := w.server.SyncDatasetSessions(ctx, datasetID)
		return err
	}
	return nil
}

func (w *InboxWorker) startedAtSnapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startedAt
}

func (w *InboxWorker) ProcessOnce(ctx context.Context) (bool, error) {
	// Pending catalog publishes read local store only (zero LLM calls), so process them
	// before checking model verification gate or extract backoff.
	w.claimMu.Lock()
	excluded := w.activeDatasetsSnapshot()
	pendingPub, pubErr := w.cfg.Store.NextPendingCatalogPublishExcludingDatasets(ctx, excluded)
	if pubErr == nil {
		w.markDatasetActive(pendingPub.DatasetID)
		w.claimMu.Unlock()
		defer w.releaseDataset(pendingPub.DatasetID)
		return w.finishPendingCatalogPublish(ctx, pendingPub)
	}
	w.claimMu.Unlock()
	if !errors.Is(pubErr, domain.ErrNotFound) {
		return false, pubErr
	}

	if w.server != nil && !w.server.managedEngineGateOpen() {
		return false, nil
	}
	if w.inExtractBackoff() {
		return false, nil
	}
	if _, gateErr := w.expectedDigest(ctx); errors.Is(gateErr, errExtractionPausedModelUnverified) {
		return false, nil
	} else if gateErr != nil {
		return false, gateErr
	}

	w.claimMu.Lock()
	excluded = w.activeDatasetsSnapshot()
	if w.needsRecovery.Load() && len(excluded) == 0 {
		if _, _, err := w.cfg.Store.RecoverStaleProcessingBefore(ctx, w.startedAtSnapshot()); err == nil {
			w.needsRecovery.Store(false)
		}
	}

	now := w.clock()
	flushExcluded := mergeDatasetSets(excluded, w.coolingDatasetsSnapshot())
	var flushPreferred []string
	if len(w.preferFlush) > 0 {
		excludedSet := make(map[string]bool, len(flushExcluded))
		for _, ds := range flushExcluded {
			excludedSet[ds] = true
		}
		for ds := range w.preferFlush {
			if !excludedSet[ds] {
				flushPreferred = append(flushPreferred, ds)
			}
		}
		slices.Sort(flushPreferred)
	}
	if len(flushPreferred) > 0 {
		job, flushErr := w.cfg.Store.ClaimNextSessionFlushInDatasetsAt(ctx, flushPreferred, now)
		if flushErr == nil {
			delete(w.preferFlush, job.DatasetID)
			w.markDatasetActive(job.DatasetID)
			w.claimMu.Unlock()
			defer w.releaseDataset(job.DatasetID)
			return w.finishSessionFlushJob(ctx, job)
		}
		if errors.Is(flushErr, domain.ErrNotFound) {
			for _, ds := range flushPreferred {
				delete(w.preferFlush, ds)
			}
		} else {
			w.claimMu.Unlock()
			return false, flushErr
		}
	}

	item, err := w.cfg.Store.ClaimNextInboxItemExcludingDatasets(ctx, excluded)
	if err == nil {
		if w.preferFlush == nil {
			w.preferFlush = make(map[string]bool)
		}
		w.preferFlush[item.DatasetID] = true
		w.markDatasetActive(item.DatasetID)
		w.claimMu.Unlock()
		defer w.releaseDataset(item.DatasetID)
		return w.finishInboxItem(ctx, item)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		w.claimMu.Unlock()
		return false, err
	}

	job, flushErr := w.cfg.Store.ClaimNextSessionFlushExcludingDatasetsAt(ctx, flushExcluded, now)
	if flushErr == nil {
		delete(w.preferFlush, job.DatasetID)
		w.markDatasetActive(job.DatasetID)
		w.claimMu.Unlock()
		defer w.releaseDataset(job.DatasetID)
		return w.finishSessionFlushJob(ctx, job)
	}
	w.claimMu.Unlock()
	if errors.Is(flushErr, domain.ErrNotFound) {
		return false, nil
	}
	return false, flushErr
}

func (w *InboxWorker) finishPendingCatalogPublish(ctx context.Context, st domain.CatalogPublishState) (bool, error) {
	err := w.syncDatasetCatalog(ctx, st.DatasetID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, context.Canceled) && w.isStopping() {
		return false, nil
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if markErr := w.cfg.Store.MarkCatalogPublishFailed(finish, st.DatasetID, st.ProjectID, sanitizeError(err)); markErr != nil {
		return true, errors.Join(err, markErr)
	}
	return true, nil
}

func (w *InboxWorker) pruneInterval() int {
	if w.cfg.InboxPruneInterval > 0 {
		return w.cfg.InboxPruneInterval
	}
	return 64
}

func (w *InboxWorker) pruneRetention() time.Duration {
	if w.cfg.InboxPruneRetention > 0 {
		return w.cfg.InboxPruneRetention
	}
	return 24 * time.Hour
}

func (w *InboxWorker) finishInboxItem(ctx context.Context, item domain.CaptureInboxItem) (bool, error) {
	if w.cfg.SessionExtractQuietWindow <= 0 &&
		item.Kind == string(engine.IngestKindCaptureQA) && item.SessionID != "" {
		var primary inboxPayload
		if json.Unmarshal([]byte(item.PayloadJSON), &primary) == nil &&
			(primary.ExtractMode == "" || primary.ExtractMode == engine.ExtractModeDefault) {
			if siblings, claimErr := w.cfg.Store.ClaimPendingSessionQAItems(ctx, item.DatasetID, item.SessionID, maxDirectSessionQABatch-1); claimErr == nil && len(siblings) > 0 {
				var matching []domain.CaptureInboxItem
				for _, sib := range siblings {
					var p inboxPayload
					if json.Unmarshal([]byte(sib.PayloadJSON), &p) == nil &&
						(p.ExtractMode == "" || p.ExtractMode == engine.ExtractModeDefault) &&
						p.EngineUserID == primary.EngineUserID {
						matching = append(matching, sib)
					} else {
						requeueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
						_ = w.cfg.Store.RequeueInboxItem(requeueCtx, sib.ID)
						cancel()
					}
				}
				if len(matching) > 0 {
					return w.finishCoalescedSessionQABatch(ctx, append([]domain.CaptureInboxItem{item}, matching...), primary)
				}
			}
		}
	}
	err := w.process(ctx, item)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var pubErr *catalogPublishError
	if errors.As(err, &pubErr) {
		status := "failed"
		errText := sanitizeError(pubErr.err)
		if errors.Is(pubErr.err, context.Canceled) && w.isStopping() {
			status = "pending"
			errText = ""
		}
		if persistErr := w.cfg.Store.CompleteInboxWithCatalogPublishState(finish, item.ID, pubErr.datasetID, pubErr.projectID, status, errText); persistErr != nil {
			return true, errors.Join(pubErr, persistErr)
		}
		if status == "pending" {
			return false, nil
		}
		if w.hasQueuedSessionFlushes() {
			w.Notify()
		}
		return true, nil
	}
	if errors.Is(err, errExtractionPausedModelUnverified) || (errors.Is(err, context.Canceled) && w.isStopping()) {
		if requeueErr := w.cfg.Store.RequeueInboxItem(finish, item.ID); requeueErr != nil {
			return true, requeueErr
		}
		return false, nil
	}
	if err != nil {
		if persistErr := w.cfg.Store.MarkInboxFailed(finish, item.ID, sanitizeError(err)); persistErr != nil {
			return true, errors.Join(err, persistErr)
		}
		if w.hasQueuedSessionFlushes() {
			w.Notify()
		}
		return true, nil
	}
	if err := w.cfg.Store.MarkInboxSucceeded(finish, item.ID); err != nil {
		return true, err
	}
	if n := w.completedSincePrune.Add(1); n%int64(w.pruneInterval()) == 0 {
		if _, pruneErr := w.PruneSucceededPayloads(finish, w.pruneRetention()); pruneErr != nil {
			log.Printf("gateway inbox auto prune succeeded payloads: %s", sanitizeError(pruneErr))
		}
	}
	if w.hasQueuedSessionFlushes() {
		w.Notify()
	}
	return true, nil
}

func (w *InboxWorker) finishCoalescedSessionQABatch(ctx context.Context, batch []domain.CaptureInboxItem, primary inboxPayload) (bool, error) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	requeueAll := func(items []domain.CaptureInboxItem) (bool, error) {
		var combined error
		for _, it := range items {
			combined = errors.Join(combined, w.cfg.Store.RequeueInboxItem(finish, it.ID))
		}
		return combined != nil, combined
	}

	ingested := make([]domain.CaptureInboxItem, 0, len(batch))
	for i, it := range batch {
		var payload inboxPayload
		if err := json.Unmarshal([]byte(it.PayloadJSON), &payload); err != nil {
			_ = w.cfg.Store.MarkInboxFailed(finish, it.ID, sanitizeError(fmt.Errorf("decode inbox: %w", err)))
			continue
		}
		if len(payload.Content) == 0 || string(payload.Content) == "null" {
			_ = w.cfg.Store.MarkInboxFailed(finish, it.ID, sanitizeError(errors.New("inbox content missing")))
			continue
		}
		if _, _, _, err := w.ingestInboxItem(ctx, it, payload, engine.ExtractModeDefault); err != nil {
			if errors.Is(err, errExtractionPausedModelUnverified) || (errors.Is(err, context.Canceled) && w.isStopping()) {
				return requeueAll(append(ingested, batch[i:]...))
			}
			_ = w.cfg.Store.MarkInboxFailed(finish, it.ID, sanitizeError(err))
			continue
		}
		ingested = append(ingested, it)
	}
	if len(ingested) == 0 {
		if w.hasQueuedSessionFlushes() {
			w.Notify()
		}
		return true, nil
	}
	model, err := w.expectedDigest(ctx)
	if err != nil {
		if errors.Is(err, errExtractionPausedModelUnverified) || (errors.Is(err, context.Canceled) && w.isStopping()) {
			return requeueAll(ingested)
		}
		var combined error
		for _, it := range ingested {
			combined = errors.Join(combined, w.cfg.Store.MarkInboxFailed(finish, it.ID, sanitizeError(err)))
		}
		return true, combined
	}
	head := ingested[0]
	extracted, err := w.cfg.Engine.Extract(ctx, engine.ExtractRequest{
		Scope:               engine.Scope{DatasetID: head.DatasetID, UserID: primary.EngineUserID},
		DocIDs:              []string{head.SessionID},
		ExpectedModelDigest: model,
		OutputLanguage:      w.cfg.OutputLanguage,
		Mode:                engine.ExtractModeDefault,
	})
	if err != nil {
		if isRetryableEnginePause(err) {
			w.advanceExtractBackoff()
			return requeueAll(ingested)
		}
		if errors.Is(err, context.Canceled) && w.isStopping() {
			return requeueAll(ingested)
		}
		errText := sanitizeError(fmt.Errorf("extract inbox: %w", err))
		var combined error
		for _, it := range ingested {
			combined = errors.Join(combined, w.cfg.Store.MarkInboxFailed(finish, it.ID, errText))
		}
		if w.hasQueuedSessionFlushes() {
			w.Notify()
		}
		return true, combined
	}
	if !extracted.OK {
		errText := sanitizeError(errors.New("engine did not complete extraction"))
		var combined error
		for _, it := range ingested {
			combined = errors.Join(combined, w.cfg.Store.MarkInboxFailed(finish, it.ID, errText))
		}
		return true, combined
	}
	w.resetExtractBackoff()
	w.recordDatasetExtract(head.DatasetID)
	if pubErr := w.syncDatasetCatalog(ctx, head.DatasetID); pubErr != nil {
		status := "failed"
		errText := sanitizeError(pubErr)
		if errors.Is(pubErr, context.Canceled) && w.isStopping() {
			status = "pending"
			errText = ""
		}
		if persistErr := w.cfg.Store.CompleteInboxWithCatalogPublishState(finish, head.ID, head.DatasetID, head.ProjectID, status, errText); persistErr != nil {
			return true, errors.Join(pubErr, persistErr)
		}
		for _, it := range ingested[1:] {
			if markErr := w.cfg.Store.MarkInboxSucceeded(finish, it.ID); markErr != nil {
				return true, markErr
			}
		}
		if status == "pending" {
			return false, nil
		}
		if w.hasQueuedSessionFlushes() {
			w.Notify()
		}
		return true, nil
	}
	for _, it := range ingested {
		if err := w.cfg.Store.MarkInboxSucceeded(finish, it.ID); err != nil {
			return true, err
		}
		if n := w.completedSincePrune.Add(1); n%int64(w.pruneInterval()) == 0 {
			if _, pruneErr := w.PruneSucceededPayloads(finish, w.pruneRetention()); pruneErr != nil {
				log.Printf("gateway inbox auto prune succeeded payloads: %s", sanitizeError(pruneErr))
			}
		}
	}
	if w.hasQueuedSessionFlushes() {
		w.Notify()
	}
	return true, nil
}

func (w *InboxWorker) finishSessionFlushJob(ctx context.Context, job domain.SessionFlushJob) (bool, error) {
	err := w.flushSession(ctx, job)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var pubErr *catalogPublishError
	if errors.As(err, &pubErr) {
		status := "failed"
		errText := sanitizeError(pubErr.err)
		if errors.Is(pubErr.err, context.Canceled) && w.isStopping() {
			status = "pending"
			errText = ""
		}
		requeued, persistErr := w.cfg.Store.CompleteSessionFlushWithCatalogPublishState(finish, job.ID, pubErr.datasetID, pubErr.projectID, status, errText)
		if persistErr != nil {
			return true, errors.Join(pubErr, persistErr)
		}
		if status == "pending" {
			return false, nil
		}
		if requeued {
			w.Notify()
		}
		return true, nil
	}
	if errors.Is(err, errExtractionPausedModelUnverified) || (errors.Is(err, context.Canceled) && w.isStopping()) {
		if requeueErr := w.cfg.Store.RequeueSessionFlush(finish, job.ID); requeueErr != nil {
			return true, requeueErr
		}
		return false, nil
	}
	if err != nil {
		requeued, persistErr := w.cfg.Store.MarkSessionFlushFailed(finish, job.ID, sanitizeError(err))
		if persistErr != nil {
			return true, errors.Join(err, persistErr)
		}
		if requeued {
			w.Notify()
		}
		return true, nil
	}
	requeued, err := w.cfg.Store.MarkSessionFlushSucceeded(finish, job.ID)
	if err != nil {
		return true, err
	}
	if requeued {
		w.Notify()
	}
	return true, nil
}

func (w *InboxWorker) ingestInboxItem(ctx context.Context, item domain.CaptureInboxItem, payload inboxPayload, mode engine.ExtractMode) (docID, ingestDocID string, alreadySaved bool, err error) {
	// Gateway already enforced project ACL before enqueueing; Hook auto-capture and legacy v4 rows
	// leave EngineUserID empty (owner scope), while trusted MCP fallback sets EngineUserID explicitly.
	scope := engine.Scope{DatasetID: item.DatasetID, UserID: payload.EngineUserID}
	key := payload.IdempotencyKey
	if key == "" {
		key = item.CaptureKey
	}
	if key == "" {
		key = item.ID
	}
	if item.CaptureKey != "" {
		receipt, rErr := w.cfg.Store.GetCaptureReceipt(ctx, item.CaptureKey)
		if rErr == nil && receipt.Status == "saved" && receipt.EntryID != "" {
			alreadySaved = true
			if mode == engine.ExtractModeDefault && item.SessionID != "" &&
				(item.Kind == string(engine.IngestKindCaptureQA) || item.Kind == string(engine.IngestKindCaptureTrace)) {
				docID = item.SessionID
			} else {
				docID = receipt.EntryID
			}
			return docID, "", true, nil
		} else if rErr != nil && !errors.Is(rErr, domain.ErrNotFound) {
			return "", "", false, fmt.Errorf("lookup capture receipt: %w", rErr)
		}
	}
	result, err := w.cfg.Engine.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKind(item.Kind),
		IdempotencyKey: key, Content: payload.Content, SourceMeta: payload.SourceMeta})
	if err != nil {
		if isRetryableEnginePause(err) {
			w.advanceExtractBackoff()
			return "", "", false, fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
		}
		return "", "", false, fmt.Errorf("ingest inbox: %w", err)
	}
	if !result.OK || result.DocumentID == "" && result.EntryID == "" {
		return "", "", false, errors.New("engine did not accept inbox item")
	}
	w.resetExtractBackoff()
	ingestDocID = result.DocumentID
	docID = result.DocumentID
	if docID == "" {
		docID = result.EntryID
	}
	if item.CaptureKey != "" {
		entry := result.EntryID
		if entry == "" {
			entry = docID
		}
		if err := w.cfg.Store.MarkCaptureReceiptSaved(ctx, item.CaptureKey, entry); err != nil {
			return "", "", false, err
		}
	}
	return docID, ingestDocID, false, nil
}

func (w *InboxWorker) process(ctx context.Context, item domain.CaptureInboxItem) error {
	var payload inboxPayload
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode inbox: %w", err)
	}
	if len(payload.Content) == 0 || string(payload.Content) == "null" {
		return errors.New("inbox content missing")
	}
	mode := payload.ExtractMode
	if mode == "" {
		mode = engine.ExtractModeDefault
	}
	scope := engine.Scope{DatasetID: item.DatasetID, UserID: payload.EngineUserID}
	docID, ingestDocID, alreadySaved, err := w.ingestInboxItem(ctx, item, payload, mode)
	if err != nil {
		return err
	}
	isTraceOnly := item.Kind == string(engine.IngestKindCaptureTrace) && mode == engine.ExtractModeDefault
	// Owner-scoped auto-captured Q&As defer heavy graph extraction into the per-session
	// debounced flush job so active sessions and backlogs coalesce into one improve() call.
	deferSessionQA := w.cfg.SessionExtractQuietWindow > 0 &&
		item.Kind == string(engine.IngestKindCaptureQA) &&
		mode == engine.ExtractModeDefault &&
		item.SessionID != "" &&
		payload.EngineUserID == ""
	if deferSessionQA {
		if _, readyNow, flushErr := w.cfg.Store.ScheduleDebouncedSessionFlush(
			ctx, item.ProjectID, item.DatasetID, item.SessionID, w.clock(),
			w.cfg.SessionExtractQuietWindow, w.cfg.SessionExtractMaxWait, w.cfg.SessionExtractBatchSize,
		); flushErr != nil {
			return fmt.Errorf("schedule debounced session flush: %w", flushErr)
		} else if readyNow {
			w.Notify()
		}
		if err := w.syncDatasetSessions(ctx, item.DatasetID); err != nil {
			return &catalogPublishError{datasetID: item.DatasetID, projectID: item.ProjectID, err: err}
		}
		return nil
	}
	if !isTraceOnly {
		model, err := w.expectedDigest(ctx)
		if err != nil {
			return err
		}
		if !alreadySaved && mode == engine.ExtractModeDefault && ingestDocID == "" && item.SessionID != "" &&
			item.Kind == string(engine.IngestKindCaptureQA) {
			docID = item.SessionID
		}
		extracted, err := w.cfg.Engine.Extract(ctx, engine.ExtractRequest{Scope: scope, DocIDs: []string{docID},
			ExpectedModelDigest: model, OutputLanguage: w.cfg.OutputLanguage, Mode: mode})
		if err != nil {
			if isRetryableEnginePause(err) {
				w.advanceExtractBackoff()
				return fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
			}
			return fmt.Errorf("extract inbox: %w", err)
		}
		if !extracted.OK {
			return errors.New("engine did not complete extraction")
		}
		w.resetExtractBackoff()
		w.recordDatasetExtract(item.DatasetID)
	}
	if isTraceOnly {
		if err := w.syncDatasetSessions(ctx, item.DatasetID); err != nil {
			return &catalogPublishError{datasetID: item.DatasetID, projectID: item.ProjectID, err: err}
		}
	} else {
		if err := w.syncDatasetCatalog(ctx, item.DatasetID); err != nil {
			return &catalogPublishError{datasetID: item.DatasetID, projectID: item.ProjectID, err: err}
		}
	}
	return nil
}

func isRetryableModelConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, engine.ErrModelDigestMismatch) || errors.Is(err, engine.ErrConflict) {
		return true
	}
	var engErr *engine.EngineError
	if errors.As(err, &engErr) && engErr != nil && engErr.StatusCode == http.StatusConflict {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "generation_handoff_in_progress") ||
		strings.Contains(msg, "generation_handoff_busy") ||
		strings.Contains(msg, "model_digest_mismatch")
}

func isRetryableEnginePause(err error) bool {
	if err == nil {
		return false
	}
	// Request timeouts (extract_timeout / engine_timeout) must surface as failed items for manual retry.
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isRetryableModelConflict(err) {
		return true
	}
	var engErr *engine.EngineError
	if errors.As(err, &engErr) && engErr != nil {
		if engErr.Code == "official_extract_failed" {
			return false
		}
		if engErr.StatusCode == http.StatusServiceUnavailable ||
			engErr.Code == "generation_not_configured" ||
			engErr.Code == "engine_unavailable" {
			return true
		}
		// HTTP 500 (e.g. CogneeSystemError during extraction) and other non-503 HTTP errors
		// must transition the item to failed rather than spinning forever in pending.
		if engErr.StatusCode > 0 {
			return false
		}
	}
	if errors.Is(err, engine.ErrUnavailable) {
		return true
	}
	return strings.Contains(err.Error(), "generation_not_configured")
}

func (w *InboxWorker) inExtractBackoff() bool {
	w.extractBackoffMu.Lock()
	defer w.extractBackoffMu.Unlock()
	return !w.extractBackoffNext.IsZero() && time.Now().Before(w.extractBackoffNext)
}

func (w *InboxWorker) advanceExtractBackoff() {
	w.extractBackoffMu.Lock()
	defer w.extractBackoffMu.Unlock()
	if w.extractBackoffDur <= 0 {
		w.extractBackoffDur = time.Second
	} else {
		w.extractBackoffDur = min(w.extractBackoffDur*2, 15*time.Second)
	}
	w.extractBackoffNext = time.Now().Add(w.extractBackoffDur)
}

func (w *InboxWorker) resetExtractBackoff() {
	w.extractBackoffMu.Lock()
	w.extractBackoffNext = time.Time{}
	w.extractBackoffDur = 0
	w.extractBackoffMu.Unlock()
}

func (w *InboxWorker) expectedDigest(ctx context.Context) (string, error) {
	if w.server != nil {
		return w.server.expectedActiveModelDigest(ctx)
	}
	state, err := w.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(state.StartupError) != "" {
		return "", fmt.Errorf("%w: %s", errExtractionPausedModelUnverified, state.StartupError)
	}
	if state.ActiveProfileID == "" || state.ActiveRevision <= 0 {
		return "", fmt.Errorf("%w: no_active_profile", errExtractionPausedModelUnverified)
	}
	profile, err := w.cfg.Store.GetGenerationProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
	if err != nil || profile.Removed {
		return "", fmt.Errorf("%w: active_profile_unavailable", errExtractionPausedModelUnverified)
	}
	return profile.Digest, nil
}

func (w *InboxWorker) RetryFailed(ctx context.Context, projectID string) ([]string, error) {
	// 1. Prioritize retrying failed catalog publish states (zero LLM calls).
	failedPubs, err := w.cfg.Store.ListFailedCatalogPublishStates(ctx, projectID)
	if err != nil {
		return nil, err
	}
	retried := make([]string, 0, len(failedPubs))
	var publishRetryErr error
	for _, st := range failedPubs {
		if err := w.syncDatasetCatalog(ctx, st.DatasetID); err != nil {
			_ = w.cfg.Store.MarkCatalogPublishFailed(ctx, st.DatasetID, st.ProjectID, sanitizeError(err))
			publishRetryErr = errors.Join(publishRetryErr, err)
			continue
		}
		retried = append(retried, "catalog:"+st.DatasetID)
	}

	// 2. Retry failed capture_inbox and session_flush_jobs entries.
	query := `SELECT id FROM capture_inbox WHERE status='failed'`
	var args []any
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	ids, err := readRows(ctx, w.cfg.Store.DB(), query+` ORDER BY created_at,id`, args, func(row rowScanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return retried, err
	}
	flushQuery := `SELECT id FROM session_flush_jobs WHERE status='failed'`
	var flushArgs []any
	if projectID != "" {
		flushQuery += ` AND project_id=?`
		flushArgs = append(flushArgs, projectID)
	}
	flushIDs, err := readRows(ctx, w.cfg.Store.DB(), flushQuery+` ORDER BY created_at,id`, flushArgs, func(row rowScanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return retried, err
	}
	if projectID == "" {
		w.clearDatasetExtractCooldown("")
	} else if proj, pErr := w.cfg.Store.GetProject(ctx, projectID); pErr == nil {
		w.clearDatasetExtractCooldown(proj.DatasetID)
	}
	defer w.Notify()
	for _, id := range ids {
		if _, err := w.cfg.Store.RetryInboxItem(ctx, id); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				continue
			}
			return retried, err
		}
		retried = append(retried, id)
	}
	for _, id := range flushIDs {
		if _, err := w.cfg.Store.RetrySessionFlushJob(ctx, id); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				continue
			}
			return retried, err
		}
		retried = append(retried, id)
	}
	if publishRetryErr != nil {
		return retried, publishRetryErr
	}
	return retried, nil
}

func (w *InboxWorker) PruneSucceededPayloads(ctx context.Context, retention time.Duration) (int64, error) {
	olderThan := ""
	if retention > 0 {
		olderThan = time.Now().UTC().Add(-retention).Format("2006-01-02T15:04:05.000000+00:00")
	}
	return w.cfg.Store.PruneSucceededInboxPayloads(ctx, olderThan)
}

func (w *InboxWorker) Summary(ctx context.Context, projectID string) (InboxSummary, error) {
	return w.summary(ctx, w.cfg.Store.DB(), projectID)
}

func (w *InboxWorker) summary(ctx context.Context, db sqlRead, projectID string) (InboxSummary, error) {
	w.mu.Lock()
	run := w.run
	w.mu.Unlock()
	out := InboxSummary{Running: run != nil}
	if run != nil {
		select {
		case <-run.done:
			out.Running = false
		default:
		}
	}
	var where string
	var args []any
	if projectID != "" {
		where = " AND project_id=?"
		args = append(args, projectID)
	} else {
		where = " AND project_id IN (SELECT id FROM projects)"
	}
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='processing'),
		COUNT(*) FILTER(WHERE status='succeeded'),COUNT(*) FILTER(WHERE status='failed') FROM capture_inbox WHERE 1=1`+where, args...).
		Scan(&out.Pending, &out.Processing, &out.Succeeded, &out.Failed)
	if err != nil {
		return out, err
	}
	var flushPending, flushProcessing, flushFailed int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='processing'),
		COUNT(*) FILTER(WHERE status='failed') FROM session_flush_jobs WHERE 1=1`+where, args...).
		Scan(&flushPending, &flushProcessing, &flushFailed)
	if err != nil {
		return out, err
	}
	out.Pending += flushPending
	out.Processing += flushProcessing
	out.Failed += flushFailed

	var pubWhere string
	var pubArgs []any
	if projectID != "" {
		pubWhere = " WHERE (project_id=? OR dataset_id IN (SELECT dataset_id FROM projects WHERE id=? AND dataset_id<>''))"
		pubArgs = []any{projectID, projectID}
	} else {
		pubWhere = " WHERE (dataset_id IN (SELECT dataset_id FROM projects WHERE dataset_id<>''))"
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='failed')
		FROM catalog_publish_state`+pubWhere, pubArgs...).Scan(&out.CatalogPublishPending, &out.CatalogPublishFailed); err != nil {
		return out, err
	}
	if out.CatalogPublishFailed > 0 {
		var pubErr, pubErrAt string
		q := `SELECT last_error,last_error_at FROM catalog_publish_state` + pubWhere + ` AND status='failed' ORDER BY updated_at DESC,dataset_id LIMIT 1`
		if err := db.QueryRowContext(ctx, q, pubArgs...).Scan(&pubErr, &pubErrAt); err == nil {
			if pubErr != "" {
				out.CatalogPublishError = sanitizeError(errors.New(pubErr))
			}
			out.CatalogPublishErrorAt = pubErrAt
		} else if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}

	unionArgs := make([]any, 0, len(args)*2)
	unionArgs = append(unionArgs, args...)
	unionArgs = append(unionArgs, args...)
	out.RecentFailures, err = readRows(ctx, db, `SELECT id,project_id,last_error,attempts,updated_at,last_error_at FROM (
		SELECT id,project_id,last_error,attempts,updated_at,last_error_at FROM capture_inbox WHERE status='failed'`+where+`
		UNION ALL
		SELECT id,project_id,last_error,attempts,updated_at,last_error_at FROM session_flush_jobs WHERE status='failed'`+where+`
	) ORDER BY updated_at DESC,id LIMIT 20`, unionArgs, func(row rowScanner) (InboxFailure, error) {
		var f InboxFailure
		err := row.Scan(&f.ID, &f.ProjectID, &f.LastError, &f.Attempts, &f.UpdatedAt, &f.LastErrorAt)
		if f.LastError != "" {
			f.LastError = sanitizeError(errors.New(f.LastError))
			f.ErrorCode, f.Summary, f.NextAction = describeInboxFailure(f.LastError)
		}
		return f, err
	})
	if err != nil {
		return out, err
	}
	out.FilteredCount, err = domain.ReadCaptureFilterCount(ctx, db, projectID)
	if err != nil {
		return out, err
	}
	if out.FilteredCount > 0 {
		out.RecentFiltered, err = domain.ReadCaptureFilterEvents(ctx, db, projectID, 10)
	}
	return out, err
}

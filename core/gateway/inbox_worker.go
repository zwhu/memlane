package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const maxSessionFlushQueue = 256

var errExtractionPausedModelUnverified = errors.New("extraction paused: active model unverified")

type sessionFlushKey struct {
	datasetID string
	sessionID string
}

type InboxWorker struct {
	cfg                Config
	server             *Server
	wake               chan struct{}
	mu                 sync.Mutex
	run                *inboxRun
	flushQueued        map[sessionFlushKey]bool
	flushActive        map[sessionFlushKey]bool
	flushOrder         []sessionFlushKey
	extractBackoffMu   sync.Mutex
	extractBackoffNext time.Time
	extractBackoffDur  time.Duration
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

func (w *InboxWorker) Start(ctx context.Context) {
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
		return ctx.Err()
	}
}

func (w *InboxWorker) ScheduleSessionFlush(datasetID, sessionID string) bool {
	key := sessionFlushKey{
		datasetID: strings.TrimSpace(datasetID),
		sessionID: strings.TrimSpace(sessionID),
	}
	if key.datasetID == "" || key.sessionID == "" {
		return false
	}
	w.mu.Lock()
	if w.run != nil && w.run.stopping {
		select {
		case <-w.run.done:
		default:
			w.mu.Unlock()
			return false
		}
	}
	if w.flushQueued == nil {
		w.flushQueued = make(map[sessionFlushKey]bool)
	}
	if w.flushActive == nil {
		w.flushActive = make(map[sessionFlushKey]bool)
	}
	if w.flushQueued[key] || w.flushActive[key] || len(w.flushOrder) >= maxSessionFlushQueue {
		w.mu.Unlock()
		return false
	}
	w.flushQueued[key] = true
	w.flushOrder = append(w.flushOrder, key)
	w.mu.Unlock()
	w.Notify()
	return true
}

func (w *InboxWorker) hasQueuedSessionFlushes() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.flushOrder) > 0
}

func (w *InboxWorker) claimNextSessionFlush(ctx context.Context) (sessionFlushKey, bool, error) {
	w.mu.Lock()
	candidates := append([]sessionFlushKey(nil), w.flushOrder...)
	w.mu.Unlock()
	if len(candidates) == 0 {
		return sessionFlushKey{}, false, nil
	}
	for _, key := range candidates {
		inFlight, err := w.sessionHasInFlightInbox(ctx, key.datasetID, key.sessionID)
		if err != nil {
			return sessionFlushKey{}, false, err
		}
		if inFlight {
			continue
		}
		w.mu.Lock()
		if !w.flushQueued[key] || w.flushActive[key] {
			w.mu.Unlock()
			continue
		}
		delete(w.flushQueued, key)
		for i, item := range w.flushOrder {
			if item == key {
				w.flushOrder = append(w.flushOrder[:i], w.flushOrder[i+1:]...)
				break
			}
		}
		if w.flushActive == nil {
			w.flushActive = make(map[sessionFlushKey]bool)
		}
		w.flushActive[key] = true
		w.mu.Unlock()
		return key, true, nil
	}
	return sessionFlushKey{}, false, nil
}

func (w *InboxWorker) finishSessionFlush(key sessionFlushKey) {
	w.mu.Lock()
	delete(w.flushActive, key)
	w.mu.Unlock()
}

func (w *InboxWorker) sessionHasInFlightInbox(ctx context.Context, datasetID, sessionID string) (bool, error) {
	var exists bool
	err := w.cfg.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM capture_inbox WHERE dataset_id=? AND session_id=? AND status IN ('pending','processing'))`,
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

func (w *InboxWorker) flushSession(ctx context.Context, key sessionFlushKey) error {
	needs, err := w.sessionNeedsConsolidation(ctx, key.datasetID, key.sessionID)
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
		Scope:               engine.Scope{DatasetID: key.datasetID},
		DocIDs:              []string{key.sessionID},
		ExpectedModelDigest: model,
		OutputLanguage:      w.cfg.OutputLanguage,
		Mode:                engine.ExtractModeDefault,
	})
	if err != nil {
		if isRetryableModelConflict(err) {
			w.advanceExtractBackoff()
			return fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
		}
		return fmt.Errorf("extract session end: %w", err)
	}
	if !extracted.OK {
		return errors.New("engine did not complete session-end extraction")
	}
	w.resetExtractBackoff()
	if w.cfg.Projector != nil {
		if _, err := w.cfg.Projector.SyncCatalog(ctx, w.cfg.Store); err != nil {
			return fmt.Errorf("publish catalog: %w", err)
		}
	}
	return nil
}

func (w *InboxWorker) ProcessOnce(ctx context.Context) (bool, error) {
	if w.inExtractBackoff() {
		return false, nil
	}
	if _, gateErr := w.expectedDigest(ctx); errors.Is(gateErr, errExtractionPausedModelUnverified) {
		return false, nil
	} else if gateErr != nil {
		return false, gateErr
	}
	item, err := w.cfg.Store.ClaimNextInboxItem(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		key, ok, claimErr := w.claimNextSessionFlush(ctx)
		if claimErr != nil {
			return false, claimErr
		}
		if !ok {
			return false, nil
		}
		defer w.finishSessionFlush(key)
		if flushErr := w.flushSession(ctx, key); flushErr != nil {
			if errors.Is(flushErr, errExtractionPausedModelUnverified) {
				w.requeueSessionFlush(key)
				return false, nil
			}
			return true, flushErr
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	err = w.process(ctx, item)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if errors.Is(err, errExtractionPausedModelUnverified) {
		if requeueErr := w.requeueInboxItem(finish, item.ID); requeueErr != nil {
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
	if w.hasQueuedSessionFlushes() {
		w.Notify()
	}
	return true, nil
}

func (w *InboxWorker) requeueInboxItem(ctx context.Context, id string) error {
	return w.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE capture_inbox SET status='pending', updated_at=? WHERE id=? AND status='processing'`,
			domain.NowUTCMicro(), id)
		return err
	})
}

func (w *InboxWorker) requeueSessionFlush(key sessionFlushKey) {
	w.mu.Lock()
	if w.flushQueued == nil {
		w.flushQueued = make(map[sessionFlushKey]bool)
	}
	if !w.flushQueued[key] && len(w.flushOrder) < maxSessionFlushQueue {
		w.flushQueued[key] = true
		w.flushOrder = append(w.flushOrder, key)
	}
	w.mu.Unlock()
}

func (w *InboxWorker) process(ctx context.Context, item domain.CaptureInboxItem) error {
	var payload inboxPayload
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode inbox: %w", err)
	}
	if len(payload.Content) == 0 || string(payload.Content) == "null" {
		return errors.New("inbox content missing")
	}
	// Gateway already enforced project ACL before enqueueing; run stateless engine
	// writes under the owner scope so newly created datasets auto-provision cleanly.
	scope := engine.Scope{DatasetID: item.DatasetID}
	key := payload.IdempotencyKey
	if key == "" {
		key = item.CaptureKey
	}
	if key == "" {
		key = item.ID
	}
	mode := payload.ExtractMode
	if mode == "" {
		mode = engine.ExtractModeDefault
	}
	var docID string
	var ingestDocID string
	alreadySaved := false
	if item.CaptureKey != "" {
		receipt, err := w.cfg.Store.GetCaptureReceipt(ctx, item.CaptureKey)
		if err == nil && receipt.Status == "saved" && receipt.EntryID != "" {
			alreadySaved = true
			if mode == engine.ExtractModeDefault && item.SessionID != "" &&
				(item.Kind == string(engine.IngestKindCaptureQA) || item.Kind == string(engine.IngestKindCaptureTrace)) {
				docID = item.SessionID
			} else {
				docID = receipt.EntryID
			}
		} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("lookup capture receipt: %w", err)
		}
	}
	if !alreadySaved {
		result, err := w.cfg.Engine.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKind(item.Kind),
			IdempotencyKey: key, Content: payload.Content, SourceMeta: payload.SourceMeta})
		if err != nil {
			if isRetryableModelConflict(err) {
				w.advanceExtractBackoff()
				return fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
			}
			return fmt.Errorf("ingest inbox: %w", err)
		}
		if !result.OK || result.DocumentID == "" && result.EntryID == "" {
			return errors.New("engine did not accept inbox item")
		}
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
				return err
			}
		}
	}
	if !(item.Kind == string(engine.IngestKindCaptureTrace) && mode == engine.ExtractModeDefault) {
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
			if isRetryableModelConflict(err) {
				w.advanceExtractBackoff()
				return fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
			}
			return fmt.Errorf("extract inbox: %w", err)
		}
		if !extracted.OK {
			return errors.New("engine did not complete extraction")
		}
		w.resetExtractBackoff()
	}
	if w.cfg.Projector != nil {
		if _, err := w.cfg.Projector.SyncCatalog(ctx, w.cfg.Store); err != nil {
			return fmt.Errorf("publish catalog: %w", err)
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
		return nil, err
	}
	retried := make([]string, 0, len(ids))
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
	return retried, nil
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
	}
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='processing'),
		COUNT(*) FILTER(WHERE status='succeeded'),COUNT(*) FILTER(WHERE status='failed') FROM capture_inbox WHERE 1=1`+where, args...).
		Scan(&out.Pending, &out.Processing, &out.Succeeded, &out.Failed)
	if err != nil {
		return out, err
	}
	out.RecentFailures, err = readRows(ctx, db, `SELECT id,project_id,last_error,attempts,updated_at,last_error_at FROM capture_inbox
		WHERE status='failed'`+where+` ORDER BY updated_at DESC,id LIMIT 20`, args, func(row rowScanner) (InboxFailure, error) {
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

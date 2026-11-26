package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type InboxWorker struct {
	cfg  Config
	wake chan struct{}
	mu   sync.Mutex
	run  *inboxRun
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

func (w *InboxWorker) ProcessOnce(ctx context.Context) (bool, error) {
	item, err := w.cfg.Store.ClaimNextInboxItem(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = w.process(ctx, item)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		if persistErr := w.cfg.Store.MarkInboxFailed(finish, item.ID, sanitizeError(err)); persistErr != nil {
			return true, errors.Join(err, persistErr)
		}
		return true, nil
	}
	return true, w.cfg.Store.MarkInboxSucceeded(finish, item.ID)
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
	result, err := w.cfg.Engine.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKind(item.Kind),
		IdempotencyKey: key, Content: payload.Content, SourceMeta: payload.SourceMeta})
	if err != nil {
		return fmt.Errorf("ingest inbox: %w", err)
	}
	if !result.OK || result.DocumentID == "" && result.EntryID == "" {
		return errors.New("engine did not accept inbox item")
	}
	docID := result.DocumentID
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
	mode := payload.ExtractMode
	if mode == "" {
		mode = engine.ExtractModeDefault
	}
	if !(item.Kind == string(engine.IngestKindCaptureTrace) && mode == engine.ExtractModeDefault) {
		model, err := w.expectedDigest(ctx)
		if err != nil {
			return err
		}
		if mode == engine.ExtractModeDefault && result.DocumentID == "" && item.SessionID != "" &&
			item.Kind == string(engine.IngestKindCaptureQA) {
			docID = item.SessionID
		}
		extracted, err := w.cfg.Engine.Extract(ctx, engine.ExtractRequest{Scope: scope, DocIDs: []string{docID},
			ExpectedModelDigest: model, OutputLanguage: w.cfg.OutputLanguage, Mode: mode})
		if err != nil {
			return fmt.Errorf("extract inbox: %w", err)
		}
		if !extracted.OK {
			return errors.New("engine did not complete extraction")
		}
	}
	if w.cfg.Projector != nil {
		if _, err := w.cfg.Projector.SyncCatalog(ctx, w.cfg.Store); err != nil {
			return fmt.Errorf("publish catalog: %w", err)
		}
	}
	return nil
}

func (w *InboxWorker) expectedDigest(ctx context.Context) (string, error) {
	var statusErr error
	if w.cfg.Models != nil {
		status, err := w.cfg.Models.ModelStatus(ctx)
		if err == nil && status.OK && status.ModelDigest != "" {
			return status.ModelDigest, nil
		}
		statusErr = err
	}
	health, healthErr := w.cfg.Engine.Health(ctx)
	if healthErr == nil && health.OK && health.ModelDigest != "" {
		return health.ModelDigest, nil
	}
	state, err := w.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		return "", errors.Join(statusErr, healthErr, err)
	}
	if state.ActiveProfileID != "" {
		profile, err := w.cfg.Store.GetGenerationProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
		if err != nil {
			return "", errors.Join(statusErr, healthErr, err)
		}
		return profile.Digest, nil
	}
	return "", errors.Join(statusErr, healthErr)
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

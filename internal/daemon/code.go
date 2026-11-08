package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

// The code loop is one bounded source scan/build at a time. It only enumerates
// persisted, explicitly enabled worktrees; user stop cancels this lifecycle.
func (s *Server) runCode(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		states, err := s.store.Codes(ctx)
		if err == nil {
			for _, state := range states {
				if ctx.Err() != nil {
					break
				}
				if state.Enabled {
					s.refreshCode(ctx, state)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.codeWake:
		}
	}
}
func (s *Server) codeAction(ctx context.Context, a records.CodeAction) (records.CodeState, error) {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	if s.codeStopping {
		return records.CodeState{}, errors.New("service is stopping")
	}
	out, e := s.store.CodeAction(ctx, a)
	if e != nil {
		return out, e
	}
	if out.Replayed {
		return out, nil
	}
	if cancel := s.codeRuns[out.ID]; cancel != nil {
		cancel()
	}
	select {
	case s.codeWake <- struct{}{}:
	default:
	}
	return out, nil
}
func (s *Server) refreshCode(ctx context.Context, c records.CodeState) {
	s.codeMu.Lock()
	current, err := s.store.Code(ctx, c.ID)
	if err != nil || s.codeStopping || !current.Enabled || current.Generation != c.Generation || s.codeRuns[c.ID] != nil {
		s.codeMu.Unlock()
		return
	}
	c = current
	// An unavailable source is retried on explicit refresh, not every poll. A
	// failed extraction with a captured digest may retry after actual changes.
	if c.State == "failed" && c.ObservedDigest == "" {
		s.codeMu.Unlock()
		return
	}
	run, cancel := context.WithTimeout(ctx, 120*time.Second)
	if s.codeRuns == nil {
		s.codeRuns = make(map[string]context.CancelFunc)
	}
	s.codeRuns[c.ID] = cancel
	s.codeMu.Unlock()
	defer func() { cancel(); s.codeMu.Lock(); delete(s.codeRuns, c.ID); s.codeMu.Unlock() }()
	capture := codegraph.Capture
	if s.codeCapture != nil {
		capture = s.codeCapture
	}
	m, err := capture(run, c.Worktree, "")
	if err != nil {
		if run.Err() == nil {
			_ = s.store.CodeFailure(ctx, c, "source_unavailable")
		}
		return
	}
	if m.CommonDir != c.CommonDir {
		_ = s.store.CodeFailure(ctx, c, "workspace_mismatch")
		return
	}
	if c.State != "queued" && c.ObservedDigest == m.Digest {
		return
	}
	s.mu.Lock()
	worker := s.worker
	s.mu.Unlock()
	if worker == nil {
		return
	}
	if worker.CodeRecipe != "enola-0.4.30-v351" {
		_ = s.store.CodeFailure(ctx, c, "code_component_required")
		return
	}
	if !filepath.IsAbs(s.config.CodeExtractor) {
		_ = s.store.CodeFailure(ctx, c, "extractor_component_required")
		return
	}
	s.codeMu.Lock()
	if s.codeStopping {
		s.codeMu.Unlock()
		cancel()
		return
	}
	j, err := s.store.StartCodeJob(run, c, m)
	s.codeMu.Unlock()
	if err != nil {
		cancel()
		return
	}
	base := filepath.Join(s.config.Root, "code-extraction")
	if err = os.MkdirAll(base, 0700); err != nil {
		s.finishCodePublication(ctx, j, nil, "output_unavailable")
		return
	}
	private := filepath.Join(base, j.ID)
	defer os.RemoveAll(private)
	db := filepath.Join(s.config.Root, "memlane.sqlite")
	extracted, err := codegraph.Extract(run, codegraph.ExtractOptions{GuardExecutable: s.proposalGuard, Executable: s.config.CodeExtractor, Worktree: c.Worktree, Directory: private, Dataset: c.Dataset, DenyReads: []string{db, db + "-wal", db + "-shm", filepath.Join(s.config.Root, "backups"), s.config.Worker.DataDir}})
	cause := ""
	var receipt *codegraph.Receipt
	if err != nil {
		cause = "extraction_failed"
	} else if extracted.Manifest.Digest != m.Digest {
		cause = "source_changed"
	} else {
		select {
		case s.gate <- struct{}{}:
			r, e := worker.CodeBuild(run, extracted.Graph)
			<-s.gate
			if e != nil {
				cause = "graph_failed"
			} else {
				receipt = &r
			}
		case <-run.Done():
			cause = "cancelled"
		}
	}
	if cause == "" {
		live, e := codegraph.Capture(run, c.Worktree, "")
		if e != nil || live.Digest != m.Digest {
			cause = "source_changed"
			receipt = nil
		}
	}
	if run.Err() != nil {
		cause = "cancelled"
		receipt = nil
	}
	s.finishCodePublication(ctx, j, receipt, cause)
}

// Retry only the short receipt transaction, never the extractor/model. An
// unrecoverable publication records failure; an unwritable database ends this
// service owner so startup can interrupt/recover the persisted running job.
func (s *Server) finishCodePublication(owner context.Context, j records.CodeJob, receipt *codegraph.Receipt, cause string) {
	for attempt := 0; attempt < 3; attempt++ {
		finish, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.codeMu.Lock()
		if owner.Err() != nil || s.codeStopping {
			cause = "interrupted"
			receipt = nil
		}
		err := s.store.FinishCodeJob(finish, j, receipt, cause)
		s.codeMu.Unlock()
		cancel()
		if err == nil || errors.Is(err, store.ErrConflict) {
			return
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(50*(attempt+1)) * time.Millisecond)
			select {
			case <-timer.C:
			case <-owner.Done():
				timer.Stop()
			}
		}
	}
	finish, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.codeMu.Lock()
	terminal := "publication_failed"
	if owner.Err() != nil || s.codeStopping {
		terminal = "interrupted"
	}
	err := s.store.FinishCodeJob(finish, j, nil, terminal)
	s.codeMu.Unlock()
	if err != nil && !errors.Is(err, store.ErrConflict) && s.stop != nil {
		s.stop()
	}
}
func (s *Server) queryCode(ctx context.Context, q records.CodeQuery) (records.CodeResult, error) {
	var out records.CodeResult
	s.codeMu.Lock()
	stopping := s.codeStopping
	s.codeMu.Unlock()
	if stopping {
		return out, errors.New("service is stopping")
	}
	c, e := s.store.Code(ctx, q.ID)
	if e != nil {
		return out, e
	}
	if !c.Enabled {
		return out, &records.AccessError{Code: "code_disabled", Message: "enable code graph in Memlane"}
	}
	if c.Snapshot == "" || c.Manifest == nil {
		return out, &records.AccessError{Code: "code_not_ready", Message: "code snapshot is not ready"}
	}
	before, e := codegraph.Capture(ctx, c.Worktree, "")
	if e != nil {
		return out, e
	}
	if before.Digest != c.Manifest.Digest {
		_ = s.store.MarkCodeStale(ctx, c.ID, c.Generation)
		return out, &records.AccessError{Code: "code_stale", Message: "source changed; refresh code graph"}
	}
	s.mu.Lock()
	worker := s.worker
	s.mu.Unlock()
	if worker == nil {
		return out, errors.New("code worker unavailable")
	}
	if q.Mode == "" {
		q.Mode = "symbols"
	}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Hops == 0 {
		q.Hops = 3
	}
	var result codegraph.Result
	select {
	case s.gate <- struct{}{}:
		result, e = worker.CodeQuery(ctx, codegraph.Query{Dataset: c.Dataset, Snapshot: c.Snapshot, Mode: q.Mode, Text: q.Text, Node: q.Node, Limit: q.Limit, Hops: q.Hops})
		<-s.gate
	case <-ctx.Done():
		return out, ctx.Err()
	}
	if e != nil {
		return out, e
	}
	if c.Receipt == nil || result.Receipt.GraphDigest != c.Receipt.GraphDigest || result.Manifest.Digest != c.Manifest.Digest {
		return out, errors.New("code current receipt mismatch")
	}
	after, e := codegraph.Capture(ctx, c.Worktree, "")
	if e != nil {
		return out, e
	}
	if after.Digest != before.Digest {
		_ = s.store.MarkCodeStale(ctx, c.ID, c.Generation)
		return out, &records.AccessError{Code: "code_stale", Message: "source changed during code query"}
	}
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	current, e := s.store.Code(ctx, c.ID)
	if e != nil {
		return out, e
	}
	if s.codeStopping || !current.Enabled || current.Generation != c.Generation || current.Snapshot != c.Snapshot {
		return out, &records.AccessError{Code: "code_stale", Message: "code authorization or snapshot changed"}
	}
	return records.CodeResult{State: current, Graph: result}, nil
}

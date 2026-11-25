package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
)

type Progress struct {
	ExitCode       *int    `json:"exit_code,omitempty"`
	State          string  `json:"-"`
	LastActivityAt float64 `json:"last_activity_at,omitempty"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	IdleSeconds    float64 `json:"idle_seconds"`
	ProcessState   string  `json:"process_state"`
}

type RunRequest struct {
	Source        string
	Prompt        string
	IdleThreshold time.Duration
}

// Runner reports progress synchronously during Run and stops when ctx is cancelled.
// Report callbacks must complete before Run returns.
type Runner interface {
	Run(ctx context.Context, req RunRequest, report func(Progress)) (string, error)
}

func (s *Service) finish(source string, op *operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, source)
	close(op.done)
}

func (s *Service) run(base context.Context, op *operation, body string) error {
	source := op.row.InstanceID
	defer s.finish(source, op)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	s.mu.Lock()
	op.cancel = cancel
	if op.cancelRequested {
		cancel()
	}
	s.mu.Unlock()

	persistCtx := context.WithoutCancel(base)
	var progressErr error
	report := func(p Progress) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if op.cancelRequested || progressErr != nil || op.row.Status == "exited" {
			return
		}
		switch p.State {
		case "waiting", "activity":
			p.ProcessState = "running"
		case "exited":
			p.ProcessState = "exited"
		default:
			return
		}
		op.row.Status, op.progress = p.State, p
		raw, _ := json.Marshal(p)
		op.row.ProgressJSON = string(raw)
		progressErr = s.persist(persistCtx, op.row, nil)
		if progressErr != nil {
			cancel()
		}
	}

	var err error
	if op.row.Method == "agent" {
		body, err = s.export(ctx, source, report)
		if progressErr != nil {
			err = progressErr
		}
		if err == nil {
			code := 0
			report(Progress{State: "exited", LastActivityAt: op.progress.LastActivityAt,
				ElapsedSeconds: op.progress.ElapsedSeconds, IdleSeconds: op.progress.IdleSeconds, ExitCode: &code})
		}
	}
	if err == nil {
		err = progressErr
	}
	if err == nil {
		err = validateBody(body)
	}
	if err == nil && ctx.Err() == nil {
		op.row.DatasetID, err = s.dataset(ctx)
	}

	s.mu.Lock()
	if op.cancelRequested || base.Err() != nil {
		err = context.Canceled
	}
	if err != nil {
		defer s.mu.Unlock()
		op.row.Status = "failed"
		if errors.Is(err, context.Canceled) {
			op.row.Status = "cancelled"
		}
		return errors.Join(err, s.persist(persistCtx, op.row, err))
	}
	op.row.Status = "saving"
	err = s.persist(persistCtx, op.row, nil)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	document, digest, err := s.save(persistCtx, op.row, body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		op.row.Status = "failed"
		return errors.Join(err, s.persist(persistCtx, op.row, err))
	}
	op.row.Document, op.row.ContentHash, op.row.Status = document, digest, "saved"
	return s.persist(persistCtx, op.row, nil)
}

func (s *Service) export(ctx context.Context, source string, report func(Progress)) (string, error) {
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.cfg.Runner.Run(ctx, RunRequest{Source: source, Prompt: s.cfg.Prompt, IdleThreshold: s.cfg.IdleThreshold}, report)
}

func (s *Service) dataset(ctx context.Context) (string, error) {
	if s.cfg.DatasetResolver == nil {
		return s.cfg.DatasetID, nil
	}
	id, err := s.cfg.DatasetResolver(ctx)
	if err == nil && strings.TrimSpace(id) == "" {
		err = domain.ErrInvalidArgument
	}
	return id, err
}

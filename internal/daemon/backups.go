package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
)

type backupRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	result records.BackupOperation
	err    error
}

func validBackupID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (s *Server) createBackup(request context.Context, id string) (records.BackupReceipt, error) {
	var empty records.BackupReceipt
	if !validBackupID(id) {
		return empty, errors.New("canonical backup UUID required")
	}
	s.backupMu.Lock()
	if prior := s.backupRuns[id]; prior != nil {
		select {
		case <-prior.done:
			if prior.err == nil && prior.result.Receipt != nil {
				receipt := *prior.result.Receipt
				s.backupMu.Unlock()
				return receipt, nil
			}
			// A new explicit create retries a failed/cancelled capture. The
			// filesystem manager still replays any already-published snapshot.
			delete(s.backupRuns, id)
		default:
			s.backupMu.Unlock()
			select {
			case <-request.Done():
				return empty, request.Err()
			case <-prior.done:
			}
			if prior.result.Receipt != nil {
				return *prior.result.Receipt, prior.err
			}
			return empty, prior.err
		}
	}
	if s.lifecycle == nil || s.lifecycle.Err() != nil {
		s.backupMu.Unlock()
		return empty, errors.New("backup service is stopping")
	}
	if s.backupRuns == nil {
		s.backupRuns = make(map[string]*backupRun)
	}
	// Keep only bounded observations; active cancellation handles are never evicted.
	if len(s.backupRuns) >= 64 {
		for key, run := range s.backupRuns {
			select {
			case <-run.done:
				delete(s.backupRuns, key)
			default:
			}
		}
	}
	ctx, cancel := context.WithTimeout(s.lifecycle, 60*time.Second)
	run := &backupRun{cancel: cancel, done: make(chan struct{}), result: records.BackupOperation{ID: id, State: "creating"}}
	s.backupRuns[id] = run
	s.backupMu.Unlock()
	defer cancel()
	create := s.backupCreate
	if create == nil {
		create = func(ctx context.Context, id string) (records.BackupReceipt, error) {
			return s.store.Backup(ctx, s.config.Root, id)
		}
	}
	receipt, err := create(ctx, id)
	s.backupMu.Lock()
	run.err = err
	switch {
	case err == nil:
		run.result.State, run.result.Receipt = "completed", &receipt
	case errors.Is(err, context.Canceled):
		run.result.State = "cancelled"
	default:
		run.result.State, run.result.Error = "failed", "备份未完成，请检查后重试。"
	}
	close(run.done)
	s.backupMu.Unlock()
	return receipt, err
}

func (s *Server) backupStatus(ctx context.Context, id string) (records.BackupOperation, error) {
	if !validBackupID(id) {
		return records.BackupOperation{}, errors.New("canonical backup UUID required")
	}
	s.backupMu.Lock()
	run := s.backupRuns[id]
	if run != nil {
		result := run.result
		s.backupMu.Unlock()
		return result, nil
	}
	s.backupMu.Unlock()
	// Published receipts remain discoverable after daemon restart.
	snapshot, err := (recoveryfiles.Manager{Root: s.config.Root}).Verify(ctx, id)
	if err != nil {
		return records.BackupOperation{}, err
	}
	return records.BackupOperation{ID: id, State: "completed", Receipt: &snapshot.Receipt}, nil
}

func (s *Server) cancelBackup(ctx context.Context, id string) (records.BackupOperation, error) {
	if !validBackupID(id) {
		return records.BackupOperation{}, errors.New("canonical backup UUID required")
	}
	s.backupMu.Lock()
	run := s.backupRuns[id]
	if run == nil {
		s.backupMu.Unlock()
		return s.backupStatus(ctx, id)
	}
	run.cancel()
	s.backupMu.Unlock()
	select {
	case <-ctx.Done():
		return records.BackupOperation{}, ctx.Err()
	case <-run.done:
	}
	// Return this exact attempt even if an explicit retry has since started.
	s.backupMu.Lock()
	result := run.result
	s.backupMu.Unlock()
	return result, nil
}

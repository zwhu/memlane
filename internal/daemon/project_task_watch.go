package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) projectTaskChanged() {
	s.projectWatchMu.Lock()
	defer s.projectWatchMu.Unlock()
	if s.projectChanged != nil {
		close(s.projectChanged)
	}
	s.projectChanged = make(chan struct{})
}

func (s *Server) projectTaskChanges() <-chan struct{} {
	s.projectWatchMu.Lock()
	defer s.projectWatchMu.Unlock()
	if s.projectChanged == nil {
		s.projectChanged = make(chan struct{})
	}
	return s.projectChanged
}

func (s *Server) watchProjectTask(ctx context.Context, q records.ProjectTaskWatch) (records.ProjectTaskObservation, error) {
	var out records.ProjectTaskObservation
	version, err := hex.DecodeString(q.Version)
	if q.Task == "" || q.Attempt < 1 || err != nil || len(version) != 32 || hex.EncodeToString(version) != q.Version {
		return out, errors.New("matching project task and snapshot version required")
	}
	// Only an explicitly open task view holds a bounded wait. State transitions
	// wake it immediately; the sparse tick also covers worker receipts, grants,
	// workspace changes and index recovery owned outside the model runner.
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		changed := s.projectTaskChanges()
		s.agentAccess.RLock()
		t, e := s.store.ProjectTask(ctx, q.Task)
		s.agentAccess.RUnlock()
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if e != nil {
			return out, e
		}
		if t.Attempt != q.Attempt {
			return out, errors.New("project task attempt changed")
		}
		out = records.ProjectTaskObservation{Task: t, Version: records.ProjectTaskVersion(t)}
		active := projectStateActive(t.State) || t.IndexRecovery != nil && (t.IndexRecovery.State == "pending" || t.IndexRecovery.State == "running")
		if out.Version != q.Version || !active || t.AccessState != "available" {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-timeout.C:
			return out, nil
		case <-tick.C:
		case <-changed:
		}
	}
}

func projectStateActive(state string) bool {
	switch state {
	case "queued", "starting", "organizing", "saving", "indexing", "cancelling":
		return true
	}
	return false
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) startMerge(ctx context.Context, d records.MergeStart) (records.MergeTask, error) {
	s.mergeMu.Lock()
	defer s.mergeMu.Unlock()
	if s.lifecycle.Err() != nil {
		return records.MergeTask{}, s.lifecycle.Err()
	}
	t, created, err := s.store.StartMerge(ctx, d)
	if err != nil || !created {
		return t, err
	}
	runCtx, cancel := context.WithCancel(s.lifecycle)
	s.mergeRuns[t.ID] = cancel
	s.mergeWG.Add(1)
	go func() {
		defer s.mergeWG.Done()
		defer cancel()
		defer func() { s.mergeMu.Lock(); delete(s.mergeRuns, t.ID); s.mergeMu.Unlock() }()
		if err := s.store.ClaimMerge(runCtx, t.ID, t.Attempt); err != nil {
			return
		}
		events := map[string]int{}
		out, err := agents.RunMergeProposal(runCtx, agents.MergeRunOptions{Snapshot: t.Snapshot, OnEvent: func(event string) {
			events[event]++
			if event == "turn.started" {
				b, _ := json.Marshal(map[string]any{"events": events, "process_active": true})
				_, _ = s.store.DB.ExecContext(runCtx, `UPDATE merge_attempts SET execution=? WHERE task=? AND attempt=? AND state='running'`, string(b), t.ID, t.Attempt)
			}
		}})
		failure := ""
		if err != nil {
			failure = "agent_failed"
			var access *records.AccessError
			if errors.As(err, &access) {
				failure = access.Code
			}
			if errors.Is(err, context.DeadlineExceeded) {
				failure = "agent_timeout"
			}
		}
		var raw []byte
		if out.Proposal != nil {
			raw, _ = json.Marshal(out.Proposal)
		}
		// Even a cancelled/old attempt retains only bounded process evidence.
		// This write cannot stage a proposal or change effective knowledge.
		b, _ := json.Marshal(out.ProposalProcess)
		_, _ = s.store.DB.ExecContext(context.Background(), `UPDATE merge_attempts SET execution=? WHERE task=? AND attempt=?`, string(b), t.ID, t.Attempt)
		if runCtx.Err() != nil {
			return
		}
		_, _ = s.store.CompleteMerge(runCtx, t.ID, t.Attempt, raw, out.ProposalProcess, failure)
	}()
	return t, nil
}
func (s *Server) cancelMerge(ctx context.Context, id string) (records.MergeTask, error) {
	t, err := s.store.CancelMerge(ctx, id)
	if err != nil {
		return t, err
	}
	s.mergeMu.Lock()
	cancel := s.mergeRuns[id]
	s.mergeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return t, nil
}

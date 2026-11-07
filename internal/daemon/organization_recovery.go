package daemon

import (
	"context"
	"strings"
	"time"
)

// Unrecognized errors are not safe automatic retry candidates. Provider auth,
// quota, protocol and source errors remain terminal. This deliberately narrow
// list identifies transport/server failures rather than any Agent failure.
func transientOrganizationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, deny := range []string{"unauthorized", "authentication", "credential", "quota", "insufficient", "rate limit", "invalid", "forbidden", "permission"} {
		if strings.Contains(message, deny) {
			return false
		}
	}
	for _, allow := range []string{"connection reset", "connection refused", "tls handshake timeout", "temporarily unavailable", "temporary failure", "http 502", "http 503", "http 504", "502 bad gateway", "503 service unavailable", "504 gateway timeout"} {
		if strings.Contains(message, allow) {
			return true
		}
	}
	return false
}

// Caller holds organizationMu. Task/attempt creation and in-memory ownership
// registration cannot be separated by a competing query or cancellation.
func (s *Server) recoveryTick(ctx context.Context, now time.Time) error {
	ids, err := s.store.OrganizationRecoveryCandidates(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, live := s.organizationRuns[id]; live {
			continue
		}
		t, err := s.store.OrganizationTask(ctx, id)
		if err != nil {
			return err
		}
		if err = s.store.ScheduleOrganizationRecovery(ctx, id, t.Snapshot.Attempt, now); err != nil {
			return err
		}
		next, err := s.store.BeginOrganizationRecovery(ctx, id, now)
		if err != nil {
			return err
		}
		if next != nil {
			s.launchOrganization(*next)
		}
	}
	return nil
}

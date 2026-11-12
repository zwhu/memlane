package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

// Fixed upstream Codex plugin 1.7.7 _capture_policy.py source contract. The
// future write broker must also verify and run this exact artifact; this
// fingerprint alone is NOT evidence that filtering or capture is operational.
const piCapturePolicyDigest = agents.OfficialPiCapturePolicyDigest

func (s *Server) reviewPiCaptureConsent(ctx context.Context, q *records.PiCaptureConsentReview, confirmed bool) (records.PiCaptureConsent, error) {
	if !confirmed || q == nil {
		return records.PiCaptureConsent{}, &records.AccessError{Code: "confirmation_required", Message: "explicit new-message pi capture review required"}
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	return s.store.ReviewPiCaptureConsent(ctx, *q, piCapturePolicyDigest)
}

func (s *Server) disablePiCaptureConsent(ctx context.Context, q *records.PiCaptureConsentDisable, confirmed bool) error {
	if !confirmed || q == nil {
		return &records.AccessError{Code: "confirmation_required", Message: "explicit pi capture change required"}
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	return s.store.DisablePiCaptureConsent(ctx, *q)
}

func (s *Server) authorizePiCapture(ctx context.Context, q records.AgentRequest) (records.PiCaptureConsent, error) {
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	return s.store.AuthorizePiCapture(ctx, q, piCapturePolicyDigest)
}

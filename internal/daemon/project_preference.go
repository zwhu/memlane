package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

// These trusted local App endpoints add no Agent tools or grants.
func (s *Server) projectPreferenceComparison(ctx context.Context, q *records.ProjectPreferenceQuery) (records.ProjectPreferenceComparison, error) {
	if q == nil {
		return records.ProjectPreferenceComparison{}, &records.AccessError{Code: "invalid_request", Message: "explicit comparison target required"}
	}
	return s.store.ProjectPreferenceComparison(ctx, *q)
}
func (s *Server) confirmProjectPreference(ctx context.Context, q *records.ProjectPreferenceConfirmation) (records.ProjectPreferenceDecision, error) {
	if q == nil {
		return records.ProjectPreferenceDecision{}, &records.AccessError{Code: "invalid_request", Message: "explicit user confirmation required"}
	}
	return s.store.ConfirmProjectPreference(ctx, *q)
}

func (s *Server) projectPreferenceDecisions(ctx context.Context, q *records.ProjectPreferenceDecisionsQuery) (records.ProjectPreferenceDecisionsPage, error) {
	if q == nil {
		return records.ProjectPreferenceDecisionsPage{}, &records.AccessError{Code: "invalid_request", Message: "exact current project record required"}
	}
	return s.store.ProjectPreferenceDecisions(ctx, *q)
}

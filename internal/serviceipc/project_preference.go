package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) ProjectPreferenceComparison(ctx context.Context, q records.ProjectPreferenceQuery) (records.ProjectPreferenceComparison, error) {
	var out records.ProjectPreferenceComparison
	err := Call(ctx, l.Root, Request{Op: "project_preference_comparison", ProjectPreferenceQuery: &q}, &out)
	return out, err
}
func (l Library) ConfirmProjectPreference(ctx context.Context, q records.ProjectPreferenceConfirmation) (records.ProjectPreferenceDecision, error) {
	var out records.ProjectPreferenceDecision
	err := Call(ctx, l.Root, Request{Op: "project_preference_confirm", ProjectPreferenceConfirmation: &q}, &out)
	return out, err
}

func (l Library) ProjectPreferenceDecisions(ctx context.Context, q records.ProjectPreferenceDecisionsQuery) (records.ProjectPreferenceDecisionsPage, error) {
	var out records.ProjectPreferenceDecisionsPage
	err := Call(ctx, l.Root, Request{Op: "project_preference_decisions", ProjectPreferenceDecisionsQuery: &q}, &out)
	return out, err
}

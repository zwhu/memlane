package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) HomeActivities(ctx context.Context, q records.HomeActivityQuery) (records.HomeActivityPage, error) {
	var out records.HomeActivityPage
	err := Call(ctx, l.Root, Request{Op: "home_activity", HomeActivity: q}, &out)
	return out, err
}

func (l Library) HomeReviews(ctx context.Context, q records.HomeReviewQuery) (records.HomeReviewPage, error) {
	var out records.HomeReviewPage
	err := Call(ctx, l.Root, Request{Op: "home_review", HomeReview: q}, &out)
	return out, err
}

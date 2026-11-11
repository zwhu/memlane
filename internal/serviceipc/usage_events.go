package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) UsageEvents(ctx context.Context, query records.UsageEventsQuery) (records.UsageEventsPage, error) {
	var page records.UsageEventsPage
	err := Call(ctx, l.Root, Request{Op: "usage_events", UsageEventsQuery: &query}, &page)
	return page, err
}

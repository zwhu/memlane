package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) usageEvents(ctx context.Context, query *records.UsageEventsQuery) (records.UsageEventsPage, error) {
	if query == nil {
		return records.UsageEventsPage{}, &records.AccessError{Code: "invalid_request", Message: "exact knowledge usage event query required"}
	}
	return s.store.UsageEvents(ctx, *query)
}

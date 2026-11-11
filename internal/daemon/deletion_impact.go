package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

// Trusted local Library IPC only; Agent tools must not expose this endpoint.
func (s *Server) deletionImpact(ctx context.Context, q records.DeletionImpactQuery) (records.DeletionImpact, error) {
	return s.store.DeletionImpact(ctx, q)
}

package daemon

import (
	"context"
	"net/url"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) changeCodingMemoryGrant(ctx context.Context, q records.CodingMemoryGrantChange) (records.CodingMemoryGrant, error) {
	if q.Enabled {
		b, err := s.sessionBinding(ctx, records.SessionQuery{Project: q.Binding.Project, Instance: q.Binding.Instance, Dataset: q.Binding.Dataset})
		if err != nil {
			return records.CodingMemoryGrant{}, err
		}
		if b != q.Binding {
			return records.CodingMemoryGrant{}, records.MergeError("scope_changed")
		}
		token, err := s.cogneeToken(b.Instance, b.Dataset)
		if err != nil {
			return records.CodingMemoryGrant{}, err
		}
		var scope struct {
			Instance, Dataset, User string
			Managed                 bool
		}
		err = official.Request(ctx, b.Endpoint, "/api/v1/memlane/coding-memory/scope?dataset="+url.QueryEscape(b.Dataset), "GET", token, "", nil, &scope)
		if err != nil {
			return records.CodingMemoryGrant{}, sessionReadError(err)
		}
		if scope.Instance != b.Instance || scope.Dataset != b.Dataset || scope.User != q.User {
			return records.CodingMemoryGrant{}, records.MergeError("scope_denied")
		}
		if !scope.Managed {
			return records.CodingMemoryGrant{}, records.MergeError("session_processing_unavailable")
		}
	}
	// Disabling stays available after credential/service failure or disconnect.
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	return s.store.ChangeCodingMemoryGrant(ctx, q)
}

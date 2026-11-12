package daemon

import (
	"context"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) authorizeOfficialVisualization(ctx context.Context, q records.OfficialVisualizationQuery) (records.CogneeBinding, error) {
	empty := records.CogneeBinding{}
	scope := records.SessionQuery{Project: q.Project, Instance: q.Instance, Dataset: q.Dataset}
	b, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return empty, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return empty, err
	}
	r := official.VisualizationReader{SessionReader: official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}}
	if err := r.Authorize(ctx); err != nil {
		return empty, sessionReadError(err)
	}
	fresh, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return empty, err
	}
	if fresh != b {
		return empty, records.MergeError("scope_changed")
	}
	return b, nil
}

func (s *Server) readOfficialVisualization(ctx context.Context, q records.OfficialVisualizationQuery) (records.OfficialVisualization, error) {
	var empty records.OfficialVisualization
	scope := records.SessionQuery{Project: q.Project, Instance: q.Instance, Dataset: q.Dataset}
	b, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return empty, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return empty, err
	}
	r := official.VisualizationReader{SessionReader: official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}}
	out, err := r.Read(ctx, q.MaxNodes, q.Seeds)
	if err != nil {
		return empty, sessionReadError(err)
	}
	fresh, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return empty, err
	}
	if fresh != b {
		return empty, &records.AccessError{Code: "scope_changed", Message: "official visualization authorization changed"}
	}
	return out, nil
}

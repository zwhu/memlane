package daemon

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// Only an existing historical-session authorization admits catalog reads.
// A material-submission binding explicitly grants no historical session access.
func (s *Server) sessionBinding(ctx context.Context, q records.SessionQuery) (records.CogneeBinding, error) {
	if id, err := uuid.Parse(q.Project); err != nil || id.String() != q.Project {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_denied", Message: "explicit approved project required for official reads"}
	}
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	var selected []records.CogneeBinding
	for _, b := range bindings {
		if b.Project == q.Project && b.Instance == q.Instance && b.Dataset == q.Dataset {
			selected = append(selected, b)
		}
	}
	if len(selected) != 1 {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_denied", Message: "approved official session binding required"}
	}
	b := selected[0]
	return b, s.store.AuthorizeCogneeBinding(ctx, b)
}

func sessionReadError(err error) error {
	if err == nil {
		return nil
	}
	code := "invalid_provenance"
	var status *official.HTTPError
	var network net.Error
	switch {
	case errors.As(err, &status):
		switch status.Status {
		case 401, 403:
			code = "scope_denied"
		case 404:
			code = "source_not_found"
		case 408, 429, 500, 502, 503, 504:
			code = "cognee_unavailable"
		}
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.As(err, &network):
		code = "cognee_unavailable"
	}
	return &records.AccessError{Code: code, Message: "official session read could not be verified"}
}

func (s *Server) readOfficialSession(ctx context.Context, q records.SessionQuery, detail bool) (any, error) {
	if q.Captured {
		return s.readCapturedSession(ctx, q, detail)
	}
	b, err := s.sessionBinding(ctx, q)
	if err != nil {
		return nil, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return nil, err
	}
	reader := official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}
	var out any
	if detail {
		if q.Selected == nil {
			return nil, &records.AccessError{Code: "invalid_request", Message: "selected official session required"}
		}
		out, err = reader.Read(ctx, *q.Selected)
	} else {
		out, err = reader.ListPage(ctx, q.Limit, q.Offset)
	}
	if err != nil {
		return nil, sessionReadError(err)
	}
	// Revocation or replacement during network work wins over the response.
	fresh, err := s.sessionBinding(ctx, q)
	if err != nil {
		return nil, err
	}
	if fresh != b {
		return nil, &records.AccessError{Code: "scope_changed", Message: "official session authorization changed"}
	}
	return out, nil
}

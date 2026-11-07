package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) agent(ctx context.Context, r records.AgentRequest, svc *memory.Service) (any, error) {
	invalid := func(msg string) error { return &records.AccessError{Code: "invalid_request", Message: msg} }
	if r.Protocol != 1 {
		return nil, &records.AccessError{Code: "protocol_mismatch", Message: "unsupported Agent protocol version"}
	}
	switch r.Tool {
	case "memory_save", "memory_get", "memory_search", "memory_status", "memory_scopes":
	default:
		return nil, invalid("unknown memory tool")
	}
	c, scopes, err := s.store.Authorize(ctx, r)
	if err != nil {
		return nil, err
	}
	a := r.Arguments
	var result any
	switch r.Tool {
	case "memory_scopes":
		available, e := s.store.Scopes(ctx)
		if e != nil {
			return nil, e
		}
		visible := []records.Scope{}
		for _, scope := range available {
			for _, id := range scopes {
				if scope.ID == id {
					visible = append(visible, scope)
					break
				}
			}
		}
		result = map[string]any{"scopes": visible, "bound_project": c.Project, "current_project": c.ContextProject, "requires_scope": c.ContextProject == ""}
	case "memory_save":
		if len(scopes) != 1 || strings.TrimSpace(a.OperationID) == "" || len(a.OperationID) > 128 || strings.TrimSpace(a.Text) == "" || len(a.Text) > 524288 || len(a.Title) > 512 {
			return nil, invalid("save requires one scope, stable operation_id and nonempty text")
		}
		if a.Memory != "" || (a.Origin != "" && a.Origin != "knowledge" && a.Origin != "raw") {
			return nil, invalid("Agent saves create knowledge or raw records; existing knowledge changes require user confirmation")
		}
		// Namespace idempotency by connection. Scope is in the mutation digest,
		// so reusing an operation ID with different scope/content conflicts.
		key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"\x00"+a.OperationID))
		m := records.Mutation{OperationID: "agent:" + key.String(), Kind: "save", Dataset: scopes[0], Memory: key.String(), ExpectedRevision: 0, Text: a.Text, Title: a.Title, Source: c.Agent, Origin: a.Origin, SourceDetail: a.Source}
		saved, e := s.store.Apply(ctx, m)
		if e != nil {
			return nil, e
		}
		current, e := s.store.Current(ctx, saved.Memory)
		if e != nil {
			return nil, e
		}
		if current.Dataset != scopes[0] {
			return nil, &records.AccessError{Code: "scope_denied", Message: "original save persisted; current knowledge moved outside requested scope"}
		}
		if e = s.store.FilterAgentVersion(ctx, &current, scopes); e != nil {
			return nil, e
		}
		result = records.SaveReceipt{Persisted: true, SavedRevision: saved.Revision, Record: current}
	case "memory_get", "memory_status":
		if a.Memory == "" {
			return nil, invalid("memory_id required")
		}
		v, e := s.store.Current(ctx, a.Memory)
		if e != nil {
			return nil, e
		}
		found := false
		for _, scope := range scopes {
			if scope == v.Dataset {
				found = true
			}
		}
		if !found {
			return nil, &records.AccessError{Code: "scope_denied", Message: "memory is outside requested allowed scopes"}
		}
		if r.Tool == "memory_get" && v.Derived != nil && v.Derived.State == "stale" {
			return nil, &records.AccessError{Code: "source_stale", Message: "summary source changed; inspect memory_status or organize again"}
		}
		if e = s.store.FilterAgentVersion(ctx, &v, scopes); e != nil {
			return nil, e
		}
		result = v
	case "memory_search":
		if strings.TrimSpace(a.Query) == "" {
			return nil, invalid("nonempty query required")
		}
		limit := a.Limit
		if limit == 0 {
			limit = 10
		}
		if limit < 1 || limit > 100 {
			return nil, invalid("limit must be between 1 and 100")
		}
		hits, e := svc.Search(ctx, scopes, a.Query, limit)
		if e != nil {
			if errors.Is(e, ctx.Err()) {
				return nil, e
			}
			return nil, &records.AccessError{Code: "index_unavailable", Message: e.Error()}
		}
		for i := range hits {
			if e = s.store.FilterAgentVersion(ctx, &hits[i].Version, scopes); e != nil {
				return nil, e
			}
		}
		result = map[string]any{"hits": hits, "scopes": scopes, "project_priority": "当前项目明确约定优先于全局建议；保留来源和适用范围"}
	}
	// A real successful tool invocation is evidence; initialization alone is
	// not. Generations invalidate old sessions after scope/config changes.
	if err = s.store.TouchConnection(ctx, c.ID, c.Generation); err != nil {
		return nil, err
	}
	return result, nil
}

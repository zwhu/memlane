package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type authorizedScope struct {
	engine.Scope
	ProjectID string `json:"project_id,omitempty"`
	Kind      string `json:"kind"`
}

func (s *Server) scopes(ctx context.Context, agent domain.Agent, selected projectSelector, kind string, health bool) ([]authorizedScope, error) {
	var projects []domain.Project
	var err error
	if selected.empty() {
		projects, err = s.cfg.Store.ListProjects(ctx)
	} else {
		var project domain.Project
		project, err = s.selectedProject(ctx, selected)
		projects = []domain.Project{project}
	}
	if err != nil {
		return nil, err
	}
	scopes := make([]authorizedScope, 0, len(projects)+1)
	userID := agentUserID(agent)
	add := func(dataset, project, kind string) {
		if strings.TrimSpace(dataset) != "" && !domain.IsProtectedProject("", dataset) {
			scopes = append(scopes, authorizedScope{Scope: engine.Scope{DatasetID: dataset, UserID: userID},
				ProjectID: project, Kind: kind})
		}
	}
	for _, project := range projects {
		allowed := false
		if !protected(project) {
			acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
			if err != nil {
				return nil, err
			}
			allowed = acl.Access == domain.AccessRead || acl.Access == domain.AccessMaintain
		}
		if !allowed {
			if !selected.empty() {
				return nil, domain.ErrForbidden
			}
			continue
		}
		if kind != "code" {
			add(project.DatasetID, project.ID, "project")
		}
		if (kind == "code" || health) && project.CodeEnabled {
			add(project.CodeDatasetID, project.ID, "code")
		}
	}
	if kind == "code" {
		return scopes, nil
	}
	acl, err := s.cfg.Store.EvaluateCommonACL(ctx, agent.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return scopes, nil
	}
	if err != nil {
		return nil, err
	}
	if acl.Access == domain.AccessRead || acl.Access == domain.AccessMaintain {
		common, err := s.cfg.Store.GetCommonConfig(ctx)
		if err != nil {
			return nil, err
		}
		if !domain.IsProtectedProject(common.Name, common.DatasetName) {
			add(common.DatasetID, "", "common")
		}
	}
	return scopes, nil
}

type RecallObservation struct {
	AgentID    string             `json:"agent_id"`
	EventID    string             `json:"event_id"`
	Channel    string             `json:"channel"`
	ReceivedAt string             `json:"received_at"`
	Hits       []domain.RecallHit `json:"hits"`
}

func (s *Server) recall(ctx context.Context, agent domain.Agent, raw json.RawMessage) (any, error) {
	var args struct {
		projectSelector
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
	}
	if json.Unmarshal(raw, &args) != nil || strings.TrimSpace(args.Query) == "" || args.Limit < 0 {
		return nil, domain.ErrInvalidArgument
	}
	if args.Limit == 0 {
		args.Limit = 10
	}
	args.Limit = min(args.Limit, 50)
	scopes, err := s.scopes(ctx, agent, args.projectSelector, args.Kind, false)
	if err != nil {
		return nil, err
	}
	req := engine.SearchRequest{Query: args.Query, Limit: args.Limit,
		Filters: engine.SearchFilters{Kind: args.Kind, Limit: args.Limit}}
	authorized := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, exists := authorized[scope.DatasetID]; !exists {
			req.Scopes = append(req.Scopes, scope.Scope)
			authorized[scope.DatasetID] = struct{}{}
		}
	}
	hits := make([]engine.SearchHit, 0, args.Limit)
	recorded := make([]domain.RecallHit, 0, args.Limit)
	if len(req.Scopes) > 0 {
		searchCtx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
		result, err := s.cfg.Engine.Search(searchCtx, req)
		cause := searchCtx.Err()
		cancel()
		if cause != nil {
			return nil, cause
		}
		if err != nil {
			return nil, err
		}
		if !result.OK {
			return nil, engine.ErrUnavailable
		}
		seen := make(map[domain.RecallHit]struct{})
		for _, hit := range result.Hits {
			ref := domain.RecallHit{DatasetID: hit.DatasetID, DocumentID: hit.DocumentID}
			if _, allowed := authorized[hit.DatasetID]; !allowed || strings.TrimSpace(hit.DocumentID) == "" {
				continue
			}
			if _, duplicate := seen[ref]; duplicate {
				continue
			}
			seen[ref] = struct{}{}
			hits, recorded = append(hits, hit), append(recorded, ref)
			if len(hits) == args.Limit {
				break
			}
		}
	}
	at, id := domain.NowUTCNS(), "mcp:"+randomID()
	if err := s.cfg.Store.RecordRecallQueryStat(ctx, "explicit", len(recorded), at); err != nil {
		return nil, err
	}
	if len(recorded) > 0 {
		if _, err := s.cfg.Store.RecordRecallEvent(ctx, domain.RecallEventInput{AgentID: agent.ID, EventID: id,
			Channel: "explicit", ReceivedAt: at, Hits: recorded}); err != nil {
			return nil, err
		}
	}
	if s.cfg.OnRecall != nil {
		s.cfg.OnRecall(RecallObservation{AgentID: agent.ID, EventID: id, Channel: "mcp", ReceivedAt: at, Hits: recorded})
	}
	return map[string]any{"hits": hits, "channel": "mcp"}, nil
}

func (s *Server) health(ctx context.Context, agent domain.Agent, _ json.RawMessage) (any, error) {
	status, err := s.cfg.Engine.Health(ctx)
	if err != nil {
		return nil, err
	}
	scopes, err := s.scopes(ctx, agent, projectSelector{}, "", true)
	if err != nil {
		return nil, err
	}
	status.StorePath = ""
	return map[string]any{"engine": status, "scopes": scopes}, nil
}

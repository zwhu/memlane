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
	unlock := s.lockRegistry()
	defer unlock()
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
		Query string            `json:"query"`
		Kind  string            `json:"kind"`
		Mode  engine.SearchMode `json:"mode"`
		Limit int               `json:"limit"`
	}
	if json.Unmarshal(raw, &args) != nil || strings.TrimSpace(args.Query) == "" || args.Limit < 0 {
		return nil, domain.ErrInvalidArgument
	}
	mode := engine.SearchMode(strings.TrimSpace(string(args.Mode)))
	if mode == "" {
		if strings.EqualFold(args.Kind, "code") {
			mode = engine.SearchModeCode
		} else {
			mode = engine.SearchModeHybrid
		}
	}
	switch mode {
	case engine.SearchModeHybrid, engine.SearchModeGraph, engine.SearchModeChunks, engine.SearchModeCode:
	default:
		return nil, domain.ErrInvalidArgument
	}
	if args.Limit == 0 {
		args.Limit = 10
	}
	args.Limit = min(args.Limit, 50)
	scopeKind := args.Kind
	if strings.EqualFold(scopeKind, "code") || mode == engine.SearchModeCode {
		scopeKind = "code"
	}
	scopes, err := s.scopes(ctx, agent, args.projectSelector, scopeKind, false)
	if err != nil {
		return nil, err
	}
	req := engine.SearchRequest{Query: args.Query, Mode: mode, Limit: args.Limit,
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
	var graphContext *engine.GraphSearchContext
	degraded := false
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
		graphContext, degraded = filterAuthorizedGraphContext(result.GraphContext, authorized)
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
	reply := map[string]any{
		"hits":               hits,
		"channel":            "mcp",
		"mode":               mode,
		"degraded_to_chunks": degraded,
	}
	if graphContext != nil {
		reply["graph_context"] = graphContext
	}
	return reply, nil
}

func filterAuthorizedGraphContext(gc *engine.GraphSearchContext, authorized map[string]struct{}) (*engine.GraphSearchContext, bool) {
	if gc == nil {
		return nil, false
	}
	degraded := gc.DegradedToChunks
	entities := make([]engine.GraphSearchEntity, 0, len(gc.Entities))
	for _, entity := range gc.Entities {
		if _, ok := authorized[entity.DatasetID]; !ok {
			continue
		}
		cloned := entity
		if len(entity.Edges) > 0 {
			cloned.Edges = append([]engine.GraphSearchEdge(nil), entity.Edges...)
		}
		if len(entity.Conflicts) > 0 {
			seen := make(map[string]bool, len(entity.Conflicts))
			cleaned := make([]string, 0, len(entity.Conflicts))
			for _, conflict := range entity.Conflicts {
				if trimmed := strings.TrimSpace(conflict); trimmed != "" && !seen[trimmed] {
					seen[trimmed] = true
					cleaned = append(cleaned, trimmed)
				}
			}
			cloned.Conflicts = cleaned
		}
		entities = append(entities, cloned)
	}
	facts := make([]engine.GraphSearchFact, 0, len(gc.Facts))
	for _, fact := range gc.Facts {
		if _, ok := authorized[fact.DatasetID]; ok && strings.TrimSpace(fact.Text) != "" {
			facts = append(facts, fact)
		}
	}
	codeFacts := make([]engine.CodeSearchFact, 0, len(gc.CodeFacts))
	for _, cf := range gc.CodeFacts {
		if _, ok := authorized[cf.DatasetID]; !ok || strings.TrimSpace(cf.Name) == "" {
			continue
		}
		cloned := cf
		if len(cf.Relations) > 0 {
			cloned.Relations = append([]engine.CodeSearchRelation(nil), cf.Relations...)
		}
		codeFacts = append(codeFacts, cloned)
	}
	conflicts := make([]engine.GraphSearchConflict, 0, len(gc.Conflicts))
	seenConflicts := make(map[[2]string]bool, len(gc.Conflicts))
	for _, raw := range gc.Conflicts {
		text := strings.TrimSpace(raw.Text)
		if _, ok := authorized[raw.DatasetID]; !ok || text == "" {
			continue
		}
		key := [2]string{raw.DatasetID, text}
		if seenConflicts[key] {
			continue
		}
		seenConflicts[key] = true
		conflicts = append(conflicts, engine.GraphSearchConflict{
			DatasetID: raw.DatasetID,
			Text:      text,
		})
	}
	guidance := strings.TrimSpace(gc.SessionGuidance)
	if len(entities) == 0 && len(facts) == 0 && len(conflicts) == 0 && guidance == "" && len(codeFacts) == 0 {
		return nil, degraded
	}
	return &engine.GraphSearchContext{
		Entities:         entities,
		Facts:            facts,
		Conflicts:        conflicts,
		SessionGuidance:  guidance,
		CodeFacts:        codeFacts,
		DegradedToChunks: degraded,
	}, degraded
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

package mcp

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/recall"
	"github.com/zwhu/memlane/kit/daemon"
)

type authorizedScope = recall.AuthorizedScope

func (s *Server) scopes(ctx context.Context, agent domain.Agent, selected projectSelector, kind string, health bool) ([]authorizedScope, error) {
	unlock := s.lockRegistry()
	defer unlock()
	var projects []domain.Project
	explicit := !selected.empty()
	if explicit {
		project, err := s.selectedProject(ctx, selected)
		if err != nil {
			return nil, err
		}
		projects = []domain.Project{project}
	}
	out, err := recall.ResolveScopes(ctx, recall.ResolveInput{
		Store:       s.cfg.Store,
		Agent:       agent,
		UserID:      recall.AgentUserID(agent),
		Projects:    projects,
		Explicit:    explicit,
		CodeOnly:    strings.EqualFold(kind, "code"),
		IncludeCode: health,
		AllowEmpty:  health,
	})
	if err != nil {
		return nil, err
	}
	return out.Scopes, nil
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

	searchCtx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
	out, err := recall.Execute(searchCtx, recall.SearchOptions{
		Engine: s.cfg.Engine,
		Scopes: scopes,
		Query:  args.Query,
		Mode:   mode,
		Limit:  args.Limit,
		Kind:   args.Kind,
	})
	cancel()
	if err != nil {
		return nil, err
	}

	recorded := make([]domain.RecallHit, 0, len(out.Hits))
	for _, hit := range out.Hits {
		recorded = append(recorded, domain.RecallHit{DatasetID: hit.DatasetID, DocumentID: hit.DocumentID})
	}
	at, id := domain.NowUTCNS(), "mcp:"+randomID()
	if err := s.cfg.Store.RecordRecallQueryStat(ctx, "explicit", len(recorded), at); err != nil {
		log.Printf("mcp recall record query stat: %s", daemon.RedactLogLine(err.Error()))
	}
	if len(recorded) > 0 {
		if _, err := s.cfg.Store.RecordRecallEvent(ctx, domain.RecallEventInput{
			AgentID:    agent.ID,
			EventID:    id,
			Channel:    "explicit",
			ReceivedAt: at,
			Hits:       recorded,
		}); err != nil {
			log.Printf("mcp recall record event: %s", daemon.RedactLogLine(err.Error()))
		}
	}
	if s.cfg.OnRecall != nil {
		s.cfg.OnRecall(RecallObservation{AgentID: agent.ID, EventID: id, Channel: "mcp", ReceivedAt: at, Hits: recorded})
	}
	reply := map[string]any{
		"hits":               out.Hits,
		"channel":            "mcp",
		"mode":               mode,
		"degraded_to_chunks": out.DegradedToChunks,
		"partial":            out.Partial,
	}
	if len(out.ScopeErrors) > 0 {
		reply["scope_errors"] = out.ScopeErrors
	}
	if out.GraphContext != nil {
		reply["graph_context"] = out.GraphContext
	}
	return reply, nil
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

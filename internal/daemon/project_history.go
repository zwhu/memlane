package daemon

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) openProjectHistory(ctx context.Context, q records.ProjectHistoryQuery) (*agents.CodexProjectHistory, string, error) {
	b, workspace, identity, err := s.store.ProjectHistoryAccess(ctx, q)
	if err != nil {
		return nil, "", err
	}
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	i, err := agents.LoadInstallation(clientRoot, b.ConnectionID)
	if err != nil || i.Connection.ID != b.ConnectionID || i.Connection.Agent != "codex" || !i.Connection.Enabled || i.ToolsetVersion != 2 || i.Options.ClientRoot != clientRoot || !filepath.IsAbs(i.Options.Bridge) || i.Connection.Generation != b.Generation || i.VerifiedGeneration != b.Generation || i.VerifiedAt == "" || i.VerificationError != "" || i.AgentVersion != "codex-cli 0.167.0-alpha.3.2" || !filepath.IsAbs(i.Options.CodexDirectory) || i.Options.ServiceRoot != s.config.Root {
		return nil, "", &records.AccessError{Code: "history_unavailable", Message: "verified bound Codex history directory required"}
	}
	if err = agents.CheckInstallation(i); err != nil {
		return nil, "", &records.AccessError{Code: "history_unavailable", Message: "bound Codex installation changed"}
	}
	r, err := agents.OpenCodexProjectHistory(ctx, i.Options.CodexDirectory, q.Project, workspace)
	return r, identity, err
}

func (s *Server) projectHistoryCatalog(ctx context.Context, q records.ProjectHistoryQuery) (records.ProjectHistoryPage, error) {
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	var empty records.ProjectHistoryPage
	r, identity, err := s.openProjectHistory(ctx, q)
	if err != nil && !errors.Is(err, agents.ErrCodexHistoryEmpty) {
		return empty, err
	}
	page := records.ProjectHistoryPage{Items: []records.ProjectHistoryChoice{}}
	if r != nil {
		defer r.Close()
		page, err = r.List(ctx, q.After)
		if err != nil {
			return empty, err
		}
	}
	_, _, current, err := s.store.ProjectHistoryAccess(ctx, q)
	if err != nil {
		return empty, err
	}
	if current != identity {
		return empty, errors.New("project changed during history selection")
	}
	page.WorkspaceID = identity
	return page, nil
}

func (s *Server) freezeProjectHistory(ctx context.Context, d records.ProjectTaskStart) ([]records.ProjectSessionSource, []records.ProjectRepositorySource, error) {
	if !d.IncludeHistory {
		return nil, nil, nil
	}
	q := records.ProjectHistoryQuery{Project: d.Project, BindingRevision: d.BindingRevision}
	r, identity, err := s.openProjectHistory(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	if identity != d.HistoryWorkspaceID {
		return nil, nil, &records.AccessError{Code: "workspace_changed", Message: "project changed since historical selection"}
	}
	selected := []records.ProjectSessionSource{}
	frozen := []records.ProjectRepositorySource{}
	total := 0
	for _, choice := range d.Sessions {
		source, e := r.Read(ctx, choice)
		if e != nil {
			code := "history_unavailable"
			if errors.Is(e, agents.ErrCodexHistoryChanged) {
				code = "history_changed"
			}
			return nil, nil, &records.AccessError{Code: code, Message: "selected historical source must be reread before task start"}
		}
		total += len(source.Text)
		if total > 32*1024*1024 {
			return nil, nil, errors.New("historical material exceeds task budget; nothing truncated")
		}
		selected = append(selected, records.ProjectSessionSource{ProjectSessionSelection: choice, Project: d.Project, Agent: "codex"})
		frozen = append(frozen, source)
	}
	return selected, frozen, nil
}

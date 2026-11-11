package daemon

import (
	"context"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) authorizeProjectCogneeMaterial(ctx context.Context, q records.CogneeProjectAuthorization) (records.CogneeBinding, error) {
	var b records.CogneeBinding
	for _, id := range []string{q.Operation, q.Project, q.ConnectionID} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return b, &records.AccessError{Code: "invalid_request", Message: "explicit project and connection required"}
		}
	}
	s.agentAccess.RLock()
	scopes, err := s.store.Scopes(ctx)
	for _, scope := range scopes {
		if scope.ID == q.Project && scope.Kind == "project" && scope.Path != "" {
			b = records.CogneeBinding{Operation: q.Operation, Project: q.Project, ConnectionID: q.ConnectionID, Generation: q.Generation, Workspace: scope.Path, MaterialSubmission: true}
		}
	}
	if err == nil && b.Project == "" {
		err = &records.AccessError{Code: "scope_required", Message: "registered project required"}
	}
	if err == nil {
		err = s.store.AuthorizeCogneeSubmissionBinding(ctx, b)
	}
	var library string
	if err == nil {
		library, err = s.store.CogneeLibraryIdentity(ctx)
	}
	s.agentAccess.RUnlock()
	if err != nil {
		return b, err
	}
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	controller := official.Controller{Root: filepath.Join(clientRoot, "official")}
	material, err := controller.PrepareProjectMaterialDataset(ctx, q.Project, library)
	if err != nil {
		return b, &records.AccessError{Code: "cognee_environment_required", Message: "prepare the local Cognee material service before authorizing this project"}
	}
	b.Instance, b.Dataset, b.Endpoint = material.Instance, material.Dataset, material.Endpoint
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	if err = s.store.AuthorizeCogneeSubmissionBinding(ctx, b); err != nil {
		return b, err
	}
	currentLibrary, err := s.store.CogneeLibraryIdentity(ctx)
	if err != nil {
		return b, err
	}
	if currentLibrary != library {
		return b, records.ErrConflict
	}
	// Persist the host credential privately before publishing the binding. It
	// never enters the frontend, task ledger, source metadata or Agent config.
	if err = s.writeCogneeToken(b.Instance, b.Dataset, material.Token); err != nil {
		return b, err
	}
	return s.store.BindProjectCogneeMaterial(ctx, b)
}

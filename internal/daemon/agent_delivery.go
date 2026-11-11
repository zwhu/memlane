package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"

	"time"
)

func (s *Server) authorizeAgentDelivery(ctx context.Context, r records.AgentRequest) (records.AgentDeliveryGrant, error) {
	var out records.AgentDeliveryGrant
	if r.Protocol != 1 || r.Tool != "memory_save" || r.ToolMode != "" || (r.Channel != "" && r.Channel != "daily") {
		return out, &records.AccessError{Code: "invalid_request", Message: "ordinary save authorization only"}
	}
	c, scopes, err := s.store.AuthorizeReadOnly(ctx, r)
	if err != nil {
		return out, err
	}
	if len(scopes) != 1 || r.Workspace == "" {
		return out, &records.AccessError{Code: "scope_required", Message: "one trusted workspace and scope required"}
	}
	canonical, workspaceIdentity, err := store.DeliveryWorkspaceIdentity(ctx, c.ContextProject, r.Workspace)
	if err != nil {
		return out, err
	}
	physical, err := store.WorkspaceDirectoryIdentity(canonical)
	if err != nil {
		return out, err
	}
	library, err := s.store.CogneeLibraryIdentity(ctx)
	if err != nil {
		return out, err
	}
	return records.AgentDeliveryGrant{WorkspacePhysical: physical, Agent: c.Agent, LibraryID: library, ConnectionID: c.ID, Generation: c.Generation, Scope: scopes[0], Workspace: canonical, WorkspaceDigest: workspaceIdentity, IssuedMS: time.Now().UnixMilli()}, nil
}
func (s *Server) validateAgentDelivery(ctx context.Context, r records.AgentRequest) error {
	if r.ExpectedLibraryID == "" || r.DeliveryGrant == nil {
		return &records.AccessError{Code: "delivery_unverified", Message: "original library observation required"}
	}
	// Never trust a client-supplied workspace digest as proof: recompute it via
	// the exact current Authorize boundary and physical workspace on every send.
	current, err := s.authorizeAgentDelivery(ctx, r)
	if err != nil {
		return err
	}
	original := r.DeliveryGrant
	if current.LibraryID != r.ExpectedLibraryID || original.LibraryID != current.LibraryID {
		return &records.AccessError{Code: "delivery_library_changed", Message: "original library changed"}
	}
	if current.WorkspacePhysical != original.WorkspacePhysical || current.Agent != original.Agent || current.ConnectionID != original.ConnectionID || current.Generation != original.Generation || current.Scope != original.Scope || current.Workspace != original.Workspace || current.WorkspaceDigest != original.WorkspaceDigest {
		return &records.AccessError{Code: "delivery_authorization_changed", Message: "original delivery authority changed"}
	}
	return nil
}

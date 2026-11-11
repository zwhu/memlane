package agentbridge

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

// RetainCapturedRaw reuses the durable outbox without sending a request or
// granting project access. The caller is the installed, explicitly authorized
// capture adapter, never an MCP tool. Unknown repositories are retained just
// like known ones; a later independent App action can assign the material.
func RetainCapturedRaw(c Config, a records.AgentArguments) (records.AgentDeliveryStatus, error) {
	var out records.AgentDeliveryStatus
	if !filepath.IsAbs(c.Root) || c.ConnectionID == "" || c.Generation < 1 || !filepath.IsAbs(c.Workspace) || a.Origin != "raw" || strings.TrimSpace(a.OperationID) == "" || len(a.OperationID) > 128 || strings.TrimSpace(a.Text) == "" || len(a.Text) > 524288 || len(a.Title) > 512 || a.Scope != "" || len(a.Scopes) != 0 || a.Memory != "" {
		return out, errors.New("capture_retention_invalid")
	}
	if a.Source != nil && (a.Source.History != nil || a.Source.Native != nil || a.Source.Official != nil) {
		return out, errors.New("capture_retention_provenance_invalid")
	}
	workspace, err := filepath.EvalSymlinks(c.Workspace)
	if err != nil {
		return out, errors.New("capture_workspace_unavailable")
	}
	physical, err := workspacePhysical(workspace)
	if err != nil {
		return out, errors.New("capture_workspace_unavailable")
	}
	d, err := openDelivery(c.Root)
	if err != nil {
		return out, err
	}
	defer d.close()
	payload := deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: c.Generation, Workspace: workspace, WorkspacePhysical: physical, Arguments: a}
	if old := d.find(deliveryKey(c, a)); old != nil && old.Payload.Grant == nil {
		// Tool/project grants may change independently of collection consent.
		// Replay still compares every frozen original field below.
		payload.Generation = old.Payload.Generation
	}
	entry, err := d.capture(payload)
	if err != nil {
		return out, err
	}
	return *deliveryStatus(entry), nil
}

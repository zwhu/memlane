package daemon

import (
	"context"
	"net/url"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) verifyOfficialCodeScope(ctx context.Context, a records.CodeAction) error {
	if a.Kind != "enable" || a.Official == nil {
		return records.MergeError("invalid_request")
	}
	b := *a.Official
	if err := s.store.CheckBoundWorkspace(ctx, a.Project, a.Path); err != nil {
		return err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return err
	}
	if token == "" {
		return records.MergeError("cognee_authorization_required")
	}
	// Explicit tags avoid accepting an old component's version string as the
	// new official scope adapter. No component is started by this probe.
	var wire struct {
		Instance     string   `json:"instance"`
		Commit       string   `json:"source_commit"`
		Capabilities []string `json:"capabilities"`
	}
	if err := official.Request(ctx, b.Endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &wire); err != nil {
		return sessionReadError(err)
	}
	capable := false
	for _, c := range wire.Capabilities {
		capable = capable || c == "official_code_scope_v1"
	}
	if wire.Instance != b.Instance || wire.Commit != official.CodingMemoryServiceCommit || !capable {
		return records.MergeError("code_component_required")
	}
	var scope struct {
		Instance, Dataset, User string
		Empty                   *bool
	}
	query := url.Values{"dataset": {b.Dataset}, "expected_instance": {b.Instance}}
	if err := official.Request(ctx, b.Endpoint, "/api/v1/memlane/code/scope?"+query.Encode(), "GET", token, "", nil, &scope); err != nil {
		return sessionReadError(err)
	}
	if scope.Instance != b.Instance || scope.Dataset != b.Dataset || scope.User != b.User || scope.Empty == nil {
		return records.MergeError("scope_denied")
	}
	known, err := s.store.OfficialCodeScopeMatches(ctx, a.Project, a.Path, b)
	if err != nil {
		return err
	}
	if !known && !*scope.Empty {
		return records.MergeError("code_dataset_not_empty")
	}
	return nil
}

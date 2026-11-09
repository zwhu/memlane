package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func (s *Server) agent(ctx context.Context, r records.AgentRequest, svc *memory.Service) (any, error) {
	invalid := func(msg string) error { return &records.AccessError{Code: "invalid_request", Message: msg} }
	if r.Protocol != 1 {
		return nil, &records.AccessError{Code: "protocol_mismatch", Message: "unsupported Agent protocol version"}
	}
	switch r.Tool {
	case "memory_save", "memory_get", "memory_search", "memory_status", "memory_scopes", "memory_adopt", "memory_initialization_get", "memory_initialization_status", "memory_initialize", "memory_code_search":
	default:
		return nil, invalid("unknown memory tool")
	}
	if r.ToolMode != "" && r.ToolMode != "initialization" {
		return nil, invalid("unknown tool mode")
	}
	if r.ToolMode == "initialization" && r.Tool != "memory_initialization_get" && r.Tool != "memory_initialization_status" && r.Tool != "memory_initialize" {
		return nil, invalid("tool is unavailable during initialization")
	}
	c, scopes, err := s.store.Authorize(ctx, r)
	if err != nil {
		return nil, err
	}
	if r.CallID == "" {
		r.CallID = uuid.NewString()
	}
	if len(r.CallID) > 128 {
		return nil, invalid("bounded bridge call ID required")
	}
	a := r.Arguments
	channel := r.Channel
	if channel == "" {
		channel = "daily"
	}
	if channel != "daily" && channel != "verification" {
		return nil, invalid("invalid Agent usage channel")
	}
	workspaceID := ""
	if r.Workspace != "" {
		canonical, e := filepath.EvalSymlinks(r.Workspace)
		if e != nil {
			return nil, e
		}
		workspaceID = store.UsageDigest([]string{c.ContextProject, canonical})
	} else if c.ContextProject != "" {
		workspaceID = store.UsageDigest([]string{c.ContextProject, "explicit-bound-project"})
	}
	observed := false
	observation := func(kind string, versions []records.Version, stable string, exposure string, observationChannel string) records.UsageObservation {
		targets := []records.UsageTarget{}
		for _, v := range versions {
			targets = append(targets, records.UsageTarget{Memory: v.Memory, Dataset: v.Dataset, Revision: v.Revision})
		}
		if c.Agent == "trae" {
			stable += "/" + observationChannel
		}
		key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"/"+fmt.Sprint(c.Generation)+"/"+kind+"/"+stable)).String()
		return records.UsageObservation{OperationID: key, Digest: store.UsageDigest(struct {
			Workspace string
			Arguments records.AgentArguments
		}{r.Workspace, r.Arguments}), Kind: kind, Channel: observationChannel, WorkspaceID: workspaceID, ConnectionID: c.ID, Generation: c.Generation, Targets: targets, Exposure: exposure}
	}
	observe := func(kind string, versions []records.Version, stable, exposure, ch string) (records.UsageReceipt, error) {
		receipt, e := s.store.RecordUsage(ctx, observation(kind, versions, stable, exposure, ch))
		if e == nil {
			observed = true
		}
		return receipt, e
	}
	var result any
	switch r.Tool {
	case "memory_code_search":
		project := a.Scope
		if project == "" {
			project = c.ContextProject
		}
		allowed := false
		for _, id := range scopes {
			if id == project {
				allowed = true
				break
			}
		}
		if !allowed || project == "" || r.Workspace == "" {
			return nil, &records.AccessError{Code: "scope_required", Message: "code queries need a trusted workspace and an authorized project"}
		}
		workspace := r.Workspace
		if project != c.ContextProject {
			known, e := s.store.Scopes(ctx)
			if e != nil {
				return nil, e
			}
			workspace = ""
			for _, scope := range known {
				if scope.ID == project && scope.Kind == "project" {
					workspace = scope.Path
					break
				}
			}
			if workspace == "" {
				return nil, invalid("code scope must be a registered project")
			}
		}
		state, e := s.store.CodeForWorkspace(ctx, project, workspace)
		if e != nil {
			return nil, e
		}
		result, e = s.queryCode(ctx, records.CodeQuery{ID: state.ID, Mode: a.CodeMode, Text: a.Query, Node: a.CodeNode, Limit: a.Limit, Hops: a.CodeHops})
		if e != nil {
			return nil, e
		}
	case "memory_initialization_get", "memory_initialization_status", "memory_initialize":
		if a.InitializationBatch == "" {
			return nil, invalid("batch_id required")
		}
		batch, e := s.store.CheckInitializationAgent(ctx, a.InitializationBatch, c.ID, c.Generation, workspaceID, scopes)
		if e != nil {
			return nil, e
		}
		switch r.Tool {
		case "memory_initialization_get":
			result, e = s.store.ReadInitializationSources(ctx, batch)
		case "memory_initialization_status":
			batch.Sources = nil
			result = batch
		case "memory_initialize":
			var receipt records.InitializationReceipt
			receipt, e = s.store.SubmitInitialization(ctx, batch, records.InitializationCommit{Batch: batch.ID, OperationID: a.OperationID, Items: a.InitializationItems})
			if e == nil {
				result = receipt
				select {
				case s.work <- struct{}{}:
				default:
				}
			} else {
				code := "submission_failed"
				var failure *records.AccessError
				if errors.As(e, &failure) {
					code = failure.Code
				}
				s.store.NoteInitializationFailure(ctx, batch, code)
			}
		}
		if e != nil {
			return nil, e
		}
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
		if c.PendingProject != "" && channel == "verification" {
			return nil, &records.AccessError{Code: "first_task_required", Message: "connection verification cannot create a project; complete an authorized real task first"}
		}
		// Namespace idempotency by connection. Scope is in the mutation digest,
		// so reusing an operation ID with different scope/content conflicts.
		key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"\x00"+a.OperationID))
		m := records.Mutation{OperationID: "agent:" + key.String(), Kind: "save", Dataset: scopes[0], Memory: key.String(), ExpectedRevision: 0, Text: a.Text, Title: a.Title, Source: c.Agent, Origin: a.Origin, SourceDetail: a.Source}
		saved, e := s.store.ApplyAgentSave(ctx, c, m)
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
		readChannel := channel
		if records.IDEVerificationRecord(c, v) {
			readChannel = "verification"
		}
		if r.Tool == "memory_status" && readChannel == "verification" && v.IndexState == "indexed" {
			if _, e := observe("status", []records.Version{v}, r.CallID, "", readChannel); e != nil {
				return nil, e
			}
		}
		if r.Tool == "memory_get" {
			receipt, e := observe("read", []records.Version{v}, r.CallID, "", readChannel)
			if e != nil {
				return nil, e
			}
			v.UsageReceipt = &receipt
		}
		result = v
	case "memory_adopt":
		if channel != "daily" || a.OperationID == "" || len(a.OperationID) > 128 || a.Memory == "" || a.Revision < 1 || a.Exposure == "" {
			return nil, invalid("adoption requires stable operation_id, memory_id, revision and exposure_receipt")
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
			return nil, &records.AccessError{Code: "scope_denied", Message: "adoption outside selected scope"}
		}
		v.Revision = a.Revision
		receipt, e := observe("adopt", []records.Version{v}, a.OperationID, a.Exposure, channel)
		if e != nil {
			return nil, e
		}
		result = receipt
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
		groups := map[string][]records.Version{}
		for _, h := range hits {
			ch := channel
			if records.IDEVerificationRecord(c, h.Version) {
				ch = "verification"
			}
			groups[ch] = append(groups[ch], h.Version)
		}
		if len(hits) == 0 {
			groups[channel] = []records.Version{}
		}
		batch := []records.UsageObservation{}
		channels := []string{}
		for _, ch := range []string{"daily", "verification"} {
			if versions, ok := groups[ch]; ok {
				batch = append(batch, observation("return", versions, r.CallID, "", ch))
				channels = append(channels, ch)
			}
		}
		returned, e := s.store.RecordUsageBatch(ctx, batch)
		if e != nil {
			return nil, e
		}
		observed = true
		receipts := map[string]records.UsageReceipt{}
		for index, receipt := range returned {
			receipts[channels[index]] = receipt
		}
		for i := range hits {
			ch := channel
			if records.IDEVerificationRecord(c, hits[i].Version) {
				ch = "verification"
			}
			receipt := receipts[ch]
			hits[i].Version.UsageReceipt = &receipt
		}
		receipt, ok := receipts[channel]
		if !ok {
			receipt = receipts["verification"]
		}
		result = map[string]any{"usage_receipt": receipt, "hits": hits, "scopes": scopes, "project_priority": "当前项目明确约定优先于全局建议；保留来源和适用范围"}
	}
	// A real successful tool invocation is evidence; initialization alone is
	// not. Generations invalidate old sessions after scope/config changes.
	if !observed {
		if err = s.store.TouchConnection(ctx, c.ID, c.Generation); err != nil {
			return nil, err
		}
	}
	return result, nil
}

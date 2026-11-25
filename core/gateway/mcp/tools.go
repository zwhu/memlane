package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type tool struct {
	toolDefinition
	call func(context.Context, domain.Agent, json.RawMessage) (any, error)
}

func schema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func (s *Server) memoryTools() []tool {
	text := map[string]string{"type": "string"}
	remember := schema(map[string]any{
		"project_id": text, "project_path": text, "content": text, "session_id": text, "message_id": text,
		"idempotency_key": text, "context_date": text, "metadata": map[string]string{"type": "object"},
		"kind":         map[string]any{"type": "string", "enum": []string{"document", "capture_qa", "capture_trace"}, "default": "document"},
		"auto_capture": map[string]any{"type": "boolean", "default": false},
	}, "content")
	forget := schema(map[string]any{"project_id": text, "project_path": text, "item_id": text}, "item_id")
	for _, shape := range []map[string]any{remember, forget} {
		shape["anyOf"] = []map[string][]string{{"required": {"project_id"}}, {"required": {"project_path"}}}
	}
	return []tool{
		{toolDefinition{"remember", "Save content to an authorized project.", remember}, s.remember},
		{toolDefinition{"recall", "Search authorized memory scopes.", schema(map[string]any{
			"project_id": text, "project_path": text, "query": text, "kind": text,
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 10},
		}, "query")}, s.recall},
		{toolDefinition{"forget", "Delete an item from an authorized project.", forget}, s.forget},
		{toolDefinition{"health", "Show engine health and authorized scopes.", schema(map[string]any{})}, s.health},
	}
}

type projectSelector struct {
	ProjectID   string `json:"project_id"`
	ProjectPath string `json:"project_path"`
}

func (sel projectSelector) empty() bool {
	return sel.ProjectID == "" && sel.ProjectPath == ""
}

func protected(project domain.Project) bool {
	return domain.IsProtectedProject(project.Name, project.DatasetName) ||
		domain.IsProtectedProject(project.ID, project.DatasetID)
}

func agentUserID(agent domain.Agent) string {
	if principalID := strings.TrimSpace(agent.PrincipalID); principalID != "" {
		return principalID
	}
	return agent.ID
}

func (s *Server) selectedProject(ctx context.Context, sel projectSelector) (domain.Project, error) {
	if sel.empty() {
		return domain.Project{}, domain.ErrInvalidArgument
	}
	if sel.ProjectID == "" {
		return s.cfg.Store.MatchProjectByPath(ctx, sel.ProjectPath)
	}
	project, err := s.cfg.Store.GetProject(ctx, sel.ProjectID)
	if err != nil || sel.ProjectPath == "" {
		return project, err
	}
	matched, err := s.cfg.Store.MatchProjectByPath(ctx, sel.ProjectPath)
	if err == nil && matched.ID != project.ID {
		return project, domain.ErrInvalidArgument
	}
	return project, err
}

func (s *Server) writableProject(ctx context.Context, sel projectSelector, agent domain.Agent, autoCapture bool) (domain.Project, error) {
	project, err := s.selectedProject(ctx, sel)
	if err != nil {
		return project, err
	}
	if protected(project) {
		return project, domain.ErrForbidden
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	if err != nil {
		return project, err
	}
	if acl.Access != domain.AccessMaintain || autoCapture && !acl.CanCapture {
		return project, domain.ErrForbidden
	}
	if strings.TrimSpace(project.DatasetID) == "" {
		return project, engine.ErrUnavailable
	}
	return project, nil
}

type rememberArgs struct {
	projectSelector
	Content        string            `json:"content"`
	Kind           engine.IngestKind `json:"kind"`
	AutoCapture    bool              `json:"auto_capture"`
	SessionID      string            `json:"session_id"`
	MessageID      string            `json:"message_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	ContextDate    string            `json:"context_date"`
	Metadata       map[string]any    `json:"metadata"`
}

func randomID() string {
	var id [16]byte
	rand.Read(id[:])
	return hex.EncodeToString(id[:])
}

func hash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *Server) remember(ctx context.Context, agent domain.Agent, raw json.RawMessage) (any, error) {
	var args rememberArgs
	if json.Unmarshal(raw, &args) != nil || strings.TrimSpace(args.Content) == "" {
		return nil, domain.ErrInvalidArgument
	}
	if args.Kind == "" {
		args.Kind = engine.IngestKindDocument
	}
	switch args.Kind {
	case engine.IngestKindDocument, engine.IngestKindCaptureQA, engine.IngestKindCaptureTrace:
	default:
		return nil, domain.ErrInvalidArgument
	}
	project, err := s.writableProject(ctx, args.projectSelector, agent, args.AutoCapture)
	if err != nil {
		return nil, err
	}
	userID := agentUserID(agent)
	key := args.IdempotencyKey
	if key == "" && args.SessionID != "" && args.MessageID != "" {
		identity, _ := json.Marshal([]string{userID, project.DatasetID, args.SessionID, args.MessageID, string(args.Kind)})
		key = "mcp:" + hash(identity)
	}
	if key == "" {
		key = "mcp:" + randomID()
	}
	var content any = args.Content
	if args.Kind != engine.IngestKindDocument && object(json.RawMessage(args.Content)) && json.Valid([]byte(args.Content)) {
		content = json.RawMessage(args.Content)
	}
	session := args.SessionID
	if session == "" && args.Kind != engine.IngestKindDocument {
		session = key
	}
	req := engine.IngestRequest{Scope: engine.Scope{DatasetID: project.DatasetID, UserID: userID},
		Kind: args.Kind, IdempotencyKey: key, Content: content, SourceMeta: engine.SourceMeta{
			SessionID: session, ContextDate: args.ContextDate, ExternalMetadata: args.Metadata,
		}}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	receipt := domain.CaptureReceipt{CaptureKey: key, UserID: userID, DatasetID: project.DatasetID,
		SessionID: args.SessionID, MessageID: args.MessageID, PayloadSHA256: hash(payload), EntryType: string(args.Kind)}
	if receipt.SessionID == "" {
		receipt.SessionID = key
	}
	if receipt.MessageID == "" {
		receipt.MessageID = key
	}
	state, prior, err := s.cfg.Store.ReserveCaptureReceipt(ctx, receipt)
	if err != nil {
		return nil, err
	}
	reply := map[string]any{"ok": true, "idempotency_key": key, "dataset_id": project.DatasetID}
	switch state {
	case "saved":
		reply["entry_id"], reply["deduplicated"] = prior.EntryID, true
		return reply, nil
	case "reserved":
	default:
		return nil, domain.ErrConflict
	}
	result, err := s.cfg.Engine.Ingest(ctx, req)
	entryID := result.EntryID
	if entryID == "" {
		entryID = result.DocumentID
	}
	finalize := context.WithoutCancel(ctx)
	if err != nil || !result.OK || strings.TrimSpace(entryID) == "" {
		if err == nil {
			err = engine.ErrUnavailable
		}
		return nil, errors.Join(err, s.cfg.Store.AbortPendingCaptureReceipt(finalize, key))
	}
	if err := s.cfg.Store.MarkCaptureReceiptSaved(finalize, key, entryID); err != nil {
		return nil, err
	}
	reply["entry_id"], reply["document_id"], reply["deduplicated"] = entryID, result.DocumentID, result.Deduplicated
	return reply, nil
}

func (s *Server) forget(ctx context.Context, agent domain.Agent, raw json.RawMessage) (any, error) {
	var args struct {
		projectSelector
		ItemID string `json:"item_id"`
	}
	if json.Unmarshal(raw, &args) != nil || strings.TrimSpace(args.ItemID) == "" {
		return nil, domain.ErrInvalidArgument
	}
	project, err := s.writableProject(ctx, args.projectSelector, agent, false)
	if err != nil {
		return nil, err
	}
	scope := engine.Scope{DatasetID: project.DatasetID, UserID: agentUserID(agent)}
	if err := s.cfg.Engine.DeleteItem(ctx, scope, args.ItemID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

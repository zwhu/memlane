package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
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
	remember["anyOf"] = []map[string][]string{{"required": {"project_id"}}, {"required": {"project_path"}}}
	return []tool{
		{toolDefinition{"remember", "Save content to an authorized project.", remember}, s.remember},
		{toolDefinition{"recall", "Search authorized memory scopes.", schema(map[string]any{
			"project_id": text, "project_path": text, "query": text, "kind": text,
			"mode":  map[string]any{"type": "string", "enum": []string{"hybrid", "graph", "chunks", "code"}, "default": "hybrid"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 10},
		}, "query")}, s.recall},
		{toolDefinition{"health", "Show engine health and authorized scopes.", schema(map[string]any{})}, s.health},
		{toolDefinition{"remember_status", "Check the status of a previously submitted remember task.", schema(map[string]any{
			"task_id": text,
		}, "task_id")}, s.rememberStatus},
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
	principalID := strings.TrimSpace(agent.PrincipalID)
	if id, err := uuid.Parse(principalID); err == nil {
		return id.String()
	}
	return ""
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

func canonicalRememberContent(raw string, kind engine.IngestKind) (any, any) {
	if kind != engine.IngestKindDocument && object(json.RawMessage(raw)) && json.Valid([]byte(raw)) {
		var parsed any
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err == nil {
			if canonicalJSON, err := json.Marshal(parsed); err == nil {
				return parsed, json.RawMessage(canonicalJSON)
			}
			return parsed, json.RawMessage(raw)
		}
	}
	return raw, raw
}

func deterministicRememberKey(agentID, receiptUser, datasetID string, args rememberArgs, canonicalContent any) string {
	if key := strings.TrimSpace(args.IdempotencyKey); key != "" {
		return key
	}
	if args.SessionID != "" && args.MessageID != "" {
		identity, _ := json.Marshal([]string{receiptUser, datasetID, args.SessionID, args.MessageID, string(args.Kind)})
		return "mcp:" + hash(identity)
	}
	var canonicalMeta any
	if len(args.Metadata) > 0 {
		canonicalMeta = args.Metadata
	}
	identity, _ := json.Marshal([]any{
		agentID,
		receiptUser,
		datasetID,
		string(args.Kind),
		args.SessionID,
		args.MessageID,
		canonicalContent,
		strings.TrimSpace(args.ContextDate),
		canonicalMeta,
	})
	return "mcp:" + hash(identity)
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
	receiptUser := userID
	if receiptUser == "" {
		receiptUser = agent.ID
	}
	canonicalForKey, content := canonicalRememberContent(args.Content, args.Kind)
	var rawContent any
	if args.Kind != engine.IngestKindDocument && object(json.RawMessage(args.Content)) && json.Valid([]byte(args.Content)) {
		rawContent = json.RawMessage(args.Content)
	}
	key := deterministicRememberKey(agent.ID, receiptUser, project.DatasetID, args, canonicalForKey)
	if svc := s.rememberService(); svc != nil {
		return svc.Remember(ctx, RememberRequest{
			Agent:          agent,
			Project:        project,
			UserID:         userID,
			ReceiptUser:    receiptUser,
			Kind:           args.Kind,
			Content:        content,
			RawContent:     rawContent,
			SessionID:      args.SessionID,
			MessageID:      args.MessageID,
			IdempotencyKey: key,
			ContextDate:    args.ContextDate,
			Metadata:       args.Metadata,
		})
	}
	return s.rememberDirect(ctx, project, userID, receiptUser, key, content, args)
}

func (s *Server) rememberDirect(ctx context.Context, project domain.Project, userID, receiptUser, key string, content any, args rememberArgs) (any, error) {
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
	receipt := domain.CaptureReceipt{CaptureKey: key, UserID: receiptUser, DatasetID: project.DatasetID,
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
	reply := map[string]any{"ok": true, "status": "saved", "saved": true, "idempotency_key": key, "dataset_id": project.DatasetID}
	switch state {
	case "saved":
		reply["entry_id"], reply["document_id"], reply["deduplicated"] = prior.EntryID, prior.EntryID, true
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

func (s *Server) rememberStatus(ctx context.Context, agent domain.Agent, raw json.RawMessage) (any, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(raw, &args) != nil || strings.TrimSpace(args.TaskID) == "" {
		return nil, domain.ErrInvalidArgument
	}
	taskID := strings.TrimSpace(args.TaskID)
	if svc := s.rememberService(); svc != nil {
		return svc.LookupTask(ctx, agent, taskID)
	}
	return s.lookupTaskDirect(ctx, agent, taskID)
}

func (s *Server) lookupTaskDirect(ctx context.Context, agent domain.Agent, taskID string) (TaskStatusResult, error) {
	item, err := s.cfg.Store.GetInboxItem(ctx, taskID)
	if err != nil {
		return TaskStatusResult{}, err
	}
	if item.AgentID != agent.ID {
		return TaskStatusResult{}, domain.ErrForbidden
	}
	project, err := s.cfg.Store.GetProject(ctx, item.ProjectID)
	if err != nil {
		return TaskStatusResult{}, domain.ErrForbidden
	}
	if protected(project) {
		return TaskStatusResult{}, domain.ErrForbidden
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	if err != nil {
		return TaskStatusResult{}, err
	}
	if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
		return TaskStatusResult{}, domain.ErrForbidden
	}
	var entryID string
	if item.CaptureKey != "" {
		if receipt, err := s.cfg.Store.GetCaptureReceipt(ctx, item.CaptureKey); err == nil {
			entryID = receipt.EntryID
		}
	}
	return TaskStatusResult{
		OK:             true,
		TaskID:         item.ID,
		ProjectID:      item.ProjectID,
		DatasetID:      item.DatasetID,
		Status:         item.Status,
		Saved:          item.Status == "succeeded",
		EntryID:        entryID,
		DocumentID:     entryID,
		IdempotencyKey: item.CaptureKey,
		LastError:      item.LastError,
		UpdatedAt:      item.UpdatedAt,
	}, nil
}

package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const (
	maxPendingPromptSessions = 256
	maxPendingTurnsPerSess   = 8
	pendingPromptTTL         = 2 * time.Hour
	maxPendingPromptRunes    = 32000
)

type pendingPrompt struct {
	Prompt     string
	TurnID     string
	PromptTime string
	DatasetIDs []string
	RecordedAt time.Time
}

func (p pendingPrompt) matchesDataset(datasetID string) bool {
	if datasetID == "" {
		return true
	}
	return slices.Contains(p.DatasetIDs, datasetID)
}

type sessionPromptKey struct {
	agentID   string
	sessionID string
}

type sessionPromptState struct {
	pending    []pendingPrompt
	lastPrompt *pendingPrompt
	updatedAt  time.Time
}

type pendingPromptStore struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[sessionPromptKey]*sessionPromptState
	order   []sessionPromptKey
}

func (p *pendingPromptStore) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *pendingPromptStore) pruneLocked(now time.Time) {
	if len(p.entries) == 0 {
		return
	}
	keptOrder := p.order[:0]
	for _, key := range p.order {
		state, ok := p.entries[key]
		if !ok {
			continue
		}
		if now.Sub(state.updatedAt) > pendingPromptTTL {
			delete(p.entries, key)
			continue
		}
		if len(state.pending) > 0 {
			live := state.pending[:0]
			for _, item := range state.pending {
				if now.Sub(item.RecordedAt) <= pendingPromptTTL {
					live = append(live, item)
				}
			}
			state.pending = live
		}
		if state.lastPrompt != nil && now.Sub(state.lastPrompt.RecordedAt) > pendingPromptTTL {
			state.lastPrompt = nil
		}
		if len(state.pending) == 0 && state.lastPrompt == nil {
			delete(p.entries, key)
			continue
		}
		keptOrder = append(keptOrder, key)
	}
	p.order = keptOrder
}

func (p *pendingPromptStore) touchLocked(key sessionPromptKey) {
	for i, existing := range p.order {
		if existing == key {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.order = append(p.order, key)
}

func (p *pendingPromptStore) record(agentID, sessionID string, item pendingPrompt) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	item.Prompt = strings.TrimSpace(item.Prompt)
	item.TurnID = strings.TrimSpace(item.TurnID)
	item.PromptTime = strings.TrimSpace(item.PromptTime)
	item.DatasetIDs = dedupeTrimmedStrings(item.DatasetIDs)
	if agentID == "" || sessionID == "" || item.Prompt == "" {
		return
	}
	if utf8.RuneCountInString(item.Prompt) > maxPendingPromptRunes {
		item.Prompt = truncateRunes(item.Prompt, maxPendingPromptRunes)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	if item.RecordedAt.IsZero() {
		item.RecordedAt = now
	}
	p.pruneLocked(now)
	if p.entries == nil {
		p.entries = make(map[sessionPromptKey]*sessionPromptState)
	}
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	state, ok := p.entries[key]
	if !ok {
		for len(p.order) >= maxPendingPromptSessions {
			oldest := p.order[0]
			p.order = p.order[1:]
			delete(p.entries, oldest)
		}
		state = &sessionPromptState{}
		p.entries[key] = state
	}
	lastCopy := item
	lastCopy.DatasetIDs = slices.Clone(item.DatasetIDs)
	state.lastPrompt = &lastCopy
	state.updatedAt = now

	filtered := state.pending[:0]
	for _, existing := range state.pending {
		if existing.TurnID == item.TurnID {
			continue
		}
		filtered = append(filtered, existing)
	}
	state.pending = append(filtered, item)
	if len(state.pending) > maxPendingTurnsPerSess {
		state.pending = append([]pendingPrompt(nil), state.pending[len(state.pending)-maxPendingTurnsPerSess:]...)
	}
	p.touchLocked(key)
}

func findPendingIndexLocked(state *sessionPromptState, datasetID, turnID, messageID string) int {
	if turnID != "" {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == turnID && state.pending[i].matchesDataset(datasetID) {
				return i
			}
		}
	}
	if messageID != "" && messageID != turnID {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == messageID && state.pending[i].matchesDataset(datasetID) {
				return i
			}
		}
	}
	if turnID != "" {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == "" && state.pending[i].matchesDataset(datasetID) {
				return i
			}
		}
		return -1
	}
	for i := len(state.pending) - 1; i >= 0; i-- {
		if state.pending[i].matchesDataset(datasetID) {
			return i
		}
	}
	return -1
}

func (p *pendingPromptStore) peekPending(agentID, sessionID, datasetID, turnID, messageID string) (pendingPrompt, bool) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	datasetID = strings.TrimSpace(datasetID)
	turnID = strings.TrimSpace(turnID)
	messageID = strings.TrimSpace(messageID)
	if agentID == "" || sessionID == "" {
		return pendingPrompt{}, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	p.pruneLocked(now)
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	state, ok := p.entries[key]
	if !ok || len(state.pending) == 0 {
		return pendingPrompt{}, false
	}
	idx := findPendingIndexLocked(state, datasetID, turnID, messageID)
	if idx < 0 {
		return pendingPrompt{}, false
	}
	chosen := state.pending[idx]
	chosen.DatasetIDs = slices.Clone(chosen.DatasetIDs)
	return chosen, true
}

func (p *pendingPromptStore) ackPending(agentID, sessionID string, matched pendingPrompt) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	p.pruneLocked(now)
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	state, ok := p.entries[key]
	if !ok || len(state.pending) == 0 {
		return
	}
	for i := len(state.pending) - 1; i >= 0; i-- {
		item := state.pending[i]
		if item.TurnID == matched.TurnID && item.Prompt == matched.Prompt && item.RecordedAt.Equal(matched.RecordedAt) {
			state.pending = append(state.pending[:i], state.pending[i+1:]...)
			state.updatedAt = now
			p.touchLocked(key)
			return
		}
	}
}

func (p *pendingPromptStore) popPending(agentID, sessionID, datasetID, turnID, messageID string) (pendingPrompt, bool) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	datasetID = strings.TrimSpace(datasetID)
	turnID = strings.TrimSpace(turnID)
	messageID = strings.TrimSpace(messageID)
	if agentID == "" || sessionID == "" {
		return pendingPrompt{}, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	p.pruneLocked(now)
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	state, ok := p.entries[key]
	if !ok || len(state.pending) == 0 {
		return pendingPrompt{}, false
	}
	idx := findPendingIndexLocked(state, datasetID, turnID, messageID)
	if idx < 0 {
		return pendingPrompt{}, false
	}
	chosen := state.pending[idx]
	chosen.DatasetIDs = slices.Clone(chosen.DatasetIDs)
	state.pending = append(state.pending[:idx], state.pending[idx+1:]...)
	state.updatedAt = now
	p.touchLocked(key)
	return chosen, true
}

func (p *pendingPromptStore) last(agentID, sessionID string) (pendingPrompt, bool) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" {
		return pendingPrompt{}, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	p.pruneLocked(now)
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	state, ok := p.entries[key]
	if !ok || state.lastPrompt == nil {
		return pendingPrompt{}, false
	}
	p.touchLocked(key)
	out := *state.lastPrompt
	out.DatasetIDs = slices.Clone(out.DatasetIDs)
	return out, true
}

func (p *pendingPromptStore) clear(agentID, sessionID string) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	key := sessionPromptKey{agentID: agentID, sessionID: sessionID}
	delete(p.entries, key)
	for i, existing := range p.order {
		if existing == key {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

type hookScope struct {
	ProjectID string `json:"project_id"`
	Workspace string `json:"workspace"`
	SessionID string `json:"session_id"`
}

func (s *Server) hookProject(ctx context.Context, scope hookScope, agentID string) (domain.Project, error) {
	switch {
	case scope.ProjectID != "":
		return s.cfg.Store.GetProject(ctx, scope.ProjectID)
	case scope.Workspace != "":
		p, err := s.cfg.Store.MatchProjectByPath(ctx, scope.Workspace)
		if err == nil || !errors.Is(err, domain.ErrNotFound) || scope.SessionID == "" {
			return p, err
		}
		fallthrough
	case scope.SessionID != "":
		binding, err := s.cfg.Store.GetSessionBinding(ctx, sessionBindingKey(agentID, scope.SessionID))
		if errors.Is(err, domain.ErrNotFound) {
			binding, err = s.cfg.Store.GetSessionBinding(ctx, scope.SessionID)
		}
		if err != nil {
			return domain.Project{}, err
		}
		return s.cfg.Store.GetProject(ctx, binding.ProjectID)
	default:
		return domain.Project{}, domain.ErrNotFound
	}
}

type inboxPayload struct {
	Content        json.RawMessage    `json:"content"`
	SourceMeta     engine.SourceMeta  `json:"source_meta"`
	UserID         string             `json:"user_id"`
	EngineUserID   string             `json:"engine_user_id,omitempty"`
	ExtractMode    engine.ExtractMode `json:"extract_mode"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
}

type captureRequest struct {
	hookScope
	inboxPayload
	Kind            engine.IngestKind `json:"kind"`
	Mode            string            `json:"mode"`
	MessageID       string            `json:"message_id"`
	TurnID          string            `json:"turn_id"`
	Host            string            `json:"host"`
	CaptureKey      string            `json:"capture_key"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	SubagentRole    string            `json:"subagent_role,omitempty"`
}

func captureQAContextDate(content any) string {
	fields, _ := content.(map[string]any)
	contextFields, _ := fields["context"].(map[string]any)
	if encoded, ok := fields["context"].(string); ok {
		if err := json.Unmarshal([]byte(encoded), &contextFields); err != nil {
			contextFields = nil
		}
	}
	for _, value := range []any{contextFields["date"], fields["prompt_time"], fields["time"]} {
		if raw, ok := value.(string); ok {
			if day := domain.LocalCalendarDate(raw, time.Local); day != "" {
				return day
			}
		}
	}
	return time.Now().Format(time.DateOnly)
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	var req captureRequest
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &req) {
		return
	}
	if req.SessionID == "" {
		req.SessionID = req.SourceMeta.SessionID
	}
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	project, err := s.hookProject(ctx, req.hookScope, agent.ID)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if domain.IsProtectedProject(project.Name, project.DatasetName) {
		writeError(w, 403, "project_protected", "host capture is disabled for protected projects")
		return
	}
	if project.DatasetID == "" {
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	if errors.Is(err, domain.ErrForbidden) || err == nil && acl.Access != domain.AccessMaintain {
		writeError(w, 403, "dataset_write_denied", "project maintain access required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	manual := req.Mode == "manual" || strings.EqualFold(r.Header.Get("X-Memlane-Capture-Mode"), "manual")
	if !manual && !acl.CanCapture {
		writeJSON(w, 403, map[string]any{"error": "capture_paused", "message": "automatic capture is paused",
			"capture_scope": acl.CaptureScope, "project_auto_capture": acl.ProjectAutoCapture, "auto_capture_version": acl.AutoCaptureVersion})
		return
	}
	if req.Kind == "" {
		req.Kind = engine.IngestKindCaptureQA
	}
	if req.Kind != engine.IngestKindCaptureQA && req.Kind != engine.IngestKindCaptureTrace && req.Kind != engine.IngestKindDocument {
		writeError(w, 400, "invalid_kind", "unsupported ingest kind")
		return
	}
	var content any
	decoder := json.NewDecoder(strings.NewReader(string(req.Content)))
	decoder.UseNumber()
	if err := decoder.Decode(&content); err != nil || content == nil {
		writeError(w, 400, "invalid_content", "capture content required")
		return
	}
	var pairedPrompt pendingPrompt
	usedPendingPrompt := false
	if req.Kind == engine.IngestKindCaptureQA && strings.TrimSpace(req.SessionID) != "" {
		if fields, ok := content.(map[string]any); ok {
			questionText, _ := fields["question"].(string)
			answerText, _ := fields["answer"].(string)
			if strings.TrimSpace(questionText) == "" && strings.TrimSpace(answerText) != "" {
				explicitTurnID := strings.TrimSpace(req.TurnID)
				if explicitTurnID == "" {
					if rawTurnID, ok := fields["turn_id"].(string); ok {
						explicitTurnID = strings.TrimSpace(rawTurnID)
					}
				}
				pending, found := s.prompts.peekPending(agent.ID, req.SessionID, project.DatasetID, explicitTurnID, req.MessageID)
				if !found {
					writeJSON(w, http.StatusOK, map[string]any{
						"ok": true, "accepted": false, "status": "skipped_missing_question",
						"project_id": project.ID, "dataset_id": project.DatasetID,
					})
					return
				}
				pairedPrompt = pending
				usedPendingPrompt = true
				fields["question"] = pending.Prompt
				existingPromptTime, _ := fields["prompt_time"].(string)
				if strings.TrimSpace(existingPromptTime) == "" && pending.PromptTime != "" {
					fields["prompt_time"] = pending.PromptTime
				}
			}
		}
	}
	if s.filterCapture(w, ctx, req, agent, project, content) {
		if usedPendingPrompt {
			s.prompts.ackPending(agent.ID, req.SessionID, pairedPrompt)
		}
		return
	}
	req.Content, err = json.Marshal(content)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	req.EngineUserID = ""
	if req.UserID == "" {
		req.UserID = agent.PrincipalID
	}
	receiptUser := req.UserID
	if receiptUser == "" {
		receiptUser = agent.ID
	}
	explicitSession := req.SessionID
	if req.SessionID == "" {
		req.SessionID = agent.ID
	}
	req.SourceMeta.SessionID = req.SessionID
	if req.ExtractMode == "" {
		req.ExtractMode = engine.ExtractModeDefault
	}
	requestKey := req.CaptureKey
	if requestKey == "" {
		requestKey = req.IdempotencyKey
	}
	req.IdempotencyKey = ""
	canonical, err := json.Marshal(req.inboxPayload)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	payloadHash := digest(canonical)
	if req.MessageID == "" {
		req.MessageID = requestKey
	}
	if req.MessageID == "" {
		req.MessageID = payloadHash
	}
	captureKey := tupleDigest(agent.ID, project.DatasetID, receiptUser, req.SessionID, req.MessageID, string(req.Kind))
	if requestKey != "" {
		captureKey = tupleDigest(agent.ID, project.DatasetID, requestKey)
	}
	if req.Kind == engine.IngestKindCaptureQA && strings.TrimSpace(req.SourceMeta.ContextDate) == "" {
		req.SourceMeta.ContextDate = captureQAContextDate(content)
	}
	req.IdempotencyKey = tupleDigest(agent.ID, project.DatasetID, captureKey)
	encoded, err := json.Marshal(req.inboxPayload)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	receipt := domain.CaptureReceipt{CaptureKey: captureKey, UserID: receiptUser, DatasetID: project.DatasetID,
		SessionID: req.SessionID, MessageID: req.MessageID, PayloadSHA256: payloadHash, EntryType: string(req.Kind)}
	itemID := tupleDigest(agent.ID, project.DatasetID, captureKey)
	enqueueInput := domain.EnqueueCaptureInput{
		Receipt: receipt,
		Inbox: domain.CaptureInboxItem{
			ID: itemID, ProjectID: project.ID, DatasetID: project.DatasetID, AgentID: agent.ID,
			SessionID: req.SessionID, MessageID: req.MessageID, CaptureKey: captureKey,
			Kind: string(req.Kind), PayloadJSON: string(encoded),
		},
		Limits: domain.CaptureLimits{
			MaxPendingItems: s.cfg.InboxMaxPendingItems,
			MaxPendingBytes: s.cfg.InboxMaxPendingBytes,
		},
	}
	if explicitSession != "" {
		enqueueInput.SessionBinding = &domain.SessionBinding{
			SessionKey: sessionBindingKey(agent.ID, explicitSession),
			ProjectID:  project.ID,
			UpdatedAt:  float64(time.Now().UnixMilli()) / 1000,
		}
		parentSessionID := strings.TrimSpace(req.ParentSessionID)
		subagentRole := strings.TrimSpace(req.SubagentRole)
		if ext := req.SourceMeta.ExternalMetadata; ext != nil {
			if parentSessionID == "" {
				if v, ok := ext["parent_session_id"].(string); ok {
					parentSessionID = strings.TrimSpace(v)
				}
			}
			if subagentRole == "" {
				if v, ok := ext["subagent_role"].(string); ok {
					subagentRole = strings.TrimSpace(v)
				}
			}
		}
		if parentSessionID != "" && parentSessionID == req.SessionID {
			parentSessionID = ""
			subagentRole = ""
		}
		if parentSessionID != "" || subagentRole != "" {
			enqueueInput.SessionRelation = &domain.SessionRelation{
				DatasetID:       project.DatasetID,
				SessionID:       req.SessionID,
				ParentSessionID: parentSessionID,
				SubagentRole:    subagentRole,
			}
		}
	}
	result, err := s.cfg.Store.ReserveAndEnqueueCapture(ctx, enqueueInput)
	if errors.Is(err, domain.ErrConflict) {
		writeError(w, http.StatusConflict, "capture_conflict", "capture key already has another payload or scope")
		return
	}
	if errors.Is(err, domain.ErrInboxCapacityExceeded) {
		writeError(w, http.StatusInsufficientStorage, "inbox_capacity_exceeded", "capture inbox capacity limit reached")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if usedPendingPrompt {
		s.prompts.ackPending(agent.ID, explicitSession, pairedPrompt)
	}
	response := map[string]any{
		"ok": true, "accepted": true,
		"capture_key": captureKey, "project_id": project.ID, "dataset_id": project.DatasetID,
	}
	status := http.StatusOK
	if !result.Deduplicated {
		s.inbox.Notify()
		status = http.StatusAccepted
		response["inbox_id"] = result.InboxItem.ID
		response["status"] = result.InboxItem.Status
		response["deduplicated"] = false
	} else {
		response["deduplicated"] = true
		if result.HasInboxItem {
			response["inbox_id"] = result.InboxItem.ID
			response["status"] = result.InboxItem.Status
		} else {
			response["status"] = result.Receipt.Status
		}
	}
	writeJSON(w, status, response)
}

func sessionBindingKey(agentID, sessionID string) string {
	// Preserve the composite key used by the legacy registry.
	agent, err := json.Marshal(agentID)
	if err != nil {
		panic(err)
	}
	session, err := json.Marshal(sessionID)
	if err != nil {
		panic(err)
	}
	return "[" + string(agent) + ", " + string(session) + "]"
}

func tupleDigest(parts ...string) string {
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(err)
	}
	return digest(raw)
}

type recallRequest struct {
	hookScope
	Query                 string            `json:"query"`
	Q                     string            `json:"q"`
	DatasetIDs            []string          `json:"dataset_ids"`
	Kind                  string            `json:"kind"`
	Scope                 string            `json:"scope"`
	Channel               string            `json:"channel"`
	Limit                 int               `json:"limit"`
	Mode                  engine.SearchMode `json:"mode"`
	Purpose               string            `json:"purpose"`
	IncludeSessionContext bool              `json:"include_session_context"`
	NodeSets              []string          `json:"node_sets"`
	TurnID                string            `json:"turn_id"`
	PromptTime            string            `json:"prompt_time"`
}

func resolveRecallMode(raw engine.SearchMode, kind, scope string) (engine.SearchMode, bool) {
	mode := engine.SearchMode(strings.TrimSpace(string(raw)))
	if mode == "" {
		if strings.EqualFold(kind, "code") || strings.EqualFold(scope, "code") {
			return engine.SearchModeCode, true
		}
		return engine.SearchModeHybrid, true
	}
	switch mode {
	case engine.SearchModeHybrid, engine.SearchModeGraph, engine.SearchModeChunks, engine.SearchModeCode:
		return mode, true
	default:
		return "", false
	}
}

func validRecallPurpose(purpose string) bool {
	switch purpose {
	case "", "precompact", "compact_restore":
		return true
	default:
		return false
	}
}

func (s *Server) recallScopes(ctx context.Context, req recallRequest, agent domain.Agent) ([]engine.Scope, bool, string, error) {
	projects, err := s.cfg.Store.ListProjects(ctx)
	if err != nil {
		return nil, false, "", err
	}
	explicit := req.ProjectID != "" || req.Workspace != "" || req.SessionID != ""
	if explicit {
		p, err := s.hookProject(ctx, req.hookScope, agent.ID)
		if err != nil {
			return nil, false, "", err
		}
		projects = []domain.Project{p}
	}
	codeOnly := strings.EqualFold(req.Kind, "code") || strings.EqualFold(req.Scope, "code") || req.Mode == engine.SearchModeCode
	type target struct {
		projectID string
		code      bool
	}
	targets := make(map[string][]target)
	for _, p := range projects {
		if p.DatasetID != "" {
			targets[p.DatasetID] = append(targets[p.DatasetID], target{p.ID, false})
		}
		if p.CodeEnabled && p.CodeDatasetID != "" {
			targets[p.CodeDatasetID] = append(targets[p.CodeDatasetID], target{p.ID, true})
		}
	}
	common, err := s.cfg.Store.GetCommonConfig(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, false, "", err
	}
	ids := req.DatasetIDs
	if len(ids) == 0 {
		for _, p := range projects {
			if (!codeOnly || !p.CodeEnabled || p.CodeDatasetID == "") && p.DatasetID != "" {
				ids = append(ids, p.DatasetID)
			}
			if codeOnly && p.CodeEnabled && p.CodeDatasetID != "" {
				ids = append(ids, p.CodeDatasetID)
			}
		}
	}
	scopes := make([]engine.Scope, 0)
	seen := make(map[string]bool)
	allCode := true
	commonRequested := false
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if id != "" && id == common.DatasetID {
			commonRequested = true
			continue
		}
		if len(targets[id]) != 1 {
			return nil, false, "", domain.ErrForbidden
		}
		target := targets[id][0]
		acl, err := s.cfg.Store.EvaluateProjectACL(ctx, target.projectID, agent.ID)
		if err != nil {
			return nil, false, "", err
		}
		if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
			if explicit || len(req.DatasetIDs) > 0 {
				return nil, false, "", domain.ErrForbidden
			}
			continue
		}
		seen[id] = true
		allCode = allCode && target.code
		scopes = append(scopes, engine.Scope{DatasetID: id})
	}
	if explicit && len(scopes) == 0 {
		return nil, false, "", domain.ErrForbidden
	}
	include := false
	commonEligible := !codeOnly && !(len(scopes) > 0 && allCode)
	if common.DatasetID != "" && (commonRequested || commonEligible) {
		acl, err := s.cfg.Store.EvaluateCommonACL(ctx, agent.ID)
		if err != nil {
			return nil, false, "", err
		}
		if commonRequested && acl.Access != domain.AccessRead {
			return nil, false, "", domain.ErrForbidden
		}
		if commonEligible && acl.Access == domain.AccessRead {
			scopes = append(scopes, engine.Scope{DatasetID: common.DatasetID})
			include = true
		}
	}
	if len(scopes) == 0 {
		return nil, false, "", domain.ErrForbidden
	}
	return scopes, include, common.DatasetID, nil
}

func projectScopeDatasets(scopes []engine.Scope, commonDatasetID string) []string {
	datasets := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope.DatasetID != "" && scope.DatasetID != commonDatasetID {
			datasets = append(datasets, scope.DatasetID)
		}
	}
	if len(datasets) > 0 {
		return dedupeTrimmedStrings(datasets)
	}
	for _, scope := range scopes {
		if scope.DatasetID != "" {
			datasets = append(datasets, scope.DatasetID)
		}
	}
	return dedupeTrimmedStrings(datasets)
}

func promptMatchesScopes(promptDatasets []string, scopes []engine.Scope, commonDatasetID string) bool {
	if len(promptDatasets) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(scopes))
	for _, ds := range projectScopeDatasets(scopes, commonDatasetID) {
		allowed[ds] = true
	}
	for _, ds := range promptDatasets {
		if allowed[ds] {
			return true
		}
	}
	return false
}

type sessionOwnership uint8

const (
	sessionOwnershipUnknown sessionOwnership = iota
	sessionOwnershipAuthorized
	sessionOwnershipConflict
)

func sessionBindingMatchesSession(sessionKey, sessionID string) bool {
	if sessionKey == sessionID {
		return true
	}
	var tuple []string
	if err := json.Unmarshal([]byte(sessionKey), &tuple); err == nil && len(tuple) == 2 && tuple[1] == sessionID {
		return true
	}
	return false
}

func normalizeDatasetIDKey(raw string) string {
	return strings.ToLower(strings.ReplaceAll(domain.CanonicalUUID(raw), "-", ""))
}

// verifyAuthorizedSession checks whether sessionID belongs to an authorized project dataset in scopes
// and has no conflicting ownership in an unauthorized dataset before allowing session-scoped reads.
func (s *Server) verifyAuthorizedSession(ctx context.Context, _ domain.Agent, sessionID string, scopes []engine.Scope, commonDatasetID string) (sessionOwnership, string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" || len(scopes) == 0 {
		return sessionOwnershipUnknown, ""
	}
	commonNorm := normalizeDatasetIDKey(commonDatasetID)
	allowed := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		norm := normalizeDatasetIDKey(scope.DatasetID)
		if norm != "" && norm != commonNorm {
			allowed[norm] = true
		}
	}
	if len(allowed) == 0 {
		return sessionOwnershipUnknown, ""
	}

	type sessionOwner struct {
		datasetID string
		userID    string
	}
	owners, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT dataset_id, user_id FROM catalog_sessions WHERE session_id=? ORDER BY sort_ts_desc_key ASC, dataset_id, user_id`,
		[]any{sid}, func(row rowScanner) (sessionOwner, error) {
			var owner sessionOwner
			err := row.Scan(&owner.datasetID, &owner.userID)
			return owner, err
		})
	if err != nil {
		log.Printf("gateway recall session ownership lookup: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	catalogAuthorized := false
	catalogUserID := ""
	for _, owner := range owners {
		if !allowed[normalizeDatasetIDKey(owner.datasetID)] {
			return sessionOwnershipConflict, ""
		}
		catalogAuthorized = true
		if catalogUserID == "" && strings.TrimSpace(owner.userID) != "" {
			catalogUserID = strings.TrimSpace(owner.userID)
		}
	}

	activeRows, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT dataset_id FROM projects WHERE dataset_id<>''
		 UNION
		 SELECT code_dataset_id FROM projects WHERE code_dataset_id<>''`,
		nil, func(row rowScanner) (string, error) {
			var ds string
			err := row.Scan(&ds)
			return ds, err
		})
	if err != nil {
		log.Printf("gateway recall active project datasets lookup: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	activeProjectDatasets := make(map[string]bool, len(activeRows))
	for _, ds := range activeRows {
		if norm := normalizeDatasetIDKey(ds); norm != "" {
			activeProjectDatasets[norm] = true
		}
	}

	captureDatasets, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT DISTINCT dataset_id FROM capture_receipts WHERE session_id=?
		 UNION
		 SELECT DISTINCT dataset_id FROM capture_inbox WHERE session_id=?`,
		[]any{sid, sid}, func(row rowScanner) (string, error) {
			var datasetID string
			err := row.Scan(&datasetID)
			return datasetID, err
		})
	if err != nil {
		log.Printf("gateway recall session capture lookup: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	captureAuthorized := false
	hasRemovedProjectResidual := false
	for _, rawDataset := range captureDatasets {
		norm := normalizeDatasetIDKey(rawDataset)
		if norm == "" {
			continue
		}
		if allowed[norm] {
			captureAuthorized = true
			continue
		}
		if activeProjectDatasets[norm] {
			return sessionOwnershipConflict, ""
		}
		hasRemovedProjectResidual = true
	}

	removedOwnerDatasets, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT DISTINCT dataset_id FROM removed_session_owners WHERE session_id=?`,
		[]any{sid}, func(row rowScanner) (string, error) {
			var datasetID string
			err := row.Scan(&datasetID)
			return datasetID, err
		})
	if err != nil {
		log.Printf("gateway recall removed session owners lookup: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	for _, rawDataset := range removedOwnerDatasets {
		norm := normalizeDatasetIDKey(rawDataset)
		if norm == "" || allowed[norm] {
			continue
		}
		hasRemovedProjectResidual = true
	}

	encodedSession, err := json.Marshal(sid)
	if err != nil {
		log.Printf("gateway recall session encode: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	bindingSuffix := string(encodedSession) + "]"
	type bindingRow struct {
		sessionKey string
		projectID  string
	}
	bindings, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT session_key, project_id FROM session_bindings WHERE session_key=? OR substr(session_key, -length(?))=?`,
		[]any{sid, bindingSuffix, bindingSuffix}, func(row rowScanner) (bindingRow, error) {
			var b bindingRow
			err := row.Scan(&b.sessionKey, &b.projectID)
			return b, err
		})
	if err != nil {
		log.Printf("gateway recall session binding lookup: %s", sanitizeError(err))
		return sessionOwnershipConflict, ""
	}
	bindingAuthorized := false
	for _, b := range bindings {
		if !sessionBindingMatchesSession(b.sessionKey, sid) {
			continue
		}
		project, pErr := s.cfg.Store.GetProject(ctx, b.projectID)
		if pErr != nil {
			if !errors.Is(pErr, domain.ErrNotFound) {
				log.Printf("gateway recall session binding project: %s", sanitizeError(pErr))
			}
			return sessionOwnershipConflict, ""
		}
		if !allowed[normalizeDatasetIDKey(project.DatasetID)] {
			return sessionOwnershipConflict, ""
		}
		bindingAuthorized = true
	}

	if hasRemovedProjectResidual {
		// Historical receipts/inbox rows from a removed project dataset share this session_id.
		// Degrade to Unknown so normal recall still succeeds, while disabling session-scoped
		// cache.db reads that could otherwise pull context from the removed dataset.
		return sessionOwnershipUnknown, ""
	}
	if catalogAuthorized {
		return sessionOwnershipAuthorized, catalogUserID
	}
	if captureAuthorized || bindingAuthorized {
		return sessionOwnershipAuthorized, ""
	}
	return sessionOwnershipUnknown, ""
}

func (s *Server) resolveCompactQuery(ctx context.Context, agent domain.Agent, sessionID string, scopes []engine.Scope, commonDatasetID string, ownership sessionOwnership, sessionUserID string) string {
	sid := strings.TrimSpace(sessionID)
	if sid == "" || ownership == sessionOwnershipConflict {
		return ""
	}
	if last, ok := s.prompts.last(agent.ID, sid); ok && strings.TrimSpace(last.Prompt) != "" &&
		promptMatchesScopes(last.DatasetIDs, scopes, commonDatasetID) {
		return strings.TrimSpace(last.Prompt)
	}
	if ownership != sessionOwnershipAuthorized || sessionUserID == "" {
		return ""
	}
	reader := s.sessionHistoryReader()
	if reader == nil {
		return ""
	}
	history, err := reader.ReadSessionHistory(ctx, engine.SessionHistoryRequest{
		UserID:    sessionUserID,
		SessionID: sid,
		QAOffset:  0,
		Limit:     20,
	})
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("gateway recall session history: %s", sanitizeError(err))
		}
		return ""
	}
	for i := len(history.QAs) - 1; i >= 0; i-- {
		if q := strings.TrimSpace(history.QAs[i].Question); q != "" {
			return truncateRunes(q, 1000)
		}
	}
	return truncateRunes(strings.TrimSpace(history.Label), 1000)
}

func (s *Server) recall(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	var req recallRequest
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &req) {
		return
	}
	if req.Query == "" {
		req.Query = req.Q
	}
	if req.Channel == "" {
		req.Channel = "automatic"
	}
	req.Purpose = strings.TrimSpace(req.Purpose)
	if req.Channel != "automatic" && req.Channel != "explicit" {
		writeError(w, 400, "invalid_recall", "query and valid recall channel required")
		return
	}
	if !validRecallPurpose(req.Purpose) {
		writeError(w, 400, "invalid_recall", "unsupported recall purpose")
		return
	}
	mode, validMode := resolveRecallMode(req.Mode, req.Kind, req.Scope)
	if !validMode {
		writeError(w, 400, "invalid_recall", "unsupported recall mode")
		return
	}
	req.Mode = mode
	query := strings.TrimSpace(req.Query)
	if req.Purpose == "" && query == "" {
		writeError(w, 400, "invalid_recall", "query and valid recall channel required")
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	scopes, include, commonDatasetID, err := s.recallScopes(ctx, req, agent)
	s.registryMu.Unlock()
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
		writeError(w, 403, "dataset_read_denied", "dataset read access required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	ownership, sessionUserID := s.verifyAuthorizedSession(ctx, agent, req.SessionID, scopes, commonDatasetID)
	if query == "" {
		query = s.resolveCompactQuery(ctx, agent, req.SessionID, scopes, commonDatasetID, ownership, sessionUserID)
		if query == "" {
			writeJSON(w, 200, map[string]any{
				"ok":              true,
				"event_id":        "",
				"channel":         req.Channel,
				"scopes":          scopes,
				"common_included": include,
				"hits":            []engine.SearchHit{},
				"context":         "",
				"mode":            mode,
			})
			return
		}
	} else if req.Purpose == "" && strings.TrimSpace(req.SessionID) != "" && ownership != sessionOwnershipConflict {
		s.prompts.record(agent.ID, req.SessionID, pendingPrompt{
			Prompt:     query,
			TurnID:     req.TurnID,
			PromptTime: req.PromptTime,
			DatasetIDs: projectScopeDatasets(scopes, commonDatasetID),
		})
	}
	limit := req.Limit
	if limit < 1 {
		limit = 30
	}
	limit = min(limit, 50)
	searchCtx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
	defer cancel()
	kind := req.Kind
	if kind == "" && strings.EqualFold(req.Scope, "code") {
		kind = "code"
	}
	nodeSets := dedupeTrimmedStrings(req.NodeSets)
	searchSessionID := ""
	includeSessionCtx := false
	if ownership == sessionOwnershipAuthorized {
		searchSessionID = strings.TrimSpace(req.SessionID)
		includeSessionCtx = req.IncludeSessionContext && searchSessionID != ""
	}
	result, err := s.cfg.Engine.Search(searchCtx, engine.SearchRequest{
		Scopes: scopes,
		Query:  query,
		Mode:   mode,
		Limit:  limit,
		Filters: engine.SearchFilters{
			Kind:                  kind,
			Limit:                 limit,
			NodeSets:              nodeSets,
			SessionID:             searchSessionID,
			IncludeSessionContext: includeSessionCtx,
		},
	})
	if err == nil {
		err = searchCtx.Err()
	}
	if err == nil && !result.OK {
		err = engine.ErrUnavailable
	}
	if err != nil {
		writeEngineError(w, err)
		return
	}
	hits := authorizedHits(filterCurrentSessionHits(result.Hits, req.SessionID, req.Purpose), scopes, limit)
	graphContext, degraded := authorizedGraphContext(result.GraphContext, scopes)
	contextText := formatRecallContext(hits, graphContext, recallFormatOptions{
		CommonDatasetID: commonDatasetID,
		SessionID:       req.SessionID,
		Purpose:         req.Purpose,
	})
	eventID := ""
	if len(hits) > 0 {
		eventID = rand.Text()
		event := domain.RecallEventInput{AgentID: agent.ID, EventID: eventID, Channel: req.Channel}
		for _, hit := range hits {
			event.Hits = append(event.Hits, domain.RecallHit{DatasetID: hit.DatasetID, DocumentID: hit.DocumentID})
		}
		if _, err := s.cfg.Store.RecordRecallEvent(ctx, event); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	if err := s.cfg.Store.RecordRecallQueryStat(ctx, req.Channel, len(hits), ""); err != nil {
		writeDomainError(w, err)
		return
	}
	resp := map[string]any{
		"ok":                 true,
		"event_id":           eventID,
		"channel":            req.Channel,
		"scopes":             scopes,
		"common_included":    include,
		"hits":               hits,
		"context":            contextText,
		"mode":               mode,
		"degraded_to_chunks": degraded,
	}
	if graphContext != nil {
		resp["graph_context"] = graphContext
	}
	writeJSON(w, 200, resp)
}

type sessionEndRequest struct {
	hookScope
	Host string `json:"host"`
	Mode string `json:"mode"`
}

func (s *Server) sessionEnd(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	var req sessionEndRequest
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &req) {
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_session_end", "session_id required")
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	project, err := s.hookProject(ctx, req.hookScope, agent.ID)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if domain.IsProtectedProject(project.Name, project.DatasetName) {
		writeError(w, 403, "project_protected", "host capture is disabled for protected projects")
		return
	}
	if project.DatasetID == "" {
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	if errors.Is(err, domain.ErrForbidden) || err == nil && acl.Access != domain.AccessMaintain {
		writeError(w, 403, "dataset_write_denied", "project maintain access required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	manual := req.Mode == "manual" || strings.EqualFold(r.Header.Get("X-Memlane-Capture-Mode"), "manual")
	if !manual && !acl.CanCapture {
		writeJSON(w, 403, map[string]any{"error": "capture_paused", "message": "automatic capture is paused",
			"capture_scope": acl.CaptureScope, "project_auto_capture": acl.ProjectAutoCapture, "auto_capture_version": acl.AutoCaptureVersion})
		return
	}

	s.prompts.clear(agent.ID, req.SessionID)
	needsConsolidation, err := s.inbox.sessionNeedsConsolidation(ctx, project.DatasetID, req.SessionID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.inbox.Notify()
	queued := false
	flushStatus := "skipped"
	if needsConsolidation {
		res, err := s.inbox.ScheduleSessionFlushJob(ctx, project.ID, project.DatasetID, req.SessionID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		flushStatus = string(res)
		queued = res == FlushScheduled
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":           true,
		"queued":       queued,
		"flush_status": flushStatus,
		"project_id":   project.ID,
		"dataset_id":   project.DatasetID,
		"session_id":   req.SessionID,
	})
}

func authorizedHits(hits []engine.SearchHit, scopes []engine.Scope, limit int) []engine.SearchHit {
	datasets := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		datasets[scope.DatasetID] = true
	}
	seen := make(map[[2]string]bool)
	result := make([]engine.SearchHit, 0)
	for _, hit := range hits {
		key := [2]string{hit.DatasetID, hit.DocumentID}
		if !datasets[hit.DatasetID] || hit.DocumentID == "" || seen[key] {
			continue
		}
		result = append(result, hit)
		seen[key] = true
		if len(result) == limit {
			break
		}
	}
	return result
}

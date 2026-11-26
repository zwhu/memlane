package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
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
	RecordedAt time.Time
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

func (p *pendingPromptStore) popPending(agentID, sessionID, turnID, messageID string) (pendingPrompt, bool) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
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

	popAt := func(idx int) pendingPrompt {
		chosen := state.pending[idx]
		state.pending = append(state.pending[:idx], state.pending[idx+1:]...)
		state.updatedAt = now
		p.touchLocked(key)
		return chosen
	}

	if turnID != "" {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == turnID {
				return popAt(i), true
			}
		}
	}
	if messageID != "" && messageID != turnID {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == messageID {
				return popAt(i), true
			}
		}
	}
	if turnID != "" {
		for i := len(state.pending) - 1; i >= 0; i-- {
			if state.pending[i].TurnID == "" {
				return popAt(i), true
			}
		}
		return pendingPrompt{}, false
	}
	return popAt(len(state.pending) - 1), true
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
	return *state.lastPrompt, true
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
				pending, found := s.prompts.popPending(agent.ID, req.SessionID, explicitTurnID, req.MessageID)
				if !found {
					writeJSON(w, http.StatusOK, map[string]any{
						"ok": true, "accepted": false, "status": "skipped_missing_question",
						"project_id": project.ID, "dataset_id": project.DatasetID,
					})
					return
				}
				fields["question"] = pending.Prompt
				existingPromptTime, _ := fields["prompt_time"].(string)
				if strings.TrimSpace(existingPromptTime) == "" && pending.PromptTime != "" {
					fields["prompt_time"] = pending.PromptTime
				}
			}
		}
	}
	if s.filterCapture(w, ctx, req, agent, project, content) {
		return
	}
	req.Content, err = json.Marshal(content)
	if err != nil {
		writeDomainError(w, err)
		return
	}
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
	state, _, err := s.cfg.Store.ReserveCaptureReceipt(ctx, receipt)
	if errors.Is(err, domain.ErrConflict) {
		writeError(w, 409, "capture_conflict", "capture key already has another payload or scope")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	itemID := tupleDigest(agent.ID, project.DatasetID, captureKey)
	response := map[string]any{"ok": true, "accepted": true, "inbox_id": itemID,
		"capture_key": captureKey, "project_id": project.ID, "dataset_id": project.DatasetID}
	// Once the reservation exists, finish publication even if the client disconnects.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	abortPending := func(err error) {
		if state == "reserved" {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			err = errors.Join(err, s.cfg.Store.AbortPendingCaptureReceipt(cleanup, captureKey))
		}
		writeDomainError(w, err)
	}
	if explicitSession != "" {
		key := sessionBindingKey(agent.ID, explicitSession)
		if err := s.cfg.Store.UpsertSessionBinding(persist, key, project.ID, float64(time.Now().UnixMilli())/1000); err != nil {
			abortPending(err)
			return
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
			if err := s.cfg.Store.UpsertSessionRelation(persist, domain.SessionRelation{
				DatasetID:       project.DatasetID,
				SessionID:       req.SessionID,
				ParentSessionID: parentSessionID,
				SubagentRole:    subagentRole,
			}); err != nil {
				abortPending(err)
				return
			}
		}
	}
	status := http.StatusOK
	response["status"], response["deduplicated"] = state, true
	if state == "reserved" {
		item, err := s.cfg.Store.EnqueueInboxItem(persist, domain.CaptureInboxItem{ID: itemID, ProjectID: project.ID,
			DatasetID: project.DatasetID, AgentID: agent.ID, SessionID: req.SessionID, MessageID: req.MessageID,
			CaptureKey: captureKey, Kind: string(req.Kind), PayloadJSON: string(encoded)})
		if err != nil {
			abortPending(err)
			return
		}
		s.inbox.Notify()
		status = http.StatusAccepted
		response["status"], response["deduplicated"] = item.Status, false
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

func (s *Server) resolveCompactQuery(ctx context.Context, agent domain.Agent, sessionID string, scopes []engine.Scope) string {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return ""
	}
	if last, ok := s.prompts.last(agent.ID, sid); ok && strings.TrimSpace(last.Prompt) != "" {
		return strings.TrimSpace(last.Prompt)
	}
	reader := s.sessionHistoryReader()
	if reader == nil {
		return ""
	}
	userID := s.resolveAuthorizedSessionUser(ctx, scopes, sid, agent.PrincipalID)
	if userID == "" {
		return ""
	}
	history, err := reader.ReadSessionHistory(ctx, engine.SessionHistoryRequest{
		UserID:    userID,
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

func (s *Server) resolveAuthorizedSessionUser(ctx context.Context, scopes []engine.Scope, sessionID, principalID string) string {
	for _, scope := range scopes {
		if scope.DatasetID == "" {
			continue
		}
		var userID string
		err := s.cfg.Store.DB().QueryRowContext(ctx,
			`SELECT user_id FROM catalog_sessions WHERE dataset_id=? AND session_id=? ORDER BY sort_ts_desc_key ASC, user_id LIMIT 1`,
			scope.DatasetID, sessionID).Scan(&userID)
		if err == nil && strings.TrimSpace(userID) != "" {
			return strings.TrimSpace(userID)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("gateway recall session lookup: %s", sanitizeError(err))
		}
	}
	return strings.TrimSpace(principalID)
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
	if query == "" {
		query = s.resolveCompactQuery(ctx, agent, req.SessionID, scopes)
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
	} else if req.Purpose == "" && strings.TrimSpace(req.SessionID) != "" {
		s.prompts.record(agent.ID, req.SessionID, pendingPrompt{
			Prompt:     query,
			TurnID:     req.TurnID,
			PromptTime: req.PromptTime,
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
	result, err := s.cfg.Engine.Search(searchCtx, engine.SearchRequest{
		Scopes: scopes,
		Query:  query,
		Mode:   mode,
		Limit:  limit,
		Filters: engine.SearchFilters{
			Kind:                  kind,
			Limit:                 limit,
			NodeSets:              nodeSets,
			SessionID:             req.SessionID,
			IncludeSessionContext: req.IncludeSessionContext && strings.TrimSpace(req.SessionID) != "",
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
	project, err := s.hookProject(ctx, req.hookScope, agent.ID)
	if errors.Is(err, domain.ErrNotFound) {
		s.registryMu.Unlock()
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	if err != nil {
		s.registryMu.Unlock()
		writeDomainError(w, err)
		return
	}
	if domain.IsProtectedProject(project.Name, project.DatasetName) {
		s.registryMu.Unlock()
		writeError(w, 403, "project_protected", "host capture is disabled for protected projects")
		return
	}
	if project.DatasetID == "" {
		s.registryMu.Unlock()
		writeError(w, 403, "project_access_denied", "managed project required")
		return
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	s.registryMu.Unlock()
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
	if needsConsolidation {
		queued = s.inbox.ScheduleSessionFlush(project.DatasetID, req.SessionID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"queued":     queued,
		"project_id": project.ID,
		"dataset_id": project.DatasetID,
		"session_id": req.SessionID,
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

package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

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
	Kind       engine.IngestKind `json:"kind"`
	Mode       string            `json:"mode"`
	MessageID  string            `json:"message_id"`
	Host       string            `json:"host"`
	CaptureKey string            `json:"capture_key"`
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
	status := http.StatusOK
	response["status"], response["deduplicated"] = state, true
	if state == "reserved" {
		item, err := s.cfg.Store.EnqueueInboxItem(persist, domain.CaptureInboxItem{ID: itemID, ProjectID: project.ID,
			DatasetID: project.DatasetID, AgentID: agent.ID, SessionID: req.SessionID, MessageID: req.MessageID,
			CaptureKey: captureKey, Kind: string(req.Kind), PayloadJSON: string(encoded)})
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			writeDomainError(w, errors.Join(err, s.cfg.Store.AbortPendingCaptureReceipt(cleanup, captureKey)))
			return
		}
		s.inbox.Notify()
		status = http.StatusAccepted
		response["status"], response["deduplicated"] = item.Status, false
	}
	if explicitSession != "" {
		key := sessionBindingKey(agent.ID, explicitSession)
		if err := s.cfg.Store.UpsertSessionBinding(persist, key, project.ID, float64(time.Now().UnixMilli())/1000); err != nil {
			writeDomainError(w, err)
			return
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
	Query      string   `json:"query"`
	Q          string   `json:"q"`
	DatasetIDs []string `json:"dataset_ids"`
	Kind       string   `json:"kind"`
	Scope      string   `json:"scope"`
	Channel    string   `json:"channel"`
	Limit      int      `json:"limit"`
}

func (s *Server) recallScopes(ctx context.Context, req recallRequest, agent domain.Agent) ([]engine.Scope, bool, error) {
	projects, err := s.cfg.Store.ListProjects(ctx)
	if err != nil {
		return nil, false, err
	}
	explicit := req.ProjectID != "" || req.Workspace != "" || req.SessionID != ""
	if explicit {
		p, err := s.hookProject(ctx, req.hookScope, agent.ID)
		if err != nil {
			return nil, false, err
		}
		projects = []domain.Project{p}
	}
	codeOnly := strings.EqualFold(req.Kind, "code") || strings.EqualFold(req.Scope, "code")
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
		return nil, false, err
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
			return nil, false, domain.ErrForbidden
		}
		target := targets[id][0]
		acl, err := s.cfg.Store.EvaluateProjectACL(ctx, target.projectID, agent.ID)
		if err != nil {
			return nil, false, err
		}
		if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
			if explicit || len(req.DatasetIDs) > 0 {
				return nil, false, domain.ErrForbidden
			}
			continue
		}
		seen[id] = true
		allCode = allCode && target.code
		scopes = append(scopes, engine.Scope{DatasetID: id})
	}
	if explicit && len(scopes) == 0 {
		return nil, false, domain.ErrForbidden
	}
	include := false
	commonEligible := !codeOnly && !(len(scopes) > 0 && allCode)
	if common.DatasetID != "" && (commonRequested || commonEligible) {
		acl, err := s.cfg.Store.EvaluateCommonACL(ctx, agent.ID)
		if err != nil {
			return nil, false, err
		}
		if commonRequested && acl.Access != domain.AccessRead {
			return nil, false, domain.ErrForbidden
		}
		if commonEligible && acl.Access == domain.AccessRead {
			scopes = append(scopes, engine.Scope{DatasetID: common.DatasetID})
			include = true
		}
	}
	if len(scopes) == 0 {
		return nil, false, domain.ErrForbidden
	}
	return scopes, include, nil
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
	if strings.TrimSpace(req.Query) == "" || req.Channel != "automatic" && req.Channel != "explicit" {
		writeError(w, 400, "invalid_recall", "query and valid recall channel required")
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	scopes, include, err := s.recallScopes(ctx, req, agent)
	s.registryMu.Unlock()
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
		writeError(w, 403, "dataset_read_denied", "dataset read access required")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
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
	result, err := s.cfg.Engine.Search(searchCtx, engine.SearchRequest{Scopes: scopes, Query: req.Query, Limit: limit,
		Filters: engine.SearchFilters{Kind: kind, Limit: limit}})
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
	hits := authorizedHits(result.Hits, scopes, limit)
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
	writeJSON(w, 200, map[string]any{"ok": true, "event_id": eventID, "channel": req.Channel,
		"scopes": scopes, "common_included": include, "hits": hits})
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

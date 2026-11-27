package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/mcp"
)

const (
	mcpQueuedMessage          = "已接收，等待保存"
	defaultMCPFastPathTimeout = 3 * time.Second
	defaultMCPFinalizeTimeout = 5 * time.Second
	mcpFastPathDeadlineMargin = 500 * time.Millisecond
)

// mcpInboxPayload aligns with Wave 2A's inboxPayload contract: user_id stays
// for v4 receipt metadata compatibility while engine_user_id is the trusted
// server-side identity forwarded to Engine.Scope.UserID on inbox replay.
type mcpInboxPayload struct {
	Content        json.RawMessage    `json:"content"`
	SourceMeta     engine.SourceMeta  `json:"source_meta"`
	UserID         string             `json:"user_id,omitempty"`
	EngineUserID   string             `json:"engine_user_id,omitempty"`
	ExtractMode    engine.ExtractMode `json:"extract_mode"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
}

type mcpRememberService struct {
	server          *Server
	fastPathTimeout time.Duration
	finalizeTimeout time.Duration
}

func (s *Server) MCPRememberService() mcp.RememberService {
	return &mcpRememberService{
		server:          s,
		fastPathTimeout: defaultMCPFastPathTimeout,
		finalizeTimeout: defaultMCPFinalizeTimeout,
	}
}

func (m *mcpRememberService) fastPathContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := m.fastPathTimeout
	if budget <= 0 {
		budget = defaultMCPFastPathTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline) - mcpFastPathDeadlineMargin
		if remaining < budget {
			if remaining < 50*time.Millisecond {
				remaining = 50 * time.Millisecond
			}
			budget = remaining
		}
	}
	return context.WithTimeout(ctx, budget)
}

func (m *mcpRememberService) newFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := m.finalizeTimeout
	if timeout <= 0 {
		timeout = defaultMCPFinalizeTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func rememberDocumentID(kind engine.IngestKind, docID, entryID string) string {
	if strings.TrimSpace(docID) != "" {
		return docID
	}
	if kind == engine.IngestKindDocument {
		return entryID
	}
	return ""
}

func isMCPClientRejection(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, domain.ErrForbidden) ||
		errors.Is(err, engine.ErrInvalidArgument) || errors.Is(err, engine.ErrNotUTF8Text) ||
		errors.Is(err, engine.ErrContentTooLarge) || errors.Is(err, engine.ErrForbidden) ||
		errors.Is(err, engine.ErrUnauthorized) || errors.Is(err, engine.ErrPathDenied) {
		return true
	}
	var engErr *engine.EngineError
	if errors.As(err, &engErr) && engErr != nil {
		code := engErr.StatusCode
		return code >= http.StatusBadRequest && code < http.StatusInternalServerError &&
			code != http.StatusRequestTimeout && code != http.StatusConflict && code != http.StatusTooManyRequests
	}
	return false
}

func (m *mcpRememberService) fastPathModelDigest(ctx context.Context) (string, bool) {
	s := m.server
	if !s.generationMu.TryLock() {
		return "", false
	}
	s.generationMu.Unlock()
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err != nil || hasPending {
		return "", false
	}
	modelDigest, err := s.expectedActiveModelDigest(ctx)
	if err != nil || strings.TrimSpace(modelDigest) == "" {
		return "", false
	}
	return modelDigest, true
}

func (m *mcpRememberService) Remember(ctx context.Context, req mcp.RememberRequest) (mcp.RememberResult, error) {
	s := m.server
	if req.Project.ID == "" || req.Project.DatasetID == "" || req.IdempotencyKey == "" || req.ReceiptUser == "" || req.Kind == "" {
		return mcp.RememberResult{}, domain.ErrInvalidArgument
	}
	sessionID := req.SessionID
	if sessionID == "" && req.Kind != engine.IngestKindDocument {
		sessionID = req.IdempotencyKey
	}
	receiptSessionID := req.SessionID
	if receiptSessionID == "" {
		receiptSessionID = req.IdempotencyKey
	}
	receiptMessageID := req.MessageID
	if receiptMessageID == "" {
		receiptMessageID = req.IdempotencyKey
	}
	contentRaw, err := json.Marshal(req.Content)
	if err != nil {
		return mcp.RememberResult{}, err
	}
	sourceMeta := engine.SourceMeta{
		SessionID:        sessionID,
		ContextDate:      req.ContextDate,
		ExternalMetadata: req.Metadata,
	}
	payload := mcpInboxPayload{
		Content:        contentRaw,
		SourceMeta:     sourceMeta,
		UserID:         req.UserID,
		EngineUserID:   req.UserID,
		ExtractMode:    engine.ExtractModeDefault,
		IdempotencyKey: req.IdempotencyKey,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return mcp.RememberResult{}, err
	}
	payloadSHA := digest(payloadJSON)
	legacyIngestReq := engine.IngestRequest{
		Scope:          engine.Scope{DatasetID: req.Project.DatasetID, UserID: req.UserID},
		Kind:           req.Kind,
		IdempotencyKey: req.IdempotencyKey,
		Content:        req.Content,
		SourceMeta:     sourceMeta,
	}
	legacyCanonicalRaw, err := json.Marshal(legacyIngestReq)
	if err != nil {
		return mcp.RememberResult{}, err
	}
	legacyCanonicalSHA := digest(legacyCanonicalRaw)
	var legacyRawSHA string
	if req.RawContent != nil {
		legacyRawReq := legacyIngestReq
		legacyRawReq.Content = req.RawContent
		if legacyRawBytes, err := json.Marshal(legacyRawReq); err == nil {
			legacyRawSHA = digest(legacyRawBytes)
		}
	}
	taskID := tupleDigest("mcp", req.Agent.ID, req.Project.DatasetID, req.IdempotencyKey)

	modelDigest, canFastPath := m.fastPathModelDigest(ctx)

	var dedupResult *mcp.RememberResult
	var notifyInbox bool
	err = s.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		now := domain.NowUTCMicro()
		var existing domain.CaptureReceipt
		var entryID sql.NullString
		receiptErr := tx.QueryRowContext(ctx,
			`SELECT capture_key,user_id,dataset_id,session_id,message_id,payload_sha256,entry_type,entry_id,status,updated_at
			 FROM capture_receipts WHERE capture_key=?`,
			req.IdempotencyKey,
		).Scan(&existing.CaptureKey, &existing.UserID, &existing.DatasetID, &existing.SessionID,
			&existing.MessageID, &existing.PayloadSHA256, &existing.EntryType, &entryID, &existing.Status, &existing.UpdatedAt)
		existing.EntryID = entryID.String

		if receiptErr == nil {
			shaMatches := existing.PayloadSHA256 == payloadSHA ||
				existing.PayloadSHA256 == legacyCanonicalSHA ||
				(legacyRawSHA != "" && existing.PayloadSHA256 == legacyRawSHA)
			if existing.UserID != req.ReceiptUser || existing.DatasetID != req.Project.DatasetID ||
				existing.SessionID != receiptSessionID || existing.MessageID != receiptMessageID ||
				!shaMatches || existing.EntryType != string(req.Kind) {
				return domain.ErrConflict
			}

			// First check this agent's own inbox row by deterministic taskID.
			var inboxID, inboxAgentID, inboxStatus string
			inboxFound := false
			ownErr := tx.QueryRowContext(ctx,
				`SELECT id,agent_id,status FROM capture_inbox WHERE id=? LIMIT 1`,
				taskID,
			).Scan(&inboxID, &inboxAgentID, &inboxStatus)
			if ownErr == nil {
				if inboxAgentID != req.Agent.ID {
					return domain.ErrConflict
				}
				inboxFound = true
			} else if !errors.Is(ownErr, sql.ErrNoRows) {
				return ownErr
			}

			// If this agent has no row yet, inspect other rows for capture_key.
			// Any real Wave 2B task owned by another agent conflicts; synthetic
			// read-only mirror rows (status='succeeded', attempts=0) created for
			// legacy saved receipts do not block other agents sharing principal_id.
			if !inboxFound {
				rows, qErr := tx.QueryContext(ctx,
					`SELECT id,agent_id,status,attempts FROM capture_inbox WHERE capture_key=? ORDER BY created_at DESC`,
					req.IdempotencyKey,
				)
				if qErr != nil {
					return qErr
				}
				for rows.Next() {
					var rowID, rowAgentID, rowStatus string
					var rowAttempts int
					if scanErr := rows.Scan(&rowID, &rowAgentID, &rowStatus, &rowAttempts); scanErr != nil {
						_ = rows.Close()
						return scanErr
					}
					if rowAgentID == req.Agent.ID {
						if !inboxFound {
							inboxID, inboxStatus, inboxFound = rowID, rowStatus, true
						}
						continue
					}
					isLegacyMirror := existing.Status == "saved" && rowStatus == "succeeded" && rowAttempts == 0
					if !isLegacyMirror {
						_ = rows.Close()
						return domain.ErrConflict
					}
				}
				if iterErr := rows.Err(); iterErr != nil {
					_ = rows.Close()
					return iterErr
				}
				_ = rows.Close()
			}

			if inboxID == "" {
				inboxID = taskID
			}
			if existing.Status == "saved" && (!inboxFound || inboxStatus == "succeeded") {
				if !inboxFound {
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO capture_inbox (id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at)
						 VALUES(?,?,?,?,?,?,?,?,?,'succeeded','','',0,?,?)`,
						taskID, req.Project.ID, req.Project.DatasetID, req.Agent.ID, receiptSessionID, receiptMessageID,
						req.IdempotencyKey, string(req.Kind), string(payloadJSON), now, now,
					); err != nil {
						return err
					}
				}
				res := mcp.RememberResult{
					OK:             true,
					Status:         "saved",
					Saved:          true,
					TaskID:         inboxID,
					EntryID:        existing.EntryID,
					DocumentID:     rememberDocumentID(req.Kind, "", existing.EntryID),
					IdempotencyKey: req.IdempotencyKey,
					DatasetID:      req.Project.DatasetID,
					Deduplicated:   true,
				}
				dedupResult = &res
				return nil
			}
			if !inboxFound {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO capture_inbox (id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at)
					 VALUES(?,?,?,?,?,?,?,?,?,'pending','','',0,?,?)`,
					taskID, req.Project.ID, req.Project.DatasetID, req.Agent.ID, receiptSessionID, receiptMessageID,
					req.IdempotencyKey, string(req.Kind), string(payloadJSON), now, now,
				); err != nil {
					return err
				}
				notifyInbox = true
			} else if inboxStatus == "failed" {
				if _, err := tx.ExecContext(ctx,
					`UPDATE capture_inbox SET status='pending',last_error='',last_error_at='',updated_at=? WHERE id=? AND agent_id=? AND status='failed'`,
					now, inboxID, req.Agent.ID,
				); err != nil {
					return err
				}
				notifyInbox = true
			}
			res := mcp.RememberResult{
				OK:             true,
				Status:         "queued",
				Saved:          false,
				TaskID:         inboxID,
				EntryID:        existing.EntryID,
				DocumentID:     rememberDocumentID(req.Kind, "", existing.EntryID),
				IdempotencyKey: req.IdempotencyKey,
				DatasetID:      req.Project.DatasetID,
				Deduplicated:   true,
				Message:        mcpQueuedMessage,
			}
			dedupResult = &res
			return nil
		}
		if !errors.Is(receiptErr, sql.ErrNoRows) {
			return receiptErr
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO capture_receipts (capture_key,user_id,dataset_id,session_id,message_id,payload_sha256,entry_type,entry_id,baseline_count,status,updated_at)
			 VALUES(?,?,?,?,?,?,?,'',NULL,'pending',?)`,
			req.IdempotencyKey, req.ReceiptUser, req.Project.DatasetID, receiptSessionID, receiptMessageID,
			payloadSHA, string(req.Kind), now,
		); err != nil {
			return err
		}

		initialStatus := "pending"
		initialAttempts := 0
		if canFastPath {
			initialStatus = "processing"
			initialAttempts = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO capture_inbox (id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,'',?,?,?)`,
			taskID, req.Project.ID, req.Project.DatasetID, req.Agent.ID, receiptSessionID, receiptMessageID,
			req.IdempotencyKey, string(req.Kind), string(payloadJSON), initialStatus, "", initialAttempts, now, now,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return mcp.RememberResult{}, err
	}
	if dedupResult != nil {
		if notifyInbox {
			s.inbox.Notify()
		}
		return *dedupResult, nil
	}
	if !canFastPath {
		s.inbox.Notify()
		return mcp.RememberResult{
			OK:             true,
			Status:         "queued",
			Saved:          false,
			TaskID:         taskID,
			IdempotencyKey: req.IdempotencyKey,
			DatasetID:      req.Project.DatasetID,
			Message:        mcpQueuedMessage,
		}, nil
	}

	requeue := func(entryID, docID string) (mcp.RememberResult, error) {
		finish, cancel := m.newFinalizeContext(ctx)
		defer cancel()
		if err := s.cfg.Store.WithTx(finish, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(finish,
				`UPDATE capture_inbox SET status='pending', updated_at=? WHERE id=? AND status='processing'`,
				domain.NowUTCMicro(), taskID)
			return err
		}); err != nil {
			return mcp.RememberResult{}, err
		}
		s.inbox.Notify()
		return mcp.RememberResult{
			OK:             true,
			Status:         "queued",
			Saved:          false,
			TaskID:         taskID,
			EntryID:        entryID,
			DocumentID:     rememberDocumentID(req.Kind, docID, entryID),
			IdempotencyKey: req.IdempotencyKey,
			DatasetID:      req.Project.DatasetID,
			Message:        mcpQueuedMessage,
		}, nil
	}

	fastCtx, cancelFast := m.fastPathContext(ctx)
	defer cancelFast()

	ingestRes, err := s.cfg.Engine.Ingest(fastCtx, legacyIngestReq)
	if err != nil {
		if isMCPClientRejection(err) {
			abortCtx, cancel := m.newFinalizeContext(ctx)
			defer cancel()
			cleanupErr := s.cfg.Store.WithTx(abortCtx, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(abortCtx, `DELETE FROM capture_inbox WHERE id=? AND status='processing'`, taskID); err != nil {
					return err
				}
				_, err := tx.ExecContext(abortCtx, `DELETE FROM capture_receipts WHERE capture_key=? AND status='pending'`, req.IdempotencyKey)
				return err
			})
			return mcp.RememberResult{}, errors.Join(err, cleanupErr)
		}
		return requeue("", "")
	}
	entryID := ingestRes.EntryID
	if entryID == "" {
		entryID = ingestRes.DocumentID
	}
	resultDocID := rememberDocumentID(req.Kind, ingestRes.DocumentID, entryID)
	if !ingestRes.OK || strings.TrimSpace(entryID) == "" {
		return requeue("", "")
	}

	saveCtx, cancelSave := m.newFinalizeContext(ctx)
	saveErr := s.cfg.Store.MarkCaptureReceiptSaved(saveCtx, req.IdempotencyKey, entryID)
	cancelSave()
	if saveErr != nil {
		return requeue(entryID, resultDocID)
	}

	if req.Kind != engine.IngestKindCaptureTrace {
		extractDocID := ingestRes.DocumentID
		if extractDocID == "" {
			if req.Kind == engine.IngestKindCaptureQA && sessionID != "" {
				extractDocID = sessionID
			} else {
				extractDocID = entryID
			}
		}
		extracted, err := s.cfg.Engine.Extract(fastCtx, engine.ExtractRequest{
			Scope:               engine.Scope{DatasetID: req.Project.DatasetID, UserID: req.UserID},
			DocIDs:              []string{extractDocID},
			ExpectedModelDigest: modelDigest,
			OutputLanguage:      s.cfg.OutputLanguage,
			Mode:                engine.ExtractModeDefault,
		})
		if err != nil || !extracted.OK {
			return requeue(entryID, resultDocID)
		}
	}

	if s.cfg.Projector != nil {
		syncErrCh := make(chan error, 1)
		go func() {
			_, syncErr := s.cfg.Projector.SyncCatalog(fastCtx, s.cfg.Store)
			syncErrCh <- syncErr
		}()
		select {
		case err := <-syncErrCh:
			if err != nil {
				return requeue(entryID, resultDocID)
			}
		case <-fastCtx.Done():
			return requeue(entryID, resultDocID)
		}
	}

	doneCtx, cancelDone := m.newFinalizeContext(ctx)
	doneErr := s.cfg.Store.MarkInboxSucceeded(doneCtx, taskID)
	cancelDone()
	if doneErr != nil {
		return mcp.RememberResult{}, doneErr
	}
	return mcp.RememberResult{
		OK:             true,
		Status:         "saved",
		Saved:          true,
		TaskID:         taskID,
		EntryID:        entryID,
		DocumentID:     resultDocID,
		IdempotencyKey: req.IdempotencyKey,
		DatasetID:      req.Project.DatasetID,
		Deduplicated:   ingestRes.Deduplicated,
	}, nil
}

func (m *mcpRememberService) LookupTask(ctx context.Context, agent domain.Agent, taskID string) (mcp.TaskStatusResult, error) {
	s := m.server
	item, err := s.cfg.Store.GetInboxItem(ctx, taskID)
	if err != nil {
		return mcp.TaskStatusResult{}, err
	}
	if item.AgentID != agent.ID {
		return mcp.TaskStatusResult{}, domain.ErrForbidden
	}
	project, err := s.cfg.Store.GetProject(ctx, item.ProjectID)
	if err != nil {
		return mcp.TaskStatusResult{}, domain.ErrForbidden
	}
	if domain.IsProtectedProject(project.Name, project.DatasetName) ||
		domain.IsProtectedProject(project.ID, project.DatasetID) {
		return mcp.TaskStatusResult{}, domain.ErrForbidden
	}
	acl, err := s.cfg.Store.EvaluateProjectACL(ctx, project.ID, agent.ID)
	if err != nil {
		return mcp.TaskStatusResult{}, err
	}
	if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
		return mcp.TaskStatusResult{}, domain.ErrForbidden
	}
	var entryID string
	if item.CaptureKey != "" {
		if receipt, err := s.cfg.Store.GetCaptureReceipt(ctx, item.CaptureKey); err == nil {
			entryID = receipt.EntryID
		}
	}
	lastError := ""
	if item.LastError != "" {
		lastError = sanitizeError(errors.New(item.LastError))
	}
	return mcp.TaskStatusResult{
		OK:             true,
		TaskID:         item.ID,
		ProjectID:      item.ProjectID,
		DatasetID:      item.DatasetID,
		Status:         item.Status,
		Saved:          item.Status == "succeeded",
		EntryID:        entryID,
		DocumentID:     rememberDocumentID(engine.IngestKind(item.Kind), "", entryID),
		IdempotencyKey: item.CaptureKey,
		LastError:      lastError,
		UpdatedAt:      item.UpdatedAt,
	}, nil
}

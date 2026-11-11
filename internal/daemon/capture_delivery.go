package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) captureClientRoot() string {
	if s.config.ClientRoot != "" {
		return s.config.ClientRoot
	}
	return filepath.Dir(s.config.Root)
}

func (s *Server) confirmCaptureRoute(ctx context.Context, q *records.CaptureRouteConfirmation, confirmed bool) (records.CaptureRoute, error) {
	var out records.CaptureRoute
	if q == nil || !confirmed {
		return out, &records.AccessError{Code: "confirmation_required", Message: "explicit capture repository routing approval required"}
	}
	if id, e := uuid.Parse(q.OperationID); e != nil || id.String() != q.OperationID {
		return out, records.ErrConflict
	}
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	err := agents.WithCodexCaptureAuthorization(ctx, s.captureClientRoot(), q.ConnectionID, func(i agents.Installation) error {
		source, e := agentbridge.ReadDeliveryAssignmentSource(s.config.Root, q.EntryID, q.ReviewDigest)
		if e != nil {
			return e
		}
		origin, ok := records.ReadCaptureOrigin(source.Review.Arguments)
		if !ok || origin.Consent != i.CaptureConsent.ID || source.Review.Item.ConnectionID != q.ConnectionID {
			return &records.AccessError{Code: "capture_source_invalid", Message: "source must belong to this current collection consent"}
		}
		if e = agents.ValidateCodexCaptureSource(s.captureClientRoot(), i, source.Review.Arguments); e != nil {
			return e
		}
		candidate := records.CaptureRoute{OperationID: q.OperationID, LibraryID: q.LibraryID, ConnectionID: q.ConnectionID, ConsentID: origin.Consent, Repository: origin.Repository, Project: q.Project}
		if e = s.store.ValidateCaptureRoute(ctx, source, candidate); e != nil {
			return e
		}
		out, e = agents.SaveCodexCaptureRoute(s.captureClientRoot(), i, candidate)
		return e
	})
	return out, err
}

func (s *Server) deliverCapturedRaw(ctx context.Context, q *records.CaptureDeliveryQuery) (records.CaptureDeliveryReceipt, error) {
	var out records.CaptureDeliveryReceipt
	if q == nil {
		return out, &records.AccessError{Code: "invalid_request", Message: "retained capture identity required"}
	}
	review, err := agentbridge.ReviewDelivery(s.config.Root, q.EntryID)
	if err != nil {
		return out, err
	}
	if review.Item.Status.ReviewDigest != q.ReviewDigest {
		return out, records.ErrConflict
	}
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	err = agents.WithCodexCaptureAuthorization(ctx, s.captureClientRoot(), review.Item.ConnectionID, func(i agents.Installation) error {
		var e error
		out, e = agentbridge.ProcessCapturedRaw(s.config.Root, *q, func(source records.DeliveryAssignmentSource) (records.CaptureDeliveryReceipt, error) {
			if prior, found, e := s.store.CapturedReceipt(ctx, source); e != nil || found {
				return prior, e
			}
			if e := agents.ValidateCodexCaptureSource(s.captureClientRoot(), i, source.Review.Arguments); e != nil {
				return records.CaptureDeliveryReceipt{}, e
			}
			origin, ok := records.ReadCaptureOrigin(source.Review.Arguments)
			if !ok {
				return records.CaptureDeliveryReceipt{}, &records.AccessError{Code: "capture_source_invalid", Message: "retained conversation source required"}
			}
			library, e := s.store.CogneeLibraryIdentity(ctx)
			if e != nil {
				return records.CaptureDeliveryReceipt{}, e
			}
			route, e := agents.CaptureRouteFor(i, origin, library)
			if e != nil {
				return records.CaptureDeliveryReceipt{}, e
			}
			return s.store.SaveCapturedRaw(ctx, source, route)
		})
		if e == nil && !out.AckPending {
			_ = agents.MarkCodexCaptureDelivered(s.captureClientRoot(), i.Connection.ID, out)
		}
		return e
	})
	if err == nil && out.Persisted {
		select {
		case s.work <- struct{}{}:
		default:
		}
	}
	return out, err
}

// Only the daemon retries its authorized capture queue. Existing ordinary
// memory_save outbox entries keep their explicit manual replay contract.
func (s *Server) runCaptureDeliveries(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	retries := map[string]*agents.CodexCaptureRetry{}
	defer func() {
		for _, retry := range retries {
			retry.Close()
		}
	}()
	for {
		connections, listErr := s.store.Connections(ctx)
		if listErr == nil {
			active := map[string]bool{}
			for _, connection := range connections {
				if !connection.Enabled || connection.Agent != "codex" {
					continue
				}
				active[connection.ID] = true
				if retries[connection.ID] == nil {
					retries[connection.ID] = &agents.CodexCaptureRetry{}
				}
				run, cancel := context.WithTimeout(ctx, 2*time.Second)
				_ = retries[connection.ID].Step(run, s.captureClientRoot(), connection.ID)
				cancel()
			}
			for id, retry := range retries {
				if !active[id] {
					retry.Close()
					delete(retries, id)
				}
			}
		}
		if _, err := os.Stat(filepath.Join(s.config.Root, "agent-outbox", "ledger.json")); err == nil {
			page, e := agentbridge.ListDeliveries(s.config.Root)
			if e == nil {
				for _, item := range page.Items {
					if ctx.Err() != nil {
						return
					}
					if item.HasGrant || item.Assignment != nil || item.Status.PersistedConfirmed || item.Origin != "raw" {
						continue
					}
					run, cancel := context.WithTimeout(ctx, 2*time.Second)
					_, _ = s.deliverCapturedRaw(run, &records.CaptureDeliveryQuery{EntryID: item.Status.ID, ReviewDigest: item.Status.ReviewDigest})
					cancel()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

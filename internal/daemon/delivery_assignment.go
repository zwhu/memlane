package daemon

import (
	"context"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) deliveryAssignmentPreview(ctx context.Context, q *records.DeliveryAssignmentPreviewQuery) (records.DeliveryAssignmentPreview, error) {
	if q == nil {
		return records.DeliveryAssignmentPreview{}, &records.AccessError{Code: "invalid_request", Message: "retained source selection required"}
	}
	source, err := agentbridge.ReadDeliveryAssignmentSource(s.config.Root, q.EntryID, q.ReviewDigest)
	if err != nil {
		return records.DeliveryAssignmentPreview{}, err
	}
	out, err := s.store.DeliveryAssignmentPreview(ctx, source, q.Project)
	if err == nil && out.Assignment != nil {
		out.Assignment.SpoolAckPending = source.Review.Item.Assignment == nil || source.Review.Item.Assignment.State != "saved"
	}
	return out, err
}
func (s *Server) confirmDeliveryAssignment(ctx context.Context, q *records.DeliveryAssignmentIntent, confirmed bool) (records.DeliveryAssignmentReceipt, error) {
	if q == nil || !confirmed {
		return records.DeliveryAssignmentReceipt{}, &records.AccessError{Code: "confirmation_required", Message: "explicit user project assignment required"}
	}
	// Recover the actual committed SQL result before requiring an extant source
	// or the old workspace. Losing a local ack is not permission to save twice.
	out, found, err := s.store.LookupDeliveryAssignment(ctx, *q)
	if err != nil {
		return out, err
	}
	if !found {
		source, e := agentbridge.ClaimDeliveryAssignment(s.config.Root, *q)
		if e != nil {
			return out, e
		}
		source, e = agentbridge.ReadDeliveryAssignmentClaim(s.config.Root, *q)
		if e != nil {
			return out, e
		}
		out, err = s.store.ConfirmDeliveryAssignment(ctx, source, *q)
		if err != nil {
			return out, err
		}
		select {
		case s.work <- struct{}{}:
		default:
		}
	}
	if err = agentbridge.AckDeliveryAssignment(s.config.Root, *q, out); err != nil {
		out.SpoolAckPending = true
	}
	return out, nil
}

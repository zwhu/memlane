package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) DeliveryAssignmentPreview(ctx context.Context, q records.DeliveryAssignmentPreviewQuery) (records.DeliveryAssignmentPreview, error) {
	var out records.DeliveryAssignmentPreview
	err := Call(ctx, l.Root, Request{Op: "delivery_assignment_preview", DeliveryAssignmentPreviewQuery: &q}, &out)
	return out, err
}
func (l Library) ConfirmDeliveryAssignment(ctx context.Context, q records.DeliveryAssignmentIntent) (records.DeliveryAssignmentReceipt, error) {
	var out records.DeliveryAssignmentReceipt
	err := Call(ctx, l.Root, Request{Op: "delivery_assignment_confirm", DeliveryAssignmentIntent: &q, UserConfirmed: true}, &out)
	return out, err
}

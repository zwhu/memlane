package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) ConfirmCaptureRoute(ctx context.Context, q records.CaptureRouteConfirmation) (records.CaptureRoute, error) {
	var out records.CaptureRoute
	err := Call(ctx, l.Root, Request{Op: "capture_route_confirm", CaptureRouteConfirmation: &q, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) DeliverCapturedRaw(ctx context.Context, q records.CaptureDeliveryQuery) (records.CaptureDeliveryReceipt, error) {
	var out records.CaptureDeliveryReceipt
	err := Call(ctx, l.Root, Request{Op: "capture_deliver", CaptureDeliveryQuery: &q}, &out)
	return out, err
}

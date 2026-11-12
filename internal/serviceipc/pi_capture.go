package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) PiCaptureSettings(ctx context.Context, q records.PiCaptureSettingsQuery) ([]records.PiCaptureSetting, error) {
	var out []records.PiCaptureSetting
	err := Call(ctx, l.Root, Request{Op: "pi_capture_settings", PiCaptureSettingsQuery: &q}, &out)
	return out, err
}

func (l Library) ReviewPiCaptureConsent(ctx context.Context, q records.PiCaptureConsentReview) (records.PiCaptureConsent, error) {
	var out records.PiCaptureConsent
	err := Call(ctx, l.Root, Request{Op: "pi_capture_consent_review", PiCaptureConsentReview: &q, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) DisablePiCaptureConsent(ctx context.Context, q records.PiCaptureConsentDisable) error {
	return Call(ctx, l.Root, Request{Op: "pi_capture_consent_disable", PiCaptureConsentDisable: &q, UserConfirmed: true}, nil)
}

func (l Library) AuthorizePiCapture(ctx context.Context, q records.AgentRequest) (records.PiCaptureConsent, error) {
	var out records.PiCaptureConsent
	err := Call(ctx, l.Root, Request{Op: "pi_capture_authorize", AgentRequest: q}, &out)
	return out, err
}

func (l Library) SubmitPiCaptureBatch(ctx context.Context, q records.PiCaptureBatchRequest) (records.PiCaptureBatchReceipt, error) {
	var out records.PiCaptureBatchReceipt
	err := Call(ctx, l.Root, Request{Op: "pi_capture_submit", PiCaptureBatch: &q}, &out)
	return out, err
}

func (l Library) InspectPiCaptureBatch(ctx context.Context, q records.PiCaptureBatchRequest) (records.PiCaptureBatchReceipt, error) {
	var out records.PiCaptureBatchReceipt
	err := Call(ctx, l.Root, Request{Op: "pi_capture_inspect", PiCaptureBatch: &q}, &out)
	return out, err
}

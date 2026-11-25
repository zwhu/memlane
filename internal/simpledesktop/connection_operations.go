package simpledesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

const ambiguousConnectionOperation = "connection_operation_ambiguous_requires_review"

func connectionOperationPath(operation string) string {
	return "/v1/connections/operations/" + url.PathEscape(operation)
}

func normalizeConnectionAction(action string) string {
	action = strings.TrimSpace(action)
	if action == "repair_trae_hooks" {
		return "repair-trae-hooks"
	}
	return action
}

func connectionReceiptMatches(o generationUIOperation, receipt controlplane.ConnectionOperationReceipt) bool {
	wantAction := normalizeConnectionAction(controlplane.UIOperation(o).ConnectionAction())
	action := normalizeConnectionAction(receipt.Action)
	if receipt.Recorded {
		return receipt.OperationID != "" && receipt.OperationID == o.Operation &&
			receipt.InstanceID != "" && receipt.InstanceID == o.Profile &&
			(wantAction == "" || action == wantAction)
	}
	return (receipt.OperationID == "" || receipt.OperationID == o.Operation) &&
		(receipt.InstanceID == "" || receipt.InstanceID == o.Profile) &&
		(receipt.Action == "" || action == wantAction)
}

type reconcilePreviewTransport struct {
	http.RoundTripper
	body bytes.Buffer
}

func (t *reconcilePreviewTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err == nil && resp.Body != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Capture only the bytes read by the control-plane client's bounded decoder.
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(resp.Body, &t.body), resp.Body}
	}
	return resp, err
}

func previewPendingConnection(ctx context.Context, cp *controlplane.Client, o generationUIOperation) (controlplane.ConnectionOperationReceipt, bool, error) {
	client := controlplane.NewLoopbackHTTPClient(cp.HTTP, cp.Config.Timeout)
	capture := &reconcilePreviewTransport{RoundTripper: client.Transport}
	client.Transport = capture
	previewClient := *cp
	previewClient.HTTP = client
	preview, err := previewClient.PreviewReconcilePendingOperationFor(ctx, o.Operation, o.Profile, controlplane.UIOperation(o).ConnectionAction())
	if err != nil {
		return preview, false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(capture.body.Bytes(), &fields); err != nil {
		return preview, false, err
	}
	_, hasRevision := fields["revision"]
	return preview, hasRevision, nil
}

func (s *integrationState) clearPendingReconcile(operation string) {
	delete(s.PendingReconcile, operation)
	delete(s.PendingReconcileHasRevision, operation)
}

func (c ProjectControlClient) confirmedConnectionRejection(ctx context.Context, o generationUIOperation, err error) bool {
	var response *ControlResponseError
	if !errors.As(err, &response) || response.Status != http.StatusConflict || response.Phase != "pre_mutation" {
		return false
	}
	var receipt controlplane.ConnectionOperationReceipt
	return c.request(ctx, http.MethodGet, connectionOperationPath(o.Operation), nil, &receipt) == nil &&
		connectionReceiptMatches(o, receipt) && receipt.Recorded && receipt.Status == "complete" && receipt.Outcome == "rejected"
}

func canSafelyUnlockPreview(p controlplane.ConnectionOperationReceipt) bool {
	return (p.Recorded || p.Status == "unrecorded") && p.Reconcilable && p.Phase != "post_mutation"
}

func (a *App) reviewConnectionOperation(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.Loading || a.Update == nil || !o.connection() || !o.valid() {
		return
	}
	s.Busy = true
	s.Error, s.Message = "", ""
	s.clearPendingReconcile(o.Operation)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cp := client.controlPlaneClient()
		receipt, err := cp.ConnectionOperation(ctx, o.Operation)
		var preview controlplane.ConnectionOperationReceipt
		complete := err == nil && receipt.Recorded && receipt.Status == "complete" &&
			connectionReceiptMatches(o, receipt) &&
			(receipt.Outcome == "rejected" || receipt.Result == nil || receipt.Result.Connection.ID == "" || receipt.Result.Connection.ID == o.Profile)
		var previewErr error
		var hasRevision bool
		if err == nil && !complete && (!receipt.Recorded || receipt.Status != "complete") && connectionReceiptMatches(o, receipt) {
			preview, hasRevision, previewErr = previewPendingConnection(ctx, cp, o)
		}
		if !connectionReceiptMatches(o, receipt) || !connectionReceiptMatches(o, preview) || previewErr != nil || preview.Status == "" && !preview.Recorded {
			preview = controlplane.ConnectionOperationReceipt{}
			hasRevision = false
			if receipt.Recorded && connectionReceiptMatches(o, receipt) {
				preview = receipt
				preview.Reconcilable = false
			}
		}
		if receipt.Recorded && connectionReceiptMatches(o, receipt) {
			preview.Recorded = true
			if receipt.Phase == "post_mutation" {
				preview.Phase = receipt.Phase
			}
			if receipt.Status == "unknown" {
				preview.Status = receipt.Status
			}
		}
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = "无法读取 Agent 原操作回执，请重新核对。"
				return
			}
			if complete {
				message := "Agent 原操作回执已核对。"
				if receipt.Outcome == "rejected" {
					message = "原接入操作已确认拒绝。" + formatControlConflictMessage(receipt.ErrorCode, receipt.Phase)
				}
				a.finishPendingConnection(o.Operation, message)
				return
			}
			if s.PendingReconcile == nil {
				s.PendingReconcile = make(map[string]controlplane.ConnectionOperationReceipt)
			}
			if s.PendingReconcileHasRevision == nil {
				s.PendingReconcileHasRevision = make(map[string]bool)
			}
			s.PendingReconcile[o.Operation] = preview
			s.PendingReconcileHasRevision[o.Operation] = hasRevision
			if canSafelyUnlockPreview(preview) {
				s.Message = "已核对该待确认操作：服务端确认未写入宿主文件，可点击「安全解锁」解除锁定。"
			} else {
				s.Message = "该操作存在写入歧义或状态未确认，已保留原记录，请人工核对或交给 Agent 排障。"
				if previewErr != nil {
					s.Error = controlUserError(previewErr)
				}
			}
		})
	}()
}

func (a *App) reconcilePendingConnectionOperation(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	preview := s.PendingReconcile[o.Operation]
	pending := s.pendingConnection(o.Profile)
	if !ok || s.Busy || s.Loading || a.Update == nil || pending == nil || *pending != o ||
		!o.valid() || !canSafelyUnlockPreview(preview) {
		return
	}
	s.Busy = true
	s.Error, s.Message = "", ""
	revision := preview.Revision
	if !s.PendingReconcileHasRevision[o.Operation] {
		revision = s.Connections.Revision
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cp := client.controlPlaneClient()
		receipt, err := cp.ReconcilePendingOperationFor(ctx, o.Operation, revision, o.Profile, controlplane.UIOperation(o).ConnectionAction())
		a.Update(func() {
			s.Busy = false
			if err != nil {
				// A subsequent attempt needs a new read-only check.
				s.clearPendingReconcile(o.Operation)
				if isControlErrorCode(err, http.StatusConflict, ambiguousConnectionOperation) {
					s.PendingReconcile[o.Operation] = controlplane.ConnectionOperationReceipt{}
				}
				s.Error = controlUserError(err)
				return
			}
			if !connectionReceiptMatches(o, receipt) || !receipt.Recorded || !(receipt.Reconciled || receipt.SafeToUnlock || receipt.AlreadyComplete ||
				(receipt.Status == "complete" && receipt.Outcome == "rejected")) {
				s.PendingReconcile[o.Operation] = controlplane.ConnectionOperationReceipt{}
				delete(s.PendingReconcileHasRevision, o.Operation)
				s.Error = "解锁回执尚未确认，已保留原记录，请重新核对。"
				return
			}
			a.finishPendingConnection(o.Operation, "已安全解锁该连接操作。")
		})
	}()
}

func (a *App) reviewHostStateForPendingOperation(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.Loading || a.Update == nil || !o.connection() || !o.valid() {
		return
	}
	s.Busy = true
	s.Error, s.Message = "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cp := client.controlPlaneClient()
		var directory ConnectionsDirectory
		reviewErr := client.request(ctx, http.MethodGet, "/v1/connections", nil, &directory)
		if reviewErr == nil {
			_, reviewErr = cp.ReviewConnection(ctx, o.Profile, directory.Revision, integrationOperation())
		}
		var receipt controlplane.ConnectionOperationReceipt
		receiptRead := false
		if reviewErr == nil {
			var err error
			receipt, err = cp.ConnectionOperation(ctx, o.Operation)
			receiptRead = err == nil && connectionReceiptMatches(o, receipt)
		}
		terminal := receiptRead && receipt.Recorded &&
			(receipt.Status == "complete" || receipt.Reconciled || receipt.AlreadyComplete || receipt.SafeToUnlock)
		var refreshErr error
		if !terminal {
			refreshErr = client.request(ctx, http.MethodGet, "/v1/connections", nil, &directory)
		}
		a.Update(func() {
			s.Busy = false
			if terminal {
				a.finishPendingConnection(o.Operation, "已核对连接状态并确认原操作回执。")
				return
			}
			if refreshErr == nil {
				s.Connections = directory
				s.ConnectionsLoaded = true
			}
			s.ConnectionsError = controlUserError(refreshErr)
			if receiptRead {
				if s.PendingReconcile == nil {
					s.PendingReconcile = make(map[string]controlplane.ConnectionOperationReceipt)
				}
				// Unlock requires a fresh dry-run preview.
				receipt.Reconcilable = false
				s.PendingReconcile[o.Operation] = receipt
				delete(s.PendingReconcileHasRevision, o.Operation)
			}
			if reviewErr != nil {
				s.Error = "无法核对连接状态，原操作仍待确认。" + controlUserError(reviewErr)
			} else {
				s.Message = "已核对连接状态，原操作回执仍待确认，请重新核对。"
			}
		})
	}()
}

func (a *App) finishPendingConnection(operation, message string) {
	if a.finishGenerationOperation(operation) {
		s := &a.integrations
		s.clearPendingReconcile(operation)
		s.Message = message
		a.RefreshIntegrations()
	}
}

package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func (c *Client) Connections(ctx context.Context, force bool) (ConnectionsDirectory, error) {
	path := "/v1/connections"
	if force {
		path += "?force=1"
	}
	var result ConnectionsDirectory
	err := c.Request(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *Client) PreviewConnection(ctx context.Context, instanceID string) (ConnectionPreview, error) {
	var result ConnectionPreview
	err := c.Request(ctx, http.MethodGet, connectionPath(instanceID, "preview"), nil, &result)
	return result, err
}

func (c *Client) PrepareConnection(ctx context.Context, instanceID string, expectedRevision int64, operationID string) (ConnectionActionResult, error) {
	return c.connectionAction(ctx, instanceID, "prepare", expectedRevision, operationID, nil)
}

func (c *Client) ActivateConnection(ctx context.Context, instanceID string, expectedRevision int64, operationID, proposedSHA string) (ConnectionActionResult, error) {
	return c.connectionAction(ctx, instanceID, "activate", expectedRevision, operationID, &proposedSHA)
}

func (c *Client) ReviewConnection(ctx context.Context, instanceID string, expectedRevision int64, operationID string) (ConnectionActionResult, error) {
	return c.connectionAction(ctx, instanceID, "review", expectedRevision, operationID, nil)
}

func (c *Client) AllowCapture(ctx context.Context, instanceID string, expectedRevision int64, operationID, reviewedSHA string) (ConnectionActionResult, error) {
	return c.connectionAction(ctx, instanceID, "allow_capture", expectedRevision, operationID, &reviewedSHA)
}

func (c *Client) DeferCapture(ctx context.Context, instanceID string, expectedRevision int64, operationID, reviewedSHA string) (ConnectionActionResult, error) {
	return c.connectionAction(ctx, instanceID, "defer_capture", expectedRevision, operationID, &reviewedSHA)
}

func (c *Client) connectionAction(ctx context.Context, instanceID, action string, expectedRevision int64, operationID string, proposedSHA *string) (ConnectionActionResult, error) {
	input := struct {
		ExpectedRevision int64   `json:"expected_revision"`
		OperationID      string  `json:"operation_id"`
		ProposedSHA      *string `json:"proposed_sha256,omitempty"`
	}{expectedRevision, operationID, proposedSHA}
	var result ConnectionActionResult
	err := c.Request(ctx, http.MethodPost, connectionPath(instanceID, action), input, &result)
	return result, err
}

func (c *Client) SelectEnvironment(ctx context.Context, kind, profile string, expectedRevision int64) (ConnectionsDirectory, error) {
	input := map[string]any{"kind": kind, "profile": profile, "expected_revision": expectedRevision}
	var result ConnectionsDirectory
	err := c.Request(ctx, http.MethodPost, "/v1/connections/environment", input, &result)
	return result, err
}

func (c *Client) PreviewRepairTraeHooks(ctx context.Context, instanceID string) (TraeHooksRepairResult, error) {
	var result TraeHooksRepairResult
	err := c.Request(ctx, http.MethodPost, connectionPath(instanceID, "repair-trae-hooks"), map[string]bool{"dry_run": true}, &result)
	return result, err
}

func (c *Client) RepairTraeHooks(ctx context.Context, instanceID string, expectedRevision int64, operationID, reviewedSHA string) (TraeHooksRepairResult, error) {
	input := map[string]any{"expected_revision": expectedRevision, "operation_id": operationID, "reviewed_sha": reviewedSHA}
	var result TraeHooksRepairResult
	err := c.Request(ctx, http.MethodPost, connectionPath(instanceID, "repair-trae-hooks"), input, &result)
	return result, err
}

func (c *Client) ConnectionOperation(ctx context.Context, operationID string) (ConnectionOperationReceipt, error) {
	var result ConnectionOperationReceipt
	err := c.Request(ctx, http.MethodGet, "/v1/connections/operations/"+url.PathEscape(operationID), nil, &result)
	return result, err
}

func (c *Client) PreviewReconcilePendingOperation(ctx context.Context, operationID string) (ConnectionOperationReceipt, error) {
	return c.PreviewReconcilePendingOperationFor(ctx, operationID, "", "")
}

func (c *Client) PreviewReconcilePendingOperationFor(ctx context.Context, operationID, instanceID, action string) (ConnectionOperationReceipt, error) {
	return c.reconcilePendingOperation(ctx, operationID, nil, instanceID, action, true)
}

func (c *Client) ReconcilePendingOperation(ctx context.Context, operationID string, expectedRevision int64) (ConnectionOperationReceipt, error) {
	return c.ReconcilePendingOperationFor(ctx, operationID, expectedRevision, "", "")
}

func (c *Client) ReconcilePendingOperationFor(ctx context.Context, operationID string, expectedRevision int64, instanceID, action string) (ConnectionOperationReceipt, error) {
	return c.reconcilePendingOperation(ctx, operationID, &expectedRevision, instanceID, action, false)
}

func (c *Client) reconcilePendingOperation(ctx context.Context, operationID string, expectedRevision *int64, instanceID, action string, dryRun bool) (ConnectionOperationReceipt, error) {
	input := map[string]any{}
	if dryRun {
		input["dry_run"] = true
	} else {
		if expectedRevision == nil || *expectedRevision < 0 {
			return ConnectionOperationReceipt{}, errors.New("expected_revision must be non-negative")
		}
		input["expected_revision"] = *expectedRevision
	}
	if instanceID != "" {
		input["instance_id"] = instanceID
	}
	if action != "" {
		input["action"] = action
	}
	var result ConnectionOperationReceipt
	err := c.Request(ctx, http.MethodPost, "/v1/connections/operations/"+url.PathEscape(operationID)+"/reconcile", input, &result)
	return result, err
}

func connectionPath(instanceID, action string) string {
	return "/v1/connections/" + url.PathEscape(instanceID) + "/" + action
}

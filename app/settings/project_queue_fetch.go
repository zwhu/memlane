package settings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zwhu/memlane/app/api"
)

func (a *Controller) fetchProjectQueueSnapshot(ctx context.Context, in projectQueueFetchInput) projectQueueSnapshot {
	snap := projectQueueSnapshot{setupOnly: in.setupOnly}
	a.fetchProjectQueueSummary(ctx, in, &snap)

	snap.authDenied = readingAuthDenied(snap.documentErr) || readingAuthDenied(snap.activityErr) ||
		IsControlErrorCode(snap.documentErr, 503, "reading_contract_mismatch")
	if !in.setupOnly && in.control != nil && !snap.authDenied && (snap.gateway || !snap.v2SummaryOK || in.refreshInbox) {
		var err error
		snap.inbox, err = fetchProjectInbox(ctx, in)
		if err == nil && isControlNotFound(snap.documentErr) {
			// The Go gateway exposes the inbox without the legacy summary.
			snap.documentErr, snap.activityErr = nil, nil
		}
		if err != nil {
			if snap.gateway {
				snap.documentErr = err
			}
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && (apiErr.IsUnauthorized() || apiErr.IsForbidden()) {
				snap.authDenied = true
				snap.documentErr = err
			}
		}
	}
	return snap
}

func fetchProjectInbox(ctx context.Context, in projectQueueFetchInput) (*api.ProjectView, error) {
	identity, err := readingIdentity(in.client)
	if err != nil {
		return nil, err
	}
	manager := ProjectControlClient{Config: *in.control, HTTP: in.client.HTTP}
	client, err := newAppAPIClient(manager, identity)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	inbox, err := client.GetProject(ctx, in.project.ID)
	if err != nil {
		return nil, err
	}
	if inbox.ID != in.project.ID || inbox.DatasetID != in.project.Dataset {
		return nil, errors.New("inbox response does not match the requested project and dataset")
	}
	return &inbox, nil
}

func (a *Controller) fetchProjectQueueSummary(ctx context.Context, in projectQueueFetchInput, snap *projectQueueSnapshot) {
	if in.control == nil {
		err := fmt.Errorf("控制面服务不可用，请确认服务已启动")
		snap.documentErr, snap.activityErr = err, err
		return
	}
	client, p := in.client, in.project
	identity, _ := readingIdentity(Client{Config: client.Config})
	summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	manager := ProjectControlClient{Config: *in.control, HTTP: client.HTTP}
	if err := manager.RequireReadingContract(summaryCtx); err != nil {
		snap.documentErr, snap.activityErr = err, err
		return
	}
	if !in.setupOnly {
		gateway, err := manager.ProbeFeature(summaryCtx, "async_capture")
		if err != nil {
			snap.documentErr, snap.activityErr = err, err
			return
		}
		if gateway {
			snap.gateway = true
			snap.readingState, err = fetchGatewayQueueReadingState(summaryCtx, manager, identity, p)
			if err == nil && snap.readingState.UnreadCount > 10 {
				apiClient, pageErr := newAppAPIClient(manager, identity)
				var page api.KnowledgePage
				if pageErr == nil {
					page, pageErr = apiClient.ListKnowledge(summaryCtx, api.KnowledgePageOptions{
						ProjectID: p.ID, Sort: "date", UnreadOnly: true, Limit: 10,
					})
				}
				if readingAuthDenied(pageErr) {
					err = pageErr
				} else if pageErr == nil && page.UnreadStateRev == snap.readingState.StateRev && page.UnreadCount == snap.readingState.UnreadCount {
					snap.unreadPage = &page
				}
			}
			if readingAuthDenied(err) {
				snap.documentErr = err
			} else if err != nil {
				snap.v2ReadStateErr = err.Error()
			}
			return
		}
	}
	v2Res, v2Err := manager.projectMessageSummaryV2(summaryCtx, identity, p.ID)
	cancel()
	if v2Err == nil {
		snap.v2Summary = v2Res
		snap.v2SummaryOK = true
		if msg := v2Res.Errors["documents"]; msg != "" {
			snap.documentErr = fmt.Errorf("%s", msg)
		} else {
			snap.pendingDocumentsCount = v2Res.PendingDocumentsCount
			snap.knowledgeVersion = v2Res.KnowledgeCatalogVersion
		}
		if msg := v2Res.Errors["activity"]; msg != "" {
			snap.activityErr = fmt.Errorf("%s", msg)
		} else {
			snap.activity = v2Res.Activity
		}
		snap.summaryBacklogErr = v2Res.Errors["backlog"]
		snap.v2ReadStateErr = v2Res.Errors["reading_state"]
		return
	}
	snap.documentErr, snap.activityErr = v2Err, v2Err
}

func fetchGatewayQueueReadingState(ctx context.Context, manager ProjectControlClient, identity string, p AccessProject) (*api.ReadingStateView, error) {
	response, err := (projectUnreadAPI{manager, identity}).ReadingState(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, state := range response.Projects {
		if state.ProjectID == p.ID && state.DatasetID == p.Dataset {
			return &state, nil
		}
	}
	return nil, fmt.Errorf("项目未读状态暂时无法读取")
}

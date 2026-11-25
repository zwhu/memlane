package settings

import (
	"context"
	"time"
)

const agentReceiptCheckError = "接入状态暂时无法核对，保留上次结果。"

func (s *IntegrationState) hasPendingAgentReceipts() bool {
	if !s.Loaded || s.Loading || s.Busy || s.ReceiptCheckLoading {
		return false
	}
	for _, row := range s.Connections.Connections {
		if (captureConnection(row) && !captureVerifiedHealthy(row)) ||
			(row.Status == "configured_pending_new_session" && row.NewSessions == 0) ||
			row.Status == "environment_receipt_unconfirmed" {
			return true
		}
	}
	return false
}

func (a *Controller) refreshAgentReceiptsIfDue(now time.Time) {
	s := &a.integrations
	if a.windowIsHidden() || a.windowIsInactive() || !s.hasPendingAgentReceipts() || now.Before(s.NextReceiptCheck) || !a.canDispatch() {
		return
	}
	client, ok := a.accessClient()
	if !ok {
		return
	}
	s.NextReceiptCheck = now.Add(10 * time.Second)
	s.ReceiptCheckLoading = true
	request := s.Request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result ConnectionsDirectory
		err := client.request(ctx, "GET", "/v1/connections", nil, &result)
		a.dispatch(func() {
			s.ReceiptCheckLoading = false
			if request != s.Request || s.Busy || a.config.ProjectControl == nil || *a.config.ProjectControl != client.Config {
				return
			}
			if err != nil {
				s.Error = agentReceiptCheckError
				a.captureReadFailed()
				return
			}
			if s.Error == agentReceiptCheckError {
				s.Error = ""
			}
			a.acceptConnectionsDirectory(result)
		})
	}()
}

func (a *Controller) selectAgentEnvironment(row AgentConnection, profile string) {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.Loading || profile == "" || !a.canDispatch() {
		return
	}
	s.Busy = true
	s.Error = ""
	s.Request++
	request, revision := s.Request, s.Connections.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var result ConnectionsDirectory
		err := client.request(ctx, "POST", "/v1/connections/environment", map[string]any{
			"expected_revision": revision, "kind": row.Kind, "profile": profile,
		}, &result)
		a.dispatch(func() {
			if s.Request != request {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "环境选择结果未确认，请重新检测核对；不会重复提交。"
				return
			}
			a.acceptConnectionsDirectory(result)
			s.Selected = map[string]bool{}
			s.EnvironmentOpen = map[string]bool{}
			s.Message = "已选择连接环境，请连接该环境并在 Agent 中新建会话。"
			if a.initialSetup {
				a.environment.pinnedPreviewRequested = nil
				a.preloadPinnedConnectionPreviews()
			}
		})
	}()
}

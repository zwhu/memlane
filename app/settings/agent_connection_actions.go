package settings

import (
	"context"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"
)

func (a *Controller) connectAgents(action string, identifiers []string) {
	a.connectAgentsReviewed(action, identifiers, "")
}

func (a *Controller) connectAgentsReviewed(action string, identifiers []string, reviewedSHA string) {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.Loading || len(identifiers) == 0 || !a.canDispatch() {
		return
	}
	pending, err := a.loadGenerationOperations()
	if err != nil {
		s.Error = "无法读取待核对操作，已停止发送新接入请求"
		return
	}
	s.PendingOperations = pending
	var eligible []string
	for _, id := range identifiers {
		if action == "review" || s.pendingConnection(id) == nil {
			eligible = append(eligible, id)
		}
	}
	if len(eligible) == 0 {
		s.Message = "请先核对 Agent 原操作；未重新提交"
		s.MessageScope = "connections"
		return
	}
	identifiers = eligible
	s.Busy = true
	s.Error = ""
	s.Message = ""
	s.MessageScope = ""
	stop := &atomic.Bool{}
	s.BatchStop = stop
	if s.ItemMessages == nil {
		s.ItemMessages = map[string]string{}
	}
	opsDir := a.generationOperationsDir()
	go func() {
		for _, id := range identifiers {
			if stop.Load() {
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			var current ConnectionsDirectory
			err := client.request(ctx, "GET", "/v1/connections", nil, &current)
			var target *AgentConnection
			for _, row := range current.Connections {
				if row.ID == id {
					value := row
					target = &value
					break
				}
			}
			if err == nil && target == nil {
				err = fmt.Errorf("Agent 接入配置已变化，请重新读取")
			}
			if err == nil && !connectionActionAllowed(*target, action) {
				if action == "allow_capture" || action == "defer_capture" {
					err = fmt.Errorf("自动采集状态已变化，请重新检测")
				} else {
					cancel()
					continue
				}
			}
			if err == nil && (action == "allow_capture" || action == "defer_capture") &&
				(target.Capture == nil || target.Capture.ReviewSHA != reviewedSHA) {
				err = fmt.Errorf("接入配置已变化，请重新检测后再操作")
			}
			o := generationUIOperation{}
			rejected := false
			if err == nil {
				o = generationUIOperation{Operation: integrationOperation(), Kind: "connection_" + action, Profile: id}
				payload := map[string]any{"expected_revision": current.Revision, "operation_id": o.Operation}
				if action == "activate" {
					if target.Preview == nil || target.Status != "prepared" {
						cancel()
						continue
					}
					if reviewedSHA != "" && target.Preview.ProposedSHA != reviewedSHA {
						cancel()
						a.dispatch(func() { s.ItemMessages[id] = formatControlConflictMessage("reviewed_preview_required", "pre_mutation") })
						continue
					}
					payload["proposed_sha256"] = target.Preview.ProposedSHA
				}
				if action == "allow_capture" || action == "defer_capture" {
					payload["proposed_sha256"] = reviewedSHA
				}
				if action != "review" {
					var updatedPending []generationUIOperation
					updatedPending, err = recordGenerationOperationAt(opsDir, o)
					if err == nil {
						a.dispatch(func() { s.PendingOperations = updatedPending })
					}
				}
				var result struct {
					Revision   int64           `json:"revision"`
					Connection AgentConnection `json:"connection"`
				}
				if err == nil {
					err = client.request(ctx, "POST", "/v1/connections/"+url.PathEscape(id)+"/"+action, payload, &result)
					if action != "review" {
						rejected = client.confirmedConnectionRejection(ctx, o, err)
					}
					if err == nil && result.Connection.ID != id {
						err = fmt.Errorf("Agent 原操作回执未确认，请核对")
					}
				}
			}
			var directory ConnectionsDirectory
			readErr := client.request(ctx, "GET", "/v1/connections", nil, &directory)
			cancel()
			a.dispatch(func() {
				if readErr == nil {
					a.acceptConnectionsDirectory(directory)
				}
				if err != nil {
					if rejected {
						a.finishGenerationOperation(o.Operation)
					}
					s.ItemMessages[id] = ControlUserError(err)
				} else {
					if action != "review" {
						a.finishGenerationOperation(o.Operation)
					}
					s.ItemMessages[id] = ""
					if action == "activate" {
						delete(s.Selected, id)
					}
					if action == "allow_capture" || action == "defer_capture" {
						delete(s.CaptureReview, id)
					}
				}
			})
		}
		a.dispatch(func() {
			s.Busy = false
			s.BatchStop = nil
			a.RefreshProjectDirectory()
			a.preloadPinnedConnectionPreviews()
		})
	}()
}

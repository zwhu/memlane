package simpledesktop

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type backlogJob struct {
	ProcessingActive    int    `json:"processing_active"`
	ProcessingPartial   int    `json:"processing_partial"`
	Automatic           bool   `json:"automatic"`
	ProcessingPending   int    `json:"processing_pending"`
	ReviewFailed        int    `json:"review_failed"`
	Operation           string `json:"operation_id"`
	Status              string `json:"status"`
	Reason              string `json:"reason"`
	Saved               int    `json:"saved"`
	Handled             int    `json:"handled"`
	Isolated            int    `json:"isolated"`
	Remaining           int    `json:"remaining"`
	Total               int    `json:"total"`
	ProcessingSubmitted int    `json:"processing_submitted"`
	ProcessingFailed    int    `json:"processing_failed"`
	ProcessingDisabled  int    `json:"processing_disabled"`
}

type bufferReview struct {
	Rejected        int    `json:"rejected"`
	RejectedUnsent  int    `json:"rejected_unsent"`
	RejectedUnknown int    `json:"rejected_unknown"`
	DeferredUnsent  int    `json:"deferred_unsent"`
	AgentID         string `json:"agent_id"`
	Host            string `json:"host"`
	Signature       string `json:"signature"`
	Filtered        int    `json:"filtered"`
}

func (q *projectQueueState) heldPending(row queuedSession) bufferReview {
	if row.View != nil && row.View.Loaded && row.View.ReadError == "" {
		for _, review := range q.BufferReviews {
			if review.AgentID == row.Agent.ID && review.Host == row.Binding.HostSessionKey && review.Signature != "" && review.Signature == row.View.Status.Signature {
				return review
			}
		}
	}
	return bufferReview{}
}

func (q *projectQueueState) filteredPending(row queuedSession) int {
	if row.View == nil || !row.View.Loaded || row.View.ReadError != "" {
		return 0
	}
	s := row.View.Status
	for _, review := range q.BufferReviews {
		if review.AgentID == row.Agent.ID && review.Host == row.Binding.HostSessionKey && review.Signature != "" && review.Signature == s.Signature {
			return min(max(0, review.Filtered), max(0, s.Pending-s.Unknown))
		}
	}
	return 0
}

type pendingCheckState struct {
	AgentID   string  `json:"agent_id"`
	Host      string  `json:"host"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason"`
	CheckedAt float64 `json:"checked_at"`
	Matched   int     `json:"matched"`
}

func (q *projectQueueState) pendingCheck(session queuedSession) pendingCheckState {
	check := q.Checks[session.Agent.ID+"/"+session.Binding.HostSessionKey]
	if q.BacklogError != "" {
		check.Status, check.Reason = "unconfirmed", "official_review_unavailable"
	}
	return check
}

func (a *App) applyPendingChecks(q *projectQueueState, checks []pendingCheckState) {
	next := map[string]pendingCheckState{}
	for _, check := range checks {
		key := check.AgentID + "/" + check.Host
		if check.CheckedAt > q.Checks[key].CheckedAt {
			q.Next = time.Time{}
		}
		next[key] = check
	}
	q.Checks = next
}

func (a *App) retryPendingCheck(q *projectQueueState, p AccessProject, session queuedSession) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	key := session.Agent.ID + "/" + session.Binding.HostSessionKey
	if q.pendingCheck(session).Status == "checking" {
		return
	}
	if q.Checks == nil {
		q.Checks = map[string]pendingCheckState{}
	}
	q.Checks[key] = pendingCheckState{AgentID: session.Agent.ID, Host: session.Binding.HostSessionKey, Status: "checking"}
	identity := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var result struct {
			Checks []pendingCheckState `json:"checks"`
		}
		err := client.request(ctx, "POST", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog/check", map[string]string{"agent_id": session.Agent.ID, "host": session.Binding.HostSessionKey}, &result)
		a.Update(func() {
			current := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
			if current != identity {
				return
			}
			if err != nil {
				q.Checks[key] = pendingCheckState{Status: "unconfirmed", Reason: "official_review_unavailable"}
				return
			}
			a.applyPendingChecks(q, result.Checks)
			q.BacklogNext = time.Time{}
		})
	}()
}

func (j *backlogJob) active() bool {
	if j == nil {
		return false
	}
	switch j.Status {
	case "queued", "reviewing", "running", "waiting", "stopping", "processing":
		return true
	}
	return false
}

func (a *App) refreshBacklog(q *projectQueueState, p AccessProject) {
	if a.isSetupOnlyRuntime() {
		q.BacklogError = ""
		return
	}
	client, ok := a.accessClient()
	if !ok || client.Config.URL == "" || client.Config.ManagementTokenFile == "" || a.Update == nil || q.BacklogLoading || q.BacklogBusy {
		return
	}
	q.BacklogLoading = true
	q.BacklogNext = time.Now().Add(2 * time.Second)
	identity := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var result struct {
			Job              *backlogJob         `json:"job"`
			Automatic        bool                `json:"automatic_enabled"`
			Checks           []pendingCheckState `json:"checks"`
			Reviews          []bufferReview      `json:"buffer_reviews"`
			ProcessingIssues []processingIssue   `json:"processing_issues"`
		}
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog", nil, &result)
		a.Update(func() {
			q.BacklogLoading = false
			current := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
			if current != identity {
				return
			}
			if err != nil {
				var rejected *ControlResponseError
				if a.isSetupOnlyRuntime() && errors.As(err, &rejected) && rejected.Status == http.StatusNotFound {
					q.BacklogError = ""
					return
				}
				q.BacklogError = "后台采集状态暂时无法读取；任务不会因此重复启动"
				return
			}
			q.BacklogError = ""
			q.Backlog = result.Job
			q.BacklogLoaded = true
			q.AutomaticSave = result.Automatic
			q.BufferReviews = result.Reviews
			q.ProcessingIssues = result.ProcessingIssues
			a.applyPendingChecks(q, result.Checks)
			recovered := []string{"backlog-read"}
			if result.Job == nil || result.Job.active() || result.Job.Status == "completed" {
				recovered = append(recovered, "backlog")
			}
			if result.Job == nil || result.Job.ProcessingFailed == 0 {
				recovered = append(recovered, "processing-failed")
			}
			currentProject := p
			for _, candidate := range a.access.Directory.Projects {
				if candidate.ID == p.ID {
					currentProject = candidate
					break
				}
			}
			if !currentProject.policySyncUnconfirmed() {
				recovered = append(recovered, "policy-sync")
			}
			a.clearRecoveredNoticeAcknowledgements(q, p, recovered)
			if result.Job != nil && result.Job.Operation == q.BacklogOperation {
				q.BacklogOperation = ""
			}
		})
	}()
}

func backlogOperation() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (a *App) changeProjectAutoCapture(q *projectQueueState, p AccessProject, enabled bool) {
	client, ok := a.accessClient()
	if !ok || a.isSetupOnlyRuntime() || a.Update == nil || a.access.Busy || q.AutoCaptureBusy {
		return
	}
	q.AutoCaptureBusy = true
	q.AutoCaptureError = ""
	identity := a.cachedBaseReadingIdentity()
	update := a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := client.SetProjectAutoCapture(ctx, p.ID, enabled)
		update(func() {
			q.AutoCaptureBusy = false
			current, ok := a.accessClient()
			if !ok || current.Config != client.Config || a.cachedBaseReadingIdentity() != identity {
				return
			}
			if err == nil {
				q.applyAutoCapture(result.AutoCapture, result.AutoCaptureVersion)
			} else {
				q.AutoCaptureError = "自动采集设置暂未确认，将重新读取当前设置。"
			}
			q.Next = time.Time{}
			q.BacklogNext = time.Time{}
			a.RefreshProjectDirectory()
		})
	}()
}

func (a *App) changeBacklog(q *projectQueueState, p AccessProject, stop bool) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || q.BacklogBusy || a.Update == nil {
		return
	}
	operation := q.BacklogOperation
	path := "/v1/projects/" + url.PathEscape(p.ID) + "/backlog"
	if stop {
		if q.Backlog == nil {
			return
		}
		operation = q.Backlog.Operation
		path += "/stop"
	} else if operation == "" {
		operation = backlogOperation()
		q.BacklogOperation = operation
	}
	if operation == "" {
		q.BacklogError = "无法创建采集任务标识"
		return
	}
	q.BacklogBusy = true
	identity := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result struct {
			Job *backlogJob `json:"job"`
		}
		err := client.request(ctx, "POST", path, map[string]any{"operation_id": operation}, &result)
		a.Update(func() {
			q.BacklogBusy = false
			current := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
			if current != identity {
				return
			}
			if err != nil {
				q.BacklogError = "启动或停止结果待核对；刷新只查原任务，不会重复发送记录"
				return
			}
			q.BacklogError = ""
			q.Backlog = result.Job
			q.BacklogOperation = ""
			q.Next = time.Time{}
			q.BacklogNext = time.Time{}
		})
	}()
}

func (a *App) reconcileProjectProcessing(q *projectQueueState, p AccessProject) {
	a.runProcessingAction(q, p, "/backlog/processing/check", "核对提炼状态失败", false)
}

func (a *App) retryProjectProcessing(q *projectQueueState, p AccessProject) {
	a.runProcessingAction(q, p, "/backlog/processing/retry", "重新提交提炼失败", true)
}

func (a *App) runProcessingAction(q *projectQueueState, p AccessProject, suffix, errPrefix string, closeIfCleared bool) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || a.Update == nil || q.ProcessingActionBusy || a.access.Busy {
		return
	}
	q.ProcessingActionBusy = true
	q.ProcessingActionError = ""
	identity := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var result struct {
			Job              *backlogJob         `json:"job"`
			Automatic        bool                `json:"automatic_enabled"`
			Checks           []pendingCheckState `json:"checks"`
			Reviews          []bufferReview      `json:"buffer_reviews"`
			ProcessingIssues []processingIssue   `json:"processing_issues"`
		}
		err := client.request(ctx, "POST", "/v1/projects/"+url.PathEscape(p.ID)+suffix, map[string]any{}, &result)
		a.Update(func() {
			q.ProcessingActionBusy = false
			current := a.cachedBaseReadingIdentity() + a.Client.PolicyRevision
			if current != identity {
				return
			}
			if err != nil {
				q.ProcessingActionError = errPrefix + "：" + err.Error()
				return
			}
			q.ProcessingActionError = ""
			q.BacklogError = ""
			q.Backlog = result.Job
			q.BacklogLoaded = true
			q.AutomaticSave = result.Automatic
			q.BufferReviews = result.Reviews
			q.ProcessingIssues = result.ProcessingIssues
			a.applyPendingChecks(q, result.Checks)
			if result.Job == nil || result.Job.ProcessingFailed == 0 {
				a.clearRecoveredNoticeAcknowledgements(q, p, []string{"processing-failed"})
				if closeIfCleared && q.DetailNoticeKey == "processing-failed" {
					q.closeDetailDialog(nil)
				}
			}
			q.Next = time.Time{}
			q.BacklogNext = time.Time{}
		})
	}()
}

func (a *App) reconcileProjectPolicy(q *projectQueueState, p AccessProject) {
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		return client.reconcile(ctx, revision, p.ID)
	}, func() {
		q.BacklogNext = time.Time{}
		q.Next = time.Time{}
		a.clearRecoveredNoticeAcknowledgements(q, p, []string{"policy-sync"})
	})
}

func backlogReason(code string) string {
	switch code {
	case "capture_filter_changed":
		return "部分工具记录不符合当前采集规则，原消息保留；系统会重新核对并继续采集其他符合规则的消息。"
	case "rate_limited":
		return "触发服务端请求频率限制，原消息已保留在本机。请稍后重试，系统会按原编号核对，避免重复采集。"
	case "sender_busy":
		return "正在等待当前发送结束，随后自动继续"
	case "write_denied", "policy_unconfirmed", "backlog_write_denied":
		return "系统未能确认当前写入权限，采集已暂停；原消息仍保留。"
	case "project_auto_capture_disabled":
		return "项目已关闭自动采集，未发送的原消息仍保留。"
	case "request_result_unknown":
		return "一条新请求的结果未知，已保留原记录并暂停；不会自动重发"
	case "owner_interrupted":
		return "后台服务曾中断；原未知记录保留，重新启动只采集明确未发送项"
	case "user_stopped":
		return "已停止后续发送，已完成的采集保留"
	case "backlog_installation_changed", "backlog_binding_changed":
		return "Agent 接入或会话归属发生变化，请核对后继续"
	default:
		return "后台任务已暂停，但没有返回明确原因。原消息仍保留，系统不会自动重发结果未知的消息。"
	}
}

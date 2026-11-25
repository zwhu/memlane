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
			Job *backlogJob `json:"job"`
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

func (a *App) reconcileProjectPolicy(q *projectQueueState, p AccessProject) {
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		return client.reconcile(ctx, revision, p.ID)
	}, func() {
		q.BacklogNext = time.Time{}
		q.Next = time.Time{}
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

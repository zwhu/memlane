package settings

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type BacklogJob struct {
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

func (j *BacklogJob) active() bool {
	if j == nil {
		return false
	}
	switch j.Status {
	case "queued", "reviewing", "running", "waiting", "stopping", "processing":
		return true
	}
	return false
}

func (a *Controller) refreshBacklog(q *ProjectQueueState, p AccessProject) {
	if a.isSetupOnlyRuntime() {
		q.BacklogError = ""
		return
	}
	client, ok := a.accessClient()
	if !ok || client.Config.URL == "" || client.Config.ManagementTokenFile == "" || !a.canDispatch() || q.BacklogLoading || q.BacklogBusy {
		return
	}
	q.BacklogLoading = true
	q.BacklogNext = time.Now().Add(2 * time.Second)
	identity := a.cachedBaseReadingIdentity() + a.client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var result struct {
			Job *BacklogJob `json:"job"`
		}
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog", nil, &result)
		a.dispatch(func() {
			q.BacklogLoading = false
			current := a.cachedBaseReadingIdentity() + a.client.PolicyRevision
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

func (a *Controller) changeProjectAutoCapture(q *ProjectQueueState, p AccessProject, enabled bool) {
	client, ok := a.accessClient()
	if !ok || a.isSetupOnlyRuntime() || !a.canDispatch() || a.access.Busy || q.AutoCaptureBusy {
		return
	}
	q.AutoCaptureBusy = true
	q.AutoCaptureError = ""
	identity := a.cachedBaseReadingIdentity()
	update := a.dispatch
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
				q.applyAutoCaptureMeta(result.AutoCapture, result.AutoCaptureVersion, result.AutoCaptureUpdatedAt,
					result.AutoCaptureActor, result.AutoCaptureReason)
				q.FilteredCount, q.RecentFiltered, q.inboxLoaded = result.FilteredCount, result.RecentFiltered, true
				if q.InboxFailed > 0 {
					q.LastErrorAt = result.LastErrorAt
				}
			} else {
				q.AutoCaptureError = "自动采集设置暂未确认，将重新读取当前设置。"
			}
			q.Next = time.Time{}
			q.BacklogNext = time.Time{}
			a.RefreshProjectDirectory()
		})
	}()
}

func (a *Controller) changeBacklog(q *ProjectQueueState, p AccessProject, stop bool) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || q.BacklogBusy || !a.canDispatch() {
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
	identity := a.cachedBaseReadingIdentity() + a.client.PolicyRevision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result struct {
			Job *BacklogJob `json:"job"`
		}
		err := client.request(ctx, "POST", path, map[string]any{"operation_id": operation}, &result)
		a.dispatch(func() {
			q.BacklogBusy = false
			current := a.cachedBaseReadingIdentity() + a.client.PolicyRevision
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

func (a *Controller) reconcileProjectPolicy(q *ProjectQueueState, p AccessProject) {
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		return client.reconcile(ctx, revision, p.ID)
	}, func() {
		q.BacklogNext = time.Time{}
		q.Next = time.Time{}
	})
}

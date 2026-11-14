package simpledesktop

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/url"
	"time"
)

type backlogJob struct {
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
	client, ok := a.accessClient()
	if !ok || client.Config.URL == "" || client.Config.ManagementTokenFile == "" || a.Update == nil || q.BacklogLoading || q.BacklogBusy {
		return
	}
	q.BacklogLoading = true
	q.BacklogNext = time.Now().Add(2 * time.Second)
	identity, _ := readingIdentity(a.Client)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var result struct {
			Job *backlogJob `json:"job"`
		}
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog", nil, &result)
		a.Update(func() {
			q.BacklogLoading = false
			current, _ := readingIdentity(a.Client)
			if current != identity {
				return
			}
			if err != nil {
				q.BacklogError = "后台保存状态暂时无法读取；任务不会因此重复启动"
				return
			}
			q.BacklogError = ""
			q.Backlog = result.Job
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

func (a *App) changeBacklog(q *projectQueueState, p AccessProject, stop bool) {
	client, ok := a.accessClient()
	if !ok || q.BacklogBusy {
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
		q.BacklogError = "无法创建保存任务标识"
		return
	}
	q.BacklogBusy = true
	identity, _ := readingIdentity(a.Client)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result struct {
			Job *backlogJob `json:"job"`
		}
		err := client.request(ctx, "POST", path, map[string]any{"operation_id": operation}, &result)
		a.Update(func() {
			q.BacklogBusy = false
			current, _ := readingIdentity(a.Client)
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

func backlogReason(code string) string {
	switch code {
	case "sender_busy":
		return "正在等待当前发送结束，随后自动继续"
	case "write_denied", "policy_unconfirmed", "backlog_write_denied":
		return "写入权限未确认，已暂停后续发送"
	case "request_result_unknown":
		return "一条新请求的结果未知，已保留原记录并暂停；不会自动重发"
	case "owner_interrupted":
		return "后台服务曾中断；原未知记录保留，重新启动只保存明确未发送项"
	case "user_stopped":
		return "已停止后续发送，已完成的保存保留"
	case "backlog_installation_changed", "backlog_binding_changed":
		return "Agent 接入或会话归属发生变化，请核对后继续"
	default:
		return "后台结果尚未确认，保留原记录；不会自动重复提交"
	}
}

func (a *App) backlogControls(c *ui.Context, q *projectQueueState, p AccessProject) {
	safe := 0
	for _, row := range q.Sessions {
		if row.View.Loaded && p.Overrides[row.Agent.ID].effective(a.access.Directory.Defaults).Access == "maintain" {
			safe += row.View.Status.Pending - row.View.Status.Unknown
		}
	}
	ui.Column(c).FillWidth().Padding(14, 20).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if q.Backlog.active() {
				homeText(c, "积压保存任务 · 后台运行", homeBody).FontSize(12)
				ui.Spacer(c)
				label := "停止后续保存"
				if q.Backlog.Status == "stopping" {
					label = "正在停止…"
				}
				projectLink(c, label, func() { a.changeBacklog(q, p, true) }).Disabled(q.BacklogBusy || q.Backlog.Status == "stopping")
			} else {
				label := "保存积压记录"
				if q.Backlog != nil && (q.Backlog.Status == "paused" || q.Backlog.Status == "stopped" || q.Backlog.Status == "interrupted") {
					label = "继续保存积压"
				}
				projectLink(c, label, func() { a.changeBacklog(q, p, false) }).Disabled(!q.Loaded || q.Loading || q.BacklogBusy || safe == 0 || a.Config.ProjectControl == nil)
				homeText(c, "一次启动，持续保存当前积压；未知项单独保留", homeMeta)
			}
		})
		if q.Backlog != nil {
			j := q.Backlog
			if j.Status == "queued" || j.Status == "reviewing" {
				homeText(c, "正在核对并固定本次会话范围…", homeMeta)
			} else {
				homeText(c, fmt.Sprintf("本次已保存 %d 条 · 其他操作已处理 %d 条 · 剩余 %d 条 · 隔离待核对 %d 条", j.Saved, j.Handled, j.Remaining, j.Isolated), homeMeta)
			}
			if j.Status == "completed" {
				homeText(c, "本次可保存积压已完成。新增记录由正常采集继续接收。", homeMeta)
			}
			if j.Reason != "" {
				homeText(c, backlogReason(j.Reason), homeMeta)
			}
			if j.Status == "processing" {
				homeText(c, "保存已完成，正在按现有设置提交知识整理…", homeMeta)
			}
			if j.ProcessingSubmitted > 0 {
				homeText(c, fmt.Sprintf("%d 个会话已提交知识整理；完成状态以官方记录为准", j.ProcessingSubmitted), homeMeta)
			}
			if j.ProcessingDisabled > 0 {
				homeText(c, "部分会话未开启自动整理，已保存的原始记录保留。", homeMeta)
			}
			if j.ProcessingFailed > 0 {
				homeText(c, "部分整理请求未确认；保留原回执，未自动重复提交。", homeMeta)
			}
		}
		if q.BacklogError != "" {
			homeText(c, q.BacklogError, homeMeta)
			projectLink(c, "核对后台任务", func() { a.refreshBacklog(q, p) })
		}
		if q.Backlog.active() {
			homeText(c, "切换页面或关闭窗口后继续；停止只阻止后续发送，等待在途请求返回。", homeMeta)
			homeText(c, "正在发送的记录收到保存回执后才会从队列移除。", homeMeta)
		}
	})
}

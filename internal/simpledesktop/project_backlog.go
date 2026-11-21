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
	identity, _ := readingIdentity(a.Client)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var result struct {
			Checks []pendingCheckState `json:"checks"`
		}
		err := client.request(ctx, "POST", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog/check", map[string]string{"agent_id": session.Agent.ID, "host": session.Binding.HostSessionKey}, &result)
		a.Update(func() {
			current, _ := readingIdentity(a.Client)
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
			Job       *backlogJob         `json:"job"`
			Automatic bool                `json:"automatic_enabled"`
			Checks    []pendingCheckState `json:"checks"`
			Reviews   []bufferReview      `json:"buffer_reviews"`
		}
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/backlog", nil, &result)
		a.Update(func() {
			q.BacklogLoading = false
			current, _ := readingIdentity(a.Client)
			if current != identity {
				return
			}
			if err != nil {
				q.BacklogError = "后台采集状态暂时无法读取；任务不会因此重复启动"
				return
			}
			q.BacklogError = ""
			q.Backlog = result.Job
			q.BacklogLoaded = true
			q.AutomaticSave = result.Automatic
			q.BufferReviews = result.Reviews
			a.applyPendingChecks(q, result.Checks)
			recovered := []string{"backlog-read"}
			if result.Job == nil || result.Job.active() || result.Job.Status == "completed" {
				recovered = append(recovered, "backlog")
			}
			if result.Job == nil || result.Job.ProcessingFailed == 0 {
				recovered = append(recovered, "processing-failed")
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
		q.BacklogError = "无法创建采集任务标识"
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
			if stop {
				q.AutomaticSave = false
			}
			q.Next = time.Time{}
			q.BacklogNext = time.Time{}
		})
	}()
}

func backlogReason(code string) string {
	switch code {
	case "capture_filter_changed":
		return "部分工具记录不符合当前采集规则，原消息保留；系统会重新核对并继续采集其他符合规则的消息。"
	case "sender_busy":
		return "正在等待当前发送结束，随后自动继续"
	case "write_denied", "policy_unconfirmed", "backlog_write_denied":
		return "系统未能确认当前写入权限，采集已暂停；原消息仍保留。"
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

func (a *App) backlogControls(c *ui.Context, q *projectQueueState, p AccessProject) {
	safe := 0
	for _, row := range q.Sessions {
		if row.View.Loaded && p.Overrides[row.Agent.ID].effective(a.access.Directory.Defaults).Access == "maintain" {
			safe += row.View.Status.Pending - row.View.Status.Unknown
		}
	}
	ui.Column(c).FillWidth().Padding(12, 20).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Background(tok.Fill1).Children(func() {
		j := q.Backlog
		paused := j != nil && (j.Status == "paused" || j.Status == "stopped" || j.Status == "interrupted")
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				title, detail := "会话等待采集", "采集已获授权的记录，采集结果未确认的条目会单独保留。"
				switch {
				case paused:
					title, detail = "会话采集已暂停", backlogReason(j.Reason)
				case q.AutomaticSave || (j != nil && j.Automatic && j.active()):
					title, detail = "自动采集已开启", "新会话记录会在后台持续采集，状态待确认的记录不会重复发送。"
				case j.active():
					title, detail = "正在采集会话", "切换页面后会继续采集。"
					if j.Status == "processing" {
						title, detail = "正在提交记忆提炼", "会话已采集，提炼完成后才会产生记忆。"
					}
				case j != nil && j.Status == "completed":
					title, detail = "上次采集已完成", "当前待采集数量见上方；新增记录单独统计。"
				}
				homeText(c, title, homeBody).FontSize(12).FontWeight(500).TextColor(homeInk)
				homeText(c, detail, homeMeta).MaxLines(2)
			})
			switch {
			case !paused && (q.AutomaticSave || j.active()):
				label := "暂停自动采集"
				if j != nil && !j.Automatic {
					label = "停止后续采集"
				}
				if j != nil && j.Status == "stopping" {
					label = "正在停止…"
				}
				if j != nil {
					queueAction(c, label, func() { a.changeBacklog(q, p, true) }).Disabled(q.BacklogBusy || j.Status == "stopping")
				}
			case paused && (j.Reason == "write_denied" || j.Reason == "policy_unconfirmed" || j.Reason == "backlog_write_denied"):
				queueAction(c, "重新检查权限", a.RefreshProjectDirectory).Disabled(a.access.Loading || a.access.Busy)
			default:
				label := "采集待处理记录"
				if paused {
					label = "继续采集"
				}
				queueAction(c, label, func() { a.changeBacklog(q, p, false) }).Disabled(!q.Loaded || q.Loading || q.BacklogBusy || (safe == 0 && (j == nil || !j.Automatic)) || a.Config.ProjectControl == nil)
			}
		})
		if q.BacklogError != "" {
			homeText(c, q.BacklogError, homeMeta)
			queueAction(c, "核对后台任务", func() { a.refreshBacklog(q, p) }).AlignSelf(ui.Start)
		}
		if j != nil {
			label := "采集详情 ›"
			if q.SaveDetailsOpen {
				label = "收起采集详情"
			}
			queueAction(c, label, func() { q.SaveDetailsOpen = !q.SaveDetailsOpen }).AlignSelf(ui.Start).Justify(ui.Start)
			if q.SaveDetailsOpen {
				ui.Column(c).Gap(5).Children(func() {
					homeText(c, "以下为后台任务统计，不代表当前待采集数量。", homeMeta)
					homeText(c, fmt.Sprintf("任务已采集 %d 条 · 任务内剩余 %d 条 · 待核对 %d 条", j.Saved, j.Remaining, j.Isolated), homeMeta)
					if j.ProcessingPending > 0 {
						homeText(c, fmt.Sprintf("%d 个会话等待提交记忆提炼", j.ProcessingPending), homeMeta)
					}
					if j.ProcessingSubmitted > 0 {
						homeText(c, fmt.Sprintf("%d 个会话已提交记忆提炼，尚不代表提炼完成。", j.ProcessingSubmitted), homeMeta)
					}
					if j.ProcessingDisabled > 0 {
						homeText(c, "部分会话未开启自动提炼，原始记录已保留。", homeMeta)
					}
					if j.ProcessingFailed > 0 {
						homeText(c, "部分提炼请求的结果尚未确认。", homeMeta)
					}
				})
			}
		}
	})
}

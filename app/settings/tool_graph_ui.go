package settings

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type ToolGraphJob struct {
	Operation string `json:"operation_id"`
	Status    string `json:"status"`
}

type ToolGraphPreview struct {
	Sessions int           `json:"session_count"`
	Steps    int           `json:"tool_steps"`
	Token    string        `json:"review_token"`
	Job      *ToolGraphJob `json:"job"`
}

type toolGraphUIState struct {
	Open, Busy     bool
	Project, Error string
	Request        uint64
	Preview        *ToolGraphPreview
	Job            *ToolGraphJob
	Next           time.Time
}

func ToolGraphActive(job *ToolGraphJob) bool {
	return job != nil && (job.Status == "queued" || job.Status == "running" || job.Status == "stopping")
}

func toolGraphStatus(job *ToolGraphJob) string {
	if job == nil {
		return ""
	}
	labels := map[string]string{"queued": "等待开始", "running": "正在构建关系图谱", "completed": "关系图谱已更新", "cancelled": "已停止", "stopping": "正在停止", "result_unknown": "结果待核对"}
	if label := labels[job.Status]; label != "" {
		return label
	}
	return "状态待核对"
}

func (a *Controller) PreviewToolGraph(project string) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || !a.canDispatch() {
		return
	}
	a.toolGraph.Request++
	request := a.toolGraph.Request
	a.toolGraph = toolGraphUIState{Open: true, Busy: true, Project: project, Request: request}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var preview ToolGraphPreview
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(project)+"/tool-graph/preview", nil, &preview)
		a.dispatch(func() {
			if a.toolGraph.Request != request || a.toolGraph.Project != project {
				return
			}
			a.toolGraph.Busy = false
			if err != nil {
				a.toolGraph.Error = err.Error()
				return
			}
			a.toolGraph.Preview, a.toolGraph.Job = &preview, preview.Job
		})
	}()
}

func (a *Controller) toolGraphAction(action string) {
	if a.isSetupOnlyRuntime() {
		return
	}
	s := &a.toolGraph
	client, ok := a.accessClient()
	if !ok || s.Busy || !a.canDispatch() {
		return
	}
	project, request := s.Project, s.Request
	method, path := "GET", "/v1/projects/"+url.PathEscape(project)+"/tool-graph"
	var body any
	operation := ""
	if action == "start" {
		if s.Preview == nil || s.Preview.Sessions == 0 {
			return
		}
		operation = backlogOperation()
		method = "POST"
		body = map[string]any{"operation_id": operation, "review_token": s.Preview.Token}
	} else {
		if s.Job == nil {
			return
		}
		operation = s.Job.Operation
		path += "/" + url.PathEscape(operation)
		if action == "cancel" {
			method = "DELETE"
		}
	}
	s.Busy, s.Error = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var job ToolGraphJob
		err := client.request(ctx, method, path, body, &job)
		a.dispatch(func() {
			if a.toolGraph.Request != request || a.toolGraph.Project != project {
				return
			}
			a.toolGraph.Busy = false
			a.toolGraph.Next = time.Now().Add(3 * time.Second)
			if err != nil {
				a.toolGraph.Error = err.Error()
				var rejected *ControlResponseError
				if method == "POST" && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					a.toolGraph.Job = nil
				} else if method != "GET" {
					a.toolGraph.Job = &ToolGraphJob{Operation: operation, Status: "result_unknown"}
				}
				return
			}
			a.toolGraph.Job = &job
			if job.Status == "completed" {
				if current, ok := a.currentProject(); ok && current.ID == project {
					if a.host.RefreshGraph != nil {
						a.host.RefreshGraph()
					}
				}
			}
		})
	}()
}

func (a *Controller) toolGraphDialog(c *ui.Context) {
	tok := a.tokens()
	s := &a.toolGraph
	if !s.Open || a.isSetupOnlyRuntime() {
		return
	}
	if ToolGraphActive(s.Job) && !s.Busy && time.Now().After(s.Next) {
		a.toolGraphAction("review")
	}
	closeLabel := "取消"
	if s.Job != nil {
		closeLabel = "关闭"
	}
	inProgress := s.Job != nil && (ToolGraphActive(s.Job) || s.Job.Status == "result_unknown")
	kitui.Dialog(c, tok, &s.Open, kitui.DialogOptions{
		Key:             "tool-graph-modal",
		Title:           "创建工具图谱",
		Label:           "创建工具图谱确认",
		Width:           kitui.DialogSmall,
		CancelText:      closeLabel,
		ConfirmText:     "开始创建",
		HideConfirm:     inProgress,
		ConfirmDisabled: s.Busy || s.Preview == nil || s.Preview.Sessions == 0,
		Hint:            "使用当前模型，会消耗额度。",
		OnCancel:        func() { s.Open = false },
		OnConfirm:       func() { a.toolGraphAction("start") },
		ExtraFooter: func() {
			if !inProgress {
				return
			}
			kitui.OutlineButton(c, tok, "核对进度", nil, func() { a.toolGraphAction("review") }).Disabled(s.Busy)
			kitui.DangerOutlineButton(c, tok, "停止本次创建", false, func() { a.toolGraphAction("cancel") }).Disabled(s.Busy)
		},
		Body: func() {
			kitui.Text(c, tok, "将已有工具记录提炼成关系图谱。", kitui.RoleBody).TextColor(tok.Text2)
			if s.Preview != nil {
				ui.Column(c).FillWidth().Padding(sp3).Gap(sp1).Background(tok.Fill1).Radius(radius).Children(func() {
					kitui.Text(c, tok, fmt.Sprintf("待处理：%d 个会话 · %d 条工具记录", s.Preview.Sessions, s.Preview.Steps), kitui.RoleBody).FontSize(fsBody).FontWeight(500).TextColor(tok.Text1)
					kitui.Text(c, tok, "已处理的会跳过，新记录下次再处理。", kitui.RoleMeta)
				})
				if s.Preview.Sessions == 0 {
					kitui.Text(c, tok, "没有需要处理的新记录。", kitui.RoleMeta)
				}
			}
			if s.Job != nil {
				kitui.Text(c, tok, toolGraphStatus(s.Job), kitui.RoleHeading).FontSize(fsBody)
			} else if s.Busy {
				kitui.SkeletonRow(c, tok, 1, false, false)
				kitui.Text(c, tok, "正在读取待处理记录…", kitui.RoleMeta)
			}
			if s.Error != "" {
				kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger).LineHeight(1.5)
			}
		},
	})
}

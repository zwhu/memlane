package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
)

type toolGraphJob struct {
	Operation string `json:"operation_id"`
	Status    string `json:"status"`
}
type toolGraphPreview struct {
	Sessions int           `json:"session_count"`
	Steps    int           `json:"tool_steps"`
	Token    string        `json:"review_token"`
	Job      *toolGraphJob `json:"job"`
}
type toolGraphUIState struct {
	Open, Busy     bool
	Project, Error string
	Request        uint64
	Preview        *toolGraphPreview
	Job            *toolGraphJob
	Next           time.Time
}

func toolGraphActive(job *toolGraphJob) bool {
	return job != nil && (job.Status == "queued" || job.Status == "running" || job.Status == "stopping")
}
func toolGraphStatus(job *toolGraphJob) string {
	if job == nil {
		return ""
	}
	labels := map[string]string{"queued": "等待开始", "running": "正在构建关系图谱", "completed": "关系图谱已更新", "cancelled": "已停止", "stopping": "正在停止", "result_unknown": "结果待核对"}
	if label := labels[job.Status]; label != "" {
		return label
	}
	return "状态待核对"
}

func (a *App) previewToolGraph(project string) {
	if a.isSetupOnlyRuntime() {
		return
	}
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	a.toolGraph.Request++
	request := a.toolGraph.Request
	a.toolGraph = toolGraphUIState{Open: true, Busy: true, Project: project, Request: request}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var preview toolGraphPreview
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(project)+"/tool-graph/preview", nil, &preview)
		a.Update(func() {
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

func (a *App) toolGraphAction(action string) {
	if a.isSetupOnlyRuntime() {
		return
	}
	s := &a.toolGraph
	client, ok := a.accessClient()
	if !ok || s.Busy || a.Update == nil {
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
		var job toolGraphJob
		err := client.request(ctx, method, path, body, &job)
		a.Update(func() {
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
					a.toolGraph.Job = &toolGraphJob{Operation: operation, Status: "result_unknown"}
				}
				return
			}
			a.toolGraph.Job = &job
			if job.Status == "completed" {
				if current, ok := a.current(); ok && current.ID == project {
					a.loadGraph()
				}
			}
		})
	}()
}

func (a *App) toolGraphDialog(c *ui.Context) {
	s := &a.toolGraph
	if !s.Open || a.isSetupOnlyRuntime() {
		return
	}
	if toolGraphActive(s.Job) && !s.Busy && time.Now().After(s.Next) {
		a.toolGraphAction("review")
	}
	closeLabel := "取消"
	if s.Job != nil {
		closeLabel = "关闭"
	}
	inProgress := s.Job != nil && (toolGraphActive(s.Job) || s.Job.Status == "result_unknown")
	v4Dialog(c, &s.Open, v4DialogOptions{
		Key:             "tool-graph-modal",
		Title:           "创建工具图谱",
		Label:           "创建工具图谱确认",
		Width:           v4DialogSmall,
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
			v4OutlineButton(c, "核对进度", nil, func() { a.toolGraphAction("review") }).Disabled(s.Busy)
			v4DangerOutlineButton(c, "停止本次创建", false, func() { a.toolGraphAction("cancel") }).Disabled(s.Busy)
		},
		Body: func() {
			homeText(c, "将已有工具记录提炼成关系图谱。", homeBody).TextColor(tok.Text2)
			if s.Preview != nil {
				ui.Column(c).FillWidth().Padding(sp3).Gap(sp1).Background(tok.Fill1).Radius(radius).Children(func() {
					homeText(c, fmt.Sprintf("待处理：%d 个会话 · %d 条工具记录", s.Preview.Sessions, s.Preview.Steps), homeBody).FontSize(fsBody).FontWeight(500).TextColor(tok.Text1)
					homeText(c, "已处理的会跳过，新记录下次再处理。", homeMeta)
				})
				if s.Preview.Sessions == 0 {
					homeText(c, "没有需要处理的新记录。", homeMeta)
				}
			}
			if s.Job != nil {
				homeText(c, toolGraphStatus(s.Job), homeHeading).FontSize(fsBody)
			} else if s.Busy {
				v4SkeletonRow(c, 1, false, false)
				homeText(c, "正在读取待处理记录…", homeMeta)
			}
			if s.Error != "" {
				homeText(c, s.Error, homeMeta).TextColor(tok.Danger).LineHeight(1.5)
			}
		},
	})
}

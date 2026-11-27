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

type GenerationHandoffReceipt struct {
	Operation string        `json:"operation_id"`
	Reference GenerationRef `json:"reference"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason"`
	Cancelled int           `json:"cancelled_sessions"`
}

type GenerationHandoffDirectory struct {
	Handoff *GenerationHandoffReceipt `json:"handoff"`
	Pending int                       `json:"pending_sessions"`
	Running int                       `json:"running_sessions"`
	Unknown int                       `json:"unconfirmed_sessions"`
}

func (a *Controller) openGenerationHandoff(ref *GenerationRef) {
	s := &a.integrations
	if !s.Generation.HandoffAvailable || s.Busy || s.HandoffOperation != "" {
		return
	}
	s.HandoffOpen, s.HandoffLoading = true, true
	s.HandoffReady = false
	s.HandoffRequest++
	request := s.HandoffRequest
	s.HandoffRef = nil
	if ref != nil {
		chosen := *ref
		s.HandoffRef = &chosen
	}
	client, ok := a.accessClient()
	if !ok {
		s.HandoffLoading = false
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var value GenerationHandoffDirectory
		err := client.request(ctx, "GET", "/v1/generation/handoffs", nil, &value)
		a.dispatch(func() {
			if request != s.HandoffRequest || !s.HandoffOpen {
				return
			}
			s.HandoffLoading = false
			if err != nil {
				s.Error = "无法读取待提炼数量"
				return
			}
			s.Handoff = value
			s.HandoffReady = true
			s.Error = ""
		})
	}()
}

func (a *Controller) generationHandoffPanel(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if !s.HandoffOpen {
		return
	}
	confirmDisabled := s.Busy || !s.HandoffReady || s.HandoffLoading || s.HandoffRef == nil ||
		(s.HandoffRef != nil && s.Generation.Active != nil && *s.HandoffRef == *s.Generation.Active)
	kitui.Dialog(c, tok, &s.HandoffOpen, kitui.DialogOptions{
		Key:             "generation-enable-review",
		Title:           "切换提炼方式？",
		Width:           kitui.DialogSmall,
		CancelText:      "取消",
		ConfirmText:     "确认启用",
		CancelDisabled:  s.Busy,
		ConfirmDisabled: confirmDisabled,
		OnCancel:        func() { s.HandoffOpen = false },
		OnConfirm:       a.startGenerationHandoff,
		Body: func() {
			kitui.Text(c, tok, generationLabel(s.Generation, s.HandoffRef), kitui.RoleBody).FontSize(fsBody).TextColor(tok.Text1).MaxLines(2)
			if s.HandoffLoading {
				kitui.SkeletonRow(c, tok, 1, false, false)
				kitui.Text(c, tok, "正在读取任务状态…", kitui.RoleMeta)
			} else {
				if s.Handoff.Running > 0 {
					kitui.Text(c, tok, fmt.Sprintf("正在提炼 %d 个会话，切换后将接续提炼。", s.Handoff.Running), kitui.RoleMeta)
				}
				if s.Handoff.Unknown > 0 {
					kitui.Text(c, tok, fmt.Sprintf("另有 %d 个会话结果待核对。", s.Handoff.Unknown), kitui.RoleMeta)
				}
			}
			if s.Error != "" {
				kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger).MaxLines(3)
			}
		},
	})
}

func isConfirmingHandoffStatus(status string) bool {
	return status == "applying" || status == "unknown" || status == "result_unknown"
}

func (s *IntegrationState) activeHandoffReceipt() *GenerationHandoffReceipt {
	if s.Generation.ActiveHandoff != nil {
		if isConfirmingHandoffStatus(s.Generation.ActiveHandoff.Status) {
			return s.Generation.ActiveHandoff
		}
		if s.Handoff.Handoff == nil || s.Handoff.Handoff.Operation != s.Generation.ActiveHandoff.Operation {
			return s.Generation.ActiveHandoff
		}
	}
	return s.Handoff.Handoff
}

func (a *Controller) generationHandoffStatus(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	row := s.activeHandoffReceipt()
	if row == nil {
		return
	}
	label := map[string]string{
		"queued":         "切换已登记",
		"preparing":      "正在准备新模型",
		"stopping":       "正在停止旧任务并核对结果",
		"applying":       "正在确认使用的模型",
		"reconciling":    "正在恢复剩余会话提炼",
		"applied":        "已切换，剩余会话继续提炼",
		"not_applied":    "未切换，仍使用原模型",
		"unknown":        "正在确认使用的模型",
		"result_unknown": "正在确认使用的模型",
	}[row.Status]
	if label == "" {
		return
	}
	if row.Status == "not_applied" && row.Reason == "generation_provider_unavailable" {
		label = "新模型服务无法连接，仍使用原模型"
	}
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		kitui.Text(c, tok, label, kitui.RoleMeta)
		if (row.Status == "unknown" || row.Status == "result_unknown") && row.Operation != "" {
			kitui.TextButton(c, tok, "核对结果", func() { a.pollGenerationHandoff(row.Operation) }).Disabled(s.Busy)
		}
	})
}

func (a *Controller) startGenerationHandoff() {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || !s.HandoffReady || s.HandoffLoading || s.HandoffRef == nil || !a.generationCanUse(s.HandoffRef) {
		return
	}
	ref := *s.HandoffRef
	op := generationUIOperation{Operation: integrationOperation(), Kind: "handoffs", Profile: ref.ID, Revision: ref.Revision}
	if err := a.recordGenerationOperation(op); err != nil {
		s.Error = "无法记录切换操作，尚未发送"
		return
	}
	s.Busy = true
	s.HandoffOperation = op.Operation
	revision := s.Generation.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var value GenerationHandoffDirectory
		err := client.request(ctx, "POST", "/v1/generation/handoffs", map[string]any{"profile": ref, "expected_revision": revision, "operation_id": op.Operation}, &value)
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				var rejected *ControlResponseError
				if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					s.Error = err.Error()
					if a.finishGenerationOperation(op.Operation) {
						s.HandoffOperation = ""
					}
					return
				}
				s.Error = "正在核对原切换结果"
			} else {
				s.Handoff = value
				s.HandoffOpen = false
				s.Error = ""
			}
			a.pollGenerationHandoff(op.Operation)
		})
	}()
}

func (a *Controller) pollGenerationHandoff(operation string) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy {
		return
	}
	s.Busy = true
	s.HandoffOperation = operation
	go func() {
		var value GenerationHandoffDirectory
		var directory GenerationDirectory
		var err error
		for n := 0; n < 180; n++ {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err = client.request(ctx, "GET", "/v1/generation/handoffs/"+url.PathEscape(operation), nil, &value)
			cancel()
			if err != nil || value.Handoff == nil {
				break
			}
			snapshot := value
			a.dispatch(func() { s.Handoff = snapshot })
			if value.Handoff.Status == "applied" || value.Handoff.Status == "not_applied" || value.Handoff.Status == "unknown" {
				break
			}
			time.Sleep(time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.dispatch(func() {
			s.Busy = false
			if readErr == nil {
				s.Generation = directory
			}
			if err != nil || value.Handoff == nil {
				s.Error = "切换结果尚未确认，请核对原操作"
				return
			}
			s.Handoff = value
			if value.Handoff.Status == "applied" || value.Handoff.Status == "not_applied" {
				if a.finishGenerationOperation(operation) {
					s.HandoffOperation = ""
				}
				s.HandoffOpen = false
				s.Error = ""
			} else if readErr == nil && directory.ActiveHandoff == nil && directory.Active != nil && *directory.Active == value.Handoff.Reference {
				if a.finishGenerationOperation(operation) {
					s.HandoffOperation = ""
				}
				s.Handoff = GenerationHandoffDirectory{}
				s.HandoffOpen = false
				s.Error = ""
				s.Message = ""
			}
		})
	}()
}

package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
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

func (a *App) openGenerationHandoff(ref *GenerationRef) {
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
		a.Update(func() {
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
func (a *App) generationHandoffPanel(c *ui.Context) {
	s := &a.integrations
	if !s.HandoffOpen {
		return
	}
	confirmDisabled := s.Busy || !s.HandoffReady || s.HandoffLoading || s.HandoffRef == nil ||
		(s.HandoffRef != nil && s.Generation.Active != nil && *s.HandoffRef == *s.Generation.Active)
	v4Dialog(c, &s.HandoffOpen, v4DialogOptions{
		Key:             "generation-enable-review",
		Title:           "切换提炼方式？",
		Width:           v4DialogSmall,
		CancelText:      "取消",
		ConfirmText:     "确认启用",
		Hint:            "仅确认后的服务回执会更新当前使用的提炼方式",
		CancelDisabled:  s.Busy,
		ConfirmDisabled: confirmDisabled,
		OnCancel:        func() { s.HandoffOpen = false },
		OnConfirm:       a.startGenerationHandoff,
		Body: func() {
			homeText(c, generationLabel(s.Generation, s.HandoffRef), homeBody).FontSize(fsBody).TextColor(tok.Text1).MaxLines(2)
			if s.HandoffLoading {
				v4SkeletonRow(c, 1, false, false)
				homeText(c, "正在读取任务状态…", homeMeta)
			} else {
				if s.Handoff.Running > 0 {
					homeText(c, fmt.Sprintf("正在提炼 %d 个会话，切换后将接续提炼。", s.Handoff.Running), homeMeta)
				}
				if s.Handoff.Unknown > 0 {
					homeText(c, fmt.Sprintf("另有 %d 个会话结果待核对。", s.Handoff.Unknown), homeMeta)
				}
			}
			if s.Error != "" {
				homeText(c, s.Error, homeMeta).TextColor(tok.Danger).MaxLines(3)
			}
		},
	})
}

func (a *App) generationHandoffStatus(c *ui.Context) {
	s := &a.integrations
	if s.Handoff.Handoff == nil {
		return
	}
	row := s.Handoff.Handoff
	label := map[string]string{"queued": "切换已登记", "preparing": "正在准备新模型", "stopping": "正在停止旧任务并核对结果", "applying": "正在切换模型", "applied": "已切换，剩余会话继续提炼", "not_applied": "未切换，仍使用原模型", "unknown": "切换结果待核对"}[row.Status]
	if label == "" {
		return
	}
	if row.Status == "not_applied" && row.Reason == "generation_provider_unavailable" {
		label = "新模型服务无法连接，仍使用原模型"
	}
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		homeText(c, label, homeMeta)
		if row.Status == "unknown" {
			textButton(c, "核对结果", func() { a.pollGenerationHandoff(row.Operation) }).Disabled(s.Busy)
		}
	})
}
func (a *App) startGenerationHandoff() {
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
		a.Update(func() {
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
func (a *App) pollGenerationHandoff(operation string) {
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
			a.Update(func() { s.Handoff = snapshot })
			if value.Handoff.Status == "applied" || value.Handoff.Status == "not_applied" || value.Handoff.Status == "unknown" {
				break
			}
			time.Sleep(time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.Update(func() {
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
			}
		})
	}()
}

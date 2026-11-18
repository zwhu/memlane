package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
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
	s.HandoffMode = ""
	s.HandoffRef = nil
	s.HandoffChoice = GenerationRef{}
	if ref != nil {
		chosen := *ref
		s.HandoffRef = &chosen
		s.HandoffChoice = chosen
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
				s.Error = "无法读取待整理数量"
				return
			}
			s.Handoff = value
			s.HandoffReady = true
			s.Error = ""
		})
	}()
}
func (a *App) generationHandoffModal(c *ui.Context) {
	s := &a.integrations
	if !s.HandoffOpen {
		return
	}
	modal := ui.Modal(c.Key("generation-handoff-modal"), &s.HandoffOpen, func() {
		title := "更换处理模型"
		if s.HandoffMode == "agent_bridge" {
			title = "切换到 Agent 桥接"
		} else if s.HandoffMode == "provider" {
			title = "切换到 Provider"
		}
		homeText(c, title, homeHeading).FontSize(18)
		homeText(c, "当前："+generationLabel(s.Generation, s.Generation.Active), homeMeta)
		refs := []GenerationRef{}
		for id, rows := range s.Generation.Profiles {
			if len(rows) == 0 {
				continue
			}
			if s.HandoffMode != "" && rows[len(rows)-1].Profile.Mode != s.HandoffMode {
				continue
			}
			ref := GenerationRef{ID: id, Revision: rows[len(rows)-1].Revision}
			if s.Generation.validated(&ref) && (s.Generation.Active == nil || *s.Generation.Active != ref) {
				refs = append(refs, ref)
			}
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
		ui.Scroll(c.Key("handoff-model-options")).FillWidth().MaxHeight(260).Children(func() {
			ui.RadioGroup(c, func() {
				for _, ref := range refs {
					ref := ref
					label := generationLabel(s.Generation, &ref)
					ui.Radio(c, &s.HandoffChoice, ref, label).Disabled(s.Busy).OnChange(func() { chosen := ref; s.HandoffRef = &chosen })
				}
			}).Label("选择处理模型").Gap(12)
			if len(refs) == 0 {
				homeText(c, "暂无其他已验证的模型", homeMeta)
			}
		})
		if s.HandoffMode != "" {
			textButton(c, "管理此方式的配置", func() { s.HandoffOpen = false; a.openGenerationModeConfiguration(s.HandoffMode) })
		}
		if s.HandoffLoading {
			homeText(c, "正在读取待整理数量…", homeMeta)
		} else {
			homeText(c, fmt.Sprintf("待整理 %d 个会话", s.Handoff.Pending), homeBody)
			if s.Handoff.Running > 0 {
				homeText(c, fmt.Sprintf("正在整理 %d 个会话", s.Handoff.Running), homeMeta)
			}
			if s.Handoff.Unknown > 0 {
				homeText(c, fmt.Sprintf("另有 %d 个会话等待核对结果", s.Handoff.Unknown), homeMeta)
			}
		}
		homeText(c, "剩余会话将使用新模型整理，并消耗相应额度。", homeMeta)
		if s.Error != "" {
			homeText(c, s.Error, homeMeta).MaxLines(3)
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(12).Children(func() {
			textButton(c, "取消", func() { s.HandoffOpen = false }).Disabled(s.Busy)
			ui.Button(c, "切换并接续").Disabled(s.Busy || !s.HandoffReady || s.HandoffLoading || s.HandoffRef == nil ||
				(s.HandoffRef != nil && s.Generation.Active != nil && *s.HandoffRef == *s.Generation.Active)).OnClick(a.startGenerationHandoff)
		})
	}).Width(500).Padding(24).Gap(16)
	if modal.Dismissed() {
		s.HandoffOpen = false
	}
}
func (a *App) generationHandoffStatus(c *ui.Context) {
	s := &a.integrations
	if s.Handoff.Handoff == nil {
		return
	}
	row := s.Handoff.Handoff
	label := map[string]string{"queued": "切换已登记", "preparing": "正在准备新模型", "stopping": "正在停止旧任务并核对结果", "applying": "正在切换模型", "applied": "已切换，剩余会话继续整理", "not_applied": "未切换，仍使用原模型", "unknown": "切换结果待核对"}[row.Status]
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
	if !ok || s.Busy || !s.HandoffReady || s.HandoffLoading || s.HandoffRef == nil || !s.Generation.validated(s.HandoffRef) {
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

func (a *App) openGenerationModeConfiguration(mode string) {
	s := &a.integrations
	s.ModeConfiguration = mode
	s.ModeConfigurationOpen = true
}
func (a *App) generationModeConfigurationModal(c *ui.Context) {
	s := &a.integrations
	if !s.ModeConfigurationOpen {
		return
	}
	ui.Modal(c.Key("generation-mode-configuration"), &s.ModeConfigurationOpen, func() {
		title := "Provider 配置"
		if s.ModeConfiguration == "agent_bridge" {
			title = "Agent 桥接配置"
		}
		homeText(c, title, homeHeading).FontSize(18)
		for _, group := range generationProfileGroups(s.Generation) {
			if (group.Key == "agent") != (s.ModeConfiguration == "agent_bridge") {
				continue
			}
			for _, item := range group.Items {
				item := item
				textButton(c, item.Title, func() { s.ModeConfigurationOpen = false; s.editProfile(item.ID, item.Row) })
			}
		}
		textButton(c, "添加配置", func() {
			s.ModeConfigurationOpen = false
			profile := GenerationProfile{Mode: "provider", Provider: "openai"}
			if s.ModeConfiguration == "agent_bridge" {
				profile = GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex"}
			}
			s.editProfile("api-"+integrationOperation(), GenerationRow{Profile: profile})
			s.MaxOutputTokens = ""
		})
		textButton(c, "关闭", func() { s.ModeConfigurationOpen = false })
	}).Width(500).Padding(24).Gap(16)
}

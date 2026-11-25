package overview

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *ControllerOf[T]) OpenFailureDetail(ev Event, loc *time.Location) {
	v := a.State
	var source DiagnosticSource
	if a.Diagnostics != nil {
		source = a.Diagnostics()
	}
	detail := BuildFailureDetail(ev, source, loc)
	report := reportForDetail(ev, detail)
	if v.ExpandedEvent != ev.Key {
		v.CopiedEvent = ""
	}
	v.ExpandedEvent = ev.Key
	v.ExpandedDetail = &detail
	v.ExpandedReport = report
	v.ExpandedTechOpen = false
}

func (a *ControllerOf[T]) CloseFailureDetail() {
	v := a.State
	v.ExpandedEvent = ""
	v.ExpandedDetail = nil
	v.ExpandedTechOpen = false
}

func (a *ControllerOf[T]) RenderFailureDialog(c *ui.Context) {
	tok := a.tokens()
	v := a.State
	if v.ExpandedEvent == "" || v.ExpandedDetail == nil {
		return
	}
	detail := v.ExpandedDetail
	open := true
	hint := "来自已有官方记录，不含凭据或会话正文"
	if v.CopiedEvent == v.ExpandedEvent {
		hint = "已复制诊断信息 · 不含凭据或会话正文"
	}
	kitui.Dialog(c, tok, &open, kitui.DialogOptions{
		Key:         "overview-failure-detail",
		Title:       detail.DialogTitle,
		Label:       "任务失败详情",
		Width:       kitui.DialogLarge,
		ScrollBody:  true,
		Leading:     func() { kitui.StatusDot(c, tok, "fail") },
		Hint:        hint,
		CancelText:  "关闭",
		ConfirmText: "复制诊断信息",
		OnCancel:    a.CloseFailureDetail,
		OnConfirm: func() {
			c.WriteClipboard(v.ExpandedReport)
			v.CopiedEvent = v.ExpandedEvent
		},
		Body: func() {
			ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp2).Background(tok.Fill1).Radius(radius).Children(func() {
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					kitui.StatusDot(c, tok, "fail")
					ui.Text(c, detail.ReasonLabel).Font("system-ui").FontSize(fsBody).FontWeight(600).TextColor(tok.Danger).Grow(1).MinWidth(0)
					if detail.ErrorClass != "" {
						ui.Text(c, detail.ErrorClass).Font("ui-monospace").FontSize(fsCaption).FontWeight(500).
							Padding(2, 6).Radius(radiusSmall).Background(tok.Fill2).TextColor(tok.Text2).SingleLine().Shrink(0)
					}
				})
				if detail.Summary != "" {
					ui.Text(c, detail.Summary).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).LineHeight(1.55)
				}
				if detail.Hint != "" {
					ui.Text(c, detail.Hint).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).LineHeight(1.5)
				}
			})

			kitui.Group(c, tok, "概览", "", nil, func() {
				if detail.Project != "" {
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-proj", Title: "所属项目", Value: detail.Project})
				}
				if detail.OperationTitle != "" {
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-op", Title: "任务操作", Value: detail.OperationTitle})
				}
				if detail.StageLabel != "" {
					stageDesc := ""
					if detail.StageRaw != "" && detail.StageRaw != detail.StageLabel {
						stageDesc = detail.StageRaw
					}
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-stage", Title: "失败环节", Desc: stageDesc, DescMono: stageDesc != "", Value: detail.StageLabel})
				}
				if detail.LocalTime != "" {
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-time", Title: "发生时间", Value: detail.LocalTime, ValueMono: true})
				}
				if detail.Duration != "" {
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-dur", Title: "持续耗时", Value: detail.Duration})
				}
				if detail.FailureCount > 1 {
					kitui.Row(c, tok, kitui.RowOptions{Key: "fail-count", Title: "失败次数", Value: fmt.Sprintf("%d 次", detail.FailureCount), ValueColor: tok.Danger})
				}
			})

			if len(detail.Items) > 1 {
				kitui.Group(c, tok, "失败记录", fmt.Sprintf("共 %d 条", len(detail.Items)), nil, func() {
					for idx, item := range detail.Items {
						metaParts := []string{}
						if item.LocalTime != "" {
							metaParts = append(metaParts, item.LocalTime)
						}
						if item.Duration != "" {
							metaParts = append(metaParts, "耗时 "+item.Duration)
						}
						if item.ErrorClass != "" {
							metaParts = append(metaParts, item.ErrorClass)
						}
						kitui.Row(c, tok, kitui.RowOptions{
							Key:        fmt.Sprintf("fail-item-%d", idx),
							Title:      item.StageLabel,
							Desc:       strings.Join(metaParts, " · "),
							DescMono:   true,
							Leading:    func() { kitui.StatusDot(c, tok, "fail") },
							Value:      item.ReasonLabel,
							ValueColor: tok.Danger,
						})
					}
				})
			}

			techDesc := "包含记录 ID、运行 ID、UTC 时间与官方服务信息"
			if detail.HasLogCandidate {
				techDesc = "包含记录 ID、运行 ID、UTC 时间及相邻日志调用位置"
			}
			kitui.Group(c, tok, "技术信息", "", nil, func() {
				title := "展开技术信息"
				ico := kitui.IconChev
				if v.ExpandedTechOpen {
					title = "收起技术信息"
					ico = kitui.IconDown
				}
				kitui.Row(c, tok, kitui.RowOptions{
					Key:         "overview-failure-tech-toggle",
					Title:       title,
					Desc:        techDesc,
					Clickable:   true,
					HideChevron: true,
					Control:     func() { ui.Icon(c, ico).FontSize(14).TextColor(tok.Text3).Shrink(0) },
					OnClick:     func() { v.ExpandedTechOpen = !v.ExpandedTechOpen },
				})
				if !v.ExpandedTechOpen {
					return
				}
				ui.Column(c.Key("overview-failure-tech-body")).FillWidth().Padding(sp3).Gap(sp2).Background(tok.Fill1).Radius(radius).Children(func() {
					metaLines := []string{}
					if detail.EventKey != "" {
						metaLines = append(metaLines, "事件 ID："+detail.EventKey)
					}
					if detail.ServiceEndpoint != "" {
						metaLines = append(metaLines, "官方服务："+detail.ServiceEndpoint)
					}
					if detail.ServiceCommit != "" {
						metaLines = append(metaLines, "Cognee 源码版本："+detail.ServiceCommit)
					}
					if detail.EventUTC != "" {
						metaLines = append(metaLines, "事件时间（UTC）："+detail.EventUTC)
					}
					if detail.LogDir != "" {
						metaLines = append(metaLines, "本机日志目录："+detail.LogDir)
					}
					if len(metaLines) > 0 {
						ui.Text(c, strings.Join(metaLines, "\n")).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text2).LineHeight(1.55)
					}
					if len(detail.TechRecords) == 0 {
						ui.Text(c, "官方未返回失败详情。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
					}
					for _, rec := range detail.TechRecords {
						lines := []string{}
						stage := rec.StageLabel
						if rec.StageRaw != "" && rec.StageRaw != rec.StageLabel {
							stage = fmt.Sprintf("%s (%s)", rec.StageLabel, rec.StageRaw)
						}
						lines = append(lines, "阶段："+stage)
						if rec.ErrorClass != "" {
							lines = append(lines, "错误类型："+rec.ErrorClass)
						}
						if rec.IsParentWrapper {
							lines = append(lines, "说明：外层任务（由子阶段错误触发，未附额外错误类型）")
						} else {
							lines = append(lines, "说明："+rec.ReasonLabel)
						}
						if rec.ID != "" {
							lines = append(lines, "记录 ID："+rec.ID)
						}
						if rec.Run != "" {
							lines = append(lines, "运行 ID："+rec.Run)
						}
						if rec.Parent != "" {
							lines = append(lines, "父操作 ID："+rec.Parent)
						}
						if rec.Dataset != "" {
							lines = append(lines, "数据组："+rec.Dataset)
						}
						if rec.Origin != "" {
							lines = append(lines, "来源："+rec.Origin)
						}
						if rec.CreatedUTC != "" {
							lines = append(lines, "记录时间（UTC）："+rec.CreatedUTC)
						}
						if rec.StartedUTC != "" {
							lines = append(lines, "开始（UTC）："+rec.StartedUTC)
						}
						if rec.EndedUTC != "" {
							lines = append(lines, "结束（UTC）："+rec.EndedUTC)
						}
						if rec.LogCandidate != "" {
							lines = append(lines, "日志候选（尚未按任务 ID 精确确认）：\n"+rec.LogCandidate)
						}
						ui.Column(c).FillWidth().Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, strings.Join(lines, "\n")).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text2).LineHeight(1.55)
						})
					}
					if detail.BoundaryNote != "" {
						ui.Text(c, detail.BoundaryNote).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5).Padding(sp2, 0, 0, 0)
					}
				})
			})
		},
	})
	if !open {
		a.CloseFailureDetail()
	}
}

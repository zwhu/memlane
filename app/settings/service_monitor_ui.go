package settings

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) serviceMonitorAlerts(c *ui.Context) {
	tok := a.tokens()
	m := &a.monitor
	if m.State == "exited" || m.State == "degraded" || m.State == "warning" {
		banner := m.alertText()
		color := tok.Danger
		if m.State == "warning" {
			color = tok.Warning
		}
		ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
			kitui.Text(c, tok, banner, kitui.RoleMeta).TextColor(color).LineHeight(1.5)
		})
	}
	if m.Snapshot != nil && m.Snapshot.Summary != nil {
		summary := m.Snapshot.Summary
		if len(summary.HookErrors) > 0 {
			for _, he := range summary.HookErrors {
				ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
					kitui.Text(c, tok, formatMonitorHookErrorLine(he), kitui.RoleMeta).TextColor(tok.Warning).LineHeight(1.5)
				})
			}
		} else if summary.HookErrorsCount > 0 {
			ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
				kitui.Text(c, tok, fmt.Sprintf("Hook 未记录（%d 项）：请展开「Agent 连接」核对对应 Agent 状态。", summary.HookErrorsCount), kitui.RoleMeta).TextColor(tok.Warning).LineHeight(1.5)
			})
		}
		if summary.FilteredCount > 0 || len(summary.RecentFiltered) > 0 {
			kitui.Row(c, tok, kitui.RowOptions{Key: "svc-filtered", Title: "因采集规则未采集",
				Desc:    filteredCaptureDescription(summary.RecentFiltered),
				Leading: func() { kitui.StatusDot(c, tok, "warn") }})
		}
	}
}

func (a *Controller) serviceMonitorDetails(c *ui.Context) {
	tok := a.tokens()
	m := &a.monitor
	expanded := m.Expanded || m.State == "degraded" || m.State == "exited"
	logCount := 0
	if m.Snapshot != nil {
		logCount = len(m.Snapshot.LogTail)
	}
	diagDesc := "日志默认收起，已脱敏"
	if m.CopiedNotice != "" {
		diagDesc = m.CopiedNotice
	}
	kitui.Group(c, tok, "诊断", diagDesc, func() {
		kitui.SmallButton(c, tok, "复制诊断", kitui.IconCopy, func() {
			c.WriteClipboard(a.ServiceDiagnosticText())
			m.CopiedNotice = "已复制诊断信息"
		}).Label("复制服务诊断")
	}, func() {
		rowDesc := fmt.Sprintf("最近 %d 条记录 · 点击展开详情", logCount)
		if expanded {
			rowDesc = fmt.Sprintf("最近 %d 条记录 · 点击收起详情", logCount)
		}
		ui.Column(c.Key("svc-diag-wrap")).FillWidth().Gap(0).Children(func() {
			kitui.Row(c, tok, kitui.RowOptions{
				Key:         "svc-diag-toggle",
				Title:       "服务日志与诊断信息",
				Desc:        rowDesc,
				Clickable:   true,
				HideChevron: true,
				Control: func() {
					ico := kitui.IconChev
					if expanded {
						ico = kitui.IconDown
					}
					ui.Icon(c, ico).FontSize(14).TextColor(tok.Text3).Shrink(0)
				},
				OnClick: func() {
					m.Expanded = !m.Expanded
					if m.Expanded {
						a.refreshServiceMonitor()
					}
				},
			})
			if !expanded {
				return
			}
			ui.Column(c.Key("svc-diag-panel")).FillWidth().Padding(0, sp4, sp4, sp4).Gap(sp2).Children(func() {
				if m.LastError != "" && m.State != "ready" {
					kitui.Text(c, tok, "最近技术错误："+m.LastError, kitui.RoleMeta).LineHeight(1.5)
				}
				if m.Snapshot == nil || len(m.Snapshot.LogTail) == 0 {
					kitui.Text(c, tok, "暂无服务日志记录。", kitui.RoleMeta)
					return
				}
				lines := m.Snapshot.LogTail
				if len(lines) > 8 {
					lines = lines[len(lines)-8:]
				}
				heading := "最近服务动态（已脱敏）："
				toggleLabel := "查看原始日志"
				if m.ShowRawLogs {
					heading = "最近服务原始日志（已脱敏）："
					toggleLabel = "查看可读摘要"
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					kitui.Text(c, tok, heading, kitui.RoleMeta).Grow(1).MinWidth(0)
					kitui.SmallButton(c, tok, toggleLabel, nil, func() { m.ShowRawLogs = !m.ShowRawLogs })
				})
				ui.Column(c).FillWidth().Padding(sp3).Gap(3).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
					for _, line := range lines {
						if m.ShowRawLogs {
							ui.Text(c, stripANSIEscapes(line)).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text2).LineHeight(1.55)
							continue
						}
						display, level := formatServiceLogLine(line)
						if display == "" {
							continue
						}
						textCol := tok.Text2
						switch level {
						case "error":
							textCol = tok.Danger
						case "warning":
							textCol = tok.Warning
						}
						ui.Text(c, display).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(textCol).LineHeight(1.55)
					}
				})
			})
		})
	})
}

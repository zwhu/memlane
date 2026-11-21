package simpledesktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *App) serviceMonitorAlerts(c *ui.Context) {
	m := &a.monitor
	if m.State == "exited" || m.State == "degraded" {
		banner := "服务连续健康检查失败，不会自动重启，请核对原因后手动重启服务。"
		if m.State == "exited" {
			banner = "Cognee 进程已退出，服务未自动重启，请核对原因后手动重启服务。"
		}
		ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			homeText(c, banner, homeMeta).TextColor(tok.Danger).LineHeight(1.5)
		})
	} else if m.State == "warning" {
		ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			homeText(c, fmt.Sprintf("检测到短暂健康波动（连续失败 %d/3 次），正在继续观测。", m.ConsecutiveFailures), homeMeta).TextColor(tok.Warning)
		})
	}
	if m.Snapshot != nil && m.Snapshot.Summary != nil {
		if len(m.Snapshot.Summary.HookErrors) > 0 {
			for _, he := range m.Snapshot.Summary.HookErrors {
				ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					homeText(c, formatMonitorHookErrorLine(he), homeMeta).TextColor(tok.Warning).LineHeight(1.5)
				})
			}
		} else if m.Snapshot.Summary.HookErrorsCount > 0 {
			ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				homeText(c, fmt.Sprintf("Hook 未记录（%d 项）：请展开「Agent 与模型」核对对应 Agent 状态。", m.Snapshot.Summary.HookErrorsCount), homeMeta).TextColor(tok.Warning).LineHeight(1.5)
			})
		}
	}
	if m.LastError != "" && m.State != "ready" {
		ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			homeText(c, "最近错误："+m.LastError, homeMeta).TextColor(tok.Danger).LineHeight(1.5)
		})
	}
}

func (a *App) serviceMonitorDetails(c *ui.Context) {
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
	v4Group(c, "诊断", diagDesc, func() {
		v4SmallButton(c, "复制诊断", v4IconCopy, func() {
			c.WriteClipboard(a.serviceDiagnosticText())
			m.CopiedNotice = "已复制诊断信息"
		}).Label("复制服务诊断")
	}, func() {
		rowDesc := fmt.Sprintf("最近 %d 条记录 · 点击展开详情", logCount)
		if expanded {
			rowDesc = fmt.Sprintf("最近 %d 条记录 · 点击收起详情", logCount)
		}
		v4Row(c, v4RowOptions{
			Key:         "svc-diag-toggle",
			Title:       "服务日志与诊断信息",
			Desc:        rowDesc,
			Clickable:   true,
			HideChevron: true,
			Control: func() {
				ico := v4IconChev
				if expanded {
					ico = v4IconDown
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
		ui.Column(c.Key("svc-diag-panel")).FillWidth().Padding(sp3, sp1, sp4, sp1).Gap(sp2).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			if m.Snapshot == nil || len(m.Snapshot.LogTail) == 0 {
				homeText(c, "暂无服务日志记录。", homeMeta)
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
				homeText(c, heading, homeMeta).Grow(1).MinWidth(0)
				v4SmallButton(c, toggleLabel, nil, func() { m.ShowRawLogs = !m.ShowRawLogs })
			})
			ui.Column(c).FillWidth().Padding(sp3).Gap(3).Background(tok.Fill1).Radius(radius).Children(func() {
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
}

package simpledesktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) serviceMonitorDetails(c *ui.Context) {
	m := &a.monitor
	if !m.Expanded && m.State != "degraded" && m.State != "exited" {
		return
	}
	settingsListDivider(c)
	ui.Column(c).FillWidth().Padding(10, 0, 4, 0).Gap(8).Children(func() {
		if m.State == "exited" || m.State == "degraded" {
			banner := "服务连续健康检查失败，不会自动重启，请核对原因后手动重启服务。"
			if m.State == "exited" {
				banner = "Cognee 进程已退出，服务未自动重启，请核对原因后手动重启服务。"
			}
			homeText(c, banner, homeMeta).TextColor(tok.Danger).LineHeight(1.5)
		} else if m.State == "warning" {
			homeText(c, fmt.Sprintf("检测到短暂健康波动（连续失败 %d/3 次），正在继续观测。", m.ConsecutiveFailures), homeMeta).TextColor(tok.Warning)
		}

		var cogneeParts []string
		if m.HasLatency {
			cogneeParts = append(cogneeParts, fmt.Sprintf("延迟 %.0f ms", m.LastLatencyMs))
		}
		if m.ConsecutiveFailures > 0 {
			cogneeParts = append(cogneeParts, fmt.Sprintf("连续失败 %d 次", m.ConsecutiveFailures))
		}
		if m.Snapshot != nil {
			if proc, ok := m.Snapshot.Processes["official"]; ok {
				if proc.PID > 0 {
					cogneeParts = append(cogneeParts, fmt.Sprintf("PID %d", proc.PID))
				}
				if proc.RSSBytes != nil {
					cogneeParts = append(cogneeParts, "内存 "+formatBytesMB(*proc.RSSBytes))
				}
			}
		}
		if len(cogneeParts) > 0 {
			homeText(c, "Cognee 指标："+strings.Join(cogneeParts, " · "), homeMeta)
		}

		if m.ControlChecked {
			controlState := "已连接"
			if !m.ControlReachable {
				controlState = "异常"
			}
			var ctrlParts = []string{"控制面：" + controlState}
			if m.Snapshot != nil {
				if proc, ok := m.Snapshot.Processes["project_control"]; ok && proc.RSSBytes != nil {
					ctrlParts = append(ctrlParts, "内存 "+formatBytesMB(*proc.RSSBytes))
				}
				if m.Snapshot.Summary != nil {
					s := m.Snapshot.Summary
					ctrlParts = append(ctrlParts, fmt.Sprintf("项目 %d 个", s.ProjectsCount))
					if s.RecentProcessingMs != nil {
						ctrlParts = append(ctrlParts, fmt.Sprintf("最近请求 %.0f ms", *s.RecentProcessingMs))
					}
					if s.HookErrorsCount > 0 {
						ctrlParts = append(ctrlParts, fmt.Sprintf("Hook 未记录 %d 项", s.HookErrorsCount))
					}
					if s.GenerationReceiptVerified != nil {
						if *s.GenerationReceiptVerified {
							ctrlParts = append(ctrlParts, "配置回执一致")
						} else {
							ctrlParts = append(ctrlParts, "配置回执待核对")
						}
					}
				}
			}
			homeText(c, strings.Join(ctrlParts, " · "), homeMeta)
		}

		if m.Snapshot != nil && m.Snapshot.Summary != nil {
			if len(m.Snapshot.Summary.HookErrors) > 0 {
				for _, he := range m.Snapshot.Summary.HookErrors {
					homeText(c, formatMonitorHookErrorLine(he), homeMeta).LineHeight(1.5)
				}
			} else if m.Snapshot.Summary.HookErrorsCount > 0 {
				homeText(c, fmt.Sprintf("Hook 未记录（%d 项）：请展开「Agent 与模型」核对对应 Agent 状态。", m.Snapshot.Summary.HookErrorsCount), homeMeta).LineHeight(1.5)
			}
		}

		if m.Snapshot != nil && m.Snapshot.Disk != nil {
			diskText := "磁盘可用空间：" + formatBytesGiB(m.Snapshot.Disk.FreeBytes)
			if m.Snapshot.Disk.Low {
				diskText += "（低于 55 GiB 预警线）"
			}
			text := homeText(c, diskText, homeMeta)
			if m.Snapshot.Disk.Low {
				text.TextColor(tok.Danger)
			}
		}

		if m.LastError != "" && m.State != "ready" {
			homeText(c, "最近错误："+m.LastError, homeMeta)
		}

		if m.Snapshot != nil && len(m.Snapshot.LogTail) > 0 && (m.State == "exited" || m.State == "degraded" || m.Expanded) {
			lines := m.Snapshot.LogTail
			if len(lines) > 8 {
				lines = lines[len(lines)-8:]
			}
			heading := "最近服务动态（已脱敏）："
			if m.ShowRawLogs {
				heading = "最近服务原始日志（已脱敏）："
			}
			homeText(c, heading, homeMeta)
			ui.Column(c).FillWidth().Padding(8, 10).Gap(3).Background(tok.Fill1).Radius(6).Children(func() {
				for _, line := range lines {
					if m.ShowRawLogs {
						homeText(c, stripANSIEscapes(line), homeMeta).FontSize(11)
						continue
					}
					display, level := formatServiceLogLine(line)
					if display == "" {
						continue
					}
					text := homeText(c, display, homeMeta).FontSize(11).LineHeight(1.4)
					switch level {
					case "error":
						text.TextColor(tok.Danger)
					case "warning":
						text.TextColor(tok.Warning)
					}
				}
			})
		}

		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			textButton(c, "复制诊断", func() {
				c.WriteClipboard(a.serviceDiagnosticText())
				m.CopiedNotice = "已复制诊断信息"
			}).Label("复制服务诊断")
			if m.Snapshot != nil && len(m.Snapshot.LogTail) > 0 && (m.State == "exited" || m.State == "degraded" || m.Expanded) {
				toggleLabel := "查看原始日志"
				if m.ShowRawLogs {
					toggleLabel = "查看可读摘要"
				}
				textButton(c, toggleLabel, func() { m.ShowRawLogs = !m.ShowRawLogs })
			}
			if m.CopiedNotice != "" {
				homeText(c, m.CopiedNotice, homeMeta)
			}
		})
	})
}

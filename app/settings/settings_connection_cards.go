package settings

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// Settings groups share the same surface, inset and divider treatment.
func settingsListSurface(tok *kitui.Tokens, c *ui.Context, content func()) {
	ui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(content)
}

func settingsSectionHeader(tok *kitui.Tokens, c *ui.Context, title string, status, actions func()) {
	ui.Row(c).FillWidth().Padding(0, sp1, sp2, sp1).AlignItems(ui.Center).Gap(sp2).Children(func() {
		ui.Text(c, title).Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
		if status != nil {
			status()
		}
		ui.Spacer(c)
		if actions != nil {
			ui.Row(c).AlignItems(ui.Center).Gap(sp2).Shrink(0).Children(actions)
		}
	})
}

func (a *Controller) ViewServiceOverview(c *ui.Context) {
	tok := a.tokens()
	m := &a.monitor
	checkedDesc := ""
	if !m.LastCheckedAt.IsZero() {
		checkedDesc = "更新于 " + m.LastCheckedAt.Local().Format("15:04:05")
	}

	kitui.Group(c, tok, "组件状态", checkedDesc, func() {
		kitui.SmallButton(c, tok, "重新检查", kitui.IconRefresh, a.health).Disabled(m.Polling)
	}, func() {
		if m.Polling && m.Snapshot == nil && m.LastCheckedAt.IsZero() {
			kitui.SkeletonGroup(c, tok, 3, 2, false, true)
			return
		}

		setupOnly := a.isSetupOnlyRuntime() || a.monitor.CompletionStatus == SetupOnlyCompletion
		hasChannelRow := setupOnly || a.config.CompletionURL != "" || (m.Snapshot != nil && m.Snapshot.Processes["completion"].PID > 0)

		// 1) 记忆引擎（Cognee）服务行
		cogneeState, cogneeStatus, cogneeColor := a.ServiceCogneeStatus()
		var cogneeParts []string
		if addr := SanitizedMonitorURL(a.config.ServiceURL); addr != "" {
			cogneeParts = append(cogneeParts, addr)
		}
		if m.HasLatency {
			cogneeParts = append(cogneeParts, "延迟 "+FormatDurationCompact(m.LastLatencyMs))
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
					cogneeParts = append(cogneeParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
				}
			}
			if !hasChannelRow && m.Snapshot.Summary != nil && m.Snapshot.Summary.RecentProcessingMs != nil {
				cogneeParts = append(cogneeParts, "最近模型生成 "+FormatDurationCompact(*m.Snapshot.Summary.RecentProcessingMs))
			}
		}
		kitui.Row(c, tok, kitui.RowOptions{
			Key:        "svc-row-cognee",
			Leading:    func() { kitui.StatusDot(c, tok, cogneeState) },
			Title:      "记忆引擎（Cognee）",
			Desc:       strings.Join(cogneeParts, " · "),
			DescMono:   true,
			Value:      cogneeStatus,
			ValueMono:  true,
			ValueColor: cogneeColor,
		})

		// 2) 接入网关服务行
		if m.ControlChecked || m.Snapshot != nil {
			ctrlDot, ctrlVal, ctrlCol := "ok", "接入网关：已连接", tok.OK
			if m.ControlChecked && !m.ControlReachable {
				ctrlDot, ctrlVal, ctrlCol = "fail", "接入网关：异常", tok.Danger
			}
			var ctrlParts []string
			if m.ControlReachable && m.HasControlLatency {
				ctrlParts = append(ctrlParts, "延迟 "+FormatDurationCompact(m.ControlLatencyMs))
			}
			if m.Snapshot != nil {
				if proc, ok := m.Snapshot.Processes["project_control"]; ok && proc.RSSBytes != nil {
					ctrlParts = append(ctrlParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
				}
				if m.Snapshot.Summary != nil {
					s := m.Snapshot.Summary
					ctrlParts = append(ctrlParts, fmt.Sprintf("项目 %d 个", s.ProjectsCount))
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
			kitui.Row(c, tok, kitui.RowOptions{
				Key:        "svc-row-control",
				Leading:    func() { kitui.StatusDot(c, tok, ctrlDot) },
				Title:      "接入网关",
				Desc:       strings.Join(ctrlParts, " · "),
				DescMono:   true,
				Value:      ctrlVal,
				ValueMono:  true,
				ValueColor: ctrlCol,
			})
		}

		// 3) Agent 模型代理行
		if hasChannelRow {
			compDot, compVal, compCol := "neutral", "待检查", tok.Text3
			if setupOnly {
				compVal = "待配置提炼"
			} else if strings.HasPrefix(a.monitor.CompletionStatus, "端点已连接") {
				compDot, compVal, compCol = "ok", "已连接", tok.OK
			} else if a.monitor.CompletionStatus != "" && a.monitor.CompletionStatus != "未配置" {
				compDot, compVal, compCol = "warn", "待检查", tok.Warning
			}
			var compParts []string
			if u := SanitizedMonitorURL(a.config.CompletionURL); u != "" && u != "未配置" {
				compParts = append(compParts, u)
			}
			if strings.HasPrefix(a.monitor.CompletionStatus, "端点已连接") && m.HasCompletionLatency {
				compParts = append(compParts, "延迟 "+FormatDurationCompact(m.CompletionLatencyMs))
			}
			if m.Snapshot != nil {
				if proc, ok := m.Snapshot.Processes["completion"]; ok && proc.RSSBytes != nil {
					compParts = append(compParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
				}
				if m.Snapshot.Summary != nil && m.Snapshot.Summary.RecentProcessingMs != nil {
					compParts = append(compParts, "最近模型生成 "+FormatDurationCompact(*m.Snapshot.Summary.RecentProcessingMs))
				}
			}
			kitui.Row(c, tok, kitui.RowOptions{
				Key:        "svc-row-channel",
				Leading:    func() { kitui.StatusDot(c, tok, compDot) },
				Title:      "Agent 模型代理",
				Desc:       strings.Join(compParts, " · "),
				DescMono:   true,
				Value:      compVal,
				ValueMono:  true,
				ValueColor: compCol,
			})
		}

		// 4) 磁盘空间行
		if m.Snapshot != nil && m.Snapshot.Disk != nil {
			diskDot, diskCol := "ok", tok.Text2
			diskDesc := "预警线 55 GiB · 安全线 50 GiB"
			if m.Snapshot.Disk.Low {
				diskDot, diskCol = "fail", tok.Danger
				diskDesc = "低于 55 GiB 预警线，请及时清理磁盘空间"
			}
			kitui.Row(c, tok, kitui.RowOptions{
				Key:        "svc-row-disk",
				Leading:    func() { kitui.StatusDot(c, tok, diskDot) },
				Title:      "磁盘可用空间",
				Desc:       diskDesc,
				DescMono:   true,
				Value:      FormatBytesGiB(m.Snapshot.Disk.FreeBytes),
				ValueMono:  true,
				ValueColor: diskCol,
			})
		}

		// 异常横幅与 Hook 错误明细
		a.serviceMonitorAlerts(c)
	})

	// 诊断折叠区（Q6：默认收起，SF Mono + fill-1，带复制诊断）
	a.serviceMonitorDetails(c)
}

func (a *Controller) ServiceCogneeStatus() (dotState, status string, color ui.Color) {
	tok := a.tokens()
	status = a.monitor.EngineStatus
	color = tok.Text3
	dotState = "neutral"
	switch a.monitor.State {
	case "ready":
		return "ok", "已连接", tok.OK
	case "warning":
		return "warn", "短暂波动", tok.Warning
	case "exited":
		return "fail", "服务已退出", tok.Danger
	case "degraded":
		return "fail", "响应异常", tok.Danger
	default:
		if a.monitor.EngineStatus == "已连接" {
			return "ok", "已连接", tok.OK
		}
		if a.monitor.EngineStatus != "" && a.monitor.EngineStatus != "尚未检查" {
			return "fail", a.monitor.EngineStatus, tok.Danger
		}
	}
	return dotState, status, color
}

// All rows share one stable surface; expanding one Agent never stretches others.
func (a *Controller) agentConnectionGrid(c *ui.Context, rows []AgentConnection) {
	if len(rows) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(0).Children(func() {
		for _, row := range rows {
			a.agentConnectionRow(c, row)
		}
	})
}

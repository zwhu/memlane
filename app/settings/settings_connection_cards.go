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

// ServiceComponentView is the unified presentation model shared by the sidebar
// status popover, Settings › Service, and the macOS menu bar flyout.
type ServiceComponentView struct {
	ID             string
	Name           string
	DotState       string
	StatusText     string
	StatusColor    ui.Color
	CompactStatus  string
	CompactMetrics string
	DetailMetrics  string
}

// serviceStatusColor enforces V4 §3.2: ok green is reserved strictly for status
// dots/icons; healthy status text uses neutral Text2 across all status surfaces.
func serviceStatusColor(tok *kitui.Tokens, dotState string) ui.Color {
	switch dotState {
	case "ok":
		return tok.Text2
	case "run":
		return tok.Accent
	case "warn":
		return tok.Warning
	case "fail":
		return tok.Danger
	default:
		return tok.Text3
	}
}

func FormatDiskThreshold(limitBytes int64, fallback string) string {
	const gib = 1024 * 1024 * 1024
	if limitBytes <= 0 {
		return fallback
	}
	if limitBytes%gib == 0 {
		return fmt.Sprintf("%d GiB", limitBytes/gib)
	}
	return FormatBytesCompact(limitBytes)
}

func completionSourceLabel(m ServiceMonitorState) string {
	provider := strings.TrimSpace(m.CompletionProvider)
	if provider == "" {
		return ""
	}
	label := connectionName(provider)
	if model := strings.TrimSpace(m.CompletionModel); model != "" {
		return label + " · " + model
	}
	if m.CompletionAgentDefault {
		return label + " 默认"
	}
	return label
}

func (a *Controller) hasChannelComponent() bool {
	setupOnly := a.isSetupOnlyRuntime() || a.monitor.CompletionStatus == SetupOnlyCompletion
	completionURL := a.EffectiveCompletionURL()
	return setupOnly || completionURL != "" || (a.monitor.Snapshot != nil && a.monitor.Snapshot.Processes["completion"].PID > 0)
}

func (a *Controller) ServiceCogneeComponent() ServiceComponentView {
	m := &a.monitor
	dot, status, col := a.ServiceCogneeStatus()
	var latencyMs float64
	hasLatency := false
	if m.HasLatency {
		latencyMs, hasLatency = m.LastLatencyMs, true
	} else if m.Snapshot != nil && m.Snapshot.LatencyMs != nil {
		latencyMs, hasLatency = *m.Snapshot.LatencyMs, true
	}
	compactStatus := status
	if dot == "ok" && hasLatency {
		compactStatus = fmt.Sprintf("%s · %s", status, FormatDurationCompact(latencyMs))
	}

	var compactParts []string
	if hasLatency {
		compactParts = append(compactParts, "延迟 "+FormatDurationCompact(latencyMs))
	}
	if m.ConsecutiveFailures > 0 {
		compactParts = append(compactParts, fmt.Sprintf("连续失败 %d 次", m.ConsecutiveFailures))
	}
	if m.Snapshot != nil {
		if proc, ok := m.Snapshot.Processes["official"]; ok {
			if proc.PID > 0 {
				compactParts = append(compactParts, fmt.Sprintf("PID %d", proc.PID))
			}
			if proc.RSSBytes != nil {
				compactParts = append(compactParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
			}
		}
	}

	detailParts := append([]string(nil), compactParts...)
	if !a.hasChannelComponent() && m.Snapshot != nil && m.Snapshot.Summary != nil && m.Snapshot.Summary.RecentProcessingMs != nil {
		detailParts = append(detailParts, "最近模型生成 "+FormatDurationCompact(*m.Snapshot.Summary.RecentProcessingMs))
	}
	if addr := SanitizedMonitorURL(a.config.ServiceURL); addr != "" && addr != "未配置" {
		detailParts = append(detailParts, addr)
	}

	return ServiceComponentView{
		ID:             "cognee",
		Name:           "记忆引擎（Cognee）",
		DotState:       dot,
		StatusText:     status,
		StatusColor:    col,
		CompactStatus:  compactStatus,
		CompactMetrics: strings.Join(compactParts, " · "),
		DetailMetrics:  strings.Join(detailParts, " · "),
	}
}

func (a *Controller) ServiceControlComponent() ServiceComponentView {
	tok := a.tokens()
	m := &a.monitor
	dot, status := "neutral", "待检查"
	switch {
	case a.config.ProjectControl == nil && !m.ControlChecked && m.Snapshot == nil:
		dot, status = "neutral", "未配置"
	case m.ControlChecked && !m.ControlReachable:
		dot, status = "fail", "异常"
	case m.ControlReachable || m.Snapshot != nil:
		dot, status = "ok", "已连接"
	}
	col := serviceStatusColor(tok, dot)
	compactStatus := status
	if dot == "ok" && m.HasControlLatency {
		compactStatus = "已连接 · " + FormatDurationCompact(m.ControlLatencyMs)
	}

	var compactParts []string
	if m.ControlReachable && m.HasControlLatency {
		compactParts = append(compactParts, "延迟 "+FormatDurationCompact(m.ControlLatencyMs))
	}
	if m.Snapshot != nil {
		proc, ok := m.Snapshot.Processes["project_control"]
		if !ok {
			proc, ok = m.Snapshot.Processes["control"]
		}
		if ok && proc.RSSBytes != nil {
			compactParts = append(compactParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
		}
		if m.Snapshot.Summary != nil {
			s := m.Snapshot.Summary
			compactParts = append(compactParts, fmt.Sprintf("项目 %d 个", s.ProjectsCount))
			if s.HookErrorsCount > 0 {
				compactParts = append(compactParts, fmt.Sprintf("Hook 未记录 %d 项", s.HookErrorsCount))
			}
		}
	}

	detailParts := append([]string(nil), compactParts...)
	if m.Snapshot != nil && m.Snapshot.Summary != nil && m.Snapshot.Summary.GenerationReceiptVerified != nil {
		if *m.Snapshot.Summary.GenerationReceiptVerified {
			detailParts = append(detailParts, "配置回执一致")
		} else {
			detailParts = append(detailParts, "配置回执待核对")
		}
	}
	if a.config.ProjectControl != nil {
		if addr := SanitizedMonitorURL(a.config.ProjectControl.URL); addr != "" && addr != "未配置" {
			detailParts = append(detailParts, addr)
		}
	}

	return ServiceComponentView{
		ID:             "control",
		Name:           "接入网关",
		DotState:       dot,
		StatusText:     status,
		StatusColor:    col,
		CompactStatus:  compactStatus,
		CompactMetrics: strings.Join(compactParts, " · "),
		DetailMetrics:  strings.Join(detailParts, " · "),
	}
}

func (a *Controller) ServiceChannelComponent() ServiceComponentView {
	tok := a.tokens()
	m := &a.monitor
	setupOnly := a.isSetupOnlyRuntime() || m.CompletionStatus == SetupOnlyCompletion
	completionURL := a.EffectiveCompletionURL()

	dot, status := "neutral", "待检查"
	switch {
	case setupOnly:
		dot, status = "neutral", "待配置提炼"
	case m.CompletionStatus == CompletionInactiveStatus:
		dot, status = "neutral", "未启用"
	case m.CompletionStatus == "未配置" || (completionURL == "" && m.CompletionStatus == ""):
		dot, status = "neutral", "未配置"
	case strings.HasPrefix(m.CompletionStatus, "端点已连接"):
		dot, status = "ok", "已连接"
	case m.CompletionStatus == "" || m.CompletionStatus == "尚未检查":
		dot, status = "neutral", "待检查"
	default:
		dot, status = "warn", "待检查"
	}
	col := serviceStatusColor(tok, dot)
	compactStatus := status
	if dot == "ok" && m.HasCompletionLatency {
		compactStatus = "已连接 · " + FormatDurationCompact(m.CompletionLatencyMs)
	}

	var compactParts []string
	switch {
	case dot == "ok":
		if m.HasCompletionLatency {
			compactParts = append(compactParts, "延迟 "+FormatDurationCompact(m.CompletionLatencyMs))
		}
		if src := completionSourceLabel(*m); src != "" {
			compactParts = append(compactParts, src)
		}
		if m.Snapshot != nil {
			if proc, ok := m.Snapshot.Processes["completion"]; ok && proc.RSSBytes != nil {
				compactParts = append(compactParts, "内存 "+FormatBytesMB(*proc.RSSBytes))
			}
			if m.Snapshot.Summary != nil && m.Snapshot.Summary.RecentProcessingMs != nil {
				compactParts = append(compactParts, "最近模型生成 "+FormatDurationCompact(*m.Snapshot.Summary.RecentProcessingMs))
			}
		}
	case m.CompletionStatus == CompletionInactiveStatus:
		compactParts = append(compactParts, "当前使用直连模型")
	case setupOnly:
		compactParts = append(compactParts, "尚未配置提炼模型")
	case dot == "warn":
		switch m.CompletionErrorCode {
		case "bridge_profile_mismatch":
			compactParts = append(compactParts, "未读取到 CLI 默认模型，请检查登录与配置")
		case "generation_bridge_changed_revalidate":
			compactParts = append(compactParts, "CLI 配置已变更，请重新检查")
		default:
			compactParts = append(compactParts, "连接状态待核对")
		}
	}

	detailParts := append([]string(nil), compactParts...)
	if m.CompletionStatus != "未配置" {
		if u := SanitizedMonitorURL(completionURL); u != "" && u != "未配置" {
			detailParts = append(detailParts, u)
		}
	}

	return ServiceComponentView{
		ID:             "channel",
		Name:           "Agent 模型代理",
		DotState:       dot,
		StatusText:     status,
		StatusColor:    col,
		CompactStatus:  compactStatus,
		CompactMetrics: strings.Join(compactParts, " · "),
		DetailMetrics:  strings.Join(detailParts, " · "),
	}
}

func (a *Controller) ServiceDiskComponent() (ServiceComponentView, bool) {
	m := &a.monitor
	if m.Snapshot == nil || m.Snapshot.Disk == nil {
		return ServiceComponentView{}, false
	}
	tok := a.tokens()
	disk := m.Snapshot.Disk
	dot := "ok"
	stopLabel := FormatDiskThreshold(disk.StopFreeBytes, "55 GiB")
	minLabel := FormatDiskThreshold(disk.MinimumFreeBytes, "50 GiB")
	compactMetrics := fmt.Sprintf("可用空间正常 · 预警线 %s", stopLabel)
	detailMetrics := fmt.Sprintf("预警线 %s · 安全线 %s", stopLabel, minLabel)
	if disk.Low {
		dot = "fail"
		compactMetrics = fmt.Sprintf("低于 %s 预警线", stopLabel)
		detailMetrics = fmt.Sprintf("低于 %s 预警线，请及时清理磁盘空间", stopLabel)
	}
	statusText := FormatBytesGiB(disk.FreeBytes)
	return ServiceComponentView{
		ID:             "disk",
		Name:           "磁盘空间",
		DotState:       dot,
		StatusText:     statusText,
		StatusColor:    serviceStatusColor(tok, dot),
		CompactStatus:  statusText,
		CompactMetrics: compactMetrics,
		DetailMetrics:  detailMetrics,
	}, true
}

// ServiceComponents returns the ordered list of active service component rows
// shared across the sidebar popover and Settings › Service.
func (a *Controller) ServiceComponents() []ServiceComponentView {
	m := &a.monitor
	rows := make([]ServiceComponentView, 0, 4)
	rows = append(rows, a.ServiceCogneeComponent())
	if m.ControlChecked || m.Snapshot != nil {
		rows = append(rows, a.ServiceControlComponent())
	}
	if a.hasChannelComponent() {
		rows = append(rows, a.ServiceChannelComponent())
	}
	if disk, ok := a.ServiceDiskComponent(); ok {
		rows = append(rows, disk)
	}
	return rows
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

		for _, comp := range a.ServiceComponents() {
			row := comp
			kitui.Row(c, tok, kitui.RowOptions{
				Key:        "svc-row-" + row.ID,
				Leading:    func() { kitui.StatusDot(c, tok, row.DotState) },
				Title:      row.Name,
				Desc:       row.DetailMetrics,
				DescMono:   true,
				Value:      row.StatusText,
				ValueColor: row.StatusColor,
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
	dotState = "neutral"
	switch a.monitor.State {
	case "ready":
		dotState, status = "ok", "已连接"
	case "warning":
		dotState, status = "warn", "短暂波动"
	case "exited":
		dotState, status = "fail", "服务已退出"
	case "degraded":
		dotState, status = "fail", "响应异常"
	default:
		if a.monitor.EngineStatus == "已连接" {
			dotState, status = "ok", "已连接"
		} else if a.monitor.EngineStatus != "" && a.monitor.EngineStatus != "尚未检查" {
			dotState, status = "fail", a.monitor.EngineStatus
		}
	}
	return dotState, status, serviceStatusColor(tok, dotState)
}

// All rows render as direct group children so expanding one Agent keeps dividers intact.
func (a *Controller) agentConnectionGrid(c *ui.Context, rows []AgentConnection) {
	for _, row := range rows {
		a.agentConnectionRow(c, row)
	}
}

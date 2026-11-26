package settings

import (
	"fmt"
	"strings"
)

func (a *Controller) ServiceDiagnosticText() string {
	m := a.monitor
	var b strings.Builder
	state := m.State
	if state == "" {
		state = a.monitor.EngineStatus
	}
	fmt.Fprintf(&b, "记忆引擎（Cognee）状态: %s\n", state)
	fmt.Fprintf(&b, "服务地址: %s\n", SanitizedMonitorURL(a.config.ServiceURL))
	fmt.Fprintf(&b, "最近检查时间：%s\n最近正常时间：%s\n", m.checkedTime(), m.lastOKTime())
	if m.HasLatency {
		fmt.Fprintf(&b, "健康检查延迟: %.1f ms\n", m.LastLatencyMs)
	}
	if m.ConsecutiveFailures > 0 {
		fmt.Fprintf(&b, "连续失败次数: %d\n", m.ConsecutiveFailures)
	}
	if m.LastError != "" {
		fmt.Fprintf(&b, "最近错误: %s\n", m.LastError)
	}
	if m.ControlChecked {
		if m.ControlReachable {
			if m.HasControlLatency {
				fmt.Fprintf(&b, "接入网关状态: 已连接 (延迟 %.1f ms)\n", m.ControlLatencyMs)
			} else {
				b.WriteString("接入网关状态: 已连接\n")
			}
		} else if m.ControlError != "" {
			fmt.Fprintf(&b, "接入网关状态: 异常 (%s)\n", m.ControlError)
		}
	}
	if m.Snapshot != nil {
		for _, role := range []string{"official", "project_control", "completion"} {
			proc, ok := m.Snapshot.Processes[role]
			if !ok {
				continue
			}
			line := fmt.Sprintf("进程 %s: pid=%d", role, proc.PID)
			if proc.Alive != nil {
				line += fmt.Sprintf(" alive=%v", *proc.Alive)
			}
			if proc.RSSBytes != nil {
				line += " rss=" + FormatBytesMB(*proc.RSSBytes)
			}
			if proc.SelfRSSBytes != nil && (proc.RSSBytes == nil || *proc.SelfRSSBytes != *proc.RSSBytes) {
				line += " self_rss=" + FormatBytesMB(*proc.SelfRSSBytes)
			}
			if proc.ChildCount > 0 {
				line += fmt.Sprintf(" children=%d", proc.ChildCount)
			}
			b.WriteString(line + "\n")
		}
		if m.Snapshot.Disk != nil {
			fmt.Fprintf(&b, "磁盘可用空间: %s (低空间预警=%v)\n", FormatBytesGiB(m.Snapshot.Disk.FreeBytes), m.Snapshot.Disk.Low)
		}
		if m.Snapshot.Summary != nil {
			s := m.Snapshot.Summary
			fmt.Fprintf(&b, "项目数: %d | Hook 未记录数: %d\n", s.ProjectsCount, s.HookErrorsCount)
			if s.FilteredCount > 0 || len(s.RecentFiltered) > 0 {
				fmt.Fprintf(&b, "因采集规则未采集：%d 条\n", max(s.FilteredCount, len(s.RecentFiltered)))
				for _, event := range s.RecentFiltered {
					fmt.Fprintf(&b, "  %s · %s\n", event.Rule, formatFilteredCaptureLine(event))
				}
			}
			for _, he := range s.HookErrors {
				b.WriteString("  " + formatMonitorHookErrorLine(he) + "\n")
			}
			if s.RecentProcessingMs != nil {
				fmt.Fprintf(&b, "最近模型生成耗时: %s\n", FormatDurationCompact(*s.RecentProcessingMs))
			}
			if s.GenerationReceiptVerified != nil {
				fmt.Fprintf(&b, "模型回执校验: %v\n", *s.GenerationReceiptVerified)
			}
			if s.BacklogFilesCount > 0 || s.LargestGraphDBBytes > 0 {
				fmt.Fprintf(&b, "积压文件数: %d | 最大图谱库: %s\n", s.BacklogFilesCount, FormatBytesCompact(s.LargestGraphDBBytes))
			}
			if s.RecallQueries != nil && s.RecallQueries.TotalQueries > 0 {
				rate := ""
				if s.RecallQueries.HitRate != nil {
					rate = fmt.Sprintf(" (命中率 %.1f%%)", *s.RecallQueries.HitRate*100)
				}
				fmt.Fprintf(&b, "记忆召回统计: 查询 %d 次 | 命中 %d 次%s\n", s.RecallQueries.TotalQueries, s.RecallQueries.HitQueries, rate)
			}
		}
		if len(m.Snapshot.LogTail) > 0 {
			b.WriteString("日志尾部（已脱敏）:\n")
			for _, line := range m.Snapshot.LogTail {
				b.WriteString("  " + stripANSIEscapes(line) + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

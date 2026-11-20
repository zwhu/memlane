package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type MonitoredProcess struct {
	PID        int      `json:"pid"`
	Alive      *bool    `json:"alive"`
	RSSBytes   *int64   `json:"rss_bytes"`
	CPUPercent *float64 `json:"cpu_percent"`
}

type DiskMonitorState struct {
	FreeBytes        int64 `json:"free_bytes"`
	MinimumFreeBytes int64 `json:"minimum_free_bytes"`
	StopFreeBytes    int64 `json:"stop_free_bytes"`
	Low              bool  `json:"low"`
}

type MonitorBusinessSummary struct {
	ProjectsCount             int      `json:"projects_count"`
	GenerationMode            string   `json:"generation_mode"`
	GenerationReceiptVerified *bool    `json:"generation_receipt_verified"`
	RecentProcessingMs        *float64 `json:"recent_processing_ms"`
	HookErrorsCount           int      `json:"hook_errors_count"`
}

type ServiceMonitorSnapshot struct {
	State               string                      `json:"state"`
	CheckedAt           float64                     `json:"checked_at"`
	LastOKAt            float64                     `json:"last_ok_at"`
	LatencyMs           *float64                    `json:"latency_ms"`
	ConsecutiveFailures int                         `json:"consecutive_failures"`
	LastError           string                      `json:"last_error"`
	ServiceURL          string                      `json:"service_url"`
	OwnerPID            int                         `json:"owner_pid"`
	OwnerPhase          string                      `json:"owner_phase"`
	Processes           map[string]MonitoredProcess `json:"processes"`
	Disk                *DiskMonitorState           `json:"disk"`
	Summary             *MonitorBusinessSummary     `json:"summary"`
	LogTail             []string                    `json:"log_tail"`
}

type serviceMonitorState struct {
	State               string
	ConsecutiveFailures int
	LastLatencyMs       float64
	HasLatency          bool
	LastOKAt            time.Time
	LastCheckedAt       time.Time
	LastError           string
	ControlReachable    bool
	ControlChecked      bool
	ControlError        string
	Snapshot            *ServiceMonitorSnapshot
	Expanded            bool
	CopiedNotice        string
	Polling             bool
	PollEnabled         bool
	NextPollAt          time.Time
	Request             uint64
}

func (a *App) refreshServiceMonitor() {
	if a.Update == nil || a.monitor.Polling {
		return
	}
	client := a.Client
	completionURL := a.Config.CompletionURL
	controlClient, hasControl := a.accessClient()
	a.monitor.Request++
	request := a.monitor.Request
	a.monitor.Polling = true
	a.monitor.NextPollAt = time.Now().Add(10 * time.Second)
	includeLogs := a.monitor.Expanded || a.monitor.State == "degraded" || a.monitor.State == "exited"

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		started := time.Now()
		_, healthErr := client.request(ctx, "GET", "/health", nil)
		latencyMs := float64(time.Since(started).Microseconds()) / 1000.0

		completion := "未配置"
		if completionURL != "" {
			cc := Client{Config: Config{ServiceURL: completionURL}}
			_, e := cc.request(ctx, "GET", "/health", nil)
			completion = "端点已连接（登录状态以实际请求为准）"
			if e != nil {
				completion = e.Error()
			}
		}

		var snap *ServiceMonitorSnapshot
		var controlErr error
		if hasControl {
			var fetched ServiceMonitorSnapshot
			path := "/v1/runtime/monitor"
			if includeLogs || healthErr != nil {
				path += "?include_logs=1"
			}
			controlErr = controlClient.request(ctx, "GET", path, nil, &fetched)
			if controlErr == nil {
				snap = &fetched
			}
		}

		a.Update(func() {
			if request != a.monitor.Request {
				return
			}
			a.applyServiceMonitorResult(time.Now(), healthErr, latencyMs, completion, hasControl, snap, controlErr)
		})
	}()
}

func (a *App) applyServiceMonitorResult(now time.Time, healthErr error, latencyMs float64, completion string, hasControl bool, snap *ServiceMonitorSnapshot, controlErr error) {
	a.completion = completion
	a.monitor.Polling = false
	a.monitor.LastCheckedAt = now
	a.monitor.ControlChecked = hasControl
	a.monitor.ControlReachable = hasControl && controlErr == nil
	if hasControl && controlErr != nil {
		a.monitor.ControlError = controlErr.Error()
	} else {
		a.monitor.ControlError = ""
	}
	if hasControl && controlErr == nil && snap != nil {
		a.monitor.Snapshot = snap
	}

	if snap != nil && snap.State == "exited" {
		a.monitor.State = "exited"
		a.monitor.ConsecutiveFailures++
		a.monitor.HasLatency = false
		a.monitor.LastError = "Cognee 进程已退出"
		a.service = "服务已退出"
		return
	}
	if healthErr == nil {
		a.monitor.State = "ready"
		a.monitor.ConsecutiveFailures = 0
		a.monitor.LastOKAt = now
		a.monitor.LastLatencyMs = latencyMs
		a.monitor.HasLatency = true
		a.monitor.LastError = ""
		a.service = "已连接"
		return
	}
	a.monitor.ConsecutiveFailures++
	a.monitor.LastError = healthErr.Error()
	a.monitor.HasLatency = false
	if !a.monitor.LastOKAt.IsZero() && a.monitor.ConsecutiveFailures < 3 {
		a.monitor.State = "warning"
		a.service = "已连接"
		return
	}
	a.monitor.State = "degraded"
	a.service = healthErr.Error()
}

func (a *App) refreshServiceMonitorIfDue(now time.Time) {
	if a.Update == nil || a.windowInactive || !a.monitor.PollEnabled || a.monitor.Polling || a.Config.ServiceURL == "" || now.Before(a.monitor.NextPollAt) {
		return
	}
	a.refreshServiceMonitor()
}

func formatBytesMB(b int64) string {
	return fmt.Sprintf("%.0f MB", float64(b)/(1024*1024))
}

func formatBytesGiB(b int64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1024*1024*1024))
}

func sanitizedMonitorURL(raw string) string {
	if raw == "" {
		return "未配置"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "未配置"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func serviceMonitorSummaryLine(m serviceMonitorState) string {
	var parts []string
	if m.HasLatency {
		parts = append(parts, fmt.Sprintf("延迟 %.0f ms", m.LastLatencyMs))
	} else if m.Snapshot != nil && m.Snapshot.LatencyMs != nil {
		parts = append(parts, fmt.Sprintf("延迟 %.0f ms", *m.Snapshot.LatencyMs))
	}
	if m.Snapshot != nil {
		if proc, ok := m.Snapshot.Processes["official"]; ok && proc.RSSBytes != nil {
			parts = append(parts, "内存 "+formatBytesMB(*proc.RSSBytes))
		}
		if m.Snapshot.Disk != nil && m.Snapshot.Disk.FreeBytes > 0 {
			parts = append(parts, "磁盘余量 "+formatBytesGiB(m.Snapshot.Disk.FreeBytes))
		}
		if m.Snapshot.Summary != nil && m.Snapshot.Summary.HookErrorsCount > 0 {
			parts = append(parts, fmt.Sprintf("Hook 异常 %d", m.Snapshot.Summary.HookErrorsCount))
		}
	}
	return strings.Join(parts, " · ")
}

func (a *App) serviceDiagnosticText() string {
	m := a.monitor
	var b strings.Builder
	state := m.State
	if state == "" {
		state = a.service
	}
	fmt.Fprintf(&b, "Cognee 服务状态: %s\n", state)
	fmt.Fprintf(&b, "服务地址: %s\n", sanitizedMonitorURL(a.Config.ServiceURL))
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
			b.WriteString("控制面状态: 已连接\n")
		} else if m.ControlError != "" {
			fmt.Fprintf(&b, "控制面状态: 异常 (%s)\n", m.ControlError)
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
				line += " rss=" + formatBytesMB(*proc.RSSBytes)
			}
			b.WriteString(line + "\n")
		}
		if m.Snapshot.Disk != nil {
			fmt.Fprintf(&b, "磁盘可用空间: %s (低空间预警=%v)\n", formatBytesGiB(m.Snapshot.Disk.FreeBytes), m.Snapshot.Disk.Low)
		}
		if m.Snapshot.Summary != nil {
			s := m.Snapshot.Summary
			fmt.Fprintf(&b, "项目数: %d | Hook 异常数: %d\n", s.ProjectsCount, s.HookErrorsCount)
			if s.RecentProcessingMs != nil {
				fmt.Fprintf(&b, "最近控制面耗时: %.1f ms\n", *s.RecentProcessingMs)
			}
			if s.GenerationReceiptVerified != nil {
				fmt.Fprintf(&b, "模型回执校验: %v\n", *s.GenerationReceiptVerified)
			}
		}
		if len(m.Snapshot.LogTail) > 0 {
			b.WriteString("日志尾部（已脱敏）:\n")
			for _, line := range m.Snapshot.LogTail {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

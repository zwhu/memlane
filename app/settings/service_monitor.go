package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/internal/controlplane"
)

type MonitoredProcess struct {
	PID          int      `json:"pid"`
	Alive        *bool    `json:"alive"`
	RSSBytes     *int64   `json:"rss_bytes"`
	CPUPercent   *float64 `json:"cpu_percent"`
	ChildCount   int      `json:"child_count,omitempty"`
	SelfRSSBytes *int64   `json:"self_rss_bytes,omitempty"`
}

type DiskMonitorState struct {
	FreeBytes        int64 `json:"free_bytes"`
	MinimumFreeBytes int64 `json:"minimum_free_bytes"`
	StopFreeBytes    int64 `json:"stop_free_bytes"`
	Low              bool  `json:"low"`
}

type MonitorHookError struct {
	InstanceID string  `json:"instance_id"`
	Kind       string  `json:"kind"`
	At         float64 `json:"at"`
	Script     string  `json:"script"`
	ErrorType  string  `json:"error_type"`
	Reason     string  `json:"reason"`
}

type MonitorRecallQueryStats struct {
	StartedAt    string   `json:"started_at,omitempty"`
	TotalQueries int      `json:"total_queries"`
	HitQueries   int      `json:"hit_queries"`
	EmptyQueries int      `json:"empty_queries"`
	TotalHits    int      `json:"total_hits"`
	HitRate      *float64 `json:"hit_rate,omitempty"`
}

type MonitorBusinessSummary struct {
	FilteredCount             int                      `json:"filtered_count,omitempty"`
	RecentFiltered            []api.CaptureFilterEvent `json:"recent_filtered,omitempty"`
	ProjectsCount             int                      `json:"projects_count"`
	GenerationMode            string                   `json:"generation_mode"`
	GenerationReceiptVerified *bool                    `json:"generation_receipt_verified"`
	RecentProcessingMs        *float64                 `json:"recent_processing_ms"`
	HookErrorsCount           int                      `json:"hook_errors_count"`
	HookErrors                []MonitorHookError       `json:"hook_errors,omitempty"`
	BacklogFilesCount         int                      `json:"backlog_files_count,omitempty"`
	LargestGraphDBBytes       int64                    `json:"largest_graph_db_bytes,omitempty"`
	RecallQueries             *MonitorRecallQueryStats `json:"recall_queries,omitempty"`
}

type ServiceMonitorSnapshot struct {
	CheckedAtISO        string                      `json:"checked_at_iso,omitempty"`
	LastOKAtISO         string                      `json:"last_ok_at_iso,omitempty"`
	LastErrorAt         string                      `json:"last_error_at,omitempty"`
	StatusSummary       string                      `json:"status_summary,omitempty"`
	NextAction          string                      `json:"next_action,omitempty"`
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

type ServiceMonitorState struct {
	EngineStatus, CompletionStatus string
	State                          string
	ConsecutiveFailures            int
	LastLatencyMs                  float64
	HasLatency                     bool
	ControlLatencyMs               float64
	HasControlLatency              bool
	CompletionLatencyMs            float64
	HasCompletionLatency           bool
	LastOKAt                       time.Time
	LastCheckedAt                  time.Time
	LastError                      string
	ControlReachable               bool
	ControlChecked                 bool
	ControlError                   string
	Snapshot                       *ServiceMonitorSnapshot
	Expanded                       bool
	ShowRawLogs                    bool
	CopiedNotice                   string
	Polling                        bool
	PollEnabled                    bool
	NextPollAt                     time.Time
	Request                        uint64
}

const SetupOnlyCompletion = "待配置记忆提炼"

func (a *Controller) refreshServiceMonitor() {
	a.PollServiceMonitor(true)
}

func (a *Controller) PollServiceMonitor(allowLogs bool) {
	if !a.canDispatch() || a.monitor.Polling {
		return
	}
	client := a.client
	completionURL, setupOnly := a.config.CompletionURL, a.isSetupOnlyRuntime()
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
		var completionLatencyMs float64
		var hasCompletionLatency bool
		if setupOnly {
			completion = SetupOnlyCompletion
		} else if completionURL != "" {
			cc := Client{Config: Config{ServiceURL: completionURL}}
			compStarted := time.Now()
			_, e := cc.request(ctx, "GET", "/health", nil)
			if e == nil {
				completionLatencyMs = float64(time.Since(compStarted).Microseconds()) / 1000.0
				hasCompletionLatency = true
			}
			completion = "端点已连接（登录状态以实际请求为准）"
			if e != nil {
				completion = e.Error()
			}
		}

		var snap *ServiceMonitorSnapshot
		var controlErr error
		var controlLatencyMs float64
		var hasControlLatency bool
		if hasControl {
			var fetched ServiceMonitorSnapshot
			path := "/v1/runtime/monitor"
			if allowLogs && (includeLogs || healthErr != nil) {
				path += "?include_logs=1"
			}
			ctrlStarted := time.Now()
			controlErr = controlClient.request(ctx, "GET", path, nil, &fetched)
			if controlErr == nil {
				controlLatencyMs = float64(time.Since(ctrlStarted).Microseconds()) / 1000.0
				hasControlLatency = true
				snap = &fetched
			}
		}

		a.dispatch(func() {
			if request != a.monitor.Request {
				return
			}
			a.ApplyServiceMonitorResult(time.Now(), healthErr, latencyMs, completion, completionLatencyMs, hasCompletionLatency, hasControl, snap, controlErr, controlLatencyMs, hasControlLatency)
		})
	}()
}

func (a *Controller) ApplyServiceMonitorResult(now time.Time, healthErr error, latencyMs float64, completion string, completionLatencyMs float64, hasCompletionLatency bool, hasControl bool, snap *ServiceMonitorSnapshot, controlErr error, controlLatencyMs float64, hasControlLatency bool) {
	a.monitor.CompletionStatus = completion
	a.monitor.Polling = false
	a.monitor.LastCheckedAt = now
	a.monitor.ControlChecked = hasControl
	a.monitor.ControlReachable = hasControl && controlErr == nil
	if a.monitor.ControlReachable && hasControlLatency {
		a.monitor.ControlLatencyMs = controlLatencyMs
		a.monitor.HasControlLatency = true
	} else {
		a.monitor.ControlLatencyMs = 0
		a.monitor.HasControlLatency = false
	}
	if strings.HasPrefix(completion, "端点已连接") && hasCompletionLatency {
		a.monitor.CompletionLatencyMs = completionLatencyMs
		a.monitor.HasCompletionLatency = true
	} else {
		a.monitor.CompletionLatencyMs = 0
		a.monitor.HasCompletionLatency = false
	}
	if hasControl && controlErr != nil {
		a.monitor.ControlError = controlplane.RedactSecretText(controlErr.Error())
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
		a.monitor.LastError = controlplane.RedactSecretText(snap.LastError)
		a.monitor.EngineStatus = "服务已退出"
		return
	}
	if snap != nil && snap.State == "degraded" {
		reason := snap.LastError
		if reason == "" {
			reason = "engine_unavailable"
		}
		healthErr = errors.New(reason)
	}
	if healthErr == nil {
		a.monitor.State = "ready"
		a.monitor.ConsecutiveFailures = 0
		a.monitor.LastOKAt = now
		a.monitor.LastLatencyMs = latencyMs
		a.monitor.HasLatency = true
		a.monitor.LastError = ""
		a.monitor.EngineStatus = "已连接"
		return
	}
	a.monitor.ConsecutiveFailures++
	a.monitor.LastError = controlplane.RedactSecretText(healthErr.Error())
	a.monitor.HasLatency = false
	if !a.monitor.LastOKAt.IsZero() && a.monitor.ConsecutiveFailures < 3 {
		a.monitor.State = "warning"
		a.monitor.EngineStatus = "已连接"
		return
	}
	a.monitor.State = "degraded"
	a.monitor.EngineStatus = a.monitor.LastError
}

func (a *Controller) RefreshServiceMonitorIfDue(now time.Time) {
	if !a.canDispatch() || a.windowIsInactive() || a.windowIsHidden() || !a.monitor.PollEnabled || a.monitor.Polling || a.config.ServiceURL == "" || now.Before(a.monitor.NextPollAt) {
		return
	}
	a.refreshServiceMonitor()
}

func FormatBytesMB(b int64) string {
	return fmt.Sprintf("%.0f MB", float64(b)/(1024*1024))
}

func FormatBytesGiB(b int64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1024*1024*1024))
}

func FormatBytesCompact(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func FormatDurationCompact(ms float64) string {
	if ms < 0 {
		ms = 0
	}
	if ms >= 999.5 {
		return fmt.Sprintf("%.1f s", ms/1000.0)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

func SanitizedMonitorURL(raw string) string {
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

var (
	ansiEscapeRe       = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	controlCharRe      = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
	structlogLineRe    = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?)\s+\[\s*(info|warning|warn|error|critical|debug)\s*\]\s*(.+)$`)
	structlogLoggerRe  = regexp.MustCompile(`\s+\[([A-Za-z0-9_.:()-]+)\]\s*$`)
	routingIndexRe     = regexp.MustCompile(`^Dataset routing index for [^:]+:\s*(\d+)\s+row\(s\)\s+written,\s*(\d+)\s+removed$`)
	taskStartedRe      = regexp.MustCompile("^(?:Coroutine|Async Generator) task started:\\s*`([^`]+)`$")
	taskCompletedRe    = regexp.MustCompile("^(?:Coroutine|Async Generator) task completed:\\s*`([^`]+)`$")
	pipelineCompleteRe = regexp.MustCompile("^Pipeline item completed:\\s*`[^`]+`$")
	neighborhoodRe     = regexp.MustCompile(`^Neighborhood retrieval \(1-hop\):\s*(\d+)\s+nodes and\s*(\d+)\s+edges in\s*([^\s]+)$`)
	recallRe           = regexp.MustCompile(`^recall:\s*(\d+)\s+results across\b`)
)

func stripANSIEscapes(s string) string {
	return controlCharRe.ReplaceAllString(ansiEscapeRe.ReplaceAllString(s, ""), "")
}

func formatServiceLogTimestamp(raw string) string {
	if t, ok := parseEventTime(raw); ok {
		return t.Local().Format("2006/01/02 15:04:05")
	}
	return unknownEventTime
}

func humanCogneeTask(name string) string {
	tasks := map[string]string{
		"build_document_index":          "建立文档索引",
		"load_dataset_content":          "加载项目内容",
		"add_data_points":               "写入知识图谱节点",
		"extract_graph_and_summarize":   "提炼图谱与摘要",
		"find_indexed_chunks":           "检查已索引片段",
		"extract_chunks_from_documents": "切分文档片段",
		"classify_documents":            "分类文档",
	}
	if label, ok := tasks[name]; ok {
		return label
	}
	return name
}

func translateCogneeInfoMessage(msg string) string {
	if m := routingIndexRe.FindStringSubmatch(msg); m != nil {
		if m[1] == "0" && m[2] == "0" {
			return "更新项目索引路由（无变更）"
		}
		return fmt.Sprintf("更新项目索引路由（写入 %s 条，移除 %s 条）", m[1], m[2])
	}
	if m := taskStartedRe.FindStringSubmatch(msg); m != nil {
		return "开始后台任务：" + humanCogneeTask(m[1])
	}
	if m := taskCompletedRe.FindStringSubmatch(msg); m != nil {
		return "完成后台任务：" + humanCogneeTask(m[1])
	}
	if pipelineCompleteRe.MatchString(msg) {
		return "记忆提炼管线完成一条记录"
	}
	if msg == "improve: global context index updated" {
		return "全局上下文索引已更新"
	}
	if m := neighborhoodRe.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("图谱关联检索（%s 个节点、%s 条关系，耗时 %s）", m[1], m[2], m[3])
	}
	if m := recallRe.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("记忆召回完成（命中 %s 条）", m[1])
	}
	if strings.HasPrefix(msg, "Skipping startup recovery for ") {
		return "跳过启动恢复（存在进行中的提炼任务）"
	}
	return msg
}

func formatStructuredServiceLog(cleaned string) (string, string, bool) {
	if !strings.HasPrefix(cleaned, "{") {
		return "", "", false
	}
	var entry struct {
		Event       string   `json:"event"`
		At          string   `json:"at"`
		ElapsedMs   *float64 `json:"elapsed_ms"`
		BackendKind string   `json:"backend_kind"`
		Component   string   `json:"component"`
	}
	if err := json.Unmarshal([]byte(cleaned), &entry); err != nil || entry.Event == "" {
		return "", "", false
	}
	switch entry.Event {
	case "capture_filtered":
		return formatServiceLogTimestamp(entry.At) + " · 警告 · 因采集规则未采集，请查看规则命中原因。", "warning", true
	case "inbox_failed", "capture_failed":
		return formatServiceLogTimestamp(entry.At) + " · 错误 · 采集记录处理失败，请查看队列详情。", "error", true
	case "request_finished":
	default:
		return "", "", false
	}
	var parts []string
	if entry.BackendKind != "" && entry.BackendKind != "unknown" {
		parts = append(parts, entry.BackendKind)
	} else if entry.Component != "" {
		parts = append(parts, entry.Component)
	}
	if entry.ElapsedMs != nil {
		if *entry.ElapsedMs >= 1000 {
			parts = append(parts, fmt.Sprintf("耗时 %.1fs", *entry.ElapsedMs/1000.0))
		} else {
			parts = append(parts, fmt.Sprintf("耗时 %.0f ms", *entry.ElapsedMs))
		}
	}
	detail := "完成处理请求"
	if len(parts) > 0 {
		detail += "（" + strings.Join(parts, "，") + "）"
	}
	prefix := formatServiceLogTimestamp(entry.At) + " · "
	return prefix + "正常 · " + detail, "info", true
}

func formatServiceLogLine(raw string) (string, string) {
	cleaned := strings.TrimSpace(stripANSIEscapes(raw))
	if cleaned == "" {
		return "", ""
	}
	if display, level, ok := formatStructuredServiceLog(cleaned); ok {
		return display, level
	}
	if m := structlogLineRe.FindStringSubmatch(cleaned); m != nil {
		ts := formatServiceLogTimestamp(m[1])
		rawLevel := strings.ToLower(m[2])
		msg := strings.TrimSpace(m[3])
		if lm := structlogLoggerRe.FindStringSubmatch(msg); lm != nil && lm[1] != "REDACTED" {
			msg = strings.TrimSpace(msg[:len(msg)-len(lm[0])])
		}
		level, levelLabel := "info", "正常"
		switch rawLevel {
		case "warning", "warn":
			level, levelLabel = "warning", "警告"
		case "error", "critical":
			level, levelLabel = "error", "错误"
		case "debug":
			level, levelLabel = "debug", "调试"
		default:
			msg = translateCogneeInfoMessage(msg)
		}
		return fmt.Sprintf("%s · %s · %s", ts, levelLabel, msg), level
	}
	lower := strings.ToLower(cleaned)
	level := ""
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "exited") || strings.Contains(lower, "failed") || strings.Contains(lower, "traceback"):
		level = "error"
	case strings.Contains(lower, "warn"):
		level = "warning"
	}
	ts, message := plainServiceLogParts(cleaned)
	switch {
	case strings.HasPrefix(message, "gateway inbox:"):
		return ts + " · 错误 · 采集队列处理遇到异常，请查看原始日志。", "error"
	case strings.HasPrefix(message, "gateway store:"):
		return ts + " · 错误 · 网关无法访问本地数据，请查看原始日志。", "error"
	case strings.HasPrefix(message, "capture_filtered"):
		return ts + " · 警告 · 因采集规则未采集，请查看规则命中原因。", "warning"
	}
	return ts + " · " + message, level
}

func plainServiceLogParts(cleaned string) (string, string) {
	const layout = "2006/01/02 15:04:05"
	if len(cleaned) > len(layout) && cleaned[len(layout)] == ' ' {
		if t, err := time.ParseInLocation(layout, cleaned[:len(layout)], time.Local); err == nil {
			return t.Format(layout), strings.TrimSpace(cleaned[len(layout):])
		}
	}
	prefix, message, _ := strings.Cut(cleaned, " ")
	if t, ok := parseEventTime(prefix); ok && strings.Contains(prefix, "T") {
		return t.Local().Format(layout), message
	}
	return unknownEventTime, cleaned
}

func formatMonitorHookErrorLine(e MonitorHookError) string {
	agentLabel := "Agent"
	if e.Kind != "" {
		agentLabel = connectionName(e.Kind)
	}
	timePart := " · " + formatUnixTimeLocal(e.At)
	reason := hookErrorSummary(&AgentHookError{ErrorType: e.ErrorType, Reason: e.Reason})
	if reason == "" {
		reason = "未记录原因"
	}
	return fmt.Sprintf("Hook 未记录（%s%s）：%s", agentLabel, timePart, reason)
}

// ServiceSummary provides the compact monitor label for the window chrome.
func (a *Controller) ServiceSummary() string {
	m := a.monitor
	var parts []string
	if m.HasLatency {
		parts = append(parts, "延迟 "+FormatDurationCompact(m.LastLatencyMs))
	} else if m.Snapshot != nil && m.Snapshot.LatencyMs != nil {
		parts = append(parts, "延迟 "+FormatDurationCompact(*m.Snapshot.LatencyMs))
	}
	if m.Snapshot != nil {
		if proc, ok := m.Snapshot.Processes["official"]; ok && proc.RSSBytes != nil {
			parts = append(parts, "内存 "+FormatBytesMB(*proc.RSSBytes))
		}
		if m.Snapshot.Disk != nil && m.Snapshot.Disk.FreeBytes > 0 {
			parts = append(parts, "磁盘余量 "+FormatBytesGiB(m.Snapshot.Disk.FreeBytes))
		}
		if m.Snapshot.Summary != nil && m.Snapshot.Summary.HookErrorsCount > 0 {
			parts = append(parts, fmt.Sprintf("Hook 未记录 %d", m.Snapshot.Summary.HookErrorsCount))
		}
	}
	return strings.Join(parts, " · ")
}

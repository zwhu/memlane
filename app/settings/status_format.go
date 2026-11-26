package settings

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/internal/controlplane"
)

const unknownEventTime = "时间未知"
const eventTimeLayout = "2006/01/02 15:04"

func parseEventTime(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func formatEventTimeLocal(raw string) string {
	if t, ok := parseEventTime(raw); ok {
		return t.Local().Format(eventTimeLayout)
	}
	return unknownEventTime
}

func formatUnixTimeLocal(sec float64) string {
	if sec <= 0 || math.IsNaN(sec) || math.IsInf(sec, 0) || sec >= math.MaxInt64 {
		return unknownEventTime
	}
	return time.Unix(int64(sec), 0).Local().Format(eventTimeLayout)
}

func formatAutoCaptureStatus(enabled bool, updatedAt, actor, reason string) (title, desc, tone string) {
	ts := formatEventTimeLocal(updatedAt)
	if enabled {
		desc = "新会话将自动采集并异步提炼记忆。"
		if ts != unknownEventTime {
			desc = "开启于 " + ts + " · " + desc
		}
		return "自动采集已开启 · 正常", desc, "ok"
	}
	why := "原因未记录"
	switch {
	case actor == "system":
		why = "由系统暂停"
	case reason == "user_disabled" || actor == "user":
		why = "由用户关闭"
	}
	return "自动采集已关闭", fmt.Sprintf("自动采集已暂停 · %s · %s；关闭期间的新会话不会自动采集，开启右侧开关可恢复。", ts, why), "neutral"
}

func readableFailure(raw, fallback string) string {
	han := false
	for _, r := range raw {
		if unicode.Is(unicode.Han, r) {
			han = true
		} else if !strings.ContainsRune(" ，。；、！？（）", r) {
			return fallback
		}
	}
	if han {
		return raw
	}
	return fallback
}

func formatInboxFailureUI(rawErr, lastErrorAt string, recent []api.InboxFailure) (headline, detail, tech string) {
	headline = readableFailure(rawErr, "采集记录暂未处理完成")
	const retry = "点击右侧「一键重试」重新处理，或展开技术详情排查。"
	var diagnostics []string
	if len(recent) > 0 {
		failure := recent[0]
		if failure.Summary != "" {
			headline = failure.Summary
		}
		if lastErrorAt == "" {
			lastErrorAt = failure.LastErrorAt
		}
		if failure.NextAction != "" {
			detail = " " + failure.NextAction
		}
		for _, f := range recent {
			diagnostics = append(diagnostics, fmt.Sprintf("%s · 尝试 %d 次 · %s\n%s %s", f.ID, f.Attempts,
				formatEventTimeLocal(f.LastErrorAt), f.ErrorCode, controlplane.RedactSecretText(f.LastError)))
		}
	} else if rawErr != "" {
		diagnostics = append(diagnostics, controlplane.RedactSecretText(rawErr))
	}
	return headline, formatEventTimeLocal(lastErrorAt) + " · " + retry + detail, strings.Join(diagnostics, "\n\n")
}

func formatFilteredCaptureLine(ev api.CaptureFilterEvent) string {
	line := formatEventTimeLocal(ev.OccurredAt) + " · " + ev.Reason
	if ev.NextAction != "" {
		line += " " + ev.NextAction
	}
	return line
}

func filteredCaptureDescription(events []api.CaptureFilterEvent) string {
	if len(events) > 0 {
		return formatFilteredCaptureLine(events[0])
	}
	return "时间未知 · 暂无规则命中详情。"
}

func monitorDisplayTime(iso string, unix float64, observed time.Time) string {
	if iso != "" {
		return formatEventTimeLocal(iso)
	}
	if unix > 0 {
		return formatUnixTimeLocal(unix)
	}
	if !observed.IsZero() {
		return observed.Local().Format(eventTimeLayout)
	}
	return unknownEventTime
}

func (m *ServiceMonitorState) checkedTime() string {
	if m.Snapshot != nil && m.ControlReachable {
		return monitorDisplayTime(m.Snapshot.CheckedAtISO, m.Snapshot.CheckedAt, m.LastCheckedAt)
	}
	if m.Snapshot != nil && m.LastCheckedAt.IsZero() {
		return monitorDisplayTime(m.Snapshot.CheckedAtISO, m.Snapshot.CheckedAt, time.Time{})
	}
	return monitorDisplayTime("", 0, m.LastCheckedAt)
}

func (m *ServiceMonitorState) lastOKTime() string {
	if m.Snapshot != nil && (m.Snapshot.LastOKAtISO != "" || m.Snapshot.LastOKAt > 0) {
		return monitorDisplayTime(m.Snapshot.LastOKAtISO, m.Snapshot.LastOKAt, m.LastOKAt)
	}
	return monitorDisplayTime("", 0, m.LastOKAt)
}

func (m *ServiceMonitorState) alertText() string {
	cause := readableFailure(m.LastError, "本地提炼服务未能通过健康检查")
	switch raw := strings.ToLower(m.LastError); {
	case strings.Contains(raw, "timeout"), strings.Contains(raw, "deadline"):
		cause = "连接或等待提炼服务响应超时"
	case strings.Contains(raw, "connection refused"), strings.Contains(raw, "unavailable"), strings.Contains(raw, "http 503"):
		cause = "提炼服务暂时无法连接"
	case strings.Contains(raw, "unauthorized"), strings.Contains(raw, "forbidden"), strings.Contains(raw, "http 401"), strings.Contains(raw, "http 403"):
		cause = "提炼服务拒绝访问，请检查服务凭据"
	}
	action := "请展开服务日志核对原因，必要时手动重启服务。"
	ts := m.checkedTime()
	if snap := m.Snapshot; snap != nil && (m.State == "exited" && snap.State == "exited" ||
		m.State != "exited" && snap.State == "degraded") {
		if snap.StatusSummary != "" {
			cause = snap.StatusSummary
		}
		if snap.NextAction != "" {
			action = snap.NextAction
		}
		if snap.LastErrorAt != "" {
			ts = formatEventTimeLocal(snap.LastErrorAt)
		} else if m.State == "exited" {
			ts = unknownEventTime
		}
	}
	switch m.State {
	case "exited":
		if !strings.Contains(cause, "服务未自动重启") {
			cause = "Cognee 进程已退出，服务未自动重启"
		}
	case "warning":
		cause = fmt.Sprintf("检测到短暂健康波动（连续失败 %d/3 次）：%s", m.ConsecutiveFailures, cause)
		action = "正在继续检查；若持续失败，请展开服务日志排查。"
	}
	return cause + " · " + ts + " · " + action
}

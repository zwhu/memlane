package overview

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"time"
)

type FailureExplanation struct {
	Label   string
	Summary string
	Hint    string
}

func DescribeFailureClass(class string) FailureExplanation {
	class = safeErrorClass(class)
	switch class {
	case "engine_unavailable":
		return FailureExplanation{"提炼服务暂时无法连接", "采集记录暂未完成处理，提炼服务无法连接。", "请在服务监控中检查运行状态，服务恢复后重试。"}
	case "engine_internal_error":
		return FailureExplanation{"记忆提炼内部出错", "提炼服务仍在运行，但执行本次任务时内部发生错误。", "可展开下方「技术信息」或服务日志排查原因，必要时重启服务后重试。"}
	case "engine_timeout", "context_deadline_exceeded":
		return FailureExplanation{"提炼服务响应超时", "服务未在等待时间内完成请求。", "请确认本地服务和模型端点可以连接，然后重试。"}
	case "capture_paused":
		return FailureExplanation{"自动采集已暂停", "当前项目没有接收自动采集的新会话。", "如需恢复，请在项目设置中打开自动采集。"}
	case "capture_filtered":
		return FailureExplanation{"因采集规则未采集", "记录命中采集规则，原文保留在本机。", "请在项目采集状态中查看规则和原因，无需重试。"}
	case "ConnectTimeout":
		return FailureExplanation{
			Label:   "连接超时",
			Summary: "在尝试连接模型接口或外部服务时未能建立连接，等待超时后中断。",
			Hint:    "常见于网络断开、电脑休眠、代理中断或目标服务暂不可达。",
		}
	case "ReadTimeout":
		return FailureExplanation{
			Label:   "等待响应超时",
			Summary: "已连接到服务，但等待返回结果超时。",
			Hint:    "常见于模型响应较慢、处理内容较多或服务繁忙。",
		}
	case "TimeoutError", "APITimeoutError", "TimeoutException":
		return FailureExplanation{
			Label:   "操作超时",
			Summary: "本次操作未能在超时限制内完成。",
			Hint:    "常见于网络波动、电脑休眠或模型响应较慢。",
		}
	case "ConnectError", "APIConnectionError", "ConnectionError", "ConnectionRefusedError", "ConnectionResetError":
		return FailureExplanation{
			Label:   "网络连接失败",
			Summary: "无法与模型接口或本地服务建立或维持连接。",
			Hint:    "请检查网络连接、代理设置以及本地服务运行状态。",
		}
	case "BadGatewayError":
		return FailureExplanation{
			Label:   "Agent 模型代理异常",
			Summary: "上游模型代理返回网关错误（HTTP 502），未能完成本次生成。",
			Hint:    "常见于代理/网络瞬时波动或 Codex 上游连接中断，可稍后重试。",
		}
	case "BrokenPipeError":
		return FailureExplanation{
			Label:   "连接管道已断开",
			Summary: "与本地服务或子进程的通信管道中途断开。",
			Hint:    "常见于进程被中断、重启或输出流提前关闭。",
		}
	case "PermissionDeniedError", "UnauthorizedError", "AuthenticationError":
		return FailureExplanation{
			Label:   "访问被拒绝",
			Summary: "服务拒绝了本次请求的凭据或访问权限。",
			Hint:    "请检查模型 API Key、访问权限或本地服务凭据。",
		}
	case "RateLimitError":
		return FailureExplanation{
			Label:   "请求频率受限",
			Summary: "触发了模型接口的请求频率或额度限制。",
			Hint:    "请稍后重试，或在「设置 › 记忆提炼」核对当前模型额度。",
		}
	case "":
		return FailureExplanation{
			Label:   "官方未提供详细原因",
			Summary: "官方记录仅标注任务失败，未返回具体错误类型。",
			Hint:    "可展开下方「技术信息」核对时间与运行 ID，或复制诊断信息排查。",
		}
	default:
		return FailureExplanation{
			Label:   class,
			Summary: fmt.Sprintf("执行过程中返回了 %s 错误。", class),
			Hint:    "可展开下方「技术信息」核对阶段与日志位置，或复制诊断信息排查。",
		}
	}
}

func FailureReason(class string) string {
	return DescribeFailureClass(class).Label
}

func EventFailureReason(event Event) string {
	for _, r := range orderedFailures(event.Failures) {
		if r.ErrorClass != "" {
			return FailureReason(r.ErrorClass)
		}
	}
	return "官方未提供详细原因"
}

func FailureStageName(row ActivityRow) (display, raw string) {
	raw = row.Operation
	if raw == "" {
		raw = row.Pipeline
	}
	raw = diagnosticID(raw)
	switch raw {
	case "memify_pipeline":
		return "提炼记忆流水线", raw
	case "memify", "improve":
		return "提炼记忆任务", raw
	case "cognify_pipeline":
		return "提炼记忆流水线", raw
	case "cognify":
		return "提炼记忆任务", raw
	case "add_pipeline":
		return "采集材料流水线", raw
	case "add", "remember":
		return "采集材料任务", raw
	case "code_graph_pipeline":
		return "更新代码图谱流水线", raw
	case "recall":
		return "召回记忆", raw
	case "search":
		return "搜索记忆", raw
	case "forget", "delete", "prune":
		return "清理内容", raw
	case "":
		return "未知阶段", ""
	default:
		if mapped := ActivityName(raw); mapped != "记忆活动" {
			return mapped, raw
		}
		return raw, raw
	}
}

func FormatDurationZH(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return "小于 1 秒"
	}
	total := int(d.Round(time.Second) / time.Second)
	if total < 60 {
		return fmt.Sprintf("%d 秒", total)
	}
	if total < 3600 {
		m, s := total/60, total%60
		if s == 0 {
			return fmt.Sprintf("%d 分钟", m)
		}
		return fmt.Sprintf("%d 分 %d 秒", m, s)
	}
	h, m := total/3600, (total%3600)/60
	if m == 0 {
		return fmt.Sprintf("%d 小时", h)
	}
	return fmt.Sprintf("%d 小时 %d 分", h, m)
}

func activityRowTime(r ActivityRow) (time.Time, bool) {
	for _, raw := range []string{r.Ended, r.Created, r.Started} {
		if t, ok := OfficialTime(raw); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func FormatLocalWindow(started, ended, created string, fallback time.Time, loc *time.Location) (localTime, duration string) {
	if loc == nil {
		loc = time.Local
	}
	s, okS := OfficialTime(started)
	e, okE := OfficialTime(ended)
	if okS && okE && !e.Before(s) {
		sl, el := s.In(loc), e.In(loc)
		if sl.Format("2006-01-02") == el.Format("2006-01-02") {
			localTime = fmt.Sprintf("%s – %s", sl.Format("2006/01/02 15:04:05"), el.Format("15:04:05"))
		} else {
			localTime = fmt.Sprintf("%s – %s", sl.Format("2006/01/02 15:04:05"), el.Format("2006/01/02 15:04:05"))
		}
		return localTime, FormatDurationZH(e.Sub(s))
	}
	for _, raw := range []string{ended, created, started} {
		if t, ok := OfficialTime(raw); ok {
			return t.In(loc).Format("2006/01/02 15:04:05"), ""
		}
	}
	if !fallback.IsZero() {
		return fallback.In(loc).Format("2006/01/02 15:04:05"), ""
	}
	return "时间未知", ""
}

func orderedFailures(failures []ActivityRow) []ActivityRow {
	out := append([]ActivityRow(nil), failures...)
	if len(out) <= 1 {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		aHas, bHas := out[i].ErrorClass != "", out[j].ErrorClass != ""
		if aHas != bHas {
			return aHas
		}
		at, aOK := activityRowTime(out[i])
		bt, bOK := activityRowTime(out[j])
		if aOK != bOK {
			return aOK
		}
		if aOK && !at.Equal(bt) {
			return at.After(bt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

const failureBoundaryNote = "此信息来自已有官方记录和本机日志位置，不代表已修复或已重试。未包含凭据、请求内容或会话正文。请结合任务时间和调用位置分析原因，先核对当前进程与任务所有权，再决定修复或重试。"

func BuildFailureDetail(event Event, c DiagnosticSource, loc *time.Location) FailureDetail {
	if loc == nil {
		loc = time.Local
	}
	dialogTitle := "任务失败详情"
	if event.Title != "" {
		dialogTitle = event.Title + " · 失败详情"
	}
	detail := FailureDetail{
		EventKey:       diagnosticID(event.Key),
		Datasets:       EventDatasets(event),
		DialogTitle:    dialogTitle,
		OperationTitle: event.Title,
		Project:        event.Project,
		ServiceCommit:  diagnosticID(c.ServiceCommit),
		BoundaryNote:   failureBoundaryNote,
	}
	if !event.At.IsZero() {
		detail.EventUTC = event.At.UTC().Format(time.RFC3339Nano)
	}
	if c.ServiceURL != "" {
		if endpoint, err := url.Parse(c.ServiceURL); err == nil && endpoint.Scheme != "" && endpoint.Host != "" {
			detail.ServiceEndpoint = fmt.Sprintf("%s://%s", endpoint.Scheme, endpoint.Host)
		}
	}
	if c.OfficialData != "" {
		detail.LogDir = filepath.Join(c.OfficialData, "logs")
	}

	rowByRun := map[string]ActivityRow{}
	childHasError := map[string]bool{}
	for _, r := range event.Failures {
		if r.Run != "" {
			rowByRun[r.Run] = r
		}
		if r.Parent != "" && r.ErrorClass != "" {
			childHasError[r.Parent] = true
		}
	}

	ordered := orderedFailures(event.Failures)
	detail.TechRecords = make([]FailureTechRecord, 0, len(ordered))
	detail.Items = make([]FailureDetailItem, 0, len(ordered))

	for _, r := range ordered {
		r.ErrorClass = safeErrorClass(r.ErrorClass)
		stageLabel, stageRaw := FailureStageName(r)
		reasonLabel := FailureReason(r.ErrorClass)
		isWrapper := r.ErrorClass == "" && r.Run != "" && childHasError[r.Run]
		candidate := FailureLogCandidate(c.OfficialData, r)
		if candidate != "" {
			detail.HasLogCandidate = true
		}
		detail.TechRecords = append(detail.TechRecords, FailureTechRecord{
			StageLabel:      stageLabel,
			StageRaw:        stageRaw,
			ErrorClass:      r.ErrorClass,
			ReasonLabel:     reasonLabel,
			IsParentWrapper: isWrapper,
			ID:              diagnosticID(r.ID),
			Run:             diagnosticID(r.Run),
			Parent:          diagnosticID(r.Parent),
			Dataset:         diagnosticID(r.Dataset),
			Origin:          diagnosticID(r.Origin),
			CreatedUTC:      diagnosticTime(r.Created),
			StartedUTC:      diagnosticTime(r.Started),
			EndedUTC:        diagnosticTime(r.Ended),
			LogCandidate:    candidate,
		})
		if isWrapper {
			continue
		}
		started, ended := r.Started, r.Ended
		if (started == "" || ended == "") && r.Parent != "" {
			if parentRow, ok := rowByRun[r.Parent]; ok {
				if started == "" {
					started = parentRow.Started
				}
				if ended == "" {
					ended = parentRow.Ended
				}
			}
		}
		localTime, dur := FormatLocalWindow(started, ended, r.Created, event.At, loc)
		detail.Items = append(detail.Items, FailureDetailItem{
			StageLabel:  stageLabel,
			StageRaw:    stageRaw,
			ErrorClass:  r.ErrorClass,
			ReasonLabel: reasonLabel,
			LocalTime:   localTime,
			Duration:    dur,
		})
	}

	if len(detail.Items) > 0 {
		primary := detail.Items[0]
		exp := DescribeFailureClass(primary.ErrorClass)
		detail.ReasonLabel = exp.Label
		detail.ErrorClass = primary.ErrorClass
		detail.Summary = exp.Summary
		detail.Hint = exp.Hint
		detail.StageLabel = primary.StageLabel
		detail.StageRaw = primary.StageRaw
		detail.LocalTime = primary.LocalTime
		detail.Duration = primary.Duration
		detail.FailureCount = len(detail.Items)
	} else {
		exp := DescribeFailureClass("")
		detail.ReasonLabel = exp.Label
		detail.Summary = exp.Summary
		detail.Hint = exp.Hint
		detail.LocalTime, detail.Duration = FormatLocalWindow("", "", "", event.At, loc)
	}
	return detail
}

type DiagnosticSource struct {
	ServiceURL, ServiceCommit, OfficialData string
}

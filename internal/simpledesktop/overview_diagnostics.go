package simpledesktop

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type failureExplanation struct {
	Label   string
	Summary string
	Hint    string
}

func describeFailureClass(class string) failureExplanation {
	switch class {
	case "ConnectTimeout":
		return failureExplanation{
			Label:   "连接超时",
			Summary: "在尝试连接模型接口或外部服务时未能建立连接，等待超时后中断。",
			Hint:    "常见于网络断开、电脑休眠、代理中断或目标服务暂不可达。",
		}
	case "ReadTimeout":
		return failureExplanation{
			Label:   "等待响应超时",
			Summary: "已连接到服务，但等待返回结果超时。",
			Hint:    "常见于模型响应较慢、处理内容较多或服务繁忙。",
		}
	case "TimeoutError", "APITimeoutError", "TimeoutException":
		return failureExplanation{
			Label:   "操作超时",
			Summary: "本次操作未能在超时限制内完成。",
			Hint:    "常见于网络波动、电脑休眠或模型响应较慢。",
		}
	case "ConnectError", "APIConnectionError", "ConnectionError", "ConnectionRefusedError", "ConnectionResetError":
		return failureExplanation{
			Label:   "网络连接失败",
			Summary: "无法与模型接口或本地服务建立或维持连接。",
			Hint:    "请检查网络连接、代理设置以及本地服务运行状态。",
		}
	case "BadGatewayError":
		return failureExplanation{
			Label:   "Agent 模型代理异常",
			Summary: "上游模型代理返回网关错误（HTTP 502），未能完成本次生成。",
			Hint:    "常见于代理/网络瞬时波动或 Codex 上游连接中断，可稍后重试。",
		}
	case "BrokenPipeError":
		return failureExplanation{
			Label:   "连接管道已断开",
			Summary: "与本地服务或子进程的通信管道中途断开。",
			Hint:    "常见于进程被中断、重启或输出流提前关闭。",
		}
	case "PermissionDeniedError", "UnauthorizedError", "AuthenticationError":
		return failureExplanation{
			Label:   "访问被拒绝",
			Summary: "服务拒绝了本次请求的凭据或访问权限。",
			Hint:    "请检查模型 API Key、访问权限或本地服务凭据。",
		}
	case "RateLimitError":
		return failureExplanation{
			Label:   "请求频率受限",
			Summary: "触发了模型接口的请求频率或额度限制。",
			Hint:    "请稍后重试，或在「设置 › Agent 与模型」核对当前模型额度。",
		}
	case "":
		return failureExplanation{
			Label:   "官方未提供详细原因",
			Summary: "官方记录仅标注任务失败，未返回具体错误类型。",
			Hint:    "可展开下方「技术信息」核对时间与运行 ID，或复制诊断信息排查。",
		}
	default:
		return failureExplanation{
			Label:   class,
			Summary: fmt.Sprintf("执行过程中返回了 %s 错误。", class),
			Hint:    "可展开下方「技术信息」核对阶段与日志位置，或复制诊断信息排查。",
		}
	}
}

func failureReason(class string) string {
	return describeFailureClass(class).Label
}

func eventFailureReason(event overviewEvent) string {
	for _, r := range orderedFailures(event.Failures) {
		if r.ErrorClass != "" {
			return failureReason(r.ErrorClass)
		}
	}
	return "官方未提供详细原因"
}

func failureStageName(row activityRow) (display, raw string) {
	raw = row.Operation
	if raw == "" {
		raw = row.Pipeline
	}
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
		if mapped := activityName(raw); mapped != "记忆活动" {
			return mapped, raw
		}
		return raw, raw
	}
}

func formatDurationZH(d time.Duration) string {
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

func activityRowTime(r activityRow) (time.Time, bool) {
	for _, raw := range []string{r.Ended, r.Created, r.Started} {
		if t, ok := officialTime(raw); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func formatLocalWindow(started, ended, created string, fallback time.Time, loc *time.Location) (localTime, duration string) {
	if loc == nil {
		loc = time.Local
	}
	s, okS := officialTime(started)
	e, okE := officialTime(ended)
	if okS && okE && !e.Before(s) {
		sl, el := s.In(loc), e.In(loc)
		if sl.Format("2006-01-02") == el.Format("2006-01-02") {
			localTime = fmt.Sprintf("%s – %s", sl.Format("2006/01/02 15:04:05"), el.Format("15:04:05"))
		} else {
			localTime = fmt.Sprintf("%s – %s", sl.Format("2006/01/02 15:04:05"), el.Format("2006/01/02 15:04:05"))
		}
		return localTime, formatDurationZH(e.Sub(s))
	}
	for _, raw := range []string{ended, created, started} {
		if t, ok := officialTime(raw); ok {
			return t.In(loc).Format("2006/01/02 15:04:05"), ""
		}
	}
	if !fallback.IsZero() {
		return fallback.In(loc).Format("2006/01/02 15:04:05"), ""
	}
	return "时间未知", ""
}

func orderedFailures(failures []activityRow) []activityRow {
	out := append([]activityRow(nil), failures...)
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

type failureDetailItem struct {
	StageLabel  string
	StageRaw    string
	ErrorClass  string
	ReasonLabel string
	LocalTime   string
	Duration    string
}

type failureTechRecord struct {
	StageLabel      string
	StageRaw        string
	ErrorClass      string
	ReasonLabel     string
	IsParentWrapper bool
	ID              string
	Run             string
	Parent          string
	Dataset         string
	Origin          string
	CreatedUTC      string
	StartedUTC      string
	EndedUTC        string
	LogCandidate    string
}

type failureDetailModel struct {
	EventKey        string
	Datasets        []string
	DialogTitle     string
	OperationTitle  string
	Project         string
	ReasonLabel     string
	ErrorClass      string
	Summary         string
	Hint            string
	StageLabel      string
	StageRaw        string
	LocalTime       string
	Duration        string
	EventUTC        string
	FailureCount    int
	Items           []failureDetailItem
	ServiceEndpoint string
	ServiceCommit   string
	LogDir          string
	HasLogCandidate bool
	TechRecords     []failureTechRecord
	BoundaryNote    string
}

const failureBoundaryNote = "此信息来自已有官方记录和本机日志位置，不代表已修复或已重试。未包含凭据、请求内容或会话正文。请结合任务时间和调用位置分析原因，先核对当前进程与任务所有权，再决定修复或重试。"

func buildFailureDetail(event overviewEvent, c Config, loc *time.Location) failureDetailModel {
	if loc == nil {
		loc = time.Local
	}
	dialogTitle := "任务失败详情"
	if event.Title != "" {
		dialogTitle = event.Title + " · 失败详情"
	}
	detail := failureDetailModel{
		EventKey:       event.Key,
		Datasets:       overviewEventDatasets(event),
		DialogTitle:    dialogTitle,
		OperationTitle: event.Title,
		Project:        event.Project,
		ServiceCommit:  c.ServiceCommit,
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

	rowByRun := map[string]activityRow{}
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
	detail.TechRecords = make([]failureTechRecord, 0, len(ordered))
	detail.Items = make([]failureDetailItem, 0, len(ordered))

	for _, r := range ordered {
		stageLabel, stageRaw := failureStageName(r)
		reasonLabel := failureReason(r.ErrorClass)
		isWrapper := r.ErrorClass == "" && r.Run != "" && childHasError[r.Run]
		candidate := failureLogCandidate(c.OfficialData, r)
		if candidate != "" {
			detail.HasLogCandidate = true
		}
		detail.TechRecords = append(detail.TechRecords, failureTechRecord{
			StageLabel:      stageLabel,
			StageRaw:        stageRaw,
			ErrorClass:      r.ErrorClass,
			ReasonLabel:     reasonLabel,
			IsParentWrapper: isWrapper,
			ID:              r.ID,
			Run:             r.Run,
			Parent:          r.Parent,
			Dataset:         r.Dataset,
			Origin:          r.Origin,
			CreatedUTC:      r.Created,
			StartedUTC:      r.Started,
			EndedUTC:        r.Ended,
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
		localTime, dur := formatLocalWindow(started, ended, r.Created, event.At, loc)
		detail.Items = append(detail.Items, failureDetailItem{
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
		exp := describeFailureClass(primary.ErrorClass)
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
		exp := describeFailureClass("")
		detail.ReasonLabel = exp.Label
		detail.Summary = exp.Summary
		detail.Hint = exp.Hint
		detail.LocalTime, detail.Duration = formatLocalWindow("", "", "", event.At, loc)
	}
	return detail
}

var diagnosticClass = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,127}$`)
var logTimestamp = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\S+)\s`)

// Only stack frame locations are returned. Source lines, local variables,
// exception payloads, request bodies and arbitrary log messages are not copied.
// A temporal/type match is a candidate, never asserted to be a task-ID join.
func failureLogCandidate(root string, failure activityRow) string {
	if root == "" || !diagnosticClass.MatchString(failure.ErrorClass) {
		return ""
	}
	target, ok := officialTime(failure.Created)
	if !ok {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(root, "logs", "*.log"))
	sort.Slice(files, func(i, j int) bool {
		a, e := os.Stat(files[i])
		b, f := os.Stat(files[j])
		return e == nil && (f != nil || a.ModTime().After(b.ModTime()))
	})
	if len(files) > 3 {
		files = files[:3]
	}
	best := ""
	distance := 6 * time.Second
	for _, name := range files {
		file, err := os.Open(name)
		if err != nil {
			continue
		}
		stat, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}
		start := max(int64(0), stat.Size()-512*1024)
		if _, err = file.Seek(start, io.SeekStart); err != nil {
			file.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 512*1024))
		file.Close()
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i := 0; i < len(lines); i++ {
			match := logTimestamp.FindStringSubmatch(lines[i])
			if len(match) < 2 {
				continue
			}
			at, valid := officialTime(match[1])
			if !valid {
				continue
			}
			delta := target.Sub(at)
			if delta < 0 {
				delta = -delta
			}
			if delta >= distance {
				continue
			}
			end := i + 1
			for end < len(lines) && logTimestamp.FindStringSubmatch(lines[end]) == nil {
				end++
			}
			block := lines[i:end]
			if !strings.Contains(strings.Join(block, "\n"), failure.ErrorClass) {
				continue
			}
			frames := []string{}
			for _, line := range block {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, `File "`) {
					runes := []rune(line)
					if len(runes) > 500 {
						line = string(runes[:500]) + "…"
					}
					frames = append(frames, line)
				}
			}
			if len(frames) == 0 {
				continue
			}
			if len(frames) > 24 {
				frames = frames[len(frames)-24:]
			}
			best = fmt.Sprintf("日志：%s\n时间：%s\n%s", name, match[1], strings.Join(frames, "\n"))
			distance = delta
		}
	}
	return best
}

func failureReport(event overviewEvent, c Config) string {
	detail := buildFailureDetail(event, c, time.Local)
	var out strings.Builder
	out.WriteString("# Memlane 失败诊断\n\n")
	if detail.OperationTitle != "" {
		fmt.Fprintf(&out, "操作：%s\n", detail.OperationTitle)
	}
	if event.State != "" {
		fmt.Fprintf(&out, "状态：%s\n", event.State)
	}
	if detail.Project != "" {
		fmt.Fprintf(&out, "项目：%s\n", detail.Project)
	}
	if detail.ErrorClass != "" {
		fmt.Fprintf(&out, "主要原因：%s（%s）\n", detail.ReasonLabel, detail.ErrorClass)
	} else {
		fmt.Fprintf(&out, "主要原因：%s\n", detail.ReasonLabel)
	}
	if detail.LocalTime != "" && detail.LocalTime != "时间未知" {
		if detail.Duration != "" {
			fmt.Fprintf(&out, "发生时间：%s（耗时 %s）\n", detail.LocalTime, detail.Duration)
		} else {
			fmt.Fprintf(&out, "发生时间：%s\n", detail.LocalTime)
		}
	}
	if detail.EventUTC != "" {
		fmt.Fprintf(&out, "时间（UTC）：%s\n", detail.EventUTC)
	}
	if detail.EventKey != "" {
		fmt.Fprintf(&out, "事件 ID：%s\n", detail.EventKey)
	}
	if detail.ServiceEndpoint != "" {
		fmt.Fprintf(&out, "官方服务：%s\n", detail.ServiceEndpoint)
	}
	if detail.ServiceCommit != "" {
		fmt.Fprintf(&out, "Cognee 固定源码版本：%s\n", detail.ServiceCommit)
	}
	out.WriteString("\n## 官方失败记录\n")
	if len(detail.TechRecords) == 0 {
		out.WriteString("官方未返回失败详情。\n")
	}
	for _, rec := range detail.TechRecords {
		stageText := rec.StageLabel
		if rec.StageRaw != "" && rec.StageRaw != rec.StageLabel {
			stageText = fmt.Sprintf("%s（%s）", rec.StageLabel, rec.StageRaw)
		}
		fmt.Fprintf(&out, "\n操作：%s\n", stageText)
		if rec.ErrorClass != "" {
			fmt.Fprintf(&out, "错误类型：%s\n", rec.ErrorClass)
		}
		if rec.IsParentWrapper {
			out.WriteString("说明：外层任务（由子阶段错误触发，未附额外错误类型）\n")
		} else {
			fmt.Fprintf(&out, "说明：%s\n", rec.ReasonLabel)
		}
		if rec.ID != "" {
			fmt.Fprintf(&out, "记录 ID：%s\n", rec.ID)
		}
		if rec.Run != "" {
			fmt.Fprintf(&out, "运行 ID：%s\n", rec.Run)
		}
		if rec.Parent != "" {
			fmt.Fprintf(&out, "父操作 ID：%s\n", rec.Parent)
		}
		if rec.Dataset != "" {
			fmt.Fprintf(&out, "数据组：%s\n", rec.Dataset)
		}
		if rec.Origin != "" {
			fmt.Fprintf(&out, "来源：%s\n", rec.Origin)
		}
		if rec.CreatedUTC != "" {
			fmt.Fprintf(&out, "记录时间（官方 UTC）：%s\n", rec.CreatedUTC)
		}
		if rec.StartedUTC != "" {
			fmt.Fprintf(&out, "开始（UTC）：%s\n", rec.StartedUTC)
		}
		if rec.EndedUTC != "" {
			fmt.Fprintf(&out, "结束（UTC）：%s\n", rec.EndedUTC)
		}
		if rec.LogCandidate != "" {
			out.WriteString("\n日志候选：按错误类型及相邻时间匹配，尚未按任务 ID 精确确认；以下仅保留调用位置。\n\n```text\n" + rec.LogCandidate + "\n```\n")
		}
	}
	if detail.LogDir != "" {
		fmt.Fprintf(&out, "\n本机官方日志目录：%s\n", detail.LogDir)
	}
	fmt.Fprintf(&out, "\n## 排查边界\n\n%s\n", detail.BoundaryNote)
	return out.String()
}

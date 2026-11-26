package overview

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var diagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,160}$`)
var diagnosticClass = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,127}$`)
var stackFrame = regexp.MustCompile(`^File "([^"<>?@[:cntrl:]]{1,350}\.py)", line ([0-9]{1,8}), in ([a-zA-Z0-9_.<>]{1,100})$`)
var logTimestamp = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\S+)\s`)

// Only stack frame locations are returned. Source lines, local variables,
// exception payloads, request bodies and arbitrary log messages are not copied.
// A temporal/type match is a candidate, never asserted to be a task-ID join.
func FailureLogCandidate(root string, failure ActivityRow) string {
	if root == "" || !diagnosticClass.MatchString(failure.ErrorClass) {
		return ""
	}
	target, ok := OfficialTime(failure.Created)
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
			at, valid := OfficialTime(match[1])
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
				if frame := stackFrame.FindStringSubmatch(strings.TrimSpace(line)); frame != nil {
					frames = append(frames, fmt.Sprintf(`File "%s", line %s, in %s`, frame[1], frame[2], frame[3]))
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
func FailureReport(event Event, c DiagnosticSource) string {
	return reportForDetail(event, BuildFailureDetail(event, c, time.Local))
}

func reportForDetail(event Event, detail FailureDetail) string {
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
	localTime := detail.LocalTime
	if localTime == "" {
		localTime = "时间未知"
	}
	if detail.Duration != "" {
		fmt.Fprintf(&out, "发生时间：%s（耗时 %s）\n", localTime, detail.Duration)
	} else {
		fmt.Fprintf(&out, "发生时间：%s\n", localTime)
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

func safeErrorClass(class string) string {
	if diagnosticClass.MatchString(class) {
		return class
	}
	return ""
}

func diagnosticID(value string) string {
	if diagnosticIdentifier.MatchString(value) {
		return value
	}
	return ""
}

func diagnosticTime(value string) string {
	if _, ok := OfficialTime(value); ok {
		return value
	}
	return ""
}

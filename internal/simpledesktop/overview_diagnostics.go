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

func failureReason(class string) string {
	switch class {
	case "BrokenPipeError":
		return "连接管道已断开"
	case "TimeoutError", "ReadTimeout", "ConnectTimeout":
		return "操作超时"
	case "PermissionDeniedError", "UnauthorizedError":
		return "访问被拒绝"
	default:
		if class != "" {
			return class
		}
		return "官方未提供详细原因"
	}
}

func eventFailureReason(event overviewEvent) string {
	for _, r := range event.Failures {
		if r.ErrorClass != "" {
			return failureReason(r.ErrorClass)
		}
	}
	return "官方未提供详细原因"
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
	var out strings.Builder
	fmt.Fprintf(&out, "# Memlane 失败诊断\n\n操作：%s\n状态：%s\n项目：%s\n时间：%s\n事件 ID：%s\n", event.Title, event.State, event.Project, event.At.UTC().Format(time.RFC3339Nano), event.Key)
	if endpoint, err := url.Parse(c.ServiceURL); err == nil {
		fmt.Fprintf(&out, "官方服务：%s://%s\n", endpoint.Scheme, endpoint.Host)
	}
	if c.ServiceCommit != "" {
		fmt.Fprintf(&out, "Cognee 固定源码版本：%s\n", c.ServiceCommit)
	}
	out.WriteString("\n## 官方失败记录\n")
	if len(event.Failures) == 0 {
		out.WriteString("官方未返回失败详情。\n")
	}
	for _, failure := range event.Failures {
		name := failure.Operation
		if name == "" {
			name = failure.Pipeline
		}
		fmt.Fprintf(&out, "\n操作：%s\n错误类型：%s\n说明：%s\n记录 ID：%s\n运行 ID：%s\n父操作 ID：%s\n数据组：%s\n来源：%s\n记录时间（官方 UTC）：%s\n开始：%s\n结束：%s\n", name, failure.ErrorClass, failureReason(failure.ErrorClass), failure.ID, failure.Run, failure.Parent, failure.Dataset, failure.Origin, failure.Created, failure.Started, failure.Ended)
		if candidate := failureLogCandidate(c.OfficialData, failure); candidate != "" {
			out.WriteString("\n日志候选：按错误类型及相邻时间匹配，尚未按任务 ID 精确确认；以下仅保留调用位置。\n\n```text\n" + candidate + "\n```\n")
		}
	}
	if c.OfficialData != "" {
		fmt.Fprintf(&out, "\n本机官方日志目录：%s\n", filepath.Join(c.OfficialData, "logs"))
	}
	out.WriteString("\n## 排查边界\n\n此信息来自已有官方记录和本机日志位置，不代表已修复或已重试。未包含凭据、请求内容或会话正文。请结合任务时间和调用位置分析原因，先核对当前进程与任务所有权，再决定修复或重试。\n")
	return out.String()
}

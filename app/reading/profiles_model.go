package reading

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type ProfileDocument struct {
	Dataset string `json:"dataset_id"`
	ID      string `json:"data_id"`
}

type Profile struct {
	ID        string `json:"instance_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Error     string `json:"error_code"`
	Operation string `json:"operation_id"`
	Progress  struct {
		Elapsed int    `json:"elapsed_seconds"`
		Idle    int    `json:"idle_seconds"`
		Process string `json:"process_state"`
	} `json:"progress"`
	ContentVersion string           `json:"content_hash"`
	FailureReason  string           `json:"failure_reason"`
	ErrorDetail    string           `json:"error_detail"`
	ExitCode       *int             `json:"exit_code"`
	Method         string           `json:"method"`
	CanGenerate    bool             `json:"can_generate"`
	SaveUnknown    bool             `json:"save_unconfirmed"`
	Updated        float64          `json:"updated_at"`
	Document       *ProfileDocument `json:"document"`
}

type ProfileDirectory struct {
	Profiles []Profile `json:"profiles"`
	Prompt   string    `json:"prompt"`
}

func (p Profile) Working() bool {
	return p.Status == "queued" || p.Status == "generating" || p.Status == "saving"
}

func (p Profile) Label() string {
	switch p.Status {
	case "queued":
		return "等待生成"
	case "generating":
		return "正在生成"
	case "saving":
		return "正在保存"
	case "ready":
		if p.Method == "manual" {
			return "已导入"
		}
		return "已生成"
	case "unknown":
		return "结果待核对"
	case "failed":
		return "上次导入未完成"
	default:
		if p.CanGenerate {
			return "尚未导入"
		}
		return "手动导入"
	}
}

func (p Profile) FailureLabel() string {
	if p.Status == "unknown" {
		if p.Error != "" {
			return "导入结果未确认：" + p.Error
		}
		return "导入结果未确认"
	}
	if p.Status != "failed" {
		return ""
	}
	message := p.ErrorDetail
	if message == "" {
		message = map[string]string{
			"arguments_rejected":      "Agent 不接受调用参数",
			"authentication_required": "Agent 登录失效",
			"quota_unavailable":       "Agent 额度不足",
			"connection_failed":       "无法连接 Agent 服务",
			"permission_denied":       "Agent 权限受限，未返回正文",
			"empty_response":          "Agent 未返回画像正文",
			"agent_exited":            "Agent 异常退出",
		}[p.FailureReason]
	}
	if message == "" && p.Error == "profile_generation_timeout" {
		message = "Agent 导出超时，未生成完整文档（profile_generation_timeout）"
	}
	if message == "" {
		message = p.Error
	}
	if message == "" {
		message = "未返回错误详情"
	}
	if p.ExitCode != nil {
		message += fmt.Sprintf("（退出码 %d）", *p.ExitCode)
	}
	return "导入失败：" + message
}

// UnwrapExportEnvelope unwraps an export envelope, never saved content or inner code.
func UnwrapExportEnvelope(body string) string {
	text := strings.TrimSpace(body)
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return body
	}
	opener := strings.TrimSpace(lines[0])
	marker := ""
	if strings.HasPrefix(opener, "```") {
		marker = "```"
	} else if strings.HasPrefix(opener, "~~~") {
		marker = "~~~"
	} else {
		return body
	}
	language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(opener, marker)))
	if language != "" && language != "text" && language != "plaintext" && language != "markdown" && language != "md" {
		return body
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker {
			end = i
			break
		}
	}
	if end < 0 {
		return body
	}
	inner := strings.Join(lines[1:end], "\n")
	// Only document-like exports qualify; a real fenced text/code sample stays intact.
	first := strings.TrimSpace(inner)
	if !strings.HasPrefix(first, "# ") && !strings.HasPrefix(first, "## ") {
		return body
	}
	tail := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if tail != "" {
		return inner + "\n\n" + tail
	}
	return inner
}

type ProfilesState struct {
	Directory                                     ProfileDirectory
	Selected, Body, BodyKey, BodyVersion, Message string
	Loading, Loaded, Reading, Busy, Open          bool
	Gateway, DeleteOpen                           bool
	Next                                          time.Time
	ReadAt                                        time.Time
	ReadGeneration                                uint64
	Scroll                                        ui.ScrollState
	ReaderList                                    ui.ListState
	Markdown                                      ProfileMarkdown
	Drafts                                        map[string]string
	Draft                                         string
	DraftSource                                   string
	LastIndicatorY                                float32
	DocumentKey                                   string
	DocumentEnter                                 uint64
	Copied                                        string
	CopyUntil                                     time.Time
}

func (s *ProfilesState) SelectedProfile() (Profile, bool) {
	for _, p := range s.Directory.Profiles {
		if p.ID == s.Selected {
			return p, true
		}
	}
	return Profile{}, false
}

func (s *ProfilesState) ClearBody() {
	s.Body, s.BodyKey, s.BodyVersion = "", "", ""
	s.Markdown.Clear()
	s.ReadAt = time.Time{}
	s.Reading = false
	s.ReadGeneration++
}

func (s *ProfilesState) Select(id string) bool {
	if s.Selected == id {
		return false
	}
	s.Selected, s.DeleteOpen, s.Message = id, false, ""
	s.ClearBody()
	s.Scroll, s.ReaderList = ui.ScrollState{}, ui.ListState{}
	return true
}

package reading

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type CommonDraft struct {
	Title, Body, Applicability, Source, Category string
}

func (d CommonDraft) FormatDocument() (string, error) {
	if d.Category != "preference" && d.Category != "experience" {
		return "", fmt.Errorf("请选择个人偏好或通用经验")
	}
	if strings.TrimSpace(d.Title) == "" || strings.ContainsAny(d.Title, "\r\n") || len([]rune(d.Title)) > 120 || strings.TrimSpace(d.Body) == "" {
		return "", fmt.Errorf("请填写单行标题和正文")
	}
	body := "# " + strings.TrimSpace(d.Title) + "\n\n" + strings.TrimSpace(d.Body)
	if strings.TrimSpace(d.Applicability) != "" {
		body += "\n\n## 适用条件\n\n" + strings.TrimSpace(d.Applicability)
	}
	if strings.TrimSpace(d.Source) != "" {
		body += "\n\n## 来源\n\n" + strings.TrimSpace(d.Source)
	}
	if len(body) > 256*1024 {
		return "", fmt.Errorf("通用记忆超过单条大小限制")
	}
	return body + "\n", nil
}

func CommonCategory(metadata map[string]any) string {
	kind, _ := metadata["memlane_kind"].(string)
	category, _ := metadata["category"].(string)
	if kind == "common_knowledge" && (category == "preference" || category == "experience") {
		return category
	}
	return ""
}

func ParseDocument(title, body, category, source string) CommonDraft {
	draft := CommonDraft{Category: category, Source: source}
	body = strings.TrimSpace(body)
	if first, rest, ok := strings.Cut(body, "\n"); ok && strings.HasPrefix(first, "# ") {
		draft.Title = strings.TrimPrefix(first, "# ")
		body = strings.TrimSpace(rest)
	} else {
		draft.Title = title
	}
	if suffix := "\n\n## 来源\n\n" + draft.Source; draft.Source != "" && strings.HasSuffix(body, suffix) {
		body = strings.TrimSuffix(body, suffix)
	}
	if at := strings.LastIndex(body, "\n\n## 适用条件\n\n"); at >= 0 {
		draft.Applicability = body[at+len("\n\n## 适用条件\n\n"):]
		body = body[:at]
	}
	draft.Body = body
	return draft
}

func CommonCategoryLabel(category string) string {
	if category == "preference" {
		return "个人偏好"
	}
	if category == "experience" {
		return "通用经验"
	}
	return "通用记忆"
}
func HasDraft(draft CommonDraft, completed, open, submitted, existing bool) bool {
	return !completed && (open || submitted || existing || strings.TrimSpace(draft.Title+draft.Body+draft.Applicability+draft.Source) != "")
}
func ProcessingStatus(message string) string {
	switch {
	case strings.Contains(message, "提炼已完成"):
		return "已完成"
	case strings.Contains(message, "提炼失败"):
		return "失败 · 内容保留"
	case strings.Contains(message, "提炼状态："):
		_, v, _ := strings.Cut(message, "提炼状态：")
		return v
	default:
		return "待核对"
	}
}
func FormatDocument(draft CommonDraft) (string, error) { return draft.FormatDocument() }

func BaselineSHA256(body string) [32]byte { return sha256.Sum256([]byte(body)) }

func CommonSaveStatus(pipeline string) string {
	switch strings.ToLower(strings.TrimSpace(pipeline)) {
	case "completed", "succeeded", "success", "ready":
		return "正文保存已核对，提炼已完成"
	case "failed", "error":
		return "正文保存已核对，提炼失败"
	case "running", "processing":
		return "正文保存已核对，提炼状态：进行中"
	default:
		return "正文保存已核对，提炼状态：待核对"
	}
}

type CommonEditorState[D, P, R any] struct {
	Generation                               uint64
	Completed, SaveVerified, DeleteSubmitted bool
	Processing                               string
	DeleteTarget                             P
	Draft                                    CommonDraft
	Existing                                 *D
	Open, Busy, Submitted                    bool
	Message                                  string
	Receipt                                  R
	Target                                   P
	Category                                 string
	Delete                                   *D
	Baseline                                 [32]byte
}

func (e CommonEditorState[D, P, R]) HasDraft() bool {
	return HasDraft(e.Draft, e.Completed, e.Open, e.Submitted, e.Existing != nil)
}

func (e CommonEditorState[D, P, R]) CanDiscard() bool { return !e.Busy && !e.Submitted }

func (e *CommonEditorState[D, P, R]) StartSave() bool {
	if !e.CanDiscard() {
		return false
	}
	if _, err := e.Draft.FormatDocument(); err != nil {
		e.Message = err.Error()
		return false
	}
	e.Busy = true
	e.Message = "正在提交，保存和提炼结果将分别核对…"
	return true
}

func (e *CommonEditorState[D, P, R]) ApplyVerification(saved bool, message string) {
	e.SaveVerified = e.SaveVerified || saved
	e.Processing = ProcessingStatus(message)
	e.Busy = false
	e.Message = message
}

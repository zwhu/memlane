package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

// This is a display projection of the old, fixed Memlane capture envelope.
// It neither groups documents into sessions nor changes the stored/copied raw
// text. Unknown formats stay intact; matching metadata alone cannot hide text.
type importedSourcePresentation struct {
	Body, Role, Session, Turn, Workspace, Time, TimeLabel string
}

func presentImportedSource(content records.OfficialDocumentText) importedSourcePresentation {
	out := importedSourcePresentation{Body: content.Text}
	var envelope struct {
		Import struct {
			Origin string `json:"origin"`
			Detail struct {
				Fragment string `json:"fragment"`
			} `json:"source_detail"`
		} `json:"memlane_import_v1"`
	}
	if len(content.Document.Metadata) > 65536 || json.Unmarshal(content.Document.Metadata, &envelope) != nil || envelope.Import.Origin != "raw" {
		return out
	}
	var source struct {
		Agent     string `json:"agent"`
		Role      string `json:"role"`
		Session   string `json:"session"`
		Turn      string `json:"turn"`
		Workspace string `json:"workspace"`
		Timestamp string `json:"timestamp"`
		TimeKind  string `json:"timestamp_kind"`
	}
	if json.Unmarshal([]byte(envelope.Import.Detail.Fragment), &source) != nil || source.Agent != "codex" || source.Session == "" || source.Turn == "" || source.Workspace == "" || source.Timestamp == "" {
		return out
	}
	label, role := "", ""
	if source.Role == "user" && source.TimeKind == "hook_observed" {
		label, role = "采集时间", "用户"
	}
	if source.Role == "assistant" && source.TimeKind == "message" {
		label, role = "消息时间", "Agent"
	}
	if role == "" {
		return out
	}
	prefix := fmt.Sprintf("Codex 原始对话 · %s\n会话：%s\n轮次：%s\n工作目录：%s\n%s：%s\n\n", source.Role, source.Session, source.Turn, source.Workspace, label, source.Timestamp)
	if !strings.HasPrefix(content.Text, prefix) {
		return out
	}
	return importedSourcePresentation{Body: strings.TrimPrefix(content.Text, prefix), Role: role, Session: source.Session, Turn: source.Turn, Workspace: source.Workspace, Time: source.Timestamp, TimeLabel: label}
}

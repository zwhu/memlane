package simpledesktop

import (
	"fmt"
	"strings"
)

// Stable host identity is separate from the first captured question. A paused
// capture may first see a much later turn, and the official detail is a tail.
func sessionIdentity(id string) string {
	var host, key string
	lowerID := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lowerID, "antigravity-"), strings.HasPrefix(lowerID, "agy-"):
		host, key = "Agy", id
	case strings.HasPrefix(lowerID, "gemini-"):
		host, key = "Gemini", id
	case strings.HasPrefix(lowerID, "traex-"):
		host, key = "TraeX", id
	case strings.HasPrefix(lowerID, "pi-"):
		host, key = "pi", id
	case strings.HasPrefix(lowerID, "opencode-"):
		host, key = "opencode", id
	default:
		var ok bool
		host, key, ok = strings.Cut(id, "_")
		if !ok {
			host, key = "Agent", id
		}
		switch strings.ToLower(host) {
		case "codex":
			host = "Codex"
		case "claude":
			host = "Claude Code"
		case "pi":
			host = "pi"
		case "opencode":
			host = "opencode"
		case "traex":
			host = "TraeX"
		case "antigravity", "agy":
			host = "Agy"
		case "gemini":
			host = "Gemini"
		}
	}
	r := []rune(key)
	if len(r) > 18 {
		key = string(r[:8]) + "…" + string(r[len(r)-8:])
	}
	return host + " 会话 · " + key
}

func sessionListMeta(i Item) string {
	return "最近活动 · " + displayDate(i.Date)
}

func sessionCoverage(d SessionDetail) string {
	return fmt.Sprintf("已保存 %d 轮问答 · %d 条工具记录", d.Count, d.ToolCount)
}

func sessionHasTopic(i Item) bool {
	t := strings.TrimSpace(i.Title)
	return t != "" && t != i.ID && t != "来源会话"
}

func subagentRoleLabel(s *Session) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.SubagentRole)
}

func subagentBadgeLabel(s *Session) string {
	if role := subagentRoleLabel(s); role != "" {
		return "子会话 · " + role
	}
	return "子会话"
}

func sessionFallbackTitle(i Item) string {
	if i.Session != nil && i.Session.IsSubagent {
		if role := subagentRoleLabel(i.Session); role != "" {
			return "子会话 · " + role
		}
		return "子会话 · " + sessionIdentity(i.ID)
	}
	return sessionIdentity(i.ID)
}

func sessionDisplaySummary(i Item) string {
	if i.SessionSummary == "" {
		return ""
	}
	if i.Session != nil && i.Session.IsSubagent && sessionHasTopic(i) {
		if role := subagentRoleLabel(i.Session); role != "" {
			return role + " · " + i.SessionSummary
		}
	}
	return i.SessionSummary
}

func sessionReadableParentTitle(i Item) string {
	if sessionHasTopic(i) {
		return strings.TrimSpace(i.Title)
	}
	return sessionIdentity(i.ID)
}

package app

import (
	"fmt"
	"strings"
)

type agentBrandSpec struct {
	kind    string
	display string
	aliases []string
}

var knownAgentBrands = []agentBrandSpec{
	{kind: "trae_ide", display: "TRAE CN", aliases: []string{"trae_ide", "trae-ide", "trae_cn", "trae-cn", "traecn"}},
	{kind: "traex", display: "TRAE CLI", aliases: []string{"traex", "trae_cli", "trae-cli", "traecli"}},
	{kind: "claude_code", display: "Claude Code", aliases: []string{"claude_code", "claude-code", "claudecode", "claude"}},
	{kind: "codex", display: "Codex", aliases: []string{"codex"}},
	{kind: "antigravity", display: "Agy", aliases: []string{"antigravity", "agy"}},
	{kind: "gemini", display: "Gemini", aliases: []string{"gemini"}},
	{kind: "pi", display: "pi", aliases: []string{"pi"}},
	{kind: "opencode", display: "opencode", aliases: []string{"opencode"}},
	{kind: "cursor", display: "Cursor", aliases: []string{"cursor"}},
}

func agentBrandFromID(raw string) (kind, display string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return "", "", false
	}
	for _, spec := range knownAgentBrands {
		for _, alias := range spec.aliases {
			if lower == alias {
				return spec.kind, spec.display, true
			}
			if strings.HasPrefix(lower, alias) {
				next := lower[len(alias)]
				if next == '-' || next == '_' {
					return spec.kind, spec.display, true
				}
			}
		}
	}
	return "", "", false
}

func shortenSessionKey(key string) string {
	r := []rune(key)
	if len(r) > 18 {
		return string(r[:8]) + "…" + string(r[len(r)-8:])
	}
	return key
}

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
	case strings.HasPrefix(lowerID, "trae_ide-"):
		host, key = "TRAE CN", id
	case strings.HasPrefix(lowerID, "traex-"):
		host, key = "TRAE CLI", id
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
			host = "TRAE CLI"
		case "antigravity", "agy":
			host = "Agy"
		case "gemini":
			host = "Gemini"
		case "cursor":
			host = "Cursor"
		}
	}
	return host + " 会话 · " + shortenSessionKey(key)
}

func sessionIdentityWithAgent(id, agentID string) string {
	id = strings.TrimSpace(id)
	if _, display, ok := agentBrandFromID(agentID); ok {
		key := id
		if prefix, rest, hasUnderscore := strings.Cut(id, "_"); hasUnderscore && rest != "" {
			if _, prefixDisplay, prefixOK := agentBrandFromID(prefix); prefixOK && prefixDisplay == display {
				key = rest
			}
		}
		return display + " 会话 · " + shortenSessionKey(key)
	}
	return sessionIdentity(id)
}

func itemSessionIdentity(i Item) string {
	return sessionIdentityWithAgent(i.ID, itemSourceAgentID(i))
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
		return "子会话 · " + itemSessionIdentity(i)
	}
	return itemSessionIdentity(i)
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
	return itemSessionIdentity(i)
}

func sessionHistoryMissing(i Item) bool {
	return i.Detail != nil && i.Detail.HistoryAvailable != nil && !*i.Detail.HistoryAvailable
}

func sessionSourceAgentDisplay(i Item) string {
	name := sourceAgentName(i)
	if name != "Agent 未记录" {
		return name
	}
	if agent := itemSourceAgentID(i); agent != "" {
		return agent
	}
	return name
}

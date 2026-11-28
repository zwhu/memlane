package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

func agentBrandIcon(kind string) *ui.SVG          { return settings.AgentBrandIconFor(kind, "") }
func agentBrandIconFor(kind, name string) *ui.SVG { return settings.AgentBrandIconFor(kind, name) }

func agentLogo(c *ui.Context, mark *ui.SVG, size float32) ui.Element {
	return ui.Image(c, mark).Size(size, size).Fit(ui.Contain).TextColor(tok.Text1)
}

func sourceAgentLabel(c *ui.Context, item Item) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		agentLogo(c, recentAgentIcon(item), 13)
		name := sourceAgentName(item)
		if name == "Agent 未记录" {
			name = "Agent"
		}
		if item.Session != nil && item.Session.IsSubagent {
			if role := subagentRoleLabel(item.Session); role != "" {
				name += " · " + role
			}
		}
		homeText(c, name, homeMeta)
	})
}

func itemSourceAgentID(item Item) string {
	if item.Session != nil {
		if agent := strings.TrimSpace(item.Session.Agent); agent != "" {
			return agent
		}
	}
	if agent := strings.TrimSpace(item.SourceAgent); agent != "" {
		return agent
	}
	if item.Document != nil {
		if agent, _ := item.Document.Metadata["agent_id"].(string); strings.TrimSpace(agent) != "" {
			return strings.TrimSpace(agent)
		}
	}
	return ""
}

func itemSourceSession(item Item) string {
	session := ""
	if item.Document != nil {
		session = documentSession(*item.Document)
	}
	if session == "" {
		session = strings.TrimSpace(item.Source)
	}
	if item.Session != nil {
		session = item.Session.ID
	}
	return session
}

// Use the retained source agent/session identity, never the project name or memory text.
func recentAgentIcon(item Item) *ui.SVG {
	if kind, _, ok := agentBrandFromID(itemSourceAgentID(item)); ok {
		return agentBrandIcon(kind)
	}
	session := itemSourceSession(item)
	if session == "" {
		return homeDocument
	}
	name, _, ok := strings.Cut(sessionIdentity(session), " 会话")
	if !ok {
		return homeDocument
	}
	if kind, found := map[string]string{
		"Codex":       "codex",
		"TRAE CLI":    "traex",
		"TRAE IDE":    "trae_ide",
		"TRAE CN":     "trae_ide",
		"pi":          "pi",
		"Claude Code": "claude_code",
		"opencode":    "opencode",
		"Cursor":      "cursor",
		"cursor":      "cursor",
		"Agy":         "antigravity",
		"Gemini":      "gemini",
	}[name]; found {
		return agentBrandIcon(kind)
	}
	return homeDocument
}

func sourceAgentName(item Item) string {
	if _, display, ok := agentBrandFromID(itemSourceAgentID(item)); ok {
		return display
	}
	session := itemSourceSession(item)
	if session == "" {
		return "Agent 未记录"
	}
	name, _, ok := strings.Cut(sessionIdentity(session), " 会话")
	if !ok || name == "Agent" {
		return "Agent 未记录"
	}
	return name
}

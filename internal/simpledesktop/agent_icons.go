package simpledesktop

import (
	"embed"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed agent_icons/*.svg
var agentIconFiles embed.FS

func agentOriginalIcon(kind string) *ui.SVG { return agentBrandIcon(kind) }

func agentLogo(c *ui.Context, mark *ui.SVG, size float32) ui.Element {
	return ui.Image(c, mark).Size(size, size).Fit(ui.Contain).TextColor(tok.Text1)
}

var agentBrandIcons = func() map[string]*ui.SVG {
	icons := make(map[string]*ui.SVG)
	for kind, file := range map[string]string{
		"codex": "codex-color", "pi": "pi", "traex": "trae", "trae_ide": "trae",
		"claude_code": "claudecode-color", "claude": "claude-color", "opencode": "opencode", "cursor": "cursor",
		"chatgpt": "openai", "openai": "openai", "gemini": "gemini-color", "antigravity": "gemini-color", "grok": "grok", "deepseek": "deepseek-color",
	} {
		data, err := agentIconFiles.ReadFile("agent_icons/" + file + ".svg")
		if err != nil {
			panic(err)
		}
		icons[kind] = ui.MustParseSVG(data)
	}
	icons["agy"] = icons["antigravity"]
	return icons
}()

func normalizeAgentIconKind(kind string) string {
	switch key := strings.ToLower(strings.TrimSpace(kind)); key {
	case "traecn", "trae cn", "trae ide":
		return "trae_ide"
	case "agy":
		return "antigravity"
	default:
		return key
	}
}

func agentBrandIcon(kind string) *ui.SVG {
	if icon := agentBrandIcons[normalizeAgentIconKind(kind)]; icon != nil {
		return icon
	}
	return homeDocument
}

// Resolve manual profile sources by their explicit name, never by message text.
func agentBrandIconFor(kind, name string) *ui.SVG {
	if icon := agentBrandIcons[normalizeAgentIconKind(kind)]; icon != nil {
		return icon
	}
	key := strings.ToLower(strings.TrimSpace(agentDisplayName(name)))
	key = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(key)
	switch key {
	case "claudecode":
		key = "claude_code"
	case "trae", "traecli":
		key = "traex"
	case "traeide", "traecn":
		key = "trae_ide"
	case "agy", "agycli", "antigravitycli", "geminiapp":
		key = "antigravity"
	case "geminicli":
		key = "gemini"
	}
	if icon := agentBrandIcons[key]; icon != nil {
		return icon
	}
	return homeDocument
}
func accessAgentIcon(agent AccessAgent) *ui.SVG {
	kind := ""
	if agent.Installation != nil {
		kind = agent.Installation.HostKind
	}
	return agentBrandIconFor(kind, agent.Name)
}
func sourceAgentLabel(c *ui.Context, item Item) {
	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
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

func itemSourceSession(item Item) string {
	session := ""
	if item.Document != nil {
		session = item.Document.session()
	}
	if item.Session != nil {
		session = item.Session.ID
	}
	return session
}

// Use the retained source session identity, never the project name or memory text.
func recentAgentIcon(item Item) *ui.SVG {
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
		"cursor":      "cursor",
		"Agy":         "antigravity",
		"Gemini":      "gemini",
	}[name]; found {
		return agentBrandIcon(kind)
	}
	return homeDocument
}

func sourceAgentName(item Item) string {
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

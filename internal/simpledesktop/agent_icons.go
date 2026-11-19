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
	return ui.Image(c, mark).Size(size, size).Fit(ui.Contain).TextColor(ui.Hex("#000000"))
}

var agentBrandIcons = func() map[string]*ui.SVG {
	icons := make(map[string]*ui.SVG)
	for kind, file := range map[string]string{
		"codex": "codex-color", "pi": "pi", "traex": "trae-color",
		"claude_code": "claudecode-color", "claude": "claude-color", "opencode": "opencode", "cursor": "cursor",
		"chatgpt": "openai", "openai": "openai", "gemini": "gemini-color", "antigravity": "gemini-color", "grok": "grok", "deepseek": "deepseek-color",
	} {
		data, err := agentIconFiles.ReadFile("agent_icons/" + file + ".svg")
		if err != nil {
			panic(err)
		}
		icons[kind] = ui.MustParseSVG(data)
	}
	return icons
}()

func agentBrandIcon(kind string) *ui.SVG {
	if icon := agentBrandIcons[kind]; icon != nil {
		return icon
	}
	return homeDocument
}

// Resolve manual profile sources by their explicit name, never by message text.
func agentBrandIconFor(kind, name string) *ui.SVG {
	if icon := agentBrandIcons[strings.ToLower(kind)]; icon != nil {
		return icon
	}
	key := strings.ToLower(strings.TrimSpace(agentDisplayName(name)))
	key = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(key)
	if key == "claudecode" {
		key = "claude_code"
	}
	if key == "trae" {
		key = "traex"
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
		homeText(c, name, homeMeta)
	})
}

// Use the retained source session identity, never the project name or memory text.
func recentAgentIcon(item Item) *ui.SVG {
	session := ""
	if item.Document != nil {
		session = item.Document.session()
	}
	if item.Session != nil {
		session = item.Session.ID
	}
	if session == "" {
		return homeDocument
	}
	label := sessionIdentity(session)
	for name, kind := range map[string]string{"Codex": "codex", "TraeX": "traex", "pi": "pi", "Claude Code": "claude_code", "opencode": "opencode", "cursor": "cursor"} {
		if strings.HasPrefix(label, name+" 会话") {
			return agentBrandIcon(kind)
		}
	}
	return homeDocument
}

func sourceAgentName(item Item) string {
	session := ""
	if item.Document != nil {
		session = item.Document.session()
	}
	if item.Session != nil {
		session = item.Session.ID
	}
	if session == "" {
		return "Agent 未记录"
	}
	name, _, ok := strings.Cut(sessionIdentity(session), " 会话")
	if !ok || name == "Agent" {
		return "Agent 未记录"
	}
	return name
}

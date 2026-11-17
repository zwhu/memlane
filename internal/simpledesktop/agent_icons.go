package simpledesktop

import (
	"embed"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed agent_icons/*.svg
var agentIconFiles embed.FS

var agentBrandIcons = func() map[string]*ui.SVG {
	icons := make(map[string]*ui.SVG)
	for kind, file := range map[string]string{
		"codex": "codex", "pi": "pi", "traex": "trae",
		"claude_code": "claude", "opencode": "opencode", "cursor": "cursor",
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

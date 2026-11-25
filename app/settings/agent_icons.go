package settings

import (
	"embed"
	"strings"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

//go:embed agent_icons/*.svg
var agentIconFiles embed.FS

func agentOriginalIcon(kind string) *ui.SVG { return agentBrandIcon(kind) }

func agentLogo(tok *kitui.Tokens, c *ui.Context, mark *ui.SVG, size float32) ui.Element {
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
	return kitui.IconDoc
}

// Resolve manual profile sources by their explicit name, never by message text.
func AgentBrandIconFor(kind, name string) *ui.SVG {
	if icon := agentBrandIcons[normalizeAgentIconKind(kind)]; icon != nil {
		return icon
	}
	key := strings.ToLower(strings.TrimSpace(AgentDisplayName(name)))
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
	return kitui.IconDoc
}

func accessAgentIcon(agent AccessAgent) *ui.SVG {
	kind := ""
	if agent.Installation != nil {
		kind = agent.Installation.HostKind
	}
	return AgentBrandIconFor(kind, agent.Name)
}

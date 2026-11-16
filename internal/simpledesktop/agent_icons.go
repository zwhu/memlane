package simpledesktop

import (
	"embed"

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

package settings

import (
	"embed"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

//go:embed dependency_icons/*.svg
var dependencyIconFiles embed.FS

var dependencyBrandIcons = func() map[string]*ui.SVG {
	icons := map[string]*ui.SVG{}
	for id, file := range map[string]string{"python": "python", "uv": "uv", "runtime": "runtime", "cognee-source": "cognee", "embedding": "huggingface", "agent-plugin": "agent_plugin"} {
		b, err := dependencyIconFiles.ReadFile("dependency_icons/" + file + ".svg")
		if err != nil {
			panic(err)
		}
		icons[id] = ui.MustParseSVG(b)
	}
	return icons
}()

func dependencyMark(tok *kitui.Tokens, c *ui.Context, id string) {
	if mark := dependencyBrandIcons[id]; mark != nil {
		ui.Image(c, mark).Size(22, 22).Fit(ui.Contain)
		return
	}
	ui.Icon(c, kitui.IconCommon).FontSize(20).TextColor(tok.Text2)
}

func dependencyDisplayName(r bootstrap.Requirement) string {
	switch r.ID {
	case "runtime":
		return "运行库"
	case "embedding":
		return "向量模型"
	case "uv":
		return "uv"
	default:
		return r.Name
	}
}

func dependencyDisplayVersion(item bootstrap.Item, environment *bootstrap.Environment) string {
	r := item.Requirement
	switch r.ID {
	case "python", "uv":
		if item.Observation.Version != "" {
			return item.Observation.Version
		}
		return r.Version
	case "cognee-source":
		if len(r.Version) >= 7 {
			if environment != nil && environment.CogneeSDKVersion != "" {
				return environment.CogneeSDKVersion + " · " + r.Version[:7]
			}
			return r.Version[:7]
		}
		return r.Version
	case "embedding":
		return "BGE Small ZH v1.5"
	case "agent-plugin":
		parts := strings.SplitN(r.Version, " · ", 3)
		if len(parts) >= 2 {
			return strings.Join(parts[:2], " · ")
		}
		return r.Version
	case "runtime":
		if environment != nil {
			versions, err := bootstrap.LockedVersions(environment.Runtime.Lock)
			if err == nil {
				databases := "LanceDB " + versions["lancedb"]
				if version := versions["ladybug"]; version != "" {
					databases += " · Ladybug " + version
				}
				return databases + " · FastEmbed " + versions["fastembed"]
			}
		}
		if len(r.Digest) >= 8 {
			return "锁定版 " + r.Digest[:8]
		}
		return r.Version
	default:
		return r.Version
	}
}

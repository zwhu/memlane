package settings

import (
	"context"
	"net/url"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type ToolGraphSettings struct {
	GlobalEnabled bool   `json:"global_enabled"`
	Override      *bool  `json:"override"`
	Effective     bool   `json:"Effective"`
	Source        string `json:"source"`
}

func (c ProjectControlClient) configureToolGraph(ctx context.Context, revision int64, project string, enabled *bool) (AccessDirectory, error) {
	path := "/v1/tool-graph/settings"
	if project != "" {
		path = "/v1/projects/" + url.PathEscape(project) + "/tool-graph/settings"
	}
	var result AccessDirectory
	err := c.request(ctx, "PUT", path, map[string]any{"expected_revision": revision, "enabled": enabled}, &result)
	return result, err
}

func (a *Controller) saveToolGraphSetting(project string, enabled *bool) {
	if a.isSetupOnlyRuntime() {
		return
	}
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		return client.configureToolGraph(ctx, revision, project, enabled)
	})
}

func (a *Controller) globalToolGraphSettings(c *ui.Context) {
	tok := a.tokens()
	if a.isSetupOnlyRuntime() {
		return
	}
	enabled := a.access.Directory.ToolGraph.GlobalEnabled
	desc := "开启后消耗模型额度；关闭不影响消息采集和记忆提炼，项目可单独覆盖"
	if a.access.Message != "" {
		message := a.access.Message
		if message == "设置已保存；生效状态以官方核对结果为准" {
			message = "已保存，后续任务按新设置执行。"
		}
		desc = message
	}
	kitui.Row(c, tok, kitui.RowOptions{
		Key:   "settings-global-tool-graph",
		Title: "自动构建关系图谱",
		Desc:  desc,
		Error: a.access.DirectoryError,
		Control: func() {
			if !a.access.Loaded {
				kitui.SmallButton(c, tok, "读取图谱设置", kitui.IconRefresh, a.RefreshProjectDirectory)
			}
			kitui.Switch(c, tok, "global-tool-graph", &enabled, "", func() {
				if !a.access.Loaded || a.access.Busy {
					return
				}
				a.saveToolGraphSetting("", &enabled)
			}).Label("自动构建关系图谱").Disabled(!a.access.Loaded || a.access.Busy)
		},
	})
}

func (a *Controller) projectToolGraphSettings(c *ui.Context, project AccessProject) {
	tok := a.tokens()
	if a.isSetupOnlyRuntime() {
		kitui.Group(c, tok, "工具记录关系图谱", "", nil, func() {
			kitui.Row(c, tok, kitui.RowOptions{
				Key:     "project-tool-graph-setup-" + project.ID,
				Title:   "待配置记忆提炼",
				Desc:    "配置并启用记忆提炼后可创建工具关系图谱",
				Control: func() { kitui.SmallButton(c, tok, "前往配置记忆提炼", nil, a.openSettingsFromDeferredBanner) },
			})
		})
		return
	}
	selected := 0
	if project.ToolGraph.Override != nil {
		selected = 2
		if *project.ToolGraph.Override {
			selected = 1
		}
	}
	state := "当前关闭"
	if project.ToolGraph.Effective {
		state = "当前开启"
	}
	kitui.Group(c, tok, "工具记录关系图谱", "", func() {
		kitui.SmallButton(c, tok, "创建工具图谱", nil, func() { a.PreviewToolGraph(project.ID) })
	}, func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key:   "project-tool-graph-row-" + project.ID,
			Title: "自动构建策略",
			Desc:  state + " · 更改仅影响后续任务，已有图谱保留",
			Control: func() {
				kitui.Segmented(c, tok, "project-tool-graph-"+project.ID, "关系图谱构建策略", &selected, []string{"跟随全局", "开启", "关闭"}, func() {
					if a.access.Busy {
						return
					}
					var value *bool
					if selected != 0 {
						enabled := selected == 1
						value = &enabled
					}
					a.saveToolGraphSetting(project.ID, value)
				})
			},
		})
	})
}

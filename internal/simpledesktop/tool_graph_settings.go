package simpledesktop

import (
	"context"
	"net/url"

	"github.com/egoist/mygo/ui"
)

type ToolGraphSettings struct {
	GlobalEnabled bool   `json:"global_enabled"`
	Override      *bool  `json:"override"`
	Effective     bool   `json:"effective"`
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

func (a *App) saveToolGraphSetting(project string, enabled *bool) {
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		return client.configureToolGraph(ctx, revision, project, enabled)
	})
}

func (a *App) globalToolGraphSettings(c *ui.Context) {
	if a.Config.GenerationSetupOnly {
		return
	}
	settingsListSurface(c, func() {
		settingsListRow(c, func() {
			homeText(c, "关系图谱", homeBody).Width(100).FontSize(13).FontWeight(500).TextColor(homeInk)
			enabled := a.access.Directory.ToolGraph.GlobalEnabled
			ui.Checkbox(c.Key("global-tool-graph"), &enabled, "自动构建关系图谱").Disabled(!a.access.Loaded || a.access.Busy).OnChange(func() { a.saveToolGraphSetting("", &enabled) })
			ui.Spacer(c)
			homeText(c, "开启后消耗模型额度", homeMeta).SingleLine().Tooltip("关闭不影响消息保存和记忆提炼；项目可单独设置。")
		})
		if a.access.Message != "" {
			message := a.access.Message
			if message == "设置已保存；生效状态以官方核对结果为准" {
				message = "已保存，后续任务按新设置执行。"
			}
			homeText(c, message, homeMeta)
		}
		if a.access.DirectoryError != "" {
			homeText(c, a.access.DirectoryError, homeMeta)
		}
		if !a.access.Loaded {
			refreshAction(c, "读取图谱设置", a.RefreshProjectDirectory)
		}
	})
}

func (a *App) projectToolGraphSettings(c *ui.Context, project AccessProject) {
	ui.Column(c.Key("project-tool-graph-card-"+project.ID)).FillWidth().Gap(14).Padding(22).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
			homeText(c, "工具记录关系图谱", homeHeading).FontSize(18)
			ui.Spacer(c)
			textButton(c, "创建工具图谱", nil).OnClick(func() { a.previewToolGraph(project.ID) })
		})
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
		ui.Segmented(c.Key("project-tool-graph-"+project.ID), &selected, "跟随全局", "开启", "关闭").Width(280).AlignSelf(ui.Start).Disabled(a.access.Busy).Tooltip(state + "；更改仅影响后续任务，已有图谱保留。").OnChange(func() {
			var value *bool
			if selected != 0 {
				enabled := selected == 1
				value = &enabled
			}
			a.saveToolGraphSetting(project.ID, value)
		})
	})
}

package simpledesktop

import (
	"context"
	"github.com/egoist/mygo/ui"
)

func policySettingRow(c *ui.Context, title, explanation, value string, inherited bool, control func()) {
	ui.Column(c).FillWidth().Padding(16).Gap(8).Background(ui.Hex("#f8fafc")).Border(1, homeBorder).Radius(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			homeText(c, title, homeHeading).FontSize(13)
			ui.Spacer(c)
			source := "独立设置"
			if inherited {
				source = "继承全局默认"
			}
			homeText(c, source, homeMeta)
		})
		homeText(c, explanation, homeMeta).LineHeight(1.6)
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			control()
			homeText(c, "已保存："+value, homeMeta)
		})
	})
}
func settingsTabs(c *ui.Context, current *string, labels []string) {
	if *current == "" {
		*current = labels[0]
	}
	ui.Row(c).Gap(6).Shrink(0).Padding(18, 28).BorderWidth(0, 0, 1, 0).BorderColor(homeBorder).Children(func() {
		for _, text := range labels {
			label := text
			bg, ink := ui.Transparent, homeMuted
			if *current == label {
				bg, ink = ui.Hex("#eaf1f8"), homeInk
			}
			ui.Button(c, label).Label(label).FontSize(12).FontWeight(400).Padding(8, 14).Background(bg).TextColor(ink).BorderWidth(0).Radius(7).OnClick(func() { *current = label })
		}
	})
}
func settingsCard(c *ui.Context, title, description string, content func()) {
	ui.Column(c).FillWidth().Gap(14).Padding(22).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
		homeText(c, title, homeHeading).FontSize(18)
		if description != "" {
			homeText(c, description, homeMeta).LineHeight(1.7)
		}
		content()
	})
}
func (a *App) globalSettingsPage(c *ui.Context) {
	settingsTabs(c, &a.access.SettingsSection, []string{"服务与连接", "项目关联", "高级"})
	ui.Scroll(c.Key("global-settings-" + a.access.SettingsSection)).Grow(1).MinHeight(0).Padding(28).Background(ui.Hex("#f8fafc")).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(900).Gap(20).Children(func() {
			switch a.access.SettingsSection {
			case "服务与连接":
				settingsCard(c, "服务状态", "显示最近一次连接检查结果；服务地址和模型来自当前开发配置。", func() {
					homeText(c, "Cognee · "+a.service, homeHeading).FontSize(13)
					homeText(c, a.Config.ServiceURL, homeMeta)
					homeText(c, "Codex 订阅推理 · "+a.Config.Model, homeHeading).FontSize(13)
					homeText(c, a.completion, homeMeta)
					if a.service != "已连接" {
						commonButton(c, "重新检查连接", false, a.health)
					}
				})
				if a.Config.ProjectControl != nil {
					settingsCard(c, "本机 Agent", "连接到当前电脑；具体项目的读写权限请进入对应项目设置。", func() { a.codexConnectionSettings(c) })
				}
			case "项目关联":
				settingsCard(c, "新建与关联", "这里管理项目归属；各项目的权限在左侧项目行右侧的 ⋯ 菜单中设置。", func() {
					if a.Config.ProjectControl != nil {
						a.projectRegistration(c)
					} else {
						for idx, p := range a.Config.Projects {
							index := idx
							commonButton(c, p.Name+" · 修改关联", false, func() { copy := a.Config.Projects[index]; a.editProject = &copy; a.editIndex = index })
						}
						commonButton(c, "关联已有官方项目", false, func() { a.editProject = &Project{}; a.editIndex = -1 })
						a.projectEditor(c)
					}
				})
			case "高级":
				if a.Config.ProjectControl != nil {
					settingsCard(c, "未保存的会话", "会话未成功保存时，在这里检查并手动处理。", func() {
						if !a.access.PendingExpanded {
							commonButton(c, "检查未保存的会话", false, func() { a.access.PendingExpanded = true })
						} else {
							for _, project := range a.access.Directory.Projects {
								for _, agent := range a.access.Directory.Agents {
									homeText(c, project.Name+" · "+agent.Name, homeHeading).FontSize(13)
									a.pendingMaterialsEditor(c, project, agent)
								}
							}
						}
					})
				}
				if a.Config.ProjectControl != nil {
					settingsCard(c, "通用知识存储", "仅在存储未准备或权限异常时核对。", func() { a.commonDatasetSettings(c) })
				}
				settingsCard(c, "开发运行", "关闭窗口后后台服务继续运行。电脑重启后通过开发启动器重新启动。", func() {
					homeText(c, "python3 scripts/dev.py", homeBody)
					homeText(c, "服务与模型配置变更后，重新启动后台生效。向量模型由 Cognee 官方配置管理。", homeMeta).LineHeight(1.7)
				})
			}
		})
	})
}
func (a *App) projectSettingsPage(c *ui.Context) {
	var project *AccessProject
	for _, row := range a.access.Directory.Projects {
		if row.ID == a.access.OpenProject {
			p := row
			project = &p
			break
		}
	}
	if project == nil {
		homeText(c, "项目设置尚未载入", homeHeading).Margin(28)
		commonButton(c, "重新读取项目", false, a.RefreshProjectDirectory)
		return
	}
	p := *project
	ui.Scroll(c.Key("project-settings-" + p.ID)).Grow(1).MinHeight(0).Padding(28).Background(ui.Hex("#f8fafc")).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(900).Gap(18).Children(func() {
			if !p.permissionsReady(a.access.Directory.Agents...) {
				homeText(c, "权限尚未同步完成", homeMeta)
				commonButton(c, "核对权限", false, func() {
					a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
						return client.reconcile(ctx, revision, p.ID)
					})
				})
			}
			if a.access.Message != "" && a.access.Message != "设置已保存；生效状态以官方核对结果为准" {
				homeText(c, a.access.Message, homeMeta).LineHeight(1.7)
			}
			if a.access.DirectoryError != "" {
				homeText(c, a.access.DirectoryError, homeMeta).LineHeight(1.7)
			}
			if len(a.access.Directory.Agents) == 0 {
				homeText(c, "尚无已连接的 Agent；请先在全局设置中连接。", homeMeta)
			}
			for _, row := range a.access.Directory.Agents {
				agent := row
				settingsCard(c, agent.Name, "", func() { a.agentPolicyEditor(c, p, agent) })
			}
		})
	})
}

var emptyReadingArt = homeSVG(`<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h3"/>`)

func (a *App) emptyReadingBackground(c *ui.Context) {
	ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Column(c).Width(160).Height(160).AlignItems(ui.Center).Justify(ui.Center).Background(ui.Hex("#f5f8fb")).Radius(80).Children(func() {
			ui.Icon(c, emptyReadingArt).FontSize(70).TextColor(ui.Hex("#c6d5e3"))
		})
	})
}

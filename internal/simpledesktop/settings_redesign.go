package simpledesktop

import (
	"context"
	"github.com/egoist/mygo/ui"
)

func policySettingRow(c *ui.Context, title, explanation, value string, inherited bool, control func()) {
	ui.Column(c).FillWidth().Padding(sp4).Gap(sp2).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
			homeText(c, title, homeHeading).FontSize(fsBody)
			ui.Spacer(c)
			source := "独立设置"
			if inherited {
				source = "继承全局默认"
			}
			homeText(c, source, homeMeta)
		})
		homeText(c, explanation, homeMeta).LineHeight(1.6)
		ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
			control()
			homeText(c, "已保存："+value, homeMeta)
		})
	})
}
func settingsTabs(c *ui.Context, current *string, labels []string) {
	selected := 0
	for n, label := range labels {
		if *current == label {
			selected = n
		}
	}
	// Retired tabs held by a hot-reloaded window return to a real page.
	*current = labels[selected]
	displayLabels := append([]string(nil), labels...)
	for n, label := range displayLabels {
		if label == "知识处理" {
			displayLabels[n] = "记忆处理"
		}
	}
	ui.Row(c).FillWidth().Shrink(0).Padding(0, sp8).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		v4UnderlineTabs(c, "settings-tabs", "设置分类", &selected, displayLabels, func() { *current = labels[selected] })
	})
}
func settingsCard(c *ui.Context, title, description string, content func()) {
	settingsListSurface(c, func() {
		ui.Column(c).FillWidth().Gap(sp3).Padding(10, 0).Children(func() {
			homeText(c, title, homeHeading).FontSize(fsReading).FontWeight(600)
			if description != "" {
				homeText(c, description, homeMeta).LineHeight(1.6)
			}
			content()
		})
	})
}
func (a *App) globalSettingsPage(c *ui.Context) {
	switch a.access.SettingsSection {
	case "服务与连接":
		a.access.SettingsSection = "服务"
	case "知识处理":
		a.access.SettingsSection = "记忆处理"
	}
	settingsTabs(c, &a.access.SettingsSection, []string{"记忆处理", "Agent 与模型", "服务"})
	ui.Scroll(c.Key("global-settings-"+a.access.SettingsSection)).Grow(1).MinHeight(0).Padding(sp5, sp8, sp6, sp8).Background(tok.Bg).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1100).Gap(16).Children(func() {
			switch a.access.SettingsSection {
			case "记忆处理":
				a.generationConfigurationFor(c, false, true)
				languageSettingsCard(c)
				a.appearanceSettingsCard(c)
				a.globalToolGraphSettings(c)
			case "Agent 与模型":
				ui.Column(c).FillWidth().Gap(24).Children(func() {
					if a.Config.ProjectControl != nil {
						a.agentConnectionSettings(c)
					}
					a.generationManagement(c)
				})
			case "服务":
				ui.Column(c).FillWidth().Gap(24).Children(func() { a.environmentSetup(c, false); a.serviceConnectionOverview(c) })
			}
		})
	})
}

// Open the project's inline Management tab while preserving caller location.
func (a *App) openProjectSettings(index int) {
	if index < 0 || index >= len(a.Config.Projects) {
		return
	}
	id, origin := a.Config.Projects[index].ID, a.readingLocation()
	a.navigateKind("知识", index, "管理")
	a.access.OpenProject, a.settingsOrigin = id, origin
	for _, p := range a.access.Directory.Projects {
		if p.ID == id {
			a.projectQueueStateFor(p).Reader = nil
			break
		}
	}
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
		refreshAction(c, "重新读取项目", a.RefreshProjectDirectory)
		return
	}
	p := *project
	q := a.projectQueueStateFor(p)
	if q.Reader != nil {
		a.queuedDocumentDetail(c, p, q)
		return
	}
	ui.Scroll(c.Key("project-settings-" + p.ID)).Grow(1).MinHeight(0).TrackScroll(&q.Scroll).Padding(sp8).Background(tok.Bg).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(900).Gap(sp5).Children(func() {
			ui.Column(c).FillWidth().Padding(sp4).Gap(10).Background(tok.BgElevated).Border(1, tok.Border).Radius(radius).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
					homeText(c, p.Name, homeHeading).FontSize(fsReading).Grow(1).MinWidth(0).SingleLine().Tooltip(p.Name)
					textButton(c, "编辑名称", nil).OnClick(func() { a.openProjectCreation(p.ID) })
				})
				for _, path := range p.Aliases {
					if path == "" {
						continue
					}
					copyableText(c, path, path, "复制项目路径", homePath, func() { c.WriteClipboard(path) })
				}
			})
			if !p.permissionsReady(a.access.Directory.Agents...) {
				ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
					homeText(c, "权限尚未同步完成", homeMeta)
					commonButton(c, "核对权限", false, func() {
						a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
							return client.reconcile(ctx, revision, p.ID)
						})
					})
				})
			}
			if len(a.access.CheckboxPending) == 0 && a.access.Message != "" && a.access.Message != "设置已保存；生效状态以官方核对结果为准" {
				homeText(c, a.access.Message, homeMeta).LineHeight(1.7)
			}
			if a.access.DirectoryError != "" {
				homeText(c, a.access.DirectoryError, homeMeta).LineHeight(1.7)
			}
			if len(a.access.Directory.Agents) == 0 {
				homeText(c, "尚无已连接的 Agent；请先在全局设置中连接。", homeMeta)
			}
			homeText(c, "权限设置", homeHeading).FontSize(fsReading)
			ui.Column(c).FillWidth().Gap(0).Background(tok.BgElevated).Border(1, tok.Border).Radius(radius).Children(func() {
				for i, row := range a.access.Directory.Agents {
					if i > 0 {
						ui.Column(c).FillWidth().Height(1).Background(tok.Border)
					}
					a.projectPermissionCard(c, p, row)
				}
			})
			a.projectToolGraphSettings(c, p)
			a.projectQueueSection(c, p)
		})
	})
}

var emptyReadingArt = homeSVG(`<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h3"/>`)

func (a *App) emptyReadingBackground(c *ui.Context) {
	v4EmptyState(c, "empty-reading-art", "", "")
}

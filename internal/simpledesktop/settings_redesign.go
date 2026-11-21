package simpledesktop

import (
	"context"

	"github.com/egoist/mygo/ui"
)

func policySettingRow(c *ui.Context, title, explanation, value string, inherited bool, control func()) {
	source := "独立设置"
	if inherited {
		source = "继承全局默认"
	}
	v4Row(c, v4RowOptions{
		Title: title,
		Desc:  explanation,
		TitleExtra: func() {
			v4Badge(c, source, "")
		},
		Control: func() {
			control()
			homeText(c, "已保存："+value, homeMeta).SingleLine()
		},
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
		if label == "知识处理" || label == "记忆处理" {
			displayLabels[n] = "记忆提炼"
		}
	}
	ui.Row(c).FillWidth().Shrink(0).Padding(0, sp8).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		v4UnderlineTabs(c, "settings-tabs", "设置分类", &selected, displayLabels, func() { *current = labels[selected] })
	})
}

func settingsCard(c *ui.Context, title, description string, content func()) {
	v4Group(c, title, description, nil, func() {
		ui.Column(c).FillWidth().Gap(sp2).Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(content)
	})
}

func (a *App) globalSettingsPage(c *ui.Context) {
	switch a.access.SettingsSection {
	case "服务与连接":
		a.access.SettingsSection = "服务"
	case "知识处理", "记忆处理":
		a.access.SettingsSection = "记忆提炼"
	}
	settingsTabs(c, &a.access.SettingsSection, []string{"记忆提炼", "Agent 与模型", "服务"})
	ui.Scroll(c.Key("global-settings-"+a.access.SettingsSection)).Grow(1).MinHeight(0).Padding(sp6, sp8, sp10, sp8).Background(tok.Bg).Children(func() {
		v4GroupWrap(c, func() {
			switch a.access.SettingsSection {
			case "记忆提炼":
				a.generationConfigurationFor(c, false, true)
				v4Group(c, "提炼与记忆策略", "", nil, func() {
					a.globalToolGraphSettings(c)
					languageSettingsCard(c)
				})
				a.v4AppearanceSettingsGroup(c)
			case "Agent 与模型":
				if a.Config.ProjectControl != nil {
					a.agentConnectionSettings(c)
				}
				a.generationManagement(c)
			case "服务":
				a.serviceConnectionOverview(c)
				a.desktopRuntimeSettings(c)
				a.versionUpdateSettings(c)
				a.environmentSetup(c, false)
			}
		})
	})
}

func (a *App) desktopRuntimeSettings(c *ui.Context) {
	keepAlive := a.KeepAliveOnQuit()
	openAtLogin := a.OpenAtLogin()
	v4Group(c, "桌面端运行方式", "与顶部菜单栏状态面板实时同步", nil, func() {
		v4Row(c, v4RowOptions{
			Key:   "settings-keep-alive-row",
			Title: "退出时保持后台运行",
			Desc:  "关闭桌面窗口或退出界面时，本地记忆引擎（Cognee）与接入网关继续在后台运行",
			Control: func() {
				v4Switch(c, "settings-keep-alive", &keepAlive, "", func() {
					a.SetKeepAliveOnQuit(keepAlive)
				}).Label("退出时保持后台运行开关")
			},
		})
		v4Row(c, v4RowOptions{
			Key:   "settings-open-at-login-row",
			Title: "登录时静默启动",
			Desc:  "系统登录时在后台启动 Memlane 与顶部菜单栏图标，不自动弹出主窗口",
			Error: a.desktopPrefs.LoginError,
			Control: func() {
				v4Switch(c, "settings-open-at-login", &openAtLogin, "", func() {
					a.ToggleOpenAtLogin(openAtLogin)
				}).Label("登录时静默启动开关")
			},
		})
	})
}

func (a *App) v4AppearanceSettingsGroup(c *ui.Context) {
	mode := a.AppearancePreference()
	selected := appearanceModeIndex(mode)
	v4Group(c, "外观", "", nil, func() {
		v4Row(c, v4RowOptions{
			Key:   "settings-appearance-row",
			Title: "外观模式",
			Desc:  "可与侧栏右下角外观按钮实时同步切换",
			Control: func() {
				v4Segmented(c, "settings-appearance", "外观模式", &selected, []string{"跟随系统", "浅色", "深色"}, func() {
					a.setAppearancePreference(appearanceModes[selected])
				})
			},
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
	a.access.RevealQueueProject = ""
	for _, p := range a.access.Directory.Projects {
		if p.ID == id {
			q := a.projectQueueStateFor(p)
			q.Reader = nil
			q.LocatedNoticeKey = ""
			break
		}
	}
}

// Open the project's Management tab and scroll directly to its active status/anomaly notice.
func (a *App) openProjectQueueStatus(index int) {
	a.openProjectSettings(index)
	if index < 0 || index >= len(a.Config.Projects) {
		return
	}
	a.access.RevealQueueProject = a.Config.Projects[index].ID
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
	ui.Scroll(c.Key("project-settings-" + p.ID)).Grow(1).MinHeight(0).TrackScroll(&q.Scroll).Padding(sp6, sp8, sp10, sp8).Background(tok.Bg).Children(func() {
		v4GroupWrap(c, func() {
			// 1. 消息与采集状态（固定顶部展示，不再随状态变化在顶部与底部之间跳动）
			a.projectQueueSection(c, p)

			// 2. 项目信息
			v4Group(c, "项目信息", "", nil, func() {
				v4Row(c, v4RowOptions{
					Key:   "proj-info-name-" + p.ID,
					Title: "项目名称",
					Value: p.Name,
					Control: func() {
						v4SmallButton(c, "编辑名称", nil, func() { a.openProjectCreation(p.ID) })
					},
				})
				for idx, path := range p.Aliases {
					if path == "" {
						continue
					}
					pathCopy := path
					title := "项目路径"
					if idx > 0 {
						title = "关联路径"
					}
					v4Row(c, v4RowOptions{
						Key:      "proj-info-path-" + pathCopy,
						Title:    title,
						Desc:     pathCopy,
						DescMono: true,
						Control: func() {
							v4SmallButton(c, "复制路径", v4IconCopy, func() { c.WriteClipboard(pathCopy) }).Label("复制项目路径")
						},
					})
				}
			})

			// 2. Agent 访问权限（Q5 三档权限下拉）
			var permActions func()
			if !p.permissionsReady(a.access.Directory.Agents...) {
				permActions = func() {
					v4SmallButton(c, "核对权限", v4IconRefresh, func() {
						a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
							return client.reconcile(ctx, revision, p.ID)
						})
					}).Disabled(a.access.Busy)
				}
			}
			permDesc := "三档权限立即保存并核对"
			if !p.permissionsReady(a.access.Directory.Agents...) {
				permDesc = "权限尚未同步完成"
			}
			v4Group(c, "Agent 访问权限", permDesc, permActions, func() {
				if len(a.access.Directory.Agents) == 0 {
					v4Row(c, v4RowOptions{
						Title: "尚无已连接的 Agent",
						Desc:  "请先在全局设置中连接 Agent。",
					})
				}
				for _, row := range a.access.Directory.Agents {
					a.projectPermissionCard(c, p, row)
				}
				if len(a.access.CheckboxPending) == 0 && a.access.Message != "" && a.access.Message != "设置已保存；生效状态以官方核对结果为准" {
					ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() {
						homeText(c, a.access.Message, homeMeta).LineHeight(1.5)
					})
				}
				if a.access.DirectoryError != "" {
					ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() {
						homeText(c, a.access.DirectoryError, homeMeta).TextColor(tok.Danger).LineHeight(1.5)
					})
				}
			})

			// 4. 工具记录关系图谱
			a.projectToolGraphSettings(c, p)
		})
	})
}

var emptyReadingArt = homeSVG(`<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h3"/>`)

func (a *App) emptyReadingBackground(c *ui.Context) {
	v4EmptyState(c, "empty-reading-art", "", "")
}

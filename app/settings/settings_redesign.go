package settings

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func policySettingRow(tok *kitui.Tokens, c *ui.Context, title, explanation, value string, inherited bool, control func()) {
	source := "独立设置"
	if inherited {
		source = "继承全局默认"
	}
	kitui.Row(c, tok, kitui.RowOptions{
		Title: title,
		Desc:  explanation,
		TitleExtra: func() {
			kitui.Badge(c, tok, source, "")
		},
		Control: func() {
			control()
			kitui.Text(c, tok, "已保存："+value, kitui.RoleMeta).SingleLine()
		},
	})
}

func settingsTabs(tok *kitui.Tokens, c *ui.Context, current *string, labels []string) {
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
	ui.Row(c).Height(listHeaderH).FillWidth().Shrink(0).Padding(0, sp8).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).AlignItems(ui.Center).Children(func() {
		kitui.UnderlineTabs(c, tok, "settings-tabs", "设置分类", &selected, displayLabels, func() { *current = labels[selected] })
	})
}

func settingsCard(tok *kitui.Tokens, c *ui.Context, title, description string, content func()) {
	kitui.Group(c, tok, title, description, nil, func() {
		ui.Column(c).FillWidth().Gap(sp2).Padding(sp3, sp4).Children(content)
	})
}

func (a *Controller) globalSettingsPage(c *ui.Context) {
	tok := a.tokens()
	switch a.access.SettingsSection {
	case "服务与连接":
		a.access.SettingsSection = "服务"
	case "知识处理", "记忆处理":
		a.access.SettingsSection = "记忆提炼"
	case "Agent 与模型":
		a.access.SettingsSection = "Agent 连接"
	}
	settingsTabs(tok, c, &a.access.SettingsSection, []string{"记忆提炼", "Agent 连接", "服务", "通用"})
	ui.Scroll(c.Key("global-settings-"+a.access.SettingsSection)).Grow(1).MinHeight(0).Padding(sp6, sp8, sp10, sp8).Background(tok.Bg).Children(func() {
		kitui.GroupWrap(c, func() {
			a.setupDeferredGenerationBanner(c, false)
			switch a.access.SettingsSection {
			case "记忆提炼":
				setup := a.canActivateFirstRuntimeFromSettings()
				if setup {
					a.settingsFirstActivationBar(c)
				}
				a.generationConfigurationFor(c, setup, true)
				kitui.Group(c, tok, "提炼策略", "", nil, func() {
					a.globalToolGraphSettings(c)
				})
			case "Agent 连接":
				if a.config.ProjectControl != nil {
					if !a.windowIsHidden() && !a.windowIsInactive() && a.integrations.hasPendingAgentReceipts() {
						c.After(2 * time.Second)
					}
					a.agentConnectionSettings(c)
				}
			case "服务":
				a.staleDaemonBanner(c)
				a.dailyRuntimeStartupError(c)
				if a.host.RenderServiceOverview != nil {
					a.host.RenderServiceOverview(c)
				} else {
					a.ViewServiceOverview(c)
				}
				a.environmentSetup(c, false)
			case "通用":
				a.generalAppearanceAndLanguageSettings(c)
				a.desktopRuntimeSettings(c)
				if a.host.RenderVersionUpdate != nil {
					a.host.RenderVersionUpdate(c)
				}
			}
		})
	})
}

func (a *Controller) desktopRuntimeSettings(c *ui.Context) {
	tok := a.tokens()
	keepAlive := a.KeepAliveOnQuit()
	openAtLogin := a.OpenAtLogin()
	loginError := a.loginError
	if a.host.OpenAtLoginError != nil {
		loginError = a.host.OpenAtLoginError()
	}
	kitui.Group(c, tok, "桌面端运行方式", "与顶部菜单栏状态面板实时同步", nil, func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key:   "settings-keep-alive-row",
			Title: "退出时保持后台运行",
			Desc:  "关闭桌面窗口或退出界面时，本地记忆引擎（Cognee）与接入网关继续在后台运行",
			Control: func() {
				kitui.Switch(c, tok, "settings-keep-alive", &keepAlive, "", func() {
					a.SetKeepAliveOnQuit(keepAlive)
				}).Label("退出时保持后台运行开关")
			},
		})
		kitui.Row(c, tok, kitui.RowOptions{
			Key:   "settings-open-at-login-row",
			Title: "登录时静默启动",
			Desc:  "系统登录时在后台启动 Memlane 与顶部菜单栏图标，不自动弹出主窗口",
			Error: loginError,
			Control: func() {
				kitui.Switch(c, tok, "settings-open-at-login", &openAtLogin, "", func() {
					a.ToggleOpenAtLogin(openAtLogin)
				}).Label("登录时静默启动开关")
			},
		})
	})
}

func (a *Controller) generalAppearanceAndLanguageSettings(c *ui.Context) {
	tok := a.tokens()
	mode := a.AppearancePreference()
	selected := appearanceModeIndex(mode)
	kitui.Group(c, tok, "外观与语言", "", nil, func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key:   "settings-appearance-row",
			Title: "外观模式",
			Desc:  "可与侧栏右下角外观按钮实时同步切换",
			Control: func() {
				kitui.Segmented(c, tok, "settings-appearance", "外观模式", &selected, []string{"跟随系统", "浅色", "深色"}, func() {
					a.setAppearancePreference(appearanceModes[selected])
				})
			},
		})
		languageSettingsCard(tok, c)
	})
}

// OpenProjectManagement requests the project's inline Management tab by stable ID.
func (a *Controller) OpenProjectManagement(projectID string) {
	for _, project := range a.config.Projects {
		if project.ID != projectID {
			continue
		}
		a.access.OpenProject = projectID
		if a.host.OpenProjectManagement != nil {
			a.host.OpenProjectManagement(projectID)
		}
		return
	}
}

// OpenProjectQueue opens Management and reveals the project's capture status.
func (a *Controller) OpenProjectQueue(projectID string) {
	a.OpenProjectManagement(projectID)
	if a.access.OpenProject == projectID {
		a.access.RevealQueueProject = projectID
	}
}

func (a *Controller) projectSettingsPage(c *ui.Context) {
	tok := a.tokens()
	var project *AccessProject
	for _, row := range a.access.Directory.Projects {
		if row.ID == a.access.OpenProject {
			p := row
			project = &p
			break
		}
	}
	if project == nil {
		kitui.Text(c, tok, "项目设置尚未载入", kitui.RoleHeading).Margin(28)
		kitui.RefreshAction(c, tok, "重新读取项目", a.RefreshProjectDirectory)
		return
	}
	p := *project
	q := a.ProjectQueue(p)
	ui.Scroll(c.Key("project-settings-"+p.ID)).Grow(1).MinHeight(0).TrackScroll(&q.Scroll).Padding(sp6, sp8, sp10, sp8).Background(tok.Bg).Children(func() {
		kitui.GroupWrap(c, func() {
			// 1. 消息与采集状态（固定顶部展示，不再随状态变化在顶部与底部之间跳动）
			a.projectQueueSection(c, p)

			// 2. 项目信息
			kitui.Group(c, tok, "项目信息", "", nil, func() {
				kitui.Row(c, tok, kitui.RowOptions{
					Key:   "proj-info-name-" + p.ID,
					Title: "项目名称",
					Value: p.Name,
					Control: func() {
						kitui.SmallButton(c, tok, "编辑名称", nil, func() { a.openProjectCreation(p.ID) })
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
					kitui.Row(c, tok, kitui.RowOptions{
						Key:      "proj-info-path-" + pathCopy,
						Title:    title,
						Desc:     pathCopy,
						DescMono: true,
						Control: func() {
							kitui.SmallButton(c, tok, "复制路径", kitui.IconCopy, func() { c.WriteClipboard(pathCopy) }).Label("复制项目路径")
						},
					})
				}
			})

			// 2. Agent 访问权限（Q5 三档权限下拉）
			var permActions func()
			if !p.PermissionsReady(a.access.Directory.Agents...) {
				permActions = func() {
					kitui.SmallButton(c, tok, "核对权限", kitui.IconRefresh, func() {
						a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
							return client.reconcile(ctx, revision, p.ID)
						})
					}).Disabled(a.access.Busy)
				}
			}
			permDesc := "三档权限立即保存并核对"
			if !p.PermissionsReady(a.access.Directory.Agents...) {
				permDesc = "权限尚未同步完成"
			}
			kitui.Group(c, tok, "Agent 访问权限", permDesc, permActions, func() {
				if len(a.access.Directory.Agents) == 0 {
					kitui.Row(c, tok, kitui.RowOptions{
						Title: "尚无已连接的 Agent",
						Desc:  "请先在全局设置中连接 Agent。",
					})
				}
				for _, row := range a.access.Directory.Agents {
					a.projectPermissionCard(c, p, row)
				}
				if len(a.access.CheckboxPending) == 0 && a.access.Message != "" && a.access.Message != "设置已保存；生效状态以官方核对结果为准" {
					ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
						kitui.Text(c, tok, a.access.Message, kitui.RoleMeta).LineHeight(1.5)
					})
				}
				if a.access.DirectoryError != "" {
					ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
						kitui.Text(c, tok, a.access.DirectoryError, kitui.RoleMeta).TextColor(tok.Danger).LineHeight(1.5)
					})
				}
			})

			// 4. 工具记录关系图谱
			a.projectToolGraphSettings(c, p)

			// 5. 危险操作（移除项目，仅非受保护项目）
			if !p.IsProtected() {
				a.projectRemoveSection(c, p)
			}
		})
	})
}

func (a *Controller) settingsFirstActivationBar(c *ui.Context) {
	tok := a.tokens()
	blocker := a.settingsFirstActivationBlocker()
	kitui.Group(c.Key("settings-first-generation-activation"), tok, "首次启用记忆提炼", "", nil, func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Title: "启用记忆提炼服务", Desc: blocker, Error: a.environment.Error,
			Control: func() {
				if a.canRetryDailyRuntimeStartup() {
					kitui.SmallButton(c, tok, a.environment.dailyRuntimeRecoveryAction(), kitui.IconRefresh, a.retryDailyRuntimeStartup).
						Disabled(a.environment.ownerInspecting || a.environment.Busy || a.environment.Loading)
				} else if a.shouldInspectDailyRuntimeOwner() && !a.environment.staleRuntimeOwner() {
					kitui.SmallButton(c, tok, "重新检查", kitui.IconRefresh, a.InspectOwnerRuntimeStatusAsync).
						Disabled(a.environment.ownerInspecting || a.environment.Busy)
				}
				kitui.SmallPrimaryButton(c, tok, "启用记忆提炼", a.activateFirstRuntimeFromSettings).Disabled(blocker != "")
			},
		})
	})
}

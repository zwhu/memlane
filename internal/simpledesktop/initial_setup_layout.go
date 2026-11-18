package simpledesktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

// A uses the existing shell palette and common controls. These dimensions and
// type roles apply to all three steps, not to the daily overview.
const setupInset float32 = 36

func setupHeading(c *ui.Context, text string) {
	homeText(c, text, homeHeading).FontSize(26).FontWeight(600)
}
func setupDescription(c *ui.Context, text string) {
	homeText(c, text, homeBody).FontSize(13).LineHeight(1.6)
}

func (a *App) initialSetupShell(c *ui.Context) {
	s := &a.environment
	step := s.InitialStep
	if step < 1 {
		step = 1
	}
	if step > 3 {
		step = 3
	}
	titles := []string{"准备环境", "连接 Agent", "配置记忆处理"}
	headings := []string{"准备好你的本地记忆环境", "连接你正在使用的 Agent", "选择如何整理记忆"}
	leads := []string{"记忆留在你的电脑上，已有的兼容组件直接复用。", "接入后，Agent 可以保存经验，并在后续任务中找回记忆。", "连接 Agent 与处理记忆分别配置，使用已有账号或自配模型。"}
	ui.Column(c.Key("first-use-shell")).Fill().Background(shellBackground).Children(func() {
		ui.Row(c).FillWidth().Height(48).Shrink(0).DragWindow()
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c.Key("first-use-navigation")).Width(homeSidebarWidth).Shrink(0).Padding(8, 12, 20, 12).Gap(6).Children(func() {
				ui.Row(c).Height(48).Padding(0, 12).Gap(10).AlignItems(ui.Center).Margin(0, 0, 20, 0).Children(func() {
					ui.Icon(c, mark).FontSize(26).TextColor(ui.Hex("#777b82"))
					homeText(c, "Memlane", homeBrand).FontSize(20).TextColor(ui.Hex("#64686f"))
				})
				for index, title := range titles {
					number := index + 1
					active := number == step
					selected := ui.Transparent
					if active {
						selected = ui.Hex("#dce7f2")
					}
					ui.Button(c.Key(fmt.Sprintf("setup-step-%d", number)), "").Label(title).FillWidth().Height(44).Padding(0, 12).Background(selected).BorderWidth(0).Radius(9).Disabled(s.Busy || a.integrations.Busy || (number > 1 && !s.RuntimeStarted)).OnClick(func() { a.setInitialStep(number) }).Children(func() {
						ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
							color, bg := homeMuted, ui.Transparent
							if active {
								color, bg = homeBackground, homeInk
							}
							ui.Row(c).Size(22, 22).Shrink(0).Radius(11).Background(bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								homeText(c, fmt.Sprint(number), homeMeta).FontSize(11).TextColor(color)
							})
							color = homeMuted
							if active {
								color = homeInk
							}
							homeText(c, title, homeTitle).FontSize(12).TextColor(color).SingleLine().Grow(1).MinWidth(0)
						})
					})
				}
				ui.Spacer(c)
				homeText(c, "以后也能在设置中调整。", homeMeta).FontSize(11).Margin(0, 12)
			})
			ui.Column(c.Key("first-use-workspace")).Grow(1).MinWidth(0).Margin(0, 12, 12, 0).Background(homeBackground).Border(1, shellBorder).Radius(14).Clip().Children(func() {
				ui.Scroll(c.Key("initial-setup-content")).Grow(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Padding(setupInset).Gap(10).Children(func() {
						homeText(c, fmt.Sprintf("开始使用 / %02d", step), homeEyebrow)
						setupHeading(c, headings[step-1])
						setupDescription(c, leads[step-1])
						if s.Busy && s.RuntimeStage != "" {
							homeText(c, s.RuntimeStage+"…", homeMeta).FontSize(11)
						}
						if s.Error != "" && step > 1 {
							homeText(c, s.Error, homeMeta).FontSize(11)
							textButton(c, "重试", func() { a.beginFirstRuntime(step) }).Disabled(s.Busy || a.integrations.Busy)
						}
						ui.Column(c).FillWidth().Gap(18).Margin(18, 0, 0, 0).Children(func() {
							switch step {
							case 1:
								a.environmentSetup(c, true)
							case 2:
								if s.RuntimeStarted {
									a.agentConnectionSettings(c)
								} else {
									homeText(c, "记忆服务尚未就绪", homeMeta)
								}
							case 3:
								if s.RuntimeStarted {
									a.generationConfiguration(c, false)
								} else {
									homeText(c, "记忆服务尚未就绪", homeMeta)
								}
							}
						})
					})
				})
				settingsListDivider(c)
				ui.Row(c.Key("first-use-footer")).FillWidth().MinHeight(68).Padding(12, setupInset).Gap(12).AlignItems(ui.Center).Children(func() {
					textButton(c, "环境详情", func() { s.Details = true }).Disabled(s.Busy)
					if step == 1 {
						homeText(c, "只安装你确认的组件", homeMeta).FontSize(11)
					}
					ui.Spacer(c)
					if step > 1 {
						commonButton(c, "上一步", false, func() { a.setInitialStep(step - 1) }).Disabled(s.Busy || a.integrations.Busy)
						if step == 2 {
							commonButton(c, "继续", true, func() { a.setInitialStep(3) }).Disabled(s.Busy || a.integrations.Busy || !s.RuntimeStarted)
						} else {
							commonButton(c, "启用并完成", true, a.enableFirstRuntime).Disabled(s.Busy || a.integrations.Busy || !s.RuntimeStarted || !a.integrations.Generation.validated(a.integrations.Generation.Next) || a.integrations.hasPendingGeneration())
						}
					}
				})
			})
		})
	})
	// Portals are rendered at the shell level on steps which do not render the
	// environment component, so details remain available without nested pages.
	if step > 1 {
		a.initialEnvironmentDetails(c)
	}
}

func (a *App) initialEnvironment(c *ui.Context) {
	s := &a.environment
	if !s.Loaded && !s.Loading && a.Update != nil {
		a.refreshEnvironment()
	}
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		settingsSectionHeader(c, "运行环境", nil, func() { textButton(c, "重新检测", a.refreshEnvironment).Disabled(s.Loading || s.Busy) })
		ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Border(1, homeBorder).Radius(10).Clip().Children(func() {
				groups := []struct {
					title, description string
					ids                []string
				}{
					{"运行基础", "Python 与运行依赖", []string{"uv", "python", "runtime"}},
					{"本地记忆引擎", "整理与检索", []string{"cognee-source", "embedding"}},
					{"Agent 接入", "保存与召回", []string{"agent-plugin"}},
				}
				shown := false
				for _, group := range groups {
					var items []bootstrap.Item
					for _, id := range group.ids {
						for _, item := range s.Plan.Items {
							if item.Requirement.ID == id {
								items = append(items, item)
							}
						}
					}
					if len(items) == 0 {
						continue
					}
					if shown {
						settingsListDivider(c)
					}
					shown = true
					ui.Column(c.Key(group.title)).FillWidth().Padding(14, 16, 10, 16).Gap(4).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							homeText(c, group.title, homePanelHeading).FontSize(13)
							ui.Spacer(c)
							homeText(c, group.description, homeMeta).FontSize(11)
						})
						for _, item := range items {
							row := item
							ui.Row(c.Key("environment-" + row.Requirement.ID)).FillWidth().MinHeight(51).Gap(11).AlignItems(ui.Center).Children(func() {
								dependencyMark(c, row.Requirement.ID)
								ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
									homeText(c, dependencyDisplayName(row.Requirement), homeTitle).FontSize(13)
									homeText(c, dependencyDisplayVersion(row, s.Environment), homeMeta).FontSize(11).SingleLine().Tooltip(row.Requirement.Version)
								})
								status := environmentRowStatus(row, s.Journal, s.Busy)
								color := homeMuted
								if status == bootstrap.Available || status == bootstrap.Ready {
									color = ui.Hex("#4d7a66")
								}
								homeText(c, environmentLabel(status), homeMeta).FontSize(11).TextColor(color)
							})
						}
					})
				}
				if !shown {
					homeText(c, "检测中…", homeMeta).Margin(20)
				}
			})
			ui.Column(c.Key("environment-summary")).Width(235).Padding(20).Gap(12).Background(ui.Hex("#f8f9fa")).Border(1, homeBorder).Radius(10).Children(func() {
				homeText(c, "准备概览", homePanelHeading).FontSize(13)
				count := 0
				for _, item := range s.Plan.Items {
					if item.Observation.Status != bootstrap.Available {
						count++
					}
				}
				label := fmt.Sprintf("%d 个组件", count)
				if s.Loading || !s.Loaded {
					label = "检测中"
				} else if s.Busy {
					label = "正在准备"
				} else if s.Plan.Complete() {
					label = "已就绪"
				}
				homeText(c, label, homeStatValue).FontSize(25).Margin(4, 0, 0, 0)
				homeText(c, "先检测已有环境，仅安装缺少的组件。", homeBody).FontSize(12).LineHeight(1.6)
				settingsListDivider(c)
				if s.Busy {
					if s.RuntimeStage != "" {
						homeText(c, s.RuntimeStage+"…", homeMeta).FontSize(11)
					} else {
						commonButton(c, "停止安装", false, func() {
							if s.Cancel != nil {
								s.Cancel()
							}
						})
					}
				} else if s.Plan.Complete() && s.Loaded {
					commonButton(c, "继续", true, a.continueEnvironment)
				} else if len(s.Plan.Items) > 0 {
					commonButton(c, "安装缺少的依赖", true, func() { s.Review = true }).Disabled(s.Loading)
				}
				textButton(c, "指定已有环境", func() { s.Details = true; s.Manual = true }).Disabled(s.Busy || s.Loading)
				textButton(c, "查看详情", func() { s.Details = true })
				if s.Error != "" {
					homeText(c, "准备未完成", homePanelHeading).FontSize(12)
					homeText(c, s.Error, homeMeta).FontSize(11).MaxLines(3)
					textButton(c, "重试", func() {
						if s.Journal.ApprovedDigest != "" {
							a.runEnvironment(false)
						} else {
							a.refreshEnvironment()
						}
					}).Disabled(s.Busy)
					textButton(c, "用 Agent 排查", func() {
						_, prompt := a.environmentDiagnostic()
						c.WriteClipboard(prompt)
						s.Message = "已复制排查提示词"
						s.AgentOpen, s.Details = true, true
						s.DiagnosticAgents = bootstrap.ExistingDiagnosticAgents(nil, nil)
					})
				}
				if s.Message != "" {
					homeText(c, s.Message, homeMeta).FontSize(11)
				}
			})
		})
	})
}

func (a *App) environmentReviewModal(c *ui.Context) {
	s := &a.environment
	if !s.Review {
		return
	}
	ui.Modal(c.Key("environment-install-plan"), &s.Review, func() {
		homeText(c, "确认安装缺少的依赖", homeHeading).FontSize(18)
		homeText(c, "仅安装以下缺失组件，已有环境保持原样。", homeBody).FontSize(12)
		ui.Scroll(c.Key("environment-plan-scroll")).FillWidth().Height(340).Children(func() {
			ui.Column(c).FillWidth().Gap(14).Children(func() {
				for _, item := range s.Plan.Items {
					if item.Observation.Status == bootstrap.Available {
						continue
					}
					r := item.Requirement
					ui.Column(c.Key("plan-" + r.ID)).FillWidth().Gap(6).Children(func() {
						ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() { dependencyMark(c, r.ID); homeText(c, r.Name+" · "+r.Version, homeTitle).FontSize(13) })
						homeText(c, r.Purpose, homeBody).FontSize(12)
						homeText(c, "下载 "+environmentSize(r.DownloadBytes)+"，占用 "+environmentSize(r.InstalledBytes), homeMeta).FontSize(11)
						if r.SizeNote != "" {
							homeText(c, r.SizeNote, homeMeta).FontSize(11)
						}
						homeText(c, "来源："+r.Source, homePath).FontSize(11).LineHeight(1.5)
						homeText(c, "位置："+r.Destination, homePath).FontSize(11).LineHeight(1.5)
					})
					settingsListDivider(c)
				}
			})
		})
		ui.Row(c).FillWidth().Gap(12).Justify(ui.End).Children(func() {
			commonButton(c, "暂不安装", false, a.declineEnvironment)
			commonButton(c, "确认安装", true, func() { a.runEnvironment(true) }).Disabled(s.Loading || s.Busy)
		})
	}).Width(680).Padding(24).Gap(16)
}

func (a *App) initialEnvironmentDetails(c *ui.Context) {
	s := &a.environment
	if !s.Details {
		return
	}
	ui.Modal(c.Key("first-use-environment-details"), &s.Details, func() {
		homeText(c, "环境详情", homeHeading).FontSize(18)
		ui.Scroll(c.Key("first-use-details-scroll")).FillWidth().Height(360).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				a.environmentDiagnosticAgents(c)
				if s.Error != "" {
					homeText(c, s.Error, homeBody).FontSize(12).LineHeight(1.6)
				}
				for _, item := range s.Plan.Items {
					homeText(c, dependencyDisplayName(item.Requirement)+" · "+dependencyDisplayVersion(item, s.Environment), homeTitle).FontSize(13)
					homeText(c, bootstrap.RedactInstallerText(item.Observation.Reason), homeMeta).FontSize(11)
					homeText(c, item.Observation.Path, homePath).FontSize(11)
					settingsListDivider(c)
				}
				if s.Manual {
					for _, field := range []struct {
						label string
						value *string
					}{{"Python 路径", &s.Python}, {"uv 路径", &s.UV}, {"Cognee 源码目录", &s.Source}, {"固定源码归档", &s.Archive}, {"已有模型缓存目录", &s.ModelCache}, {"已有 Agent 插件目录", &s.Plugin}} {
						homeText(c, field.label, homeBody).FontSize(12)
						ui.TextInput(c.Key(field.label), field.value).FillWidth().Label(field.label).Disabled(s.Busy || s.Loading)
					}
					commonButton(c, "检测这些路径", false, func() { a.detectEnvironment(true) }).Disabled(s.Busy || s.Loading)
				}
			})
		})
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			textButton(c, "复制诊断材料", func() {
				report, _ := a.environmentDiagnostic()
				c.WriteClipboard(report)
				s.Message = "脱敏诊断材料已复制"
			})
			textButton(c, "导出诊断包", a.exportEnvironmentDiagnostic).Disabled(s.Exporting || a.ChooseDiagnosticPath == nil)
			ui.Spacer(c)
			commonButton(c, "关闭", false, func() { s.Details = false })
		})
	}).Width(720).Padding(24).Gap(16)
}

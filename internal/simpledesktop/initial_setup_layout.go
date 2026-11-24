package simpledesktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

// A uses the V4 design tokens and shared components. These dimensions and
// type roles apply to all three first-use steps, not to the daily overview.
const setupInset float32 = sp8

func setupHeading(c *ui.Context, text string) {
	ui.Text(c, text).Font("system-ui").FontSize(fsTitle).FontWeight(600).TextColor(tok.Text1).LineHeight(1.3)
}

func setupDescription(c *ui.Context, text string) {
	ui.Text(c, text).Font("system-ui").FontSize(fsBody).FontWeight(400).TextColor(tok.Text2).LineHeight(1.6)
}

func environmentDotState(status bootstrap.Status) string {
	switch status {
	case bootstrap.Available, bootstrap.Ready:
		return "ok"
	case bootstrap.Installing:
		return "run"
	case bootstrap.Failed:
		return "fail"
	case bootstrap.Missing, bootstrap.Incompatible, "unconfirmed":
		return "warn"
	default:
		return ""
	}
}

func environmentStatusColor(status bootstrap.Status) ui.Color {
	switch status {
	case bootstrap.Available, bootstrap.Ready:
		return tok.OK
	case bootstrap.Installing:
		return tok.Accent
	case bootstrap.Failed:
		return tok.Danger
	case bootstrap.Missing, bootstrap.Incompatible, "unconfirmed":
		return tok.Warning
	default:
		return tok.Text3
	}
}

func (a *App) setupStageBanner(c *ui.Context, title, subtitle string, stopFn func()) {
	ui.Row(c.Key("setup-stage-banner")).FillWidth().Padding(sp4).Gap(sp4).
		Radius(radius).Background(tok.Fill1).AlignItems(ui.Center).Children(func() {
		v4StageBlocks(c)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).Font("system-ui").FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).LineHeight(1.4)
			if subtitle != "" {
				ui.Text(c, subtitle).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text2).LineHeight(1.45)
			}
		})
		if stopFn != nil {
			v4SmallButton(c, "停止安装", nil, stopFn)
		}
	})
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
	if s.setupLastStep != step {
		s.setupLastStep = step
		s.setupStepRun++
	}
	steps := []struct {
		title   string
		sub     string
		heading string
		lead    string
	}{
		{
			title:   "准备环境",
			sub:     "复用已有依赖",
			heading: "准备好你的本地记忆环境",
			lead:    "记忆留在你的电脑上，已有的兼容组件直接复用。",
		},
		{
			title:   "连接 Agent",
			sub:     "自动采集与召回",
			heading: "连接你正在使用的 Agent",
			lead:    "接入后，Agent 可以采集经验，并在后续任务中找回记忆。",
		},
		{
			title:   "配置记忆提炼",
			sub:     "Agent 或模型 API",
			heading: "选择如何提炼记忆",
			lead:    "连接 Agent 与提炼记忆分别配置，使用已有账号或自配模型。",
		},
	}

	ui.Row(c.Key("first-use-shell")).Fill().Background(tok.Bg).AlignItems(ui.Stretch).Children(func() {
		// Left step sidebar (220px, spring active pill + completion checkmarks).
		ui.Column(c.Key("first-use-navigation")).Width(sidebarW).Shrink(0).
			Background(tok.Sidebar).BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).
			Padding(0, sp2, sp4, sp2).Children(func() {
			ui.Row(c).FillWidth().Height(48).Shrink(0).DragWindow()
			ui.Row(c).FillWidth().Height(36).Padding(0, sp2).Gap(10).AlignItems(ui.Center).Margin(0, 0, sp4, 0).Children(func() {
				ui.Image(c, tok.BrandMark).Size(20, 20).Fit(ui.Contain)
				ui.Text(c, "Memlane").Font("system-ui").FontSize(fsHeading).FontWeight(600).TextColor(tok.Text1).SingleLine()
			})

			var ind ui.Element
			var activeBtn ui.Element
			stepsHost := ui.Column(c.Key("first-use-steps")).FillWidth().Gap(sp1)
			stepsHost.Children(func() {
				// Normal-flow 0x0 layer declared before step buttons paints strictly behind text.
				ui.Box(c.Key("first-use-step-ind-layer")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
					ind = ui.Box(c.Key("first-use-step-ind")).Width(sidebarW - sp2*2).Radius(radius).Background(tok.AccentSoft).PassThrough()
				})
				for index, st := range steps {
					number := index + 1
					active := number == step
					done := !active && ((number == 1 && (s.RuntimeStarted || (s.Loaded && s.Plan.Complete()))) || (number < step && s.RuntimeStarted))
					disabled := s.Busy || a.integrations.Busy || (number > 1 && !s.RuntimeStarted)

					btn := ui.ButtonBase(c.Key(fmt.Sprintf("setup-step-%d", number))).Role(ui.RoleButton).Label(st.title).
						FillWidth().MinHeight(48).Padding(sp2, sp2).Gap(10).Radius(radius).
						AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0).
						Cursor(ui.CursorPointer).Disabled(disabled).
						OnClick(func() { a.setInitialStep(number) })
					if active {
						btn.Background(ui.Transparent)
						activeBtn = btn
					} else if !disabled {
						btn.Background(homeHoverColor(btn, fmt.Sprintf("setup-step-bg-%d", number), ui.Transparent, tok.Hover))
					} else {
						btn.Background(ui.Transparent)
					}
					v4ButtonPress(btn, 0.99)

					btn.Children(func() {
						badgeBg := tok.Fill2
						badgeFg := tok.Text3
						if active {
							badgeBg, badgeFg = tok.Accent, tok.OnAccent
						} else if done {
							badgeBg, badgeFg = tok.OK, tok.OnAccent
						}
						ui.Row(c).Size(22, 22).Shrink(0).Radius(11).Background(badgeBg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							if done {
								ui.Icon(c, v4IconCheck).FontSize(12).TextColor(badgeFg)
							} else {
								ui.Text(c, fmt.Sprint(number)).Font("system-ui").FontSize(fsCaption).FontWeight(600).FontFeatures("tnum").TextColor(badgeFg).SingleLine()
							}
						})
						titleCol := tok.Text2
						titleWeight := 500
						if active {
							titleCol = tok.Accent
							titleWeight = 600
						} else if done {
							titleCol = tok.Text1
						}
						ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
							ui.Text(c, st.title).Font("system-ui").FontSize(fsBody).FontWeight(titleWeight).TextColor(titleCol).SingleLine()
							ui.Text(c, st.sub).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
						})
					})
				}
			})

			hostBox := stepsHost.Bounds()
			activeBox := activeBtn.Bounds()
			if activeBox.H > 0 {
				s.setupStepIndY = activeBox.Y - hostBox.Y
				s.setupStepIndH = activeBox.H
			}
			indY, indH := s.setupStepIndY, s.setupStepIndH
			if indH <= 0 {
				indY = float32(step-1) * 52
				indH = 48
			}
			ind.Height(indH).AnimatePose(ui.Pose{Y: indY, Scale: 1}, v4Snappy)

			ui.Spacer(c)
			ui.Column(c).FillWidth().Padding(sp3, sp2, 0, sp2).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				ui.Text(c, "以后也能在设置中随时调整所有选项。").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).LineHeight(1.5)
			})
		})

		// Right workspace (drag bar + scrollable 680px content + fixed footer).
		ui.Column(c.Key("first-use-workspace")).Grow(1).MinWidth(0).Background(tok.Bg).Children(func() {
			ui.Row(c).FillWidth().Height(48).Shrink(0).DragWindow()
			ui.Scroll(c.Key("initial-setup-content")).Grow(1).MinHeight(0).FillWidth().Children(func() {
				pane := ui.Column(c.Key(fmt.Sprintf("setup-pane-%d-%d", step, s.setupStepRun))).FillWidth().Padding(sp3, sp10, sp8, sp10)
				pane.Children(func() {
					v4GroupWrap(c, func() {
						ui.Column(c).FillWidth().Gap(6).Children(func() {
							ui.Text(c, fmt.Sprintf("开始使用 / %02d", step)).Font("ui-monospace").FontSize(fsCaption).FontWeight(600).TextColor(tok.Accent).SingleLine()
							setupHeading(c, steps[step-1].heading)
							setupDescription(c, steps[step-1].lead)
						})
						if step > 1 && s.Busy && s.RuntimeStage != "" {
							a.setupStageBanner(c, s.RuntimeStage+"…", "等待真实服务回执确认后自动继续。", nil)
						}
						if s.Error != "" && step > 1 {
							ui.Row(c).FillWidth().Padding(sp3, sp4).Gap(sp3).Radius(radius).Background(tok.Danger.Alpha(0.08)).AlignItems(ui.Center).Children(func() {
								ui.Text(c, s.Error).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Danger).Grow(1).MinWidth(0).LineHeight(1.5)
								v4SmallButton(c, "重试", v4IconRefresh, func() { a.beginFirstRuntime(step) }).Disabled(s.Busy || a.integrations.Busy)
							})
						}
						ui.Column(c).FillWidth().Gap(sp5).Children(func() {
							switch step {
							case 1:
								a.environmentSetup(c, true)
							case 2:
								if s.RuntimeStarted {
									a.agentConnectionSettings(c)
								} else {
									ui.Text(c, "记忆服务尚未就绪").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
								}
							case 3:
								a.firstGenerationStepGuide(c)
								if s.RuntimeStarted {
									a.generationConfiguration(c, false)
								} else {
									ui.Text(c, "记忆服务尚未就绪").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
								}
							}
						})
					})
				})
				a.v4PageEnter(c, pane, fmt.Sprintf("setup-step-%d", step), s.setupStepRun)
			})

			// Fixed footer: 环境详情 + hint | 上一步 / 继续
			ui.Row(c.Key("first-use-footer")).FillWidth().Height(60).Shrink(0).
				Padding(0, setupInset).Gap(sp3).AlignItems(ui.Center).
				Background(tok.Bg).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				v4SmallButton(c, "环境详情", nil, func() { s.Details = true }).Disabled(s.Busy)
				hint := "可随时返回上一步核对"
				if step == 1 {
					hint = "只安装你确认的组件"
				} else if step == 3 {
					hint = a.firstGenerationEnableHint()
				}
				ui.Text(c, hint).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).Grow(1).MinWidth(0).MaxLines(2)
				if step == 1 {
					v4OutlineButton(c, "上一步", nil, nil).Disabled(true)
					if s.Plan.Complete() && s.Loaded {
						v4PrimaryButton(c, "继续", a.continueEnvironment).Disabled(s.Busy || a.integrations.Busy)
					} else if len(s.Plan.Items) > 0 {
						label := "安装并继续"
						if isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy) {
							label = "更新 Agent 插件"
						}
						v4PrimaryButton(c, label, func() { s.Review = true }).Disabled(s.Busy || s.Loading)
					}
				} else {
					v4OutlineButton(c, "上一步", nil, func() { a.setInitialStep(step - 1) }).Disabled(s.Busy || a.integrations.Busy)
					if step == 2 {
						v4PrimaryButton(c, "继续", func() { a.setInitialStep(3) }).Disabled(s.Busy || a.integrations.Busy || !s.RuntimeStarted)
					} else {
						v4PrimaryButton(c, "启用并完成", a.enableFirstRuntime).Disabled(a.firstGenerationEnableBlocker() != "")
					}
				}
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
	missingCount := 0
	for _, item := range s.Plan.Items {
		if item.Observation.Status != bootstrap.Available {
			missingCount++
		}
	}

	ui.Column(c).FillWidth().Gap(sp5).Children(func() {
		// Top status band: stage blocks banner while busy, otherwise hairline summary row.
		if s.Busy {
			stageTitle := "正在准备依赖组件…"
			var stopFn func()
			if s.RuntimeStage != "" {
				stageTitle = s.RuntimeStage + "…"
			} else {
				stopFn = func() {
					if s.Cancel != nil {
						s.Cancel()
					}
				}
			}
			a.setupStageBanner(c, stageTitle, "请稍候，完成后将自动校验依赖并启动本地服务。", stopFn)
		} else {
			statLabel := fmt.Sprintf("尚缺 %d 个组件", missingCount)
			noteLabel := "先检测已有环境，仅安装你确认的缺失组件。"
			if s.Loading || !s.Loaded {
				statLabel = "检测中"
			} else if s.Plan.Complete() {
				statLabel = "已就绪"
				noteLabel = "全部核心依赖已通过校验，可继续连接 Agent。"
			}
			ui.Column(c.Key("environment-summary")).FillWidth().Gap(sp2).Children(func() {
				ui.Row(c).FillWidth().Padding(sp3, 0).Gap(sp3).AlignItems(ui.Center).
					BorderWidth(1, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					ui.Text(c, statLabel).Font("system-ui").FontSize(fsHeading).FontWeight(600).FontFeatures("tnum").TextColor(tok.Text1).SingleLine()
					ui.Text(c, noteLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text2).Grow(1).MinWidth(0).SingleLine()
					v4SmallButton(c, "重新检测", v4IconRefresh, a.refreshEnvironment).Disabled(s.Loading || s.Busy)
					if s.Plan.Complete() && s.Loaded {
						v4SmallPrimaryButton(c, "继续", a.continueEnvironment)
					} else if len(s.Plan.Items) > 0 {
						label := "安装缺少的依赖"
						if isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy) {
							label = "更新 Agent 插件"
						}
						v4SmallPrimaryButton(c, label, func() { s.Review = true }).Disabled(s.Loading)
					}
				})
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					v4SmallButton(c, "指定已有环境", nil, func() { s.Details = true; s.Manual = true }).Disabled(s.Busy || s.Loading)
					v4SmallButton(c, "查看详情", nil, func() { s.Details = true })
					if !s.Plan.Complete() && len(s.Plan.Items) > 0 {
						v4SmallButton(c, "复制安装命令", v4IconCopy, func() { a.copyManualInstallScript(c) }).Disabled(s.Busy || s.Loading)
					}
					ui.Spacer(c)
					a.downloadSourceSelector(c, "initial-download-source")
				})
				if s.Message != "" {
					ui.Text(c, s.Message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.4)
				}
			})
		}

		if s.Error != "" {
			ui.Column(c.Key("environment-error-banner")).FillWidth().Padding(sp3, sp4).Gap(sp2).
				Radius(radius).Background(tok.Danger.Alpha(0.08)).Children(func() {
				ui.Text(c, "准备未完成").Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Danger)
				ui.Text(c, s.Error).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text2).MaxLines(3).LineHeight(1.5)
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
					v4SmallButton(c, "重试", v4IconRefresh, func() {
						if s.Journal.ApprovedDigest != "" {
							a.runEnvironment(false)
						} else {
							a.refreshEnvironment()
						}
					}).Disabled(s.Busy)
					v4SmallButton(c, "用 Agent 排查", nil, func() {
						_, prompt := a.environmentDiagnostic()
						c.WriteClipboard(prompt)
						s.Message = "已复制排查提示词"
						s.AgentOpen, s.Details = true, true
						s.DiagnosticAgents = bootstrap.ExistingDiagnosticAgents(nil, nil)
					})
				})
			})
		}

		// Grouped dependency list (cardless V4 groups with status dot, version, size).
		groups := []struct {
			title, description string
			ids                []string
		}{
			{"运行基础", "Python 与运行依赖", []string{"uv", "python", "runtime"}},
			{"本地记忆引擎", "提炼与检索", []string{"cognee-source", "embedding"}},
			{"Agent 接入", "采集与召回", []string{"agent-plugin"}},
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
			shown = true
			v4Group(c, group.title, group.description, nil, func() {
				for _, item := range items {
					row := item
					status := environmentRowStatus(row, s.Journal, s.Busy)
					desc := dependencyDisplayVersion(row, s.Environment)
					if row.Requirement.InstalledBytes > 0 {
						desc = desc + " · " + environmentSize(row.Requirement.InstalledBytes)
					}
					dot, label, color := environmentDotState(status), environmentLabel(status), environmentStatusColor(status)
					var description func()
					if isPluginPolicyUpdate(row, status) {
						dot, label, color = "run", "策略有更新", tok.Accent
						description = func() {
							ui.Text(c, environmentIncompatibleHint(a, row)).FontSize(fsCaption).TextColor(tok.Text2).LineHeight(1.5)
						}
					}
					v4Row(c, v4RowOptions{
						Key:       "environment-" + row.Requirement.ID,
						Title:     dependencyDisplayName(row.Requirement),
						Desc:      desc,
						DescMono:  true,
						DescExtra: description,
						Tooltip:   row.Requirement.Version,
						Leading: func() {
							ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
								v4StatusDot(c, dot)
								dependencyMark(c, row.Requirement.ID)
							})
						},
						Value:      label,
						ValueColor: color,
					})
				}
			})
		}
		if !shown {
			v4Group(c, "运行环境", "检测中…", nil, func() {
				v4SkeletonGroup(c, 3, 2, true, true)
			})
		}
	})
}

func (a *App) environmentReviewModal(c *ui.Context) {
	s := &a.environment
	if !s.Review {
		return
	}
	pluginOnly := isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy)
	modalTitle := "确认安装缺少的依赖"
	modalIntro := "仅安装以下缺失组件，已有环境保持原样。"
	modalHint := "已有环境和目录保持原样，不修改系统配置"
	cancelText := "暂不安装"
	confirmText := "确认安装"
	if pluginOnly {
		modalTitle = "确认更新 Agent 插件"
		modalIntro = "将下载并校验新版 Agent 插件，然后把日常服务配置切换到新版目录（原插件目录会保留，不影响已有记忆数据）。"
		modalHint = "已有记忆数据保持不变；配置更新后重启日常服务即可由后台加载新版插件"
		if a.initialSetup || a.Config.ProjectControl == nil || a.Config.ProjectControl.PluginDir == "" || a.Config.ConfigFile == "" {
			modalIntro = "将下载并校验更新后的 Agent 插件，原插件目录会保留。"
			modalHint = "校验通过后可继续准备运行环境"
		}
		cancelText = "暂不更新"
		confirmText = "确认更新"
	}
	v4Dialog(c, &s.Review, v4DialogOptions{
		Key:             "environment-install-plan",
		Title:           modalTitle,
		Width:           v4DialogLarge,
		ScrollBody:      true,
		Hint:            modalHint,
		CancelText:      cancelText,
		ConfirmText:     confirmText,
		ConfirmDisabled: s.Loading || s.Busy,
		OnCancel:        a.declineEnvironment,
		OnConfirm:       func() { a.runEnvironment(true) },
		ExtraFooter: func() {
			if !pluginOnly {
				v4SmallButton(c, "复制全部安装命令", v4IconCopy, func() { a.copyManualInstallScript(c) })
			}
		},
		Body: func() {
			ui.Text(c, modalIntro).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
			ui.Column(c).FillWidth().Gap(0).Children(func() {
				for _, item := range s.Plan.Items {
					if item.Observation.Status == bootstrap.Available {
						continue
					}
					itemCopy := item
					r := item.Requirement
					ui.Column(c.Key("plan-"+r.ID)).FillWidth().Padding(sp3, 0).Gap(4).
						BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
						ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
							if isPluginPolicyUpdate(item, item.Observation.Status) {
								v4StatusDot(c, "run")
							}
							dependencyMark(c, r.ID)
							ui.Text(c, r.Name+" · "+r.Version).Font("system-ui").FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).Grow(1).MinWidth(0)
							if isPluginPolicyUpdate(item, item.Observation.Status) {
								ui.Text(c, "策略有更新").FontSize(fsCaption).TextColor(tok.Accent).SingleLine()
							}
							ui.Text(c, "下载 "+environmentSize(r.DownloadBytes)+"，占用 "+environmentSize(r.InstalledBytes)).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
						})
						ui.Text(c, r.Purpose).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
						if item.Observation.Status == bootstrap.Incompatible {
							if hint := environmentIncompatibleHint(a, item); hint != "" {
								prefix := "原因："
								col := tok.Warning
								if isPluginPolicyUpdate(item, item.Observation.Status) || isDailyPluginUpdateAvailable(a, item, item.Observation.Status) {
									prefix = "说明："
									col = tok.Text2
								}
								ui.Text(c, prefix+hint).Font("system-ui").FontSize(fsCaption).TextColor(col).LineHeight(1.5)
							}
							if s.Details && item.Observation.Reason != "" && isDailyPluginUpdateAvailable(a, item, item.Observation.Status) {
								ui.Text(c, "技术详情："+bootstrap.RedactInstallerText(item.Observation.Reason)).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
							}
						}
						if r.SizeNote != "" {
							ui.Text(c, r.SizeNote).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
						}
						sourceLabel := "来源：" + r.Source
						if r.OfficialSource != "" && r.OfficialSource != r.Source {
							sourceLabel = "当前来源：" + r.Source
						}
						ui.Text(c, sourceLabel).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						if r.OfficialSource != "" && r.OfficialSource != r.Source {
							ui.Text(c, "官方原址："+r.OfficialSource).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						}
						ui.Text(c, "位置："+r.Destination).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						if cmd := bootstrap.ManualInstallCommand(itemCopy, s.Environment, s.DownloadSource); cmd != "" {
							ui.Row(c).FillWidth().Padding(2, 0, 0, 0).Children(func() {
								v4SmallButton(c, "复制此项安装命令", v4IconCopy, func() {
									c.WriteClipboard(cmd)
									s.Message = "已复制 " + r.Name + " 安装命令"
								})
							})
						}
					})
				}
			})
		},
	})
}

func (a *App) initialEnvironmentDetails(c *ui.Context) {
	s := &a.environment
	if !s.Details {
		return
	}
	v4Dialog(c, &s.Details, v4DialogOptions{
		Key:         "first-use-environment-details",
		Title:       "环境详情",
		Width:       v4DialogLarge,
		ScrollBody:  true,
		Hint:        "路径与校验结果已脱敏，不含任何密钥或聊天内容",
		CancelText:  "关闭",
		HideConfirm: true,
		ExtraFooter: func() {
			v4SmallButton(c, "复制诊断材料", v4IconCopy, func() {
				report, _ := a.environmentDiagnostic()
				c.WriteClipboard(report)
				s.Message = "脱敏诊断材料已复制"
			})
			v4SmallButton(c, "导出诊断包", nil, a.exportEnvironmentDiagnostic).Disabled(s.Exporting || a.ChooseDiagnosticPath == nil)
		},
		Body: func() {
			a.environmentDiagnosticAgents(c)
			if s.Error != "" {
				ui.Text(c, s.Error).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Danger).LineHeight(1.6)
			}
			v4Group(c, "已检测组件路径", "", nil, func() {
				for _, item := range s.Plan.Items {
					row := item
					status := environmentRowStatus(row, s.Journal, s.Busy)
					desc := row.Observation.Path
					if reason := bootstrap.RedactInstallerText(row.Observation.Reason); reason != "" {
						if desc != "" {
							desc = desc + " · " + reason
						} else {
							desc = reason
						}
					}
					v4Row(c, v4RowOptions{
						Key:        "details-" + row.Requirement.ID,
						Title:      dependencyDisplayName(row.Requirement) + " · " + dependencyDisplayVersion(row, s.Environment),
						Desc:       desc,
						DescMono:   true,
						Leading:    func() { v4StatusDot(c, environmentDotState(status)) },
						Value:      environmentLabel(status),
						ValueColor: environmentStatusColor(status),
					})
				}
			})
			if s.Manual {
				ui.Column(c).FillWidth().Gap(sp2).Margin(sp2, 0, 0, 0).Children(func() {
					ui.Text(c, "指定已有路径").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
					for _, field := range []struct {
						label string
						value *string
					}{
						{"Python 路径", &s.Python},
						{"uv 路径", &s.UV},
						{"Cognee 源码目录", &s.Source},
						{"固定源码归档", &s.Archive},
						{"已有模型缓存目录", &s.ModelCache},
						{"已有 Agent 插件目录", &s.Plugin},
					} {
						ui.Column(c).FillWidth().Gap(4).Children(func() {
							ui.Text(c, field.label).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2)
							ui.TextInput(c.Key(field.label), field.value).FillWidth().Label(field.label).Disabled(s.Busy || s.Loading)
						})
					}
					ui.Row(c).FillWidth().Padding(sp2, 0, 0, 0).Children(func() {
						v4OutlineButton(c, "检测这些路径", nil, func() { a.detectEnvironment(true) }).Disabled(s.Busy || s.Loading)
					})
				})
			}
		},
	})
}

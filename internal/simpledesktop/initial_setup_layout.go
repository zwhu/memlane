package simpledesktop

import (
	"fmt"
	"strings"
	"time"

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
		{"选择 Agent", "自动采集与召回", "选择你正在使用的 Agent", "先选择你常用的编码工具；准备完本地记忆环境后，再一键确认接入。"},
		{"准备环境", "独立隔离本地运行", "准备好你的本地记忆环境", "把你的对话自动整理成结构化记忆与关系图谱，所有数据与模型仅留在你的电脑上。"},
		{"接入与提炼", "确认接入并启用", "确认接入 Agent 并启用记忆提炼", "确认接入所选 Agent，再选择本机 CLI 或模型 API 自动整理记忆。"},
	}

	if step == 3 && s.RuntimeStarted && !a.windowHidden && !a.windowInactive && a.integrations.hasPendingAgentReceipts() {
		c.After(2 * time.Second)
	}
	if s.ExternalWorkerActive && !s.Busy {
		c.After(1 * time.Second)
		if !time.Now().Before(s.WorkerPollNext) {
			a.inspectInstallWorkerAsync()
		}
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
					done := (number == 1 && step > 1) || (number == 2 && step != 2 && (s.RuntimeStarted || (s.Loaded && s.Plan.Complete())))
					disabled := s.Busy || s.RestartingStaleDaemon || a.integrations.Busy || (number == 3 && !a.canEnterConnectionStep())

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
						a.staleDaemonBanner(c)
						ui.Column(c).FillWidth().Gap(6).Children(func() {
							setupHeading(c, steps[step-1].heading)
							setupDescription(c, steps[step-1].lead)
						})
						switch step {
						case 1:
							a.initialSelectAgentStep(c)
						case 2:
							a.initialEnvironment(c)
						case 3:
							if s.Busy && s.RuntimeStage != "" {
								a.setupStageBanner(c, s.RuntimeStage+"…", "等待真实服务回执确认后自动继续。", nil)
							}
							if s.Error != "" {
								homeText(c, bootstrap.RedactInstallerText(s.Error), homeMeta).TextColor(tok.Danger)
							}
							if s.RuntimeStarted && !s.staleRuntimeOwner() {
								a.agentConnectionSettings(c)
								setupHeading(c, "记忆提炼")
								a.firstGenerationStepGuide(c)
								a.generationConfiguration(c, false)
							} else if !s.staleRuntimeOwner() && !s.Busy {
								homeText(c, "记忆服务尚未就绪", homeMeta)
								if a.Config.FirstInstall != nil && !s.OwnerStatusChecked && !s.ownerInspecting {
									v4SmallButton(c, "重新检查", v4IconRefresh, a.inspectOwnerRuntimeStatusOnlyAsync)
								} else {
									v4SmallButton(c, "重试启动", v4IconRefresh, func() {
										s.Error = ""
										a.beginFirstRuntime(3)
									}).Disabled(s.ownerInspecting || s.Loading || s.ExternalWorkerActive || s.workerInspecting ||
										s.selectedAgentsSaving || s.RestartingStaleDaemon || a.integrations.Busy ||
										(a.Config.FirstInstall != nil && (!s.OwnerStatusChecked || s.OwnerStatus == nil)))
								}
							}
						}
					})
				})
				a.v4PageEnter(c, pane, fmt.Sprintf("setup-step-%d", step), s.setupStepRun)
			})

			// Fixed footer: 环境详情 + hint | 上一步 / 继续
			ui.Row(c.Key("first-use-footer")).FillWidth().Height(60).Shrink(0).
				Padding(0, setupInset).Gap(sp3).AlignItems(ui.Center).
				Background(tok.Bg).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				blocked := s.Busy || s.RestartingStaleDaemon || a.integrations.Busy
				v4SmallButton(c, "环境详情", nil, func() { s.Details = true }).Disabled(s.Busy)
				hint := "可随时返回上一步核对"
				if step == 1 {
					hint = "不会提前修改宿主配置文件，准备完本地环境后再确认接入"
				} else if step == 3 {
					hint = a.firstGenerationEnableHint()
				}
				ui.Text(c, hint).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).Grow(1).MinWidth(0).MaxLines(2)
				if step > 1 {
					v4OutlineButton(c, "上一步", nil, func() { a.setInitialStep(step - 1) }).Disabled(blocked)
				}
				switch step {
				case 1:
					v4OutlineButton(c, "交给已选 Agent 自动安装", v4IconCopy, func() { a.copyFreshInstallPrompt(c) })
					v4PrimaryButton(c, "继续准备环境", func() {
						if s.HostsLoaded || s.SelectedAgentsExplicit {
							a.saveSelectedAgentsAsync()
						}
						a.setInitialStep(2)
					}).Disabled(blocked)
				case 2:
					disabled := blocked || s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving
					if s.Plan.Complete() && s.Loaded {
						v4PrimaryButton(c, "继续", a.continueEnvironment).Disabled(disabled ||
							(a.Config.FirstInstall != nil && !s.RuntimeStarted && (!s.OwnerStatusChecked || s.OwnerStatus == nil)))
					} else if len(s.Plan.Items) > 0 {
						label := "安装并继续"
						if isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy) {
							label = "更新 Agent 插件"
						}
						v4PrimaryButton(c, label, a.openEnvironmentReview).Disabled(disabled)
					}
				case 3:
					v4OutlineButton(c, "暂不配置提炼，先进入 Memlane", nil, a.deferFirstGenerationSetup).Disabled(!a.canDeferFirstGenerationSetup())
					v4PrimaryButton(c, "启用并完成", a.enableFirstRuntime).Disabled(a.firstGenerationEnableBlocker() != "")
				}
			})
		})
	})

	a.environmentReviewModal(c)
	a.initialEnvironmentDetails(c)
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
		if s.Busy || s.ExternalWorkerActive {
			a.environmentProgressBanner(c)
		} else {
			label := "准备本地记忆环境"
			if s.Loaded && s.Plan.Complete() {
				label = "已就绪"
			} else if s.Loaded && missingCount > 0 {
				label = fmt.Sprintf("尚缺 %d 项组件", missingCount)
			}
			ui.Row(c.Key("environment-summary")).FillWidth().Padding(sp3, 0).Gap(sp3).AlignItems(ui.Center).
				BorderWidth(1, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				homeText(c, label, homeBody).FontWeight(600).Grow(1)
				a.downloadSourceSelector(c, "initial-download-source")
				v4SmallButton(c, "重新检测", v4IconRefresh, func() {
					a.inspectOwnerRuntimeStatusAsync()
					a.inspectInstallWorkerAsync()
					a.refreshEnvironment()
				}).Disabled(s.Loading)
			})
		}
		environmentCapabilities(c)
		homeText(c, environmentSafetyPromise, homeMeta)
		if s.Error != "" {
			ui.Column(c.Key("environment-error-banner")).FillWidth().Padding(sp3, sp4).Gap(sp2).
				Radius(radius).Background(tok.Danger.Alpha(0.08)).Children(func() {
				homeText(c, "准备未完成", homeBody).TextColor(tok.Danger).FontWeight(600)
				homeText(c, bootstrap.RedactInstallerText(s.Error), homeMeta)
				v4SmallButton(c, "重试", v4IconRefresh, func() {
					if s.Journal.ApprovedDigest != "" && !s.Plan.Complete() {
						a.runEnvironment(false)
					} else {
						a.inspectOwnerRuntimeStatusAsync()
						a.refreshEnvironment()
					}
				}).Disabled(s.Busy || s.ExternalWorkerActive || s.Loading)
			})
		}
		label := fmt.Sprintf("查看技术明细 (%d 项)", len(s.Plan.Items))
		if s.TechDetailsOpen {
			label = "收起技术明细"
		}
		v4SmallButton(c, label, nil, func() { s.TechDetailsOpen = !s.TechDetailsOpen })
		if s.TechDetailsOpen {
			a.environmentTechnicalDetails(c)
		}
		ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
			homeText(c, "遇到环境问题？", homeMeta)
			v4SmallButton(c, "复制指令交给 Agent 帮我装/修", v4IconCopy, func() {
				prompt := agentTakeoverFreshInstallPrompt(s.SelectedAgents)
				if s.Error != "" || a.Config.FirstInstall != nil {
					prompt = agentTakeoverBeta2RescuePrompt()
				}
				c.WriteClipboard(prompt)
				s.Message = "已复制指令，请在 Agent 的新会话中粘贴执行"
			})
		})
		if s.Message != "" {
			homeText(c, s.Message, homeMeta)
		}
	})
}

const environmentSafetyPromise = "隔离在应用目录约 4–6 GiB · 绝不修改系统 Python 或环境变量 · 随时可一键卸载"

func environmentCapabilities(c *ui.Context) {
	v4Group(c, "准备内容", "", nil, func() {
		for _, capability := range []struct{ title, description string }{
			{"本地记忆引擎", "把你的对话自动整理成结构化记忆与关系图谱，数据仅存本机"},
			{"本地语义搜索模型", "离线理解中文与代码含义，不上传云端"},
			{"Agent 接入组件", "让 TRAE CN / Codex 等工具能自动写入和召回项目记忆"},
		} {
			v4Row(c, v4RowOptions{Title: capability.title, Desc: capability.description})
		}
	})
}

func (a *App) environmentTechnicalDetails(c *ui.Context) {
	s := &a.environment
	ui.Column(c).FillWidth().Gap(sp3).Children(func() {
		ui.Row(c).FillWidth().Gap(sp2).Children(func() {
			v4SmallButton(c, "指定已有环境", nil, func() { s.Details = true; s.Manual = true }).Disabled(s.Busy || s.Loading || s.ExternalWorkerActive)
			if len(s.Plan.Items) > 0 {
				v4SmallButton(c, "复制安装命令", v4IconCopy, func() { a.copyManualInstallScript(c) })
			}
		})
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
					if row.Requirement.ID == "embedding" && row.Requirement.Version != "" {
						desc = row.Requirement.Version
					}
					if row.Requirement.DownloadBytes > 0 || row.Requirement.InstalledBytes > 0 {
						desc += " · 下载 " + environmentSize(row.Requirement.DownloadBytes) + " · 占用 " + environmentSize(row.Requirement.InstalledBytes)
					}
					if digest := row.Requirement.Digest; digest != "" {
						desc += " · SHA-256 " + digest[:min(12, len(digest))]
					}
					dot, label, color := environmentDotState(status), environmentLabel(status), environmentStatusColor(status)
					policyUpdate := isPluginPolicyUpdate(row, status)
					if policyUpdate {
						dot, label, color = "run", "策略有更新", tok.Accent
					}
					description := func() {
						if policyUpdate {
							homeText(c, environmentIncompatibleHint(a, row), homeMeta)
						}
						if row.Requirement.Source != "" {
							homeText(c, bootstrap.RedactInstallerText(row.Requirement.Source), homeMeta).Font("ui-monospace").LineHeight(1.45)
						}
						if p := s.installProgress(); (s.Busy || s.ExternalWorkerActive) && p.Component == row.Requirement.ID {
							homeText(c, strings.Join(environmentProgressMetrics(p), " · "), homeMeta)
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

func (a *App) openEnvironmentReview() {
	a.environment.ReviewTechOpen = false
	a.environment.Review = true
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
	plainLanguage := a.initialSetup && !pluginOnly
	if plainLanguage {
		modalTitle = "确认安装本地记忆组件"
		var download, installed int64
		for _, item := range s.Plan.Items {
			if item.Observation.Status != bootstrap.Available {
				download += item.Requirement.DownloadBytes
				installed += item.Requirement.InstalledBytes
			}
		}
		modalIntro = "预计下载 " + environmentSize(download) + "，安装后新增占用 " + environmentSize(installed)
		modalHint = "隔离在应用目录约 4–6 GiB，绝不修改系统 Python 或环境变量，随时可一键卸载"
	}
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
		ConfirmDisabled: s.Loading || s.Busy || s.ExternalWorkerActive,
		OnCancel:        a.declineEnvironment,
		OnConfirm:       func() { a.runEnvironment(true) },
		ExtraFooter: func() {
			if !pluginOnly && (!plainLanguage || s.ReviewTechOpen) {
				v4SmallButton(c, "复制全部安装命令", v4IconCopy, func() { a.copyManualInstallScript(c) })
			}
		},
		Body: func() {
			ui.Text(c, modalIntro).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
			if plainLanguage {
				environmentCapabilities(c)
				label := "查看技术清单"
				if s.ReviewTechOpen {
					label = "收起技术清单"
				}
				v4SmallButton(c, label, nil, func() { s.ReviewTechOpen = !s.ReviewTechOpen })
				if !s.ReviewTechOpen {
					return
				}
			}
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
						if r.Digest != "" {
							ui.Text(c, "SHA-256 "+r.Digest).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						}
						switch r.ID {
						case "uv", "python", "runtime", "cognee-source", "embedding", "agent-plugin":
							ui.Row(c).FillWidth().Padding(2, 0, 0, 0).Children(func() {
								v4SmallButton(c, "复制此项安装命令", v4IconCopy, func() {
									c.WriteClipboard(bootstrap.ManualInstallCommand(itemCopy, s.Environment, s.DownloadSource))
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

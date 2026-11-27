package settings

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) environmentSetup(c *ui.Context, initial bool) {
	tok := a.tokens()
	if initial {
		a.onboardingEnvironment(c)
		a.environmentReviewModal(c)
		a.initialEnvironmentDetails(c)
		return
	}
	s := &a.environment
	if !s.Loaded && !s.Loading && a.canDispatch() {
		a.refreshEnvironment()
	}
	pendingPluginApply := a.hasPendingPluginConfigApply()
	ui.Column(c.Key("settings-environment-section")).FillWidth().Gap(sp3).Children(func() {
		kitui.Group(c, tok, "运行环境", "", func() {
			if s.Plan.Complete() && !s.Busy && !pendingPluginApply {
				kitui.Badge(c, tok, "已就绪", "ok")
			}
			if s.Loading && s.Loaded {
				kitui.Spinner(c, tok)
			}
			kitui.SmallButton(c, tok, "重新检测", kitui.IconRefresh, a.refreshEnvironment).Disabled(s.Loading || s.Busy)
			label := "查看详情"
			if s.Details {
				label = "收起详情"
			}
			kitui.SmallButton(c, tok, label, nil, func() { s.Details = !s.Details })
		}, func() {
			if s.Loading && !s.Loaded {
				kitui.SkeletonGroup(c, tok, 3, 2, true, true)
				return
			}
			kitui.Row(c, tok, kitui.RowOptions{
				Key:   "settings-download-source-row",
				Title: "下载来源",
				Desc:  "国内镜像源优先走清华 / 阿里云 / 中科大 / npmmirror / ModelScope，仍按固定 SHA256 严格校验",
				Control: func() {
					a.downloadSourceSelector(c, "settings-download-source")
				},
			})
			for _, item := range s.Plan.Items {
				row := item
				status := environmentRowStatus(row, s.Journal, s.Busy)
				dailyUpdate := isDailyPluginUpdateAvailable(a, row, status)
				dotState := environmentDotState(status)
				valueLabel := environmentLabel(status)
				valueColor := serviceStatusColor(tok, dotState)
				var extraHint string
				var extraColor ui.Color
				var detailReason string
				if pendingPluginApply && row.Requirement.ID == "agent-plugin" {
					dotState = "warn"
					valueLabel = "待写入配置"
					valueColor = tok.Warning
					extraHint = "新版 Agent 插件已准备并校验完成，但尚未写入当前日常配置；请点击下方「写入插件配置」重试。"
					extraColor = tok.Text2
				} else if policyUpdate := isPluginPolicyUpdate(row, status); dailyUpdate || policyUpdate {
					dotState = "run"
					valueLabel = "有更新"
					if policyUpdate {
						valueLabel = "策略有更新"
					}
					valueColor = tok.Accent
					extraHint = environmentIncompatibleHint(a, row)
					extraColor = tok.Text2
					if s.Details && row.Observation.Reason != "" {
						detailReason = "技术详情：" + bootstrap.RedactInstallerText(row.Observation.Reason)
					}
				} else if status == bootstrap.Incompatible {
					if hint := environmentIncompatibleHint(a, row); hint != "" {
						extraHint = hint
						extraColor = tok.Warning
					}
				} else if s.Details && row.Observation.Reason != "" && status != bootstrap.Available && status != bootstrap.Ready {
					extraHint = bootstrap.RedactInstallerText(row.Observation.Reason)
					extraColor = tok.Text3
				}
				var descExtra func()
				if extraHint != "" || detailReason != "" {
					hintText, hintCol, techText := extraHint, extraColor, detailReason
					descExtra = func() {
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							if hintText != "" {
								ui.Text(c, hintText).Font("system-ui").FontSize(fsCaption).TextColor(hintCol).LineHeight(1.5)
							}
							if techText != "" {
								ui.Text(c, techText).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
							}
						})
					}
				}
				kitui.Row(c, tok, kitui.RowOptions{
					Key:       "environment-" + row.Requirement.ID,
					Title:     dependencyDisplayName(row.Requirement),
					Desc:      dependencyDisplayVersion(row, s.Environment),
					DescMono:  true,
					DescExtra: descExtra,
					Tooltip:   row.Requirement.Version,
					Leading: func() {
						ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
							kitui.StatusDot(c, tok, dotState)
							dependencyMark(tok, c, row.Requirement.ID)
						})
					},
					Value:      valueLabel,
					ValueColor: valueColor,
				})
			}
		})

		if s.Error != "" {
			ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp1).Radius(radius).Background(tok.Danger.Alpha(0.08)).Children(func() {
				ui.Text(c, "准备未完成").Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Danger)
				summary := ui.Text(c, bootstrap.RedactInstallerText(s.Error)).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text2).LineHeight(1.6)
				if !s.Details {
					summary.MaxLines(2)
				}
			})
		}
		if s.Message != "" {
			ui.Text(c, s.Message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
		}
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
			a.setupStageBanner(c, stageTitle, "请稍候，完成后将自动校验依赖状态。", stopFn)
		} else {
			needInstall := !s.Plan.Complete() && len(s.Plan.Items) > 0
			if needInstall || pendingPluginApply || s.Error != "" {
				actionLabel := "安装缺少的依赖"
				if isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy) {
					actionLabel = "更新 Agent 插件"
				}
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					if pendingPluginApply {
						kitui.SmallPrimaryButton(c, tok, "写入插件配置", a.applyPendingPluginConfig).Disabled(s.Loading)
					} else if needInstall {
						kitui.SmallPrimaryButton(c, tok, actionLabel, func() { s.Review = true }).Disabled(s.Loading)
						kitui.SmallButton(c, tok, "复制安装命令", kitui.IconCopy, func() { a.copyManualInstallScript(c) }).Disabled(s.Loading)
					}
					if s.Error != "" {
						kitui.SmallButton(c, tok, "重试", kitui.IconRefresh, func() {
							if s.Journal.ApprovedDigest != "" {
								a.runEnvironment(false)
							} else {
								a.refreshEnvironment()
							}
						})
						kitui.SmallButton(c, tok, "用 Agent 排查", nil, func() {
							_, prompt := a.environmentDiagnostic()
							c.WriteClipboard(prompt)
							s.Message = "已复制排查提示词"
							s.AgentOpen = true
							s.DiagnosticAgents = bootstrap.ExistingDiagnosticAgents(nil, nil)
						})
					}
				})
			}
		}
		a.environmentDiagnosticAgents(c)
		if s.Details {
			a.environmentLocations(c)
			ui.Row(c.Key("environment-detail-actions")).FillWidth().AlignItems(ui.Center).Gap(sp2).Padding(sp2, 0, 0, 0).Children(func() {
				kitui.SmallButton(c, tok, "复制诊断材料", kitui.IconCopy, func() {
					report, _ := a.environmentDiagnostic()
					c.WriteClipboard(report)
					s.Message = "脱敏诊断材料已复制"
				})
				kitui.SmallButton(c, tok, "导出诊断包", nil, a.exportEnvironmentDiagnostic).Disabled(s.Exporting || a.host.ChooseDiagnosticPath == nil)
				ui.Spacer(c)
				kitui.SmallButton(c, tok, "指定已有路径", nil, a.openEnvironmentPaths).Disabled(s.Busy || s.Loading)
			})
		}
	})
	a.environmentReviewModal(c)
	a.environmentPathsDialog(c)
}

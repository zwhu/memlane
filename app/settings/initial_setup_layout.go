package settings

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func setupHeading(tok *kitui.Tokens, c *ui.Context, text string) {
	ui.Text(c, text).Font(kitui.FontUI).FontSize(fsTitle).FontWeight(600).TextColor(tok.Text1).LineHeight(1.3)
}

func setupDescription(tok *kitui.Tokens, c *ui.Context, text string) {
	ui.Text(c, text).Font(kitui.FontUI).FontSize(fsBody).FontWeight(400).TextColor(tok.Text2).LineHeight(1.6)
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

func environmentStatusColor(tok *kitui.Tokens, status bootstrap.Status) ui.Color {
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

func (a *Controller) setupStageBanner(c *ui.Context, title, subtitle string, stopFn func()) {
	tok := a.tokens()
	ui.Row(c.Key("setup-stage-banner")).FillWidth().Padding(sp4).Gap(sp4).
		Radius(radius).Background(tok.Fill1).AlignItems(ui.Center).Children(func() {
		kitui.StageBlocks(c, tok)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).Font(kitui.FontUI).FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).LineHeight(1.4)
			if subtitle != "" {
				ui.Text(c, subtitle).Font(kitui.FontUI).FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text2).LineHeight(1.45)
			}
		})
		if stopFn != nil {
			kitui.SmallButton(c, tok, "停止安装", nil, stopFn)
		}
	})
}

const environmentSafetyPromise = "隔离在应用目录约 4–6 GiB · 绝不修改系统 Python 或环境变量 · 随时可一键卸载"

func environmentCapabilities(tok *kitui.Tokens, c *ui.Context) {
	kitui.Group(c, tok, "准备内容", "", nil, func() {
		for _, capability := range []struct{ title, description string }{
			{"本地记忆引擎", "把你的对话自动整理成结构化记忆与关系图谱，数据仅存本机"},
			{"本地语义搜索模型", "离线理解中文与代码含义，不上传云端"},
			{"Agent 接入组件", "让 TRAE CN / Codex 等工具能自动写入和召回项目记忆"},
		} {
			kitui.Row(c, tok, kitui.RowOptions{Title: capability.title, Desc: capability.description})
		}
	})
}

func (a *Controller) environmentReviewModal(c *ui.Context) {
	tok := a.tokens()
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
		if a.initialSetup || a.config.ProjectControl == nil || a.config.ProjectControl.PluginDir == "" || a.config.ConfigFile == "" {
			modalIntro = "将下载并校验更新后的 Agent 插件，原插件目录会保留。"
			modalHint = "校验通过后可继续准备运行环境"
		}
		cancelText = "暂不更新"
		confirmText = "确认更新"
	}
	kitui.Dialog(c, tok, &s.Review, kitui.DialogOptions{
		Key:             "environment-install-plan",
		Title:           modalTitle,
		Width:           kitui.DialogLarge,
		ScrollBody:      true,
		Hint:            modalHint,
		CancelText:      cancelText,
		ConfirmText:     confirmText,
		ConfirmDisabled: s.Loading || s.Busy || s.ExternalWorkerActive,
		OnCancel:        a.declineEnvironment,
		OnConfirm:       func() { a.runEnvironment(true) },
		ExtraFooter: func() {
			if !pluginOnly && (!plainLanguage || s.ReviewTechOpen) {
				kitui.SmallButton(c, tok, "复制全部安装命令", kitui.IconCopy, func() { a.copyManualInstallScript(c) })
			}
		},
		Body: func() {
			ui.Text(c, modalIntro).Font(kitui.FontUI).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
			if plainLanguage {
				environmentCapabilities(tok, c)
				label := "查看技术清单"
				if s.ReviewTechOpen {
					label = "收起技术清单"
				}
				kitui.SmallButton(c, tok, label, nil, func() { s.ReviewTechOpen = !s.ReviewTechOpen })
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
								kitui.StatusDot(c, tok, "run")
							}
							dependencyMark(tok, c, r.ID)
							ui.Text(c, r.Name+" · "+r.Version).Font(kitui.FontUI).FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).Grow(1).MinWidth(0)
							if isPluginPolicyUpdate(item, item.Observation.Status) {
								ui.Text(c, "策略有更新").FontSize(fsCaption).TextColor(tok.Accent).SingleLine()
							}
							ui.Text(c, "下载 "+environmentSize(r.DownloadBytes)+"，占用 "+environmentSize(r.InstalledBytes)).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
						})
						ui.Text(c, r.Purpose).Font(kitui.FontUI).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
						if item.Observation.Status == bootstrap.Incompatible {
							if hint := environmentIncompatibleHint(a, item); hint != "" {
								prefix := "原因："
								col := tok.Warning
								if isPluginPolicyUpdate(item, item.Observation.Status) || isDailyPluginUpdateAvailable(a, item, item.Observation.Status) {
									prefix = "说明："
									col = tok.Text2
								}
								ui.Text(c, prefix+hint).Font(kitui.FontUI).FontSize(fsCaption).TextColor(col).LineHeight(1.5)
							}
							if s.Details && item.Observation.Reason != "" && isDailyPluginUpdateAvailable(a, item, item.Observation.Status) {
								ui.Text(c, "技术详情："+bootstrap.RedactInstallerText(item.Observation.Reason)).Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
							}
						}
						if r.SizeNote != "" {
							ui.Text(c, r.SizeNote).Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
						}
						sourceLabel := "来源：" + r.Source
						if r.OfficialSource != "" && r.OfficialSource != r.Source {
							sourceLabel = "当前来源：" + r.Source
						}
						ui.Text(c, sourceLabel).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						if r.OfficialSource != "" && r.OfficialSource != r.Source {
							ui.Text(c, "官方原址："+r.OfficialSource).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						}
						ui.Text(c, "位置："+r.Destination).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						if r.Digest != "" {
							ui.Text(c, "SHA-256 "+r.Digest).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
						}
						switch r.ID {
						case "uv", "python", "runtime", "cognee-source", "embedding", "agent-plugin":
							ui.Row(c).FillWidth().Padding(4, 0, 0, 0).Children(func() {
								kitui.SmallButton(c, tok, "复制此项安装命令", kitui.IconCopy, func() {
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

func (a *Controller) initialEnvironmentDetails(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	if !s.Details {
		return
	}
	kitui.Dialog(c, tok, &s.Details, kitui.DialogOptions{
		Key:         "first-use-environment-details",
		Title:       "环境详情",
		Width:       kitui.DialogLarge,
		ScrollBody:  true,
		Hint:        "路径与校验结果已脱敏，不含任何密钥或聊天内容",
		CancelText:  "关闭",
		HideConfirm: true,
		ExtraFooter: func() {
			kitui.SmallButton(c, tok, "复制诊断材料", kitui.IconCopy, func() {
				report, _ := a.environmentDiagnostic()
				c.WriteClipboard(report)
				s.Message = "脱敏诊断材料已复制"
			})
			kitui.SmallButton(c, tok, "导出诊断包", nil, a.exportEnvironmentDiagnostic).Disabled(s.Exporting || a.host.ChooseDiagnosticPath == nil)
		},
		Body: func() {
			a.environmentDiagnosticAgents(c)
			if s.Error != "" {
				ui.Text(c, s.Error).Font(kitui.FontUI).FontSize(fsFootnote).TextColor(tok.Danger).LineHeight(1.6)
			}
			kitui.Group(c, tok, "已检测组件路径", "", nil, func() {
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
					kitui.Row(c, tok, kitui.RowOptions{
						Key:        "details-" + row.Requirement.ID,
						Title:      dependencyDisplayName(row.Requirement) + " · " + dependencyDisplayVersion(row, s.Environment),
						Desc:       desc,
						DescMono:   true,
						Leading:    func() { kitui.StatusDot(c, tok, environmentDotState(status)) },
						Value:      environmentLabel(status),
						ValueColor: environmentStatusColor(tok, status),
					})
				}
			})
			if s.Manual {
				ui.Column(c).FillWidth().Gap(sp2).Margin(sp2, 0, 0, 0).Children(func() {
					ui.Text(c, "指定已有路径").Font(kitui.FontUI).FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
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
							ui.Text(c, field.label).Font(kitui.FontUI).FontSize(fsFootnote).TextColor(tok.Text2)
							ui.TextInput(c.Key(field.label), field.value).FillWidth().Label(field.label).Disabled(s.Busy || s.Loading)
						})
					}
					ui.Row(c).FillWidth().Padding(sp2, 0, 0, 0).Children(func() {
						kitui.OutlineButton(c, tok, "检测这些路径", nil, func() { a.detectEnvironment(true) }).Disabled(s.Busy || s.Loading)
					})
				})
			}
		},
	})
}

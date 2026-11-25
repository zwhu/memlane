package settings

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func onboardingSelectionPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return bootstrap.DefaultRuntimeConfig()
}

func (a *Controller) refreshLocalHosts() {
	s := &a.environment
	if s.HostsLoading || !a.canDispatch() {
		return
	}
	s.HostsLoading = true
	s.HostDetectGen++
	generation := s.HostDetectGen
	detect := a.detectLocalHosts
	if detect == nil {
		detect = bootstrap.DetectLocalHosts
	}
	configPath, root := a.config.ConfigFile, os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
	loadSelection := !s.HostsLoaded && !s.SelectedAgentsExplicit
	go func() {
		hosts := detect(bootstrap.HostDetectOptions{BetaPrimaryOnly: true})
		var saved bootstrap.Selections
		var err error
		if loadSelection {
			var path string
			path, err = onboardingSelectionPath(configPath)
			if err == nil {
				saved, err = bootstrap.LoadSelections(root, path)
			}
		}
		a.dispatch(func() {
			if s.HostDetectGen != generation {
				return
			}
			s.Hosts, s.HostsLoading, s.HostsLoaded = hosts, false, true
			if !s.SelectedAgentsExplicit {
				if loadSelection && err == nil && (saved.SelectedAgentsConfigured || len(saved.SelectedAgents) > 0) {
					// An existing selection can deliberately be empty.
					s.SelectedAgents = bootstrap.NormalizeSelectedAgents(saved.SelectedAgents)
					s.SelectedAgentsExplicit = true
				} else {
					var detected []string
					for _, host := range hosts {
						if host.BetaPrimary && host.Detected {
							detected = append(detected, host.Kind)
						}
					}
					s.SelectedAgents = bootstrap.NormalizeSelectedAgents(detected)
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						s.Message = "无法读取已保存的 Agent 选择，请核对后重试。"
					}
				}
			}
			if a.initialSetup && s.InitialStep == 3 && s.RuntimeStarted {
				a.integrations.PinnedAgentKinds = s.SelectedAgents
				a.preloadPinnedConnectionPreviews()
			}
		})
	}()
}

func (a *Controller) toggleSelectedAgent(kind string) {
	s := &a.environment
	selected := slices.Clone(s.SelectedAgents)
	if i := slices.Index(selected, kind); i >= 0 {
		selected = slices.Delete(selected, i, i+1)
	} else {
		selected = append(selected, kind)
	}
	s.SelectedAgents = bootstrap.NormalizeSelectedAgents(selected)
	a.saveSelectedAgentsAsync()
}

func (a *Controller) saveSelectedAgentsAsync() {
	s := &a.environment
	s.SelectedAgentsExplicit = true
	if !a.canDispatch() {
		return
	}
	// Serialize writes so a slow earlier toggle cannot overwrite a later choice.
	if s.selectedAgentsSaving {
		s.selectedAgentsDirty = true
		return
	}
	s.selectedAgentsSaving = true
	selected := slices.Clone(s.SelectedAgents)
	configPath, root := a.config.ConfigFile, os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
	go func() {
		path, err := onboardingSelectionPath(configPath)
		if err == nil {
			err = bootstrap.SaveSelectedAgents(root, path, selected)
		}
		a.dispatch(func() {
			s.selectedAgentsSaving = false
			defer a.advanceFirstRuntimeStartup()
			if s.selectedAgentsDirty {
				s.selectedAgentsDirty = false
				a.saveSelectedAgentsAsync()
				return
			}
			if err != nil {
				s.Message = "未能保存 Agent 选择，请重试。"
			}
		})
	}()
}

func (s *EnvironmentSetupState) detectedOnboardingHosts() []bootstrap.DetectedHost {
	var detected []bootstrap.DetectedHost
	for _, host := range s.Hosts {
		if host.BetaPrimary && host.Detected {
			detected = append(detected, host)
		}
	}
	return detected
}

func (a *Controller) initialSelectAgentStep(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	hosts := s.detectedOnboardingHosts()
	if s.HostsLoaded && len(hosts) == 0 {
		onboardingHeading(tok, c, onboardingAgents, "还没有检测到支持的 Agent", "安装下面任一 Agent 后点「重新检测」；也可以先跳过，环境准备好后再到「设置 › Agent 与模型」接入。")
		kitui.Group(c, tok, "Memlane 支持的 Agent", "", func() {
			kitui.GhostButton(c, tok, "重新检测", kitui.IconRefresh, a.refreshLocalHosts).Disabled(s.HostsLoading)
		}, func() { a.onboardingSupportRows(c) })
		return
	}
	onboardingHeading(tok, c, onboardingAgents, "你在用哪些 Agent？", "选中的 Agent 会接入 Memlane：对话自动采集，新会话自动召回记忆。这一步只记录选择，不会改动任何配置。")
	summary := "本机已检测到…"
	if s.HostsLoaded {
		summary = fmt.Sprintf("本机已检测到 %d 个", len(hosts))
	}
	ui.Column(c).FillWidth().Gap(sp2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Height(28).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Text(c, summary).FontSize(fsCaption).TextColor(tok.Text3).FontWeight(600).Grow(1)
			kitui.GhostButton(c, tok, "重新检测", kitui.IconRefresh, a.refreshLocalHosts).Disabled(s.HostsLoading)
		})
		if !s.HostsLoaded {
			for range 2 {
				ui.Row(c).FillWidth().Gap(sp2).Children(func() {
					for range 2 {
						ui.Row(c).Grow(1).Height(60).Gap(sp3).Padding(sp3).AlignItems(ui.Center).Border(1, tok.Border).Radius(8).Children(func() {
							kitui.SkeletonBar(c, tok, 28, 28, 7)
							ui.Column(c).Gap(sp2).Children(func() {
								kitui.SkeletonBar(c, tok, 100, 10, 4)
								kitui.SkeletonBar(c, tok, 70, 8, 4)
							})
						})
					}
				})
			}
			return
		}
		for start := 0; start < len(hosts); start += 2 {
			ui.Row(c).FillWidth().Gap(sp2).Children(func() {
				a.onboardingAgentCard(c, hosts[start])
				if start+1 < len(hosts) {
					a.onboardingAgentCard(c, hosts[start+1])
				}
			})
		}
	})
	if s.HostsLoaded {
		selected := 0
		for _, host := range hosts {
			if slices.Contains(s.SelectedAgents, host.Kind) {
				selected++
			}
		}
		if selected > 0 {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, kitui.IconCheck).FontSize(13).TextColor(tok.OK)
				ui.Text(c, fmt.Sprintf("已选 %d 个，第 3 步统一确认接入", selected)).FontSize(fsFootnote).TextColor(tok.Text2)
			})
		} else {
			kitui.Text(c, tok, "不选也可以继续，之后在「设置 › Agent 与模型」里接入", kitui.RoleMeta)
		}
	}
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		kitui.Text(c, tok, "没找到你的 Agent？", kitui.RoleMeta)
		kitui.GhostButton(c, tok, "查看支持列表", nil, func() { a.onboarding.support = true })
	})
	if s.Message != "" {
		kitui.Text(c, tok, s.Message, kitui.RoleMeta)
	}
}

func (a *Controller) onboardingAgentCard(c *ui.Context, host bootstrap.DetectedHost) {
	tok := a.tokens()
	selected := slices.Contains(a.environment.SelectedAgents, host.Kind)
	bg, rim := tok.Bg, tok.Border
	if selected {
		bg, rim = tok.AccentSofter, tok.Accent
	}
	card := ui.ButtonBase(c.Key(host.Kind)).Role(ui.RoleCheckBox).Label("选择 "+host.DisplayName).Checked(selected).
		Width((onboardingWidth-sp2)/2).MinHeight(60).Radius(8).Border(1, rim).Padding(sp3).Gap(sp3).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).OnClick(func() { a.toggleSelectedAgent(host.Kind) })
	card.Background(kitui.HoverColor(card, "agent-card", bg, tok.Hover))
	kitui.ButtonPress(card, 0.99)
	card.Children(func() {
		ui.Image(c, agentOriginalIcon(host.Kind)).Size(28, 28).Shrink(0).Fit(ui.Contain)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				kitui.Text(c, tok, host.DisplayName, kitui.RoleBody).FontWeight(600).SingleLine()
				if host.Version != "" {
					ui.Text(c, host.Version).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
				}
			})
			capability := "自动采集与召回"
			if host.SupportsProcessingCLI && host.Executable != "" {
				capability = "也可用于提炼记忆"
			}
			ui.Text(c, capability).FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
		})
		onboardingSelectionCheck(tok, c.Key(selected), selected, 4)
	})
}

func (a *Controller) onboardingSupportRows(c *ui.Context) {
	tok := a.tokens()
	for _, host := range a.environment.Hosts {
		if !host.BetaPrimary {
			continue
		}
		desc := "自动采集与召回"
		if host.SupportsProcessingCLI {
			desc += " · 也可用于提炼记忆"
		}
		kitui.Row(c, tok, kitui.RowOptions{
			Key: "supported-" + host.Kind, Title: host.DisplayName, Desc: desc,
			Leading: func() { ui.Image(c, agentOriginalIcon(host.Kind)).Size(22, 22).Fit(ui.Contain) },
			DescExtra: func() {
				if !host.Detected && host.InstallCommand != "" {
					ui.Text(c, host.InstallCommand).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3)
				}
			},
			Control: func() {
				switch {
				case host.Detected:
					kitui.Badge(c, tok, "已检测到", "ok")
				case host.InstallCommand != "":
					kitui.SmallButton(c, tok, "复制安装命令", kitui.IconCopy, func() {
						c.WriteClipboard(host.InstallCommand)
						a.showOnboardingToast("已复制 " + host.DisplayName + " 安装命令")
					}).Label("复制安装命令 " + host.DisplayName)
				case host.DocsURL != "":
					kitui.SmallButton(c, tok, "下载 "+host.DisplayName, nil, func() {
						if a.host.OpenExternalURL == nil {
							c.OpenURL(host.DocsURL)
						} else if err := a.host.OpenExternalURL(host.DocsURL); err != nil {
							a.showOnboardingToast(bootstrap.RedactInstallerText(err.Error()))
						}
					})
				}
			},
		})
	}
}

func (a *Controller) onboardingSupportDialog(c *ui.Context) {
	tok := a.tokens()
	kitui.Dialog(c, tok, &a.onboarding.support, kitui.DialogOptions{
		Key: "onboarding-support-list", Title: "Memlane 支持的 Agent", Width: kitui.DialogSmall, ScrollBody: true,
		Hint: "安装完成后回到这里点「重新检测」", CancelText: "关闭", ConfirmText: "重新检测",
		ConfirmDisabled: a.environment.HostsLoading,
		OnConfirm:       a.refreshLocalHosts,
		Body: func() {
			kitui.Text(c, tok, "公测阶段重点支持以下 Agent。检测依据是本机能找到对应程序（命令行工具或 /Applications 中的应用）。", kitui.RoleMeta)
			a.onboardingSupportRows(c)
		},
	})
}

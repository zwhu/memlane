package simpledesktop

import (
	"errors"
	"os"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func onboardingSelectionPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return bootstrap.DefaultRuntimeConfig()
}

func (a *App) refreshLocalHosts() {
	s := &a.environment
	if s.HostsLoading || a.Update == nil {
		return
	}
	s.HostsLoading = true
	s.HostDetectGen++
	generation := s.HostDetectGen
	detect := a.DetectLocalHostsFn
	if detect == nil {
		detect = bootstrap.DetectLocalHosts
	}
	configPath, root := a.Config.ConfigFile, os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
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
		a.Update(func() {
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
			if a.initialSetup && s.InitialStep == 3 {
				a.integrations.PinnedAgentKinds = s.SelectedAgents
				a.preloadPinnedConnectionPreviews()
			}
		})
	}()
}

func (a *App) toggleSelectedAgent(kind string) {
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

func (a *App) saveSelectedAgentsAsync() {
	s := &a.environment
	s.SelectedAgentsExplicit = true
	if a.Update == nil {
		return
	}
	// Serialize writes so a slow earlier toggle cannot overwrite a later choice.
	if s.selectedAgentsSaving {
		s.selectedAgentsDirty = true
		return
	}
	s.selectedAgentsSaving = true
	selected := slices.Clone(s.SelectedAgents)
	configPath, root := a.Config.ConfigFile, os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
	go func() {
		path, err := onboardingSelectionPath(configPath)
		if err == nil {
			err = bootstrap.SaveSelectedAgents(root, path, selected)
		}
		a.Update(func() {
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

func needsStep1ProcessingCLINotice(hosts []bootstrap.DetectedHost, selected []string) (bool, []string) {
	if len(selected) == 0 || bootstrap.HasDetectedProcessingCLI(hosts) {
		return false, nil
	}
	var names []string
	for _, kind := range selected {
		for _, host := range hosts {
			if host.Kind != kind {
				continue
			}
			if host.Detected && host.SupportsProcessingCLI && host.Executable != "" {
				return false, nil
			}
			if host.SupportsCapture {
				names = append(names, host.DisplayName)
			}
			break
		}
	}
	return len(names) > 0, names
}

func (a *App) copyFreshInstallPrompt(c *ui.Context) {
	s := &a.environment
	c.WriteClipboard(agentTakeoverFreshInstallPrompt(s.SelectedAgents))
	s.InstallPromptCopied = true
	s.Message = "已复制安装指令（零 Skill），请在已选 Agent 的新会话中粘贴执行"
}

func (a *App) initialSelectAgentStep(c *ui.Context) {
	s := &a.environment
	if !s.HostsLoaded && !s.HostsLoading && a.Update != nil {
		a.refreshLocalHosts()
	}
	v4Group(c, "本机已检测的 Agent", "", func() {
		v4SmallButton(c, "重新检测", v4IconRefresh, a.refreshLocalHosts).Disabled(s.HostsLoading)
	}, func() {
		if !s.HostsLoaded {
			v4SkeletonGroup(c, 3, 2, true, true)
		}
		for _, host := range s.Hosts {
			if !host.BetaPrimary {
				continue
			}
			label, state := "未检测到", ""
			if host.Detected {
				label, state = "已安装", "ok"
			}
			capability := ""
			if host.Detected && host.SupportsProcessingCLI && host.Executable != "" {
				capability = "自动采集 · 可用于记忆提炼"
			} else if host.Detected && host.SupportsCapture {
				capability = "自动采集 · 提炼需搭配 CLI 或模型 API"
			}
			v4Row(c, v4RowOptions{
				Key: "select-host-" + host.Kind, Title: host.DisplayName,
				TitleExtra: func() {
					if host.Version != "" {
						homeText(c, host.Version, homeMeta).SingleLine()
					}
				},
				Leading: func() {
					ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
						v4SelectionIndicator(c, slices.Contains(s.SelectedAgents, host.Kind))
						ui.Image(c, agentOriginalIcon(host.Kind)).Size(20, 20).Fit(ui.Contain)
					})
				},
				Control: func() {
					if capability != "" {
						v4Badge(c, capability, "")
					}
					v4Badge(c, label, state)
				},
				Clickable: true, HideChevron: true, Label: "选择 " + host.DisplayName,
				OnClick: func() { a.toggleSelectedAgent(host.Kind) },
			})
		}
	})
	if s.HostsLoaded && len(s.SelectedAgents) == 0 {
		homeText(c, "稍后再连，先准备环境", homeMeta)
	}
	if show, names := needsStep1ProcessingCLINotice(s.Hosts, s.SelectedAgents); show {
		a.step1ProcessingCLINotice(c, names[0])
	}
	if s.InstallPromptCopied && a.OpenExistingAgent != nil {
		for _, host := range s.Hosts {
			if host.Detected && host.Application != "" && slices.Contains(s.SelectedAgents, host.Kind) {
				v4SmallButton(c, "打开 "+host.DisplayName, nil, func() {
					if err := a.OpenExistingAgent(host.Application); err != nil {
						s.Message = bootstrap.RedactInstallerText(err.Error())
					}
				})
				break
			}
		}
	}
	if s.Message != "" {
		homeText(c, s.Message, homeMeta)
	}
}

func (a *App) step1ProcessingCLINotice(c *ui.Context, hostName string) {
	s := &a.environment
	ui.Column(c.Key("processing-cli-notice")).FillWidth().Gap(sp2).Padding(sp4).
		Radius(radius).Background(tok.Fill1).Border(1, tok.Border).Children(func() {
		homeText(c, "提示：把对话自动整理成记忆需要一个后台提炼工具", homeBody).FontWeight(600)
		homeText(c, "你当前选择的工具只负责自动采集对话。在最后一步启用记忆提炼时，可以搭配一个本地 CLI（复用已有账号额度）或直接填写模型 API / Ollama。", homeMeta)
		ui.Row(c).FillWidth().Gap(sp2).Children(func() {
			label := "查看推荐 CLI 安装方式"
			if s.Step1CLIGuideOpen {
				label = "收起推荐 CLI"
			}
			v4SmallButton(c, label, nil, func() { s.Step1CLIGuideOpen = !s.Step1CLIGuideOpen })
			v4SmallButton(c, "已装好，重新检测", v4IconRefresh, a.refreshLocalHosts).Disabled(s.HostsLoading)
			v4SmallButton(c, "复制指令，让 "+hostName+" 帮我安装 CLI", v4IconCopy, func() {
				c.WriteClipboard(agentTakeoverInstallCLIPrompt(hostName))
				s.Message = "已复制 CLI 安装指令，请在 Agent 的新会话中粘贴执行"
			})
		})
		if !s.Step1CLIGuideOpen {
			return
		}
		a.recommendedProcessingCLIRows(c, &s.Message)
	})
}

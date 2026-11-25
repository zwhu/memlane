package simpledesktop

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

type onboardingPage uint8

const (
	onboardingWelcome onboardingPage = iota
	onboardingAgents
	onboardingEnv
	onboardingConnect
	onboardingProcess
	onboardingDone
)

const onboardingWidth = 600

var (
	onboardingTerminalIcon = v4IconSVG(`<rect x="2.5" y="4" width="15" height="12" rx="2"/><path d="m6 7 3 3-3 3m5 0h3"/>`)
	onboardingShieldIcon   = v4IconSVG(`<path d="M10 2.5l6 2.5v5c0 3.9-2.6 6.5-6 8-3.4-1.5-6-4.1-6-8V5z"/>`)
	onboardingChatIcon     = v4IconSVG(`<path d="M3.5 4h13v9h-9l-4 3z"/>`)
	onboardingSparkIcon    = v4IconSVG(`<path d="M10 3v4m0 6v4M3 10h4m6 0h4M5 5l2 2m6 6 2 2m0-10-2 2m-6 6-2 2"/>`)
	onboardingNextIcon     = v4IconSVG(`<path d="M7 15h6m-5 2.5h4M10 3a5 5 0 0 0-3 9v1h6v-1a5 5 0 0 0-3-9z"/>`)
)

type onboardingState struct {
	page, lastPage                             onboardingPage
	visited                                    onboardingPage
	run                                        uint64
	takeover, fullPrompt, support, watchWorker bool
	toast, method                              string
	toastUntil, doneAt                         time.Time
}

func (a *App) onboardingPage() onboardingPage {
	if a.onboarding.page != onboardingWelcome {
		return a.onboarding.page
	}
	switch {
	case a.environment.InitialStep >= 3:
		return onboardingConnect
	case a.environment.InitialStep == 2 || a.environment.ExternalWorkerActive:
		return onboardingEnv
	default:
		return onboardingWelcome
	}
}

func (a *App) onboardingBusy() bool {
	s := &a.environment
	return s.Busy || s.RestartingStaleDaemon || s.ExternalWorkerActive || s.onboardingStartRequested || a.integrations.Busy
}

func (a *App) navigateOnboarding(page onboardingPage) {
	if a.onboardingBusy() || a.onboardingPage() == onboardingDone {
		return
	}
	switch page {
	case onboardingWelcome:
		a.setInitialStep(1)
		if a.environment.InitialStep == 1 {
			a.onboarding.page = onboardingWelcome
		}
	case onboardingAgents, onboardingEnv, onboardingConnect:
		a.setInitialStep(int(page))
	case onboardingProcess:
		a.showOnboardingProcess()
	}
}

func (a *App) showOnboardingProcess() {
	if a.onboardingBusy() || a.environment.InitialStep != 3 || !a.environment.RuntimeStarted || a.environment.staleRuntimeOwner() {
		return
	}
	a.onboarding.page = onboardingProcess
}

func (a *App) showOnboardingDone(method string) {
	a.onboarding.page = onboardingDone
	a.onboarding.method = method
	a.onboarding.doneAt = time.Time{}
	a.onboarding.toast = ""
}

func (a *App) finishOnboarding() {
	a.initialSetup = false
	a.page = "总览"
	a.Start()
}

func (a *App) showOnboardingToast(message string) {
	a.onboarding.toast = message
	a.onboarding.toastUntil = time.Now().Add(2200 * time.Millisecond)
}

func onboardingHeading(c *ui.Context, step onboardingPage, title, description string) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		homeText(c, fmt.Sprintf("第 %d 步，共 4 步", step), homeMeta).FontSize(fsCaption)
		setupHeading(c, title)
		setupDescription(c, description)
	})
}

func onboardingPrimary(c *ui.Context, label string, disabled bool, run func()) ui.Element {
	caption := label
	if !disabled {
		caption += "  ↵"
	}
	return v4PrimaryButton(c, caption, run).Label(label).Height(32).Padding(0, 14).Disabled(disabled)
}

func (a *App) initialSetupShell(c *ui.Context) {
	s, o := &a.environment, &a.onboarding
	page := a.onboardingPage()
	centered := page == onboardingWelcome || page == onboardingDone
	if o.run == 0 || o.lastPage != page {
		o.run++
		o.lastPage = page
	}
	if page > o.visited {
		o.visited = page
	}
	if (s.ExternalWorkerActive || (o.watchWorker && page <= onboardingEnv)) && !s.Busy && !a.windowHidden {
		c.After(time.Second)
		if !time.Now().Before(s.WorkerPollNext) {
			a.inspectInstallWorkerAsync()
		}
	}
	if page >= onboardingProcess && !a.windowHidden && !a.windowInactive && a.integrations.hasPendingAgentReceipts() {
		c.After(2 * time.Second)
		a.refreshAgentReceiptsIfDue(time.Now())
	}
	if page == onboardingProcess {
		a.prepareOnboardingProcessDraft()
	}
	ui.Column(c.Key("first-use-shell")).Fill().Background(tok.Bg).Children(func() {
		a.onboardingBar(c, page, centered)
		if centered {
			label, disabled, run := a.onboardingPrimaryAction(page)
			disabled = disabled || a.onboardingBusy()
			ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				body := ui.Column(c.Key(o.run)).Width(onboardingWidth).AlignItems(ui.Center).Gap(sp4)
				body.Children(func() {
					if page == onboardingWelcome {
						a.onboardingWelcomeContent(c, label, disabled, run)
					} else {
						a.onboardingDoneContent(c, label, disabled, run)
					}
				})
				a.onboardingPageEnter(c, body, o.run)
			})
		} else {
			ui.Scroll(c.Key(fmt.Sprintf("first-use-body-%d", page))).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(40, 0, sp8, 0).Children(func() {
					pane := ui.Column(c.Key(o.run)).Width(onboardingWidth).Gap(sp5)
					pane.Children(func() {
						a.staleDaemonBanner(c)
						if s.ExternalWorkerActive && page != onboardingEnv {
							a.environmentProgressBanner(c)
						}
						switch page {
						case onboardingAgents:
							a.initialSelectAgentStep(c)
						case onboardingEnv:
							a.onboardingEnvironment(c)
						case onboardingConnect:
							if s.RuntimeStarted && !s.staleRuntimeOwner() {
								a.onboardingConnect(c)
							} else if !s.staleRuntimeOwner() {
								a.onboardingConnectionRuntime(c)
							}
						case onboardingProcess:
							a.onboardingProcess(c)
						}
					})
					a.onboardingPageEnter(c, pane, o.run)
				})
			})
			label, disabled, run := a.onboardingPrimaryAction(page)
			a.onboardingFooter(c, page, label, disabled || a.onboardingBusy(), run)
		}
		if o.toast != "" && time.Now().Before(o.toastUntil) {
			c.After(time.Second)
			ui.Row(c).Absolute().Bottom(76).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
				ui.Text(c, o.toast).FontSize(fsFootnote).TextColor(tok.Text1).Padding(sp2, sp4).
					Background(tok.BgElevated).Border(1, tok.Border).Radius(radiusMenu)
			})
		}
	})
	a.initialEnvironmentDetails(c)
	a.onboardingSupportDialog(c)
	a.onboardingTakeoverDialog(c)
}

func (a *App) onboardingShortcuts(c *ui.Context) {
	page := a.onboardingPage()
	if a.environment.Details || a.onboarding.support || a.onboarding.takeover || a.onboardingBusy() ||
		(page == onboardingProcess && a.onboardingGeneration.inputFocused) {
		return
	}
	if _, disabled, run := a.onboardingPrimaryAction(page); !disabled && run != nil {
		c.OnShortcut(0, ui.KeyEnter, run)
	}
}

func (a *App) onboardingPrimaryAction(page onboardingPage) (string, bool, func()) {
	s := &a.environment
	switch page {
	case onboardingWelcome:
		return "开始设置", false, func() {
			a.navigateOnboarding(onboardingAgents)
			a.refreshLocalHosts()
			if !s.Loaded {
				a.refreshEnvironment()
			}
		}
	case onboardingAgents:
		label := "继续"
		if s.HostsLoaded && len(s.detectedOnboardingHosts()) == 0 {
			label = "先跳过，继续准备环境"
		}
		return label, !s.HostsLoaded, func() {
			s.SelectedAgents = slices.DeleteFunc(slices.Clone(s.SelectedAgents), func(kind string) bool {
				return !slices.ContainsFunc(s.Hosts, func(host bootstrap.DetectedHost) bool {
					return host.Kind == kind && host.BetaPrimary && host.Detected
				})
			})
			a.saveSelectedAgentsAsync()
			a.navigateOnboarding(onboardingEnv)
			if !s.Loaded {
				a.refreshEnvironment()
			}
		}
	case onboardingEnv:
		return a.onboardingEnvironmentAction()
	case onboardingConnect:
		if !s.RuntimeStarted || s.staleRuntimeOwner() {
			return "继续", true, nil
		}
		return a.onboardingConnectAction()
	case onboardingProcess:
		return a.onboardingProcessAction()
	case onboardingDone:
		return "进入 Memlane", false, a.finishOnboarding
	}
	return "", true, nil
}

func (a *App) onboardingConnectionRuntime(c *ui.Context) {
	s := &a.environment
	onboardingHeading(c, onboardingConnect, "接入 Agent", "准备好本地记忆服务后，即可确认接入所选 Agent。")
	if s.Error != "" {
		homeText(c, s.Error, homeMeta).TextColor(tok.Danger)
	}
	if s.Busy && s.RuntimeStage != "" {
		a.setupStageBanner(c, s.RuntimeStage+"…", "等待本地服务就绪", s.Cancel)
		return
	}
	if a.Config.FirstInstall != nil && (!s.OwnerStatusChecked || s.OwnerStatus == nil) {
		v4SmallButton(c, "重新检查", v4IconRefresh, a.inspectOwnerRuntimeStatusOnlyAsync).Disabled(s.ownerInspecting)
		return
	}
	v4SmallButton(c, "重试启动", v4IconRefresh, func() { a.beginFirstRuntime(3) }).
		Disabled(s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving)
}

func (a *App) onboardingBar(c *ui.Context, page onboardingPage, centered bool) {
	bar := ui.Row(c.Key("first-use-topbar")).FillWidth().Height(52).Shrink(0).Padding(0, sp4).AlignItems(ui.Center)
	if !centered {
		bar.BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
	}
	bar.Children(func() {
		ui.Row(c).Width(240).Height(52).Padding(0, 0, 0, 70).Gap(sp2).DragWindow().AlignItems(ui.Center).Children(func() {
			ui.Image(c, tok.BrandMark).Size(20, 20).Fit(ui.Contain)
			homeText(c, "Memlane", homeBody).FontWeight(600)
		})
		ui.Row(c).Grow(1).Height(52).AlignItems(ui.Center).Justify(ui.Center).DragWindow().Children(func() {
			if centered {
				return
			}
			for i, title := range []string{"选择 Agent", "准备环境", "接入", "记忆提炼"} {
				step := onboardingAgents + onboardingPage(i)
				if i > 0 {
					color := tok.BorderStrong
					if step <= a.onboarding.visited {
						color = tok.OK.Alpha(0.6)
					}
					ui.Box(c).Size(28, 1).Shrink(0).Background(color)
				}
				a.onboardingStep(c, page, step, title)
			}
		})
		ui.Row(c).Width(240).Height(52).Justify(ui.End).AlignItems(ui.Center).DragWindow().Children(func() {
			if page != onboardingDone {
				a.onboardingHelpMenu(c)
			}
		})
	})
}

func (a *App) onboardingStep(c *ui.Context, current, step onboardingPage, title string) {
	active, done := step == current, step < a.onboarding.visited && step != current
	fg, badgeBg, badgeFg := tok.Text3, tok.Fill2, tok.Text3
	if active {
		fg, badgeBg, badgeFg = tok.Accent, tok.Accent, tok.OnAccent
	} else if done {
		fg, badgeBg, badgeFg = tok.Text1, tok.OK, tok.OnAccent
	}
	body := func() {
		ui.Row(c).Size(18, 18).Shrink(0).Radius(9).Background(badgeBg).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if done {
				ui.Icon(c, v4IconCheck).FontSize(12).TextColor(badgeFg)
			} else {
				ui.Text(c, fmt.Sprint(step)).FontSize(fsCaption).FontWeight(600).TextColor(badgeFg)
			}
		})
		ui.Text(c, title).FontSize(fsFootnote).TextColor(fg).SingleLine()
	}
	if done && !a.onboardingBusy() {
		button := ui.ButtonBase(c.Key(step)).Role(ui.RoleButton).Label(title).Height(28).Padding(0, sp2).Gap(6).
			Radius(radius).BorderWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() { a.navigateOnboarding(step) })
		button.Background(homeHoverColor(button, "step", ui.Transparent, tok.Hover)).Children(body)
	} else {
		ui.Row(c.Key(step)).Height(28).Padding(0, sp2).Gap(6).AlignItems(ui.Center).Children(body)
	}
}

func (a *App) onboardingHelpMenu(c *ui.Context) {
	s := &a.environment
	v4GhostButton(c, "遇到问题", v4IconInfo, nil).PopoverMenuButton(func(m *ui.Menu) {
		if m.Item("查看环境详情").Chosen() {
			s.Details, s.Manual = true, false
		}
		if m.Item("复制诊断，交给 Agent 排查").Chosen() {
			a.copyOnboardingDiagnostic(c)
		}
		if m.Item("导出诊断包…").Disabled(s.Exporting || a.ChooseDiagnosticPath == nil).Chosen() {
			a.exportEnvironmentDiagnostic()
		}
		m.Separator()
		if m.Item("指定已有组件路径…").Disabled(a.onboardingBusy() || s.Loading).Chosen() {
			s.Details, s.Manual = true, true
		}
	})
}

func (a *App) onboardingFooter(c *ui.Context, page onboardingPage, label string, disabled bool, run func()) {
	ui.Row(c.Key("first-use-footer")).FillWidth().Height(64).Shrink(0).Padding(0, sp6).Gap(sp2).
		AlignItems(ui.Center).Background(tok.Bg).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		hint := ""
		switch page {
		case onboardingEnv:
			hint = a.onboardingEnvironmentHint()
		case onboardingConnect:
			hint = a.onboardingConnectHint()
			if a.canDeferOnboardingConnections() || a.onboardingConnection.BatchRunning {
				v4GhostButton(c, "稍后接入", nil, a.deferOnboardingConnections).Disabled(a.onboardingBusy())
			}
		case onboardingProcess:
			hint = "会发送一次简短测试请求，可能消耗少量额度"
			v4GhostButton(c, "稍后配置，先进入 Memlane", nil, a.deferFirstGenerationSetup).Disabled(!a.canDeferFirstGenerationSetup())
		}
		ui.Text(c, hint).FontSize(fsFootnote).TextColor(tok.Text3).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Center)
		v4OutlineButton(c, "上一步", nil, func() { a.navigateOnboarding(page - 1) }).Height(32).Padding(0, 14).Disabled(a.onboardingBusy())
		if page == onboardingProcess {
			if edit := a.onboardingProcessEditAction(); edit != nil {
				v4OutlineButton(c, "修改配置", nil, edit).Height(32).Padding(0, 14)
			}
		}
		onboardingPrimary(c, label, disabled, run)
	})
}

func (a *App) onboardingWelcomeContent(c *ui.Context, primary string, disabled bool, run func()) {
	s := &a.environment
	onboardingMascot(c, !a.windowHidden && !a.windowInactive).Margin(0, 0, sp1, 0)
	ui.Column(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(c, "换个 Agent，接着做。").FontSize(26).FontWeight(600).TextColor(tok.Text1).LineHeight(1.3)
		ui.Text(c, "项目记忆留在你的 Mac 上。\n换一个编码 Agent，也能用上之前积累的项目知识。").
			FontSize(fsReading).TextColor(tok.Text2).LineHeight(1.6).TextAlign(ui.Center)
	})
	ui.Row(c).FillWidth().Gap(sp3).AlignItems(ui.Stretch).Margin(sp4, 0).Children(func() {
		for _, value := range []struct {
			icon        *ui.SVG
			title, desc string
		}{
			{onboardingChatIcon, "自动采集", "接入后，Agent 的对话与工具调用自动记录，无需手动整理。"},
			{onboardingSparkIcon, "本地提炼", "把零散对话提炼成项目约定、踩坑经验和代码关联。"},
			{v4IconRecall, "按需召回", "新会话里 Agent 会自动找回相关记忆，少走回头路。"},
		} {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(sp4).Gap(sp2).Radius(8).Background(tok.Fill1).Children(func() {
				ui.Box(c).Size(28, 28).Radius(7).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, value.icon).FontSize(16).TextColor(tok.Accent)
				})
				homeText(c, value.title, homeBody).FontWeight(600)
				ui.Text(c, value.desc).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.55)
			})
		}
	})
	hosts := s.detectedOnboardingHosts()
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Children(func() {
		onboardingPrimary(c, primary, disabled, run).Height(36).Padding(0, sp5)
		if !s.HostsLoaded || len(hosts) > 0 {
			button := ui.ButtonBase(c).Role(ui.RoleButton).Label("让 Agent 代我安装").
				Height(36).Padding(0, sp5).Gap(6).Radius(radius).Border(1, tok.Accent).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				OnClick(func() { a.onboarding.takeover, a.onboarding.fullPrompt = true, false })
			button.Background(homeHoverColor(button, "takeover", tok.AccentSofter, tok.AccentSoft))
			v4ButtonPress(button, 0.96)
			button.Children(func() {
				ui.Icon(c, onboardingTerminalIcon).FontSize(14).TextColor(tok.Accent)
				ui.Text(c, "让 Agent 代我安装").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent)
			})
		}
	})
	ui.Column(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, onboardingShieldIcon).FontSize(13).TextColor(tok.Text3)
			ui.Text(c, "数据与模型只留在本机 · 随时可一键卸载").FontSize(fsFootnote).TextColor(tok.Text3)
		})
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			if !s.HostsLoaded || s.Loading {
				v4Spinner(c)
				ui.Text(c, "正在检测本机已有的 Agent 与组件…").FontSize(fsFootnote).TextColor(tok.Text3)
			} else {
				ui.Icon(c, v4IconCheck).FontSize(12).TextColor(tok.OK)
				ui.Text(c, fmt.Sprintf("已检测到 %d 个 Agent · 大约 3 分钟完成设置", len(hosts))).FontSize(fsFootnote).TextColor(tok.Text3)
			}
		})
	})
}

func (a *App) onboardingTakeoverDialog(c *ui.Context) {
	o := &a.onboarding
	if !o.takeover {
		return
	}
	hosts := a.environment.detectedOnboardingHosts()
	var kinds, names []string
	for _, host := range hosts {
		kinds = append(kinds, host.Kind)
		names = append(names, host.DisplayName)
	}
	destination := "已安装的 Agent"
	if len(names) > 0 {
		destination = strings.Join(names, "、")
	}
	prompt := agentTakeoverFreshInstallPrompt(kinds)
	v4Dialog(c, &o.takeover, v4DialogOptions{
		Key: "onboarding-agent-takeover", Title: "让 Agent 代我安装", Width: v4DialogSmall, ScrollBody: true,
		ConfirmText: "复制指令", Hint: "完成后回到这里，Memlane 会自动识别",
		OnConfirm: func() {
			c.WriteClipboard(prompt)
			o.takeover, o.watchWorker = false, true
			a.showOnboardingToast("已复制，请在 Agent 的新会话中粘贴")
		},
		Body: func() {
			homeText(c, "复制指令到 "+destination+" 的新会话，它会在终端里替你完成准备。", homeMeta)
			for i, item := range []string{
				"先检查本机环境，告诉你还缺什么",
				"每次要改动前先给你看预览，等你确认",
				"下载组件、接入 Agent、启动本地记忆服务",
				"遇到需要你亲自操作的步骤会停下来告诉你",
			} {
				homeText(c, fmt.Sprintf("%d. %s", i+1, item), homeBody)
			}
			homeText(c, "指令不会读取密钥或聊天内容，也不会手动改动配置文件。", homeMeta)
			v4GhostButton(c, "查看完整指令", v4IconChev, func() { o.fullPrompt = !o.fullPrompt })
			if o.fullPrompt {
				ui.Scroll(c).FillWidth().MaxHeight(180).Radius(radius).Background(tok.Fill1).Children(func() {
					ui.Text(c, prompt).Font("ui-monospace").FontSize(fsCaption).Padding(sp3).TextColor(tok.Text1)
				})
			}
		},
	})
}

func (a *App) onboardingDoneContent(c *ui.Context, primary string, disabled bool, run func()) {
	if a.onboarding.doneAt.IsZero() {
		a.onboarding.doneAt = c.Now()
	}
	onboardingCompletionMark(c, a.onboarding.doneAt)
	ui.Column(c).AlignItems(ui.Center).Gap(6).Children(func() {
		setupHeading(c, "Memlane 已就绪")
		setupDescription(c, "本地记忆服务正在运行。关闭窗口后它会在菜单栏继续工作。")
	})
	rows := a.onboardingConnectionRows()
	var verified, pending int
	for _, row := range rows {
		label, _ := a.onboardingConnectionStatus(row)
		switch label {
		case "已生效":
			verified++
		case "待新会话":
			pending++
		}
	}
	connectionSummary := "暂未接入"
	connectionDot := ""
	if len(rows) > 0 {
		connectionSummary = fmt.Sprintf("%d 个已生效 · %d 个待新会话", verified, pending)
		if left := len(rows) - verified - pending; left > 0 {
			connectionSummary += fmt.Sprintf(" · %d 个未接入", left)
		}
		if pending > 0 {
			connectionDot = "run"
		} else if verified > 0 {
			connectionDot = "ok"
		}
	}
	v4Group(c, "", "", nil, func() {
		v4Row(c, v4RowOptions{Title: "本地记忆服务", Desc: "记忆引擎与接入网关 · 仅本机访问",
			Leading: func() { v4StatusDot(c, "ok") }, Value: "运行中", ValueColor: tok.OK})
		v4Row(c, v4RowOptions{Title: "Agent 接入", Desc: connectionSummary, Leading: func() { v4StatusDot(c, connectionDot) },
			Control: func() {
				ui.Row(c).Gap(2).Children(func() {
					for _, row := range rows {
						icon := ui.Image(c, agentOriginalIcon(row.Kind)).Size(18, 18).Fit(ui.Contain).Label(connectionName(row.Kind))
						label, _ := a.onboardingConnectionStatus(row)
						if label != "已生效" && label != "待新会话" {
							icon.Opacity(0.35)
						}
					}
				})
			}})
		v4Row(c, v4RowOptions{Title: "记忆提炼", Desc: a.onboarding.method, Leading: func() { v4StatusDot(c, "ok") }})
	})
	if pending > 0 {
		ui.Row(c).FillWidth().Padding(sp3, sp4).Gap(10).AlignItems(ui.Start).Radius(radius).Background(tok.AccentSofter).Children(func() {
			ui.Icon(c, onboardingNextIcon).FontSize(15).TextColor(tok.Accent).Shrink(0)
			ui.Text(c, "下一步：重启已接入的 Agent，在项目里新建会话、正常提问。第一条记忆通常会在几分钟内出现在总览里。").
				Grow(1).MinWidth(0).FontSize(fsFootnote).LineHeight(1.6).TextColor(tok.Text2)
		})
	}
	onboardingPrimary(c, primary, disabled, run).Height(36).Padding(0, sp5).Margin(sp4, 0, 0, 0)
}

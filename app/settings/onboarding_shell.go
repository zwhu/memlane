package settings

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
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
	onboardingTerminalIcon = kitui.IconSVG(`<rect x="2.5" y="4" width="15" height="12" rx="2"/><path d="m6 7 3 3-3 3m5 0h3"/>`)
	onboardingShieldIcon   = kitui.IconSVG(`<path d="M10 2.5l6 2.5v5c0 3.9-2.6 6.5-6 8-3.4-1.5-6-4.1-6-8V5z"/>`)
	onboardingChatIcon     = kitui.IconSVG(`<path d="M3.5 4h13v9h-9l-4 3z"/>`)
	onboardingSparkIcon    = kitui.IconSVG(`<path d="M10 3v4m0 6v4M3 10h4m6 0h4M5 5l2 2m6 6 2 2m0-10-2 2m-6 6-2 2"/>`)
	onboardingNextIcon     = kitui.IconSVG(`<path d="M7 15h6m-5 2.5h4M10 3a5 5 0 0 0-3 9v1h6v-1a5 5 0 0 0-3-9z"/>`)
)

type onboardingState struct {
	page, lastPage                             onboardingPage
	visited                                    onboardingPage
	run                                        uint64
	takeover, fullPrompt, support, watchWorker bool
	toast, method                              string
	toastUntil, doneAt                         time.Time
}

func (a *Controller) onboardingPage() onboardingPage {
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

func (a *Controller) onboardingBusy() bool {
	s := &a.environment
	return s.Busy || s.RestartingStaleDaemon || s.ExternalWorkerActive || s.onboardingStartRequested || a.integrations.Busy
}

func (a *Controller) navigateOnboarding(page onboardingPage) {
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

func (a *Controller) showOnboardingProcess() {
	if a.onboardingBusy() || a.environment.InitialStep != 3 || !a.environment.RuntimeStarted || a.environment.staleRuntimeOwner() {
		return
	}
	a.onboarding.page = onboardingProcess
}

func (a *Controller) showOnboardingDone(method string) {
	a.onboarding.page = onboardingDone
	a.onboarding.method = method
	a.onboarding.doneAt = time.Time{}
	a.onboarding.toast = ""
}

func (a *Controller) finishOnboarding() {
	a.initialSetup = false
	a.navigateOverview()
	a.Start()
}

func (a *Controller) showOnboardingToast(message string) {
	a.onboarding.toast = message
	a.onboarding.toastUntil = time.Now().Add(2200 * time.Millisecond)
}

func onboardingHeading(tok *kitui.Tokens, c *ui.Context, step onboardingPage, title, description string) {
	ui.Column(c).FillWidth().Gap(sp1).Children(func() {
		kitui.Text(c, tok, fmt.Sprintf("第 %d 步，共 4 步", step), kitui.RoleMeta).FontSize(fsCaption)
		setupHeading(tok, c, title)
		setupDescription(tok, c, description)
	})
}

func onboardingPrimary(tok *kitui.Tokens, c *ui.Context, label string, disabled bool, run func()) ui.Element {
	caption := label
	if !disabled {
		caption += "  ↵"
	}
	return kitui.PrimaryButton(c, tok, caption, run).Label(label).Height(32).Padding(0, 16).Disabled(disabled)
}

func (a *Controller) initialSetupShell(c *ui.Context) {
	tok := a.tokens()
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
	if (s.ExternalWorkerActive || (o.watchWorker && page <= onboardingEnv)) && !s.Busy && !a.windowIsHidden() {
		c.After(time.Second)
		if !time.Now().Before(s.WorkerPollNext) {
			a.inspectInstallWorkerAsync()
		}
	}
	if page >= onboardingProcess && !a.windowIsHidden() && !a.windowIsInactive() && a.integrations.hasPendingAgentReceipts() {
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

func (a *Controller) onboardingShortcuts(c *ui.Context) {
	page := a.onboardingPage()
	if a.environment.Details || a.onboarding.support || a.onboarding.takeover || a.onboardingBusy() ||
		(page == onboardingProcess && a.onboardingGeneration.inputFocused) {
		return
	}
	if _, disabled, run := a.onboardingPrimaryAction(page); !disabled && run != nil {
		c.OnShortcut(0, ui.KeyEnter, run)
	}
}

func (a *Controller) onboardingPrimaryAction(page onboardingPage) (string, bool, func()) {
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

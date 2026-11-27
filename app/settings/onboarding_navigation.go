package settings

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) onboardingConnectionRuntime(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	onboardingHeading(tok, c, onboardingConnect, "接入 Agent", "准备好本地记忆服务后，即可确认接入所选 Agent。")
	if s.Error != "" {
		kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger)
	}
	if s.Busy && s.RuntimeStage != "" {
		a.setupStageBanner(c, s.RuntimeStage+"…", "等待本地服务就绪", s.Cancel)
		return
	}
	if a.config.FirstInstall != nil && (!s.OwnerStatusChecked || s.OwnerStatus == nil) {
		kitui.SmallButton(c, tok, "重新检查", kitui.IconRefresh, a.inspectOwnerRuntimeStatusOnlyAsync).Disabled(s.ownerInspecting)
		return
	}
	kitui.SmallButton(c, tok, "重试启动", kitui.IconRefresh, func() { a.beginFirstRuntime(3) }).
		Disabled(s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving)
}

func (a *Controller) onboardingBar(c *ui.Context, page onboardingPage, centered bool) {
	tok := a.tokens()
	bar := ui.Row(c.Key("first-use-topbar")).FillWidth().Height(52).Shrink(0).Padding(0, sp4).AlignItems(ui.Center)
	if !centered {
		bar.BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
	}
	bar.Children(func() {
		ui.Row(c).Width(240).Height(52).Padding(0, 0, 0, 72).Gap(sp2).DragWindow().AlignItems(ui.Center).Children(func() {
			ui.Image(c, a.host.BrandMark).Size(20, 20).Fit(ui.Contain)
			kitui.Text(c, tok, "Memlane", kitui.RoleBody).FontWeight(600)
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

func (a *Controller) onboardingStep(c *ui.Context, current, step onboardingPage, title string) {
	tok := a.tokens()
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
				ui.Icon(c, kitui.IconCheck).FontSize(12).TextColor(badgeFg)
			} else {
				ui.Text(c, fmt.Sprint(step)).FontSize(fsCaption).FontWeight(600).TextColor(badgeFg)
			}
		})
		ui.Text(c, title).FontSize(fsFootnote).TextColor(fg).SingleLine()
	}
	if done && !a.onboardingBusy() {
		button := ui.ButtonBase(c.Key(step)).Role(ui.RoleButton).Label(title).Height(28).Padding(0, sp2).Gap(8).
			Radius(radius).BorderWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).OnClick(func() { a.navigateOnboarding(step) })
		button.Background(kitui.HoverColor(button, "step", ui.Transparent, tok.Hover)).Children(body)
	} else {
		ui.Row(c.Key(step)).Height(28).Padding(0, sp2).Gap(8).AlignItems(ui.Center).Children(body)
	}
}

func (a *Controller) onboardingHelpMenu(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	kitui.GhostButton(c, tok, "遇到问题", kitui.IconInfo, nil).PopoverMenuButton(func(m *ui.Menu) {
		if m.Item("查看环境详情").Chosen() {
			s.Details, s.Manual = true, false
		}
		if m.Item("复制诊断，交给 Agent 排查").Chosen() {
			a.copyOnboardingDiagnostic(c)
		}
		if m.Item("导出诊断包…").Disabled(s.Exporting || a.host.ChooseDiagnosticPath == nil).Chosen() {
			a.exportEnvironmentDiagnostic()
		}
		m.Separator()
		if m.Item("指定已有组件路径…").Disabled(a.onboardingBusy() || s.Loading).Chosen() {
			s.Details, s.Manual = true, true
		}
	})
}

func (a *Controller) onboardingFooter(c *ui.Context, page onboardingPage, label string, disabled bool, run func()) {
	tok := a.tokens()
	ui.Row(c.Key("first-use-footer")).FillWidth().Height(64).Shrink(0).Padding(0, sp6).Gap(sp2).
		AlignItems(ui.Center).Background(tok.Bg).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		hint := ""
		switch page {
		case onboardingEnv:
			hint = a.onboardingEnvironmentHint()
		case onboardingConnect:
			hint = a.onboardingConnectHint()
			if a.canDeferOnboardingConnections() || a.onboardingConnection.BatchRunning {
				kitui.GhostButton(c, tok, "稍后接入", nil, a.deferOnboardingConnections).Disabled(a.onboardingBusy())
			}
		case onboardingProcess:
			hint = "会发送一次简短测试请求，可能消耗少量额度"
			kitui.GhostButton(c, tok, "稍后配置，先进入 Memlane", nil, a.deferFirstGenerationSetup).Disabled(!a.canDeferFirstGenerationSetup())
		}
		ui.Text(c, hint).FontSize(fsFootnote).TextColor(tok.Text3).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Center)
		kitui.OutlineButton(c, tok, "上一步", nil, func() { a.navigateOnboarding(page - 1) }).Height(32).Padding(0, 16).Disabled(a.onboardingBusy())
		if page == onboardingProcess {
			if edit := a.onboardingProcessEditAction(); edit != nil {
				kitui.OutlineButton(c, tok, "修改配置", nil, edit).Height(32).Padding(0, 16)
			}
		}
		onboardingPrimary(tok, c, label, disabled, run)
	})
}

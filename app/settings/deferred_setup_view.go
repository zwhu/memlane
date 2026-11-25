package settings

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func toastPrimaryButtonTextColor(tok *kitui.Tokens) ui.Color {
	if tok.Dark {
		return tok.Bg
	}
	return tok.OnAccent
}

func (a *Controller) setupDeferredToast(c *ui.Context) {
	tok := a.tokens()
	title := "尚未配置记忆提炼"
	desc := "已先进入主界面，新会话暂不自动整理成记忆。完成 CLI 登录或配置模型 API 后即可开启。"
	s := &a.integrations
	if s.GenerationLoaded && s.GenerationError == "" && len(s.Generation.AgentBridges) == 0 && !a.hasDetectedProcessingCLI() {
		title = "尚未安装记忆提炼 CLI"
		desc = "已先进入主界面，对话采集可正常使用；如需自动整理成记忆，请安装本机 CLI 或配置模型 API。"
	}
	dismiss := func() { a.setupDeferredToastDismissed = true }
	ui.Column(c.Key("setup-deferred-toast")).Absolute().Right(sp5).Bottom(sp5).Width(360).
		Padding(sp3, sp4).Gap(sp2).Background(tok.BgElevated).Border(1, tok.Border).
		Radius(radiusMenu).Shadow(0, 12, 32, 0, tok.ShadowLift).Children(func() {
		ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
			kitui.StatusDot(c, tok, "warn")
			kitui.Text(c, tok, title, kitui.RolePanelHeading).Grow(1).MinWidth(0)
			closeButton := ui.ButtonBase(c).Role(ui.RoleButton).Label("关闭记忆提炼提示").
				Cursor(ui.CursorPointer).Size(24, 24).Radius(radius).BorderWidth(0).
				AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
			closeButton.Background(kitui.HoverColor(closeButton, "toast-close-bg", ui.Transparent, tok.Hover))
			iconColor := tok.Text2
			if closeButton.Hovered() {
				iconColor = tok.Text1
			}
			closeButton.Children(func() {
				ui.Icon(c, kitui.IconClose).FontSize(13).TextColor(iconColor)
			}).OnClick(dismiss)
		})
		kitui.Text(c, tok, desc, kitui.RoleBody).LineHeight(1.5)
		ui.Row(c).FillWidth().Gap(sp2).Justify(ui.End).Children(func() {
			kitui.SmallButton(c, tok, "知道了", nil, dismiss)
			b := ui.ButtonBase(c).Role(ui.RoleButton).Label("去配置").
				Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Radius(radius).
				BorderWidth(0).AlignItems(ui.Center).Shrink(0)
			b.Background(kitui.HoverColor(b, "toast-pbtn-bg", tok.Accent, tok.AccentHover))
			kitui.ButtonPress(b, 0.96)
			b.Children(func() {
				ui.Text(c, "去配置").Font("system-ui").FontSize(fsCaption).FontWeight(500).
					TextColor(toastPrimaryButtonTextColor(tok)).SingleLine()
			}).OnClick(func() {
				dismiss()
				a.openSettingsFromDeferredBanner()
			})
		})
	})
}

func (a *Controller) setupDeferredGenerationBanner(c *ui.Context, showAction bool) {
	tok := a.tokens()
	if a.initialSetup || !a.isSetupOnlyRuntime() {
		return
	}
	ui.Row(c.Key("setup-deferred-generation-banner")).FillWidth().Shrink(0).
		Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Center).
		Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
		kitui.Text(c, tok, "尚未配置记忆提炼模型，新会话暂不自动提炼", kitui.RoleMeta).Grow(1).MinWidth(0)
		if showAction {
			kitui.SmallButton(c, tok, "立即配置", nil, a.openSettingsFromDeferredBanner)
		}
	})
}

func (a *Controller) showSetupDeferredToast(inSettings bool) bool {
	return !a.setupDeferredToastDismissed && !a.initialSetup && a.isSetupOnlyRuntime() &&
		a.integrations.Generation.Active == nil && !(inSettings && a.access.SettingsSection == "记忆提炼")
}

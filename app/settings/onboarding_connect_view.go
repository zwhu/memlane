package settings

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) onboardingConnect(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if !a.windowIsHidden() && !a.windowIsInactive() && s.hasPendingAgentReceipts() {
		c.After(2 * time.Second)
		a.refreshAgentReceiptsIfDue(time.Now())
	}
	rows := a.onboardingConnectionRows()
	if len(rows) == 0 {
		if a.environment.HostsLoading {
			onboardingHeading(tok, c, onboardingConnect, "接入 Agent", "正在读取 Agent 选择…")
			kitui.Spinner(c, tok)
			return
		}
		onboardingHeading(tok, c, onboardingConnect, "接入 Agent", "你在第 1 步没有选择 Agent。")
		ui.Row(c).FillWidth().Padding(sp4).Gap(sp3).Radius(8).Background(tok.Fill1).AlignItems(ui.Center).Children(func() {
			ui.Image(c, kitui.IconSettings).Size(32, 32).TextColor(tok.Accent)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				kitui.Text(c, tok, "暂不接入也可以", kitui.RoleBody).FontWeight(600)
				kitui.Text(c, tok, "记忆提炼仍可先配置好。之后在「设置 › Agent 连接」随时接入。", kitui.RoleMeta)
			})
			kitui.SmallButton(c, tok, "返回选择", nil, func() { a.navigateOnboarding(onboardingAgents) }).Disabled(a.onboardingConnectionBusy())
		})
		return
	}
	ui.Column(c).FillWidth().Gap(sp1).Children(func() {
		kitui.Text(c, tok, "第 3 步，共 4 步", kitui.RoleMeta).FontSize(fsCaption)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			setupHeading(tok, c, "接入你选择的 Agent")
			ui.Spacer(c)
			kitui.GhostButton(c, tok, "调整选择", nil, func() { a.navigateOnboarding(onboardingAgents) }).Disabled(a.onboardingConnectionBusy())
		})
		setupDescription(tok, c, "Memlane 会在这些 Agent 的配置里加入记忆采集与召回入口。只改动下面列出的文件，改动前自动备份。")
	})
	if s.Error != "" {
		kitui.Text(c, tok, bootstrap.RedactInstallerText(s.Error), kitui.RoleMeta).TextColor(tok.Danger)
	}
	kitui.Group(c, tok, fmt.Sprintf("%d 个 Agent", len(rows)), "", func() {
		if a.onboardingConnection.BatchRunning {
			kitui.SmallButton(c, tok, "停止接入", nil, func() { a.onboardingConnection.StopBatch = true }).Disabled(a.onboardingConnection.StopBatch)
		}
	}, func() {
		for _, row := range rows {
			a.onboardingConnectRow(c, row)
		}
	})
	for _, row := range rows {
		status, _ := a.onboardingConnectionStatus(row)
		if status == "待新会话" || status == "已生效" {
			onboardingRestartCallout(tok, c)
			break
		}
	}
}

func onboardingRestartCallout(tok *kitui.Tokens, c *ui.Context) {
	ui.Row(c).FillWidth().Padding(sp4).Gap(sp3).Radius(8).Background(tok.Fill1).Children(func() {
		ui.Row(c).Size(28, 28).Shrink(0).Radius(7).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, kitui.IconRefresh).FontSize(15).TextColor(tok.Accent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			kitui.Text(c, tok, "最后一步：重启已接入的 Agent", kitui.RoleBody).FontWeight(600)
			kitui.Text(c, tok, "1. 在 Agent 里按 ⌘Q 完全退出，再重新打开", kitui.RoleMeta)
			kitui.Text(c, tok, "2. 在项目里新建会话，正常发一条消息", kitui.RoleMeta)
			kitui.Text(c, tok, "不用在这里等待：验证会在后台自动完成，结果显示在总览和菜单栏。", kitui.RoleMeta).TextColor(tok.Text3)
		})
	})
}

var onboardingMoreIcon = kitui.IconSVG(`<path stroke-width="2.6" d="M4 10h.01M10 10h.01M16 10h.01"/>`)

func (a *Controller) onboardingConnectRow(c *ui.Context, row AgentConnection) {
	tok := a.tokens()
	s := &a.integrations
	name := connectionName(row.Kind)
	preview, previewPresent := s.ConnectionPreviews[row.ID]
	status, tone := a.onboardingConnectionStatus(row)
	path := bootstrap.RedactInstallerText(preview.Target)
	if path != "" && status != "待接入" {
		path = "配置文件 " + path
	} else if path != "" {
		path = "将写入 " + path
	}
	if status == "写入中" {
		path = "正在写入接入配置并备份原文件…"
	} else if path == "" && status == "待信任 Hooks" && row.Profile != "" {
		path = "配置目录 " + bootstrap.RedactInstallerText(compactHomePath(row.Profile))
	} else if path == "" && status != "已跳过" {
		path = "正在核对配置文件…"
		if !s.Busy && !s.Loading {
			path = "配置文件尚未确认"
		}
	}
	version := row.HostVersion
	if version == "" {
		version = s.HostVersions[row.Kind]
	}
	locked := a.onboardingConnectionBusy()
	ui.Column(c.Key("onboarding-connect-item-" + row.Kind)).FillWidth().Gap(0).Children(func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key: "onboarding-connect-" + row.Kind, Title: name, Desc: path, DescMono: true, Tooltip: path,
			TitleExtra: func() {
				if version != "" {
					kitui.Text(c, tok, version, kitui.RoleMeta).Font(kitui.FontMono).Label(name + " 版本：" + version).SingleLine()
				}
			},
			Leading: func() {
				logo := agentLogo(tok, c, agentOriginalIcon(row.Kind), 22)
				if status == "已跳过" {
					logo.Opacity(0.5)
				}
			},
			DescExtra: func() {
				note := ""
				switch status {
				case "待新会话":
					note = "已写入配置 · 等你在新会话里发一条消息"
					if row.Status == "environment_receipt_unconfirmed" {
						note = "已检测到会话，等待确认使用了此配置目录"
					}
				case "已生效":
					note = "已收到新会话的接入回执"
				case "已跳过":
					note = "之后可在「设置 › Agent 连接」接入"
				}
				if note != "" || previewPresent {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						if note != "" {
							kitui.Text(c, tok, note, kitui.RoleMeta)
						}
						if previewPresent {
							label := "查看变更预览"
							if a.onboardingConnection.PreviewOpen[row.ID] || a.onboardingConnection.previewNeedsDisplay(preview) {
								label = "收起变更预览"
							}
							kitui.TextButton(c, tok, label, func() { a.toggleOnboardingConnectionPreview(row.ID) }).
								Label(label + "：" + name).Shrink(0)
						}
					})
				}
				if status == "待新会话" && row.Kind == "trae_ide" {
					kitui.Text(c, tok, "在 TRAE CN「设置 → Hooks → 全局」打开开关并确认安全提示。", kitui.RoleMeta).TextColor(tok.Warning)
				}
			},
			Control: func() {
				onboardingConnectionBadge(tok, c, status, tone)
				btn := ui.ButtonBase(c.Key("onboarding-connect-menu-"+row.Kind)).Label(name+" 更多操作").Size(26, 26).
					Radius(radius).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center).Disabled(locked)
				btn.Background(kitui.HoverColor(btn, "onboarding-connect-menu-bg", ui.Transparent, tok.Hover))
				btn.Children(func() { ui.Icon(c, onboardingMoreIcon).FontSize(14).TextColor(tok.Text3) })
				btn.PopoverMenuButton(func(m *ui.Menu) { a.onboardingConnectionMenu(m, row, status) })
			},
		})
		ui.Column(c.Key("onboarding-connect-details-"+row.Kind)).FillWidth().Padding(0, sp4, 0, sp4+36).Gap(sp1).Children(func() {
			a.onboardingConnectionRowDetails(c, row, preview, status)
		})
	})
}

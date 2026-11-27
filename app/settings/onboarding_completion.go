package settings

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) onboardingWelcomeContent(c *ui.Context, primary string, disabled bool, run func()) {
	tok := a.tokens()
	s := &a.environment
	onboardingMascot(c, a.host.BrandMark, !a.windowIsHidden() && !a.windowIsInactive()).Margin(0, 0, sp1, 0)
	ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
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
			{kitui.IconRecall, "按需召回", "新会话里 Agent 会自动找回相关记忆，少走回头路。"},
		} {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(sp4).Gap(sp2).Radius(8).Background(tok.Fill1).Children(func() {
				ui.Box(c).Size(28, 28).Radius(7).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, value.icon).FontSize(16).TextColor(tok.Accent)
				})
				kitui.Text(c, tok, value.title, kitui.RoleBody).FontWeight(600)
				ui.Text(c, value.desc).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.55)
			})
		}
	})
	hosts := s.detectedOnboardingHosts()
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Children(func() {
		onboardingPrimary(tok, c, primary, disabled, run).Height(36).Padding(0, sp5)
		if !s.HostsLoaded || len(hosts) > 0 {
			button := ui.ButtonBase(c).Role(ui.RoleButton).Label("让 Agent 代我安装").
				Height(36).Padding(0, sp5).Gap(8).Radius(radius).Border(1, tok.Accent).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				OnClick(func() { a.onboarding.takeover, a.onboarding.fullPrompt = true, false })
			button.Background(kitui.HoverColor(button, "takeover", tok.AccentSofter, tok.AccentSoft))
			kitui.ButtonPress(button, 0.96)
			button.Children(func() {
				ui.Icon(c, onboardingTerminalIcon).FontSize(14).TextColor(tok.Accent)
				ui.Text(c, "让 Agent 代我安装").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent)
			})
		}
	})
	ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, onboardingShieldIcon).FontSize(13).TextColor(tok.Text3)
			ui.Text(c, "数据与模型只留在本机 · 随时可一键卸载").FontSize(fsFootnote).TextColor(tok.Text3)
		})
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			if !s.HostsLoaded || s.Loading {
				kitui.Spinner(c, tok)
				ui.Text(c, "正在检测本机已有的 Agent 与组件…").FontSize(fsFootnote).TextColor(tok.Text3)
			} else {
				ui.Icon(c, kitui.IconCheck).FontSize(12).TextColor(tok.OK)
				ui.Text(c, fmt.Sprintf("已检测到 %d 个 Agent · 大约 3 分钟完成设置", len(hosts))).FontSize(fsFootnote).TextColor(tok.Text3)
			}
		})
	})
}

func (a *Controller) onboardingTakeoverDialog(c *ui.Context) {
	tok := a.tokens()
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
	kitui.Dialog(c, tok, &o.takeover, kitui.DialogOptions{
		Key: "onboarding-agent-takeover", Title: "让 Agent 代我安装", Width: kitui.DialogSmall, ScrollBody: true,
		ConfirmText: "复制指令", Hint: "完成后回到这里，Memlane 会自动识别",
		OnConfirm: func() {
			c.WriteClipboard(prompt)
			o.takeover, o.watchWorker = false, true
			a.showOnboardingToast("已复制，请在 Agent 的新会话中粘贴")
		},
		Body: func() {
			kitui.Text(c, tok, "复制指令到 "+destination+" 的新会话，它会在终端里替你完成准备。", kitui.RoleMeta)
			for i, item := range []string{
				"先检查本机环境，告诉你还缺什么",
				"每次要改动前先给你看预览，等你确认",
				"下载组件、接入 Agent、启动本地记忆服务",
				"遇到需要你亲自操作的步骤会停下来告诉你",
			} {
				kitui.Text(c, tok, fmt.Sprintf("%d. %s", i+1, item), kitui.RoleBody)
			}
			kitui.Text(c, tok, "指令不会读取密钥或聊天内容，也不会手动改动配置文件。", kitui.RoleMeta)
			kitui.GhostButton(c, tok, "查看完整指令", kitui.IconChev, func() { o.fullPrompt = !o.fullPrompt })
			if o.fullPrompt {
				ui.Scroll(c).FillWidth().MaxHeight(180).Radius(radius).Background(tok.Fill1).Children(func() {
					ui.Text(c, prompt).Font(kitui.FontMono).FontSize(fsCaption).Padding(sp3).TextColor(tok.Text1)
				})
			}
		},
	})
}

func (a *Controller) onboardingDoneContent(c *ui.Context, primary string, disabled bool, run func()) {
	tok := a.tokens()
	if a.onboarding.doneAt.IsZero() {
		a.onboarding.doneAt = c.Now()
	}
	onboardingCompletionMark(tok, c, a.onboarding.doneAt)
	ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
		setupHeading(tok, c, "Memlane 已就绪")
		setupDescription(tok, c, "本地记忆服务正在运行。关闭窗口后它会在菜单栏继续工作。")
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
	kitui.Group(c, tok, "", "", nil, func() {
		kitui.Row(c, tok, kitui.RowOptions{Title: "本地记忆服务", Desc: "记忆引擎与接入网关 · 仅本机访问",
			Leading: func() { kitui.StatusDot(c, tok, "ok") }, Value: "运行中", ValueColor: tok.OK})
		kitui.Row(c, tok, kitui.RowOptions{Title: "Agent 接入", Desc: connectionSummary, Leading: func() { kitui.StatusDot(c, tok, connectionDot) },
			Control: func() {
				ui.Row(c).Gap(4).Children(func() {
					for _, row := range rows {
						icon := ui.Image(c, agentOriginalIcon(row.Kind)).Size(18, 18).Fit(ui.Contain).Label(connectionName(row.Kind))
						label, _ := a.onboardingConnectionStatus(row)
						if label != "已生效" && label != "待新会话" {
							icon.Opacity(0.35)
						}
					}
				})
			}})
		kitui.Row(c, tok, kitui.RowOptions{Title: "记忆提炼", Desc: a.onboarding.method, Leading: func() { kitui.StatusDot(c, tok, "ok") }})
	})
	if pending > 0 {
		ui.Row(c).FillWidth().Padding(sp3, sp4).Gap(12).AlignItems(ui.Start).Radius(radius).Background(tok.AccentSofter).Children(func() {
			ui.Icon(c, onboardingNextIcon).FontSize(15).TextColor(tok.Accent).Shrink(0)
			ui.Text(c, "下一步：重启已接入的 Agent，在项目里新建会话、正常提问。第一条记忆通常会在几分钟内出现在总览里。").
				Grow(1).MinWidth(0).FontSize(fsFootnote).LineHeight(1.6).TextColor(tok.Text2)
		})
	}
	onboardingPrimary(tok, c, primary, disabled, run).Height(36).Padding(0, sp5).Margin(sp4, 0, 0, 0)
}

package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// This presentation uses discovery and runtime receipts. It never installs an
// Agent or advances preparation as a consequence of rendering a page.
func (a *App) firstLaunchView(c *ui.Context) {
	s := &a.firstLaunch
	ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
		if a.firstLaunchStep() == "environment" && s.State.Stage == "discovering" {
			a.firstLaunchDetectionView(c)
			return
		}
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.firstLaunchSidebar(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 28).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
					ui.Text(c, "首次设置").FontSize(design.Body)
					ui.Spacer(c)
					ui.Text(c, a.firstLaunchStatusLabel()).FontSize(design.Small).TextColor(design.Muted)
				})
				if a.firstLaunchStep() == "processing" {
					a.processingSettings(c)
				} else {
					ui.Scroll(c).Key("first-launch-content").Grow(1).MinHeight(0).Padding(28, 36).Gap(20).Children(func() {
						if a.firstLaunchStep() == "agents" {
							a.firstLaunchAgents(c)
						} else {
							a.environmentContent(c, true)
						}
						if s.Error != "" {
							firstLaunchNotice(c, "状态保存", s.Error)
						}
					})
					a.firstLaunchFooter(c)
				}
			})
			a.firstLaunchSteps(c)
		})
	})
}

func firstLaunchStageLabel(stage string) string {
	labels := map[string]string{"results": "检查完成", "confirmation": "等待确认", "preparing": "准备中", "verifying": "验证中", "ready": "本机环境就绪", "failed": "尚未完成", "cancelling": "正在取消", "cancelled": "已取消", "interrupted": "准备已中断"}
	return labels[stage]
}

func (a *App) firstLaunchStatusLabel() string {
	switch a.firstLaunchStep() {
	case "agents":
		return "连接 Agent"
	case "processing":
		return a.processingToolbarStatus()
	default:
		return firstLaunchStageLabel(a.firstLaunch.State.Stage)
	}
}

func (a *App) firstLaunchSidebar(c *ui.Context) {
	s := &a.firstLaunch
	ui.Column(c).Width(196).Shrink(0).Background(design.SidebarFill).Padding(82, 28, 24, 28).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons["brand"]).FontSize(24).TextColor(design.Ink)
			ui.Text(c, "Memlane").FontSize(18).Bold()
		})
		ui.Text(c, "让记忆\n融入日常工作").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.8).Padding(12, 0, 0, 0)
		ui.Spacer(c)
		ui.Checkbox(c.Key("first-launch-reduced"), &s.State.ReducedMotion, "减少动态效果").FontSize(design.Small).OnChange(a.persistFirstLaunch)
	})
}

func (a *App) firstLaunchSteps(c *ui.Context) {
	active := 0
	switch a.firstLaunchStep() {
	case "agents":
		active = 1
	case "processing":
		active = 2
	}
	ui.Column(c).Width(154).Shrink(0).Background(ui.Hex("#f8fafc")).BorderWidth(0, 0, 0, 1).BorderColor(design.Border).Children(func() {
		ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "设置进度").FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Column(c).Padding(40, 14, 0, 14).Gap(12).Children(func() {
			for i, label := range []string{"准备环境", "连接 Agent", "配置记忆处理"} {
				fill, color := ui.Transparent, design.Muted
				if i == active {
					fill, color = design.Selected, design.Ink
				}
				ui.Row(c).Key(fmt.Sprintf("first-launch-step-%d", i)).Height(42).Padding(0, 10).Gap(10).Radius(6).Background(fill).AlignItems(ui.Center).Children(func() {
					mark := fmt.Sprint(i + 1)
					if i < active {
						mark = "✓"
					}
					ui.Column(c).Size(20, 20).Justify(ui.Center).AlignItems(ui.Center).Border(1, color).Radius(10).Children(func() { ui.Text(c, mark).FontSize(11).TextColor(color) })
					ui.Text(c, label).FontSize(design.Small).TextColor(color)
				})
			}
		})
	})
}

func firstLaunchHeading(c *ui.Context, title, lead string) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(22).Bold()
		ui.Text(c, lead).FontSize(design.Body).TextColor(design.Muted).LineHeight(1.6)
	})
}
func firstLaunchSection(c *ui.Context, title, hint string, content func()) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(13).Bold()
			ui.Spacer(c)
			ui.Text(c, hint).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Column(c).Border(1, design.Border).Radius(6).Children(content)
	})
}
func firstLaunchRow(c *ui.Context, key, title, subtitle, state string, icon *ui.SVG) {
	ui.Row(c).Key(key).MinHeight(54).Padding(9, 12).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Column(c).Size(28, 28).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Border(1, design.Border).Radius(5).Background(ui.Hex("#fafbfc")).Children(func() { ui.Icon(c, icon).FontSize(16).TextColor(design.Ink) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(design.Body).Bold().SingleLine()
			ui.Text(c, subtitle).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.3)
		})
		ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted).SingleLine().Shrink(0)
	})
}
func firstLaunchNotice(c *ui.Context, title, message string) {
	ui.Column(c).Gap(5).Padding(12, 14).Background(ui.Hex("#f8fafc")).Radius(5).Children(func() {
		ui.Text(c, title).FontSize(design.Body).Bold()
		ui.Text(c, message).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6).Selectable()
	})
}

func (a *App) firstLaunchItem(name string) EnvironmentItem {
	for _, item := range a.firstLaunch.State.Items {
		if item.Name == name {
			return item
		}
	}
	return EnvironmentItem{Name: name, State: "not_verified", Evidence: "本次检测未取得此项结果"}
}
func (a *App) firstLaunchAgents(c *ui.Context) {
	ui.Row(c).Gap(14).Children(func() {
		ui.Text(c, "连接 Agent").FontSize(design.Heading).Bold()
		ui.Spacer(c)
		quietButton(c, "全选可连接").Disabled(a.agents.Batch.frozen()).OnClick(a.selectAllAgentConnections)
		quietButton(c, "重新检测").Disabled(a.agents.Busy).OnClick(func() { a.refreshAgents(true) })
	})
	ui.Divider(c)
	a.agentConnectionCards(c, true)
}

func (a *App) firstLaunchFooter(c *ui.Context) {
	if a.firstLaunchStep() == "environment" {
		a.environmentFooter(c, true)
		return
	}
	ui.Row(c).Key("first-launch-footer").Wrap().MinHeight(64).Shrink(0).Padding(14, 28).Gap(14).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		a.agentConnectionActionButtons(c)
		ui.Spacer(c)
		quietButton(c, "稍后设置").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
		firstLaunchPrimary(c, "继续配置记忆处理").Key("first-launch-processing").OnClick(a.advanceFirstLaunchToProcessing)
	})
}

func firstLaunchPrimary(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(34).Padding(7, 16).FontSize(design.Body).Background(design.Ink).TextColor(ui.Hex("#ffffff")).BorderWidth(0).Radius(5).Shrink(0)
}

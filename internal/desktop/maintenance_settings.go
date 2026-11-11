package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) ConfigureWindowActions(quit, stopAndQuit func(), hide ...func()) {
	a.quitApp, a.stopAndQuitApp = quit, stopAndQuit
	if len(hide) > 0 {
		a.hideWindow = hide[0]
	}
}

func (a *App) maintenanceSettings(c *ui.Context) {
	if a.maintenanceSection == "" {
		a.maintenanceSection = "components"
	}
	width, _ := c.Size()
	listWidth := float32(272)
	if width < 1040 {
		listWidth = 212
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(listWidth).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "服务与数据").FontSize(design.Small).TextColor(design.Muted).Padding(22, 20)
			ui.Scroll(c.Key("maintenance-list")).Grow(1).Padding(0, 12, 20, 12).Gap(6).Children(func() {
				for _, item := range []struct{ key, label string }{{"service", "本地服务"}, {"components", "环境与组件"}, {"updates", "客户端更新"}, {"backups", "备份与恢复"}, {"index", "检索索引"}, {"diagnostics", "关于与诊断"}, {"window", "窗口与退出"}} {
					subtitle := ""
					if item.key == "service" {
						subtitle = a.maintenanceServiceLabel()
					}
					if item.key == "components" {
						subtitle = "状态未读取"
						if !a.environment.CheckedAt.IsZero() && a.environment.Error == "" {
							subtitle = "组件需核对"
							ready := true
							for _, card := range a.environmentCards(false) {
								state := environmentCardState(card)
								ready = ready && (state == "已校验" || state == "随 App 提供")
							}
							if ready {
								subtitle = "已有组件可复用"
							}
						}
					}
					button := ui.Button(c.Key("maintenance-"+item.key), "").Label(item.label).Height(62).Padding(12).Justify(ui.Start).AlignItems(ui.Start).BorderWidth(0).Background(ui.Transparent).Children(func() {
						ui.Column(c).Gap(5).AlignItems(ui.Start).Children(func() {
							ui.Text(c, item.label).FontSize(design.Small)
							if subtitle != "" {
								ui.Text(c, subtitle).FontSize(10).TextColor(design.Muted)
							}
						})
					})
					if a.maintenanceSection == item.key {
						button.Background(design.ListSelected)
					}
					key := item.key
					button.OnClick(func() {
						a.maintenanceSection = key
						a.maintenanceStopConfirm = false
						if key == "backups" {
							a.refreshBackups()
						}
						if key == "index" {
							a.refreshIndexRebuild()
						}
					})
				}
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if a.maintenanceSection == "components" {
				a.environmentSettings(c)
				return
			}
			if a.maintenanceSection == "index" {
				a.indexRecoveryPage(c)
				return
			}
			if a.maintenanceSection == "backups" {
				if a.maintenanceRestoreSummary(c) {
					return
				}
				a.backupPage(c)
				return
			}
			if a.maintenanceSection == "diagnostics" {
				a.maintenanceDiagnosticsPage(c)
				return
			}
			if a.maintenanceSection == "updates" {
				a.maintenanceUpdatesPage(c)
				return
			}
			if a.maintenanceSection == "window" {
				a.maintenanceWindowPage(c)
				return
			}
			ui.Scroll(c.Key("maintenance-detail-" + a.maintenanceSection)).Grow(1).Padding(28).Gap(20).Children(func() {
				switch a.maintenanceSection {
				case "service":
					a.maintenanceService(c)
				}
			})
			if a.maintenanceSection == "service" {
				a.maintenanceServiceFooter(c)
			}
		})
	})
}

func (a *App) maintenanceServiceLabel() string {
	if a.runtimeStatus.Phase == "stopped" {
		return "已主动停止"
	}
	if a.maintenanceNext.Recovery == "exhausted" && a.serviceState == "failed" {
		return "服务恢复失败"
	}
	if a.maintenanceNext.Recovery == "recovering" && (a.serviceState == "preparing" || a.serviceState == "recovering") {
		return "服务正在恢复"
	}
	if a.environmentBusy() {
		if strings.Contains(a.runtimeStatus.Message, "停止") {
			return "正在停止"
		}
		return "正在启动"
	}
	if a.runtimeStatus.Phase == "ready" && a.serviceState == "ready" {
		return "正在运行"
	}
	if a.runtimeStatus.Phase == "authorization" {
		return "后台许可需要恢复"
	}
	if a.runtimeStatus.Phase == "failed" || a.serviceState == "failed" {
		return "服务异常"
	}
	return "服务尚未就绪"
}

func (a *App) maintenanceService(c *ui.Context) {
	ui.Text(c, "本机后台服务").FontSize(design.Small).TextColor(design.Muted)
	ui.Text(c, a.maintenanceServiceLabel()).FontSize(design.Heading).Bold()
	ui.Text(c, "窗口与服务分别控制，收起窗口后后台可继续工作。").TextColor(design.Muted)
	ui.Column(c).Key("maintenance-service-state-card").Padding(14).Gap(6).Background(ui.Hex("#f7f9fc")).Border(1, design.Border).Radius(6).Children(func() {
		ui.Text(c, a.maintenanceServiceLabel()).FontSize(design.Small).Bold()
		ui.Text(c, "App 显示当前服务状态；任务与计划按各自授权执行。").FontSize(design.Small).TextColor(design.Muted)
	})
	a.maintenanceRecoveryNotice(c)
	if a.runtimeStatus.Message != "" {
		ui.Text(c, a.runtimeStatus.Message).Selectable()
	}
	if a.maintenanceStopConfirm {
		ui.Text(c, "停止后，记忆读写会暂停").Bold()
		ui.Text(c, "运行中的整理与计划任务将停止，已经保存的知识与建议保留。重新打开窗口仍保持停止，需要时明确启动服务。")
		return
	}
	ui.Divider(c)
	ui.Text(c, "影响范围").Bold()
	availability := "暂时不可用"
	if a.runtimeStatus.Phase == "ready" && a.serviceState == "ready" {
		availability = "当前可用"
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "Agent 记忆读写").FontSize(design.Small).TextColor(design.Muted).Grow(1).MinWidth(0)
		ui.Text(c, availability).FontSize(design.Small)
	})
	plans := "状态未读取"
	if a.runtimeStatus.Phase == "stopped" {
		plans = "服务已停止"
	} else if a.serviceState != "ready" {
		plans = "服务暂不可用"
	} else if a.schedules.Loaded && a.schedules.ReadError == "" && a.schedules.RuntimeError == "" {
		count := 0
		for _, plan := range a.schedules.Plans {
			if plan.Enabled {
				count++
			}
		}
		plans = fmt.Sprintf("已启用 %d 个计划 · 按各自授权执行", count)
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "计划与整理任务").FontSize(design.Small).TextColor(design.Muted).Grow(1).MinWidth(0)
		ui.Text(c, plans).FontSize(design.Small)
	})
	quietButton(c, "查看整理计划").AlignSelf(ui.Start).OnClick(func() { a.navigate("整理计划", "") })
	if a.runtimeStatus.Phase == "stopped" {
		ui.Text(c, "这是用户主动停止的状态，重新打开窗口不会自动启动。").TextColor(design.Muted)
	} else if a.runtimeStatus.Phase != "ready" {
		quietButton(c, "查看诊断").AlignSelf(ui.Start).OnClick(func() { a.maintenanceSection = "diagnostics" })
		quietButton(c, "重新检查环境").AlignSelf(ui.Start).Disabled(a.environmentBusy()).OnClick(func() { a.maintenanceSection = "components"; a.refreshEnvironment() })
	}
}

func (a *App) maintenanceServiceFooter(c *ui.Context) {
	locked := a.backups.RestoreLocked || a.backups.Busy || a.environmentBusy()
	ui.Row(c).Key("maintenance-service-footer").MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "知识、来源和历史保留").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		if a.maintenanceStopConfirm {
			quietButton(c, "取消").OnClick(func() { a.maintenanceStopConfirm = false })
			actionButton(c, "确认停止服务", nil).Disabled(locked || a.stopRuntime == nil).OnClick(func() {
				a.maintenanceStopConfirm = false
				if a.stopRuntime != nil {
					a.stopRuntime()
				}
			})
		} else if a.runtimeStatus.Phase == "authorization" {
			ui.Text(c, "请先恢复后台许可，再重新验证").FontSize(10).TextColor(design.Muted)
		} else if a.serviceState == "failed" && a.maintenanceNext.Recovery != "recovering" {
			actionButton(c, "明确重试服务", nil).Key("maintenance-retry-service").Disabled(locked || a.maintenanceNext.Busy || a.maintenanceNext.Awaiting || a.libraryContext == nil).OnClick(a.retryMaintenanceService)
		} else if a.runtimeStatus.Phase == "stopped" {
			actionButton(c, "明确启动服务", nil).Disabled(locked || a.resumeRuntime == nil).OnClick(func() {
				if a.resumeRuntime != nil {
					a.resumeRuntime()
				}
			})
		} else if a.runtimeStatus.Phase == "ready" {
			actionButton(c, "停止服务", nil).Disabled(locked || a.stopRuntime == nil).OnClick(func() { a.maintenanceStopConfirm = true })
		} else {
			ui.Text(c, "等待服务回执").FontSize(10).TextColor(design.Muted)
		}
	})
}

func (a *App) maintenanceWindow(c *ui.Context) {
	ui.Text(c, "窗口与后台分开控制").FontSize(design.Small).TextColor(design.Muted)
	ui.Text(c, "收起以后，工作可以继续").FontSize(design.Heading).Bold()
	ui.Text(c, "关闭窗口收起到菜单栏；退出时是否保留服务由你设置。")
	previous := a.keepService
	ui.Checkbox(c.Key("maintenance-keep-service"), &a.keepService, "退出 App 后继续运行记忆服务")
	if previous != a.keepService && a.keepChanged != nil {
		a.keepChanged(a.keepService)
	}
	ui.Text(c, "关闭此选项后，退出会停止服务。").TextColor(design.Muted)
	quietButton(c, "收起窗口到菜单栏").Disabled(a.hideWindow == nil || a.backups.Busy).OnClick(func() {
		if a.hideWindow != nil {
			a.hideWindow()
		}
	})
	quietButton(c, "退出 Memlane").Disabled(a.quitApp == nil || a.backups.Busy).OnClick(func() {
		if a.quitApp != nil {
			a.quitApp()
		}
	})
	quietButton(c, "停止服务并退出").Disabled(a.stopAndQuitApp == nil || a.backups.Busy).OnClick(func() {
		if a.stopAndQuitApp != nil {
			a.stopAndQuitApp()
		}
	})
	ui.Divider(c)
	ui.Text(c, "界面缩放").Bold()
	ui.Row(c).Gap(10).Children(func() {
		quietButton(c, "缩小").Disabled(a.zoom <= .8).OnClick(func() { a.zoom = max(float32(.8), a.zoom-.1) })
		ui.Text(c, fmt.Sprintf("%.0f%%", a.zoom*100))
		quietButton(c, "放大").Disabled(a.zoom >= 1.4).OnClick(func() { a.zoom = min(float32(1.4), a.zoom+.1) })
	})
}

func (a *App) maintenanceWindowPage(c *ui.Context) {
	ui.Text(c, "窗口与退出").FontSize(design.Small).TextColor(design.Muted).Padding(20, 28).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border)
	ui.Scroll(c.Key("maintenance-window-body")).Grow(1).MinHeight(0).Padding(28).Gap(20).Children(func() { a.maintenanceWindow(c) })
	ui.Text(c, "收起后可从菜单栏重新打开当前窗口").FontSize(10).TextColor(design.Muted).Padding(22, 24).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border)
}

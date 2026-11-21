package simpledesktop

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

var v4IconWindow = v4IconSVG(`<rect x="3" y="4" width="14" height="12" rx="2"/><path d="M3 8h14"/>`)

// 32x32 (@2x Retina) slim tilted Fusion H1 sea otter silhouettes (H1-C) with corner status dots.
//
//go:embed brand_icons/tray_healthy.png
var trayIconHealthy []byte

//go:embed brand_icons/tray_warn_light.png
var trayIconWarnLight []byte

//go:embed brand_icons/tray_warn_dark.png
var trayIconWarnDark []byte

//go:embed brand_icons/tray_danger_light.png
var trayIconDangerLight []byte

//go:embed brand_icons/tray_danger_dark.png
var trayIconDangerDark []byte

// SetTrayDark records whether the macOS menu bar hosting the tray icon is dark.
func (a *App) SetTrayDark(dark bool) {
	a.desktopPrefs.TrayDark = dark
}

func (a *App) trayStatusLevel() string {
	summary := a.trayCachedSummary()
	hookErrors := 0
	if a.monitor.Snapshot != nil && a.monitor.Snapshot.Summary != nil {
		hookErrors = a.monitor.Snapshot.Summary.HookErrorsCount
	}
	switch {
	case a.monitor.State == "degraded" || a.monitor.State == "exited" ||
		(a.service != "" && a.service != "已连接" && a.service != "尚未检查") ||
		summary.HasErrorAlert:
		return "danger"
	case a.monitor.State == "warning" || hookErrors > 0 || len(summary.Alerts) > 0:
		return "warn"
	default:
		return "ok"
	}
}

// TrayIconState returns the cached 32x32 PNG bytes, template flag, and tooltip
// for the macOS menu bar icon. Zero PNG encoding happens after first call.
func (a *App) TrayIconState() (pngBytes []byte, isTemplate bool, tooltip string) {
	switch a.trayStatusLevel() {
	case "danger":
		if a.desktopPrefs.TrayDark {
			return trayIconDangerDark, false, "Memlane · 服务或项目存在异常"
		}
		return trayIconDangerLight, false, "Memlane · 服务或项目存在异常"
	case "warn":
		if a.desktopPrefs.TrayDark {
			return trayIconWarnDark, false, "Memlane · 有待处理提醒"
		}
		return trayIconWarnLight, false, "Memlane · 有待处理提醒"
	default:
		return trayIconHealthy, true, "Memlane"
	}
}

func (a *App) trayOverallServiceStatus() (dotState, label string) {
	hookErrors := 0
	if a.monitor.Snapshot != nil && a.monitor.Snapshot.Summary != nil {
		hookErrors = a.monitor.Snapshot.Summary.HookErrorsCount
	}
	switch {
	case a.monitor.State == "exited":
		return "fail", "服务 · 已停止"
	case a.monitor.State == "degraded" || (a.service != "" && a.service != "已连接" && a.service != "尚未检查"):
		return "fail", "服务 · 异常"
	case a.monitor.State == "warning" || hookErrors > 0:
		return "warn", "服务 · 需关注"
	case a.monitor.State == "ready" || a.service == "已连接":
		return "ok", "服务 · 已连接"
	default:
		return "neutral", "服务 · 待检查"
	}
}

func (a *App) trayControlStatus() (dotState, metric string, col ui.Color) {
	m := &a.monitor
	if a.Config.ProjectControl == nil && !m.ControlChecked && m.Snapshot == nil {
		return "neutral", "未配置", tok.Text3
	}
	if m.ControlChecked && !m.ControlReachable {
		return "fail", "异常", tok.Danger
	}
	if m.ControlReachable || m.Snapshot != nil {
		if m.HasControlLatency {
			return "ok", "已连接 · " + formatDurationCompact(m.ControlLatencyMs), tok.Text2
		}
		return "ok", "已连接", tok.Text2
	}
	return "neutral", "待检查", tok.Text3
}

func (a *App) trayChannelStatus() (dotState, metric string, col ui.Color) {
	if a.Config.CompletionURL == "" && (a.completion == "" || a.completion == "未配置") {
		return "neutral", "未配置", tok.Text3
	}
	if strings.HasPrefix(a.completion, "端点已连接") {
		if a.monitor.HasCompletionLatency {
			return "ok", "已连接 · " + formatDurationCompact(a.monitor.CompletionLatencyMs), tok.Text2
		}
		return "ok", "已连接", tok.Text2
	}
	if a.completion == "" || a.completion == "尚未检查" {
		return "neutral", "待检查", tok.Text3
	}
	return "warn", "待检查", tok.Warning
}

func (a *App) trayTodayMemoryCount(now time.Time) int {
	v := &a.overviewState
	if !v.Loaded && len(v.Snapshot.Knowledge) == 0 {
		return 0
	}
	day := now.Format("2006-01-02")
	tc := &a.trayCache
	if tc.todayValid && tc.todayGen == v.generation && tc.todayLoaded == v.Loaded &&
		tc.todayKnowledgeCount == len(v.Snapshot.Knowledge) && tc.todayDay == day {
		return tc.todayCount
	}
	buckets := a.overviewCachedBuckets(now)
	count := buckets[day]
	tc.todayGen = v.generation
	tc.todayLoaded = v.Loaded
	tc.todayKnowledgeCount = len(v.Snapshot.Knowledge)
	tc.todayDay = day
	tc.todayCount = count
	tc.todayValid = true
	tc.todayComputations++
	return count
}

func (a *App) openSettingsFromTray(section string) {
	if a.HideTrayFlyoutFn != nil {
		a.HideTrayFlyoutFn()
	}
	if a.ShowMainWindowFn != nil {
		a.ShowMainWindowFn()
	}
	a.settingsOrigin = nil
	a.access.OpenProject = ""
	a.access.SettingsSection = section
	a.navigate("设置", 0)
}

// TrayFlyoutView renders the 300px custom V4 menu bar flyout.
func (a *App) TrayFlyoutView(c *ui.Context) {
	applyAppearance(a.effectiveDark(c.Theme().Dark))
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Background(tok.BgElevated).Children(func() {
		ui.Column(c.Key("tray-flyout-root")).Fill().Background(tok.BgElevated).Border(1, tok.Border).Radius(10).Clip().Children(func() {
			a.trayFlyoutHeader(c)
			ui.Scroll(c.Key("tray-flyout-body")).Grow(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(0).Children(func() {
					a.trayFlyoutComponentsSection(c)
					a.trayFlyoutAlertsSection(c)
					a.trayFlyoutQuickSettingsSection(c)
				})
			})
			a.trayFlyoutFooter(c)
		})
	})
}

func (a *App) trayFlyoutHeader(c *ui.Context) {
	dotState, statusLabel := a.trayOverallServiceStatus()
	ui.Row(c.Key("tray-head")).FillWidth().Shrink(0).Padding(10, sp3).Gap(sp2).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		ui.Image(c, tok.BrandMark).Size(18, 18).Fit(ui.Contain).Shrink(0)
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(6).Children(func() {
			v4StatusDot(c, dotState)
			ui.Text(c, statusLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(550).TextColor(tok.Text1).SingleLine()
		})
		trayIconButton(c, "tray-open-window", "打开主窗口", v4IconWindow, func() {
			if a.HideTrayFlyoutFn != nil {
				a.HideTrayFlyoutFn()
			}
			if a.ShowMainWindowFn != nil {
				a.ShowMainWindowFn()
			}
		})
		trayIconButton(c, "tray-open-search", "⌘K 搜索", v4IconSearch, func() {
			if a.HideTrayFlyoutFn != nil {
				a.HideTrayFlyoutFn()
			}
			if a.OpenGlobalSearchFn != nil {
				a.OpenGlobalSearchFn()
			} else {
				if a.ShowMainWindowFn != nil {
					a.ShowMainWindowFn()
				}
				a.OpenSearch()
			}
		})
	})
}

func trayIconButton(c *ui.Context, key, label string, icon *ui.SVG, fn func()) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).Tooltip(label).
		Cursor(ui.CursorPointer).Size(24, 24).Radius(radiusSmall).BorderWidth(0).
		AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	btn.Background(homeHoverColor(btn, key+"-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(btn, 0.92)
	icoCol := tok.Text2
	if btn.Hovered() {
		icoCol = tok.Text1
	}
	btn.Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(icoCol)
	}).OnClick(fn)
	return btn
}

func (a *App) trayFlyoutComponentsSection(c *ui.Context) {
	cogDot, cogStatus, cogCol := a.serviceCogneeStatus()
	cogMetric := cogStatus
	if a.monitor.HasLatency && cogDot == "ok" {
		cogMetric = fmt.Sprintf("%s · %s", cogStatus, formatDurationCompact(a.monitor.LastLatencyMs))
	}
	if cogDot == "ok" {
		cogCol = tok.Text2
	}
	ctrlDot, ctrlMetric, ctrlCol := a.trayControlStatus()
	chanDot, chanMetric, chanCol := a.trayChannelStatus()
	todayCount := a.trayTodayMemoryCount(time.Now())

	ui.Column(c.Key("tray-components")).FillWidth().Padding(sp2, sp3).Gap(5).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		trayCompRow(c, "记忆引擎（Cognee）", cogDot, cogMetric, cogCol)
		trayCompRow(c, "接入网关", ctrlDot, ctrlMetric, ctrlCol)
		trayCompRow(c, "Agent 模型代理", chanDot, chanMetric, chanCol)
		ui.Row(c).FillWidth().Padding(4, 0, 0, 0).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
			ui.Text(c, "今日新增记忆").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%d 条", todayCount)).Font("ui-monospace").FontSize(fsCaption).FontWeight(500).FontFeatures("tnum").TextColor(tok.Text1).SingleLine()
		})
	})
}

func trayCompRow(c *ui.Context, name, dotState, metric string, metricColor ui.Color) {
	ui.Row(c).FillWidth().MinHeight(20).AlignItems(ui.Center).Gap(6).Children(func() {
		v4StatusDot(c, dotState)
		ui.Text(c, name).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
		ui.Spacer(c)
		ui.Text(c, metric).Font("ui-monospace").FontSize(fsCaption).FontWeight(400).FontFeatures("tnum").TextColor(metricColor).SingleLine()
	})
}

func (a *App) trayFlyoutAlertsSection(c *ui.Context) {
	summary := a.trayCachedSummary()
	alerts := summary.Alerts
	progress := summary.Progress
	ui.Column(c.Key("tray-alerts")).FillWidth().Padding(sp2, sp3).Gap(4).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		if len(alerts) == 0 {
			ui.Row(c).FillWidth().MinHeight(24).AlignItems(ui.Center).Gap(6).Children(func() {
				v4StatusDot(c, "ok")
				ui.Text(c, "所有项目消息状态正常").Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text3).SingleLine()
			})
		} else {
			ui.Text(c, "项目待处理提醒").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
			for _, alert := range alerts {
				al := alert
				total := al.ActionCount + al.ErrorCount
				dotState := "warn"
				badgeTone := "warn"
				if al.ErrorCount > 0 {
					dotState = "fail"
					badgeTone = "danger"
				}
				row := ui.ButtonBase(c.Key("tray-alert-"+al.ProjectID)).Role(ui.RoleButton).
					Label(fmt.Sprintf("打开项目提醒：%s", al.ProjectName)).
					Cursor(ui.CursorPointer).FillWidth().MinHeight(28).Padding(4, 6).Gap(6).
					Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start)
				row.Background(homeHoverColor(row, "tray-alert-bg", ui.Transparent, tok.Hover))
				v4ButtonPress(row, 0.99)
				row.Children(func() {
					v4StatusDot(c, dotState).Margin(1, 0, 0, 0)
					ui.Text(c, al.ProjectName).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).SingleLine().Padding(0, 0, 1, 0).Grow(1).MinWidth(0)
					v4Badge(c, fmt.Sprintf("%d 项待处理", total), badgeTone)
					ui.Icon(c, v4IconChev).FontSize(12).TextColor(tok.Text4).Shrink(0)
				}).OnClick(func() {
					if a.HideTrayFlyoutFn != nil {
						a.HideTrayFlyoutFn()
					}
					if a.ShowMainWindowFn != nil {
						a.ShowMainWindowFn()
					}
					if al.ProjectIndex >= 0 {
						a.openProjectQueueStatus(al.ProjectIndex)
					}
				})
			}
		}
		if len(progress) > 0 {
			ui.Row(c).FillWidth().Padding(4, 0, 0, 0).Children(func() {
				ui.Text(c, "进行中与待提炼").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
			})
			for _, item := range progress {
				prog := item
				row := ui.ButtonBase(c.Key("tray-progress-"+prog.ProjectID)).Role(ui.RoleButton).
					Label(fmt.Sprintf("查看项目进度：%s", prog.ProjectName)).
					Cursor(ui.CursorPointer).FillWidth().MinHeight(28).Padding(4, 6).Gap(6).
					Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start)
				row.Background(homeHoverColor(row, "tray-progress-bg", ui.Transparent, tok.Hover))
				v4ButtonPress(row, 0.99)
				row.Children(func() {
					v4StatusDot(c, "run").Margin(1, 0, 0, 0)
					ui.Text(c, prog.ProjectName).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).SingleLine().Padding(0, 0, 1, 0).Grow(1).MinWidth(0)
					v4Badge(c, fmt.Sprintf("%d 项进行中", prog.Count), "accent")
					ui.Icon(c, v4IconChev).FontSize(12).TextColor(tok.Text4).Shrink(0)
				}).OnClick(func() {
					if a.HideTrayFlyoutFn != nil {
						a.HideTrayFlyoutFn()
					}
					if a.ShowMainWindowFn != nil {
						a.ShowMainWindowFn()
					}
					if prog.ProjectIndex >= 0 {
						a.openProjectQueueStatus(prog.ProjectIndex)
					}
				})
			}
		}
	})
}

func (a *App) trayFlyoutQuickSettingsSection(c *ui.Context) {
	mode := a.AppearancePreference()
	selected := appearanceModeIndex(mode)
	keepAlive := a.KeepAliveOnQuit()
	openAtLogin := a.OpenAtLogin()

	ui.Column(c.Key("tray-settings")).FillWidth().Padding(sp2, sp3).Gap(6).Children(func() {
		ui.Row(c).FillWidth().MinHeight(28).AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Text(c, "外观").Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
			ui.Spacer(c)
			v4Segmented(c, "tray-appearance", "外观模式", &selected, []string{"跟随系统", "浅色", "深色"}, func() {
				a.setAppearancePreference(appearanceModes[selected])
				if a.Update != nil {
					a.Update(nil)
				}
			})
		})
		ui.Row(c).FillWidth().MinHeight(26).AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Text(c, "退出时保持后台运行").Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
			ui.Spacer(c)
			v4Switch(c, "tray-keep-alive", &keepAlive, "", func() {
				a.SetKeepAliveOnQuit(keepAlive)
				if a.Update != nil {
					a.Update(nil)
				}
			}).Label("退出时保持后台运行开关")
		})
		ui.Row(c).FillWidth().MinHeight(26).AlignItems(ui.Center).Gap(sp2).Children(func() {
			ui.Text(c, "登录时静默启动").Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
			ui.Spacer(c)
			v4Switch(c, "tray-open-at-login", &openAtLogin, "", func() {
				a.ToggleOpenAtLogin(openAtLogin)
				if a.Update != nil {
					a.Update(nil)
				}
			}).Label("登录时静默启动开关")
		})
		if a.desktopPrefs.LoginError != "" {
			ui.Text(c, a.desktopPrefs.LoginError).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Warning).LineHeight(1.4)
		}
		ui.Column(c).FillWidth().Padding(4, 0, 0, 0).Gap(2).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
			trayNavLink(c, "tray-go-processing", "记忆提炼设置", func() {
				a.openSettingsFromTray("记忆提炼")
			})
			trayNavLink(c, "tray-go-service", "服务与组件详情", func() {
				a.openSettingsFromTray("服务")
			})
		})
	})
}

func trayNavLink(c *ui.Context, key, label string, fn func()) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).FillWidth().MinHeight(26).Padding(4, 4).Gap(sp2).
		Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start)
	btn.Background(homeHoverColor(btn, key+"-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(btn, 0.99)
	txtCol := tok.Text2
	chevCol := tok.Text4
	if btn.Hovered() {
		txtCol = tok.Text1
		chevCol = tok.Text2
	}
	btn.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(txtCol).SingleLine().Grow(1).MinWidth(0)
		ui.Icon(c, v4IconChev).FontSize(12).TextColor(chevCol).Shrink(0)
	}).OnClick(fn)
	return btn
}

func (a *App) trayFlyoutFooter(c *ui.Context) {
	keepAlive := a.KeepAliveOnQuit()
	primaryLabel := "退出并停止服务"
	if keepAlive {
		primaryLabel = "退出界面（后台保活）"
	}
	ui.Column(c.Key("tray-footer")).FillWidth().Shrink(0).Padding(sp2, sp3).Gap(6).
		BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		primaryBtn := ui.ButtonBase(c.Key("tray-quit-primary")).Role(ui.RoleButton).Label(primaryLabel).
			Cursor(ui.CursorPointer).FillWidth().Height(28).Padding(0, sp3).
			Radius(radius).Border(1, tok.BorderStrong).AlignItems(ui.Center).Justify(ui.Center)
		primaryBtn.Background(homeHoverColor(primaryBtn, "tray-quit-bg", tok.Fill1, tok.Hover))
		v4ButtonPress(primaryBtn, 0.98)
		primaryBtn.Children(func() {
			ui.Text(c, primaryLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).SingleLine()
		}).OnClick(func() {
			a.requestQuit(!keepAlive)
		})

		if keepAlive {
			hardBtn := ui.ButtonBase(c.Key("tray-quit-hard")).Role(ui.RoleButton).Label("彻底停止并退出").
				Cursor(ui.CursorPointer).FillWidth().Height(24).Padding(0, sp2).
				Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center)
			hardBtn.Background(homeHoverColor(hardBtn, "tray-quit-hard-bg", ui.Transparent, tok.Danger.Alpha(0.10)))
			v4ButtonPress(hardBtn, 0.98)
			hardBtn.Children(func() {
				ui.Text(c, "彻底停止并退出").Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Danger).SingleLine()
			}).OnClick(a.openForceQuitConfirm)
		}
	})
}

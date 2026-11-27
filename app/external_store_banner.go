package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) isExternalManagedStore() bool {
	return !a.settingsController.InitialSetup() && a.configError == "" && a.Config.FirstInstall == nil &&
		a.Config.ConfigFile != "" && settings.HasServiceMonitorEndpoint(a.Config)
}

func (a *App) isExternalManagedStoreStopped() bool {
	if !a.isExternalManagedStore() || a.monitor.EngineStatus == "已连接" ||
		a.monitor.State == "ready" || a.monitor.State == "starting" || a.monitor.State == "unknown" ||
		a.monitor.ControlReachable {
		return false
	}
	if a.monitor.ControlErrorKind == "auth" || a.monitor.ControlErrorKind == "response" ||
		a.monitor.HealthErrorKind == "auth" || a.monitor.HealthErrorKind == "response" {
		return false
	}
	if a.monitor.Snapshot.GatewayHealthy() && !a.monitor.ControlChecked {
		return false
	}
	return a.monitor.ConsecutiveFailures > 0 || a.monitor.State == "degraded" || a.monitor.State == "exited" ||
		(a.monitor.ControlChecked && !a.monitor.ControlReachable) ||
		(a.monitor.EngineStatus != "" && a.monitor.EngineStatus != "尚未检查" && a.monitor.EngineStatus != "启动中" && a.monitor.EngineStatus != "状态未知")
}

func (a *App) recheckExternalStoreConnection() {
	if !a.isExternalManagedStore() {
		return
	}
	a.externalStorePollPaused = false
	a.settingsController.EnableServicePolling(true)
	a.externalStoreNotice = ""
	a.health()
	a.RefreshProjectDirectory()
	switch a.page {
	case "总览":
		a.loadOverview()
	case "设置":
		a.RefreshIntegrations()
	}
}

func (a *App) openServiceSettings() {
	a.settingsOrigin = nil
	a.access.OpenProject = ""
	a.access.SettingsSection = "服务"
	a.navigate("设置", 0)
}

func externalStoreCommands(config Config) (start, recover string) {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	target := " --config " + quote(config.ConfigFile)
	return "sh scripts/go.sh run ./cmd/memlane dev start --no-ui" + target,
		"sh scripts/go.sh run ./cmd/memlane dev recover" + target
}

func (a *App) externalStoreStoppedBanner(c *ui.Context) {
	if !a.isExternalManagedStoreStopped() {
		return
	}
	start, recover := externalStoreCommands(a.Config)
	ui.Column(c.Key("external-store-stopped")).FillWidth().Shrink(0).Margin(sp3, sp4).
		Padding(sp3, sp4).Gap(sp2).Radius(radius).Background(tok.Fill1).BorderWidth(1).BorderColor(tok.Border).Children(func() {
		ui.Text(c, "外部托管记忆库服务尚未运行").FontSize(fsBody).FontWeight(600).TextColor(tok.Warning)
		ui.Text(c, "当前配置指向已有外部或开发态记忆库，缺少 first_install 托管标记。为保护现有库归属与数据安全，打包版应用不会自动启动或接管该后台服务。").
			FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.5)
		ui.Text(c, "在 Memlane 项目目录执行启动命令。若重启后残留旧归属记录，先执行恢复命令。").
			FontSize(fsFootnote).TextColor(tok.Text2)
		for _, command := range []string{start, recover} {
			ui.Text(c, command).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text1)
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			v4SmallPrimaryButton(c, "重新检查连接", a.recheckExternalStoreConnection).Disabled(a.monitor.Polling)
			v4SmallButton(c, "复制启动命令", v4IconCopy, func() {
				c.WriteClipboard(start)
				a.externalStoreNotice = "已复制启动命令"
			})
			v4SmallButton(c, "复制恢复命令", v4IconCopy, func() {
				c.WriteClipboard(recover)
				a.externalStoreNotice = "已复制恢复命令"
			})
			if a.externalStoreNotice != "" {
				ui.Text(c, a.externalStoreNotice).FontSize(fsCaption).TextColor(tok.Text3)
			}
		})
	})
}

func (a *App) serviceDualStateBanner(c *ui.Context) {
	if a.isExternalManagedStoreStopped() {
		return
	}
	summary := a.settingsController.DualStateSummary()
	if !summary.GatewayHealthy {
		return
	}
	if summary.EngineStarting {
		ui.Column(c.Key("service-engine-starting")).FillWidth().Shrink(0).Margin(sp3, sp4, 0, sp4).
			Padding(sp3, sp4).Gap(sp2).Radius(radius).Background(tok.Fill1).BorderWidth(1).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				v4StatusDot(c, "run")
				ui.Text(c, "记忆引擎正在启动，已可正常浏览历史记忆并继续采集对话。").
					FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).Grow(1).MinWidth(0)
				v4SmallButton(c, "查看服务详情", nil, a.openServiceSettings)
			})
			if summary.BacklogCount > 0 {
				ui.Text(c, fmt.Sprintf("当前有 %d 条记录等待引擎就绪后自动提炼。", summary.BacklogCount)).
					FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.45)
			}
		})
		return
	}
	if !summary.ExtractionPaused && !summary.EngineFault {
		return
	}
	headline := "记忆引擎连接异常，历史浏览与对话采集仍可继续。"
	dotState := "fail"
	titleColor := tok.Danger
	key := "service-engine-fault"
	if summary.ExtractionPaused {
		key = "service-extraction-paused"
		headline = "提炼已暂停，采集仍在继续。"
		if summary.BacklogCount > 0 {
			headline = fmt.Sprintf("提炼已暂停，采集仍在继续。已有 %d 条记录等待提炼。", summary.BacklogCount)
		}
		if a.monitor.State != "exited" && a.monitor.State != "degraded" {
			dotState = "warn"
			titleColor = tok.Warning
		}
	}
	ui.Column(c.Key(key)).FillWidth().Shrink(0).Margin(sp3, sp4, 0, sp4).
		Padding(sp3, sp4).Gap(sp2).Radius(radius).Background(tok.Fill1).BorderWidth(1).BorderColor(tok.Border).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			v4StatusDot(c, dotState)
			ui.Text(c, headline).FontSize(fsBody).FontWeight(600).TextColor(titleColor).Grow(1).MinWidth(0)
			v4SmallButton(c, "查看服务与日志", nil, a.openServiceSettings)
			v4SmallButton(c, "重新检查", nil, a.health).Disabled(a.monitor.Polling)
		})
		if summary.Reason != "" {
			ui.Text(c, "原因："+summary.Reason).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.45)
		}
	})
}

package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) isExternalManagedStore() bool {
	return !a.initialSetup && a.configError == "" && a.Config.FirstInstall == nil &&
		a.Config.ConfigFile != "" && a.Config.ServiceURL != ""
}

func (a *App) isExternalManagedStoreStopped() bool {
	if !a.isExternalManagedStore() || a.service == "已连接" || a.monitor.State == "ready" || a.monitor.ControlReachable {
		return false
	}
	return a.monitor.ConsecutiveFailures > 0 || a.monitor.State == "degraded" || a.monitor.State == "exited" ||
		(a.service != "" && a.service != "尚未检查")
}

func (a *App) recheckExternalStoreConnection() {
	if !a.isExternalManagedStore() {
		return
	}
	a.externalStorePollPaused = false
	a.monitor.PollEnabled = true
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

func externalStoreCommands(config Config) (start, recover string) {
	python := config.Python
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	if python == "" {
		python = ".local/cognee-python/bin/python"
	} else {
		python = quote(python)
	}
	target := " --config " + quote(config.ConfigFile)
	return python + " scripts/dev.py start --no-ui" + target, python + " scripts/dev.py recover" + target
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

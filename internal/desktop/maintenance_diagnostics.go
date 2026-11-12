package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
)

func (a *App) maintenanceDiagnosticsPage(c *ui.Context) {
	ui.Scroll(c.Key("maintenance-diagnostics-body")).TrackScroll(a.settingsScrollState("diagnostics")).Grow(1).MinHeight(0).Padding(28).Gap(24).Children(func() {
		settingsHeading(c, "关于与诊断", "版本、路径与错误属于按需信息。")
		settingsCard(c, "settings-about-brand", "Memlane", func() {
			ui.Text(c, "把项目知识留在下一次工作里").FontSize(16).Bold()
			ui.Text(c, "面向程序员的本地记忆客户端").FontSize(design.Small).TextColor(design.Muted)
		})
		settingsCard(c, "settings-about-environment", "版本与环境", func() {
			settingsFact(c, "客户端", a.version, "")
			ui.Divider(c)
			settingsFact(c, "记忆服务", "本机 · 当前状态由服务回执提供", a.maintenanceServiceLabel())
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "组件").Grow(1)
				quietButton(c, "查看环境与组件").OnClick(func() { a.selectSettingsSection("environment") })
			})
		})
		if a.runtimeStatus.Phase != "ready" || a.libraryError != "" || a.schedules.RuntimeError != "" {
			ui.Column(c).Gap(8).Children(func() {
				if a.runtimeStatus.Phase == "stopped" {
					ui.Text(c, "服务已主动停止，需要时可回到服务状态明确启动。").TextColor(design.Muted)
				} else if a.runtimeStatus.Phase != "ready" {
					ui.Text(c, "服务尚未就绪，可回到服务状态检查环境与后台许可。").TextColor(design.Muted)
				}
				if a.libraryError != "" {
					ui.Text(c, "知识读取暂未完成，请核对服务状态后重试。").TextColor(design.Muted)
				}
				if a.schedules.RuntimeError != "" {
					ui.Text(c, "计划状态暂未取得，请在整理计划中重新检查。").TextColor(design.Muted)
				}
			})
		}
		ui.Collapsible(c, "展开诊断详情", &a.maintenanceDiagnosticsOpen, func() {
			ui.Column(c).Gap(12).Children(func() {
				ui.Text(c, a.maintenanceDiagnosticSummary()).FontSize(11).LineHeight(1.6).Selectable()
				quietButton(c, "复制诊断摘要").Key("maintenance-copy-diagnostics").AlignSelf(ui.Start).OnClick(func() { c.WriteClipboard(a.maintenanceDiagnosticSummary()) })
				quietButton(c, "初始化记录").AlignSelf(ui.Start).OnClick(func() { a.openInitialization("") })
				quietButton(c, "恢复设置指引").AlignSelf(ui.Start).OnClick(func() { a.hideGuidance(false); a.navigate("使用指引", "") })
			})
		})
	})
	ui.Row(c).MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		quietButton(c, "检查更新").Disabled(a.check == nil || a.checking).OnClick(func() {
			if a.check != nil {
				a.check()
			}
		})
		actionButton(c, "回到服务状态", nil).OnClick(func() { a.maintenanceSection = "service" })
	})
}

// Copy only status/receipt fields. Configuration payloads, source text, paths,
// provider endpoints and access credentials do not belong to this summary.
func (a *App) maintenanceDiagnosticSummary() string {
	lines := []string{fmt.Sprintf("客户端：%s", a.version), fmt.Sprintf("服务阶段：%s", a.runtimeStatus.Phase), fmt.Sprintf("服务状态：%s", a.serviceState)}
	if a.processing.Loaded {
		lines = append(lines, fmt.Sprintf("处理方式：%s", a.processing.currentLabel()))
	} else {
		lines = append(lines, "处理方式：未取得当前回执")
	}
	if r := a.indexRebuild; r.ID != "" {
		lines = append(lines, fmt.Sprintf("索引重建：%s · %s · %d条", r.ID, r.State, len(r.Items)))
	}
	if r := a.backups.LastRestore; r.ID != "" {
		lines = append(lines, fmt.Sprintf("整库恢复：%s", r.ID))
	}
	if t := a.projectOrganizer.Task; t != nil {
		lines = append(lines, fmt.Sprintf("项目整理：%s · %s · %s", t.ID, t.State, t.Error))
	}
	return strings.Join(lines, "\n")
}

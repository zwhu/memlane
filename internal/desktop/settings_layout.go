package desktop

import "github.com/egoist/mygo/ui"

// Settings keeps navigation in the continuous sidebar. Maintenance state stays
// with the existing owners, including links that still select maintenanceSection.
var settingsSections = []struct{ key, label string }{
	{"environment", "环境与组件"}, {"agents", "Agent 连接"}, {"processing", "记忆处理"},
	{"service", "记忆服务"}, {"backups", "备份与恢复"}, {"index", "检索索引"},
	{"updates", "客户端更新"}, {"diagnostics", "关于与诊断"},
}

func settingsNavigationWidth(width float32) float32 {
	if width < 1000 {
		return 160
	}
	return 196
}

func (a *App) selectedSettingsSection() string {
	if a.settingsSection == "environment" && a.maintenanceSection != "" && a.maintenanceSection != "components" {
		return a.maintenanceSection
	}
	return a.settingsSection
}

func (a *App) selectSettingsSection(key string) {
	switch key {
	case "environment":
		a.settingsSection, a.maintenanceSection = key, "components"
		a.ensureEnvironment()
	case "agents":
		a.settingsSection = key
		a.refreshAgents(true)
		a.refreshAgentBatch()
	case "processing":
		a.settingsSection = key
		a.refreshProcessing()
	default:
		a.settingsSection, a.maintenanceSection = "environment", key
		if key == "backups" {
			a.refreshBackups()
		}
		if key == "index" {
			a.refreshIndexRebuild()
		}
	}
}

func settingsHeading(c *ui.Context, title, description string) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, title).FontSize(design.Heading).Bold()
		if description != "" {
			ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
		}
	})
}

func settingsCard(c *ui.Context, key, title string, body func()) {
	ui.Column(c).Key(key).Padding(20).Gap(16).Border(1, design.Border).Radius(7).Children(func() {
		if title != "" {
			ui.Text(c, title).Bold()
		}
		body()
	})
}

func settingsFact(c *ui.Context, title, description, value string) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, title).Bold()
			if description != "" {
				ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
			}
		})
		ui.Text(c, value).FontSize(design.Small).TextColor(design.Muted)
	})
}

func (a *App) settingsNavigation(c *ui.Context) {
	width, _ := c.Size()
	ui.Column(c).Key("settings-navigation").Width(settingsNavigationWidth(width)).Shrink(0).Background(design.SidebarFill).Children(func() {
		ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 12, 0, 56).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			if width >= 1000 {
				ui.Icon(c, icons["brand"]).FontSize(16).TextColor(design.Muted)
			}
			ui.Text(c, "Memlane").FontSize(13).Bold().TextColor(design.Muted)
		})
		ui.Scroll(c.Key("settings-nav-scroll")).Grow(1).MinHeight(0).Padding(20, 12, 14, 12).Gap(3).Children(func() {
			for _, label := range []string{"总览", "通用知识", "整理记录", "设置"} {
				item := label
				button := ui.Button(c.Key("settings-global-"+item), "").Label(item).Height(33).Padding(6, 12).Justify(ui.Start).BorderWidth(0).Background(ui.Transparent).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons[item]).FontSize(15).TextColor(design.Muted)
						ui.Text(c, item).FontSize(design.Body)
					})
				})
				if item == "设置" {
					button.Background(design.Selected)
				}
				button.OnClick(func() {
					if item != "设置" {
						a.navigate(item, "")
					}
				})
			}
			for _, item := range settingsSections {
				key := item.key
				button := ui.Button(c.Key("settings-"+key), item.label).Label(item.label).Height(30).Padding(6, 12, 6, 36).Justify(ui.Start).FontSize(design.Small).TextColor(design.Muted).BorderWidth(0).Background(ui.Transparent)
				if a.selectedSettingsSection() == key {
					button.Background(design.Selected).TextColor(design.Ink)
				}
				button.OnClick(func() { a.selectSettingsSection(key) })
			}
			ui.Text(c, "• "+a.maintenanceServiceLabel()).FontSize(10).TextColor(design.Muted).Padding(20, 12)
		})
	})
}

func (a *App) settingsToolbar(c *ui.Context) {
	ui.Text(c, "设置 / "+a.settingsSectionLabel()).FontSize(design.Small).TextColor(design.Muted)
	ui.Spacer(c)
	ui.Button(c.Key("settings-search"), "搜索所有记忆   ⌘ K").FontSize(design.Small).TextColor(design.Muted).Height(30).Padding(6, 14).Background(design.Surface).OnClick(a.OpenGlobalSearch)
}

// Scroll positions are view state only, never configuration or task state.
type SettingsLayoutState struct {
	ProviderAdvanced bool
	Scroll           map[string]*ui.ScrollState
}

func (a *App) settingsScrollState(key string) *ui.ScrollState {
	if a.settingsLayout.Scroll == nil {
		a.settingsLayout.Scroll = map[string]*ui.ScrollState{}
	}
	if a.settingsLayout.Scroll[key] == nil {
		a.settingsLayout.Scroll[key] = &ui.ScrollState{}
	}
	return a.settingsLayout.Scroll[key]
}

func settingsNotice(c *ui.Context, message string) {
	ui.Text(c, message).FontSize(design.Small).TextColor(design.Muted).Padding(13, 14).Background(design.Surface).Border(1, design.Border).Radius(6)
}

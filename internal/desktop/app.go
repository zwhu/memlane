// Package desktop contains the native desktop shell. Engine state stays explicit
// until a real service is connected; navigation never manufactures memory data.
package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

var (
	sidebarColor  = ui.Hex("#e8f0fa")
	selectedColor = ui.Hex("#d6e2ef")
	borderColor   = ui.Hex("#e6e8eb")
	navIcons      = map[string]*ui.SVG{
		"总览":    svg(`<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="15" y="3" width="6" height="6" rx="1"/><rect x="3" y="15" width="6" height="6" rx="1"/><rect x="15" y="15" width="6" height="6" rx="1"/>`),
		"全部记忆":  svg(`<path d="m3 8 9-5 9 5-9 5-9-5zm0 5 9 5 9-5M3 18l9 5 9-5"/>`),
		"全局记忆":  svg(`<circle cx="12" cy="7" r="4"/><path d="M4 21v-3a8 8 0 0 1 16 0v3z"/>`),
		"Agent": svg(`<rect x="2" y="4" width="20" height="16" rx="3"/><path d="m6 9 3 3-3 3m6 0h5"/>`),
		"整理记录":  svg(`<circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>`),
		"项目":    svg(`<path d="M3 6h7l2 3h9v12H3z"/>`),
		"设置":    svg(`<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3"/><path d="M12 1v3m0 16v3M1 12h3m16 0h3"/>`),
	}
)

func svg(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

type App struct {
	page           string
	collapsed      bool
	editing        bool
	draft          string
	zoom           float32
	version        string
	updateText     string
	checking       bool
	check          func()
	runtimeStatus  RuntimeStatus
	prepareRuntime func()
	cancelRuntime  func()
	stopRuntime    func()
	resumeRuntime  func()
	keepService    bool
	keepChanged    func(bool)
	installUpdate  func()
}

func New(version string, check func()) *App {
	return &App{page: "总览", zoom: 1, version: version, check: check, keepService: true,
		updateText: "此版本尚未配置更新源"}
}

func (a *App) SetRuntimeStatus(status RuntimeStatus) { a.runtimeStatus = status }
func (a *App) ConfigureRuntime(prepare, cancel, stop, resume func(), keep bool, changed func(bool)) {
	a.prepareRuntime, a.cancelRuntime, a.stopRuntime, a.resumeRuntime = prepare, cancel, stop, resume
	a.keepService, a.keepChanged = keep, changed
}
func (a *App) SetInstallUpdate(install func()) { a.installUpdate = install }

func (a *App) SetUpdateStatus(message string, checking bool) {
	a.updateText, a.checking = message, checking
}

func (a *App) View(c *ui.Context) {
	theme := *ui.LightTheme()
	theme.Spacing, theme.FontSize, theme.Radius = 3, 13, 5
	theme.Accent = ui.Hex("#303842")
	c.SetTheme(&theme)
	c.SetZoom(a.zoom)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Background(ui.Hex("#ffffff")).Children(func() {
			a.toolbar(c)
			if a.page == "总览" {
				a.home(c)
			} else {
				a.details(c)
			}
		})
	})
}

func (a *App) sidebar(c *ui.Context) {
	width := float32(212)
	if a.collapsed {
		width = 112 // Retain system window controls and accessible text navigation.
	}
	background := sidebarColor
	if c.Vibrancy() {
		background = sidebarColor.Alpha(0.65)
	}
	ui.Column(c).Key("navigation").Width(width).Shrink(0).Background(background).
		BorderWidth(0, 1, 0, 0).BorderColor(borderColor).Children(func() {
		bar := c.TitleBar()
		ui.Row(c).Height(54).Padding(0, 12, 0, max(bar.Left+8, 12)).DragWindow().Children(func() {
			if !a.collapsed {
				ui.Spacer(c)
				ui.Button(c, "收起").Label("切换侧边栏").OnClick(func() { a.collapsed = true })
			}
		})
		ui.Column(c).Padding(12, 10).Gap(6).Children(func() {
			if a.collapsed {
				ui.Button(c, "展开").Label("切换侧边栏").OnClick(func() { a.collapsed = false })
			}
			for _, page := range []string{"总览", "全部记忆", "全局记忆", "Agent", "整理记录", "项目"} {
				a.nav(c, page)
			}
		})
		ui.Spacer(c)
		ui.Column(c).Padding(10).Gap(10).Children(func() {
			a.nav(c, "设置")
			status := "服务未连接"
			if a.runtimeStatus.Phase == "ready" {
				status = "服务运行中"
			} else if a.runtimeStatus.Phase == "stopped" {
				status = "服务已停止"
			}
			ui.Text(c, status).FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}

func (a *App) nav(c *ui.Context, page string) {
	button := ui.Button(c.Key("nav-"+page), "").Label(page).Height(34).Padding(8, 12).Radius(5).
		Justify(ui.Start).Gap(10).BorderWidth(0).Background(ui.Transparent).Children(func() {
		if !a.collapsed {
			ui.Icon(c, navIcons[page]).FontSize(15).TextColor(ui.Hex("#6c7886"))
		}
		ui.Text(c, page).SingleLine()
	})
	if a.page == page {
		button.Background(selectedColor)
	} else if button.Hovered() {
		button.Background(ui.Hex("#dfE9f4"))
	}
	button.OnClick(func() { a.page = page })
}

func (a *App) toolbar(c *ui.Context) {
	ui.Row(c).Height(54).Shrink(0).Padding(10, 24).Gap(10).
		BorderWidth(0, 0, 1, 0).BorderColor(borderColor).DragWindow().Children(func() {
		ui.Text(c, a.page).Bold()
		ui.Spacer(c)
		ui.Text(c, "本机").TextColor(c.Theme().TextMuted)
		ui.Button(c, "新增记忆").OnClick(func() { a.page, a.editing = "全部记忆", true })
	})
}

func (a *App) home(c *ui.Context) {
	ui.Column(c).Key("overview").Grow(1).Padding(30, 36).Gap(22).Children(func() {
		ui.Text(c, "记忆动态").FontSize(22).Bold()
		ui.Text(c, "集中查看项目知识，以及需要你确认的改动。").TextColor(c.Theme().TextMuted)
		ui.Column(c).Border(1, borderColor).Radius(8).Padding(20).Gap(16).Children(func() {
			ui.Text(c, "每日记忆").Bold()
			message := "连接记忆服务后显示每日新增记录。"
			if a.runtimeStatus.Phase == "ready" {
				message = "记忆统计功能待启用。"
			}
			ui.Text(c, message).TextColor(c.Theme().TextMuted)
			// Empty cells express unavailable activity, not fabricated contributions.
			ui.Row(c).Gap(4).Children(func() {
				for week := 0; week < 53; week++ {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						for day := 0; day < 7; day++ {
							ui.Box(c).Height(10).Radius(2).Background(ui.Hex("#f0f2f5"))
						}
					})
				}
			})
		})
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(28).Children(func() {
			ui.Column(c).Grow(2).Gap(14).Children(func() {
				ui.Text(c, "最近新增").Bold()
				ui.Divider(c)
				message := "等待连接记忆服务"
				if a.runtimeStatus.Phase == "ready" {
					message = "记忆列表功能待启用"
				}
				ui.Text(c, message).TextColor(c.Theme().TextMuted)
				ui.Text(c, "引擎准备完成后，可连接 Agent 并开始积累项目知识。").TextColor(c.Theme().TextMuted)
				ui.Button(c, "查看准备状态").AlignSelf(ui.Start).OnClick(func() { a.page = "设置" })
			})
			ui.Column(c).Grow(1).Gap(14).Children(func() {
				ui.Text(c, "待确认").Bold()
				ui.Divider(c)
				ui.Text(c, "连接后显示整理建议").TextColor(c.Theme().TextMuted)
			})
		})
	})
}

func (a *App) details(c *ui.Context) {
	ui.Row(c).Key("details-layout").Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Key("item-list").Width(260).Shrink(0).Padding(20).Gap(12).
			BorderWidth(0, 1, 0, 0).BorderColor(borderColor).Children(func() {
			if a.page == "设置" {
				ui.Text(c, "本地组件与更新").Bold()
				ui.Text(c, "引擎、模型与客户端分别管理。").TextColor(c.Theme().TextMuted)
			} else {
				ui.Text(c, a.page).Bold()
				message := "记忆服务尚未连接"
				if a.runtimeStatus.Phase == "ready" {
					message = "记忆管理功能待启用"
				}
				ui.Text(c, message).TextColor(c.Theme().TextMuted)
			}
		})
		ui.Column(c).Key("detail-pane").Grow(1).MinWidth(0).Padding(28).Gap(18).Children(func() {
			if a.page == "设置" {
				a.settings(c)
			} else if a.page == "全部记忆" && a.editing {
				ui.Text(c, "新增记忆").FontSize(20).Bold()
				ui.Text(c, "内容暂存于当前窗口，尚未保存。").TextColor(c.Theme().TextMuted)
				ui.TextArea(c.Key("memory-draft"), &a.draft).Label("记忆内容").Placeholder("记录一条项目经验…").Grow(1)
				message := "记忆服务尚未就绪，暂不能保存。"
				if a.runtimeStatus.Phase == "ready" {
					message = "记忆编辑功能待启用，当前内容仅为草稿。"
				}
				ui.Text(c, message).TextColor(c.Theme().TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "返回").OnClick(func() { a.editing = false })
					ui.Button(c, "保存").Disabled(true)
				})
			} else {
				ui.Text(c, "等待记忆服务").FontSize(20).Bold()
				ui.Text(c, "本地组件准备完成后，这里会显示真实记录。").TextColor(c.Theme().TextMuted)
			}
		})
	})
}

func (a *App) settings(c *ui.Context) {
	ui.Text(c, "本地组件与更新").FontSize(20).Bold()
	if a.prepareRuntime == nil {
		ui.Text(c, "本地引擎：待准备").Bold()
		ui.Text(c, "引擎与模型将独立下载；当前尚无可安装的组件版本。").TextColor(c.Theme().TextMuted)
	} else {
		ui.Text(c, a.runtimeStatus.Message).Selectable()
		if a.runtimeStatus.EngineBytes > 0 && a.runtimeStatus.ModelBytes > 0 {
			ui.Text(c, fmt.Sprintf("引擎 %.2f MB · 模型 %.2f MB", float64(a.runtimeStatus.EngineBytes)/1e6, float64(a.runtimeStatus.ModelBytes)/1e6)).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		if a.runtimeStatus.Total > 0 {
			ui.Text(c, fmt.Sprintf("已准备 %.2f / %.2f MB", float64(a.runtimeStatus.Downloaded)/1e6, float64(a.runtimeStatus.Total)/1e6)).TextColor(c.Theme().TextMuted)
			ui.Progress(c, float64(a.runtimeStatus.Downloaded)/float64(a.runtimeStatus.Total))
		}
		busy := a.runtimeStatus.Phase == "checking" || a.runtimeStatus.Phase == "downloading" || a.runtimeStatus.Phase == "installing" || a.runtimeStatus.Phase == "installed" || a.runtimeStatus.Phase == "preparing"
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "准备 / 重试").Disabled(busy).OnClick(a.prepareRuntime)
			if busy {
				ui.Button(c, "取消准备").OnClick(a.cancelRuntime)
			}
			if a.runtimeStatus.Phase == "ready" {
				ui.Button(c, "停止服务").OnClick(a.stopRuntime)
			}
			if a.runtimeStatus.Phase == "stopped" {
				ui.Button(c, "启动服务").OnClick(a.resumeRuntime)
			}
		})
		previous := a.keepService
		ui.Checkbox(c, &a.keepService, "退出 App 后继续运行记忆服务")
		if previous != a.keepService && a.keepChanged != nil {
			a.keepChanged(a.keepService)
		}
	}
	ui.Divider(c)
	ui.Text(c, "客户端 "+a.version).Bold()
	ui.Text(c, a.updateText).Selectable().TextColor(c.Theme().TextMuted)
	ui.Button(c, "检查更新").Disabled(a.checking || a.check == nil).OnClick(func() {
		if a.check != nil {
			a.check()
		}
	})
	if a.installUpdate != nil {
		ui.Button(c, "安装更新并重新打开").Disabled(a.checking).OnClick(a.installUpdate)
	}
	ui.Divider(c)
	ui.Text(c, "界面缩放").Bold()
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, "缩小").Disabled(a.zoom <= 0.8).OnClick(func() { a.zoom = max(float32(0.8), a.zoom-0.1) })
		ui.Text(c, fmt.Sprintf("%.0f%%", a.zoom*100)).Width(48)
		ui.Button(c, "放大").Disabled(a.zoom >= 1.4).OnClick(func() { a.zoom = min(float32(1.4), a.zoom+0.1) })
	})
}

// UpdateTimeout bounds a manual check so a stalled feed cannot block shutdown.
const UpdateTimeout = 20 * time.Second

package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) overviewShell(c *ui.Context) {
	applyAppearance(a.effectiveDark(c.Theme().Dark))
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.mainShell(c) })
}

func overviewReadingTitle(i Item) string {
	if i.Document != nil && readableDocumentTitle(*i.Document) == "" && i.BodyLoaded {
		if title := firstReadingSentenceLimit(itemPresentation(i).VisibleBody, 240); title != "" {
			return title
		}
	}
	return i.Title
}

func (a *App) returnToOverview() {
	if a.page == "知识" || a.page == "通用知识" {
		s := a.state()
		if len(s.History) > a.overviewSourceFloor[s] {
			a.returnFromSource(s)
			return
		}
	}
	a.navigate("总览", 0)
}

func (a *App) showOverviewProjects() {
	a.openAllProjects()
}

func (a *App) overviewNavigation(c *ui.Context) {
	a.refreshSidebarQueueIfDue(time.Now())
	a.refreshServiceMonitorIfDue(time.Now())
	selected := a.page
	if (a.page == "知识" || a.page == "图谱") && a.project >= 0 && a.project < len(a.Config.Projects) {
		selected = "project:" + a.Config.Projects[a.project].ID
	}
	if a.page == "设置" && a.settingsOrigin != nil {
		selected = "project:" + a.access.OpenProject
	}
	sidebar := ui.Themed(c, sidebarTheme).Width(sidebarW).Shrink(0).Background(tok.SidebarTint)
	if !c.Vibrancy() {
		sidebar.Background(tok.Sidebar)
	}
	sidebar.Children(func() {
		// Drag region below macOS traffic lights + V4 brand header.
		ui.Row(c).Height(titlebarH).Shrink(0).DragWindow()
		ui.Row(c).Height(32).Shrink(0).Padding(0, sp4, sp2, sp4).AlignItems(ui.Center).Gap(sp2).DragWindow().Children(func() {
			ui.Image(c, tok.BrandMark).Size(20, 20).Fit(ui.Contain)
			homeText(c, "Memlane", homeBrand)
		})
		var activeItem ui.Element
		navHost := ui.Column(c.Key("sidebar-nav-host")).FillWidth().Grow(1).MinHeight(0)
		var ind ui.Element
		var sb ui.Element
		navHost.Children(func() {
			ui.Box(c.Key("sidebar-nav-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
				ind = ui.Box(c.Key("sidebar-nav-indicator")).Margin(0, 0, 0, sp2).
					Width(sidebarW - 2*sp2).Height(rowHeight1).Radius(radius).Background(tok.AccentSoft).PassThrough()
			})
			sb = ui.Sidebar(c.Key("main-sidebar"), &selected, func() {
				navEntry := func(id, label string, linear, solid *ui.SVG) {
					on := selected == id
					buildItem := func() {
						icon := linear
						if on {
							icon = solid
						}
						item := ui.SidebarItem(c.Key("nav-item-"+id), id, icon, label).Height(rowHeight1).Gap(12).Padding(0, sp2)
						if on {
							activeItem = item
						}
						sidebarSelection(item, on, activeItem.Bounds().H > 0)
					}
					itemTheme := sidebarTheme
					if on {
						itemTheme = sidebarSelectedTheme
					}
					ui.Themed(c.Key("nav-wrap-"+id), itemTheme).FillWidth().Background(ui.Transparent).Children(buildItem)
				}
				navEntry("总览", "总览", v4IconOverview, tok.SolidOverview)
				navEntry("通用知识", "通用记忆", v4IconCommon, tok.SolidCommon)
				navEntry("用户画像", "用户画像", v4IconProfile, tok.SolidProfile)

				ui.Row(c).Padding(sp5, sp1, sp1, sp2).AlignItems(ui.Center).Gap(sp1).Children(func() {
					ui.Text(c, "项目").Font(fontUI).FontSize(fsCaption).FontWeight(550).TextColor(tok.Text3)
					ui.Spacer(c)
					allBtn := ui.ButtonBase(c).Role(ui.RoleButton).Label(fmt.Sprintf("全部 %d", len(a.Config.Projects))).
						Cursor(ui.CursorPointer).Height(22).Padding(0, 8).Radius(radiusSmall).BorderWidth(0)
					allBtn.Background(homeHoverColor(allBtn, "all-proj-bg", ui.Transparent, tok.Hover)).Children(func() {
						ui.Text(c, fmt.Sprintf("全部 %d", len(a.Config.Projects))).Font(fontUI).FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3).SingleLine()
					}).OnClick(a.openAllProjects)
					a.projectAddMenu(c)
				})
				labels := settings.ProjectLabels(a.Config.Projects)
				for idx, p := range a.Config.Projects {
					if a.projectIsHidden(p) {
						continue
					}
					index := idx
					proj := p
					id := "project:" + proj.ID
					on := selected == id
					buildProject := func() {
						icon := v4IconFolder
						if on {
							icon = tok.SolidFolder
						}
						item := ui.SidebarItem(c.Key("project-nav-"+proj.ID), id, icon, middleProjectName(labels[index])).
							Label(labels[index]).Tooltip(proj.Name+" · "+proj.Path).
							MinHeight(rowHeight1).Height(rowHeight1).FillWidth().MinWidth(0).Gap(8).Padding(0, sp2)
						a.projectNotificationMenu(c, item, proj)
						if on {
							activeItem = item
						}
						sidebarSelection(item, on, activeItem.Bounds().H > 0)
						item.Children(func() {
							a.unreadProjectBadge(c, proj, index, on)
						})
					}
					itemTheme := sidebarTheme
					if on {
						itemTheme = sidebarSelectedTheme
					}
					ui.Themed(c.Key("project-wrap-"+proj.ID), itemTheme).FillWidth().Background(ui.Transparent).Children(buildProject)
				}
			}).FillWidth().Grow(1).MinHeight(0).Padding(0, sp2).Background(ui.Transparent).TrackScroll(&a.overviewState.SidebarScroll).OnChange(func() {
				for idx, p := range a.Config.Projects {
					if selected == "project:"+p.ID {
						a.navigate("知识", idx)
						return
					}
				}
				a.navigate(selected, 0)
			})
		})
		// Paint-only AnimatePose indicator slides at 120Hz without rebuilding App.View.
		sbBox := sb.Bounds()
		itemBox := activeItem.Bounds()
		hasActive := itemBox.H > 0 && sbBox.H > 0 && selected != "设置"
		targetY := a.overviewState.LastNavIndY
		fade := float32(1)
		if hasActive {
			targetY = itemBox.Y - sbBox.Y
			a.overviewState.LastNavIndY = targetY
			if targetY+itemBox.H >= 0 && targetY <= sbBox.H {
				fade = 0
			}
		}
		if itemBox.H > 0 {
			ind.Height(itemBox.H)
		}
		// Critically damped vertical slide (Bounce: 0) prevents 176px Overview<->Project
		// indicator jumps from overshooting/bouncing on the first switch.
		ind.AnimatePose(ui.Pose{Y: targetY, Scale: 1, Fade: fade}, ui.Animation{Spring: ui.SpringOptions{Duration: 240 * time.Millisecond, Bounce: 0}})

		ui.Toolbar(c, func() {
			on := a.page == "设置" && a.settingsOrigin == nil
			entry := ui.ButtonBase(c).Role(ui.RoleButton).Cursor(ui.CursorPointer).Label("设置").
				FillWidth().Height(rowHeight1).Padding(0, sp2).Gap(12).Radius(radius).
				AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0)
			if on {
				entry.Background(tok.AccentSoft)
			} else {
				entry.Background(homeHoverColor(entry, "settings-nav-bg", ui.Transparent, tok.Hover))
			}
			v4ButtonPress(entry, 0.98)
			icoColor, txtColor, weight := tok.Text2, tok.Text1, 400
			if on {
				icoColor, txtColor, weight = tok.Accent, tok.Accent, 500
			}
			entry.Children(func() {
				ui.Icon(c, v4IconSettings).FontSize(16).TextColor(icoColor)
				ui.Text(c, "设置").Font(fontUI).FontSize(fsBody).FontWeight(weight).TextColor(txtColor)
			}).OnClick(func() { a.settingsOrigin = nil; a.access.OpenProject = ""; a.navigate("设置", 0) })
		}).Height(40).Shrink(0).Padding(0, sp2)

		appFooter(c, "侧栏状态栏").Padding(0, sp3, 0, sp4).Gap(sp2).Children(func() {
			overallDot, overallLabel := a.trayOverallServiceStatus()
			svcBtn := ui.ButtonBase(c.Key("sidebar-service-status")).Role(ui.RoleButton).Label(overallLabel).
				Cursor(ui.CursorPointer).Grow(1).MinWidth(0).Height(28).Padding(0, 4).Radius(radiusSmall).BorderWidth(0).
				AlignItems(ui.Center).Justify(ui.Start).Gap(sp2)
			btnHovered := svcBtn.Hovered()
			btnFocused := svcBtn.Focused()
			svcBtn.Background(homeHoverColor(svcBtn, "svc-foot-bg", ui.Transparent, tok.Hover)).Children(func() {
				v4StatusDot(c, overallDot)
				homeText(c, overallLabel, homeMeta).SingleLine()
			}).OnClick(func() {
				a.servicePopover.hoverIntent().MarkClicked()
				a.servicePopover.PopoverOpen = false
				a.servicePopover.popoverBtnHover = false
				a.servicePopover.popoverPanelHover = false
				a.settingsOrigin = nil
				a.access.OpenProject = ""
				a.access.SettingsSection = "服务"
				a.monitor.Expanded = false
				a.navigate("设置", 0)
			})
			a.syncServicePopoverHover(btnHovered, a.servicePopover.popoverPanelHover, btnFocused)
			a.sidebarServicePopover(c, svcBtn, overallDot, overallLabel)
			a.sidebarAppearanceButton(c)
		})
	})
}

func (a *App) cancelServicePopoverTimers() {
	if a.servicePopover.intent != nil {
		a.servicePopover.intent.Cancel()
	}
}

func (a *App) closeServicePopover() {
	a.servicePopover.hoverIntent().SetOpen(false)
	a.servicePopover.PopoverOpen = false
	a.servicePopover.popoverBtnHover = false
	a.servicePopover.popoverPanelHover = false
}

// syncServicePopoverHover delegates 300ms hover-open / 200ms leave-close +
// safe-triangle cursor protection to kit/ui.HoverIntent (zero idle polling),
// and opens immediately on keyboard focus per #15.
func (a *App) syncServicePopoverHover(btnHover, panelHover, btnFocus bool) {
	intent := a.servicePopover.hoverIntent()
	if intent.Open() != a.servicePopover.PopoverOpen {
		intent.SetOpen(a.servicePopover.PopoverOpen)
	}
	a.servicePopover.popoverBtnHover = btnHover
	a.servicePopover.popoverPanelHover = panelHover
	var updateFn func(func())
	if a.Update != nil {
		updateFn = func(fn func()) {
			a.Update(func() {
				if fn != nil {
					fn()
				}
				a.servicePopover.PopoverOpen = intent.Open()
			})
		}
	}
	intent.Sync(btnHover, panelHover, btnFocus, nil, a.servicePopover.panelBounds, updateFn)
	a.servicePopover.PopoverOpen = intent.Open()
}

func (a *App) sidebarServicePopover(c *ui.Context, anchor ui.Element, overallDot, overallLabel string) {
	if !a.servicePopover.PopoverOpen {
		return
	}
	a.syncSettingsTransport()
	m := a.monitor
	checkedLabel := "尚未检查"
	if !m.LastCheckedAt.IsZero() {
		checkedLabel = "更新于 " + m.LastCheckedAt.Local().Format("15:04:05")
	}
	panelHovered := false
	ui.PopoverBase(c.Key("sidebar-service-popover"), anchor, &a.servicePopover.PopoverOpen, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).
			Width(312).Padding(0).Gap(0).Radius(radiusPopover).Background(tok.BgElevated).
			Border(1, tok.Border).Shadow(0, 10, 28, 0, tok.ShadowLift).
			Label("服务组件状态").Transition(v4DialogTransition())
		panelHovered = panel.Hovered() || panel.FocusWithin()
		a.servicePopover.panelBounds = panel.Bounds()
		panel.Children(func() {
			// Header
			ui.Row(c).FillWidth().Padding(sp3, sp3, sp2, sp3).Gap(sp2).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				v4StatusDot(c, overallDot)
				ui.Text(c, overallLabel).Font(fontUI).FontSize(fsBody).FontWeight(600).TextColor(tok.Text1).SingleLine()
				ui.Spacer(c)
				ui.Text(c, checkedLabel).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
			})

			// Component rows (unified with Settings › Service and tray flyout)
			components := a.settingsController.ServiceComponents()
			ui.Column(c).FillWidth().Padding(sp1, sp3).Gap(0).Children(func() {
				for idx, comp := range components {
					borderBottom := float32(1)
					if idx == len(components)-1 {
						borderBottom = 0
					}
					ui.Row(c).FillWidth().Padding(sp2, 0).Gap(sp2).AlignItems(ui.Start).
						BorderWidth(0, 0, borderBottom, 0).BorderColor(tok.Border).Children(func() {
						ui.Box(c).Padding(4, 0, 0, 0).Shrink(0).Children(func() { v4StatusDot(c, comp.DotState) })
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
								ui.Text(c, comp.Name).Font(fontUI).FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).LineHeight(1.35).Grow(1).MinWidth(0).SingleLine()
								ui.Text(c, comp.StatusText).Font(fontUI).FontSize(fsCaption).FontWeight(400).FontFeatures("tnum").TextColor(comp.StatusColor).Shrink(0).SingleLine()
							})
							if comp.CompactMetrics != "" {
								ui.Text(c, comp.CompactMetrics).Font(fontMono).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).LineHeight(1.4)
							}
						})
					})
				}
			})

			// Footer link
			ui.Row(c).FillWidth().Padding(sp2, sp3).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
				detailBtn := ui.ButtonBase(c.Key("sp-go-service")).Role(ui.RoleButton).Label("在设置中查看详情").
					Cursor(ui.CursorPointer).FillWidth().Height(28).Padding(0, sp2).Radius(radius).BorderWidth(0).
					AlignItems(ui.Center).Justify(ui.SpaceBetween)
				detailBtn.Background(homeHoverColor(detailBtn, "sp-go-bg", ui.Transparent, tok.AccentSofter)).Children(func() {
					ui.Text(c, "在设置中查看详情").Font(fontUI).FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
					ui.Icon(c, v4IconChev).FontSize(14).TextColor(tok.Accent).Shrink(0)
				}).OnClick(func() {
					a.closeServicePopover()
					a.settingsOrigin = nil
					a.access.OpenProject = ""
					a.access.SettingsSection = "服务"
					a.monitor.Expanded = false
					a.navigate("设置", 0)
				})
			})
		})
	})
	a.syncServicePopoverHover(a.servicePopover.popoverBtnHover, panelHovered, false)
}

func (a *App) serviceConnectionTip() string {
	address := settings.SanitizedMonitorURL(a.Config.ServiceURL)
	state := "记忆引擎（Cognee）· 待检查"
	if a.monitor.EngineStatus == "已连接" {
		state = "记忆引擎（Cognee）· 已连接"
		if a.monitor.State == "warning" {
			state = "记忆引擎（Cognee）· 短暂波动"
		}
	} else if a.monitor.EngineStatus != "尚未检查" {
		state = "记忆引擎（Cognee）· 连接异常"
	}
	tip := state + "\n地址：" + address
	if summary := a.settingsController.ServiceSummary(); summary != "" {
		tip += "\n" + summary
	}
	if a.monitor.LastError != "" && a.monitor.State != "ready" {
		tip += "\n错误：" + a.monitor.LastError
	}
	return tip
}

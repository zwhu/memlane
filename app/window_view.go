package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func button(c *ui.Context, label string, fn func()) {
	ui.Button(c, label).FontSize(12).Padding(8, 12).Background(ui.Transparent).BorderWidth(0).Justify(ui.Start).OnClick(fn)
}
func (a *App) View(c *ui.Context) {
	a.reduceMotion = c.Preferences().ReduceMotion
	applyAppearance(a.effectiveDark(c.Theme().Dark))
	if a.settingsController.InitialSetup() {
		if c.Preferences().ReduceMotion {
			c.DisableAnimations()
		}
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.settingsController.ViewOnboarding(c) })
		a.settingsController.OnboardingShortcuts(c)
		return
	}
	if a.settingsController.UseFirstGenerationView() {
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.settingsController.ViewFirstGeneration(c) })
		return
	}
	root := ui.Themed(c, homeTheme).Fill().FontWeight(400)
	if c.Vibrancy() {
		// The sidebar sits over the window material; content paints its own background.
		c.Root().Background(ui.Transparent)
		root.Background(ui.Transparent)
	}
	root.Children(func() {
		a.viewContent(c)
		a.settingsController.ViewDeferredToast(c, a.page == "设置")
		a.knowledgeDialog(c)
		a.searchDialog(c)
		a.projectCreateDialog(c)
		a.allProjectsDialog(c)
		a.settingsController.ViewDialogs(c)
		a.forceQuitConfirmDialog(c)
		if a.page == "用户画像" {
			a.userProfileImportDialog(c)
		}
	})
}
func (a *App) viewContent(c *ui.Context) {
	externalStopped := a.isExternalManagedStoreStopped()
	now := time.Now()
	if !a.windowHidden {
		c.After(2 * time.Second)
		a.pollViewReadingIdentity(c.Now())
		a.refreshServiceMonitorIfDue(now)
		if !externalStopped && now.After(a.access.Next) {
			a.RefreshProjectDirectory()
		}
	}
	if !a.windowHidden && a.page == "总览" && a.Update != nil {
		if a.Config.ProjectControl != nil && !a.access.Loaded {
			if a.access.DirectoryError != "" {
				a.overviewState.Loading, a.overviewState.ActivityLoading = false, false
				a.overviewState.Error = a.access.DirectoryError
			}
		} else if !externalStopped && a.overviewState.ScopeKey != a.overviewScope() {
			a.loadOverview(true)
		}
	}
	if !externalStopped {
		a.refreshOverviewIfDue(now)
	}
	a.mainShell(c)
}
func (a *App) mainShell(c *ui.Context) {
	shell := ui.Row(c.Key("main-shell")).Fill().AlignItems(ui.Stretch)
	if !c.Vibrancy() {
		shell.Background(tok.Sidebar)
	}
	shell.Children(func() {
		a.navigation(c)
		// V4: the title bar and content share one opaque surface; no floating panel.
		ui.Column(c).Grow(1).MinWidth(0).Background(tok.Bg).BorderWidth(0, 0, 0, 1).BorderColor(tok.Border).Children(func() {
			a.projectHeader(c)
			ui.Column(c.Key("workspace")).Grow(1).MinHeight(0).FillWidth().Clip().Children(func() {
				if a.configError != "" {
					ui.Text(c, "运行配置不可用："+a.configError).Margin(24).TextColor(ink)
					return
				}
				// The graph owns its navigation row; reading tabs live in the list pane.
				if a.page == "图谱" {
					a.projectNavigation(c)
				}
				a.externalStoreStoppedBanner(c)
				a.serviceDualStateBanner(c)
				switch a.page {
				case "用户画像":
					a.userProfilesPage(c)
				case "总览":
					a.home(c)
				case "设置":
					a.settings(c)
				case "图谱":
					a.graphView(c)
				case "通用知识":
					a.commonKnowledgePage(c)
				case "知识":
					a.projectReading(c)
				default:
					a.home(c)
				}
			})
		})
	})
}
func (a *App) navigation(c *ui.Context) { a.overviewNavigation(c) }
func (a *App) home(c *ui.Context)       { a.overviewHome(c) }
func (a *App) settings(c *ui.Context) {
	if a.settingsOrigin != nil && a.access.OpenProject != "" {
		a.settingsController.ViewProject(c, a.access.OpenProject)
		return
	}
	a.settingsController.ViewGlobal(c)
}
func displayDate(date string) string {
	if day, ok := sourceCalendarDate(date); ok {
		return day
	}
	if date == "" {
		return "时间未知"
	}
	if t, ok := officialTime(date); ok {
		return t.In(time.Local).Format("2006-01-02 15:04")
	}
	return "时间未知"
}

func (a *App) OpenSearch() {
	if !a.projectCreate.Open && !a.settingsController.ToolGraphOpen() {
		a.openUnifiedSearch(false)
	}
}

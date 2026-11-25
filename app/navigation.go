package app

import (
	"time"
)

func (a *App) loadRecent()      { a.loadOverview() }
func (a *App) routeKey() string { return a.currentRoute().Key() }
func (a *App) state() *pane {
	return a.paneFor(a.routeKey())
}
func (a *App) projects() []Project {
	if a.page == "通用知识" {
		return a.Config.Common
	}
	return a.Config.Projects
}
func (a *App) current() (Project, bool) {
	ps := a.projects()
	if a.project >= 0 && a.project < len(ps) {
		return ps[a.project], true
	}
	return Project{}, false
}
func (a *App) navigate(page string, index int) { a.navigateKind(page, index, "") }
func (a *App) navigateKind(page string, index int, kind string) {
	a.navigateWithUnreadFilter(page, index, kind, false)
}

func (a *App) navigateWithUnreadFilter(page string, index int, kind string, unreadOnly bool) {
	a.cancelOverviewRecall()
	a.commonPage.MenuOpen = false
	a.projectUI.InfoOpen = false
	a.projectUI.MenuOpen = false
	a.closeUnifiedSearch()
	a.clearSearchOrigin()
	if page != "设置" {
		a.settingsOrigin = nil
	}
	if a.page == "总览" && page != "总览" {
		a.leaveOverview(a.panes)
	}
	if page == "总览" {
		a.enterOverview()
		a.overviewPageController().StartEntrance(false)
	}
	if page == "知识" {
		a.motionRuns.projectPage++
	}
	a.cancelRecentTitles()
	a.epoch++
	a.cancelGraphLoad()
	a.graphController.Host.Clear()
	for _, p := range a.panes {
		p.Knowledge.RecallFilter = nil
		p.Knowledge.RecallAgentID = ""
		p.Knowledge.RecallEventID = ""
		p.Knowledge.CategoryFilter = ""
		p.Knowledge.UnreadOnly = false
		p.Knowledge.Detail = false
		p.HomeAnchor, p.Locating, p.RevealItem = nil, false, ""
		a.cancelTitles(p)
		if p.readCancel != nil {
			p.readCancel()
		}
		if p.cancel != nil {
			p.cancel()
			p.Loading = false
			p.Reading = false
		}
	}
	a.page = page
	if page == "用户画像" {
		a.refreshUserProfiles()
	}
	if page == "设置" {
		a.RefreshIntegrations()
		a.RefreshOpenAtLoginAsync()
		if a.startupDeferred.armed && (!a.startupDeferred.done || !a.startupDeferred.visibleDone) {
			a.finishDeferredStartup(true)
		}
	}
	a.project = index
	if page == "图谱" {
		a.EnsureGraphAttached()
	}
	if page == "总览" {
		if !a.overviewState.Loaded || a.overviewState.ScopeKey != a.overviewScope() {
			a.loadOverview()
		} else if a.overview.RefreshAt.IsZero() || !time.Now().Before(a.overview.RefreshAt) {
			// Defer stale background refresh until after the 780ms enter animation
			// settles so returning from a project never hitches mid-transition.
			a.overview.RefreshAt = time.Now().Add(overviewRefreshInterval)
			navEpoch := a.epoch
			if a.Update != nil {
				time.AfterFunc(850*time.Millisecond, func() {
					a.Update(func() {
						if !a.windowInactive && !a.windowHidden && a.page == "总览" && a.epoch == navEpoch {
							a.loadOverview()
						}
					})
				})
			} else {
				a.loadOverview()
			}
		}
	}
	if page == "知识" {
		a.kind = "知识"
		if kind != "" {
			a.kind = kind
		}
		if a.kind == "管理" && index >= 0 && index < len(a.Config.Projects) {
			a.access.OpenProject = a.Config.Projects[index].ID
		}
	}
	if page == "通用知识" {
		a.kind = "知识"
	}
	if page != "总览" && page != "设置" && page != "图谱" && page != "用户画像" && !(page == "知识" && a.kind == "管理") {
		s := a.state()
		if page == "知识" && a.kind == "知识" {
			s.Knowledge.UnreadOnly = unreadOnly
			if s.Loaded && len(s.Items) > 0 {
				a.motionRuns.projectList++
			} else {
				// Avoid a second staggered bounce when items load asynchronously
				// after knowledge-list-body has already completed Page Rise.
				a.motionRuns.projectList = 0
			}
			a.loadKnowledgeFeed(s)
		} else {
			if page == "知识" && a.kind != "知识" && a.kind != "管理" {
				if s.Loaded && len(s.Items) > 0 {
					a.motionRuns.rawPage++
					a.motionRuns.rawList++
				} else {
					a.motionRuns.rawPage = 0
					a.motionRuns.rawList = 0
				}
			}
			a.load(s)
		}
	}
}

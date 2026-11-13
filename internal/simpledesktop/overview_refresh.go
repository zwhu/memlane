package simpledesktop

import "time"

const overviewRefreshInterval = 30 * time.Second

// Called from native window events on its UI thread. Losing focus or hiding
// stops periodic requests; returning refreshes without replacing view state.
func (a *App) SetWindowActive(active bool) {
	wasInactive := a.windowInactive
	a.windowInactive = !active
	if !active {
		return
	}
	a.RefreshProjectDirectory()
	if wasInactive && a.page == "总览" && !a.overviewState.Loading && !a.overviewState.ActivityLoading && !a.searchUI.Open {
		// Avoid starting a second enumeration on rapid blur/focus cycles.
		if a.overviewRefreshAt.IsZero() || time.Since(a.overviewRefreshAt.Add(-overviewRefreshInterval)) >= 3*time.Second {
			a.loadOverview()
		}
	}
}
func (a *App) refreshOverviewIfDue(now time.Time) bool {
	if a.page != "总览" || a.windowInactive || a.searchUI.Open || a.Update == nil {
		return false
	}
	v := &a.overviewState
	if v.Loading || v.ActivityLoading {
		return false
	}
	if a.overviewRefreshAt.IsZero() {
		a.overviewRefreshAt = now.Add(overviewRefreshInterval)
		return false
	}
	if now.Before(a.overviewRefreshAt) {
		return false
	}
	a.loadOverview()
	return true
}

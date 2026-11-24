package simpledesktop

import "time"

const overviewRefreshInterval = 30 * time.Second

// Called from native window events on its UI thread.
// Losing focus pauses periodic overview/queue/monitor refreshes while keeping
// credential checks alive for visible windows; hiding stops all periodic checks
// and forces full revalidation before reveal.
func (a *App) SetWindowActive(active bool) {
	if !active {
		a.windowInactive = true
		return
	}
	wasHidden := a.windowHidden
	a.windowHidden = false
	a.setWindowActive(true, wasHidden)
}

func (a *App) SetWindowHidden(hidden bool) {
	wasHidden := a.windowHidden
	a.windowHidden = hidden
	if hidden {
		a.windowInactive = true
		return
	}
	a.setWindowActive(true, wasHidden)
}

func (a *App) setWindowActive(active, wasHidden bool) {
	wasInactive := a.windowInactive
	a.windowInactive = !active
	if !active {
		return
	}
	a.syncReadingIdentity()
	now := time.Now()
	if wasHidden || a.access.Next.IsZero() || !now.Before(a.access.Next) {
		a.RefreshProjectDirectory()
	}
	startedFirstOverview := false
	if wasHidden && a.page == "总览" && !a.overviewState.Loaded && a.access.Loaded && a.overviewState.scope != a.overviewScope() {
		a.loadOverview()
		startedFirstOverview = true
	} else if (wasInactive || wasHidden) && a.page == "总览" && !a.overviewState.Loading && !a.overviewState.ActivityLoading && !a.searchUI.Open {
		// Avoid starting a second enumeration on rapid blur/focus cycles.
		if a.overviewRefreshAt.IsZero() || time.Since(a.overviewRefreshAt.Add(-overviewRefreshInterval)) >= 3*time.Second {
			a.loadOverview()
		}
	}
	if (wasInactive || wasHidden) && a.startupDeferred.done {
		if startedFirstOverview {
			a.verifyGraphAssetsAsync()
			a.schedulePostFirstFrameStartup(true)
		} else {
			a.PrewarmGraph()
		}
		a.refreshServiceMonitorIfDue(now)
	}
}

func (a *App) coldStartOverviewPending() bool {
	v := &a.overviewState
	return !a.overviewFirstScreenReady && a.page == "总览" && !v.Loaded &&
		(v.Loading || (a.startupDeferred.armed && !a.startupDeferred.done))
}

func (a *App) refreshOverviewIfDue(now time.Time) bool {
	if a.page != "总览" || a.windowInactive || a.windowHidden || a.searchUI.Open || a.Update == nil {
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

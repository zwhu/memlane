package simpledesktop

import "time"

const overviewRefreshInterval = 30 * time.Second
const focusRefreshDebounce = 2 * time.Second

// OnWindowFocus is called on the UI thread by the native window focus handler.
func (a *App) OnWindowFocus() {
	if a.windowInactive || a.windowHidden {
		a.SetWindowActive(true)
	}
	now := time.Now()
	if a.focusRefreshNow != nil {
		now = a.focusRefreshNow()
	}
	if !a.focusRefreshAt.IsZero() && now.Sub(a.focusRefreshAt) < focusRefreshDebounce {
		return
	}
	a.focusRefreshAt = now
	a.refreshFocusAsync()
}

func (a *App) refreshFocusAsync() {
	if a.environment.RuntimeStarted || (!a.initialSetup && a.page == "设置") {
		a.RefreshIntegrations()
	}
	if a.initialSetup {
		a.refreshLocalHosts()
		a.inspectInstallWorkerAsync()
		a.inspectOwnerRuntimeStatusAsync()
		a.refreshEnvironment()
	} else if a.shouldInspectDailyRuntimeOwner() {
		a.inspectOwnerRuntimeStatusAsync()
	}
}

// Called from native window events on its UI thread.
// Losing focus pauses periodic overview/queue/monitor refreshes while keeping
// credential checks alive for visible windows; hiding stops all periodic checks
// and forces full revalidation before reveal.
func (a *App) SetWindowActive(active bool) {
	if !active {
		a.windowActivation++
		a.windowInactive = true
		return
	}
	wasInactive, wasHidden := a.windowInactive, a.windowHidden
	a.windowInactive, a.windowHidden = false, false
	a.refreshWindowProjectDirectory(wasHidden)
	update := a.Update
	if (!wasInactive && !wasHidden) || update == nil {
		return
	}
	if wasHidden {
		// Withdraw content while the foreground credential check is in flight.
		a.InvalidateReading("")
	}
	a.windowActivation++
	activation, epoch, client := a.windowActivation, a.authEpoch, a.Client
	go func() {
		identity, base, err := readingIdentityPair(client)
		update(func() {
			if activation != a.windowActivation {
				return
			}
			if epoch == a.authEpoch {
				a.applyReadingIdentity(identity, base, err)
			}
			a.refreshReactivatedWindow(true, wasHidden)
		})
	}()
}

func (a *App) SetWindowHidden(hidden bool) {
	wasInactive, wasHidden := a.windowInactive, a.windowHidden
	a.windowActivation++
	a.windowHidden = hidden
	a.windowInactive = hidden
	if hidden {
		return
	}
	a.syncReadingIdentity()
	a.refreshWindowProjectDirectory(wasHidden)
	a.refreshReactivatedWindow(wasInactive || wasHidden, wasHidden)
}

func (a *App) refreshWindowProjectDirectory(wasHidden bool) {
	if wasHidden || a.access.Next.IsZero() || !time.Now().Before(a.access.Next) {
		a.RefreshProjectDirectory()
	}
}

func (a *App) refreshReactivatedWindow(reactivated, wasHidden bool) {
	if wasHidden && a.page == "总览" && !a.overviewState.Loaded && a.access.Loaded && a.overviewState.scope != a.overviewScope() {
		a.loadOverview(true)
		return
	}
	if reactivated && a.page == "总览" && !a.overviewState.Loading && !a.overviewState.ActivityLoading && !a.searchUI.Open {
		// Avoid starting a second enumeration on rapid blur/focus cycles.
		if a.overviewRefreshAt.IsZero() || time.Since(a.overviewRefreshAt.Add(-overviewRefreshInterval)) >= 3*time.Second {
			a.loadOverview(true)
		}
	}
	if a.startupDeferred.armed && a.startupDeferred.done && !a.startupDeferred.visibleDone {
		if a.overviewState.Loaded || a.page != "总览" || (!a.overviewState.Loading && a.overviewState.Error != "") {
			a.releaseVisibleStartupTasks(false)
		} else {
			a.resetDeferredStartupFallback()
		}
		return
	}
	if reactivated && a.startupDeferred.done && a.startupDeferred.visibleDone {
		a.PrewarmGraph()
		a.refreshServiceMonitorIfDue(time.Now())
	}
}

func (a *App) coldStartOverviewPending() bool {
	v := &a.overviewState
	return !a.overviewFirstScreenReady && a.page == "总览" && !v.Loaded &&
		(v.Loading || (a.startupDeferred.armed && (!a.startupDeferred.done || !a.startupDeferred.visibleDone)))
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

package app

import (
	"time"
)

var (
	startupDeferredFallbackTimeout = 5 * time.Second
	startupPostFirstFrameDelay     = 450 * time.Millisecond
)

type deferredStartupState struct {
	armed, done    bool
	visibleDone    bool
	gen            uint64
	timer          *time.Timer
	postFrameGen   uint64
	postFrameTimer *time.Timer
	postFrameDone  bool
}

func (s *deferredStartupState) stopTimer() {
	s.gen++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *deferredStartupState) stopAllTimers() {
	s.stopTimer()
	s.postFrameGen++
	if s.postFrameTimer != nil {
		s.postFrameTimer.Stop()
		s.postFrameTimer = nil
	}
}

func (a *App) startRuntime() {
	s := &a.startupDeferred
	s.stopAllTimers()
	update := a.Update
	s.armed, s.done, s.visibleDone, s.postFrameDone = update != nil && a.page == "总览", false, false, false
	if s.armed {
		a.overviewFirstScreenReady = false
		a.settingsController.EnableServicePolling(false)
	}
	a.loadRecent()
	a.RefreshProjectDirectory()
	a.startOverviewFirstUseDaysLoad(time.Now())
	if !s.armed {
		a.runDeferredStartupTasks()
		return
	}
	gen := s.gen
	s.timer = time.AfterFunc(startupDeferredFallbackTimeout, func() {
		update(func() {
			if gen == a.startupDeferred.gen {
				a.runDeferredStartupTasks()
			}
		})
	})
}

func (a *App) resetDeferredStartupFallback() {
	s := &a.startupDeferred
	update := a.Update
	if !s.armed || (s.done && s.visibleDone) || update == nil {
		return
	}
	s.stopTimer()
	gen := s.gen
	s.timer = time.AfterFunc(startupDeferredFallbackTimeout, func() {
		update(func() {
			if gen == a.startupDeferred.gen {
				if !a.startupDeferred.done {
					a.runDeferredStartupTasks()
				} else if !a.startupDeferred.visibleDone {
					a.releaseVisibleStartupTasks(false)
				}
			}
		})
	})
}

func (a *App) onOverviewBootstrapSettled() {
	a.overviewFirstScreenReady = true
	if a.startupDeferred.armed {
		if !a.startupDeferred.done {
			a.runDeferredStartupTasks()
		} else if !a.startupDeferred.visibleDone {
			a.releaseVisibleStartupTasks(false)
		}
	}
}

func (a *App) runDeferredStartupTasks() {
	a.finishDeferredStartup(false)
}

func (a *App) finishDeferredStartup(settingsRefreshed bool) {
	s := &a.startupDeferred
	if s.done {
		if !s.visibleDone && !a.windowHidden {
			a.releaseVisibleStartupTasks(settingsRefreshed)
		}
		return
	}
	s.stopTimer()
	s.done = true
	if a.Config.FirstInstall != nil {
		a.settingsController.InspectOwnerRuntimeStatusAsync()
	}
	a.triggerProvenanceBackfillOnce()
	a.restoreCodeState()
	if !settingsRefreshed {
		a.RefreshIntegrations()
	}
	if a.windowHidden {
		a.schedulePostFirstFrameStartup(settingsRefreshed)
		return
	}
	a.releaseVisibleStartupTasks(settingsRefreshed)
}

func (a *App) releaseVisibleStartupTasks(settingsRefreshed bool) {
	s := &a.startupDeferred
	if s.visibleDone {
		return
	}
	s.stopTimer()
	s.visibleDone = true
	a.overviewFirstScreenReady = true
	a.settingsController.EnableServicePolling(true)
	if !a.windowInactive && !a.windowHidden {
		a.health()
		a.verifyGraphAssetsAsync()
		if !settingsRefreshed {
			a.refreshSidebarQueueIfDue(time.Now())
		}
	}
	a.schedulePostFirstFrameStartup(settingsRefreshed)
}

func (a *App) schedulePostFirstFrameStartup(skipLoginRefresh bool) {
	s := &a.startupDeferred
	run := func() {
		s.postFrameTimer = nil
		s.postFrameDone = true
		if !skipLoginRefresh && !a.desktopPrefs.LoginSynced {
			a.RefreshOpenAtLoginAsync()
		}
		if !a.windowInactive && !a.windowHidden {
			a.PrewarmGraph()
		}
	}
	update := a.Update
	if update == nil || startupPostFirstFrameDelay <= 0 {
		run()
		return
	}
	s.postFrameGen++
	if s.postFrameTimer != nil {
		s.postFrameTimer.Stop()
	}
	gen := s.postFrameGen
	s.postFrameTimer = time.AfterFunc(startupPostFirstFrameDelay, func() {
		update(func() {
			if gen == a.startupDeferred.postFrameGen {
				run()
			}
		})
	})
}

func (a *App) cancelAndResetPanes() {
	a.startupDeferred.stopAllTimers()
	a.readingController.cancelAndResetPanes()
}

func (a *App) FlushPendingState(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return a.flushKnowledgeReadWithTimeout(timeout)
}

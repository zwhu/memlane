package app

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

const (
	defaultTrayBackgroundPollInterval = 60 * time.Second
	defaultForegroundPollInterval     = 5 * time.Second
)

var (
	trayPollMu             sync.Mutex
	trayPollInterval       atomic.Int64
	foregroundPollMu       sync.Mutex
	foregroundPollInterval atomic.Int64
)

type trayBackgroundPoller struct {
	// Guarded by trayPollMu. Each run owns its own stop channel.
	stop chan struct{}
}

type foregroundPoller struct {
	// Guarded by foregroundPollMu. Each run owns its own stop and done channels.
	stop chan struct{}
	done chan struct{}
}

func setTrayBackgroundPollInterval(d time.Duration) {
	trayPollInterval.Store(int64(d))
}

func setForegroundPollInterval(d time.Duration) {
	foregroundPollInterval.Store(int64(d))
}

// RefreshForTrayClick reads the tray's status even during a hidden cold start.
func (a *App) RefreshForTrayClick() {
	now := time.Now()
	a.refreshServiceMonitorForTray()
	a.refreshSidebarQueueForTray(now)
	if a.Config.ProjectControl != nil && !a.access.Loading && !a.access.Busy &&
		(!a.access.Loaded || a.access.Next.IsZero() || !now.Before(a.access.Next)) {
		a.RefreshProjectDirectory()
	}
}

func (a *App) refreshServiceMonitorForTray() {
	if settings.HasServiceMonitorEndpoint(a.Config) {
		a.pollServiceMonitor(false)
	}
}

// StartTrayBackgroundPolling is called on the UI thread after app startup.
func (a *App) StartTrayBackgroundPolling() {
	update := a.Update
	if update == nil {
		return
	}
	trayPollMu.Lock()
	defer trayPollMu.Unlock()
	if a.trayPoll == nil {
		a.trayPoll = &trayBackgroundPoller{}
	}
	if a.trayPoll.stop != nil {
		return
	}
	stop := make(chan struct{})
	a.trayPoll.stop = stop
	interval := time.Duration(trayPollInterval.Load())
	if interval <= 0 {
		interval = defaultTrayBackgroundPollInterval
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case <-stop:
					return
				default:
				}
				update(func() {
					select {
					case <-stop:
						return
					default:
					}
					if a.windowHidden && !a.settingsController.InitialSetup() && !a.Config.GenerationSetupOnly {
						a.RefreshForTrayClick()
					}
				})
			}
		}
	}()
	a.StartForegroundPolling()
}

// StopTrayBackgroundPolling can run on the UI thread or in the shutdown worker.
func (a *App) StopTrayBackgroundPolling() {
	trayPollMu.Lock()
	defer trayPollMu.Unlock()
	a.StopForegroundPolling()
	if poll := a.trayPoll; poll != nil && poll.stop != nil {
		close(poll.stop)
		poll.stop = nil
	}
}

// StartForegroundPolling starts the lightweight foreground status refresh ticker
// when the desktop window is visible and active.
func (a *App) StartForegroundPolling() {
	update := a.Update
	if update == nil {
		return
	}
	a.foregroundSilent.Store(a.windowHidden || a.windowInactive)
	foregroundPollMu.Lock()
	defer foregroundPollMu.Unlock()
	if a.foregroundPoll == nil {
		a.foregroundPoll = &foregroundPoller{}
	}
	if a.foregroundPoll.stop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	a.foregroundPoll.stop = stop
	a.foregroundPoll.done = done
	interval := time.Duration(foregroundPollInterval.Load())
	if interval <= 0 {
		interval = defaultForegroundPollInterval
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case <-stop:
					return
				default:
				}
				if a.foregroundSilent.Load() {
					continue
				}
				update(func() {
					select {
					case <-stop:
						return
					default:
					}
					if !a.windowHidden && !a.windowInactive && !a.settingsController.InitialSetup() && !a.Config.GenerationSetupOnly {
						a.RefreshVisibleStatus(time.Now())
					}
				})
			}
		}
	}()
}

// StopForegroundPolling stops the foreground refresh ticker without blocking on UI updates.
func (a *App) StopForegroundPolling() {
	foregroundPollMu.Lock()
	defer foregroundPollMu.Unlock()
	if poll := a.foregroundPoll; poll != nil && poll.stop != nil {
		close(poll.stop)
		poll.stop = nil
	}
}

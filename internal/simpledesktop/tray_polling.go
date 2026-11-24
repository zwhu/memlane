package simpledesktop

import (
	"sync"
	"sync/atomic"
	"time"
)

const defaultTrayBackgroundPollInterval = 60 * time.Second

var (
	trayPollMu       sync.Mutex
	trayPollInterval atomic.Int64
)

type trayBackgroundPoller struct {
	// Guarded by trayPollMu. Each run owns its own stop channel.
	stop chan struct{}
}

func setTrayBackgroundPollInterval(d time.Duration) {
	trayPollInterval.Store(int64(d))
}

// RefreshForTrayClick reads the tray's status even during a hidden cold start.
func (a *App) RefreshForTrayClick() {
	a.refreshServiceMonitorForTray()
	a.refreshSidebarQueueForTray(time.Now())
	if !a.access.Loaded && !a.access.Loading && !a.access.Busy && a.Config.ProjectControl != nil {
		a.RefreshProjectDirectory()
	}
}

func (a *App) refreshServiceMonitorForTray() {
	if a.Config.ServiceURL != "" {
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
	if a.monitor.trayPoll == nil {
		a.monitor.trayPoll = &trayBackgroundPoller{}
	}
	if a.monitor.trayPoll.stop != nil {
		return
	}
	stop := make(chan struct{})
	a.monitor.trayPoll.stop = stop
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
					if a.windowHidden && !a.initialSetup && !a.Config.GenerationSetupOnly {
						a.RefreshForTrayClick()
					}
				})
			}
		}
	}()
}

// StopTrayBackgroundPolling can run on the UI thread or in the shutdown worker.
func (a *App) StopTrayBackgroundPolling() {
	trayPollMu.Lock()
	defer trayPollMu.Unlock()
	if poll := a.monitor.trayPoll; poll != nil && poll.stop != nil {
		close(poll.stop)
		poll.stop = nil
	}
}

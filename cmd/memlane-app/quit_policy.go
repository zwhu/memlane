package main

import (
	"context"
	"sync"
	"time"
)

// quitCoordinator coordinates desktop quit events with the keep-alive preference
// so system quits (Cmd+Q, Dock, menu) stop owned background services when
// KeepAliveOnQuit is disabled, without blocking or duplicating shutdowns.
type quitCoordinator struct {
	mu              sync.Mutex
	quitting        bool
	skipStopOnQuit  bool
	shutdownRunning bool
	shutdownDone    bool
	keepAlive       func() bool
	shutdown        func(context.Context) error
	requestQuit     func()
	shutdownTimeout time.Duration
	logf            func(string, ...any)
}

const defaultShutdownTimeout = 15 * time.Second

func newQuitCoordinator(
	keepAlive func() bool,
	shutdown func(context.Context) error,
	requestQuit func(),
	timeout time.Duration,
	logf func(string, ...any),
) *quitCoordinator {
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	if keepAlive == nil {
		keepAlive = func() bool { return true }
	}
	return &quitCoordinator{
		keepAlive:       keepAlive,
		shutdown:        shutdown,
		requestQuit:     requestQuit,
		shutdownTimeout: timeout,
		logf:            logf,
	}
}

func (c *quitCoordinator) IsQuitting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quitting
}

func (c *quitCoordinator) RequestQuit(stopServices bool) {
	if !stopServices {
		c.mu.Lock()
		c.skipStopOnQuit = true
		c.mu.Unlock()
		if c.requestQuit != nil {
			c.requestQuit()
		}
		return
	}
	c.startShutdownAndQuit()
}

func (c *quitCoordinator) HandleBeforeQuit(prevent func()) {
	c.mu.Lock()
	if c.shutdownDone || c.skipStopOnQuit || (c.keepAlive != nil && c.keepAlive()) {
		c.quitting = true
		c.mu.Unlock()
		return
	}
	if c.shutdownRunning {
		c.mu.Unlock()
		if prevent != nil {
			prevent()
		}
		return
	}
	c.mu.Unlock()

	if prevent != nil {
		prevent()
	}
	c.startShutdownAndQuit()
}

func (c *quitCoordinator) startShutdownAndQuit() {
	c.mu.Lock()
	if c.shutdownDone {
		c.quitting = true
		c.mu.Unlock()
		if c.requestQuit != nil {
			c.requestQuit()
		}
		return
	}
	if c.shutdownRunning {
		c.mu.Unlock()
		return
	}
	c.shutdownRunning = true
	c.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
		defer cancel()
		if c.shutdown != nil {
			if err := c.shutdown(ctx); err != nil && c.logf != nil {
				c.logf("停止本地后台服务时遇到问题：%v", err)
			}
		}
		c.mu.Lock()
		c.shutdownRunning = false
		c.shutdownDone = true
		c.quitting = true
		c.mu.Unlock()
		if c.requestQuit != nil {
			c.requestQuit()
		}
	}()
}

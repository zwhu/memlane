package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// QuitCoordinator coordinates desktop quit events with the keep-alive preference
// so quit events can stop owned background services without blocking the caller
// or duplicating a shutdown in progress.
type QuitCoordinator struct {
	mu                  sync.Mutex
	quitting            bool
	skipStopOnQuit      bool
	shutdownRunning     bool
	shutdownDone        bool
	lastShutdownErr     error
	shutdownFailed      bool
	onShutdownError     func(error)
	refuseQuitOnError   bool
	keepAlive           func() bool
	shutdown            func(context.Context) error
	requestQuit         func()
	shutdownTimeout     time.Duration
	logf                func(string, ...any)
	shutdownErrorFormat string
	panicErrorFormat    string
}

const DefaultShutdownTimeout = 15 * time.Second

type QuitCoordinatorConfig struct {
	KeepAlive           func() bool
	Shutdown            func(context.Context) error
	RequestQuit         func()
	ShutdownTimeout     time.Duration
	Logf                func(string, ...any)
	ShutdownErrorFormat string
	PanicErrorFormat    string
}

func NewQuitCoordinator(cfg QuitCoordinatorConfig) *QuitCoordinator {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	if cfg.KeepAlive == nil {
		cfg.KeepAlive = func() bool { return true }
	}
	if cfg.ShutdownErrorFormat == "" {
		cfg.ShutdownErrorFormat = "Background service shutdown failed: %v"
	}
	if cfg.PanicErrorFormat == "" {
		cfg.PanicErrorFormat = "Background service shutdown panicked: %v"
	}
	return &QuitCoordinator{
		keepAlive:           cfg.KeepAlive,
		shutdown:            cfg.Shutdown,
		requestQuit:         cfg.RequestQuit,
		shutdownTimeout:     cfg.ShutdownTimeout,
		logf:                cfg.Logf,
		shutdownErrorFormat: cfg.ShutdownErrorFormat,
		panicErrorFormat:    cfg.PanicErrorFormat,
	}
}

func (c *QuitCoordinator) IsQuitting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quitting
}

func (c *QuitCoordinator) LastShutdownError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastShutdownErr
}

func (c *QuitCoordinator) ShutdownFailed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdownFailed
}

func (c *QuitCoordinator) SetOnShutdownError(fn func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onShutdownError = fn
}

func (c *QuitCoordinator) SetRefuseQuitOnShutdownError(refuse bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refuseQuitOnError = refuse
}

func (c *QuitCoordinator) RequestQuit(stopServices bool) {
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

func (c *QuitCoordinator) HandleBeforeQuit(prevent func()) {
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

func (c *QuitCoordinator) startShutdownAndQuit() {
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
		err := c.runShutdown(ctx)
		c.mu.Lock()
		c.lastShutdownErr = err
		c.shutdownFailed = err != nil
		quit := err == nil || !c.refuseQuitOnError
		onError := c.onShutdownError
		c.shutdownRunning = false
		c.shutdownDone = quit
		c.quitting = quit
		c.mu.Unlock()

		if err != nil {
			if c.logf != nil {
				c.logf(c.shutdownErrorFormat, err)
			}
			if onError != nil {
				onError(err)
			}
		}
		if quit && c.requestQuit != nil {
			c.requestQuit()
		}
	}()
}

func (c *QuitCoordinator) runShutdown(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(c.panicErrorFormat, recovered)
		}
	}()
	if c.shutdown == nil {
		return nil
	}
	return c.shutdown(ctx)
}

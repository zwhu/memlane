package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuitCoordinatorAllowsImmediateQuitWhenKeepAliveEnabled(t *testing.T) {
	var shutdownCalls atomic.Int32
	coord := newQuitCoordinator(
		func() bool { return true },
		func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		},
		nil,
		time.Second,
		nil,
	)

	prevented := false
	coord.HandleBeforeQuit(func() { prevented = true })

	if prevented {
		t.Fatal("expected quit not to be prevented when KeepAliveOnQuit is enabled")
	}
	if !coord.IsQuitting() {
		t.Fatal("expected IsQuitting() to be true when quit is allowed")
	}
	if shutdownCalls.Load() != 0 {
		t.Fatalf("expected 0 shutdown calls, got %d", shutdownCalls.Load())
	}
}

func TestQuitCoordinatorStopsServicesOnSystemQuitWhenKeepAliveDisabled(t *testing.T) {
	var shutdownCalls atomic.Int32
	quitCh := make(chan struct{}, 2)

	coord := newQuitCoordinator(
		func() bool { return false },
		func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		},
		func() { quitCh <- struct{}{} },
		time.Second,
		nil,
	)

	prevented := false
	coord.HandleBeforeQuit(func() { prevented = true })
	if !prevented {
		t.Fatal("expected first HandleBeforeQuit to prevent immediate quit while stopping services")
	}

	select {
	case <-quitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requestQuit after shutdown")
	}

	if shutdownCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 shutdown call, got %d", shutdownCalls.Load())
	}

	preventedSecond := false
	coord.HandleBeforeQuit(func() { preventedSecond = true })
	if preventedSecond {
		t.Fatal("expected second HandleBeforeQuit after shutdownDone to allow quit")
	}
	if !coord.IsQuitting() {
		t.Fatal("expected IsQuitting() to be true after shutdown completes")
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("expected shutdown to remain at 1 call, got %d", shutdownCalls.Load())
	}
}

func TestQuitCoordinatorDeduplicatesConcurrentQuitDuringShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	quitCh := make(chan struct{}, 2)
	var shutdownCalls atomic.Int32

	coord := newQuitCoordinator(
		func() bool { return false },
		func(context.Context) error {
			if shutdownCalls.Add(1) == 1 {
				close(started)
			}
			<-release
			return nil
		},
		func() { quitCh <- struct{}{} },
		time.Second,
		nil,
	)

	var wg sync.WaitGroup
	var preventedCount atomic.Int32
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coord.HandleBeforeQuit(func() { preventedCount.Add(1) })
		}()
	}

	<-started
	coord.RequestQuit(true)
	coord.HandleBeforeQuit(func() { preventedCount.Add(1) })
	wg.Wait()

	if shutdownCalls.Load() != 1 {
		t.Fatalf("expected 1 concurrent shutdown call, got %d", shutdownCalls.Load())
	}
	if preventedCount.Load() != 4 {
		t.Fatalf("expected all 4 HandleBeforeQuit calls during shutdown to prevent quit, got %d", preventedCount.Load())
	}

	close(release)
	select {
	case <-quitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requestQuit after releasing shutdown")
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("expected 1 total shutdown call, got %d", shutdownCalls.Load())
	}
}

func TestQuitCoordinatorExplicitKeepAliveOverridesSetting(t *testing.T) {
	var shutdownCalls atomic.Int32
	quitCalled := false

	coord := newQuitCoordinator(
		func() bool { return false },
		func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		},
		func() { quitCalled = true },
		time.Second,
		nil,
	)

	coord.RequestQuit(false)
	if !quitCalled {
		t.Fatal("expected RequestQuit(false) to invoke requestQuit immediately")
	}
	prevented := false
	coord.HandleBeforeQuit(func() { prevented = true })
	if prevented {
		t.Fatal("expected HandleBeforeQuit after RequestQuit(false) not to prevent quit")
	}
	if !coord.IsQuitting() {
		t.Fatal("expected IsQuitting() to be true")
	}
	if shutdownCalls.Load() != 0 {
		t.Fatalf("expected 0 shutdown calls when explicit keepAlive requested, got %d", shutdownCalls.Load())
	}
}

func TestQuitCoordinatorExplicitStopOverridesKeepAlive(t *testing.T) {
	var shutdownCalls atomic.Int32
	quitCh := make(chan struct{}, 1)

	coord := newQuitCoordinator(
		func() bool { return true },
		func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		},
		func() { quitCh <- struct{}{} },
		time.Second,
		nil,
	)

	coord.RequestQuit(true)
	select {
	case <-quitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for requestQuit on RequestQuit(true)")
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("expected 1 shutdown call, got %d", shutdownCalls.Load())
	}
	prevented := false
	coord.HandleBeforeQuit(func() { prevented = true })
	if prevented {
		t.Fatal("expected HandleBeforeQuit after shutdownDone not to prevent quit")
	}
	if !coord.IsQuitting() {
		t.Fatal("expected IsQuitting() to be true")
	}
}

func TestQuitCoordinatorStillQuitsWhenShutdownErrors(t *testing.T) {
	quitCh := make(chan struct{}, 1)
	var logged string
	var logMu sync.Mutex

	coord := newQuitCoordinator(
		func() bool { return false },
		func(context.Context) error {
			return errors.New("mock stop error")
		},
		func() { quitCh <- struct{}{} },
		time.Second,
		func(format string, args ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			logged = format
		},
	)

	prevented := false
	coord.HandleBeforeQuit(func() { prevented = true })
	if !prevented {
		t.Fatal("expected HandleBeforeQuit to prevent while waiting for shutdown")
	}
	select {
	case <-quitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected requestQuit even when shutdown returns an error")
	}
	logMu.Lock()
	gotLog := logged
	logMu.Unlock()
	if gotLog == "" {
		t.Fatal("expected shutdown error to be logged")
	}
	if !coord.IsQuitting() {
		t.Fatal("expected IsQuitting() to be true even after shutdown error")
	}
}

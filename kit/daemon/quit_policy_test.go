package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuitKeepAliveAndExplicitOverride(t *testing.T) {
	for _, tc := range []struct {
		name      string
		keepAlive bool
		explicit  bool
		stop      bool
	}{
		{name: "keep alive", keepAlive: true},
		{name: "stop on quit", stop: true},
		{name: "explicit stop", keepAlive: true, explicit: true, stop: true},
		{name: "explicit keep alive", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stops, prevented atomic.Int32
			quit := make(chan struct{}, 1)
			coordinator := NewQuitCoordinator(QuitCoordinatorConfig{
				KeepAlive: func() bool { return tc.keepAlive },
				Shutdown: func(ctx context.Context) error {
					stops.Add(1)
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > DefaultShutdownTimeout {
						return errors.New("invalid shutdown deadline")
					}
					return nil
				},
				RequestQuit: func() { quit <- struct{}{} },
			})
			prevent := func() { prevented.Add(1) }
			if tc.explicit {
				coordinator.RequestQuit(tc.stop)
			} else {
				coordinator.HandleBeforeQuit(prevent)
			}
			if tc.explicit || tc.stop {
				receive(t, quit)
			}
			if tc.explicit {
				coordinator.HandleBeforeQuit(prevent)
			}
			wantStops, wantPrevent := int32(0), int32(0)
			if tc.stop {
				wantStops = 1
				if !tc.explicit {
					wantPrevent = 1
				}
			}
			if stops.Load() != wantStops || prevented.Load() != wantPrevent ||
				!coordinator.IsQuitting() || coordinator.LastShutdownError() != nil {
				t.Fatalf("stops=%d prevented=%d quitting=%v error=%v",
					stops.Load(), prevented.Load(), coordinator.IsQuitting(), coordinator.LastShutdownError())
			}
		})
	}
}

func TestQuitDeduplicatesConcurrentShutdowns(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 32)
	unblock := make(chan struct{})
	quit := make(chan struct{}, 32)
	coordinator := NewQuitCoordinator(QuitCoordinatorConfig{
		KeepAlive: func() bool { return false },
		Shutdown: func(context.Context) error {
			calls.Add(1)
			started <- struct{}{}
			<-unblock
			return nil
		},
		RequestQuit: func() { quit <- struct{}{} },
	})
	coordinator.RequestQuit(true)
	receive(t, started)
	var callers sync.WaitGroup
	for range 16 {
		callers.Go(func() { coordinator.RequestQuit(true) })
		callers.Go(func() { coordinator.HandleBeforeQuit(nil) })
	}
	callers.Wait()
	close(unblock)
	receive(t, quit)
	if calls.Load() != 1 {
		t.Fatalf("shutdown ran %d times", calls.Load())
	}
}

func TestQuitRefusesPanicThenRetries(t *testing.T) {
	var calls atomic.Int32
	logs := make(chan string, 1)
	failures := make(chan error, 1)
	quit := make(chan struct{}, 1)
	coordinator := NewQuitCoordinator(QuitCoordinatorConfig{
		Shutdown: func(context.Context) error {
			if calls.Add(1) == 1 {
				panic("callback panic")
			}
			return nil
		},
		RequestQuit:         func() { quit <- struct{}{} },
		Logf:                func(format string, args ...any) { logs <- fmt.Sprintf(format, args...) },
		ShutdownErrorFormat: "stop failed (%v)",
		PanicErrorFormat:    "panic reported (%v)",
	})
	coordinator.SetOnShutdownError(func(err error) { failures <- err })
	coordinator.SetRefuseQuitOnShutdownError(true)
	coordinator.RequestQuit(true)
	err := receive(t, failures)
	if err == nil || !strings.Contains(err.Error(), "panic reported (callback panic)") ||
		coordinator.LastShutdownError() != err || !coordinator.ShutdownFailed() || coordinator.IsQuitting() {
		t.Fatalf("panic refusal = %v, failed=%v, quitting=%v",
			err, coordinator.ShutdownFailed(), coordinator.IsQuitting())
	}
	if got := receive(t, logs); got != "stop failed (panic reported (callback panic))" {
		t.Fatalf("shutdown log = %q", got)
	}
	select {
	case <-quit:
		t.Fatal("quit requested after refused shutdown")
	default:
	}
	coordinator.RequestQuit(true)
	receive(t, quit)
	if !coordinator.IsQuitting() || coordinator.ShutdownFailed() || coordinator.LastShutdownError() != nil || calls.Load() != 2 {
		t.Fatalf("retry failed, calls=%d, error=%v", calls.Load(), coordinator.LastShutdownError())
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback")
		var zero T
		return zero
	}
}

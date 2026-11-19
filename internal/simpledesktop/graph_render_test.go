package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func graphTestReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("graph worker failed to progress")
		var zero T
		return zero
	}
}

// The real failure was UI clear waiting for a renderer that itself needs UI
// progress. Keep the renderer stuck while exercising the actual state methods.
func TestGraphBlockedJSDoesNotBlockUIAndCoalescesLatest(t *testing.T) {
	updates := make(chan func(), 20)
	sent := make(chan string, 20)
	release := make(chan struct{})
	g := &GraphHost{ready: true, update: func(fn func()) { updates <- fn }}
	g.eval = func(ctx context.Context, code string) (any, error) {
		sent <- code
		<-release // Model JS which has not completed, even after cancellation.
		return nil, errors.New("old renderer failed")
	}
	raw := json.RawMessage(`{"nodes":[{"id":"node"}],"links":[],"color_maps":{}}`)
	if err := g.apply(context.Background(), Project{ID: "old"}, "old-dataset", raw); err != nil {
		t.Fatal(err)
	}
	graphTestReceive(t, sent)
	done := make(chan struct{})
	go func() {
		// This goroutine is the fixture's single UI owner; workers only enqueue updates.
		for n := 0; n < 50; n++ {
			g.clear()
		}
		_ = g.apply(context.Background(), Project{ID: "latest"}, "latest-dataset", raw)
		close(done)
	}()
	graphTestReceive(t, done)
	if !g.rendering || g.pending == nil || g.pending.epoch != g.epoch {
		t.Fatal("latest frame not retained")
	}
	if len(sent) != 0 {
		t.Fatal("started overlapping render workers")
	}
	var reported []error
	g.onRenderError = func(err error) { reported = append(reported, err) }
	close(release)
	graphTestReceive(t, updates)()
	code := graphTestReceive(t, sent)
	if !strings.Contains(code, `"project":"latest"`) || !strings.Contains(code, `"dataset":"latest-dataset"`) {
		t.Fatalf("stale pending frame rendered: %s", code)
	}
	if len(reported) != 0 {
		t.Fatal("old render error leaked into new scope")
	}
	graphTestReceive(t, updates)()
	if g.rendering || g.pending != nil {
		t.Fatal("render queue did not settle")
	}
	if len(reported) != 1 {
		t.Fatal("current render failure was not reported")
	}
}

func TestGraphBindingStateIsDispatchedAndCancellationIsSafe(t *testing.T) {
	updates := make(chan func(), 1)
	g := &GraphHost{update: func(fn func()) { updates <- fn }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	mutated := false
	go func() { done <- g.onUI(ctx, func() error { mutated = true; return nil }) }()
	update := graphTestReceive(t, updates)
	if mutated {
		t.Fatal("binding modified UI state on SDK worker")
	}
	cancel()
	if !errors.Is(graphTestReceive(t, done), context.Canceled) {
		t.Fatal("binding did not cancel")
	}
	update()
	if mutated {
		t.Fatal("canceled binding changed UI state")
	}
}

func TestGraphClearCancelsOutstandingJSWait(t *testing.T) {
	updates := make(chan func(), 2)
	started := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	g := &GraphHost{ready: true, update: func(fn func()) { updates <- fn }}
	g.eval = func(ctx context.Context, _ string) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	}
	raw := json.RawMessage(`{"nodes":[],"links":[],"color_maps":{}}`)
	if err := g.apply(context.Background(), Project{ID: "p"}, "ds", raw); err != nil {
		t.Fatal(err)
	}
	graphTestReceive(t, started)
	g.clear()
	graphTestReceive(t, canceled)
	graphTestReceive(t, updates)()
	graphTestReceive(t, started)
	g.closed = true
	g.pending = nil
	g.renderCancel()
	graphTestReceive(t, canceled)
	graphTestReceive(t, updates)()
	if g.rendering || g.pending != nil {
		t.Fatal("closed graph retained pending work")
	}
}

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Host's mutable state belongs to the UI thread. A render worker receives
// only this immutable snapshot; it never reads or modifies the host's state.
type graphRender struct {
	epoch uint64
	frame map[string]any
}

// Binding calls run on SDK workers. Enqueue the state operation on the UI and
// return its receipt to that worker; UI code must never call this waiting seam.
func (g *Host) OnUI(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.update == nil {
		return fmt.Errorf("graph page unavailable")
	}
	done := make(chan error, 1)
	g.update(func() {
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		done <- fn()
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Flush schedules, rather than waits for, JS. At most one evaluation is active
// and one latest snapshot is pending. Leaving a page cancels the old wait.
func (g *Host) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.closed {
		return fmt.Errorf("graph page closed")
	}
	if !g.ready {
		return nil
	}
	if g.eval == nil || g.update == nil {
		return fmt.Errorf("graph renderer unavailable")
	}
	g.pending = &graphRender{epoch: g.epoch, frame: g.frame}
	if g.renderCancel != nil {
		g.renderCancel()
	}
	g.startRender()
	return nil
}

func (g *Host) startRender() {
	if g.closed || g.rendering || g.pending == nil {
		return
	}
	job := *g.pending
	g.pending = nil
	timeout := 10 * time.Second
	if job.frame == nil {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	g.rendering, g.renderCancel = true, cancel
	eval, update := g.eval, g.update
	go func() {
		defer cancel()
		code := "window.memlaneOfficialGraph.clear(" + fmt.Sprint(job.epoch) + ")"
		var err error
		if job.frame != nil {
			var raw []byte
			raw, err = json.Marshal(job.frame)
			code = "window.memlaneOfficialGraph.apply(" + string(raw) + ")"
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			_, err = eval(ctx, code)
		}
		update(func() {
			g.rendering, g.renderCancel = false, nil
			if g.closed {
				return
			}
			if job.epoch == g.epoch && err != nil && !errors.Is(err, context.Canceled) && g.onRenderError != nil {
				g.onRenderError(err)
			}
			g.startRender()
		})
	}()
}

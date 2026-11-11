package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/store"
)

func TestCogneeSubmissionStopAndUpdateQuiesceBeforeFinalCleanup(t *testing.T) {
	for _, action := range []string{"stop", "quiesce"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Server{store: db, config: Config{Root: root}, lifecycle: ctx, stop: cancel}
			o, err := newCogneeSubmissionOwner(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			s.cogneeSubmissions = o
			run, stopRun := context.WithCancel(ctx)
			o.runs["owned-executor"] = stopRun
			o.wg.Add(1)
			released := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(released) }) }
			defer release()
			go func() { <-run.Done(); <-released; o.wg.Done() }()
			if _, err = s.perform(ctx, Request{Op: action}); err != nil {
				t.Fatal(err)
			}
			if run.Err() == nil || !o.closed {
				t.Fatal("service transition retained a running or restartable material owner")
			}
			drained := make(chan struct{})
			go func() { o.Close(); close(drained) }()
			select {
			case <-drained:
				t.Fatal("close returned before executor cleanup was acknowledged")
			default:
			}
			release()
			select {
			case <-drained:
			case <-time.After(3 * time.Second):
				t.Fatal("material owner did not drain after cleanup")
			}
		})
	}
}

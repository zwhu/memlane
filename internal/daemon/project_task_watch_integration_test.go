package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestProjectWatchCurrentSnapshotCancellationWakeAndContext(t *testing.T) {
	s, d := projectDaemonFixture(t)
	s.projectExecute = func(ctx context.Context, p agents.CodexProjectPlan, task records.ProjectTask, onStart func(int) error, onEvent func(string)) (agents.CodexProjectExecution, error) {
		return projectProcessFixture(ctx, task, onStart, onEvent, true)
	}
	task, err := s.startProjectTask(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	// The observed reply is real SQLite state, not a queued event replay.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := s.watchProjectTask(ctx, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt, Version: records.ProjectTaskVersion(task)})
	if err != nil || first.Task.ID != task.ID || first.Version != records.ProjectTaskVersion(first.Task) {
		t.Fatal(first, err)
	}
	for first.Task.State != "organizing" {
		first, err = s.watchProjectTask(ctx, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt, Version: first.Version})
		if err != nil {
			t.Fatal(err)
		}
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err = s.watchProjectTask(short, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt, Version: first.Version})
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unchanged active watch ignored cancellation", err)
	}
	result := make(chan records.ProjectTaskObservation, 1)
	failed := make(chan error, 1)
	go func() {
		v, e := s.watchProjectTask(ctx, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt, Version: first.Version})
		if e != nil {
			failed <- e
		} else {
			result <- v
		}
	}()
	if _, err = s.cancelProjectTask(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-result:
		if !v.Task.Cancelled || v.Task.ID != task.ID || v.Version == first.Version {
			t.Fatal(v)
		}
	case e := <-failed:
		t.Fatal(e)
	case <-time.After(800 * time.Millisecond):
		t.Fatal("owner transition did not wake observation")
	}
	if _, err = s.watchProjectTask(ctx, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt + 1, Version: first.Version}); err == nil {
		t.Fatal("another attempt accepted")
	}
	if _, err = s.watchProjectTask(ctx, records.ProjectTaskWatch{Task: task.ID, Attempt: task.Attempt, Version: "unknown"}); err == nil {
		t.Fatal("invalid version accepted")
	}
}

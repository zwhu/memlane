package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProjectWatchLibraryReconfigurationCancelsAndAdvancesEpoch(t *testing.T) {
	a := projectEmptyFrame()
	retired := false
	a.projectOrganizer.WatchCancel = func() { retired = true }
	a.projectOrganizer.WatchSequence = 7
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ConfigureLibrary(ctx, t.TempDir(), func(fn func()) { fn() })
	if !retired || a.projectOrganizer.WatchCancel != nil || a.projectOrganizer.WatchSequence <= 7 {
		t.Fatal("old-root observation survived library replacement")
	}
}

func TestProjectStartRemainsAvailableDuringBackgroundRefresh(t *testing.T) {
	entered, release := make(chan records.ProjectTaskStart, 1), make(chan struct{})
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_task_start" {
			entered <- *r.ProjectTaskStart
			<-release
			return serviceipc.Response{OK: true, Result: records.ProjectTask{ID: "task", Attempt: 1, State: "queued", Snapshot: records.ProjectTaskSnapshot{Project: "project"}}}
		}
		return serviceipc.Response{OK: false}
	})
	a.projectOrganizer.Loading = true // An ordinary periodic read is pending.
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("使用 Codex 整理"); err != nil {
		close(release)
		t.Fatal(err)
	}
	select {
	case d := <-entered:
		if d.Project != "project" || d.BindingRevision != 2 {
			close(release)
			t.Fatal(d)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("background read blocked explicit start")
	}
	close(release)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Task == nil || a.projectOrganizer.Task.ID != "task" {
		t.Fatal("start receipt lost")
	}
}

func TestProjectResetRetiresOldWatchAndStartsFreshReadDuringRefresh(t *testing.T) {
	read := make(chan struct{}, 1)
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "project_binding":
			read <- struct{}{}
			return serviceipc.Response{OK: true, Result: records.ProjectBinding{Project: "project", ConnectionID: "connection", Generation: 1, Revision: 2, Agent: "codex", State: "available"}}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		case "project_task_list":
			return serviceipc.Response{OK: true, Result: records.ProjectTaskPage{Items: []records.ProjectTaskSummary{}}}
		}
		return serviceipc.Response{OK: false}
	})
	retired := false
	a.projectOrganizer.Task = &records.ProjectTask{ID: "old", Attempt: 1, State: "cancelled", Snapshot: records.ProjectTaskSnapshot{Project: "project"}}
	a.projectOrganizer.SelectedTask = "old"
	a.projectOrganizer.Loading = true
	a.projectOrganizer.WatchCancel = func() { retired = true }
	sequence := a.projectOrganizer.Sequence
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("重新整理"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("old pending read prevented the new range read")
	}
	awaitProcessingDispatch(t, dispatched)
	if !retired || a.projectOrganizer.WatchCancel != nil || a.projectOrganizer.SelectedTask != "" || a.projectOrganizer.Sequence <= sequence || a.projectOrganizer.Loading {
		t.Fatal("reset retained the retired watch or refresh")
	}
}

func TestProjectWatchLateObservationCannotRegressNewerRead(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	task := records.ProjectTask{ID: "task", Attempt: 1, State: "organizing", AccessState: "available", Snapshot: records.ProjectTaskSnapshot{Project: "project"}}
	saving := task
	saving.State = "saving"
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_task_watch" {
			close(entered)
			<-release
			return serviceipc.Response{OK: true, Result: records.ProjectTaskObservation{Task: saving, Version: records.ProjectTaskVersion(saving)}}
		}
		return serviceipc.Response{OK: false}
	})
	a.projectOrganizer.SelectedTask, a.projectOrganizer.Task = task.ID, &task
	a.ensureProjectTaskWatch()
	<-entered
	completed := task
	completed.State = "completed"
	a.projectOrganizer.Task = &completed // A newer ordinary Get has arrived.
	close(release)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Task.State != "completed" || a.projectOrganizer.WatchCancel != nil {
		t.Fatal("late saving observation regressed completion")
	}
}

func TestProjectWatchVisibleUpdateInvalidatesPendingRefreshAndHidingCancels(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	task := records.ProjectTask{ID: "task", Attempt: 1, State: "organizing", AccessState: "available", Snapshot: records.ProjectTaskSnapshot{Project: "project"}}
	indexed := task
	indexed.State = "completed"
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "project_task_watch" {
			close(entered)
			<-release
			return serviceipc.Response{OK: true, Result: records.ProjectTaskObservation{Task: indexed, Version: records.ProjectTaskVersion(indexed)}}
		}
		return serviceipc.Response{OK: false}
	})
	a.projectOrganizer.SelectedTask, a.projectOrganizer.Task = task.ID, &task
	sequence := a.projectOrganizer.Sequence
	a.projectOrganizer.Loading = true
	a.ensureProjectTaskWatch()
	<-entered
	close(release)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Task.State != "completed" || a.projectOrganizer.Loading || a.projectOrganizer.Sequence <= sequence {
		t.Fatal("observation did not fence the pending refresh")
	}
	a.projectOrganizer.Task = &task
	a.SetWindowVisible(false)
	a.ensureProjectTaskWatch()
	if a.projectOrganizer.WatchCancel != nil || a.projectOrganizer.Task.ID != task.ID || a.projectOrganizer.Task.State != "organizing" {
		t.Fatal("hidden view observed or cancelled the daemon task")
	}
	select {
	case <-dispatched:
		t.Fatal("hidden view dispatched another watch")
	case <-time.After(20 * time.Millisecond):
	}
}

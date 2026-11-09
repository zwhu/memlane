package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// IPC replies here are controller fault fixtures, not model or runtime evidence.
func projectTaskUIFixture(t *testing.T, handle func(serviceipc.Request) serviceipc.Response) (*App, chan func()) {
	base, dispatched := processingUIFixture(t, handle)
	a := projectEmptyFrame()
	a.openProjectOrganizer(a.scope, "")
	a.library, a.libraryContext, a.dispatch = base.library, base.libraryContext, base.dispatch
	a.projectOrganizer.Client = serviceipc.ProjectTaskClient{Root: base.library.Root}
	a.projectOrganizer.Loaded = true
	a.projectOrganizer.Binding = records.ProjectBinding{Project: a.scope, ConnectionID: "connection", Generation: 1, Revision: 2, Agent: "codex", State: "available"}
	a.startProjectTask = a.beginProjectTask
	return a, dispatched
}

func TestProjectTaskUIStartReopenAndCancelKeepOneTaskAndFrozenScope(t *testing.T) {
	started, release := make(chan records.ProjectTaskStart, 2), make(chan struct{})
	task := records.ProjectTask{ID: "same-task", State: "organizing", Attempt: 3, Snapshot: records.ProjectTaskSnapshot{Project: "project", Executor: records.ProjectTaskExecutor{Agent: "codex", Model: "fixture-model"}}}
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "project_task_start":
			started <- *r.ProjectTaskStart
			<-release
			return serviceipc.Response{OK: true, Result: task}
		case "project_task_cancel":
			value := task
			value.State, value.Cancelled = "cancelling", true
			return serviceipc.Response{OK: true, Result: value}
		}
		return serviceipc.Response{OK: false, Code: "fixture_unexpected"}
	})
	tt := ui.NewTester(a.View, 1200, 820)
	original := a.projectOrganizer.OperationID
	if err := tt.Click("使用 Codex 整理"); err != nil {
		t.Fatal(err)
	}
	draft := <-started
	if draft.OperationID != original || draft.Project != "project" || draft.IncludeHistory || len(draft.Sessions) != 0 {
		t.Fatalf("scope changed: %#v", draft)
	}
	a.beginProjectTask(draft)
	a.closeProjectOrganizer()
	a.openProjectOrganizer("project", "")
	if a.projectOrganizer.OperationID != original || !a.projectOrganizer.Operation.Active {
		t.Fatal("reopen lost pending start identity")
	}
	close(release)
	awaitProcessingDispatch(t, dispatched)
	tt.SetSize(980, 620)
	if a.projectOrganizer.Task.ID != task.ID || a.projectOrganizer.Task.Attempt != 3 || !tt.HasText("停止整理") {
		t.Fatal("reopen did not adopt same task")
	}
	if err := tt.Click("查看知识"); err != nil {
		t.Fatal(err)
	}
	if a.projectOrganizer.Task.Cancelled || a.projectOrganizer.Operation.Kind != "start" {
		t.Fatal("reading cancelled the task")
	}
	// No read is allowed to replace the snapshot in this fixture; preserve it
	// while the refresh reply fails, then cancellation must still target it.
	a.openProjectOrganizer("project", task.ID)
	awaitProcessingDispatch(t, dispatched)
	tt.SetSize(980, 620)
	if err := tt.Click("停止整理"); err != nil {
		t.Fatal(err)
	}
	awaitProcessingDispatch(t, dispatched)
	tt.SetSize(1200, 820)
	if a.projectOrganizer.Task.State != "cancelling" || a.projectOrganizer.Task.Attempt != 3 || !tt.HasText("停止整理中…") {
		t.Fatal("cancel did not adopt daemon reply")
	}
	if _, animate := a.projectOrganizer.Motion.sample(time.Now().Add(time.Hour), false); animate {
		t.Fatal("cancelling still animates")
	}
	select {
	case <-started:
		t.Fatal("navigation started another task")
	default:
	}
}

func TestProjectTaskUITerminalResetUsesNewIntentAndMinimumActionsFit(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			a := projectEmptyFrame()
			a.openProjectOrganizer(a.scope, "task")
			a.projectOrganizer.Loaded = true
			a.projectOrganizer.Task = &records.ProjectTask{ID: "task", Attempt: 1, State: state, Snapshot: records.ProjectTaskSnapshot{Project: a.scope, Executor: records.ProjectTaskExecutor{Agent: "codex"}}}
			before := a.projectOrganizer.OperationID
			tt := ui.NewTester(a.View, 980, 620)
			labels := []string{"重新整理", "查看知识"}
			if state == "completed" {
				labels = []string{"查看项目知识"}
			}
			for _, label := range labels {
				node, ok := tt.Find(label)
				if !ok || node.X < 0 || node.Y < 0 || node.X+node.W > 980 || node.Y+node.H > 620 {
					t.Fatalf("fixed action hidden: %s %#v", label, node)
				}
			}
			if state == "completed" {
				if err := tt.Click("查看项目知识"); err != nil {
					t.Fatal(err)
				}
				if a.projectOrganizer.Open || a.projectOrganizer.Task.ID != "task" {
					t.Fatal("reading completion lost task")
				}
				return
			}
			if err := tt.Click("重新整理"); err != nil {
				t.Fatal(err)
			}
			if a.projectOrganizer.Task != nil || a.projectOrganizer.SelectedTask != "" || a.projectOrganizer.OperationID == before || a.projectOrganizer.IncludeHistory {
				t.Fatal("new intent reused previous task or scope")
			}
		})
	}
}

func TestProjectMotionPhaseInterruptionAndReducedMotion(t *testing.T) {
	now := time.Now()
	m := projectStageMotion{}
	m.update("task", 1, "ready", now)
	m.update("task", 1, "starting", now)
	current, _ := m.sample(now.Add(400*time.Millisecond), false)
	m.update("task", 1, "saving", now.Add(400*time.Millisecond))
	continued, _ := m.sample(now.Add(400*time.Millisecond), false)
	if current.Tiles != continued.Tiles {
		t.Fatal("fast phase switch jumped to new start")
	}
	changed := m.Changed
	m.update("task", 1, "saving", now.Add(time.Second))
	if m.Changed != changed {
		t.Fatal("same snapshot restarted animation")
	}
	for _, state := range []string{"ready", "starting", "organizing", "saving", "indexing", "completed", "failed", "cancelled", "cancelling", "interrupted"} {
		m.update("task", 1, state, now.Add(2*time.Second))
		reduced, animate := m.sample(now.Add(3*time.Second), true)
		if animate || reduced != projectStageTarget(state) {
			t.Fatalf("reduced phase %s moves", state)
		}
		if state == "failed" || state == "cancelled" || state == "cancelling" || state == "interrupted" {
			pose, moving := m.sample(now.Add(3*time.Second), false)
			if moving || pose.CheckAlpha != 0 || pose.BookAlpha != 0 {
				t.Fatalf("terminal %s shows completion", state)
			}
		}
	}
	m.update("task", 1, "completed", now)
	m.Reenter = true
	m.update("task", 1, "completed", now.Add(time.Second))
	p, moving := m.sample(now.Add(time.Second), false)
	if moving || p.BookAlpha != 1 || p.CheckAlpha != 1 {
		t.Fatal("reopening completion replays work")
	}
}

func TestProjectTaskUILateStartReceiptCannotCrossReconfiguredLibrary(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		close(reached)
		<-release
		return serviceipc.Response{OK: true, Result: records.ProjectTask{ID: "old-task", State: "organizing", Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Project: "project"}}}
	})
	a.beginProjectTask(records.ProjectTaskStart{Project: a.scope, BindingRevision: 2, OperationID: a.projectOrganizer.OperationID})
	<-reached
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nextRoot := t.TempDir()
	a.ConfigureLibrary(ctx, nextRoot, func(fn func()) { dispatched <- fn })
	close(release)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Task != nil || a.projectOrganizer.Operation != nil || a.projectOrganizer.SelectedTask != "" || len(a.projectOverviews) != 0 || a.projectOrganizer.Client.Root != nextRoot {
		t.Fatal("old start receipt mutated the reconfigured library")
	}
}

func TestProjectTaskUIStartFailureSurvivesPollUntilExplicitRecheck(t *testing.T) {
	drafts := make(chan records.ProjectTaskStart, 2)
	a, dispatched := projectTaskUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "project_task_start":
			drafts <- *r.ProjectTaskStart
			return serviceipc.Response{OK: false, Code: "service_error", Error: "startup failure fixture"}
		case "project_binding":
			return serviceipc.Response{OK: true, Result: records.ProjectBinding{Project: "project", ConnectionID: "connection", Generation: 1, Revision: 2, Agent: "codex", State: "available"}}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		case "project_task_list":
			return serviceipc.Response{OK: true, Result: records.ProjectTaskPage{Items: []records.ProjectTaskSummary{}}}
		}
		return serviceipc.Response{OK: false, Code: "unexpected_fixture"}
	})
	d := records.ProjectTaskStart{Project: "project", BindingRevision: 2, OperationID: a.projectOrganizer.OperationID}
	a.beginProjectTask(d)
	first := <-drafts
	awaitProcessingDispatch(t, dispatched)
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Error == "" || !a.projectOrganizer.Loaded || a.projectOrganizer.Task != nil {
		t.Fatal("poll hid a failed start")
	}
	a.beginProjectTask(d)
	select {
	case <-drafts:
		t.Fatal("unconfirmed start retried without recheck")
	default:
	}
	a.recheckProjectOrganizer()
	awaitProcessingDispatch(t, dispatched)
	if a.projectOrganizer.Error != "" || a.projectOrganizer.OperationID != first.OperationID {
		t.Fatal("explicit recheck changed the stable intent")
	}
	a.beginProjectTask(d)
	second := <-drafts
	if second.OperationID != first.OperationID {
		t.Fatal("retry created a duplicate intent")
	}
	awaitProcessingDispatch(t, dispatched)
	awaitProcessingDispatch(t, dispatched)
}

func TestProjectTaskUICompletedReceiptFitsAboveFixedAction(t *testing.T) {
	a := projectEmptyFrame()
	a.openProjectOrganizer(a.scope, "task")
	a.projectOrganizer.Loaded = true
	a.projectOrganizer.Task = &records.ProjectTask{ID: "task", Attempt: 1, State: "completed", Snapshot: records.ProjectTaskSnapshot{Project: a.scope, Executor: records.ProjectTaskExecutor{Agent: "codex", Model: "fixture-model"}}, Receipt: &records.ProjectTaskReceipt{Persisted: true, Added: []records.ProjectTaskSaved{{Memory: "one"}, {Memory: "two"}}, Indexed: 2}}
	for _, size := range [][2]int{{1200, 820}, {980, 620}} {
		tt := ui.NewTester(a.View, size[0], size[1])
		result, ok := tt.Find("已保存 2 条知识 · 2 条已建立索引")
		action, present := tt.Find("查看项目知识")
		if !ok || !present || result.Y+result.H > action.Y {
			t.Fatalf("receipt obscured by fixed action at %v: %#v %#v", size, result, action)
		}
	}
}

func TestProjectTaskUISupplementDoesNotRestoreOldStartAcknowledgement(t *testing.T) {
	a := sourceReadingFrame()
	a.closeSourceReader()
	a.openProjectOrganizer(a.scope, "")
	a.projectOrganizer.Task = &records.ProjectTask{ID: "finished-task", Attempt: 1, State: "completed", Snapshot: records.ProjectTaskSnapshot{Project: a.scope}}
	a.projectOrganizer.Operation = &ProjectBindingOperation{Project: a.scope, Kind: "start", Task: "finished-task", Result: &records.ProjectTask{ID: "finished-task", Attempt: 1, State: "queued", Snapshot: records.ProjectTaskSnapshot{Project: a.scope}}}
	prior := a.projectOrganizer.OperationID
	a.openProjectOrganizer(a.scope, "")
	if a.projectOrganizer.SelectedTask != "" || a.projectOrganizer.Task != nil || a.projectOrganizer.OperationID == prior {
		t.Fatal("supplement reused the old queued acknowledgement instead of reading active tasks")
	}
}

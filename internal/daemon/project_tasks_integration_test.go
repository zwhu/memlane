package daemon

// Actual SQLite/Git/process ownership; the Agent protocol is an explicit
// synthetic fixture. These tests do not claim a real model or Native window.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func projectDaemonFixture(t *testing.T) (*Server, records.ProjectTaskStart) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if b, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: db, lifecycle: ctx, stop: cancel, projectRuns: map[string]projectTaskOwner{}, work: make(chan struct{}, 1)}
	t.Cleanup(func() { cancel(); s.projectWG.Wait(); db.Close() })
	p, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "C owner fixture", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "fixture", Project: p.ID, AllowedScopes: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TouchConnection(ctx, c.ID, c.Generation); err != nil {
		t.Fatal(err)
	}
	b, err := db.ChangeProjectBinding(ctx, records.ProjectBindingChange{Project: p.ID, ConnectionID: c.ID, ExpectedGeneration: c.Generation})
	if err != nil {
		t.Fatal(err)
	}
	executor := records.ProjectTaskExecutor{ConnectionID: c.ID, Generation: c.Generation, Agent: "codex", Version: "fixture", Model: "fixture-current-model", ConfigurationDigest: hex.EncodeToString(make([]byte, 32))}
	s.projectResolve = func(context.Context, string) (agents.CodexProjectPlan, error) {
		return agents.CodexProjectPlan{Executor: executor}, nil
	}
	return s, records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}
}

func projectProcessFixture(ctx context.Context, task records.ProjectTask, onStart func(int) error, onEvent func(string), wait bool) (agents.CodexProjectExecution, error) {
	cmd := exec.Command("/bin/sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return agents.CodexProjectExecution{}, err
	}
	out := agents.CodexProjectExecution{ProposalProcess: agents.ProposalProcess{PID: cmd.Process.Pid, Started: true, StartedMS: time.Now().UnixMilli()}, SelectedModel: task.Snapshot.Executor.Model, SelectedProvider: "fixture", Thread: "fixture-thread", Turn: "fixture-turn", PermissionProfile: "memlane_project"}
	err := onStart(cmd.Process.Pid)
	if err == nil {
		onEvent("organizing")
	}
	if wait && err == nil {
		<-ctx.Done()
		err = ctx.Err()
	}
	cmd.Process.Kill()
	cmd.Wait()
	out.Reaped = true
	out.GroupGone = syscall.Kill(-out.PID, 0) == syscall.ESRCH
	out.EndedMS = time.Now().UnixMilli()
	if err != nil {
		out.Cancelled = true
		return out, err
	}
	text := "C owner synthetic repository rule"
	digest := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(digest[:])
	sourceID := sha256.Sum256([]byte("README.md\x00" + hash))
	source := records.ProjectRepositorySource{ID: hex.EncodeToString(sourceID[:]), Path: "README.md", SHA256: hash, Text: text}
	out.Terminal = true
	out.Sources = []records.ProjectRepositorySource{source}
	out.Proposal = &records.ProjectKnowledgeProposal{Task: task.ID, Attempt: task.Attempt, Explanation: "explicit fixture", Items: []records.ProjectKnowledgeItem{{Title: "C owner fixture", Text: text, Sources: []records.ProjectKnowledgeQuote{{Source: source.ID, Quote: text}}}}}
	return out, nil
}

func TestProjectDaemonOwnerReplayCloseViewAndIndexReceipts(t *testing.T) {
	s, d := projectDaemonFixture(t)
	var launches atomic.Int32
	s.projectExecute = func(ctx context.Context, p agents.CodexProjectPlan, t records.ProjectTask, onStart func(int) error, onEvent func(string)) (agents.CodexProjectExecution, error) {
		launches.Add(1)
		return projectProcessFixture(ctx, t, onStart, onEvent, false)
	}
	task, err := s.startProjectTask(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.startProjectTask(context.Background(), d)
	if err != nil || replay.ID != task.ID {
		t.Fatal(replay, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, e := s.store.ProjectTask(context.Background(), task.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.Receipt != nil && current.State == "indexing" {
			// The model receipt has ended, while the task still waits on actual
			// index jobs. Repeated Get is a view operation and never launches.
			if current.State != "indexing" || current.Execution == nil || !current.Execution.GroupGone {
				t.Fatal(current)
			}
			for _, saved := range append(append([]records.ProjectTaskSaved(nil), current.Receipt.Added...), current.Receipt.Sources...) {
				v, e := s.store.Current(context.Background(), saved.Memory)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.store.MarkIndexed(context.Background(), v.Record, engine.Receipt{Generation: "explicit-index-fixture", ChunkIDs: []string{uuid.NewString()}}); e != nil {
					t.Fatal(e)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner did not save")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.projectWG.Wait()
	done, err := s.store.ProjectTask(context.Background(), task.ID)
	if err != nil || done.State != "completed" || launches.Load() != 1 {
		t.Fatal(done, launches.Load(), err)
	}
}

func TestProjectDaemonActiveCancelAndRestartDoesNotSignalLivePID(t *testing.T) {
	s, d := projectDaemonFixture(t)
	s.projectExecute = func(ctx context.Context, p agents.CodexProjectPlan, t records.ProjectTask, onStart func(int) error, onEvent func(string)) (agents.CodexProjectExecution, error) {
		return projectProcessFixture(ctx, t, onStart, onEvent, true)
	}
	task, err := s.startProjectTask(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var pid int
		err = s.store.DB.QueryRow(`SELECT process_pid FROM project_task_runs WHERE task=?`, task.ID).Scan(&pid)
		if err == nil && pid > 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no process registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = prepareProjectRuns(context.Background(), s.store); err == nil {
		t.Fatal("startup accepted a live previous group")
	}
	if _, err = s.cancelProjectTask(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	s.projectWG.Wait()
	done, err := s.store.ProjectTask(context.Background(), task.ID)
	if err != nil || done.State != "cancelled" || done.Execution == nil || !done.Execution.GroupGone || done.Receipt != nil {
		t.Fatal(done, err)
	}
	if err = prepareProjectRuns(context.Background(), s.store); err != nil {
		t.Fatal(err)
	}
	var reaped bool
	if err = s.store.DB.QueryRow(`SELECT json_extract(execution,'$.process_reaped') FROM project_task_runs WHERE task=?`, task.ID).Scan(&reaped); err != nil || !reaped {
		t.Fatal("startup destroyed the original process receipt", err)
	}
}

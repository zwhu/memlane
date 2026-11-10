package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestOrganizationPrivateIPCGate(t *testing.T) {
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root, RetryDelayMS: 10})
	defer stop()
	for _, op := range []string{"organization_start", "organization_resume", "organization_cancel"} {
		e := serviceipc.Call(context.Background(), root, serviceipc.Request{Op: op}, nil)
		if a, ok := e.(*serviceipc.ServiceError); !ok || a.Code != "confirmation_required" {
			t.Fatalf("missing host gate %s: %v", op, e)
		}
	}
	for _, op := range []string{"organization_start", "organization_resume"} {
		e := serviceipc.Call(context.Background(), root, serviceipc.Request{Op: op, UserConfirmed: true}, nil)
		if a, ok := e.(*serviceipc.ServiceError); !ok || a.Code != "invalid_request" {
			t.Fatalf("nil payload %s: %v", op, e)
		}
	}
	e := serviceipc.Call(context.Background(), root, serviceipc.Request{Op: "agent", AgentRequest: records.AgentRequest{Protocol: 1, Tool: "organization_start"}}, nil)
	if e == nil {
		t.Fatal("daily Agent exposed organizer")
	}
	c := serviceipc.OrganizationClient{Root: root}
	_, e = c.Start(context.Background(), records.OrganizationStart{OperationID: uuid.NewString(), Project: uuid.NewString(), Inputs: []records.MergeTarget{{Memory: uuid.NewString(), Revision: 1}}})
	if a, ok := e.(*serviceipc.ServiceError); !ok || a.Code != "scope_denied" {
		t.Fatal(e)
	}
	page, e := c.List(context.Background(), records.OrganizationListQuery{})
	requireService(t, e)
	if len(page.Items) != 0 {
		t.Fatal(page)
	}
}
func TestOrganizationRealServiceTraexAndRust(t *testing.T) {
	if os.Getenv("MEMLANE_C3_REAL") != "1" {
		t.Skip("requires explicit real traex/Rust opt-in")
	}
	var worker engine.Config
	requireService(t, json.Unmarshal([]byte(os.Getenv("MEMLANE_P3_ENGINE_CONFIG")), &worker))
	root := t.TempDir()
	worker.DataDir = filepath.Join(root, "engine")
	config := daemon.Config{Root: root, Worker: worker, RetryDelayMS: 50}
	stop := startService(t, config)
	repo := filepath.Join(root, "repo")
	requireService(t, os.Mkdir(repo, 0700))
	requireService(t, exec.Command("git", "init", "-q", repo).Run())
	var project records.Scope
	requireService(t, serviceipc.Call(context.Background(), root, serviceipc.Request{Op: "register_project", UserConfirmed: true, Project: records.ScopeDraft{Name: "C3 synthetic", Path: repo}}, &project))
	old := serviceInputs(t, root, project.ID)
	d := records.OrganizationStart{OperationID: old.OperationID, Project: old.Project, Inputs: old.Inputs}
	c := serviceipc.OrganizationClient{Root: root}
	began := time.Now()
	task, e := c.Start(context.Background(), d)
	requireService(t, e)
	if time.Since(began) > 2*time.Second {
		t.Fatal("start waited on model")
	}
	replay, e := c.Start(context.Background(), d)
	requireService(t, e)
	if replay.ID != task.ID {
		t.Fatal("unstable task ID")
	}
	waitService(t, 150*time.Second, func() bool {
		task, e = c.Get(context.Background(), task.ID)
		requireService(t, e)
		return task.State != "queued" && task.State != "running" && task.State != "applying"
	})
	if task.State != "completed" || task.Result == nil {
		t.Fatalf("real organization failed state=%s error=%s execution=%s", task.State, task.Error, task.Execution)
	}
	var process agents.ProposalProcess
	requireService(t, json.Unmarshal(task.Execution, &process))
	if !process.Terminal || !process.Reaped || !process.GroupGone || process.ExitCode != 0 || len(process.ItemErrors) > 0 || process.Events["error"] > 0 || len(process.Items) != 1 {
		t.Fatalf("unclean process %s", task.Execution)
	}
	summaries, tags := 0, 0
	for _, entry := range task.Entries {
		if entry.State != "applied" || entry.Receipt == nil {
			t.Fatal("item not atomically applied")
		}
		if entry.Item.Action == "summary" {
			summaries++
			if strings.Contains(entry.Item.Text, "覆盖其他项目") || strings.Contains(entry.Item.Text, "读取全部聊天") {
				t.Fatal("embedded instruction became knowledge")
			}
			waitService(t, 60*time.Second, func() bool { return readService(t, root, entry.Receipt.Memory).IndexState == "indexed" })
			var hits []struct {
				Version records.Version `json:"version"`
			}
			requireService(t, serviceipc.Call(context.Background(), root, serviceipc.Request{Op: "search", Datasets: []string{project.ID}, Query: "pnpm 项目依赖约定", Limit: 30}, &hits))
			found := false
			for _, hit := range hits {
				if hit.Version.Memory == entry.Receipt.Memory && hit.Version.Revision == 1 {
					found = true
				}
			}
			if !found {
				t.Fatal("real Rust did not return summary")
			}
		} else {
			tags += len(entry.Receipt.AddedTags)
		}
	}
	if summaries == 0 || tags == 0 {
		t.Fatalf("real output did not exercise summary and tag: %d/%d", summaries, tags)
	}
	for _, ref := range d.Inputs {
		v := readService(t, root, ref.Memory)
		if v.Revision != 1 || v.State != "active" {
			t.Fatal("automatic task rewrote source")
		}
	}
	cancelInputs := serviceInputs(t, root, project.ID)
	cancelled, e := c.Start(context.Background(), records.OrganizationStart{OperationID: cancelInputs.OperationID, Project: project.ID, Inputs: cancelInputs.Inputs})
	requireService(t, e)
	waitService(t, 40*time.Second, func() bool {
		v, e := c.Get(context.Background(), cancelled.ID)
		requireService(t, e)
		var p struct {
			Active bool `json:"process_active"`
		}
		_ = json.Unmarshal(v.Execution, &p)
		return p.Active
	})
	_, e = c.Cancel(context.Background(), cancelled.ID)
	requireService(t, e)
	var cancelledProcess agents.ProposalProcess
	waitService(t, 20*time.Second, func() bool {
		v, e := c.Get(context.Background(), cancelled.ID)
		requireService(t, e)
		_ = json.Unmarshal(v.Execution, &cancelledProcess)
		return v.State == "cancelled" && cancelledProcess.Reaped && cancelledProcess.GroupGone
	})
	for _, ref := range cancelInputs.Inputs {
		v := readService(t, root, ref.Memory)
		if v.Revision != 1 {
			t.Fatal("cancel changed source")
		}
	}
	stop()
	evidence := map[string]any{"status": "passed", "real_traex": true, "real_rust": true, "typed_private_ipc": true, "sources_unchanged": true, "summary_count": summaries, "new_tag_count": tags, "real_cancel": cancelledProcess, "normal_process": process, "schema": 4}
	b, _ := json.Marshal(evidence)
	t.Log("ORGANIZATION_REAL_EVIDENCE " + string(b))
}

package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func TestOrganizationProgressEvidenceAndReturn(t *testing.T) {
	a, task := materialUIFixture()
	task.Attempt = 2
	a.organization.Task = &task
	a.organization.ReviewItem = "merge"
	a.organization.Scroll.Y = 137
	a.organization.Focus = "original-focus"
	a.openOrganizationProgress()
	p := a.organization.Progress
	if !a.organizationProgressOpen() || p.Task.ID != task.ID || p.Task.Attempt != 2 {
		t.Fatal("attempt identity lost")
	}
	a.organization.Task.Attempt = 3
	if p.Task.Attempt != 2 {
		t.Fatal("snapshot alias")
	}
	a.closeOrganizationProgress()
	if a.organization.ReviewItem != "merge" || a.organization.Scroll.Y != 137 || !a.organization.RestoreFocus {
		t.Fatal("return changed review context")
	}
	a.openOrganizationProgress()
	a.libraryEpoch++
	if a.organizationProgressOpen() {
		t.Fatal("old epoch remained visible")
	}
	tsk := records.OrganizationTask{State: "completed", Entries: []records.OrganizationEntry{{State: "applied", Receipt: &records.OrganizationReceipt{Memory: "x", Revision: 1}}}}
	text := strings.Join(organizationProgressStages(tsk, map[string]MemoryItem{"x": {Revision: 2, IndexState: "indexed"}}), "\n")
	if !strings.Contains(text, "0/1") || !strings.Contains(text, "待核对") {
		t.Fatal(text)
	}
}
func TestOrganizationProgressFrames(t *testing.T) {
	for _, w := range []int{1200, 800} {
		a, task := materialUIFixture()
		a.guidanceRoot = t.TempDir()
		task.Attempt = 1
		a.scopes = []records.Scope{{ID: task.Project, Kind: "project", Name: "记忆桌面端"}}
		a.projects = []ProjectItem{{ID: task.Project, Name: "记忆桌面端"}}
		a.organization.ProjectLabel = "记忆桌面端"
		a.organization.TasksLoaded = true
		a.organization.TasksTotal = 1
		a.organization.Tasks = []records.OrganizationSummary{{ID: task.ID, Project: task.Project, State: task.State, Title: "项目知识整理", Origin: "manual"}}
		task.Snapshot.Processing = &records.ProcessingSnapshot{Revision: records.ProcessingRevision{Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "layout-fixture-only"}}}
		a.organization.Task = &task
		a.openOrganizationProgress()
		tt := ui.NewTester(a.View, w, 760)
		writeScreenshot(t, tt, fmt.Sprintf("progress-%d.png", w))
		a.closeOrganizationProgress()
		tt.Frame()
	}
}

func TestOrganizationProgressEntryDoesNotStart(t *testing.T) {
	a, task := materialUIFixture()
	a.guidanceRoot = t.TempDir()
	a.organization.ReviewItem = "merge"
	a.organization.Scroll.Y = 91
	tt := ui.NewTester(a.View, 800, 760)
	if err := tt.Click("查看进度"); err != nil {
		t.Fatal(err)
	}
	if !a.organizationProgressOpen() || a.organization.Start != nil || a.organization.Resume != nil || a.organization.Busy {
		t.Fatal("view mutated task lifecycle")
	}
	if err := tt.Click("返回建议审核"); err != nil {
		t.Fatal(err)
	}
	if a.organization.ReviewItem != "merge" || a.organization.Task.ID != task.ID {
		t.Fatal("review selection lost")
	}
}
func TestOrganizationProgressTerminalEvidence(t *testing.T) {
	for _, state := range []string{"running", "cancelled", "failed", "interrupted"} {
		tsk := records.OrganizationTask{State: state}
		text := strings.Join(organizationProgressStages(tsk, nil), " ")
		if strings.Contains(text, "已取得整理结果") {
			t.Fatal(state + ": invented result proof")
		}
	}
}

func TestOrganizationProgressFollowsExistingRefresh(t *testing.T) {
	a, task := materialUIFixture()
	task.Attempt = 1
	task.State = "running"
	a.organization.Task = &task
	a.openOrganizationProgress()
	p := a.organization.Progress
	p.Scroll.Y = 73
	a.organization.ReviewItem = "merge"
	a.organization.Scroll.Y = 144
	updated := task
	updated.State = "failed"
	a.adoptOrganizationProgress(p, p.Root, p.Epoch, updated, nil)
	if p.Task.State != "failed" || p.Scroll.Y != 73 || a.organization.Scroll.Y != 144 || a.organization.ReviewItem != "merge" {
		t.Fatal("same attempt refresh did not preserve view")
	}
	if _, active := organizationProgressMotionStage(p); active {
		t.Fatal("failed motion active")
	}
	updated.Attempt = 2
	updated.State = "running"
	a.adoptOrganizationProgress(p, p.Root, p.Epoch, updated, nil)
	if p.Task.Attempt != 1 || p.LatestAttempt != 2 {
		t.Fatal("new attempt replaced selected attempt")
	}
	if _, active := organizationProgressMotionStage(p); active {
		t.Fatal("new attempt kept old motion active")
	}
	p.LatestAttempt = 0
	p.Task.State = "running"
	p.Task.Cancelled = true
	if _, active := organizationProgressMotionStage(p); active {
		t.Fatal("cancel requested motion active")
	}
	p.Task.Cancelled = false
	a.adoptOrganizationProgress(p, "different-root", p.Epoch, task, nil)
	if p.Task.State != "running" {
		t.Fatal("wrong root adopted")
	}
	old := p
	a.openOrganizationProgress()
	a.adoptOrganizationProgress(old, old.Root, old.Epoch, updated, nil)
	if a.organization.Progress.LatestAttempt != 0 {
		t.Fatal("retired pointer adopted")
	}
}

func TestOrganizationProgressDeletionAndPartialFailure(t *testing.T) {
	p := &OrganizationProgressUI{Task: records.OrganizationTask{State: "partial_failed"}}
	if stage, active := organizationProgressMotionStage(p); stage != "failed" || active {
		t.Fatal("partial failure must be dimmed static")
	}
	p.Task.Entries = []records.OrganizationEntry{{State: "applied", Item: records.OrganizationItem{Action: "delete"}, Receipt: &records.OrganizationReceipt{Memory: "deleted", Revision: 2}}}
	text := strings.Join(organizationProgressStages(p.Task, map[string]MemoryItem{"deleted": {Revision: 2, State: "deleted", IndexState: "deleted"}}), " ")
	if !strings.Contains(text, "已保存 0 项") || !strings.Contains(text, "已删除 1 项") || !strings.Contains(text, "删除项不需要建立索引") {
		t.Fatal(text)
	}
	p.Task.Entries = append(p.Task.Entries, records.OrganizationEntry{State: "applied", Item: records.OrganizationItem{Action: "summary"}, Receipt: &records.OrganizationReceipt{Memory: "knowledge", Revision: 1}})
	text = strings.Join(organizationProgressStages(p.Task, map[string]MemoryItem{"knowledge": {Revision: 2, State: "active", IndexState: "indexed"}}), " ")
	if !strings.Contains(text, "当前版本已索引 0/1 项") || !strings.Contains(text, "待核对") {
		t.Fatal(text)
	}
}

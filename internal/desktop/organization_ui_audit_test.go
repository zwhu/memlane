package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
	"time"
)

func TestOrganizationReadOnlyAuditFrames(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a, task := materialUIFixture()
		a.guidanceRoot = t.TempDir()
		a.scopes = []records.Scope{{ID: "project", Name: "记忆桌面端", Kind: "project", Path: "/synthetic/project"}}
		a.projects = []ProjectItem{{ID: "project", Name: "记忆桌面端", Path: "/synthetic/project"}}
		spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "fixture-provider", Model: "layout-fixture-only"}
		revision := records.ProcessingRevision{ID: "fixture-processing", Spec: spec, Validation: &records.ProcessingValidation{State: "succeeded"}}
		task.CreatedAt = time.Now().Format(time.RFC3339)
		task.Attempt = 1
		task.Snapshot.Project = task.Project
		task.Snapshot.Task = task.ID
		task.Snapshot.Processing = &records.ProcessingSnapshot{Revision: revision, SelectionGeneration: 1}
		for i := range task.Snapshot.Inputs {
			task.Snapshot.Inputs[i].Source = "pi · 排布夹具"
			task.Snapshot.Inputs[i].Origin = "knowledge"
			task.Snapshot.Inputs[i].Text = []string{"项目统一使用 pnpm 管理依赖，依赖变更与锁文件一起提交。", "依赖修改与 pnpm-lock.yaml 同时提交，保证环境一致。"}[i]
		}
		task.Entries[1].Item.Text = "项目统一使用 pnpm，依赖变更与 pnpm-lock.yaml 一起提交。"
		task.Entries[1].Item.Title = "合并两条依赖管理约定"
		for i := range task.Entries[1].Item.Sources {
			task.Entries[1].Item.Sources[i].Quote = task.Snapshot.Inputs[i].Text
		}
		a.organization.Task = &task
		a.organization.Tasks = []records.OrganizationSummary{{ID: task.ID, Project: task.Project, State: "completed", Title: "项目知识整理", Origin: "manual", CreatedAt: task.CreatedAt, Processing: &spec}}
		a.organization.TasksLoaded = true
		a.organization.TasksTotal = 1
		a.organization.Results = map[string]MemoryItem{"summary": {ID: "summary", Dataset: "project", Title: "依赖摘要", Text: "项目依赖管理约定的独立摘要。", Revision: 1, Origin: "knowledge", State: "active"}}
		a.organization.Sources = map[string]MemoryItem{}
		for _, in := range task.Snapshot.Inputs {
			a.organization.Sources[in.Memory] = MemoryItem{ID: in.Memory, Dataset: "project", Title: in.Title, Text: in.Text, Source: in.Source, ScopeName: "记忆桌面端", Revision: in.Revision, Origin: in.Origin, State: "active"}
		}
		tt := ui.NewTester(a.View, width, 760)
		writeScreenshot(t, tt, fmt.Sprintf("result-%d.png", width))
		a.openOrganizationEntry("merge")
		tt.Frame()
		writeScreenshot(t, tt, fmt.Sprintf("review-%d.png", width))
		a.showOrganizationResult()
		a.organization.New = true
		a.organization.Project = "project"
		a.organization.ProjectLabel = "记忆桌面端"
		a.organization.ProcessingLoaded = true
		a.organization.Processing.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &revision}
		a.organization.ReviewMode = false
		a.organization.Candidates = nil
		a.organization.Chosen = map[string]MemoryItem{}
		a.organization.Order = nil
		for _, in := range task.Snapshot.Inputs {
			v := a.organization.Sources[in.Memory]
			a.organization.Candidates = append(a.organization.Candidates, v)
			a.organization.Chosen[v.ID] = v
			a.organization.Order = append(a.organization.Order, v.ID)
		}
		tt.Frame()
		writeScreenshot(t, tt, fmt.Sprintf("start-%d.png", width))
	}
}

func TestOrganizationAuditFrozenSourceAndVisibleProjectCaption(t *testing.T) {
	a, task := materialUIFixture()
	a.scopes = []records.Scope{{ID: "project", Name: "原项目", Kind: "project"}}
	task.Snapshot.Inputs[0].Source = "Codex · 冻结来源说明"
	task.Snapshot.Inputs[1].Source = ""
	a.organization.Task = &task
	a.organization.Sources["one"] = MemoryItem{ID: "one", Dataset: "project", State: "active", Revision: 1, Source: "当前已修改的来源说明"}
	a.openOrganizationEntry("merge")
	tt := ui.NewTester(a.View, 800, 640)
	if !tt.HasText("原项目 · Codex · 冻结来源说明") || !tt.HasText("原项目 · 来源未记录") || tt.HasText("原项目 · 当前已修改的来源说明") {
		t.Fatal("frozen source was omitted, inferred or replaced by current metadata")
	}
	if task.Snapshot.Inputs[0].Source != "Codex · 冻结来源说明" || task.Snapshot.Inputs[1].Source != "" {
		t.Fatal("display mutated task evidence")
	}
	a.showOrganizationResult()
	a.organization.New = true
	a.organization.Project = "project"
	a.organization.ProjectLabel = "原项目"
	tt.Frame()
	if !tt.HasText("本次项目") {
		t.Fatal("project selection lacks visible caption")
	}
}

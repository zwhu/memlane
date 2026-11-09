package desktop

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func projectEmptyFrame() *App {
	a := sourceReadingFrame()
	a.closeSourceReader()
	a.items, a.selectedItem, a.selectedID = nil, nil, ""
	zero := 0
	a.readingKnowledgeTotal, a.readingRawTotal = &zero, &zero
	a.readingCountsRoute = "项目:project"
	return a
}

func TestProjectOrganizerReconfigurationClearsOldScopeAndWriteOperation(t *testing.T) {
	a := projectEmptyFrame()
	a.openProjectOrganizer(a.scope, "old-task")
	a.projectOrganizer.Operation = &ProjectBindingOperation{Project: a.scope, Active: true}
	a.projectOverviews = map[string]*ProjectOverview{a.scope: {Ready: true, Task: "old-task"}}
	sequence := a.projectOrganizer.Sequence
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ConfigureLibrary(ctx, t.TempDir(), func(f func()) { f() })
	if a.projectOrganizer.Open || a.projectOrganizer.Operation != nil || a.projectOrganizer.SelectedTask != "" || a.projectOrganizer.Sequence <= sequence || len(a.projectOverviews) != 0 {
		t.Fatal("reconfigured library retained an old project or pending write")
	}
}

func TestProjectOrganizerEmptyEntryUsesCurrentUnfilteredKnowledgeCounts(t *testing.T) {
	cases := []struct {
		name   string
		change func(*App)
	}{
		{"other-scope", func(a *App) { a.readingCountsRoute = "项目:other" }},
		{"loading", func(a *App) { a.loading = true }},
		{"failed", func(a *App) { a.libraryError = "读取失败" }},
		{"filter", func(a *App) { a.readingFilter = "没有匹配项" }},
		{"search", func(a *App) { a.readingSearch = "不存在" }},
		{"original-tab", func(a *App) { a.recordKind = "raw" }},
		{"unknown-count", func(a *App) { a.readingRawTotal = nil }},
		{"knowledge-exists", func(a *App) { value := 1; a.readingKnowledgeTotal = &value }},
	}
	for _, sample := range cases {
		t.Run(sample.name, func(t *testing.T) {
			a := projectEmptyFrame()
			sample.change(a)
			tt := ui.NewTester(a.View, 1200, 820)
			if tt.HasText("整理项目") {
				t.Fatal("unknown, filtered or nonempty scope exposed empty-project start")
			}
		})
	}
	a := projectEmptyFrame()
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("整理项目") {
		t.Fatal("actual empty knowledge scope omitted entry")
	}
	if err := tt.Click("整理项目"); err != nil {
		t.Fatal(err)
	}
	if !a.projectOrganizer.Open || a.projectOrganizer.Project != a.scope || a.projectOrganizer.IncludeHistory || !tt.HasText("项目知识整理") {
		t.Fatal("entry started work or failed to open the scoped right pane")
	}
	if err := tt.Click("查看知识"); err != nil {
		t.Fatal(err)
	}
	if a.projectOrganizer.Open || !tt.Focused("整理项目") {
		t.Fatal("return lost empty-entry focus")
	}
}

func TestProjectOrganizerKeepsExistingKnowledgeAndDoesNotStartOnSelection(t *testing.T) {
	a := sourceReadingFrame()
	a.closeSourceReader()
	a.detailScroll.Y = 80
	a.openProjectOrganizer(a.scope, "")
	a.projectOrganizer.Loaded = true
	a.projectOrganizer.Binding = records.ProjectBinding{Project: a.scope, ConnectionID: "connection", Generation: 1, Revision: 1, Agent: "codex", State: "available"}
	tt := ui.NewTester(a.View, 980, 620)
	if !tt.HasText("另一条知识") || !tt.HasText("项目知识整理") || !tt.HasText("会消耗 Token") {
		t.Fatal("configuration replaced the knowledge list or omitted scope/cost")
	}
	_ = tt.Click("使用 Codex 整理")
	if a.projectOrganizer.Task != nil || a.projectOrganizer.IncludeHistory {
		t.Fatal("unattached task controller simulated a task or selected history")
	}
	if err := tt.Click("查看知识"); err != nil {
		t.Fatal(err)
	}
	if a.selectedID != "knowledge" || a.detailScroll.Y != 80 || a.projectOrganizer.Open {
		t.Fatal("return discarded reading selection or position")
	}
}

func TestProjectOrganizerReadingSwitchClosesPaneAndKeepsTaskIdentity(t *testing.T) {
	a := projectEmptyFrame()
	a.openProjectOrganizer(a.scope, "task-id")
	a.projectOrganizer.Task = &records.ProjectTask{ID: "task-id", State: "organizing", Attempt: 3}
	a.switchReadingKind("raw")
	if a.projectOrganizer.Open || a.projectOrganizer.Task.ID != "task-id" || a.projectOrganizer.Task.Cancelled || a.projectOrganizer.Task.Attempt != 3 {
		t.Fatal("reading switch cancelled or replaced the project task")
	}
}

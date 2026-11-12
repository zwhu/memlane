package desktop

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func projectMenuFrame() *App {
	a := New("test", nil)
	a.page, a.scope = "项目", "a"
	a.projects = []ProjectItem{{ID: "a", Name: "项目A", Path: "/repository/a"}, {ID: "b", Name: "项目B", Path: "/repository/b"}}
	a.scopes = []records.Scope{{ID: "a", Name: "项目A", Path: "/repository/a", Kind: "project"}, {ID: "b", Name: "项目B", Path: "/repository/b", Kind: "project"}}
	v := MemoryItem{ID: "knowledge-a", Dataset: "a", Title: "A正文", Text: strings.Repeat("保留原阅读\n", 100), Origin: "knowledge", State: "active", Revision: 1}
	a.selectedID, a.selectedItem = v.ID, &v
	a.readingSearch, a.readingFilter = "输入A", "筛选A"
	return a
}

func TestProjectSettingsHasNoPermissionEditor(t *testing.T) {
	a := projectMenuFrame()
	a.agents.Connections = []records.Connection{{ID: "pi", Agent: "pi", Name: "pi", Enabled: true, Generation: 1}}
	a.openProjectSettings("a")
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("管理范围") || tt.HasText("保存范围") || tt.HasText("允许访问本项目的知识与原始记录") {
		t.Fatal("permission editor still present")
	}
	if !tt.HasText("所有 Agent 均可读写项目知识与原始记录。") || !tt.HasText("仓库位置") {
		t.Fatal("project context or unrestricted read/write state missing")
	}
}

func TestAnotherProjectMenuKeepsReadingUntilSettingsIsChosen(t *testing.T) {
	a := projectMenuFrame()
	a.detailScroll.Y = 80
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("项目菜单：项目B"); err != nil {
		t.Fatal(err)
	}
	if a.scope != "a" || a.selectedID != "knowledge-a" || a.readingFilter != "筛选A" {
		t.Fatal("opening another menu changed reading")
	}
	tt.Key(0, ui.KeyEscape)
	if a.scope != "a" || !tt.Focused("项目菜单：项目B") || a.detailScroll.Y != 80 {
		t.Fatal("menu close lost focus or scroll")
	}
	if err := tt.Click("项目菜单：项目B"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("项目设置"); err != nil {
		t.Fatal(err)
	}
	if a.scope != "b" || a.projectSettings.Owner.ID != "b" || a.projectSettings.Owner.Path != "/repository/b" {
		t.Fatal("settings used the previously selected project")
	}
	if !tt.HasText("配置对整个项目生效，同一仓库的工作目录共用。") {
		t.Fatal("settings scope is missing")
	}
}

func TestSameProjectSettingsPreserveDraftAndUnderlyingPosition(t *testing.T) {
	a := projectMenuFrame()
	a.detailMode, a.editing = "edit", true
	a.draft = "尚未保存"
	a.detailScroll.Y = 100
	a.openProjectSettings("a")
	if a.projectSettings.Owner.ID != "a" || a.editing || a.drafts[a.selectedID].Text != "尚未保存" {
		t.Fatal("settings discarded draft")
	}
	tt := ui.NewTester(a.View, 980, 620)
	if err := tt.Click("返回知识"); err != nil {
		t.Fatal(err)
	}
	if !a.editing || a.detailMode != "edit" || a.draft != "尚未保存" || a.detailScroll.Y != 100 {
		t.Fatal("return lost original editor state")
	}
}

func TestUnknownProjectStateIsNotPresentedAsEmpty(t *testing.T) {
	a := projectMenuFrame()
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("项目菜单：项目B"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("检查项目状态") || tt.HasText("整理项目") {
		t.Fatal("unknown state became an empty project")
	}
	tt.Key(0, ui.KeyEscape)
	overview := a.projectOverview("b")
	overview.Ready, overview.Knowledge, overview.CheckedAt = true, 2, time.Now()
	if err := tt.Click("项目菜单：项目B"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("补充项目知识") {
		t.Fatal("full scope knowledge did not choose supplement wording")
	}
}

func TestProjectOverviewFailureDoesNotRetryOnEveryFrame(t *testing.T) {
	var requests atomic.Int64
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		requests.Add(1)
		return serviceipc.Response{OK: false, Code: "unavailable", Error: "service unavailable"}
	})
	a.ensureProjectOverview(a.scope)
	awaitExperienceDispatch(t, updates)
	for range 20 {
		a.ensureProjectOverview(a.scope)
	}
	if requests.Load() != 1 || a.projectOverview(a.scope).Ready || a.projectOverview(a.scope).Error == "" {
		t.Fatal("failed state caused automatic request storm or became an empty project")
	}
}

func TestProjectSettingsDoesNotRestoreOldEditorAfterSelectionChanges(t *testing.T) {
	a := projectMenuFrame()
	a.detailMode, a.editing, a.draft = "edit", true, "A未保存"
	a.openProjectSettings("a")
	a.items = []MemoryItem{{ID: "knowledge-b", Dataset: "a", Title: "另一条知识", Origin: "knowledge", State: "active", Revision: 1}}
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("另一条知识"); err != nil {
		t.Fatal(err)
	}
	if !a.projectSettings.Open || a.projectSettings.Owner.ID != "a" {
		t.Fatal("selection changed settings owner")
	}
	a.closeProjectSettings()
	if a.editing || a.detailMode != "view" || a.selectedID != "knowledge-b" || a.drafts["knowledge-a"].Text != "A未保存" {
		t.Fatal("new selection revived another record's editor")
	}
	a.openProjectSettings("a")
	a.newMemory()
	if a.projectSettings.Open || !a.editing || a.detailMode != "new" {
		t.Fatal("new memory remained hidden behind settings")
	}
}

func TestProjectReadFailureDoesNotShowAnotherProjectsListCount(t *testing.T) {
	a := projectMenuFrame()
	a.listTotal = 8
	a.libraryError = "读取失败，当前项目数量未知"
	k, r := 8, 2
	a.readingKnowledgeTotal, a.readingRawTotal = &k, &r
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("8 条知识") {
		t.Fatal("failed project read showed stale list count")
	}
}

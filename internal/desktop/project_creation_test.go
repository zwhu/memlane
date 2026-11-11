package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestProjectCreationCheckAndReturn(t *testing.T) {
	a := sourceReadingFrame()
	a.guidanceRoot = t.TempDir()
	a.detailScroll.Y = 123
	a.detailMode = "edit"
	a.editing = true
	a.draft = "完整未保存草稿"
	a.beginProjectCreation()
	if !a.projectCreationOpen() || a.scope != "project" || a.projectCreationCanRegister() {
		t.Fatal("creation context unsafe")
	}
	a.projectPath = "/synthetic/repo"
	a.projectName = "名称"
	a.projectCreation.CheckedPath = a.projectPath
	a.projectCreation.Inspection = &records.ProjectInspection{State: "ready", Identity: "proof"}
	if !a.projectCreationCanRegister() {
		t.Fatal("explicit check refused")
	}
	a.projectPath += "-changed"
	if a.projectCreationCanRegister() {
		t.Fatal("path edit reused check")
	}
	a.scope = "other-project"
	if a.projectCreationOpen() {
		t.Fatal("A form displayed in B")
	}
	a.scope = "project"
	if !a.projectCreationOpen() {
		t.Fatal("A form did not restore")
	}
	a.closeProjectCreation()
	if a.scope != "project" || a.selectedID != "knowledge" || a.detailScroll.Y != 123 || !a.editing || a.draft != "完整未保存草稿" {
		t.Fatal("return lost reading/draft")
	}
	a.beginProjectCreation()
	a.retireProjectCreation()
	if a.projectCreationOpen() || a.projectCreation.Inspection != nil {
		t.Fatal("retired proof survived")
	}
}
func TestProjectCreationFrames(t *testing.T) {
	for _, w := range []int{1200, 800} {
		a := sourceReadingFrame()
		a.guidanceRoot = t.TempDir()
		a.reader.Open = false
		a.beginProjectCreation()
		a.ConfigureProjectDirectoryChooser(func() (string, error) { return "", nil })
		tt := ui.NewTester(a.View, w, 620)
		if !tt.HasText("检查仓库") || !tt.HasText("选择文件夹") || !tt.HasText("返回项目") {
			t.Fatal("fixed controls missing")
		}
		writeScreenshot(t, tt, fmt.Sprintf("project-add-%d.png", w))
	}
}

func TestProjectCreationInFlightCannotReplaceOwner(t *testing.T) {
	a := sourceReadingFrame()
	a.beginProjectCreation()
	a.projectBusy = true
	sequence := a.projectCreation.Sequence
	a.scope = "other"
	a.beginProjectCreation()
	if a.projectCreation.Sequence != sequence || a.projectCreation.OwnerScope != "project" {
		t.Fatal("inflight registration owner replaced")
	}
}

func TestProjectCreationInspectionLateOwnerRejected(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "inspect_project" {
			t.Errorf("unexpected op %s", r.Op)
		}
		close(entered)
		<-release
		return serviceipc.Response{OK: true, Result: records.ProjectInspection{Path: r.Project.Path, State: "ready", Identity: "explicit-proof", Name: "Auto name"}}
	})
	a.page, a.scope = "项目", "A"
	a.beginProjectCreation()
	a.projectPath = "/synthetic/repo"
	a.checkProjectRepository()
	<-entered
	a.scope = "B"
	close(release)
	manualAwait(t, updates)
	if a.projectCreation.Checking || a.projectCreation.Inspection != nil || a.projectName == "Auto name" {
		t.Fatal("A inspection polluted B")
	}
}

func TestProjectCreationRegistrationCompletesWithoutStealingRoute(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "register_project" {
			t.Errorf("unexpected op %s", r.Op)
		}
		close(entered)
		<-release
		return serviceipc.Response{OK: true, Result: records.Scope{ID: "created-project", Name: "Created", Kind: "project"}}
	})
	a.beginProjectCreation()
	a.projectPath = "/synthetic/repo"
	a.projectName = "Name"
	a.projectCreation.CheckedPath = a.projectPath
	a.projectCreation.Inspection = &records.ProjectInspection{State: "ready", Identity: "explicit-proof"}
	a.registerProject()
	<-entered
	a.scope = "B"
	a.detailMode = "view"
	close(release)
	manualAwait(t, updates)
	if a.scope != "B" || a.projectBusy || a.projectCreation.Open {
		t.Fatal("late success stole route or left busy")
	}
}

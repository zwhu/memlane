package simpledesktop

import (
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProjectVisibilityPersistsWithoutChangingProjectsOrNotices(t *testing.T) {
	a, q, _, _ := notificationMenuFixture(t)
	p := a.Config.Projects[0]
	before := append([]Project(nil), a.Config.Projects...)
	unread := q.unreadCount()
	message, job := q.Sessions[0].View.Status, *q.Backlog
	if err := a.setProjectHidden(p, true); err != nil {
		t.Fatal(err)
	}
	if !a.projectIsHidden(p) || !reflect.DeepEqual(before, a.Config.Projects) || unread != q.unreadCount() || !reflect.DeepEqual(message, q.Sessions[0].View.Status) || job != *q.Backlog {
		t.Fatal("hiding changed project, notification or task data")
	}
	other := p
	other.ID = "another-project"
	if a.projectIsHidden(other) {
		t.Fatal("another project inherited hiding")
	}
	other = p
	other.Dataset = "another-dataset"
	if a.projectIsHidden(other) {
		t.Fatal("another dataset inherited hiding")
	}
	reopened := New(a.Config, nil)
	reopened.Client.PolicyRevision = "new-policy"
	if !reopened.projectIsHidden(p) {
		t.Fatal("reopening lost hiding preference")
	}
	if err := reopened.setProjectHidden(p, false); err != nil {
		t.Fatal(err)
	}
	if New(a.Config, nil).projectIsHidden(p) {
		t.Fatal("restoring did not persist")
	}
	path, _ := a.projectVisibilityPath()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("visibility state is not private", err)
	}
}

func TestProjectVisibilityMenuHideRestoreAndFilteredNavigation(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	a.Config.Projects[0].Name = "hide-target"
	a.Config.Projects[1].Name = "visible-target"
	a.overviewState.AllProjects = false
	p, second := a.Config.Projects[0], a.Config.Projects[1]
	view := func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			a.overviewNavigation(c)
			ui.Column(c).Grow(1).Children(func() { a.overviewProjectGrid(c) })
		})
	}
	tt := ui.NewTester(view, 1200, 820)
	if err := tt.RightClick(p.Name); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	if a.projectIsHidden(p) {
		t.Fatal("closing menu hid project")
	}
	page, project := a.page, a.project
	if err := tt.RightClick(p.Name); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("隐藏项目"); err != nil {
		t.Fatal(err)
	}
	if !a.projectIsHidden(p) || slices.Contains(tt.Texts(), p.Name) || a.page != page || a.project != project {
		t.Fatal("project did not hide or reading location changed")
	}
	if err := tt.Click(second.Name + " · " + second.Path + " · 查看项目记忆"); err != nil {
		t.Fatal(err)
	}
	if a.project != 1 {
		t.Fatal("filtered grid navigated to the hidden project's index")
	}
	// The component fixture has no service/update scheduler. Reveal its all-
	// project projection without starting the overview's unrelated readers.
	a.overviewState.AllProjects = true
	tt.Frame()
	if !tt.HasText("已隐藏 · 右键取消隐藏") {
		t.Fatal("all-project view lost hidden project")
	}
	if err := tt.RightClick(p.Name + " · " + p.Path + " · 查看项目记忆"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("取消隐藏"); err != nil {
		t.Fatal(err)
	}
	if a.projectIsHidden(p) || !slices.Contains(tt.Texts(), p.Name) {
		t.Fatal("project did not return to sidebar")
	}
}

func TestProjectVisibilityRestoresWhenEveryProjectIsHidden(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	a.Config.Projects = a.Config.Projects[:1]
	a.overviewState.AllProjects = false
	p := a.Config.Projects[0]
	if err := a.setProjectHidden(p, true); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.overviewProjectGrid, 1000, 500)
	if !tt.HasText("项目已全部隐藏，可从查看全部中恢复。") {
		t.Fatal("all-hidden recovery hint missing")
	}
	if err := tt.Click("查看全部  ›"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("已隐藏 · 右键取消隐藏") {
		t.Fatal("all-hidden projects cannot be recovered")
	}
}

func TestProjectVisibilityCorruptStateIsNotOverwritten(t *testing.T) {
	a := overviewFixture(t)
	path, _ := a.projectVisibilityPath()
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	p := a.Config.Projects[0]
	if a.projectIsHidden(p) {
		t.Fatal("unreadable preference hid a project")
	}
	if err := a.setProjectHidden(p, true); err == nil {
		t.Fatal("corrupt state reported success")
	}
	if b, _ := os.ReadFile(path); string(b) != "corrupt" || a.projectIsHidden(p) {
		t.Fatal("failed save replaced state or hid project")
	}
}

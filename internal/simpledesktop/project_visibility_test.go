package simpledesktop

import (
	"fmt"
	"os"
	"reflect"
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

func projectVisibilityTester(a *App) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		applyAppearance(a.effectiveDark(c.Theme().Dark))
		ui.Row(c).Fill().Children(func() {
			a.overviewNavigation(c)
			a.allProjectsDialog(c)
		})
	}, 1200, 820)
}

func TestProjectVisibilityMenuHideRestoreAndFilteredNavigation(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	a.Config.Projects[0].Name = "hide-target"
	a.Config.Projects[1].Name = "visible-target"
	p, second := a.Config.Projects[0], a.Config.Projects[1]
	tt := projectVisibilityTester(a)
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
	if _, listed := tt.Find(p.Name); !a.projectIsHidden(p) || listed || a.page != page || a.project != project {
		t.Fatal("project did not hide or reading location changed")
	}
	if err := tt.Click(second.Name); err != nil {
		t.Fatal(err)
	}
	if a.project != 1 || a.page != "知识" {
		t.Fatal("filtered sidebar navigated to the wrong project")
	}
	all := fmt.Sprintf("全部 %d", len(a.Config.Projects))
	if err := tt.Click(all); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("全部项目：" + p.Name); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("取消隐藏"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("关闭全部项目"); err != nil {
		t.Fatal(err)
	}
	if _, listed := tt.Find(p.Name); a.projectIsHidden(p) || !listed || a.project != 1 {
		t.Fatal("project did not return to sidebar or reading location changed")
	}
	if err := tt.Click(all); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("打开项目：" + p.Name); err != nil {
		t.Fatal(err)
	}
	if a.project != 0 || a.allProjectsOpen {
		t.Fatal("opening the restored project did not navigate and close the dialog")
	}
}

func TestProjectVisibilityRestoresWhenEveryProjectIsHidden(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	a.Config.Projects = a.Config.Projects[:1]
	p := a.Config.Projects[0]
	if err := a.setProjectHidden(p, true); err != nil {
		t.Fatal(err)
	}
	tt := projectVisibilityTester(a)
	if _, listed := tt.Find(p.Name); listed {
		t.Fatal("hidden project is visible in the sidebar")
	}
	if err := tt.Click("全部 1"); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("全部项目：" + p.Name); !found {
		t.Fatal("all-hidden projects cannot be recovered")
	}
	if err := tt.Click("取消隐藏项目：" + p.Name); err != nil {
		t.Fatal(err)
	}
	if a.projectIsHidden(p) || !a.allProjectsOpen || a.page != "总览" {
		t.Fatal("restoring the only project changed the overview or closed the dialog")
	}
	if err := tt.Click("关闭全部项目"); err != nil {
		t.Fatal(err)
	}
	if _, listed := tt.Find(p.Name); !listed {
		t.Fatal("restored project did not appear in the sidebar")
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

package simpledesktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAllProjectsSidebarClickRevealsRestorableHiddenCard(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	a.Config.Projects[0].Name = "hidden-first"
	p := a.Config.Projects[0]
	a.overviewState.AllProjects = false
	if err := a.setProjectHidden(p, true); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click(fmt.Sprintf("全部 %d", len(a.Config.Projects))); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	label := "全部项目：" + p.Name
	box, found := tt.Find(label)
	if !found || box.W <= 0 || box.H <= 0 || box.Y < 48 || box.Y+box.H > 820 {
		t.Fatalf("all click left the hidden project's recovery card outside the viewport: found=%v box=%+v scroll=%+v", found, box, a.overviewState.Scroll)
	}
	fixtureImage(t, tt, "all-projects-modal-1200")
	if err := tt.RightClick(label); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("取消隐藏"); err != nil {
		t.Fatal(err)
	}
	if a.projectIsHidden(p) {
		t.Fatal("all-project entry did not restore the hidden project")
	}
	if !a.allProjectsOpen {
		t.Fatal("restoring unexpectedly closed the project list")
	}
	if err := tt.Click("关闭全部项目"); err != nil {
		t.Fatal(err)
	}
	if a.allProjectsOpen || a.page != "总览" || a.overviewState.Scroll.Y != 0 {
		t.Fatal("closing all-projects changed the original page or scroll")
	}
}

func TestAllProjectsOpenKeepsCurrentProjectAndReadingPosition(t *testing.T) {
	a, _, _ := notificationMenuFixture(t)
	a.page, a.kind = "知识", "知识"
	s := a.state()
	s.Loaded, s.Knowledge.Initialized = true, true
	s.ListScroll.Y = 45
	tt := ui.NewTester(a.View, 1200, 820)
	page, project := a.page, a.project
	scroll := s.ListScroll.Y
	if err := tt.Click(fmt.Sprintf("全部 %d", len(a.Config.Projects))); err != nil {
		t.Fatal(err)
	}
	if !a.allProjectsOpen || a.page != page || a.project != project || s.ListScroll.Y != scroll {
		t.Fatal("opening all projects changed the current reading context")
	}
	if err := tt.Click("关闭全部项目"); err != nil {
		t.Fatal(err)
	}
	if a.allProjectsOpen || a.page != page || a.project != project {
		t.Fatal("closing all projects changed the current project")
	}
}

func TestAllProjectsRestoreButtonKeepsListOpen(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	p := a.Config.Projects[0]
	if err := a.setProjectHidden(p, true); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click(fmt.Sprintf("全部 %d", len(a.Config.Projects))); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("取消隐藏项目：" + p.Name); err != nil {
		t.Fatal(err)
	}
	if a.projectIsHidden(p) || !a.allProjectsOpen || a.page != "总览" {
		t.Fatal("restoring project navigated or closed the list")
	}
}

func TestAllProjectsDismissPreservesOverviewScroll(t *testing.T) {
	for _, method := range []string{"escape", "outside"} {
		t.Run(method, func(t *testing.T) {
			a := overviewFixture(t)
			a.Update = nil
			tt := ui.NewTester(a.View, 1200, 820)
			a.overviewState.Scroll.Y = 120
			tt.Frame()
			before := a.overviewState.Scroll.Y
			if before <= 0 {
				t.Fatal("fixture has no real overview scroll position")
			}
			if err := tt.Click(fmt.Sprintf("全部 %d", len(a.Config.Projects))); err != nil {
				t.Fatal(err)
			}
			if method == "escape" {
				tt.Key(0, ui.KeyEscape)
			} else {
				tt.Press(20, 20)
				tt.Release(20, 20)
			}
			if a.allProjectsOpen || a.page != "总览" || a.overviewState.Scroll.Y != before {
				t.Fatal("dismiss changed the original page or scroll")
			}
		})
	}
}

func TestAllProjectsUsesCurrentIndexAfterDirectoryChanges(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	p := a.Config.Projects[2]
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click(fmt.Sprintf("全部 %d", len(a.Config.Projects))); err != nil {
		t.Fatal(err)
	}
	a.Config.Projects = a.Config.Projects[1:]
	tt.Frame()
	if err := tt.Click("打开项目：" + p.Name); err != nil {
		t.Fatal(err)
	}
	if a.allProjectsOpen || a.project != 1 || a.Config.Projects[a.project].ID != p.ID {
		t.Fatal("project list used an obsolete index")
	}
}

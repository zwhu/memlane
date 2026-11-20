package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficialFooterBaselinesAndSplitKeepDraft(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := commonUIFixture()
			a.beginCommonDraft(nil)
			a.commonEdit.Draft.Title = "保留这个草稿"
			tt := ui.NewTester(a.View, size[0], size[1])
			assertFooters := func() {
				var baseline float32
				for n, label := range []string{"侧栏状态栏", "通用记忆分页栏", "通用记忆编辑栏"} {
					r, ok := tt.Find(label)
					if !ok {
						t.Fatal("missing footer", label)
					}
					if n == 0 {
						baseline = r.Y
					}
					// Visual V4: no floating workspace panel, so content footers share
					// the sidebar footer's baseline and the window's bottom edge.
					wantBottom := float32(size[1])
					wantY := baseline
					if math.Abs(float64(r.Y-wantY)) > 1 || r.H != 48 || r.Y+r.H != wantBottom {
						t.Fatalf("misaligned footer %s: %+v baseline=%v", label, r, baseline)
					}
				}
			}
			assertFooters()
			cancel, _ := tt.Find("放弃草稿")
			back, _ := tt.Find("返回阅读")
			save, _ := tt.Find("保存")
			for _, r := range []ui.Rect{back, save} {
				if math.Abs(float64((cancel.Y+cancel.H/2)-(r.Y+r.H/2))) > 1 {
					t.Fatal("footer actions not centered together", cancel, r)
				}
			}
			divider, ok := tt.Find("Divider")
			if !ok {
				t.Fatal("official split missing")
			}
			tt.ClickAt(divider.X+divider.W/2, divider.Y+divider.H/2)
			tt.Key(0, ui.KeyRight)
			if a.layout.CommonListWidth <= 300 || a.commonEdit.Draft.Title != "保留这个草稿" {
				t.Fatal("resizing lost draft or split did not move")
			}
			assertFooters()
			fixtureImage(t, tt, fmt.Sprintf("official-%dx%d-editor-aligned", size[0], size[1]))
			clickProjectUI(t, tt, "放弃草稿")
			if !tt.HasText("放弃未保存的草稿？") {
				t.Fatal("official alert missing")
			}
			tt.Key(0, ui.KeyEscape)
			if a.commonPage.Discard || a.commonEdit.Draft.Title != "保留这个草稿" {
				t.Fatal("escape discarded draft")
			}
		})
	}
}
func TestOfficialListAndSidebarKeyboard(t *testing.T) {
	a := commonUIFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, a.state().Items[0].Title)
	tt.Key(0, ui.KeyDown)
	if a.state().Selected == nil || a.state().Selected.ID != a.state().Items[1].ID {
		t.Fatal("official list keyboard selection lost")
	}
	clickProjectUI(t, tt, "总览")
	tt.Key(0, ui.KeyDown)
	if a.page != "通用知识" {
		t.Fatal("official sidebar keyboard did not navigate")
	}
}
func TestDirectoryDropFillsDraftOnlyAndCannotChangeLinkedProject(t *testing.T) {
	a := settingsFixture()
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	os.WriteFile(file, []byte("fixture"), 0600)
	a.projectCreate = projectCreateState{Open: true}
	a.acceptProjectFolderDrop([]string{file})
	if a.projectCreate.Folder != "" {
		t.Fatal("file accepted as directory")
	}
	a.acceptProjectFolderDrop([]string{dir, dir})
	if a.projectCreate.Folder != "" {
		t.Fatal("multiple directories accepted")
	}
	a.acceptProjectFolderDrop([]string{dir})
	if a.projectCreate.Folder != dir || a.projectCreate.Submitted {
		t.Fatal("directory drop submitted or failed to fill draft")
	}
	a.projectCreate.TargetProject = "p"
	a.projectCreate.Folder = "fixed"
	a.acceptProjectFolderDrop([]string{dir})
	if a.projectCreate.Folder != "fixed" {
		t.Fatal("drop mutated linked project")
	}
}

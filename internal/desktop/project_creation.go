package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"time"
)

type ProjectCreationUI struct {
	OwnerScope                 string
	Open, Checking, NameEdited bool
	Sequence                   uint64
	Root                       string
	Epoch                      uint64
	Inspection                 *records.ProjectInspection
	CheckedPath                string
	Scroll                     ui.ScrollState
	ReturnPage, ReturnScope    string
	ReturnPane                 PaneState
	ReturnEditing              bool
	ReturnFocus                bool
	Picker                     func() (string, error)
}

func (a *App) ConfigureProjectDirectoryChooser(picker func() (string, error)) {
	a.projectCreation.Picker = picker
}
func (a *App) retireProjectCreation() {
	p := a.projectCreation
	a.projectCreation = ProjectCreationUI{Sequence: p.Sequence + 1, Picker: p.Picker}
}
func (a *App) projectCreationOpen() bool {
	p := &a.projectCreation
	return p.Open && p.Root == a.library.Root && p.Epoch == a.libraryEpoch && a.page == "项目" && a.scope == p.OwnerScope
}
func (a *App) beginProjectCreation() {
	if a.projectBusy || a.projectCreationOpen() {
		return
	}
	p := &a.projectCreation
	p.ReturnPage, p.ReturnScope = a.page, a.scope
	p.ReturnPane = PaneState{ID: a.selectedID, Mode: a.detailMode, Kind: a.recordKind, BodyScroll: a.detailScroll.Y}
	p.ReturnEditing = a.editing
	scope := ""
	if a.page == "项目" && a.scope != "" {
		scope = a.scope
	}
	a.navigate("项目", scope)
	p.OwnerScope = scope
	p.Open, p.Root, p.Epoch = true, a.library.Root, a.libraryEpoch
	p.Sequence++
	p.Inspection = nil
	p.Checking = false
	p.CheckedPath = ""
	a.projectError = ""
	a.detailMode = "project"
	a.editing = false
}
func (a *App) closeProjectCreation() {
	p := &a.projectCreation
	if !p.Open {
		return
	}
	page, scope, pane, editing := p.ReturnPage, p.ReturnScope, p.ReturnPane, p.ReturnEditing
	p.Open = false
	p.Sequence++
	p.Checking = false
	if page == "" {
		page = "总览"
	}
	a.navigate(page, scope)
	a.selectedID, a.detailMode, a.recordKind, a.detailScroll.Y = pane.ID, pane.Mode, pane.Kind, pane.BodyScroll
	a.editing = editing
	p.ReturnFocus = true
}
func (a *App) projectCreationHeader(c *ui.Context) {
	quietButton(c, "返回项目").Key("project-creation-close").Disabled(a.projectBusy).OnClick(a.closeProjectCreation)
}
func (a *App) projectCreationPathChanged() {
	p := &a.projectCreation
	p.Sequence++
	p.Inspection = nil
	p.CheckedPath = ""
	p.Checking = false
	a.projectError = ""
}
func (a *App) chooseProjectDirectory() {
	p := &a.projectCreation
	if p.Picker == nil || a.projectBusy {
		return
	}
	root, epoch, seq := a.library.Root, a.libraryEpoch, p.Sequence
	path, err := p.Picker()
	if !a.projectCreationOpen() || root != a.library.Root || epoch != a.libraryEpoch || seq != p.Sequence {
		return
	}
	if err != nil {
		a.projectError = "无法选择文件夹，请重试或填写仓库位置。"
		return
	}
	if path == "" {
		return
	}
	a.projectPath = path
	a.projectCreationPathChanged()
}
func (a *App) checkProjectRepository() {
	p := &a.projectCreation
	if !a.projectCreationOpen() || p.Checking || a.projectBusy || a.libraryContext == nil {
		return
	}
	p.Sequence++
	seq := p.Sequence
	path := a.projectPath
	root, epoch, client, life, dispatch := a.library.Root, a.libraryEpoch, a.library, a.libraryContext, a.dispatch
	p.Checking = true
	p.Inspection = nil
	p.CheckedPath = ""
	a.projectError = ""
	go func() {
		ctx, cancel := context.WithTimeout(life, 8*time.Second)
		defer cancel()
		inspection, err := client.InspectProject(ctx, path)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if p.Sequence != seq || a.library.Root != root || a.libraryEpoch != epoch || a.projectPath != path {
				return
			}
			p.Checking = false
			if !a.projectCreationOpen() {
				return
			}
			if err != nil {
				a.projectError = "暂时无法检查仓库，请重试。"
				return
			}
			p.Inspection = &inspection
			p.CheckedPath = path
			if inspection.State == "ready" && !p.NameEdited {
				a.projectName = inspection.Name
			}
		})
	}()
}
func (a *App) projectCreationCanRegister() bool {
	p := &a.projectCreation
	return a.projectCreationOpen() && !p.Checking && p.Inspection != nil && p.Inspection.State == "ready" && p.Inspection.Identity != "" && p.CheckedPath == a.projectPath && strings.TrimSpace(a.projectName) != "" && len([]rune(strings.TrimSpace(a.projectName))) <= 80
}
func projectInspectionMessage(i records.ProjectInspection) string {
	switch i.State {
	case "ready":
		return "仓库检查通过，可以添加项目。"
	case "existing":
		return "该仓库或它的 worktree 已有项目，共用同一份项目知识。"
	case "invalid_path":
		return "请填写有效的本地文件夹绝对路径。"
	case "not_git":
		return "该文件夹不是可读取的 Git 仓库。"
	case "permission_denied":
		return "没有权限读取该仓库，请调整文件夹访问权限后重新检查。"
	case "ambiguous":
		return "多个已有项目指向该仓库，请先核对项目设置。"
	default:
		return "仓库暂时无法读取，请核对路径后重新检查。"
	}
}
func (a *App) openInspectedProject() {
	p := &a.projectCreation
	if !a.projectCreationOpen() || p.Inspection == nil || p.CheckedPath != a.projectPath || p.Inspection.State != "existing" || p.Inspection.Existing == nil {
		return
	}
	id := p.Inspection.Existing.ID
	p.Open = false
	p.Sequence++
	a.navigate("项目", id)
}

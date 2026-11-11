package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// This is a read-only disclosure for the current graph projection. It does not
// alter the real type values, active filter or persisted project selection.
type entityTypesReadView struct {
	Root                  string
	Epoch                 uint64
	Project, Entity, Body string
	Open, Focus           bool
	ReturnY               float32
}

func (a *App) entityTypesView(e records.CogneeEntity) *entityTypesReadView {
	body := strings.Join(e.Types, "\n\n")
	s := a.browsing.EntityTypes
	if s == nil || s.Root != a.library.Root || s.Epoch != a.libraryEpoch || s.Project != a.scope || s.Entity != e.ID || s.Body != body {
		s = &entityTypesReadView{Root: a.library.Root, Epoch: a.libraryEpoch, Project: a.scope, Entity: e.ID, Body: body}
		a.browsing.EntityTypes = s
	}
	return s
}
func (a *App) entityTypesSummary(c *ui.Context, e records.CogneeEntity) {
	label := entityTypeLabel(e, "项目内对象")
	if len(label) <= 160 {
		ui.Text(c, label).FontSize(10).TextColor(design.Muted)
		return
	}
	s := a.entityTypesView(e)
	ui.Text(c, label).SingleLine().MinWidth(0).FontSize(10).TextColor(design.Muted)
	button := quietButton(c, fmt.Sprintf("查看全部类型（%d）", len(e.Types))).Key("entity-types-open").AlignSelf(ui.Start).OnClick(func() {
		s.ReturnY = a.browsing.EntityBody.Y
		s.Open = true
		s.Focus = false
		a.browsing.EntityBody.Y = 0
	})
	if s.Focus {
		button.Focus()
		if button.Focused() {
			s.Focus = false
		}
	}
}
func (a *App) entityTypesReadout(c *ui.Context, e records.CogneeEntity) bool {
	if len(entityTypeLabel(e, "")) <= 160 {
		return false
	}
	s := a.entityTypesView(e)
	if !s.Open {
		return false
	}
	ui.Row(c).Wrap().Gap(10).Children(func() {
		ui.Text(c, fmt.Sprintf("对象类型 · %d 个", len(e.Types))).FontSize(12).Bold()
		quietButton(c, "返回实体详情").Key("entity-types-close").OnClick(func() { s.Open = false; s.Focus = true; a.browsing.EntityBody.Y = s.ReturnY })
	})
	ui.Text(c, e.Name).FontSize(design.Title).Bold()
	ui.Text(c, a.conflictReadableText(c, "entity-types-"+a.scope+"-"+e.ID, s.Body)).Selectable().FontSize(11).LineHeight(1.7)
	return true
}

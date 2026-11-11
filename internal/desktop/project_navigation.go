package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type ProjectOverview struct {
	Loading, Ready bool
	Knowledge, Raw int
	Task           string
	Error          string
	CheckedAt      time.Time
}

func (a *App) projectOverview(id string) *ProjectOverview {
	if a.projectOverviews == nil {
		a.projectOverviews = map[string]*ProjectOverview{}
	}
	if a.projectOverviews[id] == nil {
		a.projectOverviews[id] = &ProjectOverview{}
	}
	return a.projectOverviews[id]
}

func (a *App) ensureProjectOverview(id string) {
	s := a.projectOverview(id)
	if s.Loading || time.Since(s.CheckedAt) < 5*time.Second || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s.Loading = true
	ownerContext, library, client := a.libraryContext, a.library, a.projectOrganizer.Client
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 5*time.Second)
		defer cancel()
		page, err := library.List(ctx, records.ListQuery{Datasets: []string{id}, Kind: "knowledge", Limit: 1})
		var tasks records.ProjectTaskPage
		if err == nil {
			tasks, err = client.List(ctx, records.MergeListQuery{Project: id, ActiveOnly: true, IncludeIndexRecovery: true, Limit: 1})
		}
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.projectOverviews[id] != s {
				return
			}
			s.Loading = false
			s.CheckedAt = time.Now()
			if err != nil || page.KnowledgeTotal == nil || page.RawTotal == nil {
				s.Ready, s.Error = false, "项目状态读取未完成"
				return
			}
			s.Knowledge, s.Raw, s.Task = *page.KnowledgeTotal, *page.RawTotal, ""
			if len(tasks.Items) > 0 && tasks.Items[0].Project == id {
				s.Task = tasks.Items[0].ID
			}
			s.Ready, s.Error = true, ""
		})
	}()
}

func (a *App) projectNavigation(c *ui.Context, p ProjectItem) {
	selected := a.page == "项目" && a.scope == p.ID
	row := ui.Row(c).Key("project-navigation-" + p.ID).Height(34).Gap(0).AlignItems(ui.Center).Radius(6)
	if selected {
		row.Background(design.Selected)
	} else if row.Hovered() {
		row.Background(ui.Hex("#dfe9f4"))
	}
	row.Children(func() {
		open := ui.Button(c, "").Key("project-open-"+p.ID).Label(p.Name).Tooltip(p.Name).Grow(1).MinWidth(0).Height(34).Padding(8, 12).Justify(ui.Start).Gap(10).BorderWidth(0).Background(ui.Transparent).Children(func() {
			if !a.collapsed {
				ui.Icon(c, icons["项目"]).FontSize(design.Icon).TextColor(design.Muted)
			}
			ui.Text(c, p.Name).SingleLine().MinWidth(0)
		}).OnClick(func() { a.navigate("项目", p.ID) })
		if a.projectCreation.ReturnFocus && a.projectCreation.ReturnPage == "项目" && a.projectCreation.ReturnScope == p.ID {
			open.Focus()
			a.projectCreation.ReturnFocus = false
		}
		more := ui.Button(c, "⋯").Key("project-menu-"+p.ID).Label("项目菜单："+p.Name).Size(24, 28).Padding(0).BorderWidth(0).Background(ui.Transparent)
		if a.projectOrganizer.ReturnFocus && a.projectOrganizer.Project == p.ID && !a.projectKnowledgeEmpty() {
			more.Focus()
			a.projectOrganizer.ReturnFocus = false
		}
		if !selected && !row.Hovered() && !row.FocusWithin() && !more.FocusVisible() {
			more.Opacity(0)
		}
		more.PopoverMenuButton(func(m *ui.Menu) {
			a.ensureProjectOverview(p.ID)
			s := a.projectOverview(p.ID)
			m.Item(p.Name).Disabled(true)
			if m.Item("项目设置").Chosen() {
				a.openProjectSettings(p.ID)
			}
			m.Separator()
			label := "检查项目状态"
			if s.Ready {
				label = "整理项目"
				if s.Knowledge > 0 {
					label = "补充项目知识"
				}
				if s.Task != "" {
					label = "查看整理进度"
				}
			}
			if m.Item(label).Disabled(!s.Ready).Chosen() {
				a.openProjectOrganizer(p.ID, s.Task)
			}
			if s.Error != "" {
				m.Item(s.Error).Disabled(true)
			}
		})
	})
}

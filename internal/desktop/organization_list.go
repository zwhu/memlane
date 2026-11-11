package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func organizationOriginLabel(origin string) string {
	if origin == "schedule" {
		return "计划"
	}
	return "手动"
}
func organizationSummaryExecutor(t records.OrganizationSummary) string {
	if t.Processing != nil {
		return processingExecutorName(*t.Processing)
	}
	return "traex · 历史执行记录"
}
func (a *App) organizationListQuery(now time.Time) records.OrganizationListQuery {
	s := &a.organization
	q := records.OrganizationListQuery{MergeListQuery: records.MergeListQuery{Limit: 30, Project: s.FilterProject}}
	if s.FilterOrigin == "手动" {
		q.Origin = "manual"
	}
	if s.FilterOrigin == "计划" {
		q.Origin = "schedule"
	}
	if s.FilterTime == "今天" {
		local := now.Local()
		q.SinceMS = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).UnixMilli()
	}
	return q
}
func (a *App) changeOrganizationFilters() {
	s := &a.organization
	s.Tasks, s.Next = nil, ""
	s.TasksLoaded = false
	s.Sequence++
	a.refreshOrganization(false, false)
}
func (a *App) clearOrganizationFilters() {
	s := &a.organization
	s.FilterOrigin, s.FilterTime, s.FilterProjectLabel, s.FilterProject = "不限来源", "不限时间", "不限项目", ""
	a.changeOrganizationFilters()
}
func (a *App) organizationListControls(c *ui.Context) {
	s := &a.organization
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "整理记录").FontSize(11).TextColor(design.Muted).Grow(1)
		count := "数量未知"
		if s.TasksLoaded && !s.Loading && !s.SourcesReadFailed {
			count = fmt.Sprintf("%d 次", s.TasksTotal)
		}
		ui.Text(c, count).Key("organization-list-count").FontSize(10).TextColor(design.Muted)
	})
	ui.Row(c).Gap(8).Wrap().Children(func() {
		quietButton(c, "开始整理").Key("organization-list-new").Disabled(s.Busy).Grow(1).Justify(ui.Start).OnClick(a.beginOrganization)
		quietButton(c, "筛选").Key("organization-list-filter").Disabled(s.Busy).OnClick(func() { s.FilterOpen = !s.FilterOpen })
		quietButton(c, "计划").Key("organization-list-plans").Disabled(s.Busy).OnClick(func() { a.navigate("整理计划", "") })
	})
	if !s.FilterOpen {
		return
	}
	if s.FilterOrigin == "" {
		s.FilterOrigin = "不限来源"
	}
	if s.FilterTime == "" {
		s.FilterTime = "不限时间"
	}
	if s.FilterProjectLabel == "" {
		s.FilterProjectLabel = "不限项目"
	}
	disabled := s.Busy || s.Loading
	ui.Text(c, "来源").FontSize(10).TextColor(design.Muted)
	ui.Select(c.Key("organization-filter-origin"), &s.FilterOrigin, []string{"不限来源", "手动", "计划"}).Label("来源").Disabled(disabled).OnChange(a.changeOrganizationFilters)
	options, projects := organizationProjectFilterOptions(a.scopes)
	if s.FilterProject != "" {
		for label, id := range projects {
			if id == s.FilterProject {
				s.FilterProjectLabel = label
				break
			}
		}
	}
	ui.Text(c, "项目").FontSize(10).TextColor(design.Muted)
	ui.Select(c.Key("organization-filter-project"), &s.FilterProjectLabel, options).Label("项目").Disabled(disabled).OnChange(func() { s.FilterProject = projects[s.FilterProjectLabel]; a.changeOrganizationFilters() })
	ui.Text(c, "时间").FontSize(10).TextColor(design.Muted)
	ui.Select(c.Key("organization-filter-time"), &s.FilterTime, []string{"不限时间", "今天"}).Label("时间").Disabled(disabled).OnChange(a.changeOrganizationFilters)
	quietButton(c, "清空条件").Key("organization-list-clear").Disabled(disabled).OnClick(a.clearOrganizationFilters)
}

func organizationProjectFilterOptions(scopes []records.Scope) ([]string, map[string]string) {
	counts := map[string]int{}
	for _, scope := range scopes {
		if scope.Kind == "project" {
			counts[scope.Name]++
		}
	}
	options, projects := []string{"不限项目"}, map[string]string{}
	for _, scope := range scopes {
		if scope.Kind != "project" {
			continue
		}
		label := scope.Name
		if counts[scope.Name] > 1 || label == "不限项目" {
			label += " · " + scope.Path
		}
		options = append(options, label)
		projects[label] = scope.ID
	}
	return options, projects
}

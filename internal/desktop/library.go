package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func displayVersion(v records.Version) MemoryItem {
	created, _ := time.Parse(time.RFC3339Nano, v.CreatedAt)
	if created.IsZero() {
		created, _ = time.Parse("2006-01-02 15:04:05", v.CreatedAt)
	}
	if v.State == "deleted" {
		v.IndexState = "deleted"
	}
	category := "项目约定"
	if v.ScopeKind == "preference" {
		category = "个人偏好"
	} else if v.ScopeKind == "experience" {
		category = "通用经验"
	}
	return MemoryItem{ID: v.Memory, Dataset: v.Dataset, Title: v.Title, Text: v.Text, Original: v.Original, Source: v.Source, ScopeName: v.ScopeName, Category: category, Revision: v.Revision, State: v.State, IndexState: v.IndexState, Operation: v.Operation, CreatedAt: created.Local(), SourceDetail: v.SourceDetail}
}
func displayVersions(vs []records.Version) []MemoryItem {
	out := make([]MemoryItem, 0, len(vs))
	for _, v := range vs {
		out = append(out, displayVersion(v))
	}
	return out
}
func displayHistory(vs []records.History) []MemoryHistory {
	out := make([]MemoryHistory, 0, len(vs))
	for _, v := range vs {
		created, _ := time.Parse("2006-01-02 15:04:05", v.CreatedAt)
		if created.IsZero() {
			created, _ = time.Parse(time.RFC3339Nano, v.CreatedAt)
		}
		out = append(out, MemoryHistory{Revision: v.Revision, Title: v.Title, Text: v.Text, Operation: v.Operation, CreatedAt: created.Local()})
	}
	return out
}

// ConfigureLibrary schedules network work outside UI drawing. dispatch is the
// window's serialized Update boundary. Sequence checks keep late page responses
// from replacing a newer selection or draft; read failures retain prior data.
func (a *App) ConfigureLibrary(ctx context.Context, root string, dispatch func(func())) {
	a.library = serviceipc.Library{Root: root}
	a.merges = MergeUI{Client: serviceipc.MergeClient{Root: root}, Chosen: map[string]records.MergeTarget{}, Reviews: map[string]records.MergeReview{}}
	a.libraryContext = ctx
	a.dispatch = dispatch
	a.onNavigate = func() { a.refresh(false); a.refreshMerge(false) }
	a.onSelect = func(string) { a.history = nil; a.selectedItem = nil; a.refresh(false) }
	a.onProject = func() { a.registerProject() }
	a.onMore = func() { a.refresh(true) }
	a.onSave = a.saveMemory
	a.onRestore = a.restoreMemory
	a.onSearch = a.searchMemory
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dispatch(func() {
					if !a.loading {
						a.refresh(false)
						if a.page == "整理记录" || a.page == "总览" {
							a.refreshMerge(false)
						}
					}
				})
			}
		}
	}()
	a.refresh(false)
	a.refreshMerge(false)
}
func (a *App) listQuery() records.ListQuery {
	q := records.ListQuery{Limit: 50, Kind: a.recordKind, Date: a.activityDate, Timezone: time.Local.String()}
	if a.scope != "" {
		q.Datasets = []string{a.scope}
	} else if a.page == "全局记忆" {
		for _, scope := range a.scopes {
			if scope.Kind == "preference" || scope.Kind == "experience" {
				q.Datasets = append(q.Datasets, scope.ID)
			}
		}
	}
	return q
}
func (a *App) refresh(more bool) {
	if a.libraryContext == nil || a.libraryContext.Err() != nil || a.visualFixture {
		return
	}
	a.loadSequence++
	sequence := a.loadSequence
	a.loading = true
	query := a.listQuery()
	if more {
		query.Cursor = a.nextCursor
	}
	pageName := a.page
	selected := a.selectedID
	selectedRevision := int64(0)
	if item := a.current(); item != nil {
		selectedRevision = item.Revision
	}
	historyLoaded := len(a.history) > 0
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		var health struct {
			State string `json:"state"`
		}
		err := serviceipc.Call(ctx, a.library.Root, serviceipc.Request{Op: "health"}, &health)
		reachable := err == nil
		var scopes []records.Scope
		if err == nil {
			scopes, err = a.library.Scopes(ctx)
		}
		var page records.Page
		var activity records.ActivitySummary
		var current records.Version
		var history []records.History
		var related records.MergePage
		var relatedErr error
		if err == nil {
			if pageName == "全局记忆" {
				query.Datasets = nil
				for _, scope := range scopes {
					if scope.Kind == "preference" || scope.Kind == "experience" {
						query.Datasets = append(query.Datasets, scope.ID)
					}
				}
			}
			page, err = a.library.List(ctx, query)
		}
		if err == nil {
			activity, err = a.library.Activities(ctx, time.Local.String())
		}
		if err == nil && selected != "" {
			current, err = a.library.Get(ctx, selected)
		}
		if err == nil && selected != "" && (!historyLoaded || current.Revision != selectedRevision) {
			before := int64(0)
			for {
				var result records.HistoryPage
				result, err = a.library.History(ctx, selected, before)
				if err != nil {
					break
				}
				history = append(history, result.Items...)
				if result.NextRevision == 0 {
					break
				}
				before = result.NextRevision
			}
		}
		if err == nil && selected != "" {
			linksCtx, linksStop := context.WithTimeout(a.libraryContext, 500*time.Millisecond)
			related, relatedErr = a.merges.Client.List(linksCtx, records.MergeListQuery{Memory: selected, Limit: 100})
			linksStop()
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != a.loadSequence {
				return
			}
			a.loading = false
			if reachable {
				a.serviceState = health.State
			} else {
				a.serviceState = "unavailable"
			}
			if err != nil {
				a.libraryError = "读取失败，保留上次内容；请重试。"
				return
			}
			a.libraryError = ""
			a.scopes = scopes
			a.projects = nil
			for _, scope := range scopes {
				if scope.Kind != "preference" && scope.Kind != "experience" {
					a.projects = append(a.projects, ProjectItem{ID: scope.ID, Name: scope.Name, Path: scope.Path})
				}
			}
			if pageName == "搜索" {
				// Search results remain bound to their submitted query.
			} else if more {
				a.items = append(a.items, displayVersions(page.Items)...)
			} else {
				// Keep a loaded pagination window during background status polling.
				if len(a.items) > 50 {
					// Retain this pagination snapshot until navigation or an
					// explicit reload; new items must not shift page boundaries.
				} else {
					a.items = displayVersions(page.Items)
					a.nextCursor = page.NextCursor
				}
			}
			if more {
				a.nextCursor = page.NextCursor
			}
			if pageName != "搜索" {
				a.listTotal = page.Total
			}
			a.recent = displayVersions(activity.Recent)
			a.activities = activity.Days
			a.activityLoaded = true
			a.imported = activity.Imported
			if selected != "" && selected == a.selectedID {
				item := displayVersion(current)
				a.selectedItem = &item
				if relatedErr == nil {
					a.relatedMerges = related.Items
					a.relatedError = ""
				} else {
					if a.relatedFor != selected {
						a.relatedMerges = nil
					}
					a.relatedError = "相关整理记录暂不可用"
				}
				a.relatedFor = selected
				if history != nil {
					a.history = displayHistory(history)
				}
			}
		})
	}()
}
func (a *App) registerProject() {
	if a.projectBusy || a.libraryContext == nil {
		return
	}
	draft := records.ScopeDraft{Name: a.projectName, Path: a.projectPath}
	a.projectBusy = true
	a.projectError = ""
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		scope, err := a.library.RegisterProject(ctx, draft)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			a.projectBusy = false
			if err != nil {
				a.projectError = "添加失败：请检查项目名称和本地 Git 仓库路径。"
				return
			}
			a.projectName, a.projectPath = "", ""
			a.navigate("项目", scope.ID)
			a.detailMode = "view"
			a.editing = false
		})
	}()
}

func (a *App) scopeLabel(id string) string {
	for _, scope := range a.scopes {
		if scope.ID == id {
			count := 0
			for _, other := range a.scopes {
				if other.Name == scope.Name {
					count++
				}
			}
			if count > 1 {
				return fmt.Sprintf("%s · %s", scope.Name, scope.ID[:8])
			}
			return scope.Name
		}
	}
	return ""
}
func (a *App) datasetForLabel(label string) string {
	for _, scope := range a.scopes {
		if a.scopeLabel(scope.ID) == strings.TrimSpace(label) {
			return scope.ID
		}
	}
	return ""
}

func (a *App) visibleTotal() int {
	if a.visualFixture {
		return len(a.items)
	}
	return a.listTotal
}

package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if v.Origin == "raw" {
		category = "原始记录"
	} else if v.ScopeKind == "preference" {
		category = "个人偏好"
	} else if v.ScopeKind == "experience" {
		category = "通用经验"
	}
	return MemoryItem{Origin: v.Origin, ID: v.Memory, Dataset: v.Dataset, Title: v.Title, Text: v.Text, Original: v.Original, Source: v.Source, ScopeName: v.ScopeName, Category: category, Revision: v.Revision, State: v.State, IndexState: v.IndexState, Operation: v.Operation, CreatedAt: created.Local(), SourceDetail: v.SourceDetail, Tags: v.Tags, Derived: v.Derived, Experience: v.Experience, Usage: v.Usage}
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
		conditions := ""
		if v.Experience != nil {
			conditions = v.Experience.Conditions
		}
		out = append(out, MemoryHistory{Revision: v.Revision, Title: v.Title, Text: v.Text, Operation: v.Operation, CreatedAt: created.Local(), Conditions: conditions})
	}
	return out
}

// ConfigureLibrary schedules network work outside UI drawing. dispatch is the
// window's serialized Update boundary. Sequence checks keep late page responses
// from replacing a newer selection or draft; read failures retain prior data.
func (a *App) ConfigureLibrary(ctx context.Context, root string, dispatch func(func())) {
	a.library = serviceipc.Library{Root: root}
	a.backups = BackupUI{Client: serviceipc.BackupClient{Root: root}, Sequence: a.backups.Sequence + 1}
	a.code = CodeUI{Client: serviceipc.CodeClient{Root: root}, Mode: "函数与符号", Manage: true}
	a.initialization = InitializationUI{Client: serviceipc.InitializationClient{Root: root}}
	a.usageBrowseError = ""
	a.usageImportantError = ""
	a.usageNavigation = uuid.NewString()
	a.usageBrowsed = map[string]bool{}
	a.listSort = "最近更新"
	a.schedules = ScheduleUI{Client: serviceipc.ScheduleClient{Root: root}, Destinations: map[string]bool{}}
	a.organization = OrganizationUI{Client: serviceipc.OrganizationClient{Root: root}, Chosen: map[string]MemoryItem{}, Results: map[string]MemoryItem{}, Kind: "knowledge"}
	a.merges = MergeUI{Client: serviceipc.MergeClient{Root: root}, Chosen: map[string]records.MergeTarget{}, Reviews: map[string]records.MergeReview{}}
	a.processing.ensure()
	a.libraryContext = ctx
	a.dispatch = dispatch
	a.onNavigate = func() {
		a.closeSourceReader()
		a.textWindows = nil
		if a.page != "代码图谱" && a.code.Cancel != nil {
			a.code.Cancel()
			a.code.Cancel = nil
			a.code.Sequence++
			a.code.Loading = false
			a.code.Busy = false
		}
		a.refreshCode()
		a.usageBrowseError = ""
		a.usageImportantError = ""
		a.usageNavigation = uuid.NewString()
		a.usageBrowsed = map[string]bool{}
		a.refresh(false)
		a.refreshMerge(false)
		a.refreshOrganization(false, false)
		a.refreshSchedules()
		a.refreshInitialization()
		if a.page == "设置" {
			a.refreshIndexRebuild()
			a.refreshBackups()
		}
		if (a.page == "设置" && a.settingsSection == "processing") || (a.firstLaunch.Open && a.firstLaunchStep() == "processing") {
			a.refreshProcessing()
		}
		if a.page == "使用指引" || a.page == "初始化" {
			a.refreshAgents(false)
		}
	}
	a.onSelect = func(string) {
		a.closeSourceReader()
		a.browsing.Relation = ""
		if a.browsing.Entity != "" {
			related := false
			for _, mention := range a.browsing.Graph.Mentions {
				if mention.Memory == a.selectedID && mention.Entity == a.browsing.Entity {
					related = true
					break
				}
			}
			if !related {
				a.browsing.Entity = ""
			}
		}
		a.textWindows = nil
		a.usageBrowseError = ""
		a.usageImportantError = ""
		a.usageNavigation = uuid.NewString()
		a.usageBrowsed = map[string]bool{}
		a.history = nil
		a.selectedItem = nil
		a.refresh(false)
		a.saveBrowsing()
		a.refreshBrowsing()
	}
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
					if a.firstLaunch.Detecting {
						return
					}
					if a.page == "项目" {
						a.refreshBrowsing()
					}
					if a.reader.Open {
						a.refreshSourceReader()
					}
					if a.page == "设置" {
						a.refreshIndexRebuild()
						a.refreshBackups()
					}
					if (a.page == "设置" && a.settingsSection == "processing") || (a.firstLaunch.Open && a.firstLaunchStep() == "processing") {
						a.refreshProcessing()
					}
					if a.page == "代码图谱" && !a.code.Busy {
						a.refreshCode()
					}
					if a.page == "初始化" || a.page == "使用指引" {
						a.refreshInitialization()
					}
					if a.page == "整理计划" || a.page == "总览" {
						a.refreshSchedules()
					}
					if !a.loading {
						a.refresh(false)
						if a.page == "整理记录" || a.page == "总览" {
							a.refreshMerge(false)
							a.refreshOrganization(false, false)
						}
					}
				})
			}
		}
	}()
	a.refresh(false)
	a.refreshMerge(false)
	a.refreshProcessing()
	a.configureBrowsing()
}
func (a *App) listQuery() records.ListQuery {
	sortMode := map[string]string{"最近更新": "updated", "常用": "frequent", "重要优先": "important"}[a.listSort]
	q := records.ListQuery{Sort: sortMode, Limit: 50, Kind: a.recordKind, Date: a.activityDate, Timezone: time.Local.String()}
	if a.isMemoryPage() && a.page != "搜索" {
		q.Text = a.readingFilter
	}
	if a.page == "总览" {
		q.Kind, q.Date, q.Sort = "knowledge", a.homeDate, ""
	}
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
	if a.libraryContext == nil || a.libraryContext.Err() != nil || a.visualFixture || a.firstLaunch.Detecting || a.knowledgeHistory.Open {
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
	countsRoute := a.page + ":" + a.scope
	selected := a.selectedID
	selectedRevision := int64(0)
	if item := a.current(); item != nil {
		selectedRevision = item.Revision
	}
	historyLoaded := len(a.history) > 0
	lastHomeTask := a.homeLastTask
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		// Page reads do not own availability. Startup and the runtime observer
		// publish service health independently, including schedule diagnostics
		// when the business database itself cannot be read.
		scopes, err := a.library.Scopes(ctx)
		var page records.Page
		var activity records.ActivitySummary
		var active homeTask
		var terminal homeTask
		var current records.Version
		var history []records.History
		var related records.MergePage
		var relatedErr error
		var organizationSources []records.OrganizationSource
		var organizationSourcesErr error
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
		if err == nil && pageName == "总览" {
			var organizations records.OrganizationPage
			organizations, err = a.organization.Client.List(ctx, records.MergeListQuery{ActiveOnly: true, Limit: 1})
			if err == nil && len(organizations.Items) > 0 {
				v := organizations.Items[0]
				active = homeTask{ID: v.ID, Kind: "organization", State: v.State}
			}
			if err == nil {
				var merges records.MergePage
				merges, err = a.merges.Client.List(ctx, records.MergeListQuery{ActiveOnly: true, Limit: 1})
				if len(merges.Items) > 0 && (active.ID == "" || (active.State == "queued" && merges.Items[0].State == "running")) {
					v := merges.Items[0]
					active = homeTask{ID: v.ID, Kind: "merge", State: v.State}
				}
			}
		}
		if err == nil && pageName == "总览" && lastHomeTask.ID != "" && active.ID != lastHomeTask.ID {
			// An absent active task does not prove completion. Fetch its actual final
			// receipt; failed/cancelled/unreadable tasks never show a success glyph.
			if lastHomeTask.Kind == "organization" {
				task, e := a.organization.Client.Get(ctx, lastHomeTask.ID)
				if e == nil {
					terminal = homeTask{ID: task.ID, Kind: "organization", State: task.State}
				}
			} else {
				task, e := a.merges.Client.Get(ctx, lastHomeTask.ID)
				if e == nil {
					terminal = homeTask{ID: task.ID, Kind: "merge", State: task.State}
				}
			}
		}

		if pageName == "总览" {
			selected = ""
		}
		if err == nil && selected != "" {
			current, err = a.library.Get(ctx, selected)
			if err == nil && pageName == "项目" && len(query.Datasets) == 1 && current.Dataset != query.Datasets[0] {
				err = &records.AccessError{Code: "scope_denied", Message: "selection belongs to another project"}
			}
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
			if current.Origin != "raw" {
				sourcesCtx, sourcesStop := context.WithTimeout(a.libraryContext, 500*time.Millisecond)
				organizationSources, organizationSourcesErr = a.organization.Client.Sources(sourcesCtx, selected, current.Revision)
				sourcesStop()
			}
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if sequence != a.loadSequence || a.firstLaunch.Detecting || a.knowledgeHistory.Open {
				return
			}
			a.loading = false
			if scopes != nil {
				a.scopes = scopes
				a.projects = nil
				for _, scope := range scopes {
					if scope.Kind != "preference" && scope.Kind != "experience" {
						a.projects = append(a.projects, ProjectItem{ID: scope.ID, Name: scope.Name, Path: scope.Path})
					}
				}
			}
			if err != nil {
				a.libraryError = "读取失败，保留同范围的上次内容；请重新选择或重试。"
				if current.Dataset != "" && pageName == "项目" && current.Dataset != a.scope {
					a.selectedItem = nil
					a.selectedID = ""
				}
				return
			}
			a.libraryError = ""
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
				a.readingCountsRoute = countsRoute
				a.readingKnowledgeTotal, a.readingRawTotal = page.KnowledgeTotal, page.RawTotal
			}
			if pageName == "总览" {
				a.observeHomeTask(active, terminal, time.Now())
				a.recent = displayVersions(activity.Recent)
				if query.Date != "" {
					a.recent = displayVersions(page.Items)
				}
				a.homeRecentLoaded = true
			} else if a.homeDate == "" {
				a.recent = displayVersions(activity.Recent)
			}
			a.overviewRaw, a.overviewKnowledge = activity.RawTotal, activity.KnowledgeTotal
			a.overviewUpdated = time.Now()
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
				if organizationSourcesErr == nil {
					a.organizationSources = organizationSources
					a.organizationSourcesError = ""
				} else {
					if a.organizationSourcesFor != selected || a.organizationSourcesRevision != current.Revision {
						a.organizationSources = nil
					}
					a.organizationSourcesError = "摘要依据读取失败，保留已读取的同版本内容；请重试。"
				}
				a.organizationSourcesFor = selected
				a.organizationSourcesRevision = current.Revision
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

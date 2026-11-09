package desktop

import (
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"time"
)

// Presentation types carry service snapshots. They never fabricate persistence
// or index status; the visual fixture is compiled only into e2e builds.
type MemoryItem struct {
	Origin                                                                                        string
	Usage                                                                                         *records.UsageInfo
	ID, Dataset, Title, Text, Original, Source, ScopeName, Category, State, IndexState, Operation string
	Revision                                                                                      int64
	CreatedAt                                                                                     time.Time
	SourceDetail                                                                                  *records.AgentSource
	Tags                                                                                          []string
	Derived                                                                                       *records.DerivedInfo
	Experience                                                                                    *records.ExperienceInfo
}
type ProjectItem struct{ ID, Name, Path string }
type MemoryHistory struct {
	Revision                           int64
	Title, Text, Operation, Conditions string
	CreatedAt                          time.Time
}
type Draft struct {
	Title, Text, Scope, Conditions string
	Revision                       int64
}

func (a *App) rememberDraft() {
	if !a.editing {
		return
	}
	key := a.selectedID
	if a.detailMode == "new" {
		key = "new"
	}
	d := a.drafts[key]
	d.Title, d.Text, d.Scope = a.draftTitle, a.draft, a.draftScope
	d.Conditions = a.draftConditions
	a.drafts[key] = d
}

type PaneState struct{ ID, Mode string }

func (a *App) navigate(page, scope string) {
	a.saveBrowsing()
	a.rememberDraft()
	if a.isMemoryPage() {
		a.routeStates[a.page+":"+a.scope] = PaneState{a.selectedID, a.detailMode}
	}
	oldRoute := a.memoryRoute
	if a.page != page || a.scope != scope {
		a.nextCursor = ""
		if !a.visualFixture {
			a.items = nil
		}
	}
	a.page, a.scope = page, scope
	if page != "总览" {
		a.homeMotion = homeButtonMotion{}
		a.homeCompletedAt = time.Time{}
	}
	if page == "设置" && a.settingsSection == "environment" {
		a.ensureEnvironment()
	}
	if !a.isMemoryPage() {
		a.editing = false
	} else {
		route := page + ":" + scope
		if route != oldRoute {
			state := a.routeStates[route]
			a.selectedID = state.ID
			a.selectedItem = nil
			a.history = nil
			a.detailMode = state.Mode
			if a.detailMode == "" {
				a.detailMode = "view"
			}
			key := a.selectedID
			if a.detailMode == "new" {
				key = "new"
			}
			if draft, ok := a.drafts[key]; ok {
				a.draftTitle, a.draft, a.draftScope = draft.Title, draft.Text, draft.Scope
				a.draftConditions = draft.Conditions
			}
		}
		a.memoryRoute = route
		a.editing = a.detailMode == "new" || a.detailMode == "edit"
	}
	if a.onNavigate != nil {
		a.browseProjectChanged()
		a.onNavigate()
	}
}

func (a *App) newMemory() {
	a.rememberDraft()
	a.message = ""
	a.recordKind = "knowledge"
	a.activityDate = ""
	a.conflictCurrent = nil
	if a.scope != "" {
		a.page = "项目"
	} else if a.page != "全局记忆" {
		a.page = "全部记忆"
	}
	a.memoryRoute = a.page + ":" + a.scope
	a.detailMode = "new"
	a.editing = true
	d := a.drafts["new"]
	a.draftTitle, a.draft, a.draftScope = d.Title, d.Text, d.Scope
	a.draftConditions = d.Conditions
	if a.draftScope == "" {
		a.draftScope = "全局经验"
		for _, p := range a.projects {
			if a.scope == p.ID {
				a.draftScope = p.Name
				if label := a.scopeLabel(p.ID); label != "" {
					a.draftScope = label
				}
			}
		}
	}
}
func (a *App) editMemory() {
	item := a.current()
	if item == nil {
		return
	}
	if item.Origin == "raw" && item.SourceDetail != nil && (item.SourceDetail.Official != nil || item.SourceDetail.Native != nil) {
		a.message = "官方原始记录保持原样；请通过阅读器核验来源与授权后阅读。"
		return
	}
	a.detailMode = "edit"
	a.editing = true
	a.message = ""
	a.conflictCurrent = nil
	d, ok := a.drafts[item.ID]
	if !ok {
		d = Draft{Title: item.Title, Text: item.Text, Scope: item.ScopeName, Revision: item.Revision}
		if item.Experience != nil {
			d.Conditions = item.Experience.Conditions
		}
		if label := a.scopeLabel(item.Dataset); label != "" {
			d.Scope = label
		}
		a.drafts[item.ID] = d
	}
	a.draftTitle, a.draft, a.draftScope = d.Title, d.Text, d.Scope
	a.draftConditions = d.Conditions
}
func (a *App) scopeOptions() []string {
	if len(a.scopes) > 0 {
		out := []string{}
		for _, scope := range a.scopes {
			out = append(out, a.scopeLabel(scope.ID))
		}
		return out
	}
	out := []string{"个人偏好", "全局经验"}
	for _, p := range a.projects {
		out = append(out, p.Name)
	}
	return out
}
func preview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return text
}
func operationLabel(op string) string {
	switch op {
	case "move":
		return "项目移动"
	case "merge_archive":
		return "整理归档"
	case "save":
		return "手动新增"
	case "edit":
		return "手动编辑"
	case "restore":
		return "恢复版本"
	case "delete":
		return "删除"
	}
	return op
}
func indexLabel(state string) string {
	switch state {
	case "deleted":
		return "已删除，不参与检索"
	case "indexed":
		return "可检索"
	case "failed":
		return "索引失败 · 正文已保留"
	case "pending":
		return "等待索引"
	case "indexing":
		return "正在索引"
	}
	return "尚未索引"
}

// A line diff preserves both removed and added text using monochrome markers.
// Equal prefix/suffix lines stay out of the change block for long documents.
func lineDiff(before, after string) string {
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	ai, bi := len(a), len(b)
	for ai > prefix && bi > prefix && a[ai-1] == b[bi-1] {
		ai--
		bi--
	}
	lines := []string{}
	for _, line := range a[prefix:ai] {
		lines = append(lines, "− "+line)
	}
	for _, line := range b[prefix:bi] {
		lines = append(lines, "+ "+line)
	}
	if len(lines) == 0 {
		return "正文未改变"
	}
	return strings.Join(lines, "\n")
}

// SetVisualFixture is used only by the e2e command's explicit flag. It has no
// persistence or mock-success actions, so saves remain disabled.
func (a *App) SetVisualFixture(items []MemoryItem, projects []ProjectItem, history []MemoryHistory, activity map[string]int) {
	a.items, a.projects, a.history, a.activities = items, projects, history, activity
	a.visualFixture = true
}

func (a *App) searchPlaceholder() string {
	if a.scope != "" {
		return "搜索当前项目与全局"
	}
	if a.page == "全局记忆" {
		return "搜索全局记忆"
	}
	return "搜索所有记忆"
}

func (a *App) availableIndexLabel(state string) string {
	if state == "indexed" && a.libraryContext != nil && (a.serviceState != "ready" || a.libraryError != "" || a.runtimeStatus.Phase == "stopped") {
		return "索引已完成 · 等待服务恢复"
	}
	return indexLabel(state)
}

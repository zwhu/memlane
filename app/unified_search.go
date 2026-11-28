package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	appsearch "github.com/zwhu/memlane/app/search"
	"github.com/zwhu/memlane/app/shell"
)

type unifiedSearchState = appsearch.StateOf[Item, searchLaneResult]

func stripSearchWrapperPrefixes(s string) string { return appsearch.StripWrapperPrefixes(s) }

func (a *App) searchCatalog(scope string) []Project {
	if scope == "@common" {
		return append([]Project(nil), a.Config.Common...)
	}
	if scope == "" {
		return overviewProjects(a.Config)
	}
	for _, p := range a.Config.Projects {
		if p.ID == scope {
			return []Project{p}
		}
	}
	return nil
}
func (a *App) searchSignature() string { return a.searchPage.Signature() }
func (a *App) openUnifiedSearch(restore bool) {
	a.projectUI.InfoOpen, a.projectUI.MenuOpen = false, false
	a.loadProjectVisibility()
	p, ok := a.current()
	a.searchPage.Open(restore, a.page, projectRef(p), ok)
}
func (c *searchController) closeUnifiedSearch() {
	if c.searchPage.State != nil {
		c.searchPage.Close()
	}
}
func (c *searchController) invalidateUnifiedSearch(dataset string) {
	if c.searchPage.State == nil {
		c.searchPage.State = &c.searchUI
		c.searchPage.Describe = func(i Item) appsearch.ResultItem { return appsearch.ResultItem{DatasetID: i.Dataset} }
	}
	c.searchPage.Invalidate(dataset)
}
func (a *App) submitUnifiedSearch() { a.searchPage.Submit() }
func (a *App) runUnifiedSearch(retry bool) {
	if retry {
		a.searchPage.Retry()
	} else {
		a.searchPage.Submit()
	}
}
func (a *App) searchSubmitOrOpen()       { a.searchPage.SubmitOrOpen() }
func (a *App) visibleSearchRows() []Item { return a.searchPage.VisibleRows() }
func (a *App) cancelChangedSearch()      { a.searchPage.CancelChanged() }
func (a *App) returnToSearch()           { a.searchPage.ReturnToSearch() }

func (a *App) openSearchResult(i Item) error {
	if i.ReadingState == "unavailable" {
		return fmt.Errorf("结果暂不可读取")
	}
	origin := a.readingLocation()
	if a.searchOrigin != nil {
		origin = a.searchOrigin
	}
	page, index, kind := "知识", -1, "原始记录"
	if strings.Contains(i.Kind, "知识") {
		kind = "知识"
	}
	for n, p := range a.Config.Projects {
		if p.Dataset == i.Dataset {
			index = n
			break
		}
	}
	if index < 0 {
		for n, p := range a.Config.Common {
			if p.Dataset == i.Dataset {
				page = "通用知识"
				index = n
				break
			}
		}
	}
	if index < 0 {
		return fmt.Errorf("结果范围已失效，请重新搜索")
	}
	a.navigateKind(page, index, kind)
	a.searchOrigin = origin
	target := a.state()
	a.searchPane = target
	a.searchAnchor = &i
	if !containsReadingItem(target.Items, i.Key()) {
		target.Items = append([]Item{i}, target.Items...)
	}
	a.selectItem(target, i)
	if i.SourceDocument != "" && i.Document == nil {
		a.resolveSearchDocument(target, i)
	}
	return nil
}
func containsReadingItem(rows []Item, key string) bool {
	for _, i := range rows {
		if i.Key() == key {
			return true
		}
	}
	return false
}
func (a *App) resolveSearchDocument(s *pane, i Item) {
	if a.Update == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	a.searchResolveCancel = cancel
	adapter := appShellAdapter{a}
	fence := shell.CaptureFence(adapter)
	client, update := a.Client, a.Update
	go func() {
		defer cancel()
		result, err := client.documentInfoByID(ctx, i.Dataset, i.SourceDocument)
		update(func() {
			if !fence.Valid(adapter) || a.searchPane != s || a.searchAnchor == nil || a.searchAnchor.Key() != i.Key() || s.Selected == nil || s.Selected.Key() != i.Key() {
				return
			}
			if err != nil {
				if readingUnavailable(err) {
					a.InvalidateReading(i.Dataset)
				}
				return
			}
			a.searchAnchor = &result
			rows := []Item{result}
			for _, row := range s.Items {
				if row.Key() != i.Key() && row.Key() != result.Key() {
					rows = append(rows, row)
				}
			}
			s.Items = rows
			a.selectItem(s, result)
		})
	}()
}

func searchRowText(i Item) (string, string) {
	body := i.Preview
	if body == "" {
		body = itemPresentation(i).VisibleBody
	}
	// Technical headers are presentation-only; keep the original source bytes.
	lines := strings.Split(body, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		v := strings.TrimSpace(line)
		if rest := stripSearchWrapperPrefixes(v); rest != v {
			if rest == "" {
				continue
			}
			clean = append(clean, rest)
			continue
		}
		clean = append(clean, line)
	}
	preview := stripSearchWrapperPrefixes(strings.Join(clean, "\n"))
	title := i.Title
	if title == "检索片段" || readableName(title) == "" || strings.HasPrefix(title, "Session ID: ") || stripSearchWrapperPrefixes(title) != strings.TrimSpace(title) {
		title = firstReadingSentence(preview)
	}
	if title == "" {
		if strings.Contains(i.Kind, "知识") {
			title = "记忆"
		} else {
			title = "原始记录"
		}
	}
	if strings.TrimSpace(preview) == strings.TrimSpace(title) {
		preview = ""
	}
	return title, preview
}

func (a *App) initSearch() {
	a.searchType = "全部类型"
	adapter := appShellAdapter{a}
	a.searchPage = appsearch.ControllerOf[Item, searchLaneResult]{
		State: &a.searchUI,
		DepsOf: appsearch.DepsOf[Item, searchLaneResult]{
			Dispatcher: adapter, Router: adapter, Fence: adapter, Theme: adapter,
			CanDispatch: func() bool { return a.Update != nil },
			Catalog: func(scope string) []shell.ProjectRef {
				projects := a.searchCatalog(scope)
				rows := make([]shell.ProjectRef, 0, len(projects))
				for _, p := range projects {
					ref := projectRef(p)
					ref.Common = scope == "@common"
					rows = append(rows, ref)
				}
				return rows
			},
			TargetProject:   func() (shell.ProjectRef, bool) { p, ok := a.searchTargetProject(); return projectRef(p), ok },
			ProjectName:     a.searchProjectName,
			OnRememberQuery: func(_, _ string) { a.rememberManualSearch() },
			OnOpenItem:      a.openSearchResult,
			OnRestoreOrigin: func() { a.restoreReadingLocation(a.searchOrigin) },
			OnTrigger:       a.OpenSearch,
			ReadingDenied:   readingUnavailable,
			Describe:        a.describeSearchItem,
			FromResults:     desktopSearchItems,
			Fetch:           a.searchFetch,
			RenderIcon:      func(c *ui.Context, i Item) { agentLogo(c, recentAgentIcon(i), 20) },
			RenderMetadata:  renderSearchMetadata,
			ItemText:        searchItemPrimaryText,
		},
	}
}

func (a *App) describeSearchItem(i Item) appsearch.ResultItem {
	date := displayDate(i.Date)
	knowledge := strings.Contains(i.Kind, "知识")
	if knowledge {
		date = knowledgeDateLabel(i)
	}
	user := ""
	if i.Session != nil {
		user = i.Session.User
	}
	return appsearch.ResultItem{ID: i.ID, DatasetID: i.Dataset, Kind: i.Kind, Title: i.Title, Date: date,
		Preview: i.Preview, SourceDocument: i.SourceDocument, UserID: user,
		AgentID: itemSourceAgentID(i), SessionID: itemSourceSession(i), SearchMatch: i.SearchMatch,
		Unread: knowledge && a.itemIsUnread(i), Unavailable: i.ReadingState == "unavailable", IsSession: i.Session != nil}
}

func desktopSearchItems(rows []appsearch.ResultItem) []Item {
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		item := Item{ID: row.ID, Dataset: row.DatasetID, Kind: row.Kind, Title: row.Title, Date: row.Date,
			Preview: row.Preview, Source: row.SessionID, SourceAgent: row.AgentID, SourceDocument: row.SourceDocument, SearchMatch: row.SearchMatch,
			ServerUnread: row.Unread, ServerUnreadKnown: true}
		if row.IsSession {
			item.Session = &Session{ID: row.ID, Dataset: row.DatasetID, User: row.UserID, Agent: row.AgentID}
		}
		out = append(out, item)
	}
	return out
}

func renderSearchMetadata(c *ui.Context, i Item) {
	if strings.Contains(i.Kind, "知识") {
		v4RecallChip(c, i)
		if fields := knowledgeBasisFields(i); len(fields) > 0 {
			v4CategoryChip(c, fields[0].Value).Shrink(1).MinWidth(0)
		}
		return
	}
	rawTag := "会话"
	if i.Session == nil && !strings.Contains(i.Kind, "会话") {
		rawTag = "文档"
	}
	v4CategoryChip(c, rawTag)
	if i.SessionSummary != "" {
		ui.Text(c, i.SessionSummary).Font(fontUI).FontSize(fsCaption).TextColor(tok.Text3).SingleLine().MinWidth(0)
	}
}

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	appgraph "github.com/zwhu/memlane/app/graph"
	appoverview "github.com/zwhu/memlane/app/overview"
	appsearch "github.com/zwhu/memlane/app/search"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// searchController owns unified search modal state, result-origin navigation,
// and reading-page search filters.
type searchController struct {
	searchUI            unifiedSearchState
	searchPage          appsearch.ControllerOf[Item, searchLaneResult]
	searchOrigin        *readingLocation
	searchPane          *pane
	searchAnchor        *Item
	searchResolveCancel context.CancelFunc
	searchType          string
	searchProject       string
	focusSearch         bool
}

func (c *searchController) clearSearchOrigin() {
	if c.searchResolveCancel != nil {
		c.searchResolveCancel()
		c.searchResolveCancel = nil
	}
	c.searchOrigin = nil
	c.searchPane = nil
	c.searchAnchor = nil
}

func (c *searchController) invalidateReading(dataset string) {
	c.invalidateUnifiedSearch(dataset)
	if dataset == "" || (c.searchAnchor != nil && c.searchAnchor.Dataset == dataset) {
		c.clearSearchOrigin()
	}
}

func (c *searchController) hasSearchOriginFor(p *pane) bool {
	return c.searchOrigin != nil && c.searchPane == p
}

type graphController struct {
	appgraph.Controller
	graphPending graphPendingState
}

func (c *graphController) invalidateReading(dataset string) { c.InvalidateReading(dataset) }

// readingController owns reading panes, title cache, reading identity tracking,
// reading revisions, and recent-title hydration state.
type readingController struct {
	titles                *ReadingTitles
	detailChunks          *detailChunkCache
	readingIdentity       string
	nextViewIdentityCheck time.Time
	baseReadingIdentity   string
	baseIdentityURL       string
	baseIdentityTokenFile string
	recentCancel          context.CancelFunc
	recentCtx             context.Context
	recentRequest         uint64
	recentScope           string
	readingRevisions      map[string]uint64
	panes                 map[string]*pane
	recent                []Item
}

func (c *readingController) initReading(client Client) {
	c.panes = map[string]*pane{}
	c.titles = newReadingTitles()
	c.detailChunks = newDetailChunkCache()
	if full, base, err := readingIdentityPair(client); err == nil {
		c.readingIdentity = full
		endpointURL, tokenFile, _ := settings.EffectiveReadingEndpoint(client.Config)
		c.updateBaseReadingIdentity(base, endpointURL, tokenFile)
	}
}

func (c *readingController) updateBaseReadingIdentity(base, serviceURL, tokenFile string) {
	if c.baseReadingIdentity != base || c.baseIdentityURL != serviceURL || c.baseIdentityTokenFile != tokenFile {
		c.baseReadingIdentity = base
		c.baseIdentityURL = serviceURL
		c.baseIdentityTokenFile = tokenFile
	}
}

func (c *readingController) paneFor(key string) *pane {
	if c.panes == nil {
		c.panes = map[string]*pane{}
	}
	s := c.panes[key]
	if s == nil {
		s = &pane{
			DetailScroll: map[string]*ui.ScrollState{},
			DetailLists:  map[string]*ui.ListState{},
			Tools:        map[string]bool{},
			Technical:    map[string]bool{},
		}
		c.panes[key] = s
	}
	return s
}

func (c *readingController) cancelRecentTitles() {
	if c.recentCancel != nil {
		c.recentCancel()
		c.recentRequest++
	}
}

func (c *readingController) cancelAndResetPanes() {
	for _, state := range c.panes {
		if state.cancel != nil {
			state.cancel()
		}
		if state.readCancel != nil {
			state.readCancel()
		}
	}
	c.panes = map[string]*pane{}
}

func (c *readingController) invalidateReading(dataset string) {
	if c.titles != nil {
		c.titles.Invalidate(dataset)
	}
	if c.detailChunks != nil {
		c.detailChunks.Invalidate(dataset)
	}
	c.recentScope = ""
	if c.recentCancel != nil {
		c.recentCancel()
		c.recentCancel = nil
	}
	c.recentRequest++
	if dataset == "" {
		c.recent = nil
	} else if len(c.recent) > 0 {
		kept := c.recent[:0]
		for _, item := range c.recent {
			if item.Dataset != dataset {
				kept = append(kept, item)
			}
		}
		clear(c.recent[len(kept):])
		if len(kept) == 0 {
			c.recent = nil
		} else {
			c.recent = kept
		}
	}
	seen := make(map[*pane]bool, len(c.panes))
	for _, s := range c.panes {
		if s != nil && !seen[s] {
			seen[s] = true
			invalidateReadingPane(s, dataset)
		}
	}
}

// overviewController owns overview home state, refresh cadence, overview return
// floors, window activation state, and sidebar project visibility/modal state.
type overviewController struct {
	overviewState            overviewViewState
	overviewFirstUse         overviewFirstUseState
	overviewFirstScreenReady bool
	startupDeferred          deferredStartupState
	overview                 appoverview.ControllerOf[Item]
	focusRefreshAt           time.Time
	focusRefreshNow          func() time.Time
	windowActivation         uint64
	fromOverview             bool
	overviewSourceFloor      map[*pane]int
	windowInactive           bool
	windowHidden             bool
	allProjectsOpen          bool
	allProjectsScroll        ui.ScrollState
	projectVisibility        projectVisibilityState
	homeError                string
}

func (c *overviewController) cancelOverviewRecall() {
	if c.overviewState.RecallCancel != nil {
		c.overviewState.RecallCancel()
		c.overviewState.RecallCancel = nil
	}
}

func (c *overviewController) leaveOverview(panes map[string]*pane) {
	c.overview.CancelAll()
	c.fromOverview = true
	c.overviewSourceFloor = make(map[*pane]int, len(panes))
	for _, p := range panes {
		c.overviewSourceFloor[p] = len(p.History)
	}
}

func (c *overviewController) enterOverview() {
	c.fromOverview = false
	c.overviewSourceFloor = nil
}

func (c *overviewController) resetOverviewFloor(s *pane) {
	if c.fromOverview {
		c.overviewSourceFloor[s] = 0
	}
}

func (c *overviewController) clampOverviewFloor(s *pane, floor, historyLen int) {
	if c.fromOverview {
		c.overviewSourceFloor[s] = min(floor, historyLen)
	}
}

type appShellAdapter struct{ app *App }

var (
	_ shell.Dispatcher    = appShellAdapter{}
	_ shell.Router        = appShellAdapter{}
	_ shell.SessionFence  = appShellAdapter{}
	_ shell.ThemeProvider = appShellAdapter{}
)

func (s appShellAdapter) Dispatch(fn func())      { shell.DispatchFunc(s.app.Update).Dispatch(fn) }
func (s appShellAdapter) Epoch() uint64           { return s.app.epoch }
func (s appShellAdapter) AuthEpoch() uint64       { return s.app.authEpoch }
func (s appShellAdapter) ReadingIdentity() string { return s.app.readingIdentity }
func (s appShellAdapter) InvalidateReading()      { s.app.InvalidateReading("") }
func (s appShellAdapter) Tokens() *kitui.Tokens   { return &tok.Tokens }
func (s appShellAdapter) ReduceMotion() bool      { return s.app.reduceMotion }
func (s appShellAdapter) NavigateOverview()       { s.app.navigate("总览", 0) }
func (s appShellAdapter) NavigateProject(id string) {
	for index, project := range s.app.Config.Projects {
		if project.ID == id {
			s.app.navigate("知识", index)
			return
		}
	}
}
func (s appShellAdapter) NavigateCommon()   { s.app.navigate("通用知识", 0) }
func (s appShellAdapter) NavigateProfiles() { s.app.navigate("用户画像", 0) }
func (s appShellAdapter) NavigateSettings(section string) {
	s.app.settingsOrigin = nil
	s.app.access.OpenProject = ""
	s.app.settingsController.SetSection(section)
	s.app.navigate("设置", 0)
}
func (s appShellAdapter) OpenItem(target shell.ItemTarget) error {
	if target.ProjectID != "" {
		found := false
		for _, p := range overviewProjects(s.app.Config) {
			if p.ID != target.ProjectID {
				continue
			}
			if target.DatasetID == "" {
				target.DatasetID = p.Dataset
			}
			found = p.Dataset == target.DatasetID
			break
		}
		if !found {
			return fmt.Errorf("结果范围已失效，请重新搜索")
		}
	}
	return s.app.openSearchResult(Item{ID: target.ItemID, Dataset: target.DatasetID, Kind: target.Kind,
		SourceDocument: target.SourceDocument, Title: target.Title, Date: target.Date, Preview: target.Preview})
}

func projectRef(p Project) shell.ProjectRef {
	return shell.ProjectRef{ID: p.ID, Name: p.Name, DatasetID: p.Dataset, CodeDatasetID: p.CodeDataset, CodeEnabled: p.CodeEnabled, Path: p.Path}
}

func desktopProject(p shell.ProjectRef) Project {
	return Project{ID: p.ID, Name: p.Name, Dataset: p.DatasetID, CodeDataset: p.CodeDatasetID, CodeEnabled: p.CodeEnabled, Path: p.Path}
}

func newAppAPIClient(control ProjectControlClient, identity string) (*api.Client, error) {
	return api.NewClient(api.Config{
		BaseURL: control.Config.URL, ManagementTokenFile: control.Config.ManagementTokenFile,
		ReadingIdentity: identity, HTTPClient: control.HTTP,
	})
}

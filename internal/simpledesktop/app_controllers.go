package simpledesktop

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// searchController owns unified search modal state, result-origin navigation,
// and reading-page search filters.
type searchController struct {
	searchUI            unifiedSearchState
	searchOrigin        *readingLocation
	searchPane          *pane
	searchAnchor        *Item
	searchResolveCancel context.CancelFunc
	searchType          string
	searchProject       string
	focusSearch         bool
}

func (c *searchController) initSearch() {
	c.searchType = "全部类型"
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

// graphController owns graph rendering host state, graph mode/selection,
// tool-graph modal state, pending step hints, and code-graph indexing state.
type graphController struct {
	graph              *GraphHost
	graphWindow        *mygo.Window
	graphWindowClosed  bool
	graphAssetsDir     string
	graphAssets        map[string][]byte
	graphAssetsCheck   *graphAssetCheck
	graphAssetsChecked bool
	graphAssetsErr     error
	graphAttachErr       error
	graphAttachRequested bool
	graphAttachFn        func(*GraphHost) error
	pendingGraphLoad   *graphLoad
	graphLoadRequest   uint64
	graphError         string
	graphMode          string
	graphSelection     string
	graphLoading       bool
	graphPending       graphPendingState
	toolGraph          toolGraphUIState
	codeStatus         string
	codeBusy           bool
	codeUnknown        bool
	codeRemoteActive   bool
}

func (c *graphController) initGraph(assets string) {
	c.graphMode = "实体关系"
	c.graphAssetsDir = assets
	ensureGraphProtocolAndBridge()
}

func (c *graphController) codeIndexBlocked() bool {
	return c.codeBusy || c.codeUnknown || c.codeRemoteActive
}

func (c *graphController) resetGraphView(mode string) {
	c.graphMode = mode
	c.graph.clear()
	c.graphSelection, c.graphError = "", ""
	c.cancelGraphLoad()
}

func (c *graphController) cancelGraphLoad() {
	c.graphLoadRequest++
	c.pendingGraphLoad = nil
	c.graphLoading = false
}

func (c *graphController) invalidateReading(dataset string) {
	c.pendingGraphLoad = nil
	if dataset == "" || c.graph == nil || c.graph.dataset == "" || c.graph.dataset == dataset {
		c.graph.clear()
		c.graphSelection = ""
		c.cancelGraphLoad()
	}
}

// readingController owns reading panes, title cache, reading identity tracking,
// reading revisions, and recent-title hydration state.
type readingController struct {
	titles                *ReadingTitles
	detailChunks          *detailChunkCache
	readingIdentity       string
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
		c.updateBaseReadingIdentity(base, client.Config.ServiceURL, client.Config.TokenFile)
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
	overviewRefreshAt        time.Time
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
	if c.overviewState.recallCancel != nil {
		c.overviewState.recallCancel()
		c.overviewState.recallCancel = nil
	}
}

func (c *overviewController) leaveOverview(panes map[string]*pane) {
	if c.overviewState.cancel != nil {
		c.overviewState.cancel()
	}
	c.overviewState.generation++
	c.overviewState.Loading = false
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

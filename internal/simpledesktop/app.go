package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/bootstrap"
)

var ink = lightPalette.Text1
var muted = lightPalette.Text3
var border = lightPalette.Border
var blue = lightPalette.Fill1

type pane struct {
	Items                          []Item
	Selected                       *Item
	Offset                         int
	More, Loaded, Loading, Reading bool
	ReadingSource                  bool
	Error                          string
	Query                          string
	ListScroll                     ui.ScrollState
	Knowledge                      knowledgeFeedState
	RawCatalog                     []Item
	RawV2Mode                      bool
	RawNextCursor                  string
	RawAppending                   bool
	CommonV2Mode                   bool
	CommonNav                      []v2KnowledgePageOpts
	CommonCursors                  []string
	CommonPageIndex                int
	CommonNextCursor               string
	CommonPrevCursor               string
	CommonHasPrev                  bool
	CommonTotalFiltered            int
	WidgetList                     ui.ListState
	ListSelection                  int
	DetailScroll                   map[string]*ui.ScrollState
	DetailLists                    map[string]*ui.ListState
	Tools                          map[string]bool
	History                        []Item
	HomeAnchor                     *Item
	HomeOffset                     int
	HomeCategory                   string
	Locating                       bool
	RevealItem                     string
	SourcePage                     *SessionHistoryPage
	SourceReturn                   *readingLocation
	SessionTools                   bool
	SessionSubagents               bool
	SubagentParent                 *Item
	SubagentParentPage             *SessionHistoryPage
	SessionLoadMaxY                float32
	request                        uint64
	readRequest                    uint64
	readCancel                     context.CancelFunc
	cancel                         context.CancelFunc
	titleCancel                    context.CancelFunc
	titleCtx                       context.Context
	titleRequest                   uint64
	Technical                      map[string]bool
}
type App struct {
	searchController
	graphController
	readingController
	overviewController

	userProfiles          userProfilesState
	environment           environmentSetupState
	initialSetup          bool
	integrations          integrationState
	layout                appLayoutState
	projectUI             projectReadingState
	settingsOrigin        *readingLocation
	access                projectAccessState
	commonEdit            commonEditorState
	commonPage            commonKnowledgePageState
	commonGeneration      uint64
	editProject           *Project
	editIndex             int
	settingsMessage       string
	ChooseProjectFolder   func() (string, error)
	ChooseDiagnosticPath  func() (string, error)
	OpenExistingAgent     func(string) error
	OpenExternalURL       func(string) error
	ApplyAppearanceSource func(string)
	ReadOpenAtLoginFn     func() bool
	SetOpenAtLoginFn      func(bool) error
	ShowMainWindowFn      func()
	OpenGlobalSearchFn    func()
	HideTrayFlyoutFn      func()
	SyncTrayIconFn        func()
	QuitAppFn             func(stopServices bool)
	appearance            appearanceState
	desktopPrefs          desktopPreferencesState
	updateCheck           updateCheckState
	motionRuns            struct {
		overviewPage      uint64
		overviewKnowledge uint64
		overviewActivity  uint64
		projectPage       uint64
		projectList       uint64
		rawPage           uint64
		rawList           uint64
		docScrollKey      string
		docEnter          uint64
		docBodyKey        string
		docBodyFade       uint64
	}
	poseEnter           poseEnterTracker
	projectCreate       projectCreateState
	Config              Config
	Client              Client
	Update              func(func())
	page                string
	project             int
	kind                string
	epoch               uint64
	authEpoch           uint64
	service, completion string
	monitor             serviceMonitorState
	trayCache           trayFlyoutCache
	knowledgeWriter     *knowledgeReadWriter
	readingMigrateMu    sync.Mutex
	readingMigrated     map[string]bool
	readingMigrating    map[string]chan struct{}
	configError         string
	md                  markdown.Options
	v4MD                map[v4MDKey]*markdown.Options
}

type v4MDKey struct {
	dark bool
	size float32
}

func New(c Config, err error) *App {
	a := &App{Config: c, Client: Client{Config: c}, page: "总览", kind: "知识", service: "尚未检查", completion: "尚未检查"}
	// Cold-start entrance motion is armed atomically once overviewBootstrap
	// delivers the real snapshot (~40-90ms), preventing un-animated flash or double play.
	if c.GenerationSetupOnly {
		a.page = "设置"
		a.access.SettingsSection = "Agent 与模型"
	}
	a.initReading(a.Client)
	a.initDesktopPreferences()
	a.configureColdPendingStoreFromConfig()
	if err != nil {
		a.configError = err.Error()
		a.initialSetup = c.ConfigFile == "" || (c.FirstInstallDefault && errors.Is(err, os.ErrNotExist))
	}
	if err == nil && c.FirstInstall != nil {
		complete, stateError := bootstrap.FirstInitializationComplete(c.ConfigFile)
		a.initialSetup = !complete
		if stateError != nil {
			a.environment.Error = bootstrap.RedactInstallerText(stateError.Error())
		}
		if a.initialSetup {
			step, stepError := bootstrap.FirstWizardStep(c.ConfigFile)
			a.environment.InitialStep = step
			if stepError != nil {
				a.environment.Error = bootstrap.RedactInstallerText(stepError.Error())
			}
		}
	}
	a.initSearch()
	a.initGraph(c.GraphAssets)
	a.md = markdown.Options{Theme: &markdown.Theme{FontSize: 12, LineHeight: 1.9, Text: ink, Muted: muted, CodeSize: 11.0 / 12, CodeBlockBackground: lightPalette.Fill1, CodeBlockBorder: border}, CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
			row := ui.Row(c).Padding(6, 12).Gap(4).AlignItems(ui.Center)
			row.Children(func() {
				ui.Text(c, strings.ToUpper(b.Language)).FontSize(10).TextColor(muted)
				hoverCopyAction(c, row, "复制代码", func() { c.WriteClipboard(b.Code()) })
			})
			b.Body(c).Label("代码横向滚动")
		})
	}}
	return a
}

var (
	startupDeferredFallbackTimeout = 5 * time.Second
	startupPostFirstFrameDelay     = 450 * time.Millisecond
)

type deferredStartupState struct {
	armed, done    bool
	visibleDone    bool
	gen            uint64
	timer          *time.Timer
	postFrameGen   uint64
	postFrameTimer *time.Timer
	postFrameDone  bool
}

func (s *deferredStartupState) stopTimer() {
	s.gen++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *deferredStartupState) stopAllTimers() {
	s.stopTimer()
	s.postFrameGen++
	if s.postFrameTimer != nil {
		s.postFrameTimer.Stop()
		s.postFrameTimer = nil
	}
}

func (a *App) Start() {
	if a.initialSetup {
		if a.Config.FirstInstall != nil && !a.environment.RuntimeStarted {
			a.beginFirstRuntime(a.environment.InitialStep)
			return
		}
		if a.environment.InitialStep >= 2 {
			a.RefreshIntegrations()
			return
		}
		a.refreshEnvironment()
		return
	}
	if a.Config.FirstInstall != nil && !a.environment.RuntimeStarted {
		a.beginFirstRuntime(0)
		return
	}
	if a.Config.GenerationSetupOnly {
		a.RefreshIntegrations()
		return
	}
	s := &a.startupDeferred
	s.stopAllTimers()
	update := a.Update
	s.armed, s.done, s.visibleDone, s.postFrameDone = update != nil && a.page == "总览", false, false, false
	if s.armed {
		a.overviewFirstScreenReady = false
		a.monitor.PollEnabled = false
	}
	a.loadRecent()
	a.RefreshProjectDirectory()
	a.startOverviewFirstUseDaysLoad(time.Now())
	if !s.armed {
		a.runDeferredStartupTasks()
		return
	}
	gen := s.gen
	s.timer = time.AfterFunc(startupDeferredFallbackTimeout, func() {
		update(func() {
			if gen == a.startupDeferred.gen {
				a.runDeferredStartupTasks()
			}
		})
	})
}

func (a *App) resetDeferredStartupFallback() {
	s := &a.startupDeferred
	update := a.Update
	if !s.armed || (s.done && s.visibleDone) || update == nil {
		return
	}
	s.stopTimer()
	gen := s.gen
	s.timer = time.AfterFunc(startupDeferredFallbackTimeout, func() {
		update(func() {
			if gen == a.startupDeferred.gen {
				if !a.startupDeferred.done {
					a.runDeferredStartupTasks()
				} else if !a.startupDeferred.visibleDone {
					a.releaseVisibleStartupTasks(false)
				}
			}
		})
	})
}

func (a *App) onOverviewBootstrapSettled() {
	a.overviewFirstScreenReady = true
	if a.startupDeferred.armed {
		if !a.startupDeferred.done {
			a.runDeferredStartupTasks()
		} else if !a.startupDeferred.visibleDone {
			a.releaseVisibleStartupTasks(false)
		}
	}
}

func (a *App) runDeferredStartupTasks() {
	a.finishDeferredStartup(false)
}

func (a *App) finishDeferredStartup(settingsRefreshed bool) {
	s := &a.startupDeferred
	if s.done {
		if !s.visibleDone && !a.windowHidden {
			a.releaseVisibleStartupTasks(settingsRefreshed)
		}
		return
	}
	s.stopTimer()
	s.done = true
	a.triggerProvenanceBackfillOnce()
	a.restoreCodeState()
	if !settingsRefreshed {
		a.RefreshIntegrations()
	}
	if a.windowHidden {
		a.schedulePostFirstFrameStartup(settingsRefreshed)
		return
	}
	a.releaseVisibleStartupTasks(settingsRefreshed)
}

func (a *App) releaseVisibleStartupTasks(settingsRefreshed bool) {
	s := &a.startupDeferred
	if s.visibleDone {
		return
	}
	s.stopTimer()
	s.visibleDone = true
	a.overviewFirstScreenReady = true
	a.monitor.PollEnabled = true
	if !a.windowInactive && !a.windowHidden {
		a.health()
		a.verifyGraphAssetsAsync()
		if !settingsRefreshed {
			a.refreshSidebarQueueIfDue(time.Now())
		}
	}
	a.schedulePostFirstFrameStartup(settingsRefreshed)
}

func (a *App) schedulePostFirstFrameStartup(skipLoginRefresh bool) {
	s := &a.startupDeferred
	run := func() {
		s.postFrameTimer = nil
		s.postFrameDone = true
		if !skipLoginRefresh && !a.desktopPrefs.LoginSynced {
			a.RefreshOpenAtLoginAsync()
		}
		if !a.windowInactive && !a.windowHidden {
			a.PrewarmGraph()
		}
	}
	update := a.Update
	if update == nil || startupPostFirstFrameDelay <= 0 {
		run()
		return
	}
	s.postFrameGen++
	if s.postFrameTimer != nil {
		s.postFrameTimer.Stop()
	}
	gen := s.postFrameGen
	s.postFrameTimer = time.AfterFunc(startupPostFirstFrameDelay, func() {
		update(func() {
			if gen == a.startupDeferred.postFrameGen {
				run()
			}
		})
	})
}

func (a *App) cancelAndResetPanes() {
	a.startupDeferred.stopAllTimers()
	a.readingController.cancelAndResetPanes()
}

func (a *App) FlushPendingState(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	var readErr, coldErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readErr = a.flushKnowledgeReadWithTimeout(timeout)
	}()
	go func() {
		defer wg.Done()
		coldErr = flushColdPendingStoreWithTimeout(timeout)
	}()
	wg.Wait()
	return errors.Join(readErr, coldErr)
}

func (a *App) stepCommonKnowledgeV2(s *pane, delta int) {
	if s == nil || !s.CommonV2Mode || s.Loading {
		return
	}
	s.HomeAnchor = nil
	if delta < 0 {
		if s.CommonPageIndex > 0 {
			s.CommonPageIndex--
			s.Offset = s.CommonPageIndex * 30
			a.load(s)
			return
		}
		if !s.CommonHasPrev {
			return
		}
		prevOpts := v2KnowledgePageOpts{Cursor: s.CommonPrevCursor, Direction: "prev"}
		if prevOpts.Cursor == "" && len(s.Items) > 0 {
			prevOpts.AnchorDocID = s.Items[0].ID
		}
		if len(s.CommonNav) == 0 {
			s.CommonNav = []v2KnowledgePageOpts{{}}
		}
		s.CommonNav = append([]v2KnowledgePageOpts{prevOpts}, s.CommonNav...)
		s.CommonCursors = append([]string{prevOpts.Cursor}, s.CommonCursors...)
		s.CommonPageIndex = 0
		s.Offset = 0
		a.load(s)
		return
	}
	if delta > 0 {
		if !s.More || s.CommonNextCursor == "" {
			return
		}
		if len(s.CommonNav) == 0 {
			s.CommonNav = []v2KnowledgePageOpts{{}}
		}
		if len(s.CommonCursors) == 0 {
			s.CommonCursors = []string{""}
		}
		nextOpts := v2KnowledgePageOpts{Cursor: s.CommonNextCursor, Direction: "next"}
		nextIdx := s.CommonPageIndex + 1
		if nextIdx < len(s.CommonNav) {
			s.CommonNav[nextIdx] = nextOpts
		} else {
			s.CommonNav = append(s.CommonNav, nextOpts)
		}
		if nextIdx < len(s.CommonCursors) {
			s.CommonCursors[nextIdx] = s.CommonNextCursor
		} else {
			s.CommonCursors = append(s.CommonCursors, s.CommonNextCursor)
		}
		s.CommonPageIndex = nextIdx
		s.Offset = s.CommonPageIndex * 30
		a.load(s)
	}
}

func (a *App) health() {
	a.refreshServiceMonitor()
}
func (a *App) loadRecent()      { a.loadOverview() }
func (a *App) routeKey() string { return a.currentRoute().Key() }
func (a *App) state() *pane {
	return a.paneFor(a.routeKey())
}
func (a *App) projects() []Project {
	if a.page == "通用知识" {
		return a.Config.Common
	}
	return a.Config.Projects
}
func (a *App) current() (Project, bool) {
	ps := a.projects()
	if a.project >= 0 && a.project < len(ps) {
		return ps[a.project], true
	}
	return Project{}, false
}
func (a *App) navigate(page string, index int) { a.navigateKind(page, index, "") }
func (a *App) navigateKind(page string, index int, kind string) {
	a.cancelOverviewRecall()
	a.commonPage.MenuOpen = false
	a.projectUI.InfoOpen = false
	a.projectUI.MenuOpen = false
	a.closeUnifiedSearch()
	a.clearSearchOrigin()
	if page != "设置" {
		a.settingsOrigin = nil
	}
	if a.page == "总览" && page != "总览" {
		a.leaveOverview(a.panes)
	}
	if page == "总览" {
		a.enterOverview()
		a.motionRuns.overviewPage++
		a.motionRuns.overviewKnowledge++
		a.motionRuns.overviewActivity++
		a.overviewState.heatmapGrowStart = time.Time{}
		a.overviewState.overviewColdStartMotion = false
	}
	if page == "知识" {
		a.motionRuns.projectPage++
	}
	a.cancelRecentTitles()
	a.epoch++
	a.cancelGraphLoad()
	a.graph.clear()
	for _, p := range a.panes {
		p.Knowledge.RecallFilter = nil
		p.Knowledge.RecallAgentID = ""
		p.Knowledge.RecallEventID = ""
		p.Knowledge.CategoryFilter = ""
		p.Knowledge.UnreadOnly = false
		p.Knowledge.Detail = false
		p.HomeAnchor, p.Locating, p.RevealItem = nil, false, ""
		a.cancelTitles(p)
		if p.readCancel != nil {
			p.readCancel()
		}
		if p.cancel != nil {
			p.cancel()
			p.Loading = false
			p.Reading = false
		}
	}
	a.page = page
	if page == "用户画像" {
		a.refreshUserProfiles()
	}
	if page == "设置" {
		a.RefreshIntegrations()
		a.RefreshOpenAtLoginAsync()
		if a.startupDeferred.armed && (!a.startupDeferred.done || !a.startupDeferred.visibleDone) {
			a.finishDeferredStartup(true)
		}
	}
	a.project = index
	if page == "图谱" {
		a.EnsureGraphAttached()
	}
	if page == "总览" {
		if !a.overviewState.Loaded || a.overviewState.scope != a.overviewScope() {
			a.loadOverview()
		} else if a.overviewRefreshAt.IsZero() || !time.Now().Before(a.overviewRefreshAt) {
			// Defer stale background refresh until after the 780ms enter animation
			// settles so returning from a project never hitches mid-transition.
			a.overviewRefreshAt = time.Now().Add(overviewRefreshInterval)
			navEpoch := a.epoch
			if a.Update != nil {
				time.AfterFunc(850*time.Millisecond, func() {
					a.Update(func() {
						if !a.windowInactive && !a.windowHidden && a.page == "总览" && a.epoch == navEpoch {
							a.loadOverview()
						}
					})
				})
			} else {
				a.loadOverview()
			}
		}
	}
	if page == "知识" {
		a.kind = "知识"
		if kind != "" {
			a.kind = kind
		}
		if a.kind == "管理" && index >= 0 && index < len(a.Config.Projects) {
			a.access.OpenProject = a.Config.Projects[index].ID
		}
	}
	if page == "通用知识" {
		a.kind = "知识"
	}
	if page != "总览" && page != "设置" && page != "图谱" && page != "用户画像" && !(page == "知识" && a.kind == "管理") {
		s := a.state()
		if page == "知识" && a.kind == "知识" {
			if s.Loaded && len(s.Items) > 0 {
				a.motionRuns.projectList++
			} else {
				// Avoid a second staggered bounce when items load asynchronously
				// after knowledge-list-body has already completed Page Rise.
				a.motionRuns.projectList = 0
			}
			a.loadKnowledgeFeed(s)
		} else {
			if page == "知识" && a.kind != "知识" && a.kind != "管理" {
				if s.Loaded && len(s.Items) > 0 {
					a.motionRuns.rawPage++
					a.motionRuns.rawList++
				} else {
					a.motionRuns.rawPage = 0
					a.motionRuns.rawList = 0
				}
			}
			a.load(s)
		}
	}
}
func (a *App) load(s *pane) {
	a.syncReadingIdentity()
	if a.page == "知识" && a.kind == "知识" && s.Knowledge.Initialized && s.HomeAnchor == nil && s.Query == "" {
		a.loadKnowledgeFeed(s)
		return
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if a.page == "通用知识" && s.HomeCategory != a.commonCategoryFilter() {
			s.HomeAnchor = nil
		}
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if s.Offset == s.HomeOffset && (s.Selected == nil || s.Selected.key() == s.HomeAnchor.key()) {
			a.locateOverviewItem(s, *s.HomeAnchor)
			return
		}
		s.HomeAnchor = nil
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch, authEpoch := s.request, a.epoch, a.authEpoch
	refreshing := s.Loaded
	s.Loading = true
	s.Error = ""
	p, ok := a.current()
	page, kind, offset, q := a.page, a.kind, s.Offset, s.Query
	rawFeed := page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") && q == ""
	rawCount := max(30, len(s.Items))
	category := a.commonCategoryFilter()
	commonPageIdx := s.CommonPageIndex
	var commonOpts v2KnowledgePageOpts
	if commonPageIdx >= 0 && commonPageIdx < len(s.CommonNav) {
		commonOpts = s.CommonNav[commonPageIdx]
	} else if commonPageIdx > 0 && commonPageIdx < len(s.CommonCursors) {
		commonOpts = v2KnowledgePageOpts{Cursor: s.CommonCursors[commonPageIdx]}
	}
	commonOpts.RecallAgentID, commonOpts.RecallEventID = s.Knowledge.RecallAgentID, s.Knowledge.RecallEventID
	recallStartedAt := a.overviewState.V2RecallStartedAt
	filter := a.searchType
	if page != "搜索" {
		if kind == "知识" {
			filter = "知识"
		} else {
			filter = "原始材料"
		}
	}
	ps := a.projects()
	if page == "搜索" {
		ps = searchProjects(a.Config.Projects, a.searchProject)
	}
	client := a.Client
	control, hasControl := a.accessClient()
	titles := a.titles
	genStr := a.readingGeneration(s)
	recallFilter := map[string]bool{}
	for id, yes := range s.Knowledge.RecallFilter {
		recallFilter[id] = yes
	}
	go func() {
		defer cancel()
		var rows []Item
		var rawCatalog []Item
		var rawCacheReady bool
		var usedFast bool
		var usedV2Raw bool
		var v2RawNextCursor string
		var usedV2Common bool
		var v2CommonNextCursor string
		var v2CommonPrevCursor string
		var v2CommonHasPrev bool
		var v2CommonTotal int
		var v2CommonCursorReset bool
		var more bool
		var err error
		if page == "搜索" || q != "" {
			if q == "" {
				rows = []Item{}
			} else {
				if page != "搜索" {
					ps = []Project{p}
				}
				rows, err = client.search(ctx, ps, q, filter)
				if page == "通用知识" {
					filtered := []Item{}
					for _, row := range rows {
						if row.CommonCategory != "" && (category == "" || row.CommonCategory == category) {
							filtered = append(filtered, row)
						}
					}
					rows = filtered
				}
			}
		} else if !ok {
			rows = []Item{}
		} else if p.Dataset == "" {
			err = fmt.Errorf("项目正在登记，内容归属尚未确认")
		} else if page == "通用知识" {
			if hasControl {
				usedV2Common = true
				v2Resp, v2Err := control.commonKnowledgePageV2(ctx, p.Dataset, category, 30, commonOpts)
				if v2Err != nil && isControlInvalidCursor(v2Err) && (commonOpts.Cursor != "" || commonOpts.AnchorDocID != "") {
					// Explicitly reset expired cursor to page 1 under the same filter
					// instead of silently falling back to legacy offset pagination.
					v2CommonCursorReset = true
					v2Resp, v2Err = control.commonKnowledgePageV2(ctx, p.Dataset, category, 30, v2KnowledgePageOpts{
						RecallAgentID: commonOpts.RecallAgentID, RecallEventID: commonOpts.RecallEventID,
					})
				}
				if v2Err == nil {
					v2CommonNextCursor = v2Resp.NextCursor
					v2CommonPrevCursor = v2Resp.PrevCursor
					v2CommonHasPrev = v2Resp.HasPrev
					v2CommonTotal = v2Resp.TotalFiltered
					more = v2Resp.HasMore
					rows = make([]Item, 0, len(v2Resp.Items))
					for _, itemRow := range v2Resp.Items {
						if itemRow.DocID == "" {
							continue
						}
						rows = append(rows, itemRow.toItem(recallStartedAt))
					}
				} else {
					err = v2Err
				}
			}
			if !usedV2Common {
				if len(recallFilter) == 0 {
					rows, more, err = client.commonDocuments(ctx, p, offset, category)
				} else {
					var all []Item
					all, err = client.commonDocumentItems(ctx, p, category)
					for _, row := range all {
						if recallFilter[row.ID] {
							rows = append(rows, row)
						}
					}
					start := min(offset, len(rows))
					end := min(start+30, len(rows))
					more = end < len(rows)
					rows = rows[start:end]
				}
			}
		} else if page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") {
			if hasControl {
				usedV2Raw = true
				resp, readErr := control.rawRecordsV2(ctx, p.ID, kind, 30, "")
				if readErr != nil {
					err = readErr
				} else {
					rows, err = v2RawRecordsToItems(p.Dataset, resp)
					more = resp.HasMore
					rawCacheReady = resp.CacheReady
					v2RawNextCursor = resp.NextCursor
					usedFast = true
				}
			} else {
				rawCatalog, err = client.projectRecordCatalog(ctx, p, kind)
				if err == nil {
					end := min(rawCount, len(rawCatalog))
					rows = append([]Item{}, rawCatalog[:end]...)
					more = end < len(rawCatalog)
					if !refreshing && !rawCacheReady && len(rows) > 0 {
						preCtx, preCancel := context.WithTimeout(ctx, 500*time.Millisecond)
						limit := min(10, len(rows))
						var preMu sync.Mutex
						var preWg sync.WaitGroup
						for idx := 0; idx < limit; idx++ {
							if rows[idx].Session == nil || rows[idx].ReadingState == "ready" {
								continue
							}
							idx := idx
							item := rows[idx]
							preWg.Add(1)
							go func() {
								defer preWg.Done()
								if res, e := titles.read(preCtx, client, item, genStr, true, true); e == nil {
									preMu.Lock()
									rows[idx].Title = res.Title
									rows[idx].SessionSummary = res.SessionSummary
									rows[idx].ReadingState = "ready"
									preMu.Unlock()
								}
							}()
						}
						preWg.Wait()
						preCancel()
					}
				}
			}
		} else if kind == "会话" {
			rows, more, err = client.sessions(ctx, p, offset)
		} else {
			rows, more, err = client.documents(ctx, p, offset, kind == "文档")
		}
		a.Update(func() {
			if a.epoch != epoch || a.authEpoch != authEpoch || s.request != request {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = controlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
				}
				return
			}
			if a.searchPane == s && a.searchAnchor != nil && !containsReadingItem(rows, a.searchAnchor.key()) {
				rows = append([]Item{*a.searchAnchor}, rows...)
			}
			if s.SourceReturn != nil && sourceSession(s) && !containsReadingItem(rows, s.Selected.key()) {
				rows = append([]Item{*s.Selected}, rows...)
			}
			if refreshing && !rawCacheReady && !usedV2Common {
				preserveResolvedReadingTitles(rows, s.Items)
			}
			s.Items = rows
			if page == "通用知识" {
				s.CommonV2Mode = usedV2Common
				if usedV2Common {
					if v2CommonCursorReset || len(s.CommonNav) == 0 {
						s.CommonNav = []v2KnowledgePageOpts{{}}
						s.CommonCursors = []string{""}
						s.CommonPageIndex = 0
						s.Offset = 0
					}
					s.CommonNextCursor = v2CommonNextCursor
					s.CommonPrevCursor = v2CommonPrevCursor
					s.CommonHasPrev = v2CommonHasPrev
					s.CommonTotalFiltered = v2CommonTotal
				} else {
					s.CommonNav = nil
					s.CommonCursors = nil
					s.CommonPageIndex = 0
					s.CommonNextCursor = ""
					s.CommonPrevCursor = ""
					s.CommonHasPrev = false
					s.CommonTotalFiltered = 0
				}
			}
			if rawFeed {
				s.RawV2Mode = usedV2Raw
				s.RawNextCursor = v2RawNextCursor
				s.RawAppending = false
				if usedV2Raw {
					s.RawCatalog = nil
				} else {
					s.RawCatalog = rawCatalog
				}
				s.Offset = 0
				if !refreshing && len(rows) > 0 && a.page == "知识" && a.kind == kind {
					a.motionRuns.rawPage++
					a.motionRuns.rawList++
				}
			}
			s.More = more
			s.Loaded = true
			if !usedV2Common {
				a.startTitles(s)
			}
			if len(s.History) > 0 {
				return
			}
			if s.Selected != nil {
				targetKey := s.Selected.key()
				if s.SubagentParent != nil {
					targetKey = s.SubagentParent.key()
				}
				for _, i := range rows {
					if i.key() == targetKey {
						if refreshing && s.SubagentParent == nil {
							a.selectItem(s, i)
						}
						return
					}
				}
				if page != "搜索" && q == "" {
					s.Selected = nil
					s.SubagentParent, s.SubagentParentPage = nil, nil
					s.SessionSubagents = false
					s.Error = "选中内容已不在当前官方页面，请重新选择"
				}
			} else if rawFeed && len(s.Items) > 0 && !s.Locating && (usedFast || s.Items[0].Session != nil) {
				a.selectItem(s, s.Items[0])
			}
		})
	}()
}
func (a *App) selectItem(s *pane, i Item) {
	if a.page == "知识" && a.kind == "知识" {
		s.Knowledge.Detail = true
	}
	if s.Locating {
		if s.cancel != nil {
			s.cancel()
		}
		s.request++
		s.Loading, s.Locating = false, false
	}
	if s.HomeAnchor != nil && s.HomeAnchor.key() != i.key() {
		s.HomeAnchor = nil
	}
	s.SourcePage, s.SessionTools, s.SessionSubagents, s.SessionLoadMaxY = nil, false, false, 0
	s.SubagentParent, s.SubagentParentPage = nil, nil
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
	generation := a.readingGeneration(s)
	s.Reading = true
	s.History = nil
	s.SourceReturn = nil
	a.resetOverviewFloor(s)
	s.Error = ""
	if s.Selected == nil || s.Selected.key() != i.key() || keyForReading("", *s.Selected, generation) != keyForReading("", i, generation) {
		s.Selected = &i
	}
	client, titles := a.Client, a.titles
	control, hasControl := a.accessClient()
	revision := a.readingRevisions[i.key()]
	go func() {
		defer cancel()
		var result Item
		var sessionPage *SessionHistoryPage
		var err error
		if i.Document != nil {
			result, err = titles.read(ctx, client, i, generation, true, false)
		} else if i.Session != nil {
			result, sessionPage, err = fetchSessionHistoryPage(ctx, control, hasControl, client, i)
		} else {
			result, err = client.read(ctx, i)
		}
		if err == nil && result.Kind == "知识" && hasControl {
			statsCtx, statsCancel := context.WithTimeout(ctx, 2*time.Second)
			result.RecallStats, _ = control.knowledgeRecall(statsCtx, result)
			statsCancel()
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest || revision != a.readingRevisions[i.key()] {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				a.handleReadingFailure(i, err)
				return
			}
			s.Selected = &result
			s.SourcePage = sessionPage
			s.SessionSubagents = false
			if subagentDefaultTools(result, sessionPage) {
				s.SessionTools = true
			}
			a.publishOverviewTitle(result)
			for idx, row := range s.Items {
				if row.key() == result.key() {
					s.Items[idx].Title = result.Title
					if result.SessionSummary != "" {
						s.Items[idx].SessionSummary = result.SessionSummary
					}
					if len(result.Subagents) > 0 {
						s.Items[idx].Subagents = result.Subagents
					}
					if result.Session != nil {
						sCopy := *result.Session
						s.Items[idx].Session = &sCopy
					}
					if result.ParentTitle != "" {
						s.Items[idx].ParentTitle = result.ParentTitle
					}
					if result.ReadingState == "ready" {
						s.Items[idx].ReadingState = "ready"
					}
				}
			}
		})
	}()
}
func (a *App) source(s *pane) {
	if s.Selected == nil || s.Selected.Source == "" && s.Selected.SourceDocument == "" {
		return
	}
	original := *s.Selected
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch, authEpoch := s.readRequest, a.epoch, a.authEpoch
	s.Reading = true
	s.ReadingSource = true
	client := a.Client
	control, hasControl := a.accessClient()
	titles, generation := a.titles, a.readingGeneration(s)
	go func() {
		defer cancel()
		var i Item
		var sessionPage *SessionHistoryPage
		var err error
		if original.SourceDocument != "" {
			i, err = client.documentInfoByID(ctx, original.Dataset, original.SourceDocument)
			if err == nil {
				i, err = titles.read(ctx, client, i, generation, true, false)
			}
		} else if hasControl {
			target := Item{ID: original.Source, Dataset: original.Dataset, Kind: "会话", Session: &Session{ID: original.Source, Dataset: original.Dataset}}
			page, fastErr := control.rawSessionHistory(ctx, target, 0, 0)
			if fastErr == nil {
				i, sessionPage = sessionItemFromHistoryPage(target, page)
			} else if !shouldFallbackFromRawControlError(ctx, fastErr) {
				err = fastErr
			} else {
				i, sessionPage, err = client.sourceWithHistory(ctx, original)
			}
		} else {
			i, sessionPage, err = client.sourceWithHistory(ctx, original)
		}
		a.Update(func() {
			if epoch != a.epoch || authEpoch != a.authEpoch || request != s.readRequest {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			s.ReadingSource = false
			if err != nil {
				s.Error = err.Error()
				if i.ID != "" {
					a.handleReadingFailure(i, err)
				} else if status, ok := httpStatusCode(err); ok && status == 401 {
					a.InvalidateReading("")
				}
				return
			}
			// Stay in place on the current reading pane so the memory list
			// remains visible and returning is one click. Users can still jump
			// to the Raw Records tab via the action in the source context bar.
			s.History = append(s.History, original)
			s.Selected = &i
			s.SubagentParent, s.SubagentParentPage = nil, nil
			s.SourcePage, s.SessionTools, s.SessionSubagents = sessionPage, subagentDefaultTools(i, sessionPage), false
		})
	}()
}
func button(c *ui.Context, label string, fn func()) {
	ui.Button(c, label).FontSize(12).Padding(8, 12).Background(ui.Transparent).BorderWidth(0).Justify(ui.Start).OnClick(fn)
}
func (a *App) View(c *ui.Context) {
	applyAppearance(a.effectiveDark(c.Theme().Dark))
	if a.initialSetup {
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.initialEnvironmentPage(c) })
		return
	}
	if a.Config.GenerationSetupOnly {
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.generationSetupPage(c) })
		return
	}
	root := ui.Themed(c, homeTheme).Fill().FontWeight(400)
	if c.Vibrancy() {
		// The sidebar sits over the window material; content paints its own background.
		c.Root().Background(ui.Transparent)
		root.Background(ui.Transparent)
	}
	root.Children(func() {
		a.viewContent(c)
		a.knowledgeDialog(c)
		a.searchDialog(c)
		a.projectCreateDialog(c)
		a.allProjectsDialog(c)
		a.toolGraphDialog(c)
		a.forceQuitConfirmDialog(c)
		if a.page == "用户画像" {
			a.userProfileImportDialog(c)
		}
	})
}
func (a *App) viewContent(c *ui.Context) {
	if !a.windowHidden {
		c.After(2 * time.Second)
		a.syncReadingIdentity()
		if time.Now().After(a.access.Next) {
			a.RefreshProjectDirectory()
		}
	}
	if !a.windowHidden && a.page == "总览" && a.Update != nil {
		if a.Config.ProjectControl != nil && !a.access.Loaded {
			if a.access.DirectoryError != "" {
				a.overviewState.Loading, a.overviewState.ActivityLoading = false, false
				a.overviewState.Error = a.access.DirectoryError
			}
		} else if a.overviewState.scope != a.overviewScope() {
			a.loadOverview()
		}
	}
	a.refreshOverviewIfDue(time.Now())
	a.mainShell(c)
}
func (a *App) mainShell(c *ui.Context) {
	shell := ui.Row(c.Key("main-shell")).Fill().AlignItems(ui.Stretch)
	if !c.Vibrancy() {
		shell.Background(tok.Sidebar)
	}
	shell.Children(func() {
		a.navigation(c)
		// V4: the title bar and content share one opaque surface; no floating panel.
		ui.Column(c).Grow(1).MinWidth(0).Background(tok.Bg).BorderWidth(0, 0, 0, 1).BorderColor(tok.Border).Children(func() {
			a.projectHeader(c)
			ui.Column(c.Key("workspace")).Grow(1).MinHeight(0).FillWidth().Clip().Children(func() {
				if a.configError != "" {
					ui.Text(c, "运行配置不可用："+a.configError).Margin(24).TextColor(ink)
					return
				}
				// The graph owns its navigation row; reading tabs live in the list pane.
				if a.page == "图谱" {
					a.projectNavigation(c)
				}
				switch a.page {
				case "用户画像":
					a.userProfilesPage(c)
				case "总览":
					a.home(c)
				case "设置":
					a.settings(c)
				case "图谱":
					a.graphView(c)
				case "通用知识":
					a.commonKnowledgePage(c)
				case "知识":
					a.projectReading(c)
				default:
					a.reading(c)
				}
			})
		})
	})
}
func (a *App) navigation(c *ui.Context) { a.overviewNavigation(c) }
func (a *App) home(c *ui.Context)       { a.overviewHome(c) }
func (a *App) settings(c *ui.Context) {
	if a.settingsOrigin != nil && a.access.OpenProject != "" {
		a.projectSettingsPage(c)
		return
	}
	a.globalSettingsPage(c)
}
func (a *App) reading(c *ui.Context) {
	s := a.state()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(316).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(border).Children(func() {
			ui.Column(c).Padding(24, 18, 16, 18).Gap(12).Children(func() {
				label := "项目内容"
				if a.page == "通用知识" {
					label = "通用记忆"
				}
				ui.Text(c, label).FontSize(10).TextColor(muted)
				if a.page != "搜索" {
					ui.Row(c).Gap(3).Children(func() {
						if a.page != "通用知识" {
							button(c, "图谱", func() { a.navigate("图谱", a.project); a.loadGraph() })
						}
						for _, k := range []string{"知识", "会话", "文档"} {
							if a.page == "通用知识" && k != "知识" {
								continue
							}
							kind := k
							label := kind
							if k == "会话" {
								label = "原始记录"
							}
							button(c, memoryKindLabel(label), func() {
								a.cancelTitles(s)
								a.epoch++
								if s.cancel != nil {
									s.cancel()
								}
								a.kind = kind
								next := a.state()
								if !next.Loaded {
									a.load(next)
								}
							})
						}
					})
				} else {
					catalog := append([]Project(nil), a.Config.Projects...)
					options := []string{"全部项目"}
					options = append(options, projectLabels(catalog)...)
					selected := options[0]
					for index, p := range a.Config.Projects {
						if p.ID == a.searchProject {
							selected = options[index+1]
							break
						}
					}
					ui.Select(c, &selected, options).Width(270).OnChange(func() {
						if selected == options[0] {
							a.searchProject = ""
						}
						for index, label := range options[1:] {
							if label == selected {
								a.searchProject = catalog[index].ID
								break
							}
						}
						s.Offset = 0
						a.load(s)
					})
					ui.Select(c, &a.searchType, []string{"全部类型", "知识", "原始材料"}).Width(270).OnChange(func() { s.Offset = 0; a.load(s) })
					ui.Text(c, "最多30个官方检索片段；类型按返回元数据筛选").FontSize(10).TextColor(muted)
				}
				if a.page == "通用知识" {
					a.commonToolbar(c)
					options := []string{}
					for _, p := range a.Config.Common {
						options = append(options, p.Name)
					}
					if len(options) > 0 {
						selected := options[min(a.project, len(options)-1)]
						ui.Select(c, &selected, options).Width(270).OnChange(func() {
							for idx, name := range options {
								if name == selected {
									a.navigate("通用知识", idx)
								}
							}
						})
					}
				}

				ui.Row(c).Children(func() {
					ui.Text(c, fmt.Sprintf("已载入 %d 项", len(s.Items))).FontSize(10).TextColor(muted)
					ui.Spacer(c)
					refreshAction(c, "刷新列表", func() { a.load(s) })
				})
				if s.Loading && !s.Loaded {
					ui.Text(c, "正在读取…").FontSize(10).TextColor(muted)
				}
				if s.Error != "" {
					ui.Text(c, s.Error).FontSize(11).TextColor(muted)
				}
			})
			ui.Scroll(c.Key("list-"+a.routeKey())).Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(0, 10).Gap(4).Children(func() {
				if s.Loaded && len(s.Items) == 0 {
					label := "当前页面没有内容"
					if a.page == "搜索" && s.Query == "" {
						label = "输入关键词搜索官方内容"
					}
					if a.page == "通用知识" && len(a.Config.Common) == 0 {
						label = "官方当前暂不支持个人偏好与通用经验的独立阅读。后续可使用独立的通用记忆数据组，供所有 Agent 读取。"
					}
					if s.Query != "" {
						label = "没有匹配结果"
					}
					if s.Loading {
						label = "正在读取…"
					} else if s.Error != "" {
						label = "内容读取失败，请刷新"
					}
					ui.Text(c, label).Margin(12).FontSize(12).TextColor(muted)
				}
				for _, row := range s.Items {
					i := row
					b := ui.Button(c, "").Key("item-"+i.key()).Justify(ui.Start).Padding(14, 12).Background(ui.Transparent).BorderWidth(0).Radius(6).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
							ui.Text(c, i.Title).MinWidth(0).FontSize(13).Bold().SingleLine().TextColor(ink)
							ui.Text(c, memoryKindLabel(i.Kind)+" · "+displayDate(i.Date)).MinWidth(0).FontSize(10).TextColor(muted).SingleLine()
							if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
								button(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
							}
						})
					}).OnClick(func() { a.selectItem(s, i) })
					if s.Selected != nil && s.Selected.key() == i.key() {
						b.Background(tok.AccentSoft)
					}
				}
			})
			ui.Row(c).Height(48).Shrink(0).Padding(0, 12).BorderWidth(1, 0, 0, 0).BorderColor(border).Children(func() {
				ui.Button(c, "‹").Disabled(s.Offset == 0 || s.Loading || s.Query != "").OnClick(func() { s.Offset = max(0, s.Offset-30); a.load(s) })
				ui.Text(c, fmt.Sprintf("%d", s.Offset/30+1)).FontSize(11)
				ui.Button(c, "›").Disabled(!s.More || s.Loading || s.Query != "").OnClick(func() { s.Offset += 30; a.load(s) })
			})
		})
		if a.page == "通用知识" && a.commonEdit.Open {
			a.commonEditor(c)
		} else {
			a.detail(c, s)
		}
	})
}
func (a *App) detail(c *ui.Context, s *pane) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		if s.Selected == nil {
			ui.Text(c, "选择一项内容开始阅读").FontSize(14).TextColor(muted).Margin(30)
			return
		}
		i := s.Selected
		if len(s.History) > 0 {
			ui.Row(c).Height(42).Shrink(0).Padding(0, 22).Children(func() {
				button(c, "‹ 返回阅读", func() { a.returnFromSource(s) })
			})
		}
		scroll := s.DetailScroll[i.key()]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[i.key()] = scroll
		}
		ui.Scroll(c.Key("detail-"+i.key())).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(28, 30).Gap(18).Children(func() {
			ui.Text(c, memoryKindLabel(i.Kind)+" / 只读").FontSize(10).TextColor(muted)
			presentation := itemPresentation(*i)
			ui.Text(c, presentation.DisplayTitle).FontSize(21).Bold().TextColor(ink)
			ui.Text(c, displayDate(i.Date)).FontSize(10).TextColor(muted)
			a.commonActions(c, *i)
			if s.Reading && !i.BodyLoaded && i.Detail == nil {
				ui.Text(c, "正在读取…").FontSize(10).TextColor(muted)
			}
			if i.Detail != nil {
				mdOpts := a.v4MarkdownOptions(fsReading)
				ui.Text(c, fmt.Sprintf("官方已载入 %d/%d 条问答；工具记录独立列示", len(i.Detail.QAs), i.Detail.Count)).FontSize(fsCaption).TextColor(tok.Text3)
				for _, q := range i.Detail.QAs {
					ui.Column(c).Padding(11, 14).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Gap(6).Children(func() {
						ui.Text(c, "用户").FontSize(fsCaption).TextColor(tok.Text3)
						markdown.View(c.Key("q-"+q.ID), q.Question, mdOpts)
						if q.QuestionTruncated {
							ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
						}
					})
					sourceAgentLabel(c, *i)
					markdown.View(c.Key("a-"+q.ID), q.Answer, mdOpts)
					if q.AnswerTruncated {
						ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
					}
				}
				for _, trace := range i.Detail.Traces {
					t := trace
					key := i.key() + "/" + t.ID
					label := "› " + t.Name + " · " + t.Status
					if s.Tools[key] {
						label = "⌄ " + t.Name + " · " + t.Status
					}
					ui.Column(c).Border(1, tok.Border).Radius(radius).Padding(sp2).Children(func() {
						button(c, memoryKindLabel(label), func() { s.Tools[key] = !s.Tools[key] })
						if s.Tools[key] {
							markdown.View(c.Key("tool-"+key), "```json\n"+string(t.Params)+"\n```\n\n```json\n"+string(t.Result)+"\n```\n\n"+t.Error, mdOpts)
						}
					})
				}
			} else {
				markdown.View(c.Key("body-"+i.key()), presentation.VisibleBody, a.v4MarkdownOptions(fsReading))
				if presentation.Unrecognized {
					ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
				}
			}
			if i.Source != "" || i.SourceDocument != "" {
				ui.Column(c).Padding(16).Border(1, border).Radius(8).Gap(8).Children(func() {
					ui.Text(c, "出处 · 官方关联材料").FontSize(12).Bold()
					ui.Text(c, readingSourceLabel(*i)).FontSize(10).TextColor(muted)
					button(c, "查看原始记录 →", func() { a.source(s) })
				})
			} else if i.Kind == "知识" && (i.Document == nil || commonCategory(*i.Document) == "") {
				ui.Text(c, "官方未提供可定位的会话出处").FontSize(10).TextColor(muted)
			}
			if s.Technical == nil {
				s.Technical = map[string]bool{}
			}
			label := "› 技术详情"
			if s.Technical[i.key()] {
				label = "⌄ 技术详情"
			}
			button(c, memoryKindLabel(label), func() { s.Technical[i.key()] = !s.Technical[i.key()] })
			if s.Technical[i.key()] {
				fields := presentation.TechnicalFields
				if len(fields) == 0 {
					fields = []ReadingField{{"数据组 ID", i.Dataset}, {"对象 ID", i.ID}}
				}
				for _, field := range fields {
					f := field
					if f.Value != "" {
						copyableText(c, f.Name+"："+f.Value, f.Value, "复制"+f.Name, homeMeta, func() { c.WriteClipboard(f.Value) })
					}
				}
				if i.BodyLoaded || i.Detail != nil || i.Preview != "" {
					copyableText(c, "原文", i.Body, "复制原文", homeMeta, func() { c.WriteClipboard(i.Body) })
				}
			}
		})
		ui.Row(c).Height(48).Shrink(0).Padding(0, 24).BorderWidth(1, 0, 0, 0).BorderColor(border).Children(func() {
			row := ui.Row(c).AlignItems(ui.Center).Gap(4)
			row.Children(func() {
				ui.Text(c, "只读 · 内容由 Cognee 提供").FontSize(10).TextColor(muted)
				if i.BodyLoaded || i.Detail != nil || i.Preview != "" {
					label := "复制已载入内容"
					if i.Kind == "知识" {
						label = "复制记忆内容"
					}
					hoverCopyAction(c, row, label, func() { c.WriteClipboard(itemPresentation(*i).VisibleBody) })
				}
			})
		})
	})
}

func displayDate(date string) string {
	if day, ok := sourceCalendarDate(date); ok {
		return day
	}
	if date == "" {
		return "时间未知"
	}
	if t, ok := officialTime(date); ok {
		return t.In(time.Local).Format("2006-01-02 15:04")
	}
	return "时间未知"
}

func (a *App) OpenSearch() {
	if !a.projectCreate.Open && !a.toolGraph.Open {
		a.openUnifiedSearch(false)
	}
}
func (a *App) projectEditor(c *ui.Context) {
	if a.settingsMessage != "" {
		ui.Text(c, a.settingsMessage).FontSize(11).TextColor(muted)
	}
	p := a.editProject
	if p == nil {
		return
	}
	ui.Column(c).Padding(18).Border(1, border).Radius(8).Gap(10).Children(func() {
		ui.Text(c, "关联已有官方数据组").FontSize(14).Bold()
		for _, field := range []struct {
			Label string
			Value *string
		}{{"项目标识", &p.ID}, {"显示名称", &p.Name}, {"仓库路径", &p.Path}, {"记忆数据组 ID", &p.Dataset}, {"代码数据组 ID（可选）", &p.CodeDataset}} {
			ui.Text(c, field.Label).FontSize(10).TextColor(muted)
			ui.TextInput(c.Key("project-field-"+field.Label), field.Value).Height(32).Label(field.Label)
		}
		ui.Row(c).Gap(8).Children(func() {
			button(c, "保存关联", func() {
				if p.ID == "" || p.Name == "" || p.Dataset == "" {
					a.settingsMessage = "请填写项目标识、名称与已有官方数据组 ID"
					return
				}
				for idx, existing := range a.Config.Projects {
					if idx != a.editIndex && existing.ID == p.ID {
						a.settingsMessage = "项目标识已存在"
						return
					}
				}
				before := append([]Project(nil), a.Config.Projects...)
				if a.editIndex < 0 {
					a.Config.Projects = append(a.Config.Projects, *p)
				} else {
					a.Config.Projects[a.editIndex] = *p
				}
				if e := a.saveProjects(); e != nil {
					a.Config.Projects = before
					a.settingsMessage = e.Error()
					return
				}
				a.epoch++
				a.InvalidateReading("")
				a.cancelAndResetPanes()
				a.editProject = nil
				a.settingsMessage = "关联已保存；服务与模型配置通过重启后台生效"
				a.loadRecent()
			})
			button(c, "取消", func() { a.editProject = nil })
		})
	})
}

func (a *App) returnFromSource(s *pane) {
	s.SubagentParent, s.SubagentParentPage = nil, nil
	s.SessionSubagents = false
	if s.SourceReturn != nil {
		location := s.SourceReturn
		s.SourceReturn, s.History = nil, nil
		a.restoreReadingLocation(location)
		return
	}
	if len(s.History) == 0 {
		return
	}
	last := len(s.History) - 1
	original := s.History[last]
	history := append([]Item(nil), s.History[:last]...)
	floor := a.overviewSourceFloor[s]
	a.selectItem(s, original)
	s.History = history
	a.clampOverviewFloor(s, floor, len(history))
}

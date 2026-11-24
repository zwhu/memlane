package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

var (
	overviewLoadTimeout     = 90 * time.Second
	overviewActivityTimeout = 60 * time.Second
)

type overviewViewState struct {
	ExpandedEvent, ExpandedReport, CopiedEvent string
	ExpandedDetail                             *failureDetailModel
	ExpandedTechOpen                           bool
	RawLoaded                                  bool
	Snapshot                                   overviewSnapshot
	Loaded, Loading                            bool
	Error                                      string
	Events                                     []overviewEvent
	ActivityLoaded, ActivityLoading, More      bool
	ActivityError                              string
	RecallError                                string
	SearchHistory                              bool
	History                                    []overviewEvent
	HistoryLoaded                              bool
	HistoryError                               string
	ActivityLimit                              int
	Day                                        string
	ShowAllForDay                              bool
	Offset                                     int
	Scroll                                     ui.ScrollState
	SidebarScroll                              ui.ScrollState
	ActivityScroll                             ui.ScrollState
	KnowledgeScroll                            ui.ScrollState
	AllProjects                                bool
	ActivityExpanded                           bool
	scope                                      string
	generation, activityGeneration             uint64
	cancel, activityCancel, recallCancel       context.CancelFunc
	overviewColdStartMotion                    bool
	snapshotVersion                            uint64
	cachedBucketsVersion                       uint64
	cachedBucketsCount                         int
	cachedBucketsFirstKey                      string
	cachedBucketsLastKey                       string
	cachedBucketsDay                           string
	cachedBuckets                              map[string]int
	cachedHeatmapVersion                       uint64
	cachedHeatmapCount                         int
	cachedHeatmapFirstKey                      string
	cachedHeatmapLastKey                       string
	cachedHeatmapDay                           string
	cachedHeatmapLoaded                        bool
	cachedHeatmapWeeks                         int
	cachedHeatmapCells                         []overviewHeatmapCell
	heatmapGrowStart                           time.Time
	heatmapTapDay                              string
	heatmapTapStart                            time.Time
	heatmapTapPending                          bool
	heatmapRingPrevDay                         string
	heatmapRingStart                           time.Time
	heatmapRingPending                         bool
	lastNavIndY                                float32
	activityFetchedAt                          time.Time
	cachedGroupedGen                           uint64
	cachedGroupedHistory                       bool
	cachedGroupedCount                         int
	cachedGroupedEvents                        []overviewEvent
	V2Mode                                     bool
	V2Generation                               int64
	V2TotalKnowledge                           int
	V2DayBuckets                               map[string]int
	V2RecallStartedAt                          string
	DayItems                                   []Item
	DayList                                    ui.ListState
	DayTotal                                   int
	DayNextCursor                              string
	DayHasMore                                 bool
	DayLoading                                 bool
	DayError                                   string
	dayRequest                                 uint64
	dayCancel                                  context.CancelFunc
}

type overviewHeatmapCell struct {
	w     int
	wd    int
	date  string
	level int
}

func overviewKnowledgeBounds(items []Item) (firstKey, lastKey string) {
	if len(items) == 0 {
		return "", ""
	}
	return items[0].key(), items[len(items)-1].key()
}

type overviewFirstUseKey struct {
	clientState, config, day string
}

type overviewFirstUseState struct {
	cachedFirstUseKey        overviewFirstUseKey
	cachedFirstUseLoadingKey overviewFirstUseKey
	cachedFirstUseDays       int
	cachedFirstUseErr        error
	seq                      uint64
}

func firstUseKey(cfg Config, now time.Time) overviewFirstUseKey {
	return overviewFirstUseKey{cfg.ClientStateFile, cfg.ConfigFile, now.Format("2006-01-02")}
}

func (a *App) startOverviewFirstUseDaysLoad(now time.Time) {
	v := &a.overviewFirstUse
	cfg, update := a.Config, a.Update
	key := firstUseKey(cfg, now)
	if v.cachedFirstUseKey == key || v.cachedFirstUseLoadingKey == key {
		return
	}
	if key.config == "" && key.clientState == "" {
		v.seq++
		v.cachedFirstUseLoadingKey = overviewFirstUseKey{}
		v.cachedFirstUseKey, v.cachedFirstUseDays = key, 0
		v.cachedFirstUseErr = errors.New("尚未完成初始化")
		return
	}
	if update == nil {
		return
	}
	v.seq++
	seq := v.seq
	v.cachedFirstUseLoadingKey = key
	go func() {
		days, err := firstUseDays(cfg, now)
		update(func() {
			if v.seq != seq || v.cachedFirstUseLoadingKey != key {
				return
			}
			v.cachedFirstUseLoadingKey = overviewFirstUseKey{}
			if firstUseKey(a.Config, now) == key {
				v.cachedFirstUseKey, v.cachedFirstUseDays, v.cachedFirstUseErr = key, days, err
			}
		})
	}()
}

func (a *App) overviewFirstUseDays(now time.Time) (int, error, bool) {
	v := &a.overviewFirstUse
	key := firstUseKey(a.Config, now)
	if v.cachedFirstUseKey != key && v.cachedFirstUseLoadingKey != key {
		a.startOverviewFirstUseDaysLoad(now)
	}
	if v.cachedFirstUseKey == key {
		return v.cachedFirstUseDays, v.cachedFirstUseErr, true
	}
	return 0, nil, false
}

func (a *App) overviewCachedBuckets(now time.Time) map[string]int {
	v := &a.overviewState
	if v.V2Mode {
		if v.V2DayBuckets == nil {
			return map[string]int{}
		}
		return v.V2DayBuckets
	}
	day := now.Format("2006-01-02")
	firstKey, lastKey := overviewKnowledgeBounds(v.Snapshot.Knowledge)
	if v.cachedBuckets != nil && v.cachedBucketsVersion == v.snapshotVersion && v.cachedBucketsCount == len(v.Snapshot.Knowledge) && v.cachedBucketsFirstKey == firstKey && v.cachedBucketsLastKey == lastKey && v.cachedBucketsDay == day {
		return v.cachedBuckets
	}
	v.cachedBuckets = overviewBuckets(v.Snapshot, now.Location())
	v.cachedBucketsVersion = v.snapshotVersion
	v.cachedBucketsCount = len(v.Snapshot.Knowledge)
	v.cachedBucketsFirstKey = firstKey
	v.cachedBucketsLastKey = lastKey
	v.cachedBucketsDay = day
	return v.cachedBuckets
}

func (a *App) overviewCachedHeatmapCells(now, today, start, gridStart time.Time, weeks int, buckets map[string]int) []overviewHeatmapCell {
	v := &a.overviewState
	day := now.Format("2006-01-02")
	firstKey, lastKey := overviewKnowledgeBounds(v.Snapshot.Knowledge)
	if v.cachedHeatmapCells != nil && v.cachedHeatmapVersion == v.snapshotVersion && v.cachedHeatmapCount == len(v.Snapshot.Knowledge) && v.cachedHeatmapFirstKey == firstKey && v.cachedHeatmapLastKey == lastKey && v.cachedHeatmapDay == day && v.cachedHeatmapLoaded == v.Loaded && v.cachedHeatmapWeeks == weeks {
		return v.cachedHeatmapCells
	}
	cells := make([]overviewHeatmapCell, 0, weeks*7)
	for w := 0; w < weeks; w++ {
		for wd := 0; wd < 7; wd++ {
			d := gridStart.AddDate(0, 0, w*7+wd)
			if d.Before(start) || d.After(today) {
				continue
			}
			date := d.Format("2006-01-02")
			level := 0
			if v.Loaded {
				count := buckets[date]
				switch {
				case count >= 10:
					level = 4
				case count >= 6:
					level = 3
				case count >= 3:
					level = 2
				case count > 0:
					level = 1
				}
			}
			cells = append(cells, overviewHeatmapCell{w: w, wd: wd, date: date, level: level})
		}
	}
	v.cachedHeatmapCells = cells
	v.cachedHeatmapVersion = v.snapshotVersion
	v.cachedHeatmapCount = len(v.Snapshot.Knowledge)
	v.cachedHeatmapFirstKey = firstKey
	v.cachedHeatmapLastKey = lastKey
	v.cachedHeatmapDay = day
	v.cachedHeatmapLoaded = v.Loaded
	v.cachedHeatmapWeeks = weeks
	return v.cachedHeatmapCells
}

func (a *App) overviewScope() string {
	datasets := []string{}
	for _, p := range overviewProjects(a.Config) {
		datasets = append(datasets, p.Dataset)
	}
	sort.Strings(datasets)
	scope, _ := json.Marshal(struct {
		Identity string
		Datasets []string
	}{a.readingIdentity, datasets})
	return string(scope)
}

func (a *App) withdrawOverviewScope() {
	// Preserve the failed scope so subsequent App.View frames do not treat
	// the withdrawal as a scope change and retry on every frame.
	scope := a.overviewState.scope
	if scope == "" {
		scope = a.overviewScope()
	}
	a.InvalidateReading("")
	a.overviewState.scope = scope
	a.overviewState.Snapshot = overviewSnapshot{}
}

func (a *App) loadOverview() {
	if a.Update == nil {
		return
	}
	v := &a.overviewState
	a.syncReadingIdentity()
	a.cancelOverviewRecall()
	a.overviewRefreshAt = time.Now().Add(overviewRefreshInterval)
	if a.Config.ProjectControl != nil && !a.access.Loaded {
		v.Loading, v.ActivityLoading = true, true
		return
	}
	a.resetDeferredStartupFallback()
	scope := a.overviewScope()
	if scope != v.scope {
		if v.cancel != nil {
			v.cancel()
		}
		if v.activityCancel != nil {
			v.activityCancel()
		}
		generation, activityGeneration := v.generation+1, v.activityGeneration+1
		// A directory/range refresh invalidates content, not the user's date or
		// reading position. Old diagnostics are dropped with the old scope.
		*v = overviewViewState{scope: scope, generation: generation, activityGeneration: activityGeneration, snapshotVersion: v.snapshotVersion + 1,
			Day: v.Day, Offset: v.Offset, Scroll: v.Scroll, SidebarScroll: v.SidebarScroll, ActivityScroll: v.ActivityScroll, KnowledgeScroll: v.KnowledgeScroll, AllProjects: v.AllProjects, ActivityExpanded: v.ActivityExpanded, ActivityLimit: v.ActivityLimit, SearchHistory: v.SearchHistory}
	}
	wasLoaded := v.Loaded
	if v.cancel != nil {
		v.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), overviewLoadTimeout)
	v.cancel = cancel
	v.generation++
	generation, authEpoch := v.generation, a.authEpoch
	epoch, identity := a.epoch, a.readingIdentity
	v.Loading = true
	client := a.Client
	control, hasControl := a.accessClient()
	stateDir := a.readingStateDir()
	accessProjects := append([]AccessProject(nil), a.access.Directory.Projects...)
	if len(accessProjects) == 0 && len(a.Config.Projects) > 0 {
		accessProjects = make([]AccessProject, 0, len(a.Config.Projects))
		for _, p := range a.Config.Projects {
			if p.ID != "" && p.Dataset != "" {
				accessProjects = append(accessProjects, AccessProject{ID: p.ID, Name: p.Name, Dataset: p.Dataset})
			}
		}
	}

	if hasControl {
		if v.activityCancel != nil {
			v.activityCancel()
		}
		v.activityCancel = cancel
		v.activityGeneration++
		actGen := v.activityGeneration
		v.ActivityLoading = true
		searchHistory := v.SearchHistory
		projects := overviewProjects(a.Config)
		readingID, _ := readingIdentity(Client{Config: client.Config})
		go func() {
			defer cancel()
			failClosed := func(err error) {
				if errors.Is(ctx.Err(), context.Canceled) {
					return
				}
				a.Update(func() {
					if generation != a.overviewState.generation || authEpoch != a.authEpoch || epoch != a.epoch || identity != a.readingIdentity || !a.syncReadingIdentity() {
						return
					}
					withdraw := readingUnavailable(err)
					if withdraw {
						a.withdrawOverviewScope()
					}
					a.overviewState.Loading = false
					message := controlUserError(err)
					a.overviewState.Error = message
					if withdraw || actGen == a.overviewState.activityGeneration {
						a.overviewState.ActivityLoading = false
						a.overviewState.ActivityError = message
					}
					a.onOverviewBootstrapSettled()
				})
			}
			if err := control.requireReadingContract(ctx); err != nil {
				failClosed(err)
				return
			}
			if len(accessProjects) > 0 {
				if migErr := a.migrateReadingStateV2Snapshot(ctx, control, readingID, stateDir, accessProjects); migErr != nil {
					if ctx.Err() != nil || isControlAuthDenied(migErr) || a.hasPendingLocalReadingMigration(control.Config.URL, stateDir, readingID, accessProjects) {
						failClosed(migErr)
						return
					}
				}
			}
			v2Resp, v2Err := control.overviewBootstrap(ctx, readingID, 365, 20, 50, 50)
			if v2Err == nil {
				snapshot, buckets, events, more := buildOverviewFromV2Bootstrap(projects, v2Resp, 50)
				var historyEvents []overviewEvent
				var historyErr error
				if searchHistory {
					historyEvents, historyErr = client.overviewSearchHistory(ctx)
				}
				a.Update(func() {
					if generation != a.overviewState.generation || authEpoch != a.authEpoch || epoch != a.epoch || identity != a.readingIdentity || !a.syncReadingIdentity() {
						return
					}
					a.overviewState.Loading = false
					a.overviewState.RawLoaded = true
					a.overviewState.V2Mode = true
					a.overviewState.V2Generation = v2Resp.Generation
					a.overviewState.V2TotalKnowledge = v2Resp.TotalKnowledgeCount
					a.overviewState.V2DayBuckets = buckets
					a.overviewState.V2RecallStartedAt = v2Resp.RecallStartedAt
					a.overviewState.Snapshot = snapshot
					a.overviewState.snapshotVersion++
					a.recentScope = ""
					a.overviewState.Loaded = true
					a.overviewState.Error = ""
					a.applyOverviewV2ProjectSummaries(v2Resp.Projects)
					if !wasLoaded {
						a.startOverviewEntrance()
					}
					if actGen == a.overviewState.activityGeneration {
						a.overviewState.ActivityLoading = false
						a.overviewState.Events = events
						a.overviewState.RecallError = ""
						a.overviewState.More = more
						a.overviewState.ActivityError = ""
						a.overviewState.ActivityLoaded = true
						a.overviewState.activityFetchedAt = time.Now()
						if searchHistory {
							if historyErr != nil {
								a.overviewState.HistoryError = historyErr.Error()
							} else {
								a.overviewState.History = historyEvents
								a.overviewState.HistoryLoaded = true
								a.overviewState.HistoryError = ""
							}
						}
					}
					if a.overviewState.Day != "" {
						a.loadOverviewDayV2(a.overviewState.Day, false)
					}
					a.onOverviewBootstrapSettled()
				})
				return
			}
			failClosed(v2Err)
		}()
		return
	}
	a.loadOverviewLegacy(generation, wasLoaded)
}

func (a *App) applyOverviewV2ProjectSummaries(summaries []v2OverviewProjectSummary) {
	if len(summaries) == 0 {
		return
	}
	byID := make(map[string]v2OverviewProjectSummary, len(summaries))
	for _, s := range summaries {
		if s.ProjectID != "" {
			byID[s.ProjectID] = s
		}
	}
	for _, p := range a.access.Directory.Projects {
		summary, ok := byID[p.ID]
		if !ok {
			continue
		}
		q := a.projectQueueStateForAsync(p)
		applyServerUnreadSnapshot(q, summary.UnreadCount, summary.UnreadStateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset))
		if summary.CatalogVersion != "" {
			q.KnowledgeCatalogVersion = summary.CatalogVersion
		}
		q.StateVersion++
	}
}

func (a *App) loadOverviewLegacy(generation uint64, wasLoaded bool) {
	v := &a.overviewState
	authEpoch := a.authEpoch
	activityDone := a.startOverviewActivity()
	if v.cancel != nil {
		v.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), overviewLoadTimeout)
	v.cancel = cancel
	epoch, identity := a.epoch, a.readingIdentity
	v.Loading = true
	client := a.Client
	titles := a.titles
	genStr := fmt.Sprint(generation)
	go func() {
		defer cancel()
		if wasLoaded && activityDone != nil {
			wait := time.NewTimer(140 * time.Millisecond)
			select {
			case <-activityDone:
			case <-ctx.Done():
				wait.Stop()
				if errors.Is(ctx.Err(), context.Canceled) {
					return
				}
			case <-wait.C:
			}
			wait.Stop()
		}
		snapshot, err := client.overview(ctx)
		if err == nil && !wasLoaded && len(snapshot.Knowledge) > 0 {
			// On cold start without cache, preheat the first 10 visible titles
			// within a bounded 800ms budget so the first paint swaps atomically
			// from skeleton to real 2-line rich text with zero flicker.
			preCtx, preCancel := context.WithTimeout(ctx, 800*time.Millisecond)
			topN := min(10, len(snapshot.Knowledge))
			var wg sync.WaitGroup
			for idx := 0; idx < topN; idx++ {
				n := idx
				item := snapshot.Knowledge[n]
				if item.Document == nil || readableDocumentTitle(*item.Document) != "" {
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res, e := titles.read(preCtx, client, item, genStr, true, true); e == nil {
						snapshot.Knowledge[n].Title = overviewReadingTitle(res)
						snapshot.Knowledge[n].ReadingState = "ready"
					}
				}()
			}
			wg.Wait()
			preCancel()
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		a.Update(func() {
			if generation != a.overviewState.generation || authEpoch != a.authEpoch || epoch != a.epoch || identity != a.readingIdentity || !a.syncReadingIdentity() {
				return
			}
			defer a.settleLegacyOverview()
			a.overviewState.Loading = false
			if err != nil {
				if readingAuthDenied(err) {
					a.withdrawOverviewScope()
				}
				a.overviewState.Error = err.Error()
				return
			}
			if snapshot.RawError != "" {
				snapshot.Raw = a.overviewState.Snapshot.Raw
			} else {
				a.overviewState.RawLoaded = true
			}
			ready := map[string]Item{}
			for _, old := range a.overviewState.Snapshot.Knowledge {
				ready[old.key()] = old
			}
			for n, item := range snapshot.Knowledge {
				if old, ok := ready[item.key()]; ok {
					if snapshot.Knowledge[n].ReadingState != "ready" && old.ReadingState == "ready" && item.Document != nil && (item.Document.ContentHash != "" || item.Document.Updated != "") && keyForReading(a.readingIdentity, old, genStr) == keyForReading(a.readingIdentity, item, genStr) {
						snapshot.Knowledge[n].Title, snapshot.Knowledge[n].ReadingState = old.Title, old.ReadingState
					}
					if snapshot.Knowledge[n].RecallStats == nil && old.RecallStats != nil {
						snapshot.Knowledge[n].RecallStats = old.RecallStats
					}
				}
			}
			a.overviewState.Snapshot = snapshot
			a.overviewState.snapshotVersion++
			a.loadOverviewRecallStats(generation)
			// A new catalog may have the same IDs as the already preheated old
			// catalog. Let that publication consume cached titles again.
			a.recentScope = ""
			a.overviewState.Loaded = true
			a.overviewState.Error = ""
			if !wasLoaded {
				a.startOverviewEntrance()
			}
		})
	}()
}

func (a *App) startOverviewEntrance() {
	v := &a.overviewState
	v.overviewColdStartMotion = a.motionRuns.overviewPage == 0
	a.motionRuns.overviewPage++
	a.motionRuns.overviewKnowledge++
	a.motionRuns.overviewActivity++
	v.heatmapGrowStart = time.Time{}
}

func (a *App) settleLegacyOverview() {
	if !a.overviewState.Loading && !a.overviewState.ActivityLoading {
		a.onOverviewBootstrapSettled()
	}
}

func (a *App) loadOverviewActivity() {
	a.startOverviewActivity()
}

func (a *App) startOverviewActivity() <-chan struct{} {
	v := &a.overviewState
	if v.activityCancel != nil {
		v.activityCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), overviewActivityTimeout)
	v.activityCancel = cancel
	v.activityGeneration++
	generation, authEpoch := v.activityGeneration, a.authEpoch
	epoch, identity := a.epoch, a.readingIdentity
	v.ActivityLoading = true
	want := 50
	client := a.Client
	history := v.SearchHistory
	control, hasControl := a.accessClient()
	done := make(chan struct{})
	var closeDone sync.Once
	signalDone := func() { closeDone.Do(func() { close(done) }) }
	go func() {
		defer cancel()
		defer signalDone()
		var events []overviewEvent
		var more bool
		var err error
		var recallErrorText string
		if history {
			events, err = client.overviewSearchHistory(ctx)
		} else {
			// Fetch official pipeline-runs and local control recall-events in parallel.
			var (
				recalls     []RecallEvent
				recallMore  bool
				recallError error
				wg          sync.WaitGroup
			)
			if hasControl {
				wg.Add(1)
				go func() {
					defer wg.Done()
					recalls, recallMore, recallError = control.recallEvents(ctx, want)
				}()
			}
			events, more, err = client.overviewActivity(ctx, want)
			wg.Wait()
			if err == nil && hasControl {
				if recallError == nil {
					var mergedMore bool
					events, mergedMore = mergeRecallActivity(events, recalls, overviewProjects(client.Config), want)
					more = more || recallMore || mergedMore
				} else {
					recallErrorText = "召回明细暂不可用"
					for i := range events {
						if events[i].Record.Operation == "recall" && events[i].State == "已完成" {
							events[i].Title = "召回请求完成 · 明细未读取"
						}
					}
				}
			}
		}
		signalDone()
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		a.Update(func() {
			if generation != a.overviewState.activityGeneration || authEpoch != a.authEpoch || epoch != a.epoch || identity != a.readingIdentity || !a.syncReadingIdentity() {
				return
			}
			defer a.settleLegacyOverview()
			a.overviewState.ActivityLoading = false
			if history {
				if err != nil {
					if readingAuthDenied(err) {
						a.withdrawOverviewScope()
					}
					a.overviewState.HistoryError = err.Error()
					return
				}
				a.overviewState.History = events
				a.overviewState.HistoryLoaded = true
				a.overviewState.HistoryError = ""
				return
			}
			if err != nil {
				if readingAuthDenied(err) {
					a.withdrawOverviewScope()
				}
				a.overviewState.ActivityError = err.Error()
				return
			}
			a.overviewState.Events = events
			a.overviewState.RecallError = recallErrorText
			a.overviewState.More = more
			a.overviewState.ActivityError = ""
			a.overviewState.ActivityLoaded = true
			a.overviewState.activityFetchedAt = time.Now()
		})
	}()
	return done
}

func overviewBuckets(snapshot overviewSnapshot, location *time.Location) map[string]int {
	days := map[string]int{}
	for _, item := range snapshot.Knowledge {
		if item.Document != nil {
			if day, ok := item.Document.knowledgeDay(location); ok {
				days[day]++
			}
		}
	}
	return days
}

func (a *App) openOverviewItem(item Item) {
	for idx, p := range a.Config.Projects {
		if p.Dataset == item.Dataset {
			a.navigate("知识", idx)
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
	for idx, p := range a.Config.Common {
		if p.Dataset == item.Dataset {
			a.navigate("通用知识", idx)
			a.commonEdit.Category = ""
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
}

func (a *App) overviewHome(c *ui.Context) {
	v := &a.overviewState
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	if !a.windowHidden {
		c.After(midnight.Sub(now))
	}
	ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
		pageKey := fmt.Sprintf("overview-page-%d", a.motionRuns.overviewPage)
		page := ui.Column(c.Key(pageKey)).Grow(1).MinHeight(0).FillWidth().MaxWidth(1040).Padding(0, sp8).Font("system-ui").FontWeight(400)
		if v.overviewColdStartMotion {
			a.v4ColdStartPageEnter(c, page, "overview-page", a.motionRuns.overviewPage)
		} else {
			a.v4PageEnter(c, page, "overview-page", a.motionRuns.overviewPage)
		}
		page.Children(func() {
			if _, err, ready := a.overviewFirstUseDays(now); ready && err != nil {
				homeText(c, "使用时间暂不可用。", homeAction).Margin(0, 0, sp2, 0)
			}
			if v.Error != "" {
				message := "统计读取失败 · " + v.Error
				if v.Loaded {
					message = "未能刷新，显示上次统计 · " + v.Error
				}
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
					homeText(c, message, homeBody)
					projectLink(c, "重试读取统计", a.loadOverview)
				})
			}
			if v.Snapshot.RawError != "" {
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
					homeText(c, "原始记录统计暂不可用 · "+v.Snapshot.RawError, homeAction)
					projectLink(c, "重试读取记录统计", a.loadOverview)
				})
			}
			a.overviewHeatmap(c, now)
			// V4: dual columns fill remaining height and scroll independently; no project cards.
			ui.Row(c).Key("overview-paired-sections").Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Key("overview-knowledge-section").Width(0).Grow(1.6).MinWidth(0).MinHeight(0).Padding(sp4, sp6, 0, 0).BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Gap(sp2).Children(func() {
					ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
						title := "最近记忆"
						if v.Day != "" {
							dayCount := len(a.overviewKnowledgeRows(now))
							if v.V2Mode {
								dayCount = v.DayTotal
								if dayCount == 0 && v.V2DayBuckets[v.Day] > 0 {
									dayCount = v.V2DayBuckets[v.Day]
								}
							}
							title = fmt.Sprintf("%s · %d 条记忆", v.Day, dayCount)
						}
						homeText(c, title, homeHeading)
						ui.Spacer(c)
						if v.Day != "" {
							homeLink(c, "返回最近记忆", func() { a.selectOverviewDay("") })
						}
					})
					a.overviewKnowledge(c, now)
					if v.Loaded && v.Day != "" {
						displayed := len(a.overviewDisplayedRows(now))
						total := len(a.overviewKnowledgeRows(now))
						if v.V2Mode {
							total = max(v.DayTotal, displayed)
						}
						ui.Row(c).AlignItems(ui.Center).Gap(sp2).Padding(sp2, 0, sp3, 0).Shrink(0).Children(func() {
							homeText(c, fmt.Sprintf("已显示 %d 条，共 %d 条", displayed, total), homeReadableMeta)
							ui.Spacer(c)
							if v.V2Mode {
								if v.DayHasMore && !v.DayLoading {
									homeLink(c, "加载更多", func() { a.loadOverviewDayV2(v.Day, true) })
								} else if v.DayLoading {
									homeText(c, "正在加载…", homeMeta)
								}
							} else if total > 20 && !v.ShowAllForDay {
								homeLink(c, "显示全部", func() { v.ShowAllForDay = true })
							}
						})
					}
				})
				ui.Column(c).Key("overview-activity-section").Width(0).Grow(1).MinWidth(280).MinHeight(0).Padding(sp4, 0, 0, sp6).Gap(sp2).Children(func() {
					ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Children(func() {
						homeText(c, "近期动态", homeHeading)
						ui.Spacer(c)
						a.overviewActivityTabs(c)
					})
					a.overviewActivities(c, now)
				})
			})
		})
	})
	a.overviewFailureDialog(c)
}

func (a *App) openOverviewFailureDetail(ev overviewEvent, loc *time.Location) {
	v := &a.overviewState
	detail := buildFailureDetail(ev, a.Config, loc)
	report := failureReport(ev, a.Config)
	if v.ExpandedEvent != ev.Key {
		v.CopiedEvent = ""
	}
	v.ExpandedEvent = ev.Key
	v.ExpandedDetail = &detail
	v.ExpandedReport = report
	v.ExpandedTechOpen = false
}

func (a *App) closeOverviewFailureDetail() {
	v := &a.overviewState
	v.ExpandedEvent = ""
	v.ExpandedDetail = nil
	v.ExpandedTechOpen = false
}

func (a *App) overviewFailureDialog(c *ui.Context) {
	v := &a.overviewState
	if v.ExpandedEvent == "" || v.ExpandedDetail == nil {
		return
	}
	detail := v.ExpandedDetail
	open := true
	hint := "来自已有官方记录，不含凭据或会话正文"
	if v.CopiedEvent == detail.EventKey {
		hint = "已复制诊断信息 · 不含凭据或会话正文"
	}
	v4Dialog(c, &open, v4DialogOptions{
		Key:         "overview-failure-detail",
		Title:       detail.DialogTitle,
		Label:       "任务失败详情",
		Width:       v4DialogLarge,
		ScrollBody:  true,
		Leading:     func() { v4StatusDot(c, "fail") },
		Hint:        hint,
		CancelText:  "关闭",
		ConfirmText: "复制诊断信息",
		OnCancel:    a.closeOverviewFailureDetail,
		OnConfirm: func() {
			c.WriteClipboard(v.ExpandedReport)
			v.CopiedEvent = detail.EventKey
		},
		Body: func() {
			ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp2).Background(tok.Fill1).Radius(radius).Children(func() {
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					v4StatusDot(c, "fail")
					ui.Text(c, detail.ReasonLabel).Font("system-ui").FontSize(fsBody).FontWeight(600).TextColor(tok.Danger).Grow(1).MinWidth(0)
					if detail.ErrorClass != "" {
						ui.Text(c, detail.ErrorClass).Font("ui-monospace").FontSize(fsCaption).FontWeight(500).
							Padding(2, 6).Radius(radiusSmall).Background(tok.Fill2).TextColor(tok.Text2).SingleLine().Shrink(0)
					}
				})
				if detail.Summary != "" {
					ui.Text(c, detail.Summary).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).LineHeight(1.55)
				}
				if detail.Hint != "" {
					ui.Text(c, detail.Hint).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).LineHeight(1.5)
				}
			})

			v4Group(c, "概览", "", nil, func() {
				if detail.Project != "" {
					v4Row(c, v4RowOptions{Key: "fail-proj", Title: "所属项目", Value: detail.Project})
				}
				if detail.OperationTitle != "" {
					v4Row(c, v4RowOptions{Key: "fail-op", Title: "任务操作", Value: detail.OperationTitle})
				}
				if detail.StageLabel != "" {
					stageDesc := ""
					if detail.StageRaw != "" && detail.StageRaw != detail.StageLabel {
						stageDesc = detail.StageRaw
					}
					v4Row(c, v4RowOptions{Key: "fail-stage", Title: "失败环节", Desc: stageDesc, DescMono: stageDesc != "", Value: detail.StageLabel})
				}
				if detail.LocalTime != "" {
					v4Row(c, v4RowOptions{Key: "fail-time", Title: "发生时间", Value: detail.LocalTime, ValueMono: true})
				}
				if detail.Duration != "" {
					v4Row(c, v4RowOptions{Key: "fail-dur", Title: "持续耗时", Value: detail.Duration})
				}
				if detail.FailureCount > 1 {
					v4Row(c, v4RowOptions{Key: "fail-count", Title: "失败次数", Value: fmt.Sprintf("%d 次", detail.FailureCount), ValueColor: tok.Danger})
				}
			})

			if len(detail.Items) > 1 {
				v4Group(c, "失败记录", fmt.Sprintf("共 %d 条", len(detail.Items)), nil, func() {
					for idx, item := range detail.Items {
						metaParts := []string{}
						if item.LocalTime != "" {
							metaParts = append(metaParts, item.LocalTime)
						}
						if item.Duration != "" {
							metaParts = append(metaParts, "耗时 "+item.Duration)
						}
						if item.ErrorClass != "" {
							metaParts = append(metaParts, item.ErrorClass)
						}
						v4Row(c, v4RowOptions{
							Key:        fmt.Sprintf("fail-item-%d", idx),
							Title:      item.StageLabel,
							Desc:       strings.Join(metaParts, " · "),
							DescMono:   true,
							Leading:    func() { v4StatusDot(c, "fail") },
							Value:      item.ReasonLabel,
							ValueColor: tok.Danger,
						})
					}
				})
			}

			techDesc := "包含记录 ID、运行 ID、UTC 时间与官方服务信息"
			if detail.HasLogCandidate {
				techDesc = "包含记录 ID、运行 ID、UTC 时间及相邻日志调用位置"
			}
			v4Group(c, "技术信息", "", nil, func() {
				title := "展开技术信息"
				ico := v4IconChev
				if v.ExpandedTechOpen {
					title = "收起技术信息"
					ico = v4IconDown
				}
				v4Row(c, v4RowOptions{
					Key:         "overview-failure-tech-toggle",
					Title:       title,
					Desc:        techDesc,
					Clickable:   true,
					HideChevron: true,
					Control:     func() { ui.Icon(c, ico).FontSize(14).TextColor(tok.Text3).Shrink(0) },
					OnClick:     func() { v.ExpandedTechOpen = !v.ExpandedTechOpen },
				})
				if !v.ExpandedTechOpen {
					return
				}
				ui.Column(c.Key("overview-failure-tech-body")).FillWidth().Padding(sp3).Gap(sp2).Background(tok.Fill1).Radius(radius).Children(func() {
					metaLines := []string{}
					if detail.EventKey != "" {
						metaLines = append(metaLines, "事件 ID："+detail.EventKey)
					}
					if detail.ServiceEndpoint != "" {
						metaLines = append(metaLines, "官方服务："+detail.ServiceEndpoint)
					}
					if detail.ServiceCommit != "" {
						metaLines = append(metaLines, "Cognee 源码版本："+detail.ServiceCommit)
					}
					if detail.EventUTC != "" {
						metaLines = append(metaLines, "事件时间（UTC）："+detail.EventUTC)
					}
					if detail.LogDir != "" {
						metaLines = append(metaLines, "本机日志目录："+detail.LogDir)
					}
					if len(metaLines) > 0 {
						ui.Text(c, strings.Join(metaLines, "\n")).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text2).LineHeight(1.55)
					}
					if len(detail.TechRecords) == 0 {
						ui.Text(c, "官方未返回失败详情。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
					}
					for _, rec := range detail.TechRecords {
						lines := []string{}
						stage := rec.StageLabel
						if rec.StageRaw != "" && rec.StageRaw != rec.StageLabel {
							stage = fmt.Sprintf("%s (%s)", rec.StageLabel, rec.StageRaw)
						}
						lines = append(lines, "阶段："+stage)
						if rec.ErrorClass != "" {
							lines = append(lines, "错误类型："+rec.ErrorClass)
						}
						if rec.IsParentWrapper {
							lines = append(lines, "说明：外层任务（由子阶段错误触发，未附额外错误类型）")
						} else {
							lines = append(lines, "说明："+rec.ReasonLabel)
						}
						if rec.ID != "" {
							lines = append(lines, "记录 ID："+rec.ID)
						}
						if rec.Run != "" {
							lines = append(lines, "运行 ID："+rec.Run)
						}
						if rec.Parent != "" {
							lines = append(lines, "父操作 ID："+rec.Parent)
						}
						if rec.Dataset != "" {
							lines = append(lines, "数据组："+rec.Dataset)
						}
						if rec.Origin != "" {
							lines = append(lines, "来源："+rec.Origin)
						}
						if rec.CreatedUTC != "" {
							lines = append(lines, "记录时间（UTC）："+rec.CreatedUTC)
						}
						if rec.StartedUTC != "" {
							lines = append(lines, "开始（UTC）："+rec.StartedUTC)
						}
						if rec.EndedUTC != "" {
							lines = append(lines, "结束（UTC）："+rec.EndedUTC)
						}
						if rec.LogCandidate != "" {
							lines = append(lines, "日志候选（尚未按任务 ID 精确确认）：\n"+rec.LogCandidate)
						}
						ui.Column(c).FillWidth().Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, strings.Join(lines, "\n")).Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text2).LineHeight(1.55)
						})
					}
					if detail.BoundaryNote != "" {
						ui.Text(c, detail.BoundaryNote).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5).Padding(sp2, 0, 0, 0)
					}
				})
			})
		},
	})
	if !open {
		a.closeOverviewFailureDetail()
	}
}

func (a *App) overviewHeatmap(c *ui.Context, now time.Time) {
	v := &a.overviewState
	buckets := a.overviewCachedBuckets(now)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := today.AddDate(0, 0, -364)
	gridStart := start.AddDate(0, 0, -int(start.Weekday()))
	weeks := 0
	for day := gridStart; !day.After(today); day = day.AddDate(0, 0, 7) {
		weeks++
	}
	usage := "—"
	if n, err, ready := a.overviewFirstUseDays(now); ready && err == nil {
		usage = fmt.Sprint(n)
	}
	knowledge, raw := "—", "—"
	if v.Loaded {
		if v.V2Mode {
			knowledge = fmt.Sprint(v.V2TotalKnowledge)
		} else {
			knowledge = fmt.Sprint(len(v.Snapshot.Knowledge))
		}
	}
	if v.RawLoaded {
		raw = fmt.Sprint(v.Snapshot.Raw)
	}
	// V4: stats merged into heatmap header; no card frame or shadow, separated by hairline divider.
	ui.Column(c).Key("overview-heatmap-module").FillWidth().Shrink(0).Padding(sp2, 0, sp5, 0).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Gap(sp3).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp3).Children(func() {
			homeText(c, "记忆分布", homeHeading).Tooltip("日期优先取自原始记录，不代表提炼完成时间。")
			homeText(c, start.Format("2006.01")+" — "+today.Format("2006.01"), homeReadableMeta)
			ui.Spacer(c)
			for idx, metric := range []struct{ value, unit, label string }{{usage, "天", "已使用"}, {knowledge, "条", "记忆"}, {raw, "条", "原始记录"}} {
				stat := ui.Row(c).Key("overview-stat-" + metric.label).AlignItems(ui.Baseline).Gap(4).Shrink(0)
				if idx > 0 {
					stat.Margin(0, 0, 0, sp6).Padding(0, 0, 0, sp6).BorderWidth(0, 0, 0, 1).BorderColor(tok.Border)
				}
				stat.Children(func() {
					homeText(c, metric.value, homeStatValue)
					homeText(c, metric.unit+" "+metric.label, homeStatUnit)
				})
			}
		})
		ui.Row(c).FillWidth().Gap(sp6).AlignItems(ui.End).Children(func() {
			// Left: heatmap grid painted in a single canvas node (11px cells, 3px gap)
			// instead of 371 separate ButtonBase widgets so Overview builds in ~1ms.
			gridW := float32(weeks*14 - 3)
			gridH := float32(7*14 - 3)
			ui.Column(c).Shrink(0).Gap(4).Children(func() {
				// Month labels row: only emit actual month ticks (12 texts, not 53 columns).
				ui.Box(c).Width(gridW + 18).Height(14).Children(func() {
					for week := 0; week < weeks; week++ {
						label := ""
						if week == 0 {
							nxt := start.AddDate(0, 0, 21)
							if nxt.Month() == start.Month() {
								label = fmt.Sprint(int(start.Month())) + "月"
							}
						} else {
							for weekday := 0; weekday < 7; weekday++ {
								day := gridStart.AddDate(0, 0, week*7+weekday)
								if day.Day() == 1 && !day.After(today) {
									label = fmt.Sprint(int(day.Month())) + "月"
								}
							}
						}
						if label != "" {
							t := homeText(c, label, homeReadableMeta).Absolute().Top(0).SingleLine()
							if week >= weeks-2 {
								t.Right(0)
							} else {
								t.Left(18 + float32(week*14))
							}
						}
					}
				})
				ui.Row(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
					ui.Box(c).Width(12).Height(gridH).Shrink(0).Children(func() {
						for _, wd := range []struct {
							row   int
							label string
						}{{1, "一"}, {3, "三"}, {5, "五"}} {
							ui.Text(c, wd.label).Font("system-ui").FontSize(9).TextColor(tok.Text3).
								Absolute().Left(0).Top(float32(wd.row * 14)).SingleLine()
						}
					})
					canvas := ui.Box(c.Key("overview-heatmap-canvas")).Label("记忆分布热力图").Size(gridW, gridH).Shrink(0)
					hoverW, hoverWD := -1, -1
					hoveredDate := ""
					hoveredLabel := ""
					if v.Loaded {
						if px, py, ok := canvas.PointerPosition(); ok && px >= 0 && py >= 0 {
							w := int(px) / 14
							wd := int(py) / 14
							if w >= 0 && w < weeks && wd >= 0 && wd < 7 && px-float32(w*14) <= 12 && py-float32(wd*14) <= 12 {
								day := gridStart.AddDate(0, 0, w*7+wd)
								if !day.Before(start) && !day.After(today) {
									hoverW, hoverWD = w, wd
									hoveredDate = day.Format("2006-01-02")
									hoveredLabel = fmt.Sprintf("%s · 新增 %d 条记忆", hoveredDate, buckets[hoveredDate])
								}
							}
						}
					}
					if hoveredLabel != "" {
						canvas.Cursor(ui.CursorPointer)
					}
					if v.Loaded {
						canvas.OnClick(func() {
							if hoveredDate == "" {
								return
							}
							if v.Day == hoveredDate {
								a.selectOverviewDay("")
							} else {
								a.selectOverviewDay(hoveredDate)
							}
						})
					}
					cells := a.overviewCachedHeatmapCells(now, today, start, gridStart, weeks, buckets)
					selDay := v.Day
					loaded := v.Loaded
					if loaded && v.heatmapGrowStart.IsZero() && a.motionRuns.overviewPage > 0 {
						a.overviewState.heatmapGrowStart = c.Now()
					}
					if v.heatmapTapPending {
						a.overviewState.heatmapTapPending = false
						a.overviewState.heatmapTapStart = c.Now()
					}
					if v.heatmapRingPending {
						a.overviewState.heatmapRingPending = false
						a.overviewState.heatmapRingStart = c.Now()
					}
					growStart := time.Time{}
					if loaded {
						growStart = a.overviewState.heatmapGrowStart
					}
					tapDay := v.heatmapTapDay
					tapStart := v.heatmapTapStart
					ringPrevDay := v.heatmapRingPrevDay
					ringStart := v.heatmapRingStart
					coldStart := v.overviewColdStartMotion
					weekDelay, waveDuration := 7*time.Millisecond, 420*time.Millisecond
					if coldStart {
						weekDelay, waveDuration = 3*time.Millisecond, 320*time.Millisecond
					}
					waveTotal := time.Duration(weeks-1)*weekDelay + waveDuration
					canvas.Draw(func(p *ui.Painter, r ui.Rect) {
						elapsed, waving := v4HeatmapWaveElapsed(c, p, growStart, waveTotal)
						tapElapsed, tapping := v4HeatmapWaveElapsed(c, p, tapStart, 650*time.Millisecond)
						ringElapsed, ringing := v4HeatmapWaveElapsed(c, p, ringStart, 140*time.Millisecond)
						lastW := -1
						var x, scale, alpha float32
						skipWeek := false
						for _, cell := range cells {
							if cell.w != lastW {
								lastW = cell.w
								x = r.X + float32(cell.w*14)
								scale = 1
								alpha = 1
								if waving {
									t := elapsed - float64(cell.w)*weekDelay.Seconds()
									scale = v4BouncyScale(t)
									if coldStart {
										alpha = float32(0.35 + 0.65*max(0, min(t/0.12, 1)))
										scale = 0.72 + 0.28*scale
									} else if t <= 0 {
										alpha = 0
									} else if t < 0.14 {
										alpha = float32(t / 0.14)
									}
								}
								skipWeek = scale <= 0.01 || alpha <= 0.01
							}
							if skipWeek {
								continue
							}
							y := r.Y + float32(cell.wd*14)
							date := cell.date
							color := tok.Heat[cell.level]
							if alpha < 1 {
								color = color.Alpha(alpha)
							}
							cellScale := scale
							if !waving {
								targetScale := float32(1)
								if cell.w == hoverW && cell.wd == hoverWD {
									targetScale = 1.35
								}
								if tapping && date == tapDay {
									// Cell Tap: Bouncy spring 0.6 -> targetScale (1.35 if still hovered,
									// 1.0 otherwise) so there is zero snap when the spring settles at 650ms.
									cellScale = 0.6 + (targetScale-0.6)*v4BouncyScale(tapElapsed)
								} else {
									cellScale = targetScale
								}
							}
							if cellScale != 1 {
								sz := 11 * cellScale
								off := (11 - sz) / 2
								p.Fill(ui.Rect{X: x + off, Y: y + off, W: sz, H: sz}, color, max(1, 2*cellScale))
							} else {
								p.Fill(ui.Rect{X: x, Y: y, W: 11, H: 11}, color, 2)
							}
							// Double-ring halo matching prototype .cell.sel:
							// 1.5px bg gap + 1.5px accent outline with smooth fade in/out.
							if loaded {
								ringAlpha := float32(0)
								if selDay == date {
									ringAlpha = 1
									if ringing {
										ringAlpha = min(1, float32(ringElapsed/0.12))
									}
								} else if ringing && ringPrevDay == date && selDay == "" {
									ringAlpha = max(0, 1-float32(ringElapsed/0.12))
								}
								if ringAlpha > 0.01 {
									sz := 11 * cellScale
									off := (11 - sz) / 2
									p.Stroke(ui.Rect{X: x + off - 0.75, Y: y + off - 0.75, W: sz + 1.5, H: sz + 1.5}, tok.Bg.Alpha(ringAlpha), 2.5, 1.5)
									p.Stroke(ui.Rect{X: x + off - 2.25, Y: y + off - 2.25, W: sz + 4.5, H: sz + 4.5}, tok.Accent.Alpha(ringAlpha), 3.8, 1.5)
								}
							}
						}
					})
					// Expose a single accessible hit-target for today's cell so keyboard
					// and UI tests can locate/click today directly without 371 nodes,
					// plus a floating cell-anchored tooltip pill right above the hovered day.
					if v.Loaded {
						todayDate := today.Format("2006-01-02")
						todayLabel := fmt.Sprintf("%s · 新增 %d 条记忆", todayDate, buckets[todayDate])
						todayW := weeks - 1
						todayWD := int(today.Weekday())
						canvas.Children(func() {
							ui.ButtonBase(c.Key("overview-day-"+todayDate)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
								Label(todayLabel).
								Absolute().Left(float32(todayW*14)).Top(float32(todayWD*14)).Size(11, 11).
								Background(ui.Transparent).BorderWidth(0).OnClick(func() {
								if v.Day == todayDate {
									a.selectOverviewDay("")
								} else {
									a.selectOverviewDay(todayDate)
								}
							})
							if hoverW >= 0 && hoverWD >= 0 && hoveredLabel != "" {
								tip := ui.Row(c.Key("overview-heatmap-tip")).PassThrough().
									Background(tok.TooltipBg).Border(1, tok.Border).Radius(radiusSmall).
									Padding(4, 8).Shadow(0, 4, 14, 0, tok.ShadowLift)
								tipW := tip.Bounds().W
								if tipW <= 0 {
									tipW = 156
								}
								tipX := float32(hoverW*14) + 5.5 - tipW/2
								if tipX < 0 {
									tipX = 0
								}
								if maxX := gridW - tipW; tipX > maxX {
									tipX = maxX
								}
								tipY := float32(hoverWD*14) - 30
								tip.Absolute().Left(tipX).Top(tipY).Children(func() {
									ui.Text(c, hoveredLabel).Font("system-ui").FontSize(fsCaption).FontWeight(500).
										FontFeatures("tnum").TextColor(tok.TooltipText).SingleLine()
								})
							}
						})
					}
				})
			})
			// Right: today summary + legend.
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.End).Gap(sp2).Padding(0, 0, 2, 0).Children(func() {
				if !v.Loaded {
					homeText(c, "读取中…", homeBody)
				} else {
					n := buckets[today.Format("2006-01-02")]
					ui.Row(c).Label(fmt.Sprintf("今天新增 %d 条", n)).AlignItems(ui.Baseline).Gap(3).Children(func() {
						homeText(c, "今天新增", homeBody)
						ui.Text(c, fmt.Sprint(n)).Font("system-ui").FontSize(fsFootnote).FontWeight(550).FontFeatures("tnum").TextColor(tok.Text1)
						homeText(c, "条", homeBody)
					})
				}
				ui.Row(c).AlignItems(ui.Center).Gap(3).Children(func() {
					ui.Text(c, "少").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
					for level := 1; level <= 4; level++ {
						ui.Box(c).Size(9, 9).Radius(2).Background(tok.Heat[level])
					}
					ui.Text(c, "多").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
				})
			})
		})
	})
}

func (a *App) overviewKnowledgeRows(now time.Time) []Item {
	v := &a.overviewState
	if v.V2Mode && v.Day != "" {
		return v.DayItems
	}
	rows := []Item{}
	for _, item := range v.Snapshot.Knowledge {
		if v.Day != "" {
			if item.Document == nil {
				continue
			}
			day, ok := item.Document.knowledgeDay(now.Location())
			if !ok || day != v.Day {
				continue
			}
		}
		rows = append(rows, item)
	}
	return rows
}

func (a *App) loadOverviewDayV2(day string, appendMore bool) {
	v := &a.overviewState
	if day == "" || !v.V2Mode || a.Update == nil {
		return
	}
	control, hasControl := a.accessClient()
	if !hasControl {
		return
	}
	cursor := ""
	if appendMore {
		if v.DayLoading || !v.DayHasMore || v.DayNextCursor == "" {
			return
		}
		cursor = v.DayNextCursor
	} else {
		if v.dayCancel != nil {
			v.dayCancel()
		}
		v.DayItems = nil
		v.DayNextCursor = ""
		v.DayHasMore = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	v.dayCancel = cancel
	v.dayRequest++
	reqID, gen, authEpoch := v.dayRequest, v.generation, a.authEpoch
	v.DayLoading = true
	v.DayError = ""
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	recallStartedAt := v.V2RecallStartedAt
	go func() {
		defer cancel()
		resp, err := control.overviewDayV2(ctx, readingID, day, 30, cursor)
		a.Update(func() {
			if reqID != a.overviewState.dayRequest || gen != a.overviewState.generation || authEpoch != a.authEpoch || a.overviewState.Day != day {
				return
			}
			a.overviewState.DayLoading = false
			if err != nil {
				if isControlInvalidCursor(err) && appendMore {
					a.overviewState.DayNextCursor = ""
					a.overviewState.DayHasMore = false
					a.loadOverviewDayV2(day, false)
					return
				}
				a.overviewState.DayError = controlUserError(err)
				if readingUnavailable(err) {
					a.InvalidateReading("")
				}
				return
			}
			a.overviewState.DayError = ""
			a.overviewState.DayTotal = resp.TotalCount
			a.overviewState.DayNextCursor = resp.NextCursor
			a.overviewState.DayHasMore = resp.HasMore
			mapped := make([]Item, 0, len(resp.Items))
			for _, row := range resp.Items {
				if row.DocID == "" {
					continue
				}
				mapped = append(mapped, row.toItem(recallStartedAt))
			}
			if appendMore {
				seen := make(map[string]bool, len(a.overviewState.DayItems))
				for _, existing := range a.overviewState.DayItems {
					seen[existing.key()] = true
				}
				for _, item := range mapped {
					if !seen[item.key()] {
						a.overviewState.DayItems = append(a.overviewState.DayItems, item)
						seen[item.key()] = true
					}
				}
				if len(a.overviewState.DayItems) > maxActiveListItems {
					drop := len(a.overviewState.DayItems) - maxActiveListItems
					kept := make([]Item, maxActiveListItems)
					copy(kept, a.overviewState.DayItems[drop:])
					a.overviewState.DayItems = kept
				}
			} else {
				a.overviewState.DayItems = mapped
			}
		})
	}()
}

func (a *App) selectOverviewDay(day string) {
	v := &a.overviewState
	prev := v.Day
	if day != "" {
		v.heatmapTapDay = day
		v.heatmapRingPrevDay = prev
	} else {
		v.heatmapTapDay = prev
		v.heatmapRingPrevDay = prev
	}
	v.heatmapTapStart = time.Time{}
	v.heatmapRingStart = time.Time{}
	v.heatmapTapPending = true
	v.heatmapRingPending = true
	v.Day, v.ShowAllForDay = day, false
	v.KnowledgeScroll.Y = 0
	a.motionRuns.overviewKnowledge++
	if v.V2Mode {
		if v.dayCancel != nil {
			v.dayCancel()
			v.dayCancel = nil
		}
		v.dayRequest++
		v.DayItems = nil
		v.DayTotal = 0
		v.DayNextCursor = ""
		v.DayHasMore = false
		v.DayLoading = false
		v.DayError = ""
		if day != "" {
			a.loadOverviewDayV2(day, false)
		}
	}
}

func (a *App) overviewDisplayedRows(now time.Time) []Item {
	rows := a.overviewKnowledgeRows(now)
	if a.overviewState.V2Mode && a.overviewState.Day != "" {
		return rows
	}
	if a.overviewState.Day == "" || !a.overviewState.ShowAllForDay {
		return rows[:min(20, len(rows))]
	}
	return rows
}

func (a *App) overviewKnowledgeRow(c *ui.Context, now time.Time, i Item, idx int, prevHovered bool) bool {
	rowKey := fmt.Sprintf("recent-%d-%s", a.motionRuns.overviewKnowledge, i.key())
	row := ui.ButtonBase(c.Key(rowKey)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label(i.Title).FillWidth().Justify(ui.Start).AlignItems(ui.Center).
		MinHeight(rowHeight2).Padding(sp2+2, sp2).Radius(radius).Gap(sp3).BorderWidth(0)
	hovered := row.Hovered()
	hoverBg := tok.BgElevated
	if tok.Dark {
		hoverBg = tok.Fill1
	}
	row.Background(homeHoverColor(row, "knowledge-surface", ui.Transparent, hoverBg))
	if hovered {
		row.Shadow(0, 2, 10, 0, tok.ShadowLift)
	}
	if a.overviewState.overviewColdStartMotion {
		a.v4ColdStartStaggerEnter(c, row, "recent-"+i.key(), idx, a.motionRuns.overviewKnowledge, true)
	} else {
		a.v4StaggerEnter(c, row, "recent-"+i.key(), idx, a.motionRuns.overviewKnowledge, true)
	}
	// Hairline divider between rows starting after the agent icon.
	// Hide both above and below a hovered lifted row (.row:hover + .row::before).
	if idx > 0 && !hovered && !prevHovered {
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 42, Y: r.Y, W: max(0, r.W-50), H: 1}, tok.Border, 0)
		})
	}
	row.Children(func() {
		ui.Column(c).Size(22, 22).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			agentLogo(c, recentAgentIcon(i), 20)
		})
		ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
			// Lock a 2-line title slot height so async title hydration never
			// stretches the row downward or shifts rows below it.
			titlePending := i.ReadingState != "ready" && i.Preview == "" && (i.Document == nil || readableDocumentTitle(*i.Document) == "")
			ui.Column(c).FillWidth().MinHeight(38).Justify(ui.Center).Children(func() {
				if titlePending {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						ui.Box(c).FillWidth().Height(12).Radius(4).Background(tok.Fill2)
						ui.Box(c).Width(220).Height(12).Radius(4).Background(tok.Fill1)
					})
				} else {
					v4InlineMarkdown(c, knowledgePreview(i.Title), fsBody, 500, 1.45, tok.Text1).MaxLines(2).Tooltip(stripInlineBackticks(knowledgePreview(i.Title)))
				}
			})
			date := "来源日期未知"
			if i.Document != nil {
				if day, ok := i.Document.knowledgeDay(now.Location()); ok {
					date = day
				}
			}
			ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
				if a.itemIsUnread(i) {
					v4UnreadDot(c)
				}
				source := a.overviewSourceName(i)
				homeText(c, middleProjectName(source), homeReadableMeta).Tooltip("所属项目：" + source).MaxLines(1)
				v4RecallChip(c, i)
				if fields := knowledgeBasisFields(i); len(fields) > 0 {
					v4CategoryChip(c, fields[0].Value)
				}
				homeText(c, date, homeReadableMeta)
			})
		})
		chevColor := tok.Text4.Alpha(0)
		if hovered {
			chevColor = tok.Text4
		}
		ui.Icon(c, v4IconChev).FontSize(13).TextColor(row.AnimateColor("chev-col", chevColor, v4Fast)).Shrink(0)
	}).OnClick(func() { a.openOverviewItem(i) })
	if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
		button(c, "标题读取失败 · 重试", func() { a.retryRecentTitle(i) })
	}
	return hovered
}

func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	v := &a.overviewState
	rows := a.overviewDisplayedRows(now)
	if !v.V2Mode {
		a.startRecentTitles(rows)
	}
	if v.V2Mode && v.Day != "" && len(rows) > 0 {
		if v.DayHasMore && !v.DayLoading && v.DayError == "" && v.KnowledgeScroll.MaxY > 0 && v.KnowledgeScroll.Y >= v.KnowledgeScroll.MaxY-160 {
			a.loadOverviewDayV2(v.Day, true)
		}
		ui.Column(c.Key("overview-knowledge-v2-day")).Grow(1).MinHeight(0).FillWidth().Background(tok.Bg).Children(func() {
			if v.DayError != "" {
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Padding(sp2, sp2).Shrink(0).Children(func() {
					homeText(c, "读取失败 · "+v.DayError, homeMeta).MaxLines(2)
					projectLink(c, "重试", func() { a.loadOverviewDayV2(v.Day, false) })
				})
			}
			v.DayList.Key = func(n int) any { return rows[n].key() }
			v.DayList.KeepOffset = true
			v.DayList.Label = func(n int) string { return rows[n].Title }
			ui.List(c.Key("overview-day-list-"+v.Day), &v.DayList, len(rows), func(n int) {
				a.overviewKnowledgeRow(c, now, rows[n], n, false)
			}).Label("当日记忆列表").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.KnowledgeScroll).Padding(0, 0, sp4, 0)
			if v.DayHasMore {
				ui.Row(c).FillWidth().Padding(sp1, sp2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
					if v.DayLoading {
						homeText(c, "正在加载更多记忆…", homeMeta)
					} else {
						projectLink(c, "加载更多", func() { a.loadOverviewDayV2(v.Day, true) })
					}
				})
			}
		})
		return
	}
	ui.Scroll(c.Key("overview-knowledge")).Label("最近记忆内容").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.KnowledgeScroll).BorderWidth(0).Radius(0).Background(tok.Bg).Padding(0, 0, sp4, 0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		bg := tok.Bg
		if v.KnowledgeScroll.MaxY-v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H - 24, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.KnowledgeScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W - 6, H: 16}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if ((!v.Loaded && v.Loading) || (v.V2Mode && v.Day != "" && v.DayLoading)) && len(rows) == 0 {
			overviewLoadingRows(c, "正在读取记忆", false)
			return
		}
		if v.V2Mode && v.Day != "" && v.DayError != "" {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Padding(sp2, sp2).Children(func() {
				homeText(c, "读取失败 · "+v.DayError, homeMeta).MaxLines(2)
				projectLink(c, "重试", func() { a.loadOverviewDayV2(v.Day, false) })
			})
		}
		prevHovered := false
		for index, item := range rows {
			prevHovered = a.overviewKnowledgeRow(c, now, item, index, prevHovered)
		}
		if len(rows) == 0 {
			label := "暂无记忆"
			if !v.Loaded {
				label = "记忆尚未载入"
				if v.Error != "" {
					label = "记忆读取失败"
				}
			} else if v.Day != "" {
				label = "这一天没有记忆"
			}
			v4EmptyState(c, "overview-knowledge-empty", label, "")
		}
	})
}

func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	v := &a.overviewState
	events, loaded, loadError := v.Events, v.ActivityLoaded, v.ActivityError
	if v.SearchHistory {
		events, loaded, loadError = v.History, v.HistoryLoaded, v.HistoryError
	}
	events = events[:min(50, len(events))]
	ui.Scroll(c.Key("overview-activity")).Label("近期动态内容").Grow(1).MinHeight(0).FillWidth().TrackScroll(&v.ActivityScroll).BorderWidth(0).Background(tok.Bg).Radius(0).Padding(sp1, 0, sp4, 0).Gap(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
		bg := tok.Bg
		if v.ActivityScroll.MaxY-v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H - 24, W: r.W - 6, H: 24}, ui.LinearGradient{From: bg.Alpha(0), To: bg, Angle: 180}, 0)
		}
		if v.ActivityScroll.Y > 1 {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W - 6, H: 16}, ui.LinearGradient{From: bg, To: bg.Alpha(0), Angle: 180}, 0)
		}
	}).Children(func() {
		if !loaded && v.ActivityLoading && len(events) == 0 {
			overviewLoadingRows(c, "正在读取动态", true)
			return
		}
		if len(events) == 0 {
			title, hint := "暂无动态", "采集和提炼记忆后，动态会显示在这里"
			if v.SearchHistory {
				title, hint = "暂无搜索历史", "在搜索框中输入，记录会显示在这里"
			}
			if v.ActivityLoading && !loaded {
				title, hint = "正在读取动态…", ""
				if v.SearchHistory {
					title = "正在读取搜索历史…"
				}
			} else if loadError != "" {
				title, hint = "暂时无法读取", loadError
			}
			ui.Column(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				v4EmptyState(c, "overview-activity-empty", title, hint)
				if loadError != "" {
					ui.Row(c).FillWidth().Justify(ui.Center).Children(func() { homeLink(c, "重试", a.loadOverviewActivity) })
				}
			})
			return
		}
		ui.Column(c).FillWidth().Shrink(0).Gap(0).Children(func() {
			if !v.SearchHistory && v.RecallError != "" {
				homeText(c, v.RecallError, homeReadableMeta)
				homeLink(c, "重试", a.loadOverviewActivity).Margin(0, 0, sp2, 0)
			}
			if loadError != "" {
				homeText(c, "读取失败，保留上次结果 · "+loadError, homeReadableMeta).MaxLines(2)
				homeLink(c, "重试读取动态", a.loadOverviewActivity).Margin(0, 0, sp2, 0)
			}
			if v.cachedGroupedEvents == nil || v.cachedGroupedGen != v.activityGeneration || v.cachedGroupedHistory != v.SearchHistory || v.cachedGroupedCount != len(events) {
				v.cachedGroupedEvents = groupOverviewFailures(events)
				v.cachedGroupedGen = v.activityGeneration
				v.cachedGroupedHistory = v.SearchHistory
				v.cachedGroupedCount = len(events)
			}
			shown := v.cachedGroupedEvents
			for idx, event := range shown {
				ev := event
				dotColor := tok.Text4
				running := false
				switch ev.State {
				case "已完成":
					dotColor = tok.OK
				case "失败":
					dotColor = tok.Danger
				case "处理中", "已启动":
					dotColor = tok.Accent
					running = true
				}
				var recallGroups []recallActivityGroup
				if ev.Recall != nil {
					recallGroups = a.recallActivityGroups(*ev.Recall)
				}
				var row ui.Element
				evKey := fmt.Sprintf("overview-event-%d-%s", a.motionRuns.overviewActivity, ev.Key)
				if ev.Recall != nil && len(recallGroups) > 0 {
					btn := ui.ButtonBase(c.Key(evKey)).Role(ui.RoleButton).
						Label(fmt.Sprintf("%s · %s · %d 条记忆", ev.Title, ev.Project, len(ev.Recall.Hits))).
						FillWidth().Justify(ui.Start).AlignItems(ui.Stretch).
						Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).BorderWidth(0).
						Cursor(ui.CursorPointer).Tooltip("查看本次召回的记忆")
					btn.Background(homeHoverColor(btn, "ev-hover", ui.Transparent, tok.Hover))
					if len(recallGroups) == 1 {
						ds := recallGroups[0].dataset
						rc := *ev.Recall
						btn.OnClick(func() { a.openRecalledCards(rc, ds) })
					} else {
						rc := *ev.Recall
						groupsCopy := recallGroups
						btn.PopoverMenuButton(func(m *ui.Menu) {
							for _, g := range groupsCopy {
								if m.Item(fmt.Sprintf("%s · %d 条", g.name, g.count)).Chosen() {
									a.openRecalledCards(rc, g.dataset)
								}
							}
						})
					}
					row = btn
				} else {
					row = ui.Row(c).Key(evKey).Padding(sp2, sp2, sp3, sp2).Gap(sp3).Radius(radius).AlignItems(ui.Stretch)
				}
				interactive := ev.Recall != nil && len(recallGroups) > 0
				if a.overviewState.overviewColdStartMotion {
					a.v4ColdStartStaggerEnter(c, row, "event-"+ev.Key, idx, a.motionRuns.overviewActivity, interactive)
				} else {
					a.v4StaggerEnter(c, row, "event-"+ev.Key, idx, a.motionRuns.overviewActivity, interactive)
				}
				row.Children(func() {
					ui.Column(c).Width(8).Shrink(0).Padding(6, 0, 0, 0).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(dotColor).Shrink(0)
						if idx < len(shown)-1 {
							ui.Box(c).Width(1).Grow(1).Margin(4, 0, -18, 0).Background(tok.Border)
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
							homeText(c, ev.Title, homeActivityTitle).Grow(1).MinWidth(0).MaxLines(2)
							if running {
								v4StageBlocks(c)
							}
							stamp := "时间未知"
							if !ev.At.IsZero() {
								stamp = ev.At.In(now.Location()).Format("15:04")
								if ev.At.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02") {
									stamp = ev.At.In(now.Location()).Format("2006/01/02 15:04")
								}
							}
							homeText(c, stamp, homeReadableMeta).FontFeatures("tnum").Shrink(0)
						})
						if ev.State == "失败" {
							ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
								homeText(c, ev.Project+" ·", homeReadableMeta)
								homeText(c, eventFailureReason(ev), homeReadableMeta).TextColor(tok.Danger).MaxLines(1)
							})
						} else {
							label := ev.Project + " · " + ev.State
							if ev.Recall != nil {
								label = fmt.Sprintf("%s · %d 条记忆", ev.Project, len(ev.Recall.Hits))
							}
							homeText(c, label, homeReadableMeta)
						}
						if ev.Recall != nil && len(recallGroups) == 0 {
							homeText(c, "记忆库暂不可用", homeMeta)
						}
						if running {
							if index, ok := a.overviewActivityProject(ev); ok {
								homeLink(c, "查看项目", func() { a.navigate("知识", index) }).AlignSelf(ui.Start).Justify(ui.Start)
							}
						}
						if ev.State == "失败" {
							detailBtn := ui.ButtonBase(c).Role(ui.RoleButton).Label("失败操作："+ev.Key).
								Cursor(ui.CursorPointer).Height(24).MinHeight(24).Padding(2, 0).Gap(3).
								Radius(radiusSmall).BorderWidth(0).Background(ui.Transparent).
								AlignItems(ui.Center).AlignSelf(ui.Start)
							fg := tok.Text3
							if detailBtn.Hovered() {
								fg = tok.Text1
							}
							detailBtn.Children(func() {
								ui.Text(c, "查看详情").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(fg).SingleLine()
								ui.Icon(c, v4IconChev).FontSize(10).TextColor(fg).Shrink(0)
							}).OnClick(func() {
								a.openOverviewFailureDetail(ev, now.Location())
							})
						}
					})
				})
			}
		})
	})
}

func (a *App) overviewActivityProject(event overviewEvent) (int, bool) {
	// Activity labels are presentation only. Same-name projects must never
	// route through their label; only an unambiguous official dataset does.
	index := -1
	if event.Record.Dataset != "" {
		for i, p := range a.Config.Projects {
			if p.Dataset == event.Record.Dataset {
				if index >= 0 {
					return 0, false
				}
				index = i
			}
		}
	}
	return index, index >= 0
}

func (a *App) overviewActivityTabs(c *ui.Context) {
	v := &a.overviewState
	selected := 0
	if v.SearchHistory {
		selected = 1
	}
	v4Segmented(c, "overview-activity-tabs", "近期动态类型", &selected, []string{"动态", "最近搜索"}, func() {
		v.SearchHistory = selected == 1
		v.ActivityExpanded = false
		v.ActivityScroll.Y = 0
		a.motionRuns.overviewActivity++
		// Reuse fresh activity when switching back from 最近搜索 within 15s
		// instead of firing redundant HTTP fetches on every tab toggle.
		if !v.SearchHistory && v.ActivityLoaded && !v.activityFetchedAt.IsZero() && time.Since(v.activityFetchedAt) < 15*time.Second {
			return
		}
		a.loadOverviewActivity()
	})
}

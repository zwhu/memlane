package overview

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const RefreshInterval = 30 * time.Second

type Controller = ControllerOf[Item]
type Deps = DepsOf[Item]

type ActivityPage struct {
	Events      []Event
	More        bool
	RecallError string
}

type DepsOf[T any] struct {
	Dispatcher    shell.Dispatcher
	Router        shell.Router
	Fence         shell.SessionFence
	Theme         shell.ThemeProvider
	Client        *api.Client
	ClientFactory func() (*api.Client, error)

	// Adapters capture configuration on the dispatcher before returning a worker.
	FetchBootstrap                                            func() func(context.Context) (Hydration[T], error)
	FetchDay                                                  func() func(context.Context, string, string) (DayPage[T], error)
	FetchActivity                                             func() func(context.Context) (ActivityPage, error)
	FetchSearchHistory                                        func() func(context.Context) ([]Event, error)
	Projects                                                  func() []shell.ProjectRef
	CanDispatch, IsSetupOnly, IsWindowHidden, ValidateReading func() bool
	FirstUseFiles                                             func() (clientState, config string)
	Diagnostics                                               func() DiagnosticSource
	ErrorMessage                                              func(error) string
	ReadingDenied, InvalidCursor                              func(error) bool
	BootstrapTimeout, ActivityTimeout                         func() time.Duration
	OnBootstrapApplied                                        func(Hydration[T])
	OnBootstrapSettled, OnReadingDenied                       func()
	Reload                                                    func()

	FromItem                          func(Item) T
	Describe                          func(T) Item
	OpenItem, RetryTitle              func(T)
	OpenRecalled                      func(RecallEvent, string)
	NavigateProject                   func(string)
	ItemIsUnread                      func(T) bool
	SourceName                        func(T) string
	ItemDate                          func(T, *time.Location) (string, bool)
	WithdrawItem                      func(*T)
	PrepareTitles                     func([]T)
	RenderAgentIcon, RenderItemBadges func(*ui.Context, T)
	RenderTopBanners                  func(*ui.Context)
	PageEnter                         func(*ui.Context, ui.Element, string, uint64, bool)
	StaggerEnter                      func(*ui.Context, ui.Element, string, int, uint64, bool, bool)
}

type ControllerOf[T any] struct {
	DepsOf[T]
	State     *StateOf[T]
	FirstUse  *FirstUseState
	RefreshAt time.Time
	pose      kitui.PoseTracker
}

func New(deps Deps) Controller {
	deps.FromItem = func(i Item) Item { return i }
	deps.Describe = func(i Item) Item { return i }
	return NewOf(deps, new(State), new(FirstUseState))
}

func NewOf[T any](deps DepsOf[T], state *StateOf[T], firstUse *FirstUseState) ControllerOf[T] {
	return ControllerOf[T]{DepsOf: deps, State: state, FirstUse: firstUse}
}

func (a *ControllerOf[T]) projects() []shell.ProjectRef {
	if a.Projects != nil {
		return a.Projects()
	}
	return nil
}

func (a *ControllerOf[T]) canDispatch() bool {
	return a.Dispatcher != nil && a.Fence != nil && (a.CanDispatch == nil || a.CanDispatch())
}

func (a *ControllerOf[T]) valid(fence shell.FenceSnapshot) bool {
	return fence.Valid(a.Fence) && (a.ValidateReading == nil || a.ValidateReading())
}

func (a *ControllerOf[T]) clientFactory() func() (*api.Client, error) {
	if a.ClientFactory != nil {
		return a.ClientFactory
	}
	client := a.Client
	return func() (*api.Client, error) {
		if client == nil {
			return nil, errors.New("控制面服务不可用，请确认服务已启动")
		}
		return client, nil
	}
}

func (a *ControllerOf[T]) Scope() string {
	datasets := []string{}
	seen := make(map[string]bool)
	for _, p := range a.projects() {
		if p.DatasetID != "" && !seen[p.DatasetID] {
			datasets = append(datasets, p.DatasetID)
			seen[p.DatasetID] = true
		}
	}
	sort.Strings(datasets)
	scope, _ := json.Marshal(struct {
		Identity string
		Datasets []string
	}{shell.CaptureFence(a.Fence).ReadingIdentity, datasets})
	return string(scope)
}

func (a *ControllerOf[T]) WithdrawScope() {
	scope := a.State.ScopeKey
	if scope == "" {
		scope = a.Scope()
	}
	generation := a.State.Generation
	if a.Fence != nil {
		a.Fence.InvalidateReading()
	}
	if a.State.Generation == generation {
		a.Invalidate("")
	}
	a.State.Snapshot = SnapshotOf[T]{}
	a.State.ScopeKey = scope
	if a.OnReadingDenied != nil {
		a.OnReadingDenied()
	}
}

func (a *ControllerOf[T]) denied(err error) bool {
	var failure *api.APIError
	return (errors.As(err, &failure) && (failure.IsUnauthorized() || failure.IsForbidden())) ||
		(a.ReadingDenied != nil && a.ReadingDenied(err))
}

func (a *ControllerOf[T]) message(err error) string {
	if a.ErrorMessage != nil {
		return a.ErrorMessage(err)
	}
	var failure *api.APIError
	if errors.As(err, &failure) && (failure.IsUnauthorized() || failure.IsForbidden()) {
		return "没有读取权限"
	}
	return err.Error()
}

func (a *ControllerOf[T]) Load() {
	if !a.canDispatch() {
		return
	}
	v := a.State
	a.RefreshAt = time.Now().Add(RefreshInterval)
	a.CancelAll()
	scope := a.Scope()
	if v.ScopeKey != scope {
		*v = StateOf[T]{ScopeKey: scope, Generation: v.Generation, ActivityGeneration: v.ActivityGeneration,
			Day: v.Day, Offset: v.Offset, ActivityLimit: v.ActivityLimit, AllProjects: v.AllProjects, ActivityExpanded: v.ActivityExpanded, Scroll: v.Scroll, SidebarScroll: v.SidebarScroll, LastNavIndY: v.LastNavIndY,
			KnowledgeScroll: v.KnowledgeScroll, ActivityScroll: v.ActivityScroll, SearchHistory: v.SearchHistory,
			DayRequest: v.DayRequest, SnapshotVersion: v.SnapshotVersion + 1,
			PageRun: v.PageRun, KnowledgeRun: v.KnowledgeRun, ActivityRun: v.ActivityRun}
	}
	timeout := 90 * time.Second
	if a.BootstrapTimeout != nil {
		timeout = a.BootstrapTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	v.BootstrapCancel = cancel
	v.Loading, v.ActivityLoading = true, true
	generation, activityGeneration := v.Generation, v.ActivityGeneration
	fence, dispatcher := shell.CaptureFence(a.Fence), a.Dispatcher
	fetch := a.fetchBootstrap(a.projects())
	go func() {
		defer cancel()
		data, err := fetch(ctx)
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		dispatcher.Dispatch(func() {
			if generation != v.Generation || !a.valid(fence) {
				return
			}
			a.applyBootstrap(data, err, activityGeneration)
		})
	}()
}

func (a *ControllerOf[T]) applyBootstrap(data Hydration[T], err error, activityGeneration uint64) {
	v := a.State
	v.Loading = false
	if a.OnBootstrapSettled != nil {
		defer a.OnBootstrapSettled()
	}
	if err != nil {
		withdraw := a.denied(err)
		if withdraw {
			a.WithdrawScope()
		}
		v.Error = a.message(err)
		if withdraw || activityGeneration == v.ActivityGeneration {
			v.ActivityLoading = false
			v.ActivityError = v.Error
		}
		return
	}
	wasLoaded := v.Loaded
	v.Loaded, v.RawLoaded, v.V2Mode = true, true, true
	v.Error = ""
	v.Snapshot = data.Snapshot
	v.SnapshotVersion++
	v.V2Generation, v.V2TotalKnowledge, v.V2DayBuckets = data.Generation, data.TotalKnowledge, data.Buckets
	v.V2RecallStartedAt = data.RecallStartedAt
	if !wasLoaded {
		a.StartEntrance(v.PageRun == 0)
	}
	if activityGeneration == v.ActivityGeneration {
		v.ActivityLoading, v.ActivityLoaded, v.More = false, true, data.More
		v.Events, v.ActivityError, v.RecallError = data.Events, "", ""
		v.GroupedEvents = nil
		v.activityFetchedAt = time.Now()
		if data.HistoryLoaded {
			v.History, v.HistoryLoaded, v.HistoryError = data.History, true, ""
		}
		if v.SearchHistory && !data.HistoryLoaded {
			a.LoadActivity()
		}
	}
	if a.OnBootstrapApplied != nil {
		a.OnBootstrapApplied(data)
	}
	if v.Day != "" {
		a.LoadDay(v.Day, false)
	}
}

func (a *ControllerOf[T]) CancelActivity() {
	v := a.State
	if v.ActivityCancel != nil {
		v.ActivityCancel()
		v.ActivityCancel = nil
	}
	v.ActivityGeneration++
	v.ActivityLoading = false
}

func (a *ControllerOf[T]) LoadActivity() <-chan struct{} {
	done := make(chan struct{})
	if !a.canDispatch() {
		close(done)
		return done
	}
	v := a.State
	a.CancelActivity()
	timeout := 60 * time.Second
	if a.ActivityTimeout != nil {
		timeout = a.ActivityTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	v.ActivityCancel, v.ActivityLoading = cancel, true
	req, history := v.ActivityGeneration, v.SearchHistory
	fence, dispatcher := shell.CaptureFence(a.Fence), a.Dispatcher
	var fetch func(context.Context) (ActivityPage, error)
	switch {
	case history:
		historyFetch := a.fetchHistory()
		fetch = func(ctx context.Context) (ActivityPage, error) {
			events, err := historyFetch(ctx)
			return ActivityPage{Events: events}, err
		}
	case a.FetchActivity != nil:
		fetch = a.FetchActivity()
	default:
		bootstrap := a.fetchBootstrap(a.projects())
		fetch = func(ctx context.Context) (ActivityPage, error) {
			data, err := bootstrap(ctx)
			return ActivityPage{Events: data.Events, More: data.More}, err
		}
	}
	go func() {
		defer cancel()
		page, err := fetch(ctx)
		close(done)
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		dispatcher.Dispatch(func() {
			if req != v.ActivityGeneration || !a.valid(fence) {
				return
			}
			v.ActivityLoading = false
			if a.OnBootstrapSettled != nil && !v.Loading {
				defer a.OnBootstrapSettled()
			}
			if a.denied(err) {
				a.WithdrawScope()
			}
			if history {
				if err != nil {
					v.HistoryError = a.message(err)
					return
				}
				v.History, v.HistoryLoaded, v.HistoryError = page.Events, true, ""
			} else {
				if err != nil {
					v.ActivityError = a.message(err)
					return
				}
				v.Events, v.More, v.RecallError = page.Events, page.More, page.RecallError
				v.ActivityLoaded, v.ActivityError = true, ""
				v.activityFetchedAt = time.Now()
			}
			v.GroupedEvents = nil
		})
	}()
	return done
}

func (a *ControllerOf[T]) SetSearchHistory(history bool) {
	v := a.State
	if v.SearchHistory == history {
		return
	}
	a.CancelActivity()
	v.SearchHistory, v.ActivityExpanded, v.ActivityScroll.Y = history, false, 0
	v.ActivityRun++
	if !history && v.ActivityLoaded && time.Since(v.activityFetchedAt) < 15*time.Second {
		return
	}
	a.LoadActivity()
}

func (a *ControllerOf[T]) RefreshIfDue(now time.Time) bool {
	if !a.canDispatch() || a.windowHidden() || a.State.Loading || a.State.ActivityLoading {
		return false
	}
	if a.RefreshAt.IsZero() {
		a.RefreshAt = now.Add(RefreshInterval)
		return false
	}
	if now.Before(a.RefreshAt) {
		return false
	}
	a.reload()
	return true
}

func (a *ControllerOf[T]) reload() {
	if a.Reload != nil {
		a.Reload()
	} else {
		a.Load()
	}
}

func (a *ControllerOf[T]) windowHidden() bool {
	return a.IsWindowHidden != nil && a.IsWindowHidden()
}

func (a *ControllerOf[T]) setupOnly() bool {
	return a.IsSetupOnly != nil && a.IsSetupOnly()
}

func (a *ControllerOf[T]) StartEntrance(cold bool) {
	v := a.State
	v.ColdStartMotion = cold
	v.PageRun++
	v.KnowledgeRun++
	v.ActivityRun++
	v.HeatmapGrowStart = time.Time{}
}

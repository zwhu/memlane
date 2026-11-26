package settings

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

type UnreadState = api.UnreadScopeStateOf[Item]

type ProjectQueueState struct {
	UnreadState
	InboxPending        int
	InboxFailed         int
	LastError           string
	LastErrorAt         string
	RecentFailures      []api.InboxFailure
	FilteredCount       int
	RecentFiltered      []api.CaptureFilterEvent
	ShowFailureTech     bool
	ShowFilteredDetails bool
	inboxLoaded         bool
	InboxRetryBusy      bool
	InboxRetryError     string
	inboxRevision       uint64

	KnowledgeCatalogVersion string

	PendingDocumentsCount int

	StateVersion                   uint64
	Scroll                         ui.ScrollState
	Activity                       []overviewEvent
	ActivityError                  string
	Loading, Loaded                bool
	Request                        uint64
	Next                           time.Time
	Documents                      []Item
	DocumentError                  string
	Offset                         int
	Backlog                        *BacklogJob
	AutoCapture                    *bool
	AutoCaptureVersion             int64
	AutoCaptureBusy                bool
	AutoCaptureError               string
	BacklogLoading, BacklogBusy    bool
	BacklogLoaded                  bool
	BacklogNext                    time.Time
	BacklogError, BacklogOperation string

	AutoCaptureUpdatedAt, AutoCaptureReason, AutoCaptureActor string
}

func (q *ProjectQueueState) projectAutoCapture(p AccessProject) bool {
	if q.AutoCapture != nil && (p.AutoCapture == nil || q.AutoCaptureVersion >= p.AutoCaptureVersion) {
		return *q.AutoCapture
	}
	return p.AutoCapture == nil || *p.AutoCapture
}

func (q *ProjectQueueState) applyAutoCapture(enabled *bool, version int64) {
	if enabled != nil && version >= q.AutoCaptureVersion {
		if version > q.AutoCaptureVersion {
			q.AutoCaptureUpdatedAt, q.AutoCaptureActor, q.AutoCaptureReason = "", "", ""
		}
		q.AutoCapture = enabled
		q.AutoCaptureVersion = version
	}
}

func (q *ProjectQueueState) applyAutoCaptureMeta(enabled *bool, version int64, updatedAt, actor, reason string) {
	if enabled == nil || version < q.AutoCaptureVersion {
		return
	}
	q.applyAutoCapture(enabled, version)
	q.AutoCaptureUpdatedAt, q.AutoCaptureActor, q.AutoCaptureReason = updatedAt, actor, reason
}

func (q *ProjectQueueState) autoCaptureStatus(p AccessProject) (string, string, string) {
	updatedAt, actor, reason := p.AutoCaptureUpdatedAt, p.AutoCaptureActor, p.AutoCaptureReason
	if q.AutoCapture != nil && (p.AutoCapture == nil || q.AutoCaptureVersion > p.AutoCaptureVersion ||
		q.AutoCaptureVersion == p.AutoCaptureVersion && q.AutoCaptureUpdatedAt != "") {
		updatedAt, actor, reason = q.AutoCaptureUpdatedAt, q.AutoCaptureActor, q.AutoCaptureReason
	}
	return formatAutoCaptureStatus(q.projectAutoCapture(p), updatedAt, actor, reason)
}

func (a *Controller) ExistingProjectQueue(p AccessProject) *ProjectQueueState {
	if a.access.Queues == nil {
		return nil
	}
	identity := a.cachedBaseReadingIdentity()
	return a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
}

func (a *Controller) ProjectQueue(p AccessProject) *ProjectQueueState {
	if a.access.Queues == nil {
		a.access.Queues = map[string]*ProjectQueueState{}
	}
	identity := a.cachedBaseReadingIdentity()
	key := identity + "/" + p.ID + "/" + p.Dataset
	q := a.access.Queues[key]
	if q == nil {
		q = &ProjectQueueState{}
		a.access.Queues[key] = q
	}
	return q
}

func (a *Controller) projectQueueStateFor(p AccessProject) *ProjectQueueState {
	return a.ProjectQueue(p)
}

const (
	projectQueueIdleInterval   = 30 * time.Second
	projectQueueActiveInterval = 5 * time.Second
)

func (a *Controller) projectQueue(p AccessProject) *ProjectQueueState {
	q := a.ProjectQueue(p)
	if !q.Loading && !a.access.Busy && (!q.Loaded || !time.Now().Before(q.Next)) && a.canDispatch() {
		a.LoadProjectQueue(q, p)
	}
	return q
}

type projectQueueFetchInput struct {
	project      AccessProject
	client       Client
	control      *ProjectControlConfig
	setupOnly    bool
	refreshInbox bool
}

type projectQueueSnapshot struct {
	setupOnly             bool
	gateway               bool
	readingState          *api.ReadingStateView
	unreadPage            *v2ProjectKnowledgeResponse
	inbox                 *api.ProjectView
	authDenied            bool
	v2Summary             ProjectMessageSummary
	v2SummaryOK           bool
	summaryBacklogErr     string
	v2ReadStateErr        string
	pendingDocumentsCount int
	knowledgeVersion      string
	documentErr           error
	activity              []activityRow
	activityErr           error
}

type projectQueueApplyMeta struct {
	request       uint64
	authEpoch     uint64
	baseIdentity  string
	inboxRevision uint64
}

func (a *Controller) LoadProjectQueue(q *ProjectQueueState, p AccessProject) {
	if q.Loading || a.access.Busy || !a.canDispatch() {
		return
	}
	q.Loading = true
	q.Request++
	request, authEpoch := q.Request, a.authEpoch()
	q.Next = time.Now().Add(projectQueueIdleInterval)
	if q.InboxPending > 0 || q.Backlog.active() {
		q.Next = time.Now().Add(projectQueueActiveInterval)
	}
	baseIdentity := a.cachedBaseReadingIdentity()
	var control *ProjectControlConfig
	if ctrl, ok := a.accessClient(); ok {
		cfg := ctrl.Config
		control = &cfg
	}
	in := projectQueueFetchInput{
		project: p, client: a.client, control: control,
		setupOnly: a.isSetupOnlyRuntime(), refreshInbox: q.inboxRevision > 0,
	}
	meta := projectQueueApplyMeta{request, authEpoch, baseIdentity, q.inboxRevision}
	update := a.dispatch
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		snap := a.fetchProjectQueueSnapshot(ctx, in)
		rec := reconcileProjectQueueState(p, snap)
		update(func() { a.applyProjectQueueResult(q, p, meta, snap, rec) })
	}()
}

func resetProjectQueue(q *ProjectQueueState) {
	if q == nil {
		return
	}
	q.Request++
	q.Loading = false
	q.UnreadState = UnreadState{}
	q.Activity = nil
	q.Documents = nil
	q.PendingDocumentsCount = 0
	q.StateVersion++
}

func (q *ProjectQueueState) UnreadCount() int {
	if q.UnreadState.Error != "" {
		return 0
	}
	if q.ServerUnreadKnown {
		return max(0, q.ServerUnreadCount)
	}
	return 0
}

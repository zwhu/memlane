package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/reading"
)

type UnreadState = reading.UnreadScopeStateOf[Item]

type projectQueueState struct {
	UnreadState
	InboxPending    int
	InboxFailed     int
	LastError       string
	InboxRetryBusy  bool
	InboxRetryError string
	inboxRevision   uint64

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
	Backlog                        *backlogJob
	AutoCapture                    *bool
	AutoCaptureVersion             int64
	AutoCaptureBusy                bool
	AutoCaptureError               string
	BacklogLoading, BacklogBusy    bool
	BacklogLoaded                  bool
	BacklogNext                    time.Time
	BacklogError, BacklogOperation string
}

func (q *projectQueueState) projectAutoCapture(p AccessProject) bool {
	if q.AutoCapture != nil && (p.AutoCapture == nil || q.AutoCaptureVersion >= p.AutoCaptureVersion) {
		return *q.AutoCapture
	}
	return p.AutoCapture == nil || *p.AutoCapture
}

func (q *projectQueueState) applyAutoCapture(enabled *bool, version int64) {
	if enabled != nil && version >= q.AutoCaptureVersion {
		q.AutoCapture = enabled
		q.AutoCaptureVersion = version
	}
}

func (a *App) existingProjectQueueState(p AccessProject) *projectQueueState {
	if a.access.Queues == nil {
		return nil
	}
	identity := a.cachedBaseReadingIdentity()
	return a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
}

func (a *App) projectQueueStateForAsync(p AccessProject) *projectQueueState {
	if a.access.Queues == nil {
		a.access.Queues = map[string]*projectQueueState{}
	}
	identity := a.cachedBaseReadingIdentity()
	key := identity + "/" + p.ID + "/" + p.Dataset
	q := a.access.Queues[key]
	if q == nil {
		q = &projectQueueState{}
		a.access.Queues[key] = q
	}
	return q
}

func (a *App) projectQueueStateFor(p AccessProject) *projectQueueState {
	return a.projectQueueStateForAsync(p)
}

func (a *App) projectQueue(p AccessProject) *projectQueueState {
	q := a.projectQueueStateForAsync(p)
	if !q.Loading && !a.access.Busy && (!q.Loaded || time.Now().After(q.Next)) && a.Update != nil {
		a.loadProjectQueue(q, p)
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
	v2Summary             v2MessageSummaryResponse
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

func (a *App) loadProjectQueue(q *projectQueueState, p AccessProject) {
	if q.Loading || a.access.Busy || a.Update == nil {
		return
	}
	q.Loading = true
	q.Request++
	request, authEpoch := q.Request, a.authEpoch
	q.Next = time.Now().Add(30 * time.Second)
	if q.Backlog.active() {
		q.Next = time.Now().Add(5 * time.Second)
	}
	baseIdentity := a.cachedBaseReadingIdentity()
	var control *ProjectControlConfig
	if ctrl, ok := a.accessClient(); ok {
		cfg := ctrl.Config
		control = &cfg
	}
	in := projectQueueFetchInput{
		project: p, client: a.Client, control: control,
		setupOnly: a.isSetupOnlyRuntime(), refreshInbox: q.inboxRevision > 0,
	}
	meta := projectQueueApplyMeta{request, authEpoch, baseIdentity, q.inboxRevision}
	update := a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		snap := a.fetchProjectQueueSnapshot(ctx, in)
		rec := reconcileProjectQueueState(p, snap)
		update(func() { a.applyProjectQueueResult(q, p, meta, snap, rec) })
	}()
}

func (a *App) fetchProjectQueueSnapshot(ctx context.Context, in projectQueueFetchInput) projectQueueSnapshot {
	snap := projectQueueSnapshot{setupOnly: in.setupOnly}
	a.fetchProjectQueueSummary(ctx, in, &snap)

	snap.authDenied = readingAuthDenied(snap.documentErr) || readingAuthDenied(snap.activityErr) ||
		isControlErrorCode(snap.documentErr, 503, "reading_contract_mismatch")
	if !in.setupOnly && in.control != nil && !snap.authDenied && (snap.gateway || !snap.v2SummaryOK || in.refreshInbox) {
		var err error
		snap.inbox, err = fetchProjectInbox(ctx, in)
		if err == nil && isControlNotFound(snap.documentErr) {
			// The Go gateway exposes the inbox without the legacy summary.
			snap.documentErr, snap.activityErr = nil, nil
		}
		if err != nil {
			if snap.gateway {
				snap.documentErr = err
			}
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && (apiErr.IsUnauthorized() || apiErr.IsForbidden()) {
				snap.authDenied = true
				snap.documentErr = err
			}
		}
	}
	return snap
}

func fetchProjectInbox(ctx context.Context, in projectQueueFetchInput) (*api.ProjectView, error) {
	identity, err := readingIdentity(in.client)
	if err != nil {
		return nil, err
	}
	manager := ProjectControlClient{Config: *in.control, HTTP: in.client.HTTP}
	client, err := newAppAPIClient(manager, identity)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	inbox, err := client.GetProject(ctx, in.project.ID)
	if err != nil {
		return nil, err
	}
	if inbox.ID != in.project.ID || inbox.DatasetID != in.project.Dataset {
		return nil, errors.New("inbox response does not match the requested project and dataset")
	}
	return &inbox, nil
}

func (a *App) fetchProjectQueueSummary(ctx context.Context, in projectQueueFetchInput, snap *projectQueueSnapshot) {
	if in.control == nil {
		err := fmt.Errorf("控制面服务不可用，请确认服务已启动")
		snap.documentErr, snap.activityErr = err, err
		return
	}
	client, p := in.client, in.project
	identity, _ := readingIdentity(Client{Config: client.Config})
	summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	manager := ProjectControlClient{Config: *in.control, HTTP: client.HTTP}
	if err := manager.requireReadingContract(summaryCtx); err != nil {
		snap.documentErr, snap.activityErr = err, err
		return
	}
	if !in.setupOnly {
		gateway, err := manager.probeFeature(summaryCtx, "async_capture")
		if err != nil {
			snap.documentErr, snap.activityErr = err, err
			return
		}
		if gateway {
			snap.gateway = true
			snap.readingState, err = fetchGatewayQueueReadingState(summaryCtx, manager, identity, p)
			if err == nil && snap.readingState.UnreadCount > 10 {
				page, pageErr := manager.knowledgePage(summaryCtx, identity, api.KnowledgePageOptions{
					ProjectID: p.ID, Sort: "date", UnreadOnly: true, Limit: 10,
				})
				if readingAuthDenied(pageErr) {
					err = pageErr
				} else if pageErr == nil && page.UnreadStateRev == snap.readingState.StateRev && page.UnreadCount == snap.readingState.UnreadCount {
					snap.unreadPage = &page
				}
			}
			if readingAuthDenied(err) {
				snap.documentErr = err
			} else if err != nil {
				snap.v2ReadStateErr = err.Error()
			}
			return
		}
	}
	v2Res, v2Err := manager.projectMessageSummaryV2(summaryCtx, identity, p.ID)
	cancel()
	if v2Err == nil {
		snap.v2Summary = v2Res
		snap.v2SummaryOK = true
		if msg := v2Res.Errors["documents"]; msg != "" {
			snap.documentErr = fmt.Errorf("%s", msg)
		} else {
			snap.pendingDocumentsCount = v2Res.PendingDocumentsCount
			snap.knowledgeVersion = v2Res.KnowledgeCatalogVersion
		}
		if msg := v2Res.Errors["activity"]; msg != "" {
			snap.activityErr = fmt.Errorf("%s", msg)
		} else {
			snap.activity = v2Res.Activity
		}
		snap.summaryBacklogErr = v2Res.Errors["backlog"]
		snap.v2ReadStateErr = v2Res.Errors["reading_state"]
		return
	}
	snap.documentErr, snap.activityErr = v2Err, v2Err
}

func fetchGatewayQueueReadingState(ctx context.Context, manager ProjectControlClient, identity string, p AccessProject) (*api.ReadingStateView, error) {
	response, err := (projectUnreadAPI{manager, identity}).ReadingState(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, state := range response.Projects {
		if state.ProjectID == p.ID && state.DatasetID == p.Dataset {
			return &state, nil
		}
	}
	return nil, fmt.Errorf("项目未读状态暂时无法读取")
}

type projectQueueReconciled struct {
	AuthDenied              bool
	AuthDeniedError         string
	BacklogError            string
	UpdateBacklog           bool
	Backlog                 *backlogJob
	ActivityError           string
	Activity                []overviewEvent
	DocumentError           string
	PendingDocumentsCount   int
	KnowledgeReadError      string
	ServerUnreadCount       int
	ServerUnreadStateRev    int64
	UnreadHasMore           bool
	UnreadKnowledge         []Item
	KnowledgeCatalogVersion string
}

func reconcileProjectQueueState(p AccessProject, snap projectQueueSnapshot) projectQueueReconciled {
	rec := projectQueueReconciled{AuthDenied: snap.authDenied}
	if rec.AuthDenied {
		rec.AuthDeniedError = controlUserError(snap.documentErr)
		return rec
	}
	if snap.v2SummaryOK {
		rec.UpdateBacklog = snap.summaryBacklogErr == ""
		if rec.UpdateBacklog {
			summary := snap.v2Summary
			rec.Backlog = summary.Backlog
		} else if !snap.setupOnly {
			rec.BacklogError = "后台采集状态暂时无法读取；任务不会因此重复启动"
		}
	}
	if snap.activityErr != nil {
		rec.ActivityError = "近期提炼记录暂时无法读取"
	} else {
		projectActivity := make([]activityRow, 0, len(snap.activity))
		for _, row := range snap.activity {
			if row.Dataset == p.Dataset {
				projectActivity = append(projectActivity, row)
			}
		}
		for _, event := range aggregateActivity(projectActivity, []Project{{ID: p.ID, Name: p.Name, Dataset: p.Dataset}}) {
			switch event.Title {
			case "提炼记忆", "处理记忆", "整理知识", "整理记忆":
				rec.Activity = append(rec.Activity, event)
			}
		}
	}
	if snap.documentErr != nil {
		rec.DocumentError = "提炼状态暂时无法读取"
		return rec
	}
	rec.PendingDocumentsCount = snap.pendingDocumentsCount
	rec.KnowledgeCatalogVersion = snap.knowledgeVersion
	if snap.v2SummaryOK {
		if snap.v2ReadStateErr != "" {
			rec.KnowledgeReadError = "未读状态暂时无法读取"
		} else {
			summary := snap.v2Summary
			rec.ServerUnreadCount = summary.UnreadCount
			rec.ServerUnreadStateRev = summary.UnreadStateRev
			rec.UnreadHasMore = summary.UnreadHasMore || summary.UnreadCount > len(summary.UnreadPreview)
			rec.UnreadKnowledge = summary.unreadPreviewItems(p.Dataset)
		}
	}
	if snap.gateway {
		if snap.v2ReadStateErr != "" {
			rec.KnowledgeReadError = "未读状态暂时无法读取"
		} else if snap.readingState != nil {
			rec.ServerUnreadCount = snap.readingState.UnreadCount
			rec.ServerUnreadStateRev = snap.readingState.StateRev
			if snap.unreadPage != nil {
				for _, row := range snap.unreadPage.Items {
					if row.DatasetID == p.Dataset && row.DocID != "" {
						rec.UnreadKnowledge = append(rec.UnreadKnowledge, row.toItem(""))
					}
				}
			}
			rec.UnreadHasMore = rec.ServerUnreadCount > len(rec.UnreadKnowledge)
		}
		if snap.inbox != nil {
			rec.KnowledgeCatalogVersion = snap.inbox.CatalogVersion
		}
	}
	return rec
}

func (a *App) applyProjectQueueResult(q *projectQueueState, p AccessProject, meta projectQueueApplyMeta, snap projectQueueSnapshot, rec projectQueueReconciled) {
	if meta.request != q.Request {
		return
	}
	q.Loading = false
	if rec.AuthDenied {
		q.InboxPending, q.InboxFailed, q.LastError, q.InboxRetryError = 0, 0, "", ""
		q.DocumentError = rec.AuthDeniedError
		q.Activity = nil
		a.InvalidateReading(p.Dataset)
		return
	}
	if meta.authEpoch != a.authEpoch {
		return
	}
	_, current, _ := readingIdentityPair(Client{Config: a.Client.Config})
	if meta.baseIdentity != current {
		q.InboxPending, q.InboxFailed, q.LastError, q.InboxRetryError = 0, 0, "", ""
		q.Documents = nil
		q.Loaded = false
		return
	}
	if snap.inbox != nil && snap.inbox.AutoCaptureVersion > 0 {
		autoCapture := snap.inbox.AutoCapture
		q.applyAutoCapture(&autoCapture, int64(snap.inbox.AutoCaptureVersion))
	}
	if snap.inbox != nil && meta.inboxRevision == q.inboxRevision {
		q.InboxPending, q.InboxFailed, q.LastError = snap.inbox.InboxPending, snap.inbox.InboxFailed, snap.inbox.LastError
		if q.InboxFailed == 0 && q.LastError == "" {
			q.InboxRetryError = ""
		}
	}
	if a.isSetupOnlyRuntime() {
		q.BacklogError = ""
	} else if snap.v2SummaryOK {
		q.BacklogError = rec.BacklogError
	}
	if snap.v2SummaryOK {
		q.applyAutoCapture(snap.v2Summary.AutoCapture, snap.v2Summary.AutoCaptureVersion)
		if rec.UpdateBacklog {
			q.Backlog = rec.Backlog
			q.BacklogLoaded = true
		}
	}
	q.Loaded = true
	q.ActivityError = rec.ActivityError
	if rec.ActivityError == "" {
		q.Activity = rec.Activity
	}
	q.DocumentError = rec.DocumentError
	prevCatalogVersion := q.KnowledgeCatalogVersion
	if rec.DocumentError == "" {
		if snap.v2SummaryOK {
			q.PendingDocumentsCount = rec.PendingDocumentsCount
			if q.PendingDocumentsCount == 0 {
				q.Documents, q.Offset = nil, 0
			}
		}
		if snap.v2SummaryOK || snap.gateway {
			q.UnreadState.Error = rec.KnowledgeReadError
			if rec.KnowledgeReadError == "" &&
				applyServerUnreadSnapshot(q, rec.ServerUnreadCount, rec.ServerUnreadStateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset)) {
				q.UnreadHasMore = rec.UnreadHasMore
				q.UnreadPreview = rec.UnreadKnowledge
				if len(q.ReadIDs) > 0 {
					// Pending local marks remain hidden from a racing server preview.
					filtered := q.UnreadPreview[:0]
					for _, item := range q.UnreadPreview {
						if !q.ReadIDs[item.ID] {
							filtered = append(filtered, item)
						}
					}
					q.UnreadPreview = filtered
				}
			}
			if rec.KnowledgeCatalogVersion != "" {
				q.KnowledgeCatalogVersion = rec.KnowledgeCatalogVersion
			}

		}
	}
	q.StateVersion++
	if prevCatalogVersion != "" && q.KnowledgeCatalogVersion != "" && prevCatalogVersion != q.KnowledgeCatalogVersion {
		a.handleKnowledgeCatalogVersionChanged(p.ID, p.Dataset, q.KnowledgeCatalogVersion)
	}
}

func (a *App) retryProjectInbox(q *projectQueueState, p AccessProject) {
	if a.isSetupOnlyRuntime() || q.InboxRetryBusy || a.access.Busy || a.Update == nil {
		return
	}
	control, ok := a.accessClient()
	if !ok {
		return
	}
	identity, baseIdentity, err := readingIdentityPair(a.Client)
	if err != nil {
		q.InboxRetryError = "读取身份暂不可用"
		return
	}
	q.InboxRetryBusy = true
	q.InboxRetryError = ""
	authEpoch, update := a.authEpoch, a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		client, err := newAppAPIClient(control, identity)
		if err == nil {
			_, err = client.RetryProject(ctx, p.ID)
		}
		update(func() {
			q.InboxRetryBusy = false
			current, ok := a.accessClient()
			currentIdentity, currentBase, identityErr := readingIdentityPair(a.Client)
			if !ok || current.Config != control.Config || identityErr != nil || currentBase != baseIdentity ||
				currentIdentity != identity || a.authEpoch != authEpoch || a.isSetupOnlyRuntime() {
				return
			}
			if err != nil {
				q.InboxRetryError = "重试结果未确认，请刷新后核对。"
				var rejected *api.APIError
				if errors.As(err, &rejected) && rejected.Message != "" {
					q.InboxRetryError = "重试未确认：" + rejected.Message
				}
				return
			}
			q.inboxRevision++
			q.Next = time.Time{}
			if !q.Loading && !a.access.Busy {
				a.loadProjectQueue(q, p)
			}
		})
	}()
}

type trayProjectAlert struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	ActionCount  int
	ErrorCount   int
}

type trayProjectProgress struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	Count        int
}

// Reuse the settings queue reader with bounded concurrent projects. Enumeration
// only reads status; it never saves materials or starts knowledge processing.
func (a *App) refreshSidebarQueueIfDue(now time.Time) {
	if a.windowInactive || a.windowHidden || a.coldStartOverviewPending() {
		return
	}
	a.refreshSidebarQueueForTray(now)
}

func (a *App) refreshSidebarQueueForTray(now time.Time) {
	if a.Update == nil || !a.access.Loaded || a.access.Busy {
		return
	}
	projects := a.access.Directory.Projects
	if len(projects) == 0 {
		return
	}
	active := 0
	for _, p := range projects {
		if q := a.existingProjectQueueState(p); q != nil && q.Loading {
			active++
		}
	}
	start := a.access.SidebarQueueIndex % len(projects)
	for offset := 0; offset < len(projects) && active < 3; offset++ {
		index := (start + offset) % len(projects)
		p := projects[index]
		q := a.projectQueueStateForAsync(p)
		if !q.Loading && (!q.Loaded || now.After(q.Next)) {
			a.loadProjectQueue(q, p)
			if q.Loading {
				active++
			}
			a.access.SidebarQueueIndex = (index + 1) % len(projects)
		}
	}
}

type trayProjectSummary struct {
	Alerts        []trayProjectAlert
	Progress      []trayProjectProgress
	HasErrorAlert bool
}

type trayFlyoutCache struct {
	summarySig          uint64
	summaryValid        bool
	summary             trayProjectSummary
	summaryComputations uint64

	todayGen            uint64
	todayLoaded         bool
	todayKnowledgeCount int
	todayDay            string
	todayCount          int
	todayValid          bool
	todayComputations   uint64
}

func fnvMixUint64(h, v uint64) uint64 {
	const prime = 1099511628211
	h ^= v & 0xff
	h *= prime
	h ^= (v >> 8) & 0xff
	h *= prime
	h ^= (v >> 16) & 0xff
	h *= prime
	h ^= (v >> 24) & 0xff
	h *= prime
	h ^= (v >> 32) & 0xff
	h *= prime
	h ^= (v >> 40) & 0xff
	h *= prime
	h ^= (v >> 48) & 0xff
	h *= prime
	h ^= (v >> 56) & 0xff
	h *= prime
	return h
}

func fnvMixString(h uint64, s string) uint64 {
	const prime = 1099511628211
	h = fnvMixUint64(h, uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func fnvMixBool(h uint64, b bool) uint64 {
	if b {
		return fnvMixUint64(h, 1)
	}
	return fnvMixUint64(h, 0)
}

func (a *App) trayProjectAlerts() []trayProjectAlert {
	return a.trayCachedSummary().Alerts
}

func (a *App) trayProjectProgressItems() []trayProjectProgress {
	return a.trayCachedSummary().Progress
}

func (a *App) traySummaryFingerprint() uint64 {
	h := uint64(14695981039346656037)
	for _, p := range a.Config.Projects {
		h = fnvMixString(h, p.ID)
		h = fnvMixString(h, p.Dataset)
		h = fnvMixString(h, p.Name)
	}
	for _, p := range a.access.Directory.Projects {
		h = fnvMixString(h, p.ID)
		h = fnvMixString(h, p.Dataset)
		h = fnvMixString(h, p.Name)
		h = fnvMixBool(h, p.policySyncUnconfirmed())
		q := a.existingProjectQueueState(p)
		h = fnvMixBool(h, q != nil)
		if q != nil {
			h = fnvMixBool(h, q.Loaded)
			h = fnvMixBool(h, q.LastError != "")
			h = fnvMixBool(h, q.DocumentError != "")
			h = fnvMixUint64(h, uint64(q.InboxFailed))
			h = fnvMixUint64(h, uint64(q.InboxPending))
			h = fnvMixUint64(h, uint64(max(q.PendingDocumentsCount, len(q.Documents))))
		}
	}
	return h
}

func (a *App) trayCachedSummary() trayProjectSummary {
	sig := a.traySummaryFingerprint()
	if a.trayCache.summaryValid && a.trayCache.summarySig == sig {
		return a.trayCache.summary
	}
	indexByID, indexByDataset := make(map[string]int), make(map[string]int)
	for i, p := range a.Config.Projects {
		if _, ok := indexByID[p.ID]; !ok && p.ID != "" {
			indexByID[p.ID] = i
		}
		if _, ok := indexByDataset[p.Dataset]; !ok && p.Dataset != "" {
			indexByDataset[p.Dataset] = i
		}
	}
	var out trayProjectSummary
	for _, p := range a.access.Directory.Projects {
		syncUnconfirmed := p.policySyncUnconfirmed()
		q := a.existingProjectQueueState(p)
		errors, progress := 0, 0
		if q != nil {
			if q.InboxFailed > 0 || q.LastError != "" || q.DocumentError != "" {
				errors = max(1, q.InboxFailed)
			}
			if q.Loaded && q.DocumentError == "" {
				progress = max(0, q.InboxPending) + max(q.PendingDocumentsCount, len(q.Documents))
			}
		}
		if !syncUnconfirmed && errors == 0 && progress == 0 {
			continue
		}
		index, ok := indexByID[p.ID]
		if byDataset, found := indexByDataset[p.Dataset]; found && (!ok || byDataset < index) {
			index, ok = byDataset, true
		}
		if !ok {
			index = -1
		}
		name := p.Name
		if name == "" && ok {
			name = a.Config.Projects[index].Name
		}
		if name == "" {
			name = p.ID
		}
		if syncUnconfirmed || errors > 0 {
			actions := 0
			if syncUnconfirmed {
				actions = 1
			}
			out.Alerts = append(out.Alerts, trayProjectAlert{p.ID, name, index, actions, errors})
			out.HasErrorAlert = out.HasErrorAlert || errors > 0
		}
		if progress > 0 {
			out.Progress = append(out.Progress, trayProjectProgress{p.ID, name, index, progress})
		}
	}
	a.trayCache.summarySig, a.trayCache.summaryValid, a.trayCache.summary = sig, true, out
	a.trayCache.summaryComputations++
	return out
}

package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

type pendingMessageDetail struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Unknown bool   `json:"unknown"`
}

type PendingMaterials struct {
	Details          []pendingMessageDetail `json:"details,omitempty"`
	Pending          int                    `json:"pending"`
	UnknownEntryIDs  []string               `json:"unknown_entry_ids"`
	Signature        string                 `json:"signature"`
	Unknown          int                    `json:"unknown_outcomes"`
	RetryableUnknown int                    `json:"retryable_unknown"`
	Blocked          *struct {
		Reason string `json:"reason"`
	} `json:"blocked"`
	Drained         int    `json:"drained"`
	Discarded       int    `json:"discarded"`
	Error           string `json:"error"`
	Checked         bool   `json:"checked"`
	Matched         int    `json:"matched_unknown"`
	SessionID       string `json:"session_id"`
	SessionInactive bool   `json:"session_inactive,omitempty"`
}

var pendingBufferObserveSeq atomic.Uint64

type pendingBufferView struct {
	Status           PendingMaterials
	Loaded, Busy     bool
	Message          string
	ReadError        string
	ReadCode         string
	Details          []pendingMessageDetail
	DetailsLoaded    bool
	DetailsError     string
	Observed         bool
	Added            int
	Action           string
	CheckDetailsOpen bool
	DiagnosticsOpen  bool
	ExpandedMessages map[string]bool
	AllMessagesOpen  bool
}

func (v *pendingBufferView) observe(status PendingMaterials) {
	if v.Observed && status.Pending > v.Status.Pending {
		v.Added += status.Pending - v.Status.Pending
	}
	if status.Signature == v.Status.Signature && v.Status.Checked {
		status.Checked, status.Matched, status.SessionID = v.Status.Checked, v.Status.Matched, v.Status.SessionID
	}
	if status.Signature != v.Status.Signature {
		v.Details = nil
		v.DetailsLoaded = false
		v.ExpandedMessages = nil
		v.AllMessagesOpen = false
	}
	v.Status = status
	if v.ReadError != "" && v.Message == v.ReadError {
		v.Message = ""
	}
	v.ReadError = ""
	v.ReadCode = ""
	v.Loaded, v.Observed = true, true
	pendingBufferObserveSeq.Add(1)
}

func shortSessionLabel(key string) string {
	r := []rune(key)
	if len(r) > 22 {
		return string(r[:10]) + "…" + string(r[len(r)-8:])
	}
	return key
}

type queuedSession struct {
	Agent   AccessAgent
	Binding ProjectBinding
	View    *pendingBufferView
}
type processingNotice struct {
	Operation   string `json:"operation_id"`
	Request     string `json:"request_id"`
	At          string `json:"at"`
	RecoveredAt string `json:"recovered_at,omitempty"`
	ResolvedAt  string `json:"resolved_at,omitempty"`
	FailedAt    string `json:"failed_at,omitempty"`
	Status      int    `json:"http_status"`
	Code        string `json:"code"`
	Backend     string `json:"backend"`
}

func (n processingNotice) resolved() bool {
	return n.hasTerminalTime(n.ResolvedAt)
}

func (n processingNotice) hasTerminalTime(terminal string) bool {
	at, validAt := officialTime(n.At)
	ended, validEnd := officialTime(terminal)
	return validAt && validEnd && !ended.Before(at)
}

type processingIssue struct {
	AgentID         string  `json:"agent_id"`
	Host            string  `json:"host"`
	SessionID       string  `json:"session_id"`
	Processing      string  `json:"processing"`
	Reason          string  `json:"reason"`
	AttemptedAt     float64 `json:"attempted_at"`
	ReconcileStatus string  `json:"reconcile_status"`
	ReconciledAt    float64 `json:"reconciled_at"`
}

type projectQueueState struct {
	InboxPending                   int
	InboxFailed                    int
	LastError                      string
	InboxRetryBusy                 bool
	InboxRetryError                string
	inboxRevision                  uint64
	BufferReviews                  []bufferReview
	noticeAckEpoch                 uint64
	NoticeAcknowledgements         map[string]noticeAcknowledgement
	NoticeAcknowledgementError     string
	NoticeHistoryOpen              bool
	ResolvedProcessingOpen         bool
	ProcessingNotices              []processingNotice
	ProcessingIssues               []processingIssue
	ProcessingActionBusy           bool
	ProcessingActionError          string
	ProcessingReadError            string
	NotificationFailures           map[string]projectFailure
	KnowledgeRead                  knowledgeReadState
	KnowledgeCatalogVersion        string
	ServerUnreadKnown              bool
	ServerUnreadCount              int
	ServerUnreadStateRev           int64
	ServerUnreadSnapshot           uint64
	UnreadHasMore                  bool
	PendingDocumentsCount          int
	UnreadKnowledge                []Item
	KnowledgeReadError             string
	StateVersion                   uint64
	Scroll                         ui.ScrollState
	LocatedNoticeKey               string
	DetailNoticeKey                string
	DetailDialogOpen               bool
	DetailDiagnosticsOpen          bool
	Reader                         *pane
	Activity                       []overviewEvent
	ActivityError                  string
	Loading, Loaded                bool
	Request                        uint64
	Next                           time.Time
	Sessions                       []queuedSession
	Checks                         map[string]pendingCheckState
	Documents                      []Item
	DocumentError                  string
	Offset                         int
	Backlog                        *backlogJob
	AutomaticSave                  bool
	AutoCapture                    *bool
	AutoCaptureVersion             int64
	AutoCaptureBusy                bool
	AutoCaptureError               string
	SaveDetailsOpen                bool
	BacklogLoading, BacklogBusy    bool
	BacklogLoaded                  bool
	BacklogNext                    time.Time
	BacklogError, BacklogOperation string
	localStateLoaded               bool
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
	if a.access.Queues == nil {
		a.access.Queues = map[string]*projectQueueState{}
	}
	identity := a.cachedBaseReadingIdentity()
	key := identity + "/" + p.ID + "/" + p.Dataset
	q := a.access.Queues[key]
	if q == nil {
		q = &projectQueueState{}
		q.NoticeAcknowledgements, q.NoticeAcknowledgementError = a.readNoticeAcknowledgements(p)
		if path, err := a.knowledgeReadPath(p); err == nil {
			if s, readErr := readKnowledgeState(path); readErr == nil && s.Initialized {
				q.KnowledgeRead = s
			}
		}
		q.localStateLoaded = true
		a.access.Queues[key] = q
	}
	return q
}
func (a *App) projectQueue(p AccessProject) *projectQueueState {
	var q *projectQueueState
	if a.Update != nil {
		q = a.projectQueueStateForAsync(p)
	} else {
		q = a.projectQueueStateFor(p)
	}
	if time.Now().After(q.BacklogNext) {
		a.refreshBacklog(q, p)
	}
	if !q.Loading && !a.access.Busy && (!q.Loaded || time.Now().After(q.Next)) && a.Update != nil {
		a.loadProjectQueue(q, p)
	}
	return q
}

type projectQueueFetchInput struct {
	project          AccessProject
	client           Client
	control          *ProjectControlConfig
	stateDir         string
	migrationProject AccessProject
	needLocalState   bool
	setupOnly        bool
	refreshInbox     bool
}

type projectQueueSnapshot struct {
	setupOnly             bool
	inbox                 *api.ProjectView
	authDenied            bool
	v2Summary             v2MessageSummaryResponse
	v2SummaryOK           bool
	summaryBacklogErr     string
	v2ReadStateErr        string
	documents             []Item
	pendingDocumentsCount int
	knowledgeVersion      string
	documentErr           error
	activity              []activityRow
	activityErr           error
	notices               []processingNotice
	noticeErr             error
	localKnowledgeRead    knowledgeReadState
	ackPath               string
	noticeAckSeq          uint64
	noticeAcks            map[string]noticeAcknowledgement
	noticeAckErr          string
}

type projectQueueApplyMeta struct {
	request       uint64
	authEpoch     uint64
	ackEpoch      uint64
	baseIdentity  string
	inboxRevision uint64
}

func (a *App) loadProjectQueue(q *projectQueueState, p AccessProject) {
	if q.Loading || a.access.Busy {
		return
	}
	q.Loading = true
	q.Request++
	request, authEpoch := q.Request, a.authEpoch
	ackEpoch := q.noticeAckEpoch
	q.Next = time.Now().Add(30 * time.Second)
	if q.Backlog.active() {
		q.Next = time.Now().Add(5 * time.Second)
	}
	baseIdentity := a.cachedBaseReadingIdentity()
	needLocalState := !q.localStateLoaded
	var control *ProjectControlConfig
	if ctrl, ok := a.accessClient(); ok {
		cfg := ctrl.Config
		control = &cfg
	}
	stateDir := a.readingStateDir()
	// Queue loads only depend on migration for their own project.
	migrationProject := p
	for _, ap := range a.access.Directory.Projects {
		if ap.ID == p.ID && ap.Dataset == p.Dataset {
			migrationProject = ap
			break
		}
	}
	in := projectQueueFetchInput{
		project: p, client: a.Client, control: control,
		stateDir: stateDir, migrationProject: migrationProject, needLocalState: needLocalState, setupOnly: a.isSetupOnlyRuntime(), refreshInbox: q.inboxRevision > 0,
	}
	meta := projectQueueApplyMeta{request, authEpoch, ackEpoch, baseIdentity, q.inboxRevision}
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
	var workers sync.WaitGroup
	workers.Go(func() {
		a.fetchProjectQueueSummary(ctx, in, &snap)
	})
	workers.Go(func() {
		p := in.project
		snap.ackPath, _ = noticeAcknowledgementPath(in.stateDir, in.client, p)
		snap.noticeAcks, snap.noticeAckErr, snap.noticeAckSeq = readNoticeAcknowledgementsWithSeq(snap.ackPath)
		if in.needLocalState {
			if path, err := knowledgeReadPath(in.stateDir, in.client, p); err == nil {
				if s, readErr := readKnowledgeState(path); readErr == nil && s.Initialized {
					snap.localKnowledgeRead = s
				}
			}
		}
	})

	workers.Wait()

	snap.authDenied = readingAuthDenied(snap.documentErr) || readingAuthDenied(snap.noticeErr) || readingAuthDenied(snap.activityErr) ||
		isControlErrorCode(snap.documentErr, 503, "reading_contract_mismatch")
	if !in.setupOnly && in.control != nil && !snap.authDenied && (!snap.v2SummaryOK || in.refreshInbox) {
		var err error
		snap.inbox, err = fetchProjectInbox(ctx, in)
		if err != nil {
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
	client, err := newInboxClient(manager, identity)
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
		snap.noticeErr, snap.documentErr, snap.activityErr = err, err, err
		return
	}
	client, p := in.client, in.project
	identity, _ := readingIdentity(Client{Config: client.Config})
	migrationProjects := []AccessProject{in.migrationProject}
	summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	manager := ProjectControlClient{Config: *in.control, HTTP: client.HTTP}
	if err := manager.requireReadingContract(summaryCtx); err != nil {
		snap.noticeErr, snap.documentErr, snap.activityErr = err, err, err
		return
	}
	if migErr := a.migrateReadingStateV2Snapshot(summaryCtx, manager, identity, in.stateDir, migrationProjects); migErr != nil {
		if isControlAuthDenied(migErr) || a.hasPendingLocalReadingMigration(manager.Config.URL, in.stateDir, identity, migrationProjects) {
			cancel()
			snap.noticeErr = migErr
			snap.documentErr = migErr
			return
		}
	}
	v2Res, v2Err := manager.projectMessageSummaryV2(summaryCtx, identity, p.ID)
	cancel()
	if v2Err == nil {
		snap.v2Summary = v2Res
		snap.v2SummaryOK = true
		if msg := v2Res.Errors["processing_notices"]; msg != "" {
			snap.noticeErr = fmt.Errorf("%s", msg)
		} else {
			snap.notices = v2Res.Notices
		}
		if msg := v2Res.Errors["documents"]; msg != "" {
			snap.documentErr = fmt.Errorf("%s", msg)
		} else {
			snap.documents = v2Res.pendingItems(p.Dataset)
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
	snap.noticeErr, snap.documentErr, snap.activityErr = v2Err, v2Err, v2Err
}

type projectQueueReconciled struct {
	AuthDenied              bool
	AuthDeniedError         string
	BacklogError            string
	UpdateBacklog           bool
	Backlog                 *backlogJob
	ProcessingIssues        []processingIssue
	AutomaticSave           bool
	BufferReviews           []bufferReview
	Checks                  []pendingCheckState
	ProcessingReadError     string
	UpdateNotices           bool
	Notices                 []processingNotice
	ActivityError           string
	Activity                []overviewEvent
	DocumentError           string
	Documents               []Item
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
			rec.ProcessingIssues = summary.ProcessingIssues
			rec.AutomaticSave = summary.AutomaticSave
			rec.BufferReviews = summary.BufferReviews
			rec.Checks = summary.Checks
		} else if !snap.setupOnly {
			rec.BacklogError = "后台采集状态暂时无法读取；任务不会因此重复启动"
		}
	}
	if snap.noticeErr != nil {
		rec.ProcessingReadError = "记忆提炼异常记录暂时无法读取"
	} else {
		rec.UpdateNotices = true
		rec.Notices = snap.notices
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
	rec.Documents = snap.documents
	rec.PendingDocumentsCount = max(snap.pendingDocumentsCount, len(snap.documents))
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
		q.Sessions = nil
		q.Documents = nil
		q.Loaded = false
		return
	}
	if snap.inbox != nil && meta.inboxRevision == q.inboxRevision {
		q.InboxPending, q.InboxFailed, q.LastError = snap.inbox.InboxPending, snap.inbox.InboxFailed, snap.inbox.LastError
		if q.InboxFailed == 0 && q.LastError == "" {
			q.InboxRetryError = ""
		}
	}
	if !q.localStateLoaded {
		if !q.KnowledgeRead.Initialized && snap.localKnowledgeRead.Initialized {
			q.KnowledgeRead = snap.localKnowledgeRead
		}
		q.localStateLoaded = true
	}
	q.applyNoticeAcknowledgementRead(snap.ackPath, meta.ackEpoch, snap.noticeAckSeq, snap.noticeAcks, snap.noticeAckErr)
	if a.isSetupOnlyRuntime() {
		q.BacklogError = ""
	} else if snap.v2SummaryOK {
		q.BacklogError = rec.BacklogError
	}
	if snap.v2SummaryOK {
		q.applyAutoCapture(snap.v2Summary.AutoCapture, snap.v2Summary.AutoCaptureVersion)
		if rec.UpdateBacklog {
			q.Backlog = rec.Backlog
			q.ProcessingIssues = rec.ProcessingIssues
			q.BacklogLoaded = true
			q.AutomaticSave = rec.AutomaticSave
			if rec.BufferReviews != nil {
				q.BufferReviews = rec.BufferReviews
			}
			if rec.Checks != nil {
				a.applyPendingChecks(q, rec.Checks)
			}
		}
	}
	q.ProcessingReadError = rec.ProcessingReadError
	if rec.UpdateNotices {
		q.ProcessingNotices = rec.Notices
	}
	q.Loaded = true
	q.ActivityError = rec.ActivityError
	if rec.ActivityError == "" {
		q.Activity = rec.Activity
	}
	q.DocumentError = rec.DocumentError
	prevCatalogVersion := q.KnowledgeCatalogVersion
	if rec.DocumentError == "" {
		q.Documents = rec.Documents
		q.PendingDocumentsCount = rec.PendingDocumentsCount
		if snap.v2SummaryOK {
			q.KnowledgeReadError = rec.KnowledgeReadError
			if rec.KnowledgeReadError == "" &&
				applyServerUnreadSnapshot(q, rec.ServerUnreadCount, rec.ServerUnreadStateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset)) {
				q.UnreadHasMore = rec.UnreadHasMore
				q.UnreadKnowledge = rec.UnreadKnowledge
			}
			if rec.KnowledgeCatalogVersion != "" {
				q.KnowledgeCatalogVersion = rec.KnowledgeCatalogVersion
			}

		}
		q.Offset = min(q.Offset, max(0, len(rec.Documents)-1))
	}
	q.StateVersion++
	if prevCatalogVersion != "" && q.KnowledgeCatalogVersion != "" && prevCatalogVersion != q.KnowledgeCatalogVersion {
		a.handleKnowledgeCatalogVersionChanged(p.ID, p.Dataset, q.KnowledgeCatalogVersion)
	}
	q.observeNotificationFailures(time.Now())
	recovered := reconcileRecoveredNoticeKeys(p, q, snap.v2SummaryOK, snap.summaryBacklogErr)
	a.clearRecoveredNoticeAcknowledgements(q, p, recovered)
}

func reconcileRecoveredNoticeKeys(p AccessProject, q *projectQueueState, v2SummaryOK bool, summaryBacklogErr string) []string {
	recovered := []string{}
	for index, row := range q.Sessions {
		if !row.View.Loaded || row.View.ReadError != "" {
			continue
		}
		key := noticeSessionKey(row, index)
		recovered = append(recovered, key+"/read")
		s := row.View.Status
		if s.Unknown == 0 {
			recovered = append(recovered, key+"/unknown", key+"/retry")
		}
		if s.Pending == 0 {
			recovered = append(recovered, key+"/pending")
		}
	}
	for key, message := range map[string]string{"knowledge": q.KnowledgeReadError, "activity": q.ActivityError, "documents": q.DocumentError, "processing-read": q.ProcessingReadError} {
		if message == "" {
			recovered = append(recovered, key)
		}
	}
	if v2SummaryOK && summaryBacklogErr == "" {
		if q.Backlog == nil || q.Backlog.ProcessingFailed == 0 {
			recovered = append(recovered, "processing-failed")
		}
	}
	if !p.policySyncUnconfirmed() {
		recovered = append(recovered, "policy-sync")
	}
	return recovered
}

func newInboxClient(control ProjectControlClient, identity string) (*api.Client, error) {
	return api.NewClient(api.Config{
		BaseURL: control.Config.URL, ManagementTokenFile: control.Config.ManagementTokenFile,
		ReadingIdentity: identity, HTTPClient: control.HTTP,
	})
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
		client, err := newInboxClient(control, identity)
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

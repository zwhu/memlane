package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

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
	Archives                       []pendingArchiveRow
	ArchiveError                   string
	ArchiveHistoryOpen             bool
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

func (c Client) pendingProjectDocuments(ctx context.Context, p Project) ([]Item, error) {
	items, _, err := c.pendingProjectCatalog(ctx, p)
	return items, err
}
func (c Client) pendingProjectCatalog(ctx context.Context, p Project) ([]Item, []Document, error) {
	var status struct {
		Items []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Completed *bool  `json:"completed"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/api/v1/datasets/"+url.PathEscape(p.Dataset)+"/processing-status?pipeline=cognify_pipeline", &status); err != nil {
		return nil, nil, err
	}
	if status.Items == nil {
		return nil, nil, fmt.Errorf("提炼状态缺少文档清单")
	}
	docs, err := c.overviewDocuments(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	metadata := map[string]Document{}
	for _, d := range docs {
		metadata[d.ID] = d
	}
	result := []Item{}
	for _, row := range status.Items {
		if row.Completed == nil {
			return nil, nil, fmt.Errorf("文档提炼状态不完整")
		}
		if *row.Completed {
			continue
		}
		d, ok := metadata[row.ID]
		if !ok {
			return nil, nil, fmt.Errorf("文档列表已变化，请重新读取")
		}
		if d.knowledge() || derivedMaterial(d) || metadataString(d, "memlane_kind") == "user_profile" {
			continue
		}
		copy := d
		result = append(result, Item{ID: d.ID, Dataset: p.Dataset, Kind: "文档", Title: d.title(), Date: d.date(), Document: &copy})
	}
	return result, docs, nil
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
		q.Archives, q.ArchiveError = a.readPendingArchives(p)
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
	project                AccessProject
	client                 Client
	control                *ProjectControlConfig
	sessions               []queuedSession
	slots                  chan struct{}
	python                 string
	operator               PendingOperatorConfig
	stateDir               string
	migrationProject       AccessProject
	needLocalState         bool
	knowledgePath          string
	knowledgePathErr       error
	knowledgeWriter        *knowledgeReadWriter
	prevKnowledgeStartedAt time.Time
}

type projectQueueSessionResult struct {
	Status PendingMaterials
	Error  error
}

type projectQueueSnapshot struct {
	setupOnly             bool
	sessions              []projectQueueSessionResult
	authDenied            bool
	v2Summary             v2MessageSummaryResponse
	v2SummaryOK           bool
	summaryBacklogErr     string
	v2ReadStateErr        string
	documents             []Item
	pendingDocumentsCount int
	knowledgeVersion      string
	computedCatalog       *computedKnowledgeCatalog
	documentErr           error
	activity              []activityRow
	activityErr           error
	notices               []processingNotice
	noticeErr             error
	localArchives         []pendingArchiveRow
	localArchiveErr       string
	localKnowledgeRead    knowledgeReadState
	ackPath               string
	noticeAckSeq          uint64
	noticeAcks            map[string]noticeAcknowledgement
	noticeAckErr          string
}

type projectQueueApplyMeta struct {
	request      uint64
	authEpoch    uint64
	ackEpoch     uint64
	baseIdentity string
	sessions     []queuedSession
}

func (a *App) loadProjectQueue(q *projectQueueState, p AccessProject) {
	if q.Loading || a.access.Busy {
		return
	}
	for _, row := range q.Sessions {
		if row.View.Busy {
			return
		}
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
	sessions := a.projectQueueSessions(p)
	var control *ProjectControlConfig
	if ctrl, ok := a.accessClient(); ok {
		cfg := ctrl.Config
		control = &cfg
	}
	stateDir := a.readingStateDir()
	knowledgePath, knowledgePathErr := a.knowledgeReadPath(p)
	knowledgeWriter := a.ensureKnowledgeWriter()
	// Queue loads only depend on migration for their own project.
	migrationProject := p
	for _, ap := range a.access.Directory.Projects {
		if ap.ID == p.ID && ap.Dataset == p.Dataset {
			migrationProject = ap
			break
		}
	}
	if a.access.QueueReadSlots == nil {
		a.access.QueueReadSlots = make(chan struct{}, 4)
	}
	in := projectQueueFetchInput{
		project: p, client: a.Client, control: control, sessions: sessions, slots: a.access.QueueReadSlots,
		python: a.Config.Python, operator: pendingOperatorConfig(a.Config),
		stateDir: stateDir, migrationProject: migrationProject, needLocalState: needLocalState,
		knowledgePath: knowledgePath, knowledgePathErr: knowledgePathErr,
		knowledgeWriter: knowledgeWriter, prevKnowledgeStartedAt: q.KnowledgeRead.StartedAt,
	}
	meta := projectQueueApplyMeta{request, authEpoch, ackEpoch, baseIdentity, sessions}
	update, setupOnly := a.Update, a.isSetupOnlyRuntime()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		snap := a.fetchProjectQueueSnapshot(ctx, in)
		snap.setupOnly = setupOnly
		rec := reconcileProjectQueueState(p, snap)
		update(func() { a.applyProjectQueueResult(q, p, meta, snap, rec) })
	}()
}

func (a *App) projectQueueSessions(p AccessProject) []queuedSession {
	if a.access.Buffers == nil {
		a.access.Buffers = map[string]*pendingBufferView{}
	}
	sessions := []queuedSession{}
	for _, binding := range a.access.Directory.Bindings {
		if binding.ProjectID != p.ID {
			continue
		}
		for _, agent := range a.access.Directory.Agents {
			if agent.ID != binding.AgentID || agent.Installation == nil {
				continue
			}
			key := agent.ID + "/" + binding.HostSessionKey
			view := a.access.Buffers[key]
			if view == nil {
				view = &pendingBufferView{}
				a.access.Buffers[key] = view
			}
			sessions = append(sessions, queuedSession{agent, binding, view})
		}
	}
	return sessions
}

func (a *App) fetchProjectQueueSnapshot(ctx context.Context, in projectQueueFetchInput) projectQueueSnapshot {
	var snap projectQueueSnapshot
	var catalog []Document
	var workers sync.WaitGroup
	workers.Go(func() {
		catalog = a.fetchProjectQueueSummary(ctx, in, &snap)
	})
	workers.Go(func() {
		snap.sessions = fetchProjectQueueSessions(ctx, in)
	})
	workers.Go(func() {
		p := in.project
		snap.ackPath, _ = noticeAcknowledgementPath(in.stateDir, in.client, p)
		snap.noticeAcks, snap.noticeAckErr, snap.noticeAckSeq = readNoticeAcknowledgementsWithSeq(snap.ackPath)
		if in.needLocalState {
			snap.localArchives, snap.localArchiveErr = readPendingArchives(in.stateDir, in.client, p)
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
	if !snap.v2SummaryOK && snap.documentErr == nil && !snap.authDenied && catalog != nil && in.knowledgePathErr == nil {
		in.knowledgeWriter.waitKnowledgeReadFlush()
		result := computeKnowledgeCatalogOffUI(in.knowledgeWriter, in.knowledgePath, in.project, catalog, in.prevKnowledgeStartedAt)
		snap.computedCatalog = &result
	}
	return snap
}

func (a *App) fetchProjectQueueSummary(ctx context.Context, in projectQueueFetchInput, snap *projectQueueSnapshot) []Document {
	client, p := in.client, in.project
	identity, _ := readingIdentity(Client{Config: client.Config})
	migrationProjects := []AccessProject{in.migrationProject}
	if in.control != nil {
		summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		manager := ProjectControlClient{Config: *in.control, HTTP: client.HTTP}
		if err := manager.requireReadingContract(summaryCtx); err != nil {
			snap.noticeErr, snap.documentErr, snap.activityErr = err, err, err
			return nil
		}
		if migErr := a.migrateReadingStateV2Snapshot(summaryCtx, manager, identity, in.stateDir, migrationProjects); migErr != nil {
			if isControlAuthDenied(migErr) || a.hasPendingLocalReadingMigration(manager.Config.URL, in.stateDir, identity, migrationProjects) {
				cancel()
				snap.noticeErr = migErr
				snap.documentErr = migErr
				return nil
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
			return nil
		}
		snap.noticeErr, snap.documentErr, snap.activityErr = v2Err, v2Err, v2Err
		return nil
	}
	var catalog []Document
	var fallback sync.WaitGroup
	fallback.Add(1)
	go func() {
		defer fallback.Done()
		activityCtx, activityCancel := context.WithTimeout(ctx, 5*time.Second)
		defer activityCancel()
		snap.activityErr = client.get(activityCtx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.Dataset)+"&limit=100", &snap.activity)
	}()
	fallback.Add(1)
	go func() {
		defer fallback.Done()
		snap.documents, catalog, snap.documentErr = client.pendingProjectCatalog(ctx, Project{ID: p.ID, Dataset: p.Dataset})
	}()
	fallback.Wait()
	return catalog
}

func fetchProjectQueueSessions(ctx context.Context, in projectQueueFetchInput) []projectQueueSessionResult {
	// Bounded worker pool (<=4 workers) avoids spawning thousands of goroutines
	// when a project has hundreds or thousands of historical session bindings.
	if len(in.sessions) == 0 {
		return nil
	}
	results := make([]projectQueueSessionResult, len(in.sessions))
	var workers sync.WaitGroup

	poolSize := min(4, len(in.sessions))
	jobs := make(chan int, len(in.sessions))
	for n := range in.sessions {
		jobs <- n
	}
	close(jobs)
	for wIdx := 0; wIdx < poolSize; wIdx++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := range jobs {
				row := in.sessions[n]
				select {
				case in.slots <- struct{}{}:
				case <-ctx.Done():
					results[n].Error = ctx.Err()
					continue
				}
				results[n].Status, results[n].Error = runPendingMaterials(ctx, in.python, *row.Agent.Installation, row.Binding, in.project.Dataset, "show", "", in.operator)
				if results[n].Error != nil && results[n].Status.Error == "pending_materials_changed_review_again" && ctx.Err() == nil {
					results[n].Status, results[n].Error = runPendingMaterials(ctx, in.python, *row.Agent.Installation, row.Binding, in.project.Dataset, "show", "", in.operator)
				}
				<-in.slots
				status := results[n].Status
				if results[n].Error == nil && (status.Signature == "" || status.Pending < 0 || status.Unknown < 0 || status.Unknown > status.Pending) {
					results[n].Error = fmt.Errorf("会话采集状态不完整")
				}
			}
		}()
	}
	workers.Wait()
	return results
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
	ComputedCatalog         *computedKnowledgeCatalog
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
	rec.ComputedCatalog = snap.computedCatalog
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
		q.Sessions = nil
		q.Documents = nil
		q.Loaded = false
		return
	}
	if !q.localStateLoaded {
		if len(q.Archives) == 0 && q.ArchiveError == "" {
			q.Archives, q.ArchiveError = snap.localArchives, snap.localArchiveErr
		}
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
	wasLoaded := q.Loaded
	q.Loaded = true
	q.ActivityError = rec.ActivityError
	if rec.ActivityError == "" {
		q.Activity = rec.Activity
	}
	q.Sessions = meta.sessions
	for n, row := range meta.sessions {
		result := snap.sessions[n]
		if result.Error != nil {
			row.View.ReadCode = result.Status.Error
			row.View.Details = nil
			row.View.DetailsLoaded = false
			row.View.ReadError = "采集状态暂时无法读取"
			if result.Status.Error == "official_session_unconfirmed" {
				row.View.ReadError = "尚未确认这次会话的采集状态；重新读取不会补采历史对话"
			}
			row.View.Loaded = false
		} else {
			if wasLoaded && !row.View.Observed {
				row.View.Added = result.Status.Pending
			}
			row.View.observe(result.Status)
		}
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
		} else if rec.ComputedCatalog != nil {
			applyComputedKnowledgeCatalog(q, *rec.ComputedCatalog)
			if q.KnowledgeReadError == "" {
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

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
	SaveDetailsOpen                bool
	BacklogLoading, BacklogBusy    bool
	BacklogLoaded                  bool
	BacklogNext                    time.Time
	BacklogError, BacklogOperation string
	localStateLoaded               bool
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
	var control *ProjectControlConfig
	if ctrl, ok := a.accessClient(); ok {
		cfg := ctrl.Config
		control = &cfg
	}
	client, python := a.Client, a.Config.Python
	operator := pendingOperatorConfig(a.Config)
	prevKnowledgeStartedAt := q.KnowledgeRead.StartedAt
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
	migrationProjects := []AccessProject{migrationProject}
	if a.access.QueueReadSlots == nil {
		a.access.QueueReadSlots = make(chan struct{}, 4)
	}
	slots := a.access.QueueReadSlots
	go func() {
		identity, _ := readingIdentity(Client{Config: client.Config})
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		type result struct {
			Status PendingMaterials
			Error  error
		}
		results := make([]result, len(sessions))
		var documents []Item
		var pendingDocumentsCount int
		var knowledgeCatalog []Document
		var computedCatalog *computedKnowledgeCatalog
		var knowledgeVersion string
		var documentErr error
		var activity []activityRow
		var activityErr error
		var notices []processingNotice
		var noticeErr error
		var summaryBacklogErr string
		var v2Summary v2MessageSummaryResponse
		var v2SummaryOK bool
		var v2ReadStateErr string
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			if control != nil {
				summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				manager := ProjectControlClient{Config: *control, HTTP: client.HTTP}
				if err := manager.requireReadingContract(summaryCtx); err != nil {
					noticeErr, documentErr, activityErr = err, err, err
					return
				}
				if migErr := a.migrateReadingStateV2Snapshot(summaryCtx, manager, identity, stateDir, migrationProjects); migErr != nil {
					if isControlAuthDenied(migErr) || a.hasPendingLocalReadingMigration(manager.Config.URL, stateDir, identity, migrationProjects) {
						cancel()
						noticeErr = migErr
						documentErr = migErr
						return
					}
				}
				v2Res, v2Err := manager.projectMessageSummaryV2(summaryCtx, identity, p.ID)
				cancel()
				if v2Err == nil {
					v2Summary = v2Res
					v2SummaryOK = true
					if msg := v2Res.Errors["processing_notices"]; msg != "" {
						noticeErr = fmt.Errorf("%s", msg)
					} else {
						notices = v2Res.Notices
					}
					if msg := v2Res.Errors["documents"]; msg != "" {
						documentErr = fmt.Errorf("%s", msg)
					} else {
						documents = v2Res.pendingItems(p.Dataset)
						pendingDocumentsCount = max(v2Res.PendingDocumentsCount, len(documents))
						knowledgeVersion = v2Res.KnowledgeCatalogVersion
					}
					if msg := v2Res.Errors["activity"]; msg != "" {
						activityErr = fmt.Errorf("%s", msg)
					} else {
						activity = v2Res.Activity
					}
					summaryBacklogErr = v2Res.Errors["backlog"]
					v2ReadStateErr = v2Res.Errors["reading_state"]
					return
				}
				noticeErr, documentErr, activityErr = v2Err, v2Err, v2Err
				return
			}
			var fallback sync.WaitGroup
			fallback.Add(1)
			go func() {
				defer fallback.Done()
				activityCtx, activityCancel := context.WithTimeout(ctx, 5*time.Second)
				defer activityCancel()
				activityErr = client.get(activityCtx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.Dataset)+"&limit=100", &activity)
			}()
			fallback.Add(1)
			go func() {
				defer fallback.Done()
				documents, knowledgeCatalog, documentErr = client.pendingProjectCatalog(ctx, Project{ID: p.ID, Dataset: p.Dataset})
			}()
			fallback.Wait()
		}()
		// Bounded worker pool (<=4 workers) avoids spawning thousands of goroutines
		// when a project has hundreds or thousands of historical session bindings.
		if len(sessions) > 0 {
			poolSize := min(4, len(sessions))
			jobs := make(chan int, len(sessions))
			for n := range sessions {
				jobs <- n
			}
			close(jobs)
			for wIdx := 0; wIdx < poolSize; wIdx++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for n := range jobs {
						row := sessions[n]
						select {
						case slots <- struct{}{}:
						case <-ctx.Done():
							results[n].Error = ctx.Err()
							continue
						}
						results[n].Status, results[n].Error = runPendingMaterials(ctx, python, *row.Agent.Installation, row.Binding, p.Dataset, "show", "", operator)
						if results[n].Error != nil && results[n].Status.Error == "pending_materials_changed_review_again" && ctx.Err() == nil {
							results[n].Status, results[n].Error = runPendingMaterials(ctx, python, *row.Agent.Installation, row.Binding, p.Dataset, "show", "", operator)
						}
						<-slots
						status := results[n].Status
						if results[n].Error == nil && (status.Signature == "" || status.Pending < 0 || status.Unknown < 0 || status.Unknown > status.Pending) {
							results[n].Error = fmt.Errorf("会话采集状态不完整")
						}
					}
				}()
			}
		}
		var localArchives []pendingArchiveRow
		var localArchiveErr string
		var localKnowledgeRead knowledgeReadState
		var noticeAcks map[string]noticeAcknowledgement
		var ackPath, noticeAckErr string
		var noticeAckSeq uint64
		workers.Add(1)
		go func() {
			defer workers.Done()
			ackPath, _ = a.noticeAcknowledgementPath(p)
			noticeAcks, noticeAckErr, noticeAckSeq = readNoticeAcknowledgementsWithSeq(ackPath)
			if needLocalState {
				localArchives, localArchiveErr = a.readPendingArchives(p)
				if path, err := a.knowledgeReadPath(p); err == nil {
					if s, readErr := readKnowledgeState(path); readErr == nil && s.Initialized {
						localKnowledgeRead = s
					}
				}
			}
		}()
		workers.Wait()
		authDenied := readingAuthDenied(documentErr) || readingAuthDenied(noticeErr) || readingAuthDenied(activityErr) ||
			isControlErrorCode(documentErr, 503, "reading_contract_mismatch")
		// Execute legacy knowledge-read catalog reconciliation off the UI thread so
		// a.Update never blocks on waitKnowledgeReadFlush or disk I/O.
		if !v2SummaryOK && documentErr == nil && !authDenied && knowledgeCatalog != nil && knowledgePathErr == nil {
			a.waitKnowledgeReadFlush()
			res := computeKnowledgeCatalogOffUI(knowledgeWriter, knowledgePath, p, knowledgeCatalog, prevKnowledgeStartedAt)
			computedCatalog = &res
		}
		a.Update(func() {
			if request != q.Request {
				return
			}
			q.Loading = false
			if authDenied {
				q.DocumentError = controlUserError(documentErr)
				q.Activity = nil
				a.InvalidateReading(p.Dataset)
				return
			}
			if authEpoch != a.authEpoch {
				return
			}
			_, current, _ := readingIdentityPair(Client{Config: a.Client.Config})
			if baseIdentity != current {
				q.Sessions = nil
				q.Documents = nil
				q.Loaded = false
				return
			}
			if !q.localStateLoaded {
				if len(q.Archives) == 0 && q.ArchiveError == "" {
					q.Archives, q.ArchiveError = localArchives, localArchiveErr
				}
				if !q.KnowledgeRead.Initialized && localKnowledgeRead.Initialized {
					q.KnowledgeRead = localKnowledgeRead
				}
				q.localStateLoaded = true
			}
			q.applyNoticeAcknowledgementRead(ackPath, ackEpoch, noticeAckSeq, noticeAcks, noticeAckErr)
			if v2SummaryOK {
				if summaryBacklogErr != "" {
					q.BacklogError = "后台采集状态暂时无法读取；任务不会因此重复启动"
				} else {
					q.BacklogError = ""
					q.Backlog = v2Summary.Backlog
					q.ProcessingIssues = v2Summary.ProcessingIssues
					q.BacklogLoaded = true
					q.AutomaticSave = v2Summary.AutomaticSave
					if v2Summary.BufferReviews != nil {
						q.BufferReviews = v2Summary.BufferReviews
					}
					if v2Summary.Checks != nil {
						a.applyPendingChecks(q, v2Summary.Checks)
					}
				}
			}
			q.ProcessingReadError = ""
			if noticeErr != nil {
				q.ProcessingReadError = "记忆提炼异常记录暂时无法读取"
			} else {
				q.ProcessingNotices = notices
			}
			wasLoaded := q.Loaded
			q.Loaded = true
			q.ActivityError = ""
			if activityErr != nil {
				q.ActivityError = "近期提炼记录暂时无法读取"
			} else {
				projectActivity := make([]activityRow, 0, len(activity))
				for _, row := range activity {
					if row.Dataset == p.Dataset {
						projectActivity = append(projectActivity, row)
					}
				}
				q.Activity = nil
				for _, event := range aggregateActivity(projectActivity, []Project{{ID: p.ID, Name: p.Name, Dataset: p.Dataset}}) {
					if event.Title == "提炼记忆" || event.Title == "处理记忆" || (event.Title == "整理知识" || event.Title == "整理记忆") {
						q.Activity = append(q.Activity, event)
					}
				}
			}
			q.Sessions = sessions
			for n, row := range sessions {
				if results[n].Error != nil {
					row.View.ReadCode = results[n].Status.Error
					row.View.Details = nil
					row.View.DetailsLoaded = false
					row.View.ReadError = "采集状态暂时无法读取"
					if results[n].Status.Error == "official_session_unconfirmed" {
						row.View.ReadError = "尚未确认这次会话的采集状态；重新读取不会补采历史对话"
					}
					row.View.Loaded = false
				} else {
					if wasLoaded && !row.View.Observed {
						row.View.Added = results[n].Status.Pending
					}
					row.View.observe(results[n].Status)
				}
			}
			q.DocumentError = ""
			prevCatalogVersion := q.KnowledgeCatalogVersion
			if documentErr != nil {
				q.DocumentError = "提炼状态暂时无法读取"
			} else {
				q.Documents = documents
				q.PendingDocumentsCount = max(pendingDocumentsCount, len(documents))
				if v2SummaryOK {
					if v2ReadStateErr != "" {
						q.KnowledgeReadError = "未读状态暂时无法读取"
					} else {
						q.KnowledgeReadError = ""
						if applyServerUnreadSnapshot(q, v2Summary.UnreadCount, v2Summary.UnreadStateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset)) {
							q.UnreadHasMore = v2Summary.UnreadHasMore || v2Summary.UnreadCount > len(v2Summary.UnreadPreview)
							q.UnreadKnowledge = v2Summary.unreadPreviewItems(p.Dataset)
						}
					}
					if knowledgeVersion != "" {
						q.KnowledgeCatalogVersion = knowledgeVersion
					}
				} else if computedCatalog != nil {
					applyComputedKnowledgeCatalog(q, *computedCatalog)
					if q.KnowledgeReadError == "" {
						q.KnowledgeCatalogVersion = knowledgeVersion
					}
				}
				q.Offset = min(q.Offset, max(0, len(documents)-1))
			}
			q.StateVersion++
			if prevCatalogVersion != "" && q.KnowledgeCatalogVersion != "" && prevCatalogVersion != q.KnowledgeCatalogVersion {
				a.handleKnowledgeCatalogVersionChanged(p.ID, p.Dataset, q.KnowledgeCatalogVersion)
			}
			q.observeNotificationFailures(time.Now())
			recovered := []string{}
			for index, row := range sessions {
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
			a.clearRecoveredNoticeAcknowledgements(q, p, recovered)
		})
	}()
}

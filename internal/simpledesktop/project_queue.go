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
	Status      int    `json:"http_status"`
	Code        string `json:"code"`
	Backend     string `json:"backend"`
}

type projectQueueState struct {
	BufferReviews                  []bufferReview
	NoticeAcknowledgements         map[string]noticeAcknowledgement
	NoticeAcknowledgementError     string
	NoticeHistoryOpen              bool
	ProcessingNotices              []processingNotice
	ProcessingReadError            string
	NotificationFailures           map[string]projectFailure
	KnowledgeRead                  knowledgeReadState
	UnreadKnowledge                []Item
	KnowledgeReadError             string
	Archives                       []pendingArchiveRow
	ArchiveError                   string
	ArchiveHistoryOpen             bool
	Scroll                         ui.ScrollState
	LocatedNoticeKey               string
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
		return nil, nil, fmt.Errorf("整理状态缺少文档清单")
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
			return nil, nil, fmt.Errorf("文档整理状态不完整")
		}
		if *row.Completed {
			continue
		}
		d, ok := metadata[row.ID]
		if !ok {
			return nil, nil, fmt.Errorf("文档列表已变化，请重新读取")
		}
		if d.knowledge() || derivedMaterial(d) {
			continue
		}
		copy := d
		result = append(result, Item{ID: d.ID, Dataset: p.Dataset, Kind: "文档", Title: d.title(), Date: d.date(), Document: &copy})
	}
	return result, docs, nil
}
func (a *App) projectQueueStateFor(p AccessProject) *projectQueueState {
	if a.access.Queues == nil {
		a.access.Queues = map[string]*projectQueueState{}
	}
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	key := identity + "/" + p.ID + "/" + p.Dataset
	q := a.access.Queues[key]
	if q == nil {
		q = &projectQueueState{}
		q.Archives, q.ArchiveError = a.readPendingArchives(p)
		q.NoticeAcknowledgements, q.NoticeAcknowledgementError = a.readNoticeAcknowledgements(p)
		a.access.Queues[key] = q
	}
	return q
}
func (a *App) projectQueue(p AccessProject) *projectQueueState {
	q := a.projectQueueStateFor(p)
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
	request := q.Request
	q.Next = time.Now().Add(30 * time.Second)
	if q.Backlog.active() {
		q.Next = time.Now().Add(5 * time.Second)
	}
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
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
	control := a.Config.ProjectControl
	client, python := a.Client, a.Config.Python
	operator := pendingOperatorConfig(a.Config)
	if a.access.QueueReadSlots == nil {
		a.access.QueueReadSlots = make(chan struct{}, 4)
	}
	slots := a.access.QueueReadSlots
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		type result struct {
			Status PendingMaterials
			Error  error
		}
		results := make([]result, len(sessions))
		var documents []Item
		var knowledgeCatalog []Document
		var documentErr error
		var activity []activityRow
		var activityErr error
		var notices struct {
			Items []processingNotice `json:"items"`
		}
		var noticeErr error
		var workers sync.WaitGroup
		if control != nil {
			workers.Add(1)
			go func() {
				defer workers.Done()
				noticeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				manager := ProjectControlClient{Config: *control, HTTP: client.HTTP}
				noticeErr = manager.request(noticeCtx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/processing-notices", nil, &notices)
			}()
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			activityCtx, activityCancel := context.WithTimeout(ctx, 5*time.Second)
			defer activityCancel()
			activityErr = client.get(activityCtx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.Dataset)+"&limit=100", &activity)
		}()
		workers.Add(1)
		go func() {
			defer workers.Done()
			documents, knowledgeCatalog, documentErr = client.pendingProjectCatalog(ctx, Project{ID: p.ID, Dataset: p.Dataset})
		}()
		for n, row := range sessions {
			workers.Add(1)
			go func(n int, row queuedSession) {
				defer workers.Done()
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					results[n].Error = ctx.Err()
					return
				}
				defer func() { <-slots }()
				results[n].Status, results[n].Error = runPendingMaterials(ctx, python, *row.Agent.Installation, row.Binding, p.Dataset, "show", "", operator)
				status := results[n].Status
				if results[n].Error == nil && (status.Signature == "" || status.Pending < 0 || status.Unknown < 0 || status.Unknown > status.Pending) {
					results[n].Error = fmt.Errorf("会话保存状态不完整")
				}
			}(n, row)
		}
		workers.Wait()
		a.Update(func() {
			if request != q.Request {
				return
			}
			q.Loading = false
			current, _ := readingIdentity(Client{Config: a.Client.Config})
			if identity != current {
				q.Sessions = nil
				q.Documents = nil
				q.Loaded = false
				return
			}
			q.NoticeAcknowledgements, q.NoticeAcknowledgementError = a.readNoticeAcknowledgements(p)
			q.ProcessingReadError = ""
			if noticeErr != nil {
				q.ProcessingReadError = "记忆整理异常记录暂时无法读取"
			} else {
				q.ProcessingNotices = notices.Items
			}
			wasLoaded := q.Loaded
			q.Loaded = true
			q.ActivityError = ""
			if activityErr != nil {
				q.ActivityError = "近期处理记录暂时无法读取"
			} else {
				projectActivity := make([]activityRow, 0, len(activity))
				for _, row := range activity {
					if row.Dataset == p.Dataset {
						projectActivity = append(projectActivity, row)
					}
				}
				q.Activity = nil
				for _, event := range aggregateActivity(projectActivity, []Project{{ID: p.ID, Name: p.Name, Dataset: p.Dataset}}) {
					if event.Title == "处理记忆" || (event.Title == "整理知识" || event.Title == "整理记忆") {
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
					row.View.ReadError = "保存状态暂时无法读取"
					if results[n].Status.Error == "official_session_unconfirmed" {
						row.View.ReadError = "尚未确认这次会话的保存状态；重新读取不会补采历史对话"
					}
					row.View.Message = row.View.ReadError
					row.View.Loaded = false
				} else {
					if wasLoaded && !row.View.Observed {
						row.View.Added = results[n].Status.Pending
					}
					row.View.observe(results[n].Status)
				}
			}
			q.DocumentError = ""
			if documentErr != nil {
				q.DocumentError = "整理状态暂时无法读取"
				if readingUnavailable(documentErr) {
					q.Documents = nil
				}
			} else {
				q.Documents = documents
				a.observeKnowledgeCatalog(p, q, knowledgeCatalog)
				q.Offset = min(q.Offset, max(0, len(documents)-1))
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
			a.clearRecoveredNoticeAcknowledgements(q, p, recovered)
		})
	}()
}

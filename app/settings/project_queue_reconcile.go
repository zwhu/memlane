package settings

import (
	"context"
	"errors"
	"time"

	"github.com/zwhu/memlane/app/api"
)

type projectQueueReconciled struct {
	AuthDenied              bool
	AuthDeniedError         string
	BacklogError            string
	UpdateBacklog           bool
	Backlog                 *BacklogJob
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
		rec.AuthDeniedError = ControlUserError(snap.documentErr)
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

func (a *Controller) applyProjectQueueResult(q *ProjectQueueState, p AccessProject, meta projectQueueApplyMeta, snap projectQueueSnapshot, rec projectQueueReconciled) {
	if meta.request != q.Request {
		return
	}
	q.Loading = false
	if rec.AuthDenied {
		q.InboxPending, q.InboxFailed, q.LastError, q.InboxRetryError = 0, 0, "", ""
		q.DocumentError = rec.AuthDeniedError
		q.Activity = nil
		a.invalidateReading(p.Dataset)
		return
	}
	if meta.authEpoch != a.authEpoch() {
		return
	}
	_, current, _ := readingIdentityPair(Client{Config: a.client.Config})
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

func (a *Controller) RetryProjectInbox(q *ProjectQueueState, p AccessProject) {
	if a.isSetupOnlyRuntime() || q.InboxRetryBusy || a.access.Busy || !a.canDispatch() {
		return
	}
	control, ok := a.accessClient()
	if !ok {
		return
	}
	identity, baseIdentity, err := readingIdentityPair(a.client)
	if err != nil {
		q.InboxRetryError = "读取身份暂不可用"
		return
	}
	q.InboxRetryBusy = true
	q.InboxRetryError = ""
	authEpoch, update := a.authEpoch(), a.dispatch
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
			currentIdentity, currentBase, identityErr := readingIdentityPair(a.client)
			if !ok || current.Config != control.Config || identityErr != nil || currentBase != baseIdentity ||
				currentIdentity != identity || a.authEpoch() != authEpoch || a.isSetupOnlyRuntime() {
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
				a.LoadProjectQueue(q, p)
			}
		})
	}()
}

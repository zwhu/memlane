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
		if d.knowledge() || derivedMaterial(d) {
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

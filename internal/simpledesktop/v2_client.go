package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	v2HeaderReadingIdentity = "X-Memlane-Reading-Identity"

	v2FeatureCatalogIndex          = "catalog_index_v2"
	v2FeatureReadingState          = "reading_state_v2"
	v2FeatureLocalBodyFastpath     = "local_body_fastpath_v2"
	v2FeatureOverview              = "overview_v2"
	v2FeatureKnowledgeCursor       = "knowledge_cursor_v2"
	v2FeatureCommonKnowledgeCursor = "common_knowledge_cursor_v2"
	v2FeatureRawRecords            = "raw_records_v2"
	v2FeatureSearch                = "search_v2"
	v2FeatureMessageSummary        = "message_summary_v2"
	v2FeatureProvenanceBackfill    = "provenance_backfill_v2"

	maxActiveListItems = 300
)

type v2DocumentBodyResponse struct {
	ID          string  `json:"id"`
	DatasetID   string  `json:"dataset_id"`
	Body        *string `json:"body"`
	MIMEType    string  `json:"mime_type"`
	ContentHash string  `json:"content_hash"`
	Truncated   bool    `json:"truncated"`
	BytesTotal  int64   `json:"bytes_total"`
	Source      string  `json:"source"`
}

func (c ProjectControlClient) documentBodyV2(ctx context.Context, datasetID, docID string) (v2DocumentBodyResponse, error) {
	var out v2DocumentBodyResponse
	path := "/v2/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(docID) + "/body"
	// Allow up to 16 MiB JSON payload to cover worst-case JSON escaping of an 8 MiB UTF-8 body.
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 16*1024*1024); err != nil {
		return v2DocumentBodyResponse{}, err
	}
	return out, nil
}

type v2KnowledgeItem struct {
	DocID            string `json:"doc_id"`
	DatasetID        string `json:"dataset_id"`
	ProjectID        string `json:"project_id"`
	Title            string `json:"title"`
	DocName          string `json:"doc_name"`
	DocLabel         string `json:"doc_label"`
	EffectiveDate    string `json:"effective_date"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ContentHash      string `json:"content_hash"`
	Kind             string `json:"kind"`
	Category         string `json:"category"`
	CommonCategory   string `json:"common_category"`
	LessonType       string `json:"lesson_type"`
	StateKey         string `json:"state_key"`
	SessionID        string `json:"session_id"`
	RecallCount      int64  `json:"recall_count"`
	IsUnread         bool   `json:"is_unread"`
	PreviewText      string `json:"preview_text"`
	PreviewTruncated bool   `json:"preview_truncated"`
	MatchField       string `json:"match_field,omitempty"`
}

var categoryLabelToLessonType = map[string]string{
	"项目状态":    "state",
	"踩坑经验":    "pitfall",
	"项目约定":    "convention",
	"代码位置与关联": "location",
	"代码变更":    "change",
	"约束条件":    "constraint",
	"其他":      "other",
}

func (row v2KnowledgeItem) toItem(recallStartedAt string) Item {
	kind := "知识"
	lowerKind := strings.ToLower(strings.TrimSpace(row.Kind))
	if lowerKind == "raw_doc" || lowerKind == "document" || lowerKind == "raw" || lowerKind == "raw_document" || row.Kind == "文档" {
		kind = "文档"
	}

	meta := map[string]any{}
	if kind == "知识" {
		if row.CommonCategory != "" {
			meta["memlane_kind"] = "common_knowledge"
			meta["category"] = row.CommonCategory
			meta["node_set"] = []any{"memlane_common"}
		} else if lowerKind == "direct_memory" || lowerKind == "user_context" || lowerKind == "memory" || row.Category == "直接保存" {
			meta["node_set"] = []any{"user_context"}
		} else {
			nodeSets := []any{"session_learnings"}
			if row.SessionID != "" {
				nodeSets = append(nodeSets, "session_learnings:"+row.SessionID)
				meta["session_id"] = row.SessionID
			}
			meta["node_set"] = nodeSets
			lessonType := strings.TrimSpace(row.LessonType)
			if lessonType == "" && row.Category != "" {
				lessonType = categoryLabelToLessonType[strings.TrimSpace(row.Category)]
			}
			if lessonType != "" {
				meta["lesson_type"] = lessonType
			}
			if row.StateKey != "" {
				meta["state_key"] = row.StateKey
			}
		}
	}

	var labelPtr *string
	if row.DocLabel != "" {
		l := row.DocLabel
		labelPtr = &l
	}
	doc := Document{
		FirstSourceDate: row.EffectiveDate,
		ID:              row.DocID,
		Dataset:         row.DatasetID,
		Name:            row.DocName,
		Label:           labelPtr,
		Metadata:        meta,
		Created:         row.CreatedAt,
		Updated:         row.UpdatedAt,
		ContentHash:     row.ContentHash,
	}

	date := row.EffectiveDate
	if date == "" {
		date = doc.date()
	}
	title := strings.TrimSpace(row.Title)
	if title == "" {
		if preview := strings.TrimSpace(row.PreviewText); preview != "" {
			title = firstReadingSentenceLimit(preview, 240)
		}
	}
	if title == "" {
		title = doc.title()
	}

	readingState := ""
	if row.PreviewText != "" || title != "" {
		readingState = "ready"
	}

	var stats *KnowledgeRecallStats
	if kind == "知识" {
		stats = &KnowledgeRecallStats{
			Count:     row.RecallCount,
			StartedAt: recallStartedAt,
		}
	}

	// Strictly decouple Preview from Body / BodyLoaded:
	// list cards display Preview directly while Body remains empty and BodyLoaded=false
	// until the full document is fetched via /v2/datasets/{ds}/documents/{doc}/body.
	return Item{
		ID:                row.DocID,
		Dataset:           row.DatasetID,
		Kind:              kind,
		Title:             title,
		Date:              date,
		Preview:           row.PreviewText,
		PreviewTruncated:  row.PreviewTruncated,
		Body:              "",
		BodyLoaded:        false,
		Source:            row.SessionID,
		CommonCategory:    row.CommonCategory,
		Document:          &doc,
		RecallStats:       stats,
		ReadingState:      readingState,
		ServerUnread:      row.IsUnread,
		ServerUnreadKnown: true,
	}
}

type v2OverviewDayBucket struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type v2OverviewProjectSummary struct {
	ProjectID      string `json:"project_id"`
	DatasetID      string `json:"dataset_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	RawDocCount    int    `json:"raw_doc_count"`
	SessionCount   int    `json:"session_count"`
	UnreadCount    int    `json:"unread_count"`
	CatalogVersion string `json:"catalog_version"`
}

type v2OverviewCommonSummary struct {
	DatasetID      string `json:"dataset_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	RawDocCount    int    `json:"raw_doc_count"`
	CatalogVersion string `json:"catalog_version"`
}

type v2OverviewBootstrapResponse struct {
	Generation          int64                      `json:"generation"`
	LocalTZName         string                     `json:"local_tz_name"`
	TotalKnowledgeCount int                        `json:"total_knowledge_count"`
	TotalRawCount       *int                       `json:"total_raw_count,omitempty"`
	DayBuckets          []v2OverviewDayBucket      `json:"day_buckets"`
	Projects            []v2OverviewProjectSummary `json:"projects"`
	Common              *v2OverviewCommonSummary   `json:"common"`
	RecentItems         []v2KnowledgeItem          `json:"recent_items"`
	PipelineRuns        []activityRow              `json:"pipeline_runs"`
	PipelineRunsMore    bool                       `json:"pipeline_runs_more"`
	RecallEvents        []RecallEvent              `json:"recall_events"`
	RecallEventsMore    bool                       `json:"recall_events_more"`
	RecallStartedAt     string                     `json:"recall_started_at"`
}

func v2IdentityHeaders(readingID string) map[string]string {
	if readingID == "" {
		return nil
	}
	return map[string]string{v2HeaderReadingIdentity: readingID}
}

func (c ProjectControlClient) overviewBootstrapV2(ctx context.Context, readingID string, days, recentLimit, activityLimit, recallLimit int) (v2OverviewBootstrapResponse, error) {
	var out v2OverviewBootstrapResponse
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	if recentLimit > 0 {
		q.Set("recent_limit", strconv.Itoa(min(50, recentLimit)))
	}
	if activityLimit > 0 {
		q.Set("activity_limit", strconv.Itoa(min(50, activityLimit)))
	}
	if recallLimit > 0 {
		q.Set("recall_limit", strconv.Itoa(min(50, recallLimit)))
	}
	path := "/v2/overview/bootstrap"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2OverviewDayResponse struct {
	Day        string            `json:"day"`
	Generation int64             `json:"generation"`
	TotalCount int               `json:"total_count"`
	Items      []v2KnowledgeItem `json:"items"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) overviewDayV2(ctx context.Context, readingID, day string, limit int, cursor string) (v2OverviewDayResponse, error) {
	var out v2OverviewDayResponse
	q := url.Values{}
	q.Set("day", day)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v2/overview/day?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

func buildOverviewFromV2Bootstrap(projects []Project, resp v2OverviewBootstrapResponse, wantActivity int) (overviewSnapshot, map[string]int, []overviewEvent, bool) {
	allowed := make(map[string]bool, len(projects))
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	recent := make([]Item, 0, len(resp.RecentItems))
	for _, row := range resp.RecentItems {
		if row.DocID == "" || (len(allowed) > 0 && !allowed[row.DatasetID]) {
			continue
		}
		recent = append(recent, row.toItem(resp.RecallStartedAt))
	}
	allServerDatasetsAllowed := true
	if len(allowed) > 0 {
		for _, proj := range resp.Projects {
			if proj.DatasetID != "" && !allowed[proj.DatasetID] {
				allServerDatasetsAllowed = false
				break
			}
		}
		if allServerDatasetsAllowed && resp.Common != nil && resp.Common.DatasetID != "" && !allowed[resp.Common.DatasetID] {
			allServerDatasetsAllowed = false
		}
	}
	rawTotal := 0
	if resp.TotalRawCount != nil && allServerDatasetsAllowed {
		rawTotal = *resp.TotalRawCount
	} else {
		for _, proj := range resp.Projects {
			if len(allowed) == 0 || allowed[proj.DatasetID] {
				rawTotal += proj.RawDocCount + proj.SessionCount
			}
		}
		if resp.Common != nil && (len(allowed) == 0 || allowed[resp.Common.DatasetID]) {
			rawTotal += resp.Common.RawDocCount
		}
	}
	buckets := make(map[string]int, len(resp.DayBuckets))
	for _, b := range resp.DayBuckets {
		if b.Day != "" && b.Count > 0 {
			buckets[b.Day] = b.Count
		}
	}
	snapshot := overviewSnapshot{
		Knowledge: recent,
		Raw:       rawTotal,
	}
	events := aggregateActivity(resp.PipelineRuns, projects)
	more := len(events) > wantActivity || resp.PipelineRunsMore
	if len(events) > wantActivity {
		events = events[:wantActivity]
	}
	merged, mergedMore := mergeRecallActivity(events, resp.RecallEvents, projects, wantActivity)
	more = more || resp.RecallEventsMore || mergedMore
	return snapshot, buckets, merged, more
}

type v2KnowledgePageOpts struct {
	Cursor      string
	Direction   string
	AnchorDocID string
}

type v2ProjectKnowledgeResponse struct {
	ProjectID      string            `json:"project_id"`
	DatasetID      string            `json:"dataset_id"`
	Generation     int64             `json:"generation"`
	CatalogVersion string            `json:"catalog_version"`
	Sort           string            `json:"sort"`
	TotalFiltered  int               `json:"total_filtered"`
	UnreadCount    int               `json:"unread_count"`
	CategoryCounts map[string]int    `json:"category_counts"`
	Items          []v2KnowledgeItem `json:"items"`
	NextCursor     string            `json:"next_cursor"`
	HasMore        bool              `json:"has_more"`
	PrevCursor     string            `json:"prev_cursor"`
	HasPrev        bool              `json:"has_prev"`
}

func (c ProjectControlClient) projectKnowledgeV2(ctx context.Context, readingID, projectID, sortBy, category string, unreadOnly bool, recallAgentID, recallEventID string, limit int, cursor string) (v2ProjectKnowledgeResponse, error) {
	return c.projectKnowledgePageV2(ctx, readingID, projectID, sortBy, category, unreadOnly, recallAgentID, recallEventID, limit, v2KnowledgePageOpts{Cursor: cursor})
}

func (c ProjectControlClient) projectKnowledgePageV2(ctx context.Context, readingID, projectID, sortBy, category string, unreadOnly bool, recallAgentID, recallEventID string, limit int, opts v2KnowledgePageOpts) (v2ProjectKnowledgeResponse, error) {
	var out v2ProjectKnowledgeResponse
	q := url.Values{}
	if sortBy != "" {
		q.Set("sort", sortBy)
	}
	if category != "" {
		q.Set("category", category)
	}
	if unreadOnly {
		q.Set("unread_only", "1")
	}
	if recallAgentID != "" && recallEventID != "" {
		q.Set("recall_agent_id", recallAgentID)
		q.Set("recall_event_id", recallEventID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(50, limit)))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Direction == "prev" || opts.Direction == "next" {
		q.Set("direction", opts.Direction)
	}
	if opts.AnchorDocID != "" {
		q.Set("anchor_doc_id", opts.AnchorDocID)
	}
	path := "/v2/projects/" + url.PathEscape(projectID) + "/knowledge"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2CommonKnowledgeResponse struct {
	DatasetID      string            `json:"dataset_id"`
	Generation     int64             `json:"generation"`
	CatalogVersion string            `json:"catalog_version"`
	CommonCategory string            `json:"common_category"`
	TotalFiltered  int               `json:"total_filtered"`
	CategoryCounts map[string]int    `json:"category_counts"`
	Items          []v2KnowledgeItem `json:"items"`
	NextCursor     string            `json:"next_cursor"`
	HasMore        bool              `json:"has_more"`
	PrevCursor     string            `json:"prev_cursor"`
	HasPrev        bool              `json:"has_prev"`
}

func (c ProjectControlClient) commonKnowledgeV2(ctx context.Context, datasetID, commonCategory string, limit int, cursor string) (v2CommonKnowledgeResponse, error) {
	return c.commonKnowledgePageV2(ctx, datasetID, commonCategory, limit, v2KnowledgePageOpts{Cursor: cursor})
}

func (c ProjectControlClient) commonKnowledgePageV2(ctx context.Context, datasetID, commonCategory string, limit int, opts v2KnowledgePageOpts) (v2CommonKnowledgeResponse, error) {
	var out v2CommonKnowledgeResponse
	q := url.Values{}
	if datasetID != "" {
		q.Set("dataset_id", datasetID)
	}
	if commonCategory == "" {
		commonCategory = "all"
	}
	q.Set("common_category", commonCategory)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(50, limit)))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Direction == "prev" || opts.Direction == "next" {
		q.Set("direction", opts.Direction)
	}
	if opts.AnchorDocID != "" {
		q.Set("anchor_doc_id", opts.AnchorDocID)
	}
	path := "/v2/common/knowledge?" + q.Encode()
	err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024)
	return out, err
}

type v2SearchSessionItem struct {
	SessionID      string `json:"session_id"`
	DatasetID      string `json:"dataset_id"`
	ProjectID      string `json:"project_id"`
	UserID         string `json:"user_id"`
	AgentID        string `json:"agent_id"`
	Label          string `json:"label"`
	MsgCount       int    `json:"msg_count"`
	ToolCalls      int    `json:"tool_calls"`
	LastActivityAt string `json:"last_activity_at"`
	StartedAt      string `json:"started_at"`
}

func (row v2SearchSessionItem) toItem() Item {
	sess := Session{
		ID:      row.SessionID,
		Dataset: row.DatasetID,
		User:    row.UserID,
		Agent:   row.AgentID,
		Updated: row.LastActivityAt,
		Started: row.StartedAt,
	}
	title := row.SessionID
	if strings.TrimSpace(row.Label) != "" {
		title = sessionTitle(row.Label)
	}
	summary := sessionCoverage(SessionDetail{Count: row.MsgCount, ToolCount: row.ToolCalls})
	return Item{
		ID:             row.SessionID,
		Dataset:        row.DatasetID,
		Kind:           "会话",
		Title:          title,
		SessionSummary: summary,
		Date:           row.LastActivityAt,
		Session:        &sess,
		ReadingState:   "ready",
	}
}

type v2SearchRecentResponse struct {
	Generation     int64                 `json:"generation"`
	KnowledgeItems []v2KnowledgeItem     `json:"knowledge_items"`
	SessionItems   []v2SearchSessionItem `json:"session_items"`
}

func (c ProjectControlClient) searchRecentV2(ctx context.Context, readingID string, limit int, projectID string) (v2SearchRecentResponse, error) {
	var out v2SearchRecentResponse
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	path := "/v2/search/recent?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2SearchMetadataResponse struct {
	Query      string            `json:"query"`
	Generation int64             `json:"generation"`
	Items      []v2KnowledgeItem `json:"items"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) searchMetadataV2(ctx context.Context, readingID, query, filter string, limit int, projectID string) (v2SearchMetadataResponse, error) {
	var out v2SearchMetadataResponse
	q := url.Values{}
	q.Set("q", query)
	if filter == "" {
		filter = "all"
	}
	q.Set("filter", filter)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	path := "/v2/search/metadata?" + q.Encode()
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2RawSessionEntry struct {
	Session
	Label           string            `json:"label"`
	MsgCount        int               `json:"msg_count"`
	ToolCalls       int               `json:"tool_calls"`
	ChildCount      int               `json:"child_count"`
	ChildrenPreview []rawSessionEntry `json:"children_preview"`
	ChildrenHasMore bool              `json:"children_has_more"`
}

type v2RawRecordItem struct {
	ItemKind string             `json:"item_kind"`
	SortTSNS int64              `json:"sort_ts_ns"`
	Session  *v2RawSessionEntry `json:"session,omitempty"`
	Document *Document          `json:"document,omitempty"`
}

type v2RawRecordsResponse struct {
	ProjectID  string            `json:"project_id"`
	DatasetID  string            `json:"dataset_id"`
	Kind       string            `json:"kind"`
	CacheReady bool              `json:"cache_ready"`
	Items      []v2RawRecordItem `json:"items"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) rawRecordsV2(ctx context.Context, projectID, kind string, limit int, cursor string) (v2RawRecordsResponse, error) {
	var out v2RawRecordsResponse
	q := url.Values{}
	q.Set("kind", rawRecordsAPIKind(kind))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v2/projects/" + url.PathEscape(projectID) + "/raw-records?" + q.Encode()
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024); err != nil {
		return v2RawRecordsResponse{}, err
	}
	return out, nil
}

type v2SessionChildrenResponse struct {
	ProjectID       string            `json:"project_id"`
	DatasetID       string            `json:"dataset_id"`
	ParentSessionID string            `json:"parent_session_id"`
	TotalCount      int               `json:"total_count"`
	Items           []rawSessionEntry `json:"items"`
	NextCursor      string            `json:"next_cursor"`
	HasMore         bool              `json:"has_more"`
}

func (c ProjectControlClient) sessionChildrenV2(ctx context.Context, projectID, sessionID, userID string, limit int, cursor string) (v2SessionChildrenResponse, error) {
	var out v2SessionChildrenResponse
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v2/projects/" + url.PathEscape(projectID) + "/raw-records/sessions/" + url.PathEscape(sessionID) + "/children"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024); err != nil {
		return v2SessionChildrenResponse{}, err
	}
	return out, nil
}

func v2RawRecordsToItems(dataset string, resp v2RawRecordsResponse) ([]Item, error) {
	if resp.DatasetID != "" && dataset != "" && resp.DatasetID != dataset {
		return nil, fmt.Errorf("官方数据组不匹配")
	}
	ds := dataset
	if ds == "" {
		ds = resp.DatasetID
	}
	out := make([]Item, 0, len(resp.Items))
	for _, entry := range resp.Items {
		switch entry.ItemKind {
		case "session":
			if entry.Session == nil || entry.Session.ID == "" {
				continue
			}
			if entry.Session.Dataset != "" && ds != "" && entry.Session.Dataset != ds {
				return nil, fmt.Errorf("官方会话身份不匹配")
			}
			base := rawSessionEntry{
				Session:   entry.Session.Session,
				Label:     entry.Session.Label,
				MsgCount:  entry.Session.MsgCount,
				ToolCalls: entry.Session.ToolCalls,
			}
			item := rawSessionEntryToItem(ds, base, resp.CacheReady)
			item.SubagentTotalCount = entry.Session.ChildCount
			item.SubagentsHasMore = entry.Session.ChildrenHasMore
			if len(entry.Session.ChildrenPreview) > 0 {
				parentTitle := sessionReadableParentTitle(item)
				kids := make([]Item, 0, len(entry.Session.ChildrenPreview))
				for _, childEntry := range entry.Session.ChildrenPreview {
					if childEntry.ID == "" {
						continue
					}
					child := rawSessionEntryToItem(ds, childEntry, resp.CacheReady)
					child.ParentTitle = parentTitle
					kids = append(kids, child)
				}
				item.Subagents = kids
				if item.SubagentTotalCount < len(kids) {
					item.SubagentTotalCount = len(kids)
				}
			}
			out = append(out, item)
		case "document":
			if entry.Document == nil || entry.Document.ID == "" {
				continue
			}
			docCopy := *entry.Document
			if docCopy.Dataset == "" {
				docCopy.Dataset = ds
			}
			if ds != "" && docCopy.Dataset != ds {
				return nil, fmt.Errorf("官方内容身份不匹配")
			}
			if docCopy.knowledge() || derivedMaterial(docCopy) {
				continue
			}
			out = append(out, Item{
				ID:       docCopy.ID,
				Dataset:  docCopy.Dataset,
				Kind:     "文档",
				Title:    docCopy.title(),
				Date:     docCopy.date(),
				Document: &docCopy,
			})
		}
	}
	return out, nil
}

type v2UnreadPreviewItem struct {
	DocID         string `json:"doc_id"`
	Title         string `json:"title"`
	EffectiveDate string `json:"effective_date"`
	CreatedAt     string `json:"created_at"`
}

type v2MessageSummaryResponse struct {
	ProjectID               string                          `json:"project_id"`
	DatasetID               string                          `json:"dataset_id"`
	AutomaticSave           bool                            `json:"automatic_save"`
	Backlog                 *backlogJob                     `json:"backlog"`
	Checks                  []pendingCheckState             `json:"checks,omitempty"`
	BufferReviews           []bufferReview                  `json:"buffer_reviews,omitempty"`
	KnowledgeCatalogVersion string                          `json:"knowledge_catalog_version"`
	UnreadCount             int                             `json:"unread_count"`
	UnreadPreview           []v2UnreadPreviewItem           `json:"unread_preview"`
	UnreadHasMore           bool                            `json:"unread_has_more"`
	PendingDocumentsCount   int                             `json:"pending_documents_count"`
	PendingDocuments        []projectMessageSummaryDocument `json:"pending_documents"`
	Notices                 []processingNotice              `json:"notices"`
	ProcessingNotices       []processingNotice              `json:"processing_notices,omitempty"`
	Activity                []activityRow                   `json:"activity"`
	Errors                  map[string]string               `json:"errors"`
}

func (r v2MessageSummaryResponse) notices() []processingNotice {
	if len(r.Notices) > 0 {
		return r.Notices
	}
	return r.ProcessingNotices
}

func (r v2MessageSummaryResponse) pendingItems(defaultDataset string) []Item {
	compat := projectMessageSummaryResponse{
		DatasetID:        r.DatasetID,
		PendingDocuments: r.PendingDocuments,
	}
	return compat.pendingItems(defaultDataset)
}

func (r v2MessageSummaryResponse) unreadPreviewItems(defaultDataset string) []Item {
	ds := r.DatasetID
	if ds == "" {
		ds = defaultDataset
	}
	out := make([]Item, 0, len(r.UnreadPreview))
	for _, row := range r.UnreadPreview {
		if row.DocID == "" {
			continue
		}
		doc := Document{
			ID:              row.DocID,
			Dataset:         ds,
			FirstSourceDate: row.EffectiveDate,
			Created:         row.CreatedAt,
			Metadata:        map[string]any{"node_set": []any{"session_learnings"}},
		}
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = knowledgePlaceholder(row.EffectiveDate)
		}
		date := row.EffectiveDate
		if date == "" {
			date = doc.date()
		}
		out = append(out, Item{
			ID:                row.DocID,
			Dataset:           ds,
			Kind:              "知识",
			Title:             title,
			Date:              date,
			Document:          &doc,
			ReadingState:      "ready",
			ServerUnread:      true,
			ServerUnreadKnown: true,
		})
	}
	return out
}

func (c ProjectControlClient) projectMessageSummaryV2(ctx context.Context, readingID, projectID string) (v2MessageSummaryResponse, error) {
	var out v2MessageSummaryResponse
	path := "/v2/projects/" + url.PathEscape(projectID) + "/message-summary"
	err := c.requestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type v2ReadingStateProjectRef struct {
	ProjectID string `json:"project_id"`
	DatasetID string `json:"dataset_id"`
}

type v2ReadingStateMigrateRequest struct {
	ReadingIdentity string                     `json:"reading_identity"`
	Projects        []v2ReadingStateProjectRef `json:"projects,omitempty"`
}

type v2ReadingStateMigratedScope struct {
	ProjectID     string `json:"project_id"`
	DatasetID     string `json:"dataset_id"`
	ScopeHash     string `json:"scope_hash"`
	Status        string `json:"status"`
	ImportedItems int    `json:"imported_items"`
	UnreadCount   int    `json:"unread_count"`
}

type v2ReadingStateMigrateResponse struct {
	Migrated []v2ReadingStateMigratedScope `json:"migrated"`
}

func (c ProjectControlClient) readingStateMigrateLocalV2(ctx context.Context, req v2ReadingStateMigrateRequest) (v2ReadingStateMigrateResponse, error) {
	var out v2ReadingStateMigrateResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/migrate-local", req, &out, 512*1024)
	return out, err
}

type v2ReadingStateImportBeginRequest struct {
	ReadingIdentity string `json:"reading_identity"`
	ProjectID       string `json:"project_id"`
	DatasetID       string `json:"dataset_id"`
	Initialized     bool   `json:"initialized"`
	EnabledAt       string `json:"enabled_at"`
}

type v2ReadingStateImportBeginResponse struct {
	BatchID   string `json:"batch_id"`
	ScopeHash string `json:"scope_hash"`
}

func (c ProjectControlClient) readingStateImportBeginV2(ctx context.Context, req v2ReadingStateImportBeginRequest) (v2ReadingStateImportBeginResponse, error) {
	var out v2ReadingStateImportBeginResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/import/begin", req, &out, 256*1024)
	return out, err
}

type v2ReadingStateImportChunkItem struct {
	DocID  string `json:"doc_id"`
	IsRead bool   `json:"is_read"`
}

type v2ReadingStateImportChunkRequest struct {
	BatchID string                          `json:"batch_id"`
	Items   []v2ReadingStateImportChunkItem `json:"items"`
}

type v2ReadingStateImportChunkResponse struct {
	BatchID string `json:"batch_id"`
	Staged  int    `json:"staged"`
}

func (c ProjectControlClient) readingStateImportChunkV2(ctx context.Context, req v2ReadingStateImportChunkRequest) (v2ReadingStateImportChunkResponse, error) {
	var out v2ReadingStateImportChunkResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/import/chunk", req, &out, 256*1024)
	return out, err
}

type v2ReadingStateImportCommitRequest struct {
	BatchID       string `json:"batch_id"`
	ExpectedItems int    `json:"expected_items,omitempty"`
}

type v2ReadingStateImportCommitResponse struct {
	ScopeHash   string `json:"scope_hash"`
	Committed   int    `json:"committed"`
	UnreadCount int    `json:"unread_count"`
}

func (c ProjectControlClient) readingStateImportCommitV2(ctx context.Context, req v2ReadingStateImportCommitRequest) (v2ReadingStateImportCommitResponse, error) {
	var out v2ReadingStateImportCommitResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/import/commit", req, &out, 256*1024)
	return out, err
}

type v2ReadingStateMarkReadRequest struct {
	ReadingIdentity string   `json:"reading_identity"`
	ProjectID       string   `json:"project_id"`
	DatasetID       string   `json:"dataset_id"`
	DocIDs          []string `json:"doc_ids"`
}

type v2ReadingStateMarkReadResponse struct {
	ScopeHash        string `json:"scope_hash"`
	Marked           int    `json:"marked"`
	UnreadCount      int    `json:"unread_count"`
	UnreadCountKnown *bool  `json:"unread_count_known,omitempty"`
	StateRev         int64  `json:"state_rev,omitempty"`
	ReadHorizonAtNS  string `json:"read_horizon_at_ns"`
}

func (r v2ReadingStateMarkReadResponse) unreadCountKnown() bool {
	return r.UnreadCountKnown == nil || *r.UnreadCountKnown
}

func (c ProjectControlClient) readingStateMarkReadV2(ctx context.Context, req v2ReadingStateMarkReadRequest) (v2ReadingStateMarkReadResponse, error) {
	var out v2ReadingStateMarkReadResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/mark-read", req, &out, 256*1024)
	return out, err
}

type v2ReadingStateMarkAllReadRequest struct {
	ReadingIdentity string `json:"reading_identity"`
	ProjectID       string `json:"project_id"`
	DatasetID       string `json:"dataset_id"`
}

type v2ReadingStateMarkAllReadResponse struct {
	ScopeHash        string `json:"scope_hash"`
	UnreadCount      int    `json:"unread_count"`
	UnreadCountKnown *bool  `json:"unread_count_known,omitempty"`
	StateRev         int64  `json:"state_rev,omitempty"`
	ReadHorizonAtNS  string `json:"read_horizon_at_ns"`
}

func (r v2ReadingStateMarkAllReadResponse) unreadCountKnown() bool {
	return r.UnreadCountKnown == nil || *r.UnreadCountKnown
}

func (c ProjectControlClient) readingStateMarkAllReadV2(ctx context.Context, req v2ReadingStateMarkAllReadRequest) (v2ReadingStateMarkAllReadResponse, error) {
	var out v2ReadingStateMarkAllReadResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/reading-state/mark-all-read", req, &out, 256*1024)
	return out, err
}

type v2ProvenanceBackfillRequest struct {
	ReadingIdentity string            `json:"reading_identity"`
	Days            map[string]string `json:"days,omitempty"`
}

type v2ProvenanceBackfillResponse struct {
	Status                 string `json:"status"`
	Matched                int    `json:"matched"`
	Imported               int    `json:"imported"`
	Skipped                int    `json:"skipped"`
	Unmapped               int    `json:"unmapped"`
	HasUnconfirmedProjects bool   `json:"has_unconfirmed_projects"`
	BackupPath             string `json:"backup_path"`
}

func (c ProjectControlClient) provenanceBackfillLegacyDatesV2(ctx context.Context, req v2ProvenanceBackfillRequest) (v2ProvenanceBackfillResponse, error) {
	var out v2ProvenanceBackfillResponse
	err := c.requestWithLimit(ctx, "POST", "/v2/provenance/backfill-legacy-dates", req, &out, 512*1024)
	return out, err
}

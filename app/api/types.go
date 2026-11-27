package api

import "encoding/json"

type AccessLevel string

const (
	AccessNone     AccessLevel = "none"
	AccessRead     AccessLevel = "read"
	AccessMaintain AccessLevel = "maintain"
)

type CaptureFilterEvent struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	AgentID    string `json:"agent_id"`
	HostKind   string `json:"host_kind"`
	SessionID  string `json:"session_id"`
	ToolName   string `json:"tool_name"`
	Rule       string `json:"rule"`
	Category   string `json:"category"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	NextAction string `json:"next_action"`
	OccurredAt string `json:"occurred_at"`
}

type InboxSummary struct {
	Running        bool           `json:"running"`
	Pending        int            `json:"pending"`
	Processing     int            `json:"processing"`
	Succeeded      int            `json:"succeeded"`
	Failed         int            `json:"failed"`
	RecentFailures []InboxFailure `json:"recent_failures"`

	FilteredCount  int                  `json:"filtered_count,omitempty"`
	RecentFiltered []CaptureFilterEvent `json:"recent_filtered,omitempty"`
}

type InboxFailure struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	LastError string `json:"last_error"`
	Attempts  int    `json:"attempts"`
	UpdatedAt string `json:"updated_at"`

	LastErrorAt string `json:"last_error_at,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Summary     string `json:"summary,omitempty"`
	NextAction  string `json:"next_action,omitempty"`
}

type EngineCapabilities struct {
	ToolGraph      bool `json:"tool_graph"`
	LanguageEgress bool `json:"language_egress"`
	ModelHandoff   bool `json:"model_handoff"`
}

type EngineHealth struct {
	OK           bool               `json:"ok"`
	Engine       string             `json:"engine"`
	Version      string             `json:"version"`
	Mode         string             `json:"mode"`
	ModelDigest  string             `json:"model_digest"`
	StorePath    string             `json:"store_path"`
	Capabilities EngineCapabilities `json:"capabilities"`
	Error        string             `json:"error,omitempty"`
}

type HealthResponse struct {
	OK            bool         `json:"ok"`
	Status        string       `json:"status"`
	UptimeSeconds int64        `json:"uptime_seconds"`
	EngineOK      bool         `json:"engine_ok"`
	Inbox         InboxSummary `json:"inbox"`
}

type CapabilitiesResponse struct {
	APIVersions     []string           `json:"api_versions"`
	ReadingContract string             `json:"reading_contract"`
	Capabilities    EngineCapabilities `json:"capabilities"`
	Features        map[string]bool    `json:"features"`
}

type RuntimeMonitorResponse struct {
	Daemon map[string]json.RawMessage `json:"daemon"`
	Engine EngineHealth               `json:"engine"`
	Inbox  InboxSummary               `json:"inbox"`

	CheckedAtISO  string `json:"checked_at_iso"`
	LastOKAtISO   string `json:"last_ok_at_iso"`
	LastErrorAt   string `json:"last_error_at"`
	StatusSummary string `json:"status_summary"`
	NextAction    string `json:"next_action"`
	Summary       struct {
		FilteredCount  int                  `json:"filtered_count,omitempty"`
		RecentFiltered []CaptureFilterEvent `json:"recent_filtered,omitempty"`
	} `json:"summary"`
}

type ProjectSummary struct {
	ProjectID          string `json:"project_id"`
	Name               string `json:"name"`
	DatasetID          string `json:"dataset_id"`
	AutoCapture        bool   `json:"auto_capture"`
	AutoCaptureVersion int    `json:"auto_capture_version"`
	KnowledgeCount     int    `json:"knowledge_count"`
	RawDocCount        int    `json:"raw_doc_count"`
	SessionCount       int    `json:"session_count"`
	UnreadCount        int    `json:"unread_count"`
	UnreadStateRev     int64  `json:"unread_state_rev"`
	CatalogVersion     string `json:"catalog_version"`
	InboxPending       int    `json:"inbox_pending"`
	InboxFailed        int    `json:"inbox_failed"`
	LastError          string `json:"last_error"`

	AutoCaptureUpdatedAt string `json:"auto_capture_updated_at"`
	AutoCaptureReason    string `json:"auto_capture_reason"`
	AutoCaptureActor     string `json:"auto_capture_actor"`
	LastErrorAt          string `json:"last_error_at"`
	FilteredCount        int    `json:"filtered_count"`
}

type ACLOverride struct {
	AgentID     string       `json:"agent_id"`
	Access      *AccessLevel `json:"access,omitempty"`
	Capture     *bool        `json:"capture,omitempty"`
	AutoProcess *bool        `json:"auto_process,omitempty"`
}

type ProjectView struct {
	ProjectSummary
	ID              string                 `json:"id"`
	DatasetName     string                 `json:"dataset_name"`
	CodeDatasetID   string                 `json:"code_dataset_id"`
	CodeEnabled     bool                   `json:"code_enabled"`
	Status          string                 `json:"status"`
	Aliases         []string               `json:"aliases"`
	Overrides       map[string]ACLOverride `json:"overrides"`
	Inbox           InboxSummary           `json:"inbox"`
	DesiredRevision int64                  `json:"desired_revision"`
	Applied         map[string]AppliedACL  `json:"applied"`

	RecentFiltered []CaptureFilterEvent `json:"recent_filtered,omitempty"`
}

type CreateProjectRequest struct {
	ID            string   `json:"id,omitempty"`
	ProjectID     string   `json:"project_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	DatasetID     string   `json:"dataset_id,omitempty"`
	DatasetName   string   `json:"dataset_name,omitempty"`
	AutoCapture   *bool    `json:"auto_capture,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	Status        *string  `json:"status,omitempty"`
	CodeEnabled   *bool    `json:"code_enabled,omitempty"`
	CodeDatasetID *string  `json:"code_dataset_id,omitempty"`
}

type UpdateProjectRequest struct {
	Name          *string   `json:"name,omitempty"`
	AutoCapture   *bool     `json:"auto_capture,omitempty"`
	Aliases       *[]string `json:"aliases,omitempty"`
	Status        *string   `json:"status,omitempty"`
	CodeEnabled   *bool     `json:"code_enabled,omitempty"`
	CodeDatasetID *string   `json:"code_dataset_id,omitempty"`
}

type ProjectPolicy struct {
	Access      AccessLevel `json:"access"`
	Capture     bool        `json:"capture"`
	AutoProcess bool        `json:"auto_process"`
}
type AppliedACL struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}
type ListProjectsResponse struct {
	Projects []ProjectView `json:"projects"`
	Agents   []AgentView   `json:"agents"`
	Common   *ProjectView  `json:"common"`
	Revision int64         `json:"revision"`
	Defaults ProjectPolicy `json:"defaults"`
}

type RetryProjectResponse struct {
	ProjectID    string   `json:"project_id"`
	RetriedCount int      `json:"retried_count"`
	ItemIDs      []string `json:"item_ids"`
}

type OverviewOptions struct {
	Days, RecentLimit, RecallLimit, SearchLimit int
}

type DayBucket struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type OverviewSnapshot struct {
	Generation       int64                `json:"generation"`
	LocalTZName      string               `json:"local_tz_name"`
	TotalKnowledge   int                  `json:"total_knowledge_count"`
	TotalRaw         int                  `json:"total_raw_count"`
	TotalUnread      int                  `json:"total_unread_count"`
	DayBuckets       []DayBucket          `json:"day_buckets"`
	Projects         []ProjectSummary     `json:"projects"`
	Common           *ProjectSummary      `json:"common"`
	RecentItems      []CatalogItem        `json:"recent_items"`
	RecallEvents     []OverviewRecall     `json:"recall_events"`
	RecallStats      []OverviewRecallStat `json:"recall_query_stats"`
	RecallEventsMore bool                 `json:"recall_events_more"`
	RecallStartedAt  string               `json:"recall_started_at"`
	Searches         []SearchHistoryEntry `json:"recent_searches"`
	Inbox            InboxSummary         `json:"inbox"`
}

type OverviewRecallHit struct {
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
}

type OverviewRecall struct {
	AgentName  string              `json:"agent_name"`
	AgentKind  string              `json:"agent_kind"`
	AgentID    string              `json:"agent_id"`
	EventID    string              `json:"event_id"`
	Channel    string              `json:"channel"`
	ReceivedAt string              `json:"received_at"`
	Hits       []OverviewRecallHit `json:"hits"`
}

type OverviewRecallStat struct {
	Channel      string `json:"channel"`
	TotalQueries int    `json:"total_queries"`
	HitQueries   int    `json:"hit_queries"`
	EmptyQueries int    `json:"empty_queries"`
	TotalHits    int    `json:"total_hits"`
	LastQueryAt  string `json:"last_query_at"`
}

type SearchHistoryEntry struct {
	Query      string `json:"query"`
	Filter     string `json:"filter"`
	ProjectID  string `json:"project_id,omitempty"`
	DatasetID  string `json:"dataset_id,omitempty"`
	HitCount   int    `json:"hit_count"`
	SearchedAt string `json:"searched_at"`
}

type CatalogItem struct {
	ProjectID      string `json:"project_id,omitempty"`
	DatasetID      string `json:"dataset_id"`
	DocID          string `json:"doc_id"`
	DocumentID     string `json:"document_id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	DocName        string `json:"doc_name"`
	DocLabel       string `json:"doc_label"`
	EffectiveDate  string `json:"effective_date"`
	Category       string `json:"category"`
	CommonCategory string `json:"common_category"`
	RecallCount    int    `json:"recall_count"`
	IsUnread       bool   `json:"is_unread"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	LessonType     string `json:"lesson_type"`
	StateKey       string `json:"state_key"`
	SessionID      string `json:"session_id"`
	ContentHash    string `json:"content_hash"`
	PipelineStatus string `json:"pipeline_status"`
}

type KnowledgePageOptions struct {
	ProjectID, Sort, Category, CommonCategory, RecallAgentID, RecallEventID string
	Cursor, Direction, AnchorDocID                                          string
	Limit                                                                   int
	UnreadOnly                                                              bool
}

type KnowledgePage struct {
	ProjectID      string         `json:"project_id"`
	DatasetID      string         `json:"dataset_id"`
	Sort           string         `json:"sort"`
	Day            string         `json:"day"`
	Items          []CatalogItem  `json:"items"`
	NextCursor     string         `json:"next_cursor"`
	PrevCursor     string         `json:"prev_cursor"`
	HasMore        bool           `json:"has_more"`
	HasPrev        bool           `json:"has_prev"`
	TotalFiltered  int            `json:"total_filtered"`
	TotalCount     int            `json:"total_count"`
	CategoryCounts map[string]int `json:"category_counts"`
	UnreadCount    int            `json:"unread_count"`
	UnreadStateRev int64          `json:"unread_state_rev"`
	CatalogVersion string         `json:"catalog_version"`
}

type DocumentDetail struct {
	CatalogItem
	Content  string `json:"content"`
	MimeType string `json:"mime_type"`
}

type DocumentBody struct {
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
	Content    string `json:"content"`
	MimeType   string `json:"mime_type"`
}

type RawItem struct {
	DatasetID       string `json:"dataset_id"`
	ProjectID       string `json:"project_id,omitempty"`
	Kind            string `json:"kind"`
	DocID           string `json:"doc_id,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Title           string `json:"title"`
	Label           string `json:"label,omitempty"`
	MsgCount        int       `json:"msg_count"`
	ToolCalls       int       `json:"tool_calls"`
	IsSubagent      bool      `json:"is_subagent,omitempty"`
	DocName         string    `json:"doc_name,omitempty"`
	DocLabel        string    `json:"doc_label,omitempty"`
	Status          string    `json:"status,omitempty"`
	StartedAt       string    `json:"started_at,omitempty"`
	LastActivityAt  string    `json:"last_activity_at,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	RootSessionID   string    `json:"root_session_id,omitempty"`
	SubagentRole    string    `json:"subagent_role,omitempty"`
	NestingDepth    int       `json:"nesting_depth,omitempty"`
	IsTopLevel      bool      `json:"is_top_level"`
	ChildCount      int       `json:"child_count"`
	ChildrenPreview []RawItem `json:"children_preview,omitempty"`
	ChildrenHasMore bool      `json:"children_has_more"`
	EffectiveDate   string    `json:"effective_date,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type RawRecordsOptions struct {
	ProjectID, Kind, UserID, Cursor, Direction, AnchorDocID string
	Limit                                                   int
}

type RawRecordsPage struct {
	Items      []RawItem `json:"items"`
	NextCursor string    `json:"next_cursor"`
	PrevCursor string    `json:"prev_cursor"`
	HasMore    bool      `json:"has_more"`
	HasPrev    bool      `json:"has_prev"`
}

type SessionHistoryOptions struct {
	DatasetID   string
	SessionID   string
	UserID      string
	QAOffset    int
	TraceOffset int
	Limit       int
}

type SessionHistoryResponse struct {
	SessionDetail
	Paging      bool      `json:"history_paging"`
	QAOffset    int       `json:"qa_offset"`
	TraceOffset int       `json:"trace_offset"`
	Limit       int       `json:"page_limit"`
	QAMore      bool      `json:"qa_more"`
	TraceMore   bool      `json:"trace_more"`
	ParentLabel string    `json:"parent_label,omitempty"`
	Subagents   []RawItem `json:"subagents,omitempty"`
}

type SaveCommonMemoryRequest struct {
	Category *string `json:"common_category,omitempty"`
	Title    *string `json:"title,omitempty"`
	Label    *string `json:"label,omitempty"`
	Content  *string `json:"content,omitempty"`
}

type SaveCommonMemoryResponse struct {
	OK         bool   `json:"ok"`
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
	DocID      string `json:"doc_id"`
	Category   string `json:"common_category"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

type SearchRequest struct {
	Query     string `json:"q"`
	Filter    string `json:"filter"`
	ProjectID string `json:"project_id"`
	DatasetID string `json:"dataset_id"`
	Limit     int    `json:"limit"`
	Semantic  bool   `json:"semantic"`
}

type SearchHit struct {
	DatasetID  string         `json:"dataset_id"`
	DocumentID string         `json:"document_id"`
	Title      string         `json:"title"`
	Snippet    string         `json:"snippet"`
	Score      float64        `json:"score"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// SearchItem includes the fields returned for catalog items and sessions.
type SearchItem struct {
	CatalogItem
	UserID          string `json:"user_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Status          string `json:"status,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	LastActivityAt  string `json:"last_activity_at,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	RootSessionID   string `json:"root_session_id,omitempty"`
	SubagentRole    string `json:"subagent_role,omitempty"`
	NestingDepth    int    `json:"nesting_depth,omitempty"`
	IsTopLevel      bool   `json:"is_top_level"`
	ChildCount      int    `json:"child_count"`
}

type SearchResponse struct {
	OK           bool         `json:"ok"`
	Query        string       `json:"query"`
	Filter       string       `json:"filter"`
	Items        []SearchItem `json:"items"`
	SemanticHits []SearchHit  `json:"semantic_hits"`
}

type RecentSearchResponse struct {
	RecentSearches []SearchHistoryEntry `json:"recent_searches"`
	RecentItems    []CatalogItem        `json:"recent_items"`
}

type ReadingStateView struct {
	ProjectID   string `json:"project_id"`
	DatasetID   string `json:"dataset_id"`
	ScopeHash   string `json:"scope_hash"`
	UnreadCount int    `json:"unread_count"`
	StateRev    int64  `json:"state_rev"`
	ReadHorizon string `json:"read_horizon_at_ns"`
}

type ReadingStateResponse struct {
	Projects []ReadingStateView `json:"projects"`
}

type MarkReadRequest struct {
	ReadingIdentity string   `json:"reading_identity,omitempty"`
	ProjectID       string   `json:"project_id"`
	DatasetID       string   `json:"dataset_id"`
	DocIDs          []string `json:"doc_ids,omitempty"`
}

type MarkReadResponse struct {
	ReadingStateView
	Marked int `json:"marked"`
}

type GraphNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label,omitempty"`
	Type       string         `json:"type,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

type GraphEdge struct {
	ID           string         `json:"id,omitempty"`
	Source       string         `json:"source"`
	Target       string         `json:"target"`
	Label        string         `json:"label,omitempty"`
	Relationship string         `json:"relationship,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
}

type GraphResponse struct {
	OK        bool        `json:"ok"`
	DatasetID string      `json:"dataset_id"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
}

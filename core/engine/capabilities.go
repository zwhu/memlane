package engine

import (
	"context"
	"encoding/json"
	"time"
)

type ModelController interface {
	ApplyModel(ctx context.Context, req ApplyModelRequest) (ApplyModelResult, error)
	ModelStatus(ctx context.Context) (ModelStatusResult, error)
	ProbeModel(ctx context.Context, req ProbeRequest) (ProbeResult, error)
}

type ApplyModelRequest struct {
	GenerationConfig map[string]any `json:"generation_config"`
	OutputLanguage   string         `json:"output_language"`
	OperationID      string         `json:"operation_id"`
}

type ApplyModelResult struct {
	OK          bool   `json:"ok"`
	Applied     bool   `json:"applied"`
	ModelDigest string `json:"model_digest"`
	OperationID string `json:"operation_id"`
}

type ModelStatusResult struct {
	OK             bool   `json:"ok"`
	Mode           string `json:"mode"`
	ModelDigest    string `json:"model_digest"`
	OutputLanguage string `json:"output_language"`
	ActiveTasks    int    `json:"active_tasks"`
}

type ProbeRequest struct {
	GenerationMode        string
	Environment           map[string]string
	Runtime               map[string]any
	MaxConcurrentRequests int
	BackendKind           string
	Timeout               time.Duration
}

type ProbeResult struct {
	Status         string         `json:"status"`
	PlainPassed    bool           `json:"plain_passed"`
	SchemaPassed   bool           `json:"schema_passed"`
	QualityPassed  bool           `json:"quality_passed"`
	StructuredMode string         `json:"structured_mode"`
	ErrorCode      string         `json:"error_code,omitempty"`
	AdapterCalls   int            `json:"adapter_calls,omitempty"`
	Details        map[string]any `json:"-"`
}

type CatalogProjector interface {
	SnapshotCatalog(ctx context.Context, targets []CatalogDatasetTarget, opts CatalogSnapshotOptions) (*CatalogSnapshot, error)
	ReadLocalBody(ctx context.Context, ref LocalBodyRef) (string, error)
}

type CatalogDatasetTarget struct {
	DatasetID string
	Protected bool
}

type CatalogDocKey struct {
	DatasetID string
	DocID     string
}

type CatalogTitleHint struct {
	DocLabel        string
	ContentHash     string
	RawDataLocation string
}

type CatalogSnapshotOptions struct {
	SessionsOnly bool
	TitleHints   map[CatalogDocKey]CatalogTitleHint
}

type CatalogSnapshot struct {
	Datasets []CatalogDatasetSnapshot
}

type CatalogDatasetSnapshot struct {
	DatasetID string
	Exists    bool
	Docs      []CatalogSnapshotDoc
	Sessions  []CatalogSnapshotSession
}

type CatalogSnapshotDoc struct {
	DatasetID           string
	DocID               string
	Kind                string
	IsKnowledge         bool
	CommonCategory      string
	LessonType          string
	StateKey            string
	Category            string
	SessionID           string
	DocName             string
	DocLabel            string
	RawDataLocation     string
	ContentHash         string
	CreatedAtRaw        string
	CreatedAtNS         string
	CreatedAtInvalid    bool
	UpdatedAtNS         string
	EffectiveDate       string
	EffectiveDateSource string
	DayDescKey          int64
	SortTSDescKey       int64
	PipelineStatus      string
	SourceUpdatedAt     string
	Provenance          *CatalogSnapshotProvenance
}

type CatalogSnapshotProvenance struct {
	DatasetID        string
	DocumentID       string
	LessonType       string
	StateKey         string
	FirstSourceDate  string
	SourceDateStatus string
	SourceQAIDs      []string
}

type CatalogSnapshotSession struct {
	DatasetID       string
	UserID          string
	SessionID       string
	AgentID         string
	Status          string
	StartedAt       string
	LastActivityAt  string
	SortTSDescKey   int64
	ParentSessionID string
	RootSessionID   string
	SubagentRole    string
	NestingDepth    int
	IsTopLevel      bool
	ChildCount      int
}

type LocalBodyRef struct {
	DatasetID  string
	DocumentID string
	Location   string
	MimeType   string
	MaxBytes   int
}

type SyncStats struct {
	Datasets             int
	DocsProjected        int
	KnowledgeCount       int
	CommonCount          int
	RawDocCount          int
	PendingRawCount      int
	SessionsProjected    int
	DistinctSessionUsers int
	ProvenanceLocked     int
	Duration             time.Duration
}

func AsModelController(e MemoryEngine) (ModelController, bool) {
	controller, ok := e.(ModelController)
	return controller, ok
}

func AsCatalogProjector(e MemoryEngine) (CatalogProjector, bool) {
	projector, ok := e.(CatalogProjector)
	return projector, ok
}

type DiscoveredDataset struct {
	ID        string
	Name      string
	Protected bool
}

type DatasetDiscoverer interface {
	DiscoverDatasets(ctx context.Context) ([]DiscoveredDataset, error)
}

func AsDatasetDiscoverer(e MemoryEngine) (DatasetDiscoverer, bool) {
	discoverer, ok := e.(DatasetDiscoverer)
	return discoverer, ok
}

type SessionRef struct {
	UserID    string
	SessionID string
}

type SessionCacheInfo struct {
	Label     string
	MsgCount  int
	ToolCalls int
}

type SessionQA struct {
	ID                string `json:"qa_id"`
	Question          string `json:"question"`
	Answer            string `json:"answer"`
	QuestionTruncated bool   `json:"question_truncated"`
	AnswerTruncated   bool   `json:"answer_truncated"`
}

type SessionTrace struct {
	ID     string          `json:"trace_id"`
	Name   string          `json:"origin_function"`
	Status string          `json:"status"`
	Params json.RawMessage `json:"method_params"`
	Result json.RawMessage `json:"method_return_value"`
	Error  string          `json:"error_message"`
}

type SessionHistoryRequest struct {
	UserID      string
	SessionID   string
	QAOffset    int
	TraceOffset int
	Limit       int
}

type SessionHistoryResult struct {
	Label     string
	MsgCount  int
	ToolCalls int
	QAs       []SessionQA
	Traces    []SessionTrace
}

type SessionHistoryReader interface {
	HydrateSessionsFromCache(ctx context.Context, refs []SessionRef) (map[SessionRef]SessionCacheInfo, error)
	ReadSessionHistory(ctx context.Context, req SessionHistoryRequest) (SessionHistoryResult, error)
}

func AsSessionHistoryReader(e MemoryEngine) (SessionHistoryReader, bool) {
	reader, ok := e.(SessionHistoryReader)
	return reader, ok
}

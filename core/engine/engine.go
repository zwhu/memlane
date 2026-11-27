package engine

import (
	"context"
	"encoding/json"
)

// MemoryEngine is the storage and retrieval contract shared by engine implementations.
type MemoryEngine interface {
	Health(ctx context.Context) (HealthStatus, error)
	Ingest(ctx context.Context, req IngestRequest) (IngestResult, error)
	Extract(ctx context.Context, req ExtractRequest) (ExtractResult, error)
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
	ListItems(ctx context.Context, req ListRequest) (ListResult, error)
	GetItem(ctx context.Context, scope Scope, itemID string) (Item, error)
	DeleteItem(ctx context.Context, scope Scope, itemID string) error
	Graph(ctx context.Context, req GraphRequest) (GraphPayload, error)
}

type Scope struct {
	DatasetID string `json:"dataset_id"`
	UserID    string `json:"user_id,omitempty"`
}

const (
	HealthModeSetupReady = "setup_ready"
	HealthModeReady      = "ready"
)

type Capabilities struct {
	ToolGraph      bool     `json:"tool_graph"`
	LanguageEgress bool     `json:"language_egress"`
	ModelHandoff   bool     `json:"model_handoff"`
	HybridRecall   bool     `json:"hybrid_recall,omitempty"`
	SessionContext bool     `json:"session_context,omitempty"`
	CodeSearch     bool     `json:"code_search,omitempty"`
	SearchModes    []string `json:"search_modes,omitempty"`
}

type HealthStatus struct {
	OK           bool         `json:"ok"`
	Engine       string       `json:"engine"`
	Version      string       `json:"version"`
	Mode         string       `json:"mode"`
	ModelDigest  string       `json:"model_digest"`
	StorePath    string       `json:"store_path"`
	Capabilities Capabilities `json:"capabilities"`
}

type IngestKind string

const (
	IngestKindCaptureQA    IngestKind = "capture_qa"
	IngestKindCaptureTrace IngestKind = "capture_trace"
	IngestKindDocument     IngestKind = "document"
)

type SourceMeta struct {
	SessionID        string         `json:"session_id,omitempty"`
	ContextDate      string         `json:"context_date,omitempty"`
	ExternalMetadata map[string]any `json:"external_metadata,omitempty"`
	NodeSet          []string       `json:"node_set,omitempty"`
}

type IngestRequest struct {
	Scope          Scope      `json:"scope"`
	Kind           IngestKind `json:"kind"`
	IdempotencyKey string     `json:"idempotency_key"`
	Content        any        `json:"content"`
	SourceMeta     SourceMeta `json:"source_meta,omitempty"`
}

type IngestResult struct {
	OK           bool   `json:"ok"`
	EntryID      string `json:"entry_id"`
	DocumentID   string `json:"document_id"`
	ContentHash  string `json:"content_hash"`
	Deduplicated bool   `json:"deduplicated"`
}

type ExtractMode string

const (
	ExtractModeDefault   ExtractMode = "default"
	ExtractModeToolGraph ExtractMode = "tool_graph"
)

type ExtractRequest struct {
	Scope               Scope       `json:"scope"`
	DocIDs              []string    `json:"doc_ids"`
	ExpectedModelDigest string      `json:"expected_model_digest"`
	OutputLanguage      string      `json:"output_language"`
	Mode                ExtractMode `json:"mode"`
}

type ExtractResult struct {
	OK            bool     `json:"ok"`
	TouchedDocIDs []string `json:"touched_doc_ids"`
	ModelDigest   string   `json:"model_digest"`
	PipelineRunID string   `json:"pipeline_run_id"`
}

type SearchMode string

const (
	SearchModeChunks SearchMode = "chunks"
	SearchModeHybrid SearchMode = "hybrid"
	SearchModeGraph  SearchMode = "graph"
	SearchModeCode   SearchMode = "code"
)

type SearchFilters struct {
	Kind                  string   `json:"kind,omitempty"`
	Limit                 int      `json:"limit,omitempty"`
	NodeSets              []string `json:"node_sets,omitempty"`
	SessionID             string   `json:"session_id,omitempty"`
	IncludeSessionContext bool     `json:"include_session_context,omitempty"`
}

type SearchRequest struct {
	Scopes  []Scope       `json:"scopes"`
	Query   string        `json:"query"`
	Mode    SearchMode    `json:"mode,omitempty"`
	Filters SearchFilters `json:"filters,omitempty"`
	Limit   int           `json:"limit"`
}

type SearchHit struct {
	DatasetID    string         `json:"dataset_id"`
	DocumentID   string         `json:"document_id"`
	Title        string         `json:"title"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	ScoreMissing bool           `json:"-"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func (h *SearchHit) UnmarshalJSON(data []byte) error {
	type rawSearchHit struct {
		DatasetID  string         `json:"dataset_id"`
		DocumentID string         `json:"document_id"`
		Title      string         `json:"title"`
		Snippet    string         `json:"snippet"`
		Score      *float64       `json:"score"`
		Metadata   map[string]any `json:"metadata,omitempty"`
	}
	var raw rawSearchHit
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	h.DatasetID = raw.DatasetID
	h.DocumentID = raw.DocumentID
	h.Title = raw.Title
	h.Snippet = raw.Snippet
	h.Metadata = raw.Metadata
	if raw.Score == nil {
		h.Score = 0
		h.ScoreMissing = true
	} else {
		h.Score = *raw.Score
		h.ScoreMissing = false
	}
	return nil
}

type GraphSearchEdge struct {
	Text          string `json:"text"`
	Source        string `json:"source,omitempty"`
	Target        string `json:"target,omitempty"`
	Relationship  string `json:"relationship,omitempty"`
	Superseded    bool   `json:"superseded,omitempty"`
	Authoritative bool   `json:"authoritative,omitempty"`
}

type GraphSearchEntity struct {
	DatasetID string            `json:"dataset_id,omitempty"`
	Name      string            `json:"name"`
	Type      string            `json:"type,omitempty"`
	Edges     []GraphSearchEdge `json:"edges,omitempty"`
	Conflicts []string          `json:"conflicts,omitempty"`
}

type GraphSearchFact struct {
	DatasetID string `json:"dataset_id,omitempty"`
	Text      string `json:"text"`
}

type GraphSearchConflict struct {
	DatasetID string `json:"dataset_id"`
	Text      string `json:"text"`
}

type CodeSearchRelation struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

type CodeSearchFact struct {
	DatasetID string               `json:"dataset_id,omitempty"`
	Name      string               `json:"name"`
	Kind      string               `json:"kind,omitempty"`
	File      string               `json:"file,omitempty"`
	Line      int                  `json:"line,omitempty"`
	Relations []CodeSearchRelation `json:"relations,omitempty"`
}

type GraphSearchContext struct {
	Entities         []GraphSearchEntity   `json:"entities,omitempty"`
	Facts            []GraphSearchFact     `json:"facts,omitempty"`
	Conflicts        []GraphSearchConflict `json:"conflicts,omitempty"`
	SessionGuidance  string                `json:"session_guidance,omitempty"`
	CodeFacts        []CodeSearchFact      `json:"code_facts,omitempty"`
	DegradedToChunks bool                  `json:"degraded_to_chunks,omitempty"`
}

type SearchResult struct {
	OK           bool                `json:"ok"`
	Hits         []SearchHit         `json:"hits"`
	GraphContext *GraphSearchContext `json:"graph_context,omitempty"`
}

type ListRequest struct {
	Scope  Scope
	Limit  int
	Cursor string
}

type Item struct {
	ID              string         `json:"id"`
	DatasetID       string         `json:"dataset_id"`
	Kind            string         `json:"kind,omitempty"`
	Name            string         `json:"name,omitempty"`
	Label           string         `json:"label,omitempty"`
	ContentHash     string         `json:"content_hash,omitempty"`
	CreatedAt       string         `json:"created_at,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
	RawDataLocation string         `json:"raw_data_location,omitempty"`
	MimeType        string         `json:"mime_type,omitempty"`
	PipelineStatus  string         `json:"pipeline_status,omitempty"`
	Content         string         `json:"content,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type ListResult struct {
	OK         bool   `json:"ok"`
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
}

type GraphRequest struct {
	Scope  Scope
	Filter string
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

type GraphPayload struct {
	OK    bool        `json:"ok"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

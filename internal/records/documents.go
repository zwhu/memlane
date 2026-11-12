package records

import "encoding/json"

// Metadata comes from Cognee's DataDTO, including unknown/null fields. A data
// row is not automatically knowledge, processed, or a Memlane revision.
type OfficialDocument struct {
	ID          string          `json:"id"`
	Dataset     string          `json:"datasetId"`
	Name        string          `json:"name"`
	CreatedAt   *string         `json:"createdAt"`
	UpdatedAt   *string         `json:"updatedAt"`
	Extension   string          `json:"extension"`
	MIMEType    string          `json:"mimeType"`
	Label       *string         `json:"label"`
	Metadata    json.RawMessage `json:"externalMetadata"`
	Size        *int64          `json:"dataSize"`
	ContentHash *string         `json:"contentHash"`
}

// HasMore uses one upstream lookahead row. It is not a snapshot or total count.
type OfficialDocumentPage struct {
	Documents []OfficialDocument `json:"documents"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
	HasMore   bool               `json:"has_more"`
}

type OfficialDocumentText struct {
	Document OfficialDocument `json:"document"`
	Text     string           `json:"text"`
	// A format-derived reference from matching node_set and the pinned official
	// rendering template. It is not a verified session owner, QA attribution,
	// processing result or proof that a model generated this document.
	SessionReference string `json:"session_reference,omitempty"`
}

type OfficialDocumentQuery struct {
	Project        string            `json:"project"`
	Instance       string            `json:"instance"`
	Dataset        string            `json:"dataset"`
	Limit          int               `json:"limit"`
	Offset         int               `json:"offset"`
	Selected       *OfficialDocument `json:"selected,omitempty"`
	SourceDocument string            `json:"source_document,omitempty"`
}

// Preserved migration lineage, not an invented session or question attribution.
type OfficialDocumentReference struct {
	Document string `json:"document"`
	Memory   string `json:"memory"`
	Project  string `json:"project"`
	Revision int    `json:"revision"`
	Digest   string `json:"text_sha256"`
	Quote    string `json:"quote"`
}

type OfficialDocumentSource struct {
	Document  OfficialDocument          `json:"document"`
	Reference OfficialDocumentReference `json:"reference"`
	Content   OfficialDocumentText      `json:"content"`
}

// This identifies the authorized session, without inventing a QA/trace target.
// A missing/expired session does not invalidate the separately stored document.
type OfficialDocumentSession struct {
	Document OfficialDocument `json:"document"`
	Detail   SessionDetail    `json:"detail"`
}

// A known official session-learning format, read directly from Cognee. The
// marker identifies the content category; it is not a model provenance proof.
type OfficialKnowledge struct {
	Content         OfficialDocumentText `json:"content"`
	Title           string               `json:"title"`
	TitleFromBody   bool                 `json:"title_from_body"`
	ProcessingState string               `json:"processing_state"`
}

// Pagination refers to the upstream document catalog, before filtering.
// Empty items with HasMore do not mean the dataset has no session knowledge.
type OfficialKnowledgePage struct {
	Items   []OfficialKnowledge `json:"items"`
	Limit   int                 `json:"limit"`
	Offset  int                 `json:"offset"`
	HasMore bool                `json:"has_more"`
}

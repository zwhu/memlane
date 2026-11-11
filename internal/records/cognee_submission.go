package records

import "encoding/json"

// Explicit, bounded material selection. No title, body, service endpoint or
// credential is accepted from the frontend. Processing must be explicitly on.
type CogneeSubmissionRequest struct {
	Operation  string                      `json:"operation"`
	Project    string                      `json:"project"`
	Dataset    string                      `json:"dataset"`
	Sources    []MergeSource               `json:"sources"`
	Processing ProcessingTaskAuthorization `json:"processing"`
}

// Host-generated immutable body sent only to the authorized Cognee instance.
// Library distinguishes two independent Memlane libraries with matching IDs.
type CogneeSubmissionMaterial struct {
	Title      string             `json:"title"`
	Processing ProcessingSnapshot `json:"processing"`
	Library    string             `json:"library"`
	Operation  string             `json:"operation"`
	Instance   string             `json:"instance"`
	Dataset    string             `json:"dataset"`
	Project    string             `json:"project"`
	Memory     string             `json:"memory"`
	Revision   int64              `json:"revision"`
	Origin     string             `json:"origin"`
	Digest     string             `json:"digest"`
	Text       string             `json:"text"`
}

type CogneeSubmissionReceipt struct {
	ProcessingRevisionDigest   string `json:"processing_revision_digest"`
	ProcessingDependencyDigest string `json:"processing_dependency_digest"`
	Library                    string `json:"library"`
	Operation                  string `json:"operation"`
	Instance                   string `json:"instance"`
	Dataset                    string `json:"dataset"`
	Project                    string `json:"project"`
	Memory                     string `json:"memory"`
	Revision                   int64  `json:"revision"`
	Digest                     string `json:"digest"`
	Document                   string `json:"document"`
	State                      string `json:"state"` // processed only after official pipeline completion
	CogneeVersion              string `json:"cognee_version"`
}

type CogneeSubmissionTask struct {
	Sources            []CogneeSubmissionSource `json:"sources"`
	ProcessingRevision string                   `json:"processing_revision"`
	ProcessingMode     string                   `json:"processing_mode"`
	RequestedModel     string                   `json:"requested_model"`
	Executions         json.RawMessage          `json:"executions,omitempty"`
	Operation          string                   `json:"operation"`
	Project            string                   `json:"project"`
	Dataset            string                   `json:"dataset"`
	State              string                   `json:"state"`
	ErrorCode          string                   `json:"error_code,omitempty"`
	CancelRequested    bool                     `json:"cancel_requested"`
	Materials          int                      `json:"materials"`
	Completed          int                      `json:"completed"`
}

type CogneeSubmissionSource struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	Origin   string `json:"origin"`
	Title    string `json:"title"`
}

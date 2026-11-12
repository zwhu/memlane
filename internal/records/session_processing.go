package records

import "encoding/json"

// This is the observed tail and its counts, not a complete transcript archive.
// Processing may update status/activity metadata without changing that input.
func SessionProcessingSourceDigest(detail SessionDetail) string {
	return MergeDigest(struct {
		Session, User, Dataset string
		QAs                    []SessionQA
		Traces                 []SessionTrace
		QACount, ToolCount     int
	}{detail.Session, detail.User, detail.Dataset, detail.QAs, detail.Traces, detail.QACount, detail.ToolCount})
}

// A session is the processing scope, not just the last 20 displayed entries.
// ObservedDigest records the selected read snapshot; no QA/trace body is stored.
type SessionProcessingRequest struct {
	AutomaticGrant *CodingMemoryGrant          `json:"automatic_grant,omitempty"`
	Operation      string                      `json:"operation"`
	Project        string                      `json:"project"`
	Instance       string                      `json:"instance"`
	Dataset        string                      `json:"dataset"`
	Session        string                      `json:"session"`
	User           string                      `json:"user"`
	ObservedDigest string                      `json:"observed_digest"`
	Processing     ProcessingTaskAuthorization `json:"processing"`
}

type SessionProcessingStage struct {
	Stage     string         `json:"stage"`
	Status    string         `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	Counts    map[string]int `json:"counts"`
}

type SessionProcessingReceipt struct {
	Instance         string                   `json:"instance"`
	Library          string                   `json:"library"`
	Operation        string                   `json:"operation"`
	Project          string                   `json:"project"`
	Dataset          string                   `json:"dataset"`
	Session          string                   `json:"session"`
	User             string                   `json:"user"`
	CogneeVersion    string                   `json:"cognee_version"`
	RevisionDigest   string                   `json:"processing_revision_digest"`
	DependencyDigest string                   `json:"processing_dependency_digest"`
	Status           string                   `json:"status"`
	Stages           []SessionProcessingStage `json:"stages"`
}

type SessionProcessingTask struct {
	Request         SessionProcessingRequest  `json:"request"`
	Library         string                    `json:"library"`
	State           string                    `json:"state"`
	ErrorCode       string                    `json:"error_code,omitempty"`
	CancelRequested bool                      `json:"cancel_requested"`
	Receipt         *SessionProcessingReceipt `json:"receipt,omitempty"`
	Executions      json.RawMessage           `json:"executions,omitempty"`
}

// Only the daemon loads the frozen binding and processing identity. This
// contains no model capability, credential, prompt, QA body or generated text.
type SessionProcessingFrozen struct {
	Task       SessionProcessingTask `json:"task"`
	Binding    CogneeBinding         `json:"binding"`
	Processing ProcessingSnapshot    `json:"processing"`
}

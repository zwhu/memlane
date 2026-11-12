package records

import "github.com/zwhu/memlane/internal/codegraph"

type CodeAction struct {
	Official           *OfficialCodeScope `json:"official,omitempty"`
	OperationID        string             `json:"operation_id"`
	Kind               string             `json:"kind"` // enable, disable, refresh; trusted user actions only
	Project            string             `json:"project,omitempty"`
	Path               string             `json:"path,omitempty"`
	ID                 string             `json:"id,omitempty"`
	ExpectedGeneration int64              `json:"expected_generation,omitempty"`
}
type CodeState struct {
	OfficialReceipt *OfficialCodeReceipt `json:"official_receipt,omitempty"`
	Official        *OfficialCodeScope   `json:"official,omitempty"`
	Replayed        bool                 `json:"replayed,omitempty"`
	ID              string               `json:"id"`
	Project         string               `json:"project"`
	Worktree        string               `json:"worktree"`
	CommonDir       string               `json:"common_dir"`
	Dataset         string               `json:"dataset"`
	Enabled         bool                 `json:"enabled"`
	Generation      int64                `json:"generation"`
	State           string               `json:"state"`
	Snapshot        string               `json:"snapshot"`
	Manifest        *codegraph.Manifest  `json:"manifest,omitempty"`
	Receipt         *codegraph.Receipt   `json:"receipt,omitempty"`
	ObservedDigest  string               `json:"observed_digest"`
	Error           string               `json:"error"`
	UpdatedMS       int64                `json:"updated_ms"`
}

// The official graph is a separate dataset and engine, not the legacy worker
// snapshot. This tuple contains no credential or project content.
type OfficialCodeScope struct {
	Instance string `json:"instance"`
	Dataset  string `json:"dataset"`
	User     string `json:"user"`
	Endpoint string `json:"endpoint"`
}
type CodeJob struct {
	ExecutionEpoch  string               `json:"execution_epoch,omitempty"`
	Project         string               `json:"project,omitempty"`
	Replayed        bool                 `json:"replayed,omitempty"`
	Official        *OfficialCodeScope   `json:"official,omitempty"`
	OfficialReceipt *OfficialCodeReceipt `json:"official_receipt,omitempty"`
	ID              string               `json:"id"`
	WorktreeID      string               `json:"worktree_id"`
	Generation      int64                `json:"generation"`
	State           string               `json:"state"`
	Manifest        *codegraph.Manifest  `json:"manifest,omitempty"`
	Receipt         *codegraph.Receipt   `json:"receipt,omitempty"`
	Error           string               `json:"error"`
	CreatedMS       int64                `json:"created_ms"`
	FinishedMS      int64                `json:"finished_ms"`
}

// A receipt from the official backend, separate from the legacy worker graph.
// It carries identifiers/source metadata only, never source or model content.
type OfficialCodeReceipt struct {
	ExecutionEpoch string `json:"execution_epoch,omitempty"`
	Operation      string `json:"operation"`
	Instance       string `json:"instance"`
	Dataset        string `json:"dataset"`
	User           string `json:"user"`
	SourceDigest   string `json:"source_digest"`
	Status         string `json:"status"`
	PipelineRunID  string `json:"pipeline_run_id,omitempty"`
	DataID         string `json:"data_id,omitempty"`
}

// Private host-to-service grant, never page/model input or a durable receipt.
type OfficialCodeExecutionGrant struct {
	Operation      string   `json:"operation"`
	Instance       string   `json:"instance"`
	Dataset        string   `json:"dataset"`
	User           string   `json:"user"`
	SourceDigest   string   `json:"source_digest"`
	ExecutionEpoch string   `json:"execution_epoch"`
	Repository     string   `json:"repository"`
	Scratch        string   `json:"scratch"`
	Binary         string   `json:"binary"`
	BinarySHA256   string   `json:"binary_sha256"`
	DenyReads      []string `json:"deny_reads"`
}
type CodeQuery struct {
	ID    string `json:"id"`
	Mode  string `json:"mode"`
	Text  string `json:"text,omitempty"`
	Node  string `json:"node,omitempty"`
	Limit int    `json:"limit,omitempty"`
	Hops  int    `json:"hops,omitempty"`
}
type CodeResult struct {
	State CodeState        `json:"state"`
	Graph codegraph.Result `json:"graph"`
}

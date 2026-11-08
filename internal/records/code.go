package records

import "github.com/zwhu/memlane/internal/codegraph"

type CodeAction struct {
	OperationID        string `json:"operation_id"`
	Kind               string `json:"kind"` // enable, disable, refresh; trusted user actions only
	Project            string `json:"project,omitempty"`
	Path               string `json:"path,omitempty"`
	ID                 string `json:"id,omitempty"`
	ExpectedGeneration int64  `json:"expected_generation,omitempty"`
}
type CodeState struct {
	Replayed       bool                `json:"replayed,omitempty"`
	ID             string              `json:"id"`
	Project        string              `json:"project"`
	Worktree       string              `json:"worktree"`
	CommonDir      string              `json:"common_dir"`
	Dataset        string              `json:"dataset"`
	Enabled        bool                `json:"enabled"`
	Generation     int64               `json:"generation"`
	State          string              `json:"state"`
	Snapshot       string              `json:"snapshot"`
	Manifest       *codegraph.Manifest `json:"manifest,omitempty"`
	Receipt        *codegraph.Receipt  `json:"receipt,omitempty"`
	ObservedDigest string              `json:"observed_digest"`
	Error          string              `json:"error"`
	UpdatedMS      int64               `json:"updated_ms"`
}
type CodeJob struct {
	ID         string              `json:"id"`
	WorktreeID string              `json:"worktree_id"`
	Generation int64               `json:"generation"`
	State      string              `json:"state"`
	Manifest   *codegraph.Manifest `json:"manifest,omitempty"`
	Receipt    *codegraph.Receipt  `json:"receipt,omitempty"`
	Error      string              `json:"error"`
	CreatedMS  int64               `json:"created_ms"`
	FinishedMS int64               `json:"finished_ms"`
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

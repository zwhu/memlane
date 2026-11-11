package records

import "encoding/json"

// A batch freezes installation targets, never project or memory grants.
type AgentConnectionTarget struct {
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	Directory string `json:"directory"`
}

type AgentConnectionBatchStart struct {
	Identity    string                  `json:"identity"`
	OperationID string                  `json:"operation_id"`
	Targets     []AgentConnectionTarget `json:"targets"`
}

type AgentConnectionBatchItem struct {
	Target                      AgentConnectionTarget `json:"target"`
	ConnectionID                string                `json:"connection_id"`
	Generation                  int64                 `json:"generation"`
	State                       string                `json:"state"`
	Error                       string                `json:"error,omitempty"`
	Attempt                     int                   `json:"attempt"`
	Phase                       string                `json:"phase,omitempty"`
	Existing                    bool                  `json:"existing,omitempty"`
	Verification                json.RawMessage       `json:"verification,omitempty"`
	VerificationWorkspace       string                `json:"verification_workspace,omitempty"`
	VerificationProject         string                `json:"verification_project,omitempty"`
	VerificationWorkspaceDigest string                `json:"verification_workspace_digest,omitempty"`
}

type AgentConnectionBatch struct {
	Identity      string                     `json:"identity"`
	ID            string                     `json:"id"`
	OperationID   string                     `json:"operation_id"`
	Revision      int64                      `json:"revision"`
	State         string                     `json:"state"`
	CreatedAt     string                     `json:"created_at"`
	UpdatedAt     string                     `json:"updated_at"`
	Items         []AgentConnectionBatchItem `json:"items"`
	Actions       map[string]string          `json:"actions"`
	ActionDigests map[string]string          `json:"action_digests,omitempty"`
}

type AgentConnectionBatchAction struct {
	Identity    string `json:"identity"`
	BatchID     string `json:"batch_id"`
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`                // verify, retry, cancel
	Workspace   string `json:"workspace,omitempty"` // explicitly selected; daemon resolves registered authorized project
}

type AgentConnectionBatchState struct {
	Identity string                `json:"identity"`
	Batch    *AgentConnectionBatch `json:"batch,omitempty"`
}

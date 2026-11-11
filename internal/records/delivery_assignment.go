package records

// This is an independent App user's action, never a replay or success receipt
// for the retained Agent operation. No body is accepted in its RPC input.
type DeliveryAssignmentPreviewQuery struct {
	EntryID      string `json:"entry_id"`
	ReviewDigest string `json:"review_digest"`
	Project      string `json:"project,omitempty"`
}
type DeliveryAssignmentIntent struct {
	OperationID     string `json:"operation_id"`
	LibraryID       string `json:"library_id"`
	EntryID         string `json:"entry_id"`
	RequestDigest   string `json:"request_digest"`
	ReviewDigest    string `json:"review_digest"`
	Project         string `json:"project"`
	WorkspaceDigest string `json:"workspace_digest"`
}

// Only constructed from an owned spool read/claim, never decoded from RPC.
type DeliveryAssignmentSource struct {
	Review            AgentDeliveryReview `json:"review"`
	WorkspacePhysical string              `json:"workspace_physical"`
}
type DeliveryAssignmentPreview struct {
	LibraryID        string                     `json:"library_id"`
	EntryID          string                     `json:"entry_id"`
	RequestDigest    string                     `json:"request_digest"`
	ReviewDigest     string                     `json:"review_digest"`
	Workspace        string                     `json:"workspace"`
	WorkspaceDigest  string                     `json:"workspace_digest"`
	Item             AgentDeliveryItem          `json:"item"`
	Arguments        AgentArguments             `json:"arguments"`
	Projects         []Scope                    `json:"projects"`
	MatchingProject  *Scope                     `json:"matching_project,omitempty"`
	SelectedProject  string                     `json:"selected_project,omitempty"`
	CanConfirm       bool                       `json:"can_confirm"`
	Code             string                     `json:"code,omitempty"`
	OriginalMemory   string                     `json:"original_memory,omitempty"`
	OriginalRevision int64                      `json:"original_revision,omitempty"`
	Assignment       *DeliveryAssignmentReceipt `json:"assignment,omitempty"`
}
type DeliveryAssignmentReceipt struct {
	OperationID          string `json:"operation_id"`
	LibraryID            string `json:"library_id"`
	EntryID              string `json:"entry_id"`
	RequestDigest        string `json:"request_digest"`
	ReviewDigest         string `json:"review_digest"`
	Project              string `json:"project"`
	WorkspaceDigest      string `json:"workspace_digest"`
	Memory               string `json:"memory"`
	Revision             int64  `json:"revision"`
	OriginalOperationID  string `json:"original_operation_id"`
	OriginalConnectionID string `json:"original_connection_id"`
	Source               string `json:"source"`
	State                string `json:"state"`
	CreatedMS            int64  `json:"created_ms"`
	SpoolAckPending      bool   `json:"spool_ack_pending,omitempty"`
}

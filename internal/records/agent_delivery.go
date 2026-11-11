package records

// A SameUser daemon observation, not an independent write permission. No TTL
// is invented: each send rechecks current generation/scope/workspace/library.
type AgentDeliveryGrant struct {
	WorkspacePhysical string `json:"workspace_physical"`
	Agent             string `json:"agent"`
	LibraryID         string `json:"library_id"`
	ConnectionID      string `json:"connection_id"`
	Generation        int64  `json:"generation"`
	Scope             string `json:"scope"`
	Workspace         string `json:"workspace"`
	WorkspaceDigest   string `json:"workspace_digest"`
	IssuedMS          int64  `json:"issued_ms"`
}
type AgentDeliveryStatus struct {
	ReviewDigest       string `json:"review_digest"`
	ID                 string `json:"id"`
	RequestDigest      string `json:"request_digest"`
	State              string `json:"state"`
	LocalRetained      bool   `json:"local_retained"`
	PersistedConfirmed bool   `json:"persisted_confirmed"`
	SavedRevision      int64  `json:"saved_revision,omitempty"`
	Memory             string `json:"memory,omitempty"`
	Code               string `json:"code,omitempty"`
}

// Local adapter retention is independent of persisted knowledge and indexes.
// No original body is returned by the summary list.
type AgentDeliveryItem struct {
	Assignment   *DeliveryAssignmentSpoolStatus `json:"assignment,omitempty"`
	Agent        string                         `json:"agent,omitempty"`
	Attempts     int                            `json:"attempts,omitempty"`
	Status       AgentDeliveryStatus            `json:"status"`
	OperationID  string                         `json:"operation_id"`
	Title        string                         `json:"title,omitempty"`
	Origin       string                         `json:"origin,omitempty"`
	Scope        string                         `json:"scope,omitempty"`
	ConnectionID string                         `json:"connection_id"`
	Generation   int64                          `json:"generation"`
	Workspace    string                         `json:"workspace"`
	LibraryID    string                         `json:"library_id,omitempty"`
	HasGrant     bool                           `json:"has_grant"`
	CreatedMS    int64                          `json:"created_ms,omitempty"`
	UpdatedMS    int64                          `json:"updated_ms,omitempty"`
}
type AgentDeliveryPage struct {
	Items []AgentDeliveryItem `json:"items"`
	Limit int                 `json:"limit"`
}
type AgentDeliveryReview struct {
	Item      AgentDeliveryItem `json:"item"`
	Arguments AgentArguments    `json:"arguments"`
}

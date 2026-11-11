package records

// A user's independent assignment, not the original Agent save receipt.
type DeliveryAssignmentSpoolStatus struct {
	OperationID string `json:"operation_id"`
	LibraryID   string `json:"library_id"`
	Project     string `json:"project"`
	State       string `json:"state"`
	Memory      string `json:"memory,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
}

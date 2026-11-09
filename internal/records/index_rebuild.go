package records

type IndexRebuildStart struct {
	OperationID string `json:"operation_id"`
	Scope       string `json:"scope,omitempty"`
	ProjectTask string `json:"project_task,omitempty"`
	Attempt     int64  `json:"project_attempt,omitempty"`
}

type IndexRebuildItem struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

type IndexRebuild struct {
	ID          string             `json:"id"`
	Scope       string             `json:"scope,omitempty"`
	CreatedAt   string             `json:"created_at"`
	State       string             `json:"state"`
	Items       []IndexRebuildItem `json:"items"`
	ProjectTask string             `json:"project_task,omitempty"`
	Attempt     int64              `json:"project_attempt,omitempty"`
}

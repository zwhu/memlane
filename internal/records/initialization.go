package records

type InitializationStart struct {
	OperationID  string   `json:"operation_id"`
	Project      string   `json:"project"`
	ConnectionID string   `json:"connection_id"`
	Generation   int64    `json:"generation"`
	Workspace    string   `json:"workspace"`
	Paths        []string `json:"paths"`
}
type InitializationSource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
}
type InitializationQuote struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Quote  string `json:"quote"`
}
type InitializationItem struct {
	ID      string                `json:"id"`
	Title   string                `json:"title"`
	Text    string                `json:"text"`
	Sources []InitializationQuote `json:"sources"`
}
type InitializationCommit struct {
	Batch       string               `json:"batch"`
	OperationID string               `json:"operation_id"`
	Items       []InitializationItem `json:"items"`
}
type InitializationSaved struct {
	ID       string `json:"id"`
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
}
type InitializationReceipt struct {
	ID        string                `json:"id"`
	Batch     string                `json:"batch"`
	Persisted bool                  `json:"persisted"`
	Items     []InitializationSaved `json:"items"`
}
type InitializationBatch struct {
	ID           string                 `json:"batch"`
	Project      string                 `json:"project"`
	ConnectionID string                 `json:"connection_id"`
	Generation   int64                  `json:"generation"`
	Workspace    string                 `json:"workspace"`
	WorkspaceID  string                 `json:"workspace_identity"`
	State        string                 `json:"state"`
	Error        string                 `json:"error,omitempty"`
	CreatedMS    int64                  `json:"created_ms"`
	SourceReadMS int64                  `json:"source_read_ms,omitempty"`
	Sources      []InitializationSource `json:"sources,omitempty"`
	Receipt      *InitializationReceipt `json:"receipt,omitempty"`
	Saved        int                    `json:"saved"`
	Indexed      int                    `json:"indexed"`
	IndexFailed  int                    `json:"index_failed"`
	Changed      int                    `json:"changed"`
}

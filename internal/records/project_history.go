package records

// Host-frozen local Agent history. It is not an official Cognee instance,
// dataset, graph trace or automatic knowledge writeback receipt.
type ProjectHistoryOriginal struct {
	Agent      string `json:"agent"`
	Session    string `json:"session"`
	Workspace  string `json:"workspace"`
	CLIVersion string `json:"cli_version"`
	CapturedAt string `json:"captured_at"`
	Digest     string `json:"digest"`
}

type ProjectHistoryQuery struct {
	Project         string `json:"project"`
	BindingRevision int64  `json:"binding_revision"`
	After           string `json:"after,omitempty"`
}

type ProjectHistoryChoice struct {
	ProjectSessionSelection
	CreatedAt string `json:"created_at"`
	State     string `json:"state"`
}

type ProjectHistoryPage struct {
	WorkspaceID  string                 `json:"workspace_identity"`
	Items        []ProjectHistoryChoice `json:"items"`
	Next         string                 `json:"next,omitempty"`
	Total        int                    `json:"total"`
	Unattributed int                    `json:"unattributed"`
}

type ProjectHistoryContextReceipt struct {
	Selected int    `json:"selected"`
	Read     int    `json:"read"`
	Sent     int    `json:"sent"`
	Bytes    int64  `json:"bytes"`
	Digest   string `json:"digest"`
}

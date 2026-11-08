package records

// CogneeImport is a trusted-host import of an actual official API result. It
// is not a daily Agent tool and cannot update existing knowledge. The host
// binds the approved workspace/connection; external records never choose them.
type CogneeImport struct {
	ConnectionID string        `json:"connection_id"`
	Generation   int64         `json:"generation"`
	Workspace    string        `json:"workspace"`
	Project      string        `json:"project"`
	ExternalID   string        `json:"external_id"`
	Origin       string        `json:"origin"`
	Title        string        `json:"title"`
	Text         string        `json:"text"`
	Fragment     string        `json:"fragment"`
	Sources      []MergeSource `json:"sources,omitempty"`
}

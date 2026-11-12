package records

// CodingMemoryGrant is explicit permission to process future official runs in
// one dataset. Neither a connection nor a read grant implies this permission.
// Rebinding or reconnecting never revives the old generation.
type CodingMemoryGrant struct {
	Binding    CogneeBinding `json:"binding"`
	User       string        `json:"user"`
	Generation int64         `json:"generation"`
	Enabled    bool          `json:"enabled"`
	ChangedMS  int64         `json:"changed_ms"`
}

type CodingMemoryGrantChange struct {
	Operation          string        `json:"operation"`
	Binding            CogneeBinding `json:"binding"`
	User               string        `json:"user"`
	ExpectedGeneration int64         `json:"expected_generation"`
	Enabled            bool          `json:"enabled"`
}

// The service reports authenticated scope, not project/model authorization.
// Daemon resolves the project, current grant, source digest and model itself.
type CodingMemoryTrigger struct {
	Operation string `json:"operation"`
	Instance  string `json:"instance"`
	Dataset   string `json:"dataset"`
	User      string `json:"user"`
	Session   string `json:"session"`
}

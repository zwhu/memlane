package agents

// CompletionObservation is filled at the actual runner gates. It contains no
// model content, credential, path, session ID or provider diagnostics.
type CompletionObservation struct {
	ResponseComplete    bool   `json:"response_complete"`
	LocalSchemaValid    bool   `json:"local_schema_valid"`
	IsolationConfigured bool   `json:"isolation_configured"`
	IsolationPolicy     string `json:"isolation_policy,omitempty"`
}

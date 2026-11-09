package records

// A project organizer is an explicit preference, separate from a connection's
// primary project and from the globally selected memory-processing provider.
type ProjectBinding struct {
	Project      string `json:"project"`
	ConnectionID string `json:"connection_id,omitempty"`
	Generation   int64  `json:"generation,omitempty"`
	Revision     int64  `json:"revision"`
	Agent        string `json:"agent,omitempty"`
	State        string `json:"state"`
}

type ProjectBindingChange struct {
	Project            string `json:"project"`
	ConnectionID       string `json:"connection_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	ExpectedRevision   int64  `json:"expected_revision"`
}

// History selection carries identity and a frozen content digest. Filesystem
// paths and agent/project attribution are resolved by the host, not the model.
type ProjectSessionSelection struct {
	ID     string `json:"session"`
	Digest string `json:"digest"`
}

type ProjectSessionSource struct {
	ProjectSessionSelection
	Project string `json:"project"`
	Agent   string `json:"agent"`
}

type ProjectTaskStart struct {
	OperationID     string                    `json:"operation_id"`
	Project         string                    `json:"project"`
	BindingRevision int64                     `json:"binding_revision"`
	IncludeHistory  bool                      `json:"include_history"`
	Sessions        []ProjectSessionSelection `json:"sessions"`
}

// Resolved by the task owner from the connected Agent's current configuration.
// Neither a UI request nor a model response chooses a fallback processing mode.
type ProjectTaskExecutor struct {
	ConnectionID        string `json:"connection_id"`
	Generation          int64  `json:"generation"`
	Agent               string `json:"agent"`
	Version             string `json:"version"`
	Model               string `json:"model"`
	ConfigurationDigest string `json:"configuration_digest"`
}

type ProjectTaskSnapshot struct {
	Project         string                 `json:"project"`
	BindingRevision int64                  `json:"binding_revision"`
	Workspace       string                 `json:"workspace"`
	WorkspaceID     string                 `json:"workspace_identity"`
	Executor        ProjectTaskExecutor    `json:"executor"`
	IncludeHistory  bool                   `json:"include_history"`
	Sessions        []ProjectSessionSource `json:"sessions"`
}

type ProjectTask struct {
	ID          string              `json:"task"`
	Attempt     int64               `json:"attempt"`
	State       string              `json:"state"`
	Cancelled   bool                `json:"cancel_requested"`
	Error       string              `json:"error_code,omitempty"`
	Snapshot    ProjectTaskSnapshot `json:"snapshot"`
	CreatedMS   int64               `json:"created_ms"`
	AccessState string              `json:"access_state"`
}

type ProjectTaskSummary struct {
	ID        string `json:"task"`
	Project   string `json:"project"`
	State     string `json:"state"`
	Attempt   int64  `json:"attempt"`
	CreatedMS int64  `json:"created_ms"`
}

type ProjectTaskPage struct {
	Items []ProjectTaskSummary `json:"items"`
	Next  string               `json:"next,omitempty"`
}

// A repository source is read and frozen by the host. The model only cites its
// opaque ID and exact quote; it cannot submit arbitrary source bytes or paths.
type ProjectRepositorySource struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
}
type ProjectKnowledgeQuote struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}
type ProjectKnowledgeItem struct {
	Title   string                  `json:"title"`
	Text    string                  `json:"text"`
	Sources []ProjectKnowledgeQuote `json:"sources"`
}
type ProjectKnowledgeProposal struct {
	Task        string                 `json:"task"`
	Attempt     int64                  `json:"attempt"`
	Items       []ProjectKnowledgeItem `json:"items"`
	Explanation string                 `json:"explanation"`
}

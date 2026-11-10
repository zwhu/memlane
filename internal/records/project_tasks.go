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
	OperationID        string                    `json:"operation_id"`
	Project            string                    `json:"project"`
	BindingRevision    int64                     `json:"binding_revision"`
	IncludeHistory     bool                      `json:"include_history"`
	HistoryWorkspaceID string                    `json:"history_workspace_identity,omitempty"`
	Sessions           []ProjectSessionSelection `json:"sessions"`
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
	ID            string                `json:"task"`
	Attempt       int64                 `json:"attempt"`
	State         string                `json:"state"`
	Cancelled     bool                  `json:"cancel_requested"`
	Error         string                `json:"error_code,omitempty"`
	Snapshot      ProjectTaskSnapshot   `json:"snapshot"`
	CreatedMS     int64                 `json:"created_ms"`
	AccessState   string                `json:"access_state"`
	Receipt       *ProjectTaskReceipt   `json:"receipt,omitempty"`
	Execution     *ProjectTaskExecution `json:"execution,omitempty"`
	IndexRecovery *IndexRebuild         `json:"index_recovery,omitempty"`
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
	ID      string                  `json:"id"`
	Path    string                  `json:"path"`
	SHA256  string                  `json:"sha256"`
	Text    string                  `json:"text"`
	History *ProjectHistoryOriginal `json:"history,omitempty"`
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

// Current project knowledge is context for avoiding duplicate additions. It is
// not a repository source, and this reader grants no mutation authorization.
type ProjectKnowledgeContextItem struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	Title    string `json:"title"`
	Text     string `json:"text"`
}
type ProjectKnowledgeContextPage struct {
	Items []ProjectKnowledgeContextItem `json:"items"`
	Next  string                        `json:"next,omitempty"`
	Total int                           `json:"total"`
}
type ProjectKnowledgeContextReceipt struct {
	Pages     int    `json:"pages"`
	SentPages int    `json:"sent_pages"`
	Items     int    `json:"items"`
	Bytes     int64  `json:"bytes"`
	Complete  bool   `json:"complete"`
	Digest    string `json:"digest"`
}

// Only the daemon can seal process receipts. An exit code is not a model
// terminal event, and restored PIDs are historical data rather than owners.
type ProjectTaskExecution struct {
	HistoryContext        *ProjectHistoryContextReceipt   `json:"history_context,omitempty"`
	KnowledgeContext      *ProjectKnowledgeContextReceipt `json:"knowledge_context,omitempty"`
	PID                   int                             `json:"pid"`
	StartedMS             int64                           `json:"started_ms"`
	EndedMS               int64                           `json:"ended_ms"`
	Terminal              bool                            `json:"terminal_completed"`
	Reaped                bool                            `json:"process_reaped"`
	GroupGone             bool                            `json:"process_group_gone"`
	Thread                string                          `json:"thread,omitempty"`
	Turn                  string                          `json:"turn,omitempty"`
	Model                 string                          `json:"model,omitempty"`
	Provider              string                          `json:"provider,omitempty"`
	PermissionProfile     string                          `json:"permission_profile,omitempty"`
	InterruptSent         bool                            `json:"interrupt_sent"`
	InterruptAcknowledged bool                            `json:"interrupt_acknowledged"`
}
type ProjectTaskSaved struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
}
type ProjectTaskReceipt struct {
	Task              string             `json:"task"`
	Attempt           int64              `json:"attempt"`
	Persisted         bool               `json:"persisted"`
	Added             []ProjectTaskSaved `json:"added"`
	Sources           []ProjectTaskSaved `json:"sources"`
	Skipped           int                `json:"skipped"`
	SaveFailed        int                `json:"save_failed"`
	Indexed           int                `json:"indexed"`
	IndexFailed       int                `json:"index_failed"`
	SourceIndexed     int                `json:"source_indexed"`
	SourceIndexFailed int                `json:"source_index_failed"`
}

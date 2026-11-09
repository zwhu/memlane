package records

// AgentSource contains only content and references supplied by the Agent.
type AgentSource struct {
	Native     *CodexOriginal  `json:"native,omitempty"`
	Official   *CogneeOriginal `json:"official,omitempty"`
	Fragment   string          `json:"fragment,omitempty"`
	References []string        `json:"references,omitempty"`
}

type Connection struct {
	AutoProject       bool     `json:"auto_project,omitempty"`
	PendingProject    string   `json:"pending_project,omitempty"`
	TrustedWorkspace  string   `json:"trusted_workspace,omitempty"`
	TrustedRepository string   `json:"trusted_repository,omitempty"`
	ID                string   `json:"id"`
	Agent             string   `json:"agent"`
	Name              string   `json:"name"`
	Project           string   `json:"project,omitempty"`
	AllowedScopes     []string `json:"allowed_scopes"`
	Enabled           bool     `json:"enabled"`
	Generation        int64    `json:"generation"`
	LastSeen          string   `json:"last_seen,omitempty"`
	ContextProject    string   `json:"context_project,omitempty"` // resolved per request, not persisted
}

type ConnectionDraft struct {
	AutoProject   bool     `json:"auto_project,omitempty"`
	Workspace     string   `json:"workspace,omitempty"`
	ID            string   `json:"id,omitempty"`
	Agent         string   `json:"agent"`
	Name          string   `json:"name"`
	Project       string   `json:"project,omitempty"`
	AllowedScopes []string `json:"allowed_scopes"`
}

// ConnectionScopeChange changes one project grant without replacing the
// connection's other scopes, identity or trusted workspace.
type ConnectionScopeChange struct {
	ConnectionID       string `json:"connection_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	Project            string `json:"project"`
	Allowed            bool   `json:"allowed"`
}

// Workspace comes from an explicit bridge binding or pi ctx.cwd, never a
// model tool argument or the MCP process's current working directory.
type AgentRequest struct {
	ToolMode     string         `json:"tool_mode,omitempty"`
	CallID       string         `json:"call_id,omitempty"`
	Channel      string         `json:"usage_channel,omitempty"`
	Protocol     int            `json:"protocol_version"`
	ConnectionID string         `json:"connection_id"`
	Generation   int64          `json:"generation"`
	Workspace    string         `json:"workspace,omitempty"`
	Tool         string         `json:"tool"`
	Arguments    AgentArguments `json:"arguments"`
}

type AgentArguments struct {
	CodeMode            string               `json:"mode,omitempty"`
	CodeNode            string               `json:"node,omitempty"`
	CodeHops            int                  `json:"hops,omitempty"`
	InitializationBatch string               `json:"batch_id,omitempty"`
	InitializationItems []InitializationItem `json:"items,omitempty"`
	Exposure            string               `json:"exposure_receipt,omitempty"`
	Revision            int64                `json:"revision,omitempty"`
	OperationID         string               `json:"operation_id,omitempty"`
	Scope               string               `json:"scope,omitempty"`
	Scopes              []string             `json:"scopes,omitempty"`
	Memory              string               `json:"memory_id,omitempty"`
	Query               string               `json:"query,omitempty"`
	Limit               int                  `json:"limit,omitempty"`
	Title               string               `json:"title,omitempty"`
	Text                string               `json:"text,omitempty"`
	Origin              string               `json:"origin,omitempty"`
	Source              *AgentSource         `json:"source,omitempty"`
}

type SaveReceipt struct {
	Persisted     bool    `json:"persisted"`
	SavedRevision int64   `json:"saved_revision"`
	Record        Version `json:"record"`
}

type AccessError struct{ Code, Message string }

func (e *AccessError) Error() string { return e.Message }

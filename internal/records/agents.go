package records

// AgentSource contains only content and references supplied by the Agent.
type AgentSource struct {
	Fragment   string   `json:"fragment,omitempty"`
	References []string `json:"references,omitempty"`
}

type Connection struct {
	ID             string   `json:"id"`
	Agent          string   `json:"agent"`
	Name           string   `json:"name"`
	Project        string   `json:"project,omitempty"`
	AllowedScopes  []string `json:"allowed_scopes"`
	Enabled        bool     `json:"enabled"`
	Generation     int64    `json:"generation"`
	LastSeen       string   `json:"last_seen,omitempty"`
	ContextProject string   `json:"context_project,omitempty"` // resolved per request, not persisted
}

type ConnectionDraft struct {
	ID            string   `json:"id,omitempty"`
	Agent         string   `json:"agent"`
	Name          string   `json:"name"`
	Project       string   `json:"project,omitempty"`
	AllowedScopes []string `json:"allowed_scopes"`
}

// Workspace comes from an explicit bridge binding or pi ctx.cwd, never a
// model tool argument or the MCP process's current working directory.
type AgentRequest struct {
	Protocol     int            `json:"protocol_version"`
	ConnectionID string         `json:"connection_id"`
	Generation   int64          `json:"generation"`
	Workspace    string         `json:"workspace,omitempty"`
	Tool         string         `json:"tool"`
	Arguments    AgentArguments `json:"arguments"`
}

type AgentArguments struct {
	OperationID string       `json:"operation_id,omitempty"`
	Scope       string       `json:"scope,omitempty"`
	Scopes      []string     `json:"scopes,omitempty"`
	Memory      string       `json:"memory_id,omitempty"`
	Query       string       `json:"query,omitempty"`
	Limit       int          `json:"limit,omitempty"`
	Title       string       `json:"title,omitempty"`
	Text        string       `json:"text,omitempty"`
	Origin      string       `json:"origin,omitempty"`
	Source      *AgentSource `json:"source,omitempty"`
}

type SaveReceipt struct {
	Persisted     bool    `json:"persisted"`
	SavedRevision int64   `json:"saved_revision"`
	Record        Version `json:"record"`
}

type AccessError struct{ Code, Message string }

func (e *AccessError) Error() string { return e.Message }

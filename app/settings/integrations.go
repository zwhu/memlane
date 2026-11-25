package settings

type ConnectionCapability struct {
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Condition string `json:"condition"`
}

type AgentHookError struct {
	At          float64  `json:"at"`
	Script      string   `json:"script"`
	ErrorType   string   `json:"error_type"`
	Reason      string   `json:"reason"`
	PayloadKeys []string `json:"payload_keys,omitempty"`
}

type AgentConnection struct {
	NeedsHookRepair            bool                            `json:"needs_hook_repair"`
	RepairReason               string                          `json:"repair_reason"`
	RepairSummary              string                          `json:"repair_summary"`
	HostVersion                string                          `json:"host_version,omitempty"`
	Capture                    *AgentCaptureState              `json:"capture,omitempty"`
	LastHookError              *AgentHookError                 `json:"last_hook_error,omitempty"`
	ID                         string                          `json:"instance_id"`
	Kind                       string                          `json:"kind"`
	Status                     string                          `json:"status"`
	Detected                   bool                            `json:"detected"`
	AgentRegistered            bool                            `json:"agent_registered"`
	NewSessions                int                             `json:"new_sessions"`
	ErrorCode                  string                          `json:"error_code"`
	Preview                    *CodexConnectionPreview         `json:"preview"`
	Capabilities               map[string]ConnectionCapability `json:"capabilities"`
	Profile                    string                          `json:"profile"`
	EnvironmentIssue           string                          `json:"environment_issue"`
	Environments               []AgentEnvironment              `json:"environments"`
	ReceiptOverdue             bool                            `json:"receipt_overdue"`
	LastReceiptAt              float64                         `json:"last_receipt_at"`
	PluginVersion              string                          `json:"plugin_version"`
	EnvironmentVerified        bool                            `json:"environment_verified"`
	AwaitingPostUpgradeReceipt bool                            `json:"awaiting_post_upgrade_receipt,omitempty"`
}

type AgentCaptureState struct {
	ProjectID   string  `json:"project_id"`
	State       string  `json:"state"`
	Permission  string  `json:"permission"`
	ReviewSHA   string  `json:"review_sha"`
	Reason      string  `json:"reason"`
	LastSavedAt float64 `json:"last_saved_at"`
	CheckedAt   float64 `json:"checked_at"`
}

type AgentEnvironment struct {
	Profile      string `json:"profile"`
	Source       string `json:"source"`
	Exists       bool   `json:"exists"`
	Verification bool   `json:"verification"`
}

type ConnectionsDirectory struct {
	Revision    int64             `json:"revision"`
	Connections []AgentConnection `json:"connections"`
}

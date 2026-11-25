package controlplane

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
	NeedsHookRepair            bool                            `json:"needs_hook_repair,omitempty"`
	RepairReason               string                          `json:"repair_reason,omitempty"`
	RepairSummary              string                          `json:"repair_summary,omitempty"`
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
type GenerationRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type GenerationProfile struct {
	Mode                 string   `json:"mode"`
	Provider             string   `json:"provider,omitempty"`
	BridgeKind           string   `json:"bridge_kind,omitempty"`
	BridgeProvider       string   `json:"bridge_provider,omitempty"`
	Model                string   `json:"model"`
	Endpoint             string   `json:"endpoint,omitempty"`
	Temperature          *float64 `json:"temperature,omitempty"`
	MaxOutputTokens      *int     `json:"max_output_tokens,omitempty"`
	CredentialVersion    string   `json:"credential_version,omitempty"`
	CredentialConfigured bool     `json:"credential_configured,omitempty"`
}
type GenerationRow struct {
	Revision int64             `json:"revision"`
	Digest   string            `json:"digest"`
	Profile  GenerationProfile `json:"profile"`
}
type GenerationValidation struct {
	Phase              string              `json:"phase,omitempty"`
	FailedPhase        string              `json:"failed_phase,omitempty"`
	DiagnosticCode     string              `json:"diagnostic_code,omitempty"`
	DiagnosticStage    string              `json:"diagnostic_stage,omitempty"`
	NetworkError       string              `json:"network_error,omitempty"`
	BridgeErrorCode    string              `json:"bridge_error_code,omitempty"`
	HTTPStatus         int                 `json:"http_status,omitempty"`
	ProcessExitCode    *int                `json:"process_exit_code,omitempty"`
	ElapsedMs          float64             `json:"elapsed_ms,omitempty"`
	DeadlineSeconds    int                 `json:"deadline_seconds,omitempty"`
	StartedAt          float64             `json:"started_at,omitempty"`
	AgentDefaults      *AgentModelDefaults `json:"agent_defaults,omitempty"`
	Reference          *GenerationRef      `json:"reference,omitempty"`
	Status             string              `json:"status"`
	Digest             string              `json:"digest"`
	ErrorCode          string              `json:"error_code"`
	Plain              bool                `json:"plain_passed"`
	Schema             bool                `json:"schema_passed"`
	Quality            bool                `json:"quality_passed"`
	RegisteredRevision int64               `json:"registered_revision"`
	StructuredMode     string              `json:"structured_mode"`
	ModeEvidence       string              `json:"structured_mode_evidence"`
}
type AgentModelDefaults struct {
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	Source              string `json:"source,omitempty"`
	ConfigurationSource string `json:"configuration_source,omitempty"`
	ReasoningEffort     string `json:"reasoning_effort,omitempty"`
	ThinkingLevel       string `json:"thinking_level,omitempty"`
	ServiceTier         string `json:"service_tier,omitempty"`
}
type GenerationDirectory struct {
	AgentDefaults    map[string]AgentModelDefaults   `json:"agent_defaults"`
	AgentBridges     map[string]GenerationProfile    `json:"agent_bridges"`
	HandoffAvailable bool                            `json:"handoff_available"`
	Revision         int64                           `json:"revision"`
	Active           *GenerationRef                  `json:"active"`
	Next             *GenerationRef                  `json:"next"`
	Profiles         map[string][]GenerationRow      `json:"profiles"`
	Validations      map[string]GenerationValidation `json:"validations"`
	NextError        string                          `json:"next_error"`
	StartupError     string                          `json:"startup_error"`
}

type CodexConnectionPreview struct {
	BaselineSHA256  string   `json:"baseline_sha256,omitempty"`
	ProposedSHA     string   `json:"proposed_sha256"`
	DisabledPlugins []string `json:"disabled_plugins"`
	EnabledPlugin   string   `json:"enabled_plugin"`
}

type ControlCapabilities struct {
	APIVersions     []string `json:"api_versions"`
	ReadingContract string   `json:"reading_contract"`
	Features        []string `json:"features"`
}

type MonitoredProcess struct {
	PID          int      `json:"pid"`
	Alive        *bool    `json:"alive"`
	RSSBytes     *int64   `json:"rss_bytes"`
	CPUPercent   *float64 `json:"cpu_percent"`
	ChildCount   int      `json:"child_count,omitempty"`
	SelfRSSBytes *int64   `json:"self_rss_bytes,omitempty"`
}

type DiskMonitorState struct {
	FreeBytes        int64 `json:"free_bytes"`
	MinimumFreeBytes int64 `json:"minimum_free_bytes"`
	StopFreeBytes    int64 `json:"stop_free_bytes"`
	Low              bool  `json:"low"`
}

type MonitorHookError struct {
	InstanceID string  `json:"instance_id"`
	Kind       string  `json:"kind"`
	At         float64 `json:"at"`
	Script     string  `json:"script"`
	ErrorType  string  `json:"error_type"`
	Reason     string  `json:"reason"`
}

type MonitorRecallQueryStats struct {
	StartedAt    string   `json:"started_at,omitempty"`
	TotalQueries int      `json:"total_queries"`
	HitQueries   int      `json:"hit_queries"`
	EmptyQueries int      `json:"empty_queries"`
	TotalHits    int      `json:"total_hits"`
	HitRate      *float64 `json:"hit_rate,omitempty"`
}

type MonitorBusinessSummary struct {
	ProjectsCount             int                      `json:"projects_count"`
	GenerationMode            string                   `json:"generation_mode"`
	GenerationReceiptVerified *bool                    `json:"generation_receipt_verified"`
	RecentProcessingMs        *float64                 `json:"recent_processing_ms"`
	HookErrorsCount           int                      `json:"hook_errors_count"`
	HookErrors                []MonitorHookError       `json:"hook_errors,omitempty"`
	BacklogFilesCount         int                      `json:"backlog_files_count,omitempty"`
	LargestGraphDBBytes       int64                    `json:"largest_graph_db_bytes,omitempty"`
	RecallQueries             *MonitorRecallQueryStats `json:"recall_queries,omitempty"`
}

type ServiceMonitorSnapshot struct {
	State               string                      `json:"state"`
	CheckedAt           float64                     `json:"checked_at"`
	LastOKAt            float64                     `json:"last_ok_at"`
	LatencyMs           *float64                    `json:"latency_ms"`
	ConsecutiveFailures int                         `json:"consecutive_failures"`
	LastError           string                      `json:"last_error"`
	ServiceURL          string                      `json:"service_url"`
	OwnerPID            int                         `json:"owner_pid"`
	OwnerPhase          string                      `json:"owner_phase"`
	Processes           map[string]MonitoredProcess `json:"processes"`
	Disk                *DiskMonitorState           `json:"disk"`
	Summary             *MonitorBusinessSummary     `json:"summary"`
	LogTail             []string                    `json:"log_tail"`
}

type ConnectionActionResult struct {
	Revision    int64           `json:"revision"`
	Connection  AgentConnection `json:"connection"`
	OperationID string          `json:"operation_id,omitempty"`
	Outcome     string          `json:"outcome,omitempty"`
	Phase       string          `json:"phase,omitempty"`
}

type BlockerDetail struct {
	Code   string `json:"code"`
	Status int    `json:"status,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

type ConnectionPreview struct {
	Revision             int64                   `json:"revision"`
	PlanDigest           string                  `json:"plan_digest,omitempty"`
	OK                   bool                    `json:"ok,omitempty"`
	InstanceID           string                  `json:"instance_id,omitempty"`
	Kind                 string                  `json:"kind,omitempty"`
	Status               string                  `json:"status,omitempty"`
	Target               string                  `json:"target,omitempty"`
	BaselineSHA256       string                  `json:"baseline_sha256,omitempty"`
	ReviewedSHA          string                  `json:"reviewed_sha,omitempty"`
	ProposedSHA256       string                  `json:"proposed_sha256,omitempty"`
	DisabledPlugins      []string                `json:"disabled_plugins,omitempty"`
	EnabledPlugin        string                  `json:"enabled_plugin,omitempty"`
	Summary              string                  `json:"summary,omitempty"`
	DiffSummary          string                  `json:"diff_summary,omitempty"`
	Blockers             []string                `json:"blockers,omitempty"`
	BlockerDetails       []BlockerDetail         `json:"blocker_details,omitempty"`
	PreserveOtherPlugins *bool                   `json:"preserve_other_plugins,omitempty"`
	RequiresReload       *bool                   `json:"requires_reload,omitempty"`
	RequiresHostTrust    *bool                   `json:"requires_host_trust,omitempty"`
	NeedsHookRepair      bool                    `json:"needs_hook_repair,omitempty"`
	RepairReason         string                  `json:"repair_reason,omitempty"`
	RepairSummary        string                  `json:"repair_summary,omitempty"`
	BackupPlan           string                  `json:"backup_plan,omitempty"`
	Preview              *CodexConnectionPreview `json:"preview,omitempty"`
	Connection           *AgentConnection        `json:"connection,omitempty"`
}

func (p ConnectionPreview) EffectiveProposedSHA() string {
	if p.ProposedSHA256 != "" {
		return p.ProposedSHA256
	}
	if p.Preview != nil && p.Preview.ProposedSHA != "" {
		return p.Preview.ProposedSHA
	}
	if p.Connection != nil && p.Connection.Preview != nil && p.Connection.Preview.ProposedSHA != "" {
		return p.Connection.Preview.ProposedSHA
	}
	return p.ReviewedSHA
}

type ConnectionOperationReceipt struct {
	Recorded        bool                    `json:"recorded"`
	OperationID     string                  `json:"operation_id,omitempty"`
	Revision        int64                   `json:"revision,omitempty"`
	InstanceID      string                  `json:"instance_id,omitempty"`
	Action          string                  `json:"action,omitempty"`
	Status          string                  `json:"status,omitempty"`
	Outcome         string                  `json:"outcome,omitempty"`
	Phase           string                  `json:"phase,omitempty"`
	ErrorCode       string                  `json:"error_code,omitempty"`
	Reconciled      bool                    `json:"reconciled,omitempty"`
	Reconcilable    bool                    `json:"reconcilable,omitempty"`
	AlreadyComplete bool                    `json:"already_complete,omitempty"`
	SafeToUnlock    bool                    `json:"safe_to_unlock,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	Result          *ConnectionActionResult `json:"result,omitempty"`
}

type TraeHooksRepairResult struct {
	Revision                  int64            `json:"revision"`
	OperationID               string           `json:"operation_id,omitempty"`
	Repaired                  bool             `json:"repaired"`
	Mode                      string           `json:"mode,omitempty"`
	BackupPath                string           `json:"backup_path,omitempty"`
	RemovedEvents             []string         `json:"removed_events,omitempty"`
	RetainedEvents            []string         `json:"retained_events,omitempty"`
	UpdatedEvents             []string         `json:"updated_events,omitempty"`
	PreservedThirdPartyGroups int              `json:"preserved_third_party_groups,omitempty"`
	BackupPlan                string           `json:"backup_plan,omitempty"`
	RollForwardArtifact       bool             `json:"roll_forward_artifact,omitempty"`
	NeedsHookRepair           bool             `json:"needs_hook_repair,omitempty"`
	Repairable                bool             `json:"repairable,omitempty"`
	RepairReason              string           `json:"repair_reason,omitempty"`
	RepairSummary             string           `json:"repair_summary,omitempty"`
	BaselineSHA256            string           `json:"baseline_sha256,omitempty"`
	Blockers                  []BlockerDetail  `json:"blockers,omitempty"`
	ReviewedSHA               string           `json:"reviewed_sha,omitempty"`
	ProposedSHA256            string           `json:"proposed_sha256,omitempty"`
	Summary                   string           `json:"summary,omitempty"`
	Connection                *AgentConnection `json:"connection,omitempty"`
}

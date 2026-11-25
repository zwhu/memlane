package setupcli

import "github.com/zwhu/memlane/internal/bootstrap"

const (
	ExitOK               = 0
	ExitActionRequired   = 10
	ExitHumanRequired    = 11
	ExitInProgressOrBusy = 20
	ExitPreflightBlocked = 30
	ExitNeedsReview      = 40
	ExitFatal            = 50
	ExitInvalidArguments = 64

	diskMinimum = 2 << 30
	retryMillis = 10000
)

var statuses = map[int]string{
	ExitOK: "ok", ExitActionRequired: "action_required", ExitHumanRequired: "human_required",
	ExitInProgressOrBusy: "in_progress_or_busy", ExitPreflightBlocked: "preflight_blocked",
	ExitNeedsReview: "needs_review", ExitFatal: "fatal", ExitInvalidArguments: "invalid_arguments",
}

const agentGuidance = "Execute read-only checks first. Show preview and ask user confirmation before any requires=explicit_confirmation action. Never read or print tokens, API keys, .local/ paths, hooks.json raw contents, or memory data; never run xattr or edit host config files directly. On exit 20 follow next_actions after retry_after_ms (poll doctor --operation-id while active; retry install after confirmation on lock contention). Stop after 3 retries (4th failure). On exit 11 hand human_steps to user."

const unconfiguredProcessingHintFormat = "本机已检测到可用于后台记忆提炼的 %s，但尚未完成登录或未配置默认模型。请先在终端运行对应 CLI 完成登录与默认模型配置，或先进入 Memlane 稍后在设置中改用直连模型 API。"

const processingHint = "本机尚未检测到可用于后台记忆提炼的 CLI（如 codex、traex、pi、agy），也未配置直连模型 API。对话自动采集仍可先接入，但把对话自动整理成记忆需要安装提炼 CLI 或在桌面端设置中配置直连模型 API。"

type envelope struct {
	SchemaVersion int                `json:"schema_version"`
	Command       string             `json:"command"`
	Mode          string             `json:"mode"`
	OK            bool               `json:"ok"`
	Status        string             `json:"status"`
	ExitCode      int                `json:"exit_code"`
	OperationID   string             `json:"operation_id"`
	RetryAfterMS  int                `json:"retry_after_ms"`
	App           appSummary         `json:"app"`
	Environment   environmentSummary `json:"environment"`
	Components    componentSummary   `json:"components"`
	Daemon        daemonSummary      `json:"daemon"`
	Connections   connectionSummary  `json:"connections"`
	Generation    generationSummary  `json:"generation"`
	Blockers      []blocker          `json:"blockers"`
	NextActions   []nextAction       `json:"next_actions"`
	HumanSteps    []string           `json:"human_steps"`
	Artifacts     artifacts          `json:"artifacts"`
	Capabilities  *manifest          `json:"capabilities"`
	AgentGuidance string             `json:"agent_guidance"`
}

type appSummary struct {
	Version            string `json:"version"`
	Executable         string `json:"executable"`
	BundledRuntimeHash string `json:"bundled_runtime_hash"`
	Platform           string `json:"platform"`
}

type environmentSummary struct {
	ConfigPath     string   `json:"config_path"`
	ComponentRoot  string   `json:"component_root"`
	StoreMode      string   `json:"store_mode"`
	DownloadSource string   `json:"download_source"`
	SelectedAgents []string `json:"selected_agents"`
	DiskFreeBytes  uint64   `json:"disk_free_bytes"`
	DiskMinBytes   uint64   `json:"disk_min_bytes"`
	DNSOK          *bool    `json:"dns_ok"`
}

type componentSummary struct {
	Complete     bool            `json:"complete"`
	PlanDigest   string          `json:"plan_digest"`
	WorkerActive bool            `json:"worker_active"`
	Progress     *workerProgress `json:"progress"`
	Items        []componentItem `json:"items"`
}

type componentItem struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	DownloadBytes  int64  `json:"download_bytes"`
	InstalledBytes int64  `json:"installed_bytes"`
}

type workerProgress struct {
	State                  string `json:"state"`
	Stage                  string `json:"stage"`
	Component              string `json:"component"`
	ErrorCode              string `json:"error_code"`
	SafeMessage            string `json:"safe_message"`
	Mirror                 string `json:"mirror"`
	BytesDone              int64  `json:"bytes_done"`
	BytesTotal             int64  `json:"bytes_total"`
	Indeterminate          bool   `json:"indeterminate"`
	ElapsedMS              int64  `json:"elapsed_ms"`
	FallbackAttempt        int    `json:"fallback_attempt,omitempty"`
	FallbackTotal          int    `json:"fallback_total,omitempty"`
	NetworkBytesDownloaded int64  `json:"network_bytes_downloaded,omitempty"`
	RuntimeSyncRan         bool   `json:"runtime_sync_ran,omitempty"`
}

type daemonSummary struct {
	State                string            `json:"state"`
	OwnerLocked          bool              `json:"owner_locked"`
	OwnerFingerprint     string            `json:"owner_fingerprint"`
	RunningRuntimeHash   string            `json:"running_runtime_hash"`
	StaleVersionDetected bool              `json:"stale_version_detected"`
	GraphAssetsStale     bool              `json:"graph_assets_stale"`
	DeveloperCheckout    bool              `json:"developer_checkout"`
	Endpoints            map[string]string `json:"endpoints"`
}

type connectionSummary struct {
	Revision int64              `json:"revision"`
	Pending  []operationSummary `json:"pending_ui_operations"`
	Hosts    []hostSummary      `json:"hosts"`
}

type hostSummary struct {
	ID                         string                `json:"instance_id,omitempty"`
	Kind                       string                `json:"kind"`
	Status                     string                `json:"status,omitempty"`
	Detected                   bool                  `json:"detected"`
	NeedsHookRepair            bool                  `json:"needs_hook_repair"`
	AwaitingPostUpgradeReceipt bool                  `json:"awaiting_post_upgrade_receipt"`
	Capabilities               map[string]capability `json:"capabilities"`
}

type capability struct {
	Available bool `json:"available"`
	Verified  bool `json:"verified"`
}

type operationSummary struct {
	ID                        string `json:"operation_id"`
	Kind                      string `json:"kind,omitempty"`
	InstanceID                string `json:"instance_id,omitempty"`
	Revision                  int64  `json:"revision"`
	SafeToUnlock              bool   `json:"safe_to_unlock,omitempty"`
	AlreadyComplete           bool   `json:"already_complete,omitempty"`
	Reconciled                bool   `json:"reconciled,omitempty"`
	Recorded                  bool   `json:"recorded,omitempty"`
	Reconcilable              bool   `json:"reconcilable,omitempty"`
	Phase                     string `json:"phase,omitempty"`
	Status                    string `json:"status,omitempty"`
	PreservedThirdPartyGroups int    `json:"preserved_third_party_groups,omitempty"`
}

type generationSummary struct {
	DetectedUnconfiguredCLIs  []string                             `json:"detected_unconfigured_clis,omitempty"`
	InitializationComplete    bool                                 `json:"initialization_complete"`
	Deferred                  bool                                 `json:"deferred"`
	Active                    *generationReference                 `json:"active"`
	AvailableBridges          []string                             `json:"available_bridges"`
	MissingProcessingCLIHint  string                               `json:"missing_processing_cli_hint"`
	RecommendedProcessingCLIs []bootstrap.RecommendedProcessingCLI `json:"recommended_processing_clis"`
}

type generationReference struct {
	Revision int64 `json:"revision"`
}

type blocker struct {
	Code   string `json:"code"`
	Status string `json:"status"`
}

type nextAction struct {
	Args     []string `json:"args"`
	Requires string   `json:"requires"`
}

type artifacts struct {
	PlanDigest          string             `json:"plan_digest"`
	ReviewedSHA         string             `json:"reviewed_sha"`
	OwnerFingerprint    string             `json:"owner_fingerprint"`
	OperationID         string             `json:"operation_id"`
	LogTail             []string           `json:"log_tail,omitempty"`
	ConnectionOperation *operationSummary  `json:"connection_operation,omitempty"`
	HookRepair          *hookRepairSummary `json:"hook_repair,omitempty"`
}

type hookRepairSummary struct {
	NeedsHookRepair           bool     `json:"needs_hook_repair"`
	Repairable                bool     `json:"repairable"`
	Repaired                  bool     `json:"repaired"`
	RemovedEvents             []string `json:"removed_events"`
	RetainedEvents            []string `json:"retained_events"`
	PreservedThirdPartyGroups int      `json:"preserved_third_party_groups"`
}

func newEnvelope(command, mode string) envelope {
	return envelope{
		SchemaVersion: 1, Command: command, Mode: mode, AgentGuidance: agentGuidance,
		Environment: environmentSummary{StoreMode: "uninitialized", DownloadSource: "china", DiskMinBytes: diskMinimum, SelectedAgents: []string{}},
		Components:  componentSummary{Items: []componentItem{}},
		Daemon:      daemonSummary{State: "unknown", Endpoints: map[string]string{}},
		Connections: connectionSummary{Pending: []operationSummary{}, Hosts: []hostSummary{}},
		Generation:  generationSummary{AvailableBridges: []string{}, RecommendedProcessingCLIs: []bootstrap.RecommendedProcessingCLI{}},
		Blockers:    []blocker{}, NextActions: []nextAction{}, HumanSteps: []string{},
	}
}

var exitPriority = map[int]int{
	ExitOK: 0, ExitHumanRequired: 1, ExitActionRequired: 2, ExitPreflightBlocked: 3,
	ExitInProgressOrBusy: 4, ExitNeedsReview: 5, ExitFatal: 6, ExitInvalidArguments: 7,
}

func (e *envelope) block(code int, reason string) {
	e.Blockers = append(e.Blockers, blocker{Code: reason, Status: statuses[code]})
	if exitPriority[code] > exitPriority[e.ExitCode] {
		e.ExitCode = code
	}
}

func (e *envelope) busy(reason string) {
	e.block(ExitInProgressOrBusy, reason)
	e.RetryAfterMS = retryMillis
}

func (e *envelope) operation(id string) {
	e.OperationID = id
	e.Artifacts.OperationID = id
}

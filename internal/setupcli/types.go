package setupcli

import (
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/kit/aiinstaller/cli"
)

const (
	ExitOK               = cli.ExitOK
	ExitActionRequired   = cli.ExitActionRequired
	ExitHumanRequired    = cli.ExitHumanRequired
	ExitInProgressOrBusy = cli.ExitInProgressOrBusy
	ExitPreflightBlocked = cli.ExitPreflightBlocked
	ExitNeedsReview      = cli.ExitNeedsReview
	ExitFatal            = cli.ExitFatal
	ExitInvalidArguments = cli.ExitInvalidArguments

	diskMinimum = 2 << 30
	retryMillis = cli.DefaultRetryMillis
)

var statuses = cli.StatusMap()

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

type appSummary = cli.AppSummary

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
	NeedsHookTrust             bool                  `json:"needs_hook_trust,omitempty"`
	UntrustedHookCount         int                   `json:"untrusted_hook_count,omitempty"`
	ModifiedHookCount          int                   `json:"modified_hook_count,omitempty"`
	DisabledHookCount          int                   `json:"disabled_hook_count,omitempty"`
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

type blocker = cli.Blocker
type nextAction = cli.NextAction

type artifacts struct {
	PlanDigest          string                 `json:"plan_digest"`
	ReviewedSHA         string                 `json:"reviewed_sha"`
	OwnerFingerprint    string                 `json:"owner_fingerprint"`
	OperationID         string                 `json:"operation_id"`
	LogTail             []string               `json:"log_tail,omitempty"`
	ConnectionOperation *operationSummary      `json:"connection_operation,omitempty"`
	HookRepair          *hookRepairSummary     `json:"hook_repair,omitempty"`
	UpgradeCleanup      *upgradeCleanupSummary `json:"upgrade_cleanup,omitempty"`
}

type upgradeCleanupSummary struct {
	NeedsCleanup       bool     `json:"needs_cleanup"`
	Applied            bool     `json:"applied"`
	LegacyPycCount     int      `json:"legacy_pyc_count"`
	LegacyPycacheDirs  int      `json:"legacy_pycache_dirs"`
	RetiredRuntimeDirs []string `json:"retired_runtime_dirs"`
	RemovedPycCount    int      `json:"removed_pyc_count,omitempty"`
	RemovedPycacheDirs int      `json:"removed_pycache_dirs,omitempty"`
	RemovedRuntimeDirs []string `json:"removed_runtime_dirs,omitempty"`
	InstallItems       []string `json:"install_items,omitempty"`
	CleanupItems       []string `json:"cleanup_items,omitempty"`
	PreservedItems     []string `json:"preserved_items,omitempty"`
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
	common := cli.NewEnvelope(command, mode, agentGuidance)
	return envelope{
		SchemaVersion: common.SchemaVersion,
		Command:       common.Command,
		Mode:          common.Mode,
		AgentGuidance: common.AgentGuidance,
		Environment:   environmentSummary{StoreMode: "uninitialized", DownloadSource: "china", DiskMinBytes: diskMinimum, SelectedAgents: []string{}},
		Components:    componentSummary{Items: []componentItem{}},
		Daemon:        daemonSummary{State: "unknown", Endpoints: map[string]string{}},
		Connections:   connectionSummary{Pending: []operationSummary{}, Hosts: []hostSummary{}},
		Generation:    generationSummary{AvailableBridges: []string{}, RecommendedProcessingCLIs: []bootstrap.RecommendedProcessingCLI{}},
		Blockers:      common.Blockers,
		NextActions:   common.NextActions,
		HumanSteps:    common.HumanSteps,
	}
}

func (e *envelope) block(code int, reason string) {
	common := cli.Envelope{Blockers: e.Blockers, ExitCode: e.ExitCode}
	common.Block(code, reason)
	e.Blockers, e.ExitCode = common.Blockers, common.ExitCode
}

func (e *envelope) busy(reason string) {
	common := cli.Envelope{Blockers: e.Blockers, ExitCode: e.ExitCode}
	common.Busy(reason)
	e.Blockers, e.ExitCode, e.RetryAfterMS = common.Blockers, common.ExitCode, common.RetryAfterMS
}

func (e *envelope) operation(id string) {
	common := cli.Envelope{}
	common.SetOperation(id)
	e.OperationID = common.OperationID
	e.Artifacts.OperationID = id
}

func (e *envelope) finalize() {
	common := cli.Envelope{ExitCode: e.ExitCode}
	common.Finalize()
	e.OK, e.Status = common.OK, common.Status
}

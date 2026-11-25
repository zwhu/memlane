package api

import "encoding/json"

type GenerationState struct {
	ActiveProfileID string `json:"active_profile_id"`
	ActiveRevision  int    `json:"active_revision"`
	NextProfileID   string `json:"next_profile_id"`
	NextRevision    int    `json:"next_revision"`
	Revision        int    `json:"revision"`
	StartupError    string `json:"startup_error"`
}

type GenerationProfile struct {
	ProfileID             string   `json:"profile_id,omitempty"`
	Revision              int      `json:"revision,omitempty"`
	Digest                string   `json:"digest,omitempty"`
	Mode                  string   `json:"mode"`
	Provider              string   `json:"provider,omitempty"`
	BridgeKind            string   `json:"bridge_kind,omitempty"`
	BridgeProvider        string   `json:"bridge_provider,omitempty"`
	Model                 string   `json:"model"`
	Endpoint              string   `json:"endpoint,omitempty"`
	CredentialRef         string   `json:"credential_ref,omitempty"`
	CredentialVersion     string   `json:"credential_version,omitempty"`
	Temperature           *float64 `json:"temperature,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxConcurrentRequests *int     `json:"max_concurrent_requests,omitempty"`
	Removed               bool     `json:"removed,omitempty"`
	CreatedAt             string   `json:"created_at,omitempty"`
	CredentialConfigured  bool     `json:"credential_configured,omitempty"`
	HasCredential         bool     `json:"has_credential,omitempty"`
}

type SaveGenerationProfileRequest struct {
	GenerationProfile
	APIKey *string `json:"api_key,omitempty"`
}

type ModelStatus struct {
	OK             bool   `json:"ok"`
	Mode           string `json:"mode"`
	ModelDigest    string `json:"model_digest"`
	OutputLanguage string `json:"output_language"`
	ActiveTasks    int    `json:"active_tasks"`
	Error          string `json:"error,omitempty"`
}

type GenerationOverviewResponse struct {
	State                  GenerationState                 `json:"state"`
	Profiles               []GenerationProfile             `json:"profiles"`
	ModelStatus            *ModelStatus                    `json:"model_status"`
	Validations            map[string]GenerationValidation `json:"validations"`
	HandoffAvailable       bool                            `json:"handoff_available"`
	DetectedProcessingCLIs []string                        `json:"detected_processing_clis"`
	OutputLanguage         string                          `json:"output_language"`
}

type ProfileSelection struct {
	ProfileID string `json:"profile_id"`
	Revision  int    `json:"revision"`
}

type ProbeResult struct {
	Status         string `json:"status"`
	PlainPassed    bool   `json:"plain_passed"`
	SchemaPassed   bool   `json:"schema_passed"`
	QualityPassed  bool   `json:"quality_passed"`
	StructuredMode string `json:"structured_mode"`
	ErrorCode      string `json:"error_code,omitempty"`
	AdapterCalls   int    `json:"adapter_calls,omitempty"`
	OperationID    string `json:"operation_id"`
}

type ActivateProfileResponse struct {
	OK              bool   `json:"ok"`
	Applied         bool   `json:"applied"`
	ActiveCommitted bool   `json:"active_committed"`
	OperationID     string `json:"operation_id"`
	ProfileID       string `json:"profile_id"`
	Revision        int    `json:"revision"`
	ModelDigest     string `json:"model_digest"`
}

type DetectedHost struct {
	Kind                  string `json:"kind"`
	DisplayName           string `json:"display_name"`
	Detected              bool   `json:"detected"`
	Executable            string `json:"executable,omitempty"`
	Application           string `json:"application,omitempty"`
	Version               string `json:"version,omitempty"`
	ProfileDir            string `json:"profile_dir,omitempty"`
	ProfileFound          bool   `json:"profile_found"`
	SupportsCapture       bool   `json:"supports_capture"`
	SupportsProcessingCLI bool   `json:"supports_processing_cli"`
	BundlesProcessingCLI  bool   `json:"bundles_processing_cli,omitempty"`
	InstallCommand        string `json:"install_command,omitempty"`
	DocsURL               string `json:"docs_url,omitempty"`
}

type AgentView struct {
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	Name          string      `json:"name"`
	PrincipalID   string      `json:"principal_id"`
	DefaultAccess AccessLevel `json:"default_access"`
	Archived      bool        `json:"archived"`
	RetiredAt     string      `json:"retired_at,omitempty"`
}

type ListAgentsResponse struct {
	DetectedHosts    []DetectedHost `json:"detected_hosts"`
	RegisteredAgents []AgentView    `json:"registered_agents"`
}

type ConnectAgentRequest struct {
	ID               string `json:"id"`
	AgentID          string `json:"agent_id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	PrincipalID      string `json:"principal_id"`
	KeyFile          string `json:"key_file"`
	ControlTokenFile string `json:"control_token_file"`
	HostProfile      string `json:"host_profile"`
}

type ConflictFinding struct {
	Kind    string `json:"host_kind"`
	Surface string `json:"surface"`
	Name    string `json:"entry_name"`
	Path    string `json:"config_path"`
	Action  string `json:"action"`
	Blocked bool   `json:"blocked"`
}

type AgentConflictsResponse struct {
	ManagedLegacy       []ConflictFinding `json:"managed_legacy"`
	ThirdPartyConflicts []ConflictFinding `json:"third_party_conflicts"`
	UnrelatedCount      int               `json:"unrelated_count"`
	Blocked             bool              `json:"blocked"`
}

type ProfileDocumentRef struct {
	DatasetID string `json:"dataset_id"`
	DataID    string `json:"data_id"`
}

type UserProfileView struct {
	Source      string              `json:"source"`
	InstanceID  string              `json:"instance_id"`
	Name        string              `json:"name"`
	Kind        string              `json:"kind"`
	Status      string              `json:"status"`
	CanGenerate bool                `json:"can_generate"`
	OperationID string              `json:"operation_id,omitempty"`
	Method      string              `json:"method,omitempty"`
	Document    *ProfileDocumentRef `json:"document,omitempty"`
	Previous    *ProfileDocumentRef `json:"previous,omitempty"`
	ContentHash string              `json:"content_hash,omitempty"`
	StartedAt   float64             `json:"started_at,omitempty"`
	UpdatedAt   float64             `json:"updated_at,omitempty"`
	Progress    json.RawMessage     `json:"progress"`
	Extra       json.RawMessage     `json:"extra"`
	ErrorCode   string              `json:"error_code,omitempty"`
	Body        string              `json:"body,omitempty"`
}

type ListUserProfilesResponse struct {
	Profiles []UserProfileView `json:"profiles"`
	Prompt   string            `json:"prompt"`
}

type ProfileImportResponse struct {
	OperationID string `json:"operation_id"`
	Replayed    bool   `json:"replayed"`
}

type ProfileCancelResponse struct {
	CancelRequested bool `json:"cancel_requested"`
}

type ProfileDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

type GenerationValidation struct {
	ProbeResult
	Digest             string `json:"digest"`
	RegisteredRevision int64  `json:"registered_revision"`
	Reference          struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	} `json:"reference"`
}

package settings

import (
	"net/http"

	"github.com/zwhu/memlane/internal/controlplane"
)

type ProjectControlConfig struct {
	URL                 string `json:"url"`
	RegistryFile        string `json:"registry_file"`
	ManagementTokenFile string `json:"management_token_file"`
	PluginDir           string `json:"plugin_dir"`
}

type ProjectPolicy struct {
	Access      string `json:"access"`
	Capture     bool   `json:"capture"`
	AutoProcess bool   `json:"auto_process"`
}

type ProjectOverride struct {
	Access      *string `json:"access,omitempty"`
	Capture     *bool   `json:"capture,omitempty"`
	AutoProcess *bool   `json:"auto_process,omitempty"`
}

func (o ProjectOverride) Effective(defaults ProjectPolicy) ProjectPolicy {
	if o.Access != nil {
		defaults.Access = *o.Access
	}
	if o.Capture != nil {
		defaults.Capture = *o.Capture
	}
	if o.AutoProcess != nil {
		defaults.AutoProcess = *o.AutoProcess
	}
	return defaults
}

type AccessApplied struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

type AccessProject struct {
	AutoCapture          *bool                      `json:"auto_capture,omitempty"`
	AutoCaptureVersion   int64                      `json:"auto_capture_version,omitempty"`
	AutoCaptureUpdatedAt string                     `json:"auto_capture_updated_at,omitempty"`
	ToolGraph            ToolGraphSettings          `json:"tool_graph"`
	ID                   string                     `json:"id"`
	Name                 string                     `json:"name"`
	Aliases              []string                   `json:"aliases"`
	Dataset              string                     `json:"dataset_id"`
	DatasetName          string                     `json:"dataset_name,omitempty"`
	Protected            bool                       `json:"protected,omitempty"`
	Status               string                     `json:"status"`
	DesiredRevision      int64                      `json:"desired_revision"`
	Overrides            map[string]ProjectOverride `json:"overrides"`
	Applied              map[string]AccessApplied   `json:"applied"`
	CodeEnabled          bool                       `json:"code_enabled"`
	CodeConfigured       bool                       `json:"code_configured"`
	CodeDataset          string                     `json:"code_dataset_id"`
}

func (p AccessProject) IsProtected() bool {
	const profiles = "ml-user-profiles"
	return p.Protected || p.Name == profiles || p.DatasetName == profiles || p.ID == profiles || p.Dataset == profiles
}

func (p AccessProject) SyncLabel(agents ...AccessAgent) string {
	if p.Status == "transition" {
		return "同步中"
	}
	if !p.PermissionsReady(agents...) {
		return "权限未确认，自动操作暂缓"
	}
	return "已生效"
}

func (p AccessProject) PermissionsReady(agents ...AccessAgent) bool {
	if p.Status != "verified" || p.Dataset == "" {
		return false
	}
	for _, agent := range agents {
		if applied, ok := p.Applied[agent.ID]; !ok || applied.Status != "verified" || applied.Revision != p.DesiredRevision {
			return false
		}
	}
	for _, applied := range p.Applied {
		if applied.Status != "verified" || applied.Revision != p.DesiredRevision {
			return false
		}
	}
	return true
}

func (p AccessProject) PolicySyncUnconfirmed() bool {
	return p.Status == "unconfirmed" || p.Status == "transition"
}

func (p AccessProject) AgentPolicyUnconfirmed(agentID string) bool {
	if p.PolicySyncUnconfirmed() {
		return true
	}
	if applied, ok := p.Applied[agentID]; ok && applied.Status != "" && applied.Status != "verified" {
		return true
	}
	return false
}

type AccessAgent struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	Installation *AgentInstallation `json:"installation"`
}

type AgentInstallation struct {
	HostKind       string `json:"host_kind"`
	BindingFile    string `json:"binding_file"`
	BindingSHA     string `json:"binding_sha256"`
	NodeExecutable string `json:"node_executable"`
	NodeSHA        string `json:"node_sha256"`

	PluginDir       string `json:"plugin_dir"`
	MarketplaceRoot string `json:"marketplace_root"`
	MarketplaceName string `json:"marketplace_name"`
	EnvFile         string `json:"env_file"`
	Home            string `json:"home"`
	HelperSHA       string `json:"helper_sha256"`
	ManifestSHA     string `json:"manifest_sha256"`
	Status          string `json:"status"`
}

type ProjectBinding struct {
	AgentID        string `json:"agent_id"`
	ProjectID      string `json:"project_id"`
	HostSessionKey string `json:"host_session_key"`
}

type AccessDirectory struct {
	ToolGraph       ToolGraphSettings `json:"tool_graph"`
	Revision        int64             `json:"revision"`
	Defaults        ProjectPolicy     `json:"defaults"`
	Agents          []AccessAgent     `json:"agents"`
	Projects        []AccessProject   `json:"projects"`
	Common          *AccessProject    `json:"common"`
	Bindings        []ProjectBinding  `json:"bindings"`
	CodexConnection *CodexConnection  `json:"codex_connection"`
}

type CodexConnection struct {
	InstanceID             string   `json:"instance_id"`
	Home                   string   `json:"codex_home"`
	Status                 string   `json:"status"`
	AgentRegistered        bool     `json:"agent_registered"`
	NewSessions            int      `json:"new_sessions"`
	ProjectConfigConflicts []string `json:"project_config_conflicts"`
	LegacyConsumers        []struct {
		PID    int    `json:"pid"`
		Script string `json:"script"`
	} `json:"legacy_consumers"`
	Preview *CodexConnectionPreview `json:"preview"`
}

type CodexConnectionPreview struct {
	BaselineSHA256  string   `json:"baseline_sha256,omitempty"`
	ProposedSHA     string   `json:"proposed_sha256"`
	DisabledPlugins []string `json:"disabled_plugins"`
	EnabledPlugin   string   `json:"enabled_plugin"`
}

type ProjectControlClient struct {
	Config ProjectControlConfig
	HTTP   *http.Client
}

func (c ProjectControlClient) controlPlaneClient() *controlplane.Client {
	return &controlplane.Client{Config: controlplane.Config{
		URL: c.Config.URL, RegistryFile: c.Config.RegistryFile,
		ManagementTokenFile: c.Config.ManagementTokenFile, PluginDir: c.Config.PluginDir,
	}, HTTP: c.HTTP}
}

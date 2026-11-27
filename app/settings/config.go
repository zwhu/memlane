package settings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Dataset     string `json:"dataset_id"`
	CodeDataset string `json:"code_dataset_id"`
	CodeEnabled bool   `json:"code_enabled"`
}

type Config struct {
	CodexExecutable string `json:"codex_executable"`
	AgentHosts      map[string]struct {
		Executable string `json:"executable"`
	} `json:"agent_hosts"`
	FirstInstall           *bootstrap.FirstInstallation `json:"first_install,omitempty"`
	FirstInstallDefault    bool                         `json:"-"`
	Python                 string                       `json:"python"`
	ProjectControl         *ProjectControlConfig        `json:"project_control"`
	ClientStateFile        string                       `json:"client_state_file"`
	OfficialData           string                       `json:"official_data"`
	OfficialSource         string                       `json:"official_source"`
	OfficialArchive        string                       `json:"official_archive"`
	ServiceEnvironmentFile string                       `json:"service_environment_file"`
	ServiceCommit          string                       `json:"service_commit"`
	GraphAssets            string                       `json:"graph_assets"`
	ConfigFile             string                       `json:"-"`
	ServiceURL             string                       `json:"service_url"`
	TokenFile              string                       `json:"token_file"`
	CompletionURL          string                       `json:"completion_url"`
	Model                  string                       `json:"model"`
	GenerationMode         string                       `json:"generation_mode"`
	GenerationFile         string                       `json:"generation_file"`
	GenerationSetupOnly    bool                         `json:"generation_setup_only"`
	Projects               []Project                    `json:"projects"`
	Common                 []Project                    `json:"common_datasets"`
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func LoadConfig(path string) (Config, error) {
	var c Config
	c.ConfigFile = path
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, e
	}
	hasControl := false
	if c.ProjectControl != nil {
		_, controlErr := controlplane.ValidateLoopbackOrigin(c.ProjectControl.URL)
		hasControl = controlErr == nil
	}
	if !hasControl && !validHTTPURL(c.ServiceURL) {
		return c, fmt.Errorf("project_control.url or service_url required")
	}
	return c, nil
}

type Client struct {
	Config         Config
	HTTP           *http.Client
	PolicyRevision string
}

func (c Client) controlClient() (ProjectControlClient, bool) {
	if c.Config.ProjectControl == nil || strings.TrimSpace(c.Config.ProjectControl.URL) == "" {
		return ProjectControlClient{}, false
	}
	return ProjectControlClient{Config: *c.Config.ProjectControl, HTTP: c.HTTP}, true
}

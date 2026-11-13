package simpledesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
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

func (o ProjectOverride) effective(defaults ProjectPolicy) ProjectPolicy {
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
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Aliases         []string                   `json:"aliases"`
	Dataset         string                     `json:"dataset_id"`
	Status          string                     `json:"status"`
	DesiredRevision int64                      `json:"desired_revision"`
	Overrides       map[string]ProjectOverride `json:"overrides"`
	Applied         map[string]AccessApplied   `json:"applied"`
	CodeEnabled     bool                       `json:"code_enabled"`
	CodeConfigured  bool                       `json:"code_configured"`
	CodeDataset     string                     `json:"code_dataset_id"`
}

func (p AccessProject) syncLabel(agents ...AccessAgent) string {
	if p.Status == "transition" {
		return "同步中"
	}
	if !p.permissionsReady(agents...) {
		return "权限未确认，自动操作暂缓"
	}
	return "已生效"
}

func (p AccessProject) permissionsReady(agents ...AccessAgent) bool {
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

type AccessAgent struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Installation *AgentInstallation `json:"installation"`
}

type AgentInstallation struct {
	PluginDir   string `json:"plugin_dir"`
	EnvFile     string `json:"env_file"`
	Home        string `json:"home"`
	HelperSHA   string `json:"helper_sha256"`
	ManifestSHA string `json:"manifest_sha256"`
	Status      string `json:"status"`
}
type ProjectBinding struct {
	AgentID        string `json:"agent_id"`
	ProjectID      string `json:"project_id"`
	HostSessionKey string `json:"host_session_key"`
}

type AccessDirectory struct {
	Revision int64            `json:"revision"`
	Defaults ProjectPolicy    `json:"defaults"`
	Agents   []AccessAgent    `json:"agents"`
	Projects []AccessProject  `json:"projects"`
	Common   *AccessProject   `json:"common"`
	Bindings []ProjectBinding `json:"bindings"`
}

type ProjectControlClient struct {
	Config ProjectControlConfig
	HTTP   *http.Client
}

func (c ProjectControlClient) request(ctx context.Context, method, path string, input, output any) error {
	u, err := url.Parse(c.Config.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("项目管理服务尚未配置")
	}
	info, err := os.Lstat(c.Config.ManagementTokenFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("项目管理连接凭据不可用")
	}
	key, err := os.ReadFile(c.Config.ManagementTokenFile)
	if err != nil || len(strings.TrimSpace(string(key))) < 32 {
		return fmt.Errorf("项目管理连接凭据不可用")
	}
	var body io.Reader
	if input != nil {
		data, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return marshalErr
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	req.Header.Set("Content-Type", "application/json")
	hc := http.Client{Timeout: 125 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := hc.Do(req)
	if err != nil {
		if method != "GET" {
			return fmt.Errorf("设置结果未确认，请刷新核对原操作；不会自动重复提交")
		}
		return fmt.Errorf("项目目录暂时不可用，保留已载入项目")
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		return fmt.Errorf("设置已变化或关联存在冲突，请刷新核对后再保存")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("项目管理请求未确认（HTTP %d），请核对原操作", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 || json.Unmarshal(data, output) != nil {
		return fmt.Errorf("项目管理回执未确认，请刷新核对")
	}
	return nil
}

func (c ProjectControlClient) directory(ctx context.Context) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "GET", "/v1/projects", nil, &result)
	return result, err
}

func (c ProjectControlClient) defaults(ctx context.Context, revision int64, policy ProjectPolicy) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "PUT", "/v1/defaults", map[string]any{"expected_revision": revision, "fields": policy}, &result)
	return result, err
}

func (c ProjectControlClient) overrides(ctx context.Context, revision int64, project, agent string, fields map[string]any) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(project)+"/overrides", map[string]any{"expected_revision": revision, "agent_id": agent, "fields": fields}, &result)
	return result, err
}

func (c ProjectControlClient) create(ctx context.Context, revision int64, name, workspace, dataset string) (AccessProject, error) {
	var result AccessProject
	input := map[string]any{"expected_revision": revision, "name": name, "workspace": workspace}
	if dataset != "" {
		input["dataset_id"] = dataset
	}
	err := c.request(ctx, "POST", "/v1/projects", input, &result)
	return result, err
}

func (c ProjectControlClient) reconcile(ctx context.Context, revision int64, project string) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "POST", "/v1/projects/"+url.PathEscape(project)+"/reconcile", map[string]any{"expected_revision": revision}, &result)
	return result, err
}

type LinkableDataset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c ProjectControlClient) linkableDatasets(ctx context.Context) ([]LinkableDataset, error) {
	var result []LinkableDataset
	err := c.request(ctx, "GET", "/v1/linkable-datasets", nil, &result)
	return result, err
}

func (c ProjectControlClient) alias(ctx context.Context, revision int64, project, workspace string) (AccessProject, error) {
	var result AccessProject
	err := c.request(ctx, "POST", "/v1/projects/"+url.PathEscape(project)+"/aliases", map[string]any{"expected_revision": revision, "workspace": workspace}, &result)
	return result, err
}

package simpledesktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	ToolGraph       ToolGraphSettings          `json:"tool_graph"`
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

func (p AccessProject) policySyncUnconfirmed() bool {
	return p.Status == "unconfirmed" || p.Status == "transition"
}

func (p AccessProject) agentPolicyUnconfirmed(agentID string) bool {
	if p.policySyncUnconfirmed() {
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
	ProposedSHA     string   `json:"proposed_sha256"`
	DisabledPlugins []string `json:"disabled_plugins"`
	EnabledPlugin   string   `json:"enabled_plugin"`
}

type ProjectControlClient struct {
	Config ProjectControlConfig
	HTTP   *http.Client
}
type ControlResponseError struct {
	Status  int
	Code    string
	Message string
}

func (e *ControlResponseError) Error() string { return e.Message }

func parseControlErrorCode(body []byte) string {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if payload.Error != "" {
		return payload.Error
	}
	return payload.Code
}

func isControlAuthDenied(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && (ce.Status == http.StatusUnauthorized || ce.Status == http.StatusForbidden)
}

func isControlErrorCode(err error, status int, code string) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == status && ce.Code == code
}

func isControlIndexRebuilding(err error) bool {
	return isControlErrorCode(err, http.StatusServiceUnavailable, "catalog_index_rebuilding")
}

func isControlInvalidCursor(err error) bool {
	return isControlErrorCode(err, http.StatusBadRequest, "invalid_cursor")
}

func isControlFailClosedV2(err error) bool {
	if isControlAuthDenied(err) {
		return true
	}
	return isControlIndexRebuilding(err)
}

func controlUserError(err error) string {
	if err == nil {
		return ""
	}
	var ce *ControlResponseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case "project_unconfirmed":
			return "项目尚未确认，无法读取内容"
		case "document_path_denied":
			return "内容路径受限，无法读取"
		case "catalog_index_rebuilding":
			return "索引正在构建中，请稍候刷新"
		case "reading_contract_mismatch":
			return "桌面端与本地后台的读取协议不一致，请用当前版本重启后台服务"
		case "invalid_cursor":
			return "列表分页游标已失效，请刷新列表"
		}
	}
	return err.Error()
}

func (c ProjectControlClient) request(ctx context.Context, method, path string, input, output any) error {
	return c.requestWithHeaders(ctx, method, path, nil, input, output, 2*1024*1024)
}

func (c ProjectControlClient) requestWithLimit(ctx context.Context, method, path string, input, output any, maxBytes int64) error {
	return c.requestWithHeaders(ctx, method, path, nil, input, output, maxBytes)
}

func (c ProjectControlClient) requestWithHeaders(ctx context.Context, method, path string, headers map[string]string, input, output any, maxBytes int64) error {
	route, _, _ := strings.Cut(path, "?")
	// These paths existed with a different response contract before the cutover.
	requiredReadingField := ""
	if method == http.MethodGet {
		switch {
		case route == "/v1/overview/bootstrap":
			requiredReadingField = "generation"
		case strings.HasPrefix(route, "/v1/projects/") && strings.HasSuffix(route, "/message-summary"):
			requiredReadingField = "unread_count"
		}
	}
	if requiredReadingField != "" {
		if err := c.requireReadingContract(ctx); err != nil {
			return err
		}
	}

	u, err := url.Parse(c.Config.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("项目管理服务尚未配置")
	}
	key, err := readCachedToken(c.Config.ManagementTokenFile, 32, true)
	if err != nil {
		return &ControlResponseError{
			Status:  http.StatusUnauthorized,
			Code:    "control_credential_unavailable",
			Message: "项目管理连接凭据不可用",
		}
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
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}
	if requiredReadingField != "" {
		req.Header.Set("X-Memlane-Reading-Contract", readingContract)
	}
	hc := newHTTPClient(c.HTTP, 125*time.Second)
	response, err := hc.Do(req)
	if err != nil {
		if method != "GET" {
			return fmt.Errorf("设置结果未确认，请刷新核对原操作；不会自动重复提交")
		}
		return fmt.Errorf("项目目录暂时不可用，保留已载入项目")
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return &ControlResponseError{Status: 409, Code: parseControlErrorCode(snippet), Message: "设置已变化或关联存在冲突，请刷新核对后再保存"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return &ControlResponseError{Status: response.StatusCode, Code: parseControlErrorCode(snippet), Message: fmt.Sprintf("项目管理请求未确认（HTTP %d），请核对原操作", response.StatusCode)}
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return fmt.Errorf("项目管理回执未确认，请刷新核对")
	}
	if requiredReadingField != "" {
		// The running backend may have changed after the successful capability probe.
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields[requiredReadingField] == nil {
			return readingContractMismatch()
		}
	}
	if json.Unmarshal(data, output) != nil {
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
	input := map[string]any{"expected_revision": revision, "name": name}
	if workspace != "" {
		input["workspace"] = workspace
	}
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

// rename changes only the client display name; dataset identity and bindings stay fixed.
func (c ProjectControlClient) rename(ctx context.Context, revision int64, project, name string) (AccessProject, error) {
	var result AccessProject
	err := c.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(project)+"/name", map[string]any{"expected_revision": revision, "name": name}, &result)
	return result, err
}

type ControlCapabilities struct {
	APIVersions     []string `json:"api_versions"`
	ReadingContract string   `json:"reading_contract"`
	Features        []string `json:"features"`
}

func (caps ControlCapabilities) HasFeature(feature string) bool {
	if feature == readingContract {
		return caps.ReadingContract == readingContract
	}

	for _, f := range caps.Features {
		if f == feature {
			return true
		}
	}
	return false
}

const readingContract = "catalog-cursor-v1"

func (c ProjectControlClient) requireReadingContract(ctx context.Context) error {
	ok, err := c.probeFeature(ctx, readingContract)
	if err != nil {
		return err
	}
	if !ok {
		return readingContractMismatch()
	}
	return nil
}

func readingContractMismatch() error {
	return &ControlResponseError{
		Status: http.StatusServiceUnavailable, Code: "reading_contract_mismatch",
		Message: "本地后台仍在使用旧版读取接口，请停止并重启后台服务",
	}
}

type cachedCapabilitiesEntry struct {
	caps      ControlCapabilities
	supported bool
	expiresAt time.Time
}

var controlCapabilitiesCache = struct {
	mu      sync.Mutex
	entries map[string]cachedCapabilitiesEntry
}{
	entries: map[string]cachedCapabilitiesEntry{},
}

func (c ProjectControlClient) capabilities(ctx context.Context) (ControlCapabilities, error) {
	var result ControlCapabilities
	err := c.request(ctx, "GET", "/v1/capabilities", nil, &result)
	return result, err
}

func (c ProjectControlClient) probeFeature(ctx context.Context, feature string) (bool, error) {
	token, tokErr := readCachedToken(c.Config.ManagementTokenFile, 32, true)
	if tokErr != nil {
		// Fail closed when management credentials are unavailable; never silently
		// downgrade to official /api/v1/* with a separate API key.
		return false, fmt.Errorf("项目管理服务凭据不可用：%w", tokErr)
	}
	tokenDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	cacheKey := c.Config.URL + "|" + tokenDigest
	now := time.Now()
	controlCapabilitiesCache.mu.Lock()
	entry, ok := controlCapabilitiesCache.entries[cacheKey]
	controlCapabilitiesCache.mu.Unlock()
	if ok && now.Before(entry.expiresAt) && (feature != readingContract || entry.caps.ReadingContract == readingContract) {
		return entry.supported && entry.caps.HasFeature(feature), nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	caps, err := c.capabilities(probeCtx)
	if err != nil {
		if isControlNotFound(err) {
			// Confirmed legacy control server without /v1/capabilities: safe to cache fallback.
			controlCapabilitiesCache.mu.Lock()
			if len(controlCapabilitiesCache.entries) >= 128 {
				clear(controlCapabilitiesCache.entries)
			}
			controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{supported: false, expiresAt: now.Add(5 * time.Minute)}
			controlCapabilitiesCache.mu.Unlock()
			return false, nil
		}
		// All other errors (401/403, 5xx, network failure, cancellation) fail closed
		// without caching supported=false or falling back to official /api/v1/*.
		if isControlAuthDenied(err) {
			controlCapabilitiesCache.mu.Lock()
			delete(controlCapabilitiesCache.entries, cacheKey)
			controlCapabilitiesCache.mu.Unlock()
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, err
	}
	controlCapabilitiesCache.mu.Lock()
	if len(controlCapabilitiesCache.entries) >= 128 {
		clear(controlCapabilitiesCache.entries)
	}
	controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{
		caps:      caps,
		supported: true,
		expiresAt: now.Add(5 * time.Minute),
	}
	controlCapabilitiesCache.mu.Unlock()
	return caps.HasFeature(feature), nil
}

type documentLookupV2Response struct {
	Document
	EffectiveDate  string `json:"effective_date"`
	Category       string `json:"category"`
	CommonCategory string `json:"common_category"`
	SessionID      string `json:"session_id"`
	RecallCount    int64  `json:"recall_count"`
}

func (c ProjectControlClient) documentLookupV2(ctx context.Context, datasetID, docID string) (documentLookupV2Response, error) {
	var result documentLookupV2Response
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(docID)
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

type sessionLookupV2Response struct {
	Session
	Status string `json:"status"`
}

func (c ProjectControlClient) sessionLookupV2(ctx context.Context, datasetID, sessionID, userID string) (sessionLookupV2Response, error) {
	var result sessionLookupV2Response
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/sessions/" + url.PathEscape(sessionID)
	if userID != "" {
		path += "?user_id=" + url.QueryEscape(userID)
	}
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

type codeActivityV2Response struct {
	ProjectID        string `json:"project_id"`
	CodeDatasetID    string `json:"code_dataset_id"`
	DatasetCreatedAt string `json:"dataset_created_at"`
	Active           bool   `json:"active"`
	Status           string `json:"status"`
	RunID            string `json:"run_id"`
	LatestCreatedAt  string `json:"latest_created_at"`
	LatestStartedAt  string `json:"latest_started_at"`
	LatestEndedAt    string `json:"latest_ended_at"`
}

func (c ProjectControlClient) projectCodeActivityV2(ctx context.Context, projectID string) (codeActivityV2Response, error) {
	var result codeActivityV2Response
	path := "/v1/projects/" + url.PathEscape(projectID) + "/code-activity"
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

package cognee

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

type Config struct {
	Endpoint          string
	TokenFile         string
	StorePath         string
	PythonBin         string
	WorkDir           string
	HTTPTimeout       time.Duration
	ExtractTimeout    time.Duration
	Location          *time.Location
	EncryptionEnabled *bool
}

type Adapter struct {
	client     *client
	projector  *Projector
	bodyReader *BodyReader
	runner     SubprocessRunner
	location   *time.Location
}

var (
	_ engine.MemoryEngine         = (*Adapter)(nil)
	_ engine.ModelController      = (*Adapter)(nil)
	_ engine.CatalogProjector     = (*Adapter)(nil)
	_ engine.DatasetDiscoverer    = (*Adapter)(nil)
	_ engine.SessionHistoryReader = (*Adapter)(nil)
	_ engine.CodeIndexer          = (*Adapter)(nil)
	_ engine.ActivityReader       = (*Adapter)(nil)
)

func init() {
	engine.MustRegister("cognee", func(_ context.Context, cfg engine.Config) (engine.MemoryEngine, error) {
		config := Config{
			Endpoint: cfg.Endpoint, TokenFile: cfg.TokenFile, StorePath: cfg.StorePath,
			PythonBin: cfg.PythonBin, WorkDir: cfg.WorkDir, HTTPTimeout: cfg.HTTPTimeout,
		}
		config.ExtractTimeout, _ = cfg.Options["extract_timeout"].(time.Duration)
		config.Location, _ = cfg.Options["location"].(*time.Location)
		if encrypted, ok := cfg.Options["encryption_enabled"].(bool); ok {
			config.EncryptionEnabled = &encrypted
		}
		return New(config)
	})
}

func New(cfg Config) (*Adapter, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	location := cfg.Location
	if location == nil {
		location = time.Local
	}
	projector := NewProjector(cfg.StorePath, location)
	if cfg.EncryptionEnabled != nil {
		projector.bodyReader = NewBodyReader(cfg.StorePath, cfg.EncryptionEnabled)
	}
	return &Adapter{
		client:     client,
		projector:  projector,
		bodyReader: projector.bodyReader,
		runner:     SubprocessRunner{PythonBin: cfg.PythonBin, WorkDir: cfg.WorkDir},
		location:   location,
	}, nil
}

func (a *Adapter) Health(ctx context.Context) (engine.HealthStatus, error) {
	var result engine.HealthStatus
	err := a.client.request(ctx, http.MethodGet, "/health", nil, nil, &result)
	return result, err
}

func (a *Adapter) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	if req.Kind == engine.IngestKindCaptureQA {
		var err error
		req, err = qaWithContextDate(req, time.Now(), a.location)
		if err != nil {
			return engine.IngestResult{}, err
		}
	}
	var result engine.IngestResult
	err := a.client.request(ctx, http.MethodPost, "/ingest", nil, req, &result)
	return result, err
}

func (a *Adapter) Extract(ctx context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
	var result engine.ExtractResult
	err := a.client.request(ctx, http.MethodPost, "/extract", nil, req, &result)
	return result, err
}

func (a *Adapter) Search(ctx context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	var result engine.SearchResult
	err := a.client.request(ctx, http.MethodPost, "/search", nil, req, &result)
	return result, err
}

type wireItem struct {
	ID                   string            `json:"id"`
	DatasetID            string            `json:"dataset_id"`
	CamelDatasetID       string            `json:"datasetId"`
	Kind                 string            `json:"kind"`
	Name                 string            `json:"name"`
	Label                string            `json:"label"`
	Content              string            `json:"content"`
	ContentHash          string            `json:"content_hash"`
	CamelContentHash     string            `json:"contentHash"`
	CreatedAt            string            `json:"created_at"`
	CamelCreatedAt       string            `json:"createdAt"`
	UpdatedAt            string            `json:"updated_at"`
	CamelUpdatedAt       string            `json:"updatedAt"`
	RawDataLocation      string            `json:"raw_data_location"`
	CamelRawDataLocation string            `json:"rawDataLocation"`
	MimeType             string            `json:"mime_type"`
	CamelMimeType        string            `json:"mimeType"`
	PipelineStatus       string            `json:"pipeline_status"`
	CamelPipelineStatus  string            `json:"pipelineStatus"`
	Metadata             map[string]any    `json:"metadata"`
	Extension            wireMetadataValue `json:"extension"`
	NodeSet              wireMetadataValue `json:"node_set"`
	CamelNodeSet         wireMetadataValue `json:"nodeSet"`
}

// wireMetadataValue distinguishes an explicit null from an omitted field.
type wireMetadataValue struct {
	value   any
	present bool
}

func (v *wireMetadataValue) UnmarshalJSON(data []byte) error {
	v.present = true
	return json.Unmarshal(data, &v.value)
}

func (w wireItem) toEngineItem(datasetID string) engine.Item {
	metadata := maps.Clone(w.Metadata)
	nodeSet := w.NodeSet
	if !nodeSet.present {
		nodeSet = w.CamelNodeSet
	}
	for key, field := range map[string]wireMetadataValue{"extension": w.Extension, "node_set": nodeSet} {
		if _, exists := metadata[key]; field.present && !exists {
			if metadata == nil {
				metadata = make(map[string]any)
			}
			metadata[key] = field.value
		}
	}
	return engine.Item{
		ID: w.ID, DatasetID: cmp.Or(w.DatasetID, w.CamelDatasetID, datasetID),
		Kind: w.Kind, Name: w.Name, Label: w.Label, Content: w.Content,
		ContentHash:     cmp.Or(w.ContentHash, w.CamelContentHash),
		CreatedAt:       cmp.Or(w.CreatedAt, w.CamelCreatedAt),
		UpdatedAt:       cmp.Or(w.UpdatedAt, w.CamelUpdatedAt),
		RawDataLocation: cmp.Or(w.RawDataLocation, w.CamelRawDataLocation),
		MimeType:        cmp.Or(w.MimeType, w.CamelMimeType),
		PipelineStatus:  cmp.Or(w.PipelineStatus, w.CamelPipelineStatus),
		Metadata:        metadata,
	}
}

type wireItemResponse struct {
	OK   bool      `json:"ok"`
	Item *wireItem `json:"item"`
}

type wireListResult struct {
	OK         bool       `json:"ok"`
	Items      []wireItem `json:"items"`
	NextCursor string     `json:"next_cursor"`
	HasMore    bool       `json:"has_more"`
}

func (a *Adapter) ListItems(ctx context.Context, req engine.ListRequest) (engine.ListResult, error) {
	query := scopeQuery(req.Scope.DatasetID, req.Scope.UserID)
	if req.Limit != 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Cursor != "" {
		query.Set("cursor", req.Cursor)
	}
	var wire wireListResult
	if err := a.client.request(ctx, http.MethodGet, "/items", query, nil, &wire); err != nil {
		return engine.ListResult{}, err
	}
	result := engine.ListResult{OK: wire.OK, NextCursor: wire.NextCursor, HasMore: wire.HasMore || wire.NextCursor != ""}
	if wire.Items != nil {
		result.Items = make([]engine.Item, len(wire.Items))
		for i, item := range wire.Items {
			result.Items[i] = item.toEngineItem(req.Scope.DatasetID)
		}
	}
	return result, nil
}

func (a *Adapter) GetItem(ctx context.Context, scope engine.Scope, itemID string) (engine.Item, error) {
	path, err := itemPath(itemID)
	if err != nil {
		return engine.Item{}, err
	}
	var resp wireItemResponse
	if err := a.client.request(ctx, http.MethodGet, path, scopeQuery(scope.DatasetID, scope.UserID), nil, &resp); err != nil {
		return engine.Item{}, err
	}
	if !resp.OK || resp.Item == nil {
		return engine.Item{}, runnerUnavailable("invalid engine item response", nil)
	}
	return resp.Item.toEngineItem(scope.DatasetID), nil
}

func (a *Adapter) DeleteItem(ctx context.Context, scope engine.Scope, itemID string) error {
	path, err := itemPath(itemID)
	if err != nil {
		return err
	}
	return a.client.request(ctx, http.MethodDelete, path, scopeQuery(scope.DatasetID, scope.UserID), nil, nil)
}

func (a *Adapter) Graph(ctx context.Context, req engine.GraphRequest) (engine.GraphPayload, error) {
	query := scopeQuery(req.Scope.DatasetID, "")
	if req.Filter != "" {
		query.Set("filter", req.Filter)
	}
	var result engine.GraphPayload
	err := a.client.request(ctx, http.MethodGet, "/graph", query, nil, &result)
	return result, err
}

func (a *Adapter) ApplyModel(ctx context.Context, req engine.ApplyModelRequest) (engine.ApplyModelResult, error) {
	var result engine.ApplyModelResult
	err := a.client.request(ctx, http.MethodPost, "/model/apply", nil, req, &result)
	return result, err
}

func (a *Adapter) ModelStatus(ctx context.Context) (engine.ModelStatusResult, error) {
	var result engine.ModelStatusResult
	err := a.client.request(ctx, http.MethodGet, "/model/status", nil, nil, &result)
	return result, err
}

func (a *Adapter) ProbeModel(ctx context.Context, req engine.ProbeRequest) (engine.ProbeResult, error) {
	return a.runner.ProbeModel(ctx, req)
}

func (a *Adapter) SnapshotCatalog(ctx context.Context, targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	return a.projector.SnapshotCatalog(ctx, targets, opts)
}

func (a *Adapter) DiscoverDatasets(ctx context.Context) ([]engine.DiscoveredDataset, error) {
	found, err := a.projector.DiscoverDatasets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]engine.DiscoveredDataset, 0, len(found))
	for _, ds := range found {
		out = append(out, engine.DiscoveredDataset{ID: ds.ID, Name: ds.Name, Protected: ds.Protected})
	}
	return out, nil
}

func (a *Adapter) ReadLocalBody(ctx context.Context, ref engine.LocalBodyRef) (string, error) {
	result, err := a.bodyReader.ReadBody(ctx, ref)
	return result.Body, err
}

func (a *Adapter) HydrateSessionsFromCache(ctx context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	return a.projector.HydrateSessionsFromCache(ctx, refs)
}

func (a *Adapter) ReadSessionHistory(ctx context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	return a.projector.ReadSessionHistory(ctx, req)
}

func (a *Adapter) EnsureDataset(ctx context.Context, name string) (engine.DatasetRecord, error) {
	if strings.TrimSpace(name) == "" {
		return engine.DatasetRecord{}, invalidArgument("dataset name required", nil)
	}
	var resp struct {
		OK        bool   `json:"ok"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	if err := a.client.request(ctx, http.MethodPost, "/datasets", nil, map[string]string{"name": strings.TrimSpace(name)}, &resp); err != nil {
		return engine.DatasetRecord{}, err
	}
	if !resp.OK || strings.TrimSpace(resp.ID) == "" {
		return engine.DatasetRecord{}, runnerUnavailable("invalid ensure dataset response", nil)
	}
	return engine.DatasetRecord{ID: resp.ID, Name: resp.Name, CreatedAt: resp.CreatedAt}, nil
}

func (a *Adapter) IndexCode(ctx context.Context, req engine.CodeIndexRequest) (engine.CodeIndexResult, error) {
	if strings.TrimSpace(req.DatasetID) == "" || strings.TrimSpace(req.RepoPath) == "" {
		return engine.CodeIndexResult{}, invalidArgument("dataset_id and repo_path required", nil)
	}
	var result engine.CodeIndexResult
	if err := a.client.request(ctx, http.MethodPost, "/code-index", nil, req, &result); err != nil {
		return engine.CodeIndexResult{}, err
	}
	return result, nil
}

func (a *Adapter) ProjectCodeActivity(ctx context.Context, datasetID string) (engine.CodeActivitySnapshot, error) {
	return a.projector.ProjectCodeActivity(ctx, datasetID)
}

func (a *Adapter) ListPipelineRuns(ctx context.Context, query engine.PipelineRunsQuery) ([]engine.PipelineRun, error) {
	return a.projector.ListPipelineRuns(ctx, query)
}

func qaWithContextDate(req engine.IngestRequest, now time.Time, loc *time.Location) (engine.IngestRequest, error) {
	data, err := json.Marshal(req.Content)
	if err != nil {
		return req, invalidArgument("encode QA content", err)
	}
	var content map[string]json.RawMessage
	// Object content can be safely amended while retaining the original representation of each field.
	objectContent := json.Unmarshal(data, &content) == nil && content != nil
	rawContext := contextValue(content["context"])
	contextDate := jsonString(rawContext.object["date"])
	day := contextDate
	if strings.TrimSpace(day) == "" {
		day = qaLocalDay(content, now, loc)
	}
	if strings.TrimSpace(req.SourceMeta.ContextDate) == "" {
		req.SourceMeta.ContextDate = day
	}
	if objectContent && rawContext.object != nil && strings.TrimSpace(contextDate) == "" {
		rawContext.object["date"], _ = json.Marshal(day)
		encoded, err := json.Marshal(rawContext.object)
		if err != nil {
			return req, invalidArgument("encode QA context", err)
		}
		if rawContext.stringForm {
			encoded, _ = json.Marshal(string(encoded))
		}
		content["context"] = encoded
		req.Content = content
	}
	return req, nil
}

type qaContext struct {
	object     map[string]json.RawMessage
	stringForm bool
}

func contextValue(raw json.RawMessage) qaContext {
	if len(raw) == 0 || string(raw) == "null" {
		return qaContext{object: make(map[string]json.RawMessage), stringForm: true}
	}
	var text string
	stringForm := json.Unmarshal(raw, &text) == nil
	if stringForm {
		if strings.TrimSpace(text) == "" {
			return qaContext{object: make(map[string]json.RawMessage), stringForm: true}
		}
		raw = json.RawMessage(text)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		return qaContext{object: object, stringForm: stringForm}
	}
	return qaContext{}
}

func qaLocalDay(content map[string]json.RawMessage, now time.Time, loc *time.Location) string {
	stamp := strings.TrimSpace(jsonString(content["prompt_time"]))
	if stamp == "" {
		stamp = strings.TrimSpace(jsonString(content["time"]))
	}
	if _, err := time.Parse(time.DateOnly, stamp); err == nil {
		return stamp
	}
	for _, layout := range []string{
		time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02T15:04:05", time.DateTime,
	} {
		if parsed, err := time.Parse(layout, stamp); err == nil {
			return parsed.In(loc).Format(time.DateOnly)
		}
	}
	return now.In(loc).Format(time.DateOnly)
}

func jsonString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

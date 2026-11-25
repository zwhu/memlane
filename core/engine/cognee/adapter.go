package cognee

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
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
	_ engine.MemoryEngine     = (*Adapter)(nil)
	_ engine.ModelController  = (*Adapter)(nil)
	_ engine.CatalogProjector = (*Adapter)(nil)
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
	return &Adapter{
		client:     client,
		projector:  NewProjector(cfg.StorePath, location),
		bodyReader: NewBodyReader(cfg.StorePath, cfg.EncryptionEnabled),
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

func (a *Adapter) ListItems(ctx context.Context, req engine.ListRequest) (engine.ListResult, error) {
	query := scopeQuery(req.Scope.DatasetID, req.Scope.UserID)
	if req.Limit != 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Cursor != "" {
		query.Set("cursor", req.Cursor)
	}
	var result engine.ListResult
	err := a.client.request(ctx, http.MethodGet, "/items", query, nil, &result)
	return result, err
}

func (a *Adapter) GetItem(ctx context.Context, scope engine.Scope, itemID string) (engine.Item, error) {
	path, err := itemPath(itemID)
	if err != nil {
		return engine.Item{}, err
	}
	var result engine.Item
	err = a.client.request(ctx, http.MethodGet, path, scopeQuery(scope.DatasetID, scope.UserID), nil, &result)
	return result, err
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

func (a *Adapter) SyncCatalog(ctx context.Context, store *domain.Store) (*engine.SyncStats, error) {
	return a.projector.SyncCatalog(ctx, store)
}

func (a *Adapter) ReadLocalBody(ctx context.Context, ref engine.LocalBodyRef) (string, error) {
	result, err := a.bodyReader.ReadBody(ctx, ref)
	return result.Body, err
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

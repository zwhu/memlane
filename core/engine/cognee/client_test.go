package cognee

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

func testConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "runner.token")
	if err := os.WriteFile(tokenFile, []byte("synthetic-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{
		Endpoint: endpoint, TokenFile: tokenFile, StorePath: t.TempDir(),
		Location: time.FixedZone("UTC+8", 8*60*60),
	}
}

func openTestAdapter(t *testing.T, serve http.HandlerFunc) (*Adapter, Config) {
	t.Helper()
	endpoint := "http://127.0.0.1:1"
	listener, bindErr := net.Listen("tcp4", "127.0.0.1:0")
	if bindErr == nil {
		server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: serve}}
		server.Start()
		t.Cleanup(server.Close)
		endpoint = server.URL
	} else if !errors.Is(bindErr, syscall.EPERM) && !errors.Is(bindErr, syscall.EACCES) {
		t.Fatal(bindErr)
	}

	cfg := testConfig(t, endpoint)
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bindErr != nil {
		t.Logf("loopback bind unavailable, using in-process HTTP transport: %v", bindErr)
		transport := inProcessTransport{handler: serve}
		adapter.client.http.Transport = transport
		adapter.client.extractHTTP.Transport = transport
	}
	return adapter, cfg
}

type inProcessTransport struct {
	handler http.Handler
}

func (t inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, req)
	return recorder.Result(), nil
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Errorf("invalid JSON %q: %v", got, err)
		return
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Errorf("invalid fixture %q: %v", want, err)
		return
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestAdapterRunnerWire(t *testing.T) {
	scope := engine.Scope{DatasetID: "ds & space", UserID: "user + symbol"}
	itemID := "item?# 中文"
	type wireCall struct {
		method, path string
		query        url.Values
		body         string
		response     string
		call         func(*Adapter) (any, error)
		want         any
	}
	ctx := context.Background()
	scopeParams := url.Values{"dataset_id": {scope.DatasetID}, "user_id": {scope.UserID}}
	cases := []wireCall{
		{
			method: "GET", path: "/health",
			response: `{"ok":true,"engine":"cognee","mode":"ready","capabilities":{"tool_graph":true}}`,
			call:     func(a *Adapter) (any, error) { return a.Health(ctx) },
			want: engine.HealthStatus{OK: true, Engine: "cognee", Mode: engine.HealthModeReady,
				Capabilities: engine.Capabilities{ToolGraph: true}},
		},
		{
			method: "POST", path: "/ingest",
			body:     `{"scope":{"dataset_id":"ds & space","user_id":"user + symbol"},"kind":"capture_qa","idempotency_key":"capture","content":{"prompt_time":"2026-11-25T20:15:00Z","context":{"topic":"topic","date":"2026-11-26"}},"source_meta":{"context_date":"2026-11-26"}}`,
			response: `{"ok":true,"entry_id":"entry","deduplicated":true}`,
			call: func(a *Adapter) (any, error) {
				return a.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKindCaptureQA,
					IdempotencyKey: "capture", Content: map[string]any{
						"prompt_time": "2026-11-25T20:15:00Z", "context": map[string]any{"topic": "topic"},
					}})
			},
			want: engine.IngestResult{OK: true, EntryID: "entry", Deduplicated: true},
		},
		{
			method: "POST", path: "/extract",
			body:     `{"scope":{"dataset_id":"ds & space","user_id":"user + symbol"},"doc_ids":["doc"],"expected_model_digest":"sha","output_language":"zh-CN","mode":"tool_graph"}`,
			response: `{"ok":true,"touched_doc_ids":["doc"],"model_digest":"sha","pipeline_run_id":"run"}`,
			call: func(a *Adapter) (any, error) {
				return a.Extract(ctx, engine.ExtractRequest{Scope: scope, DocIDs: []string{"doc"},
					ExpectedModelDigest: "sha", OutputLanguage: "zh-CN", Mode: engine.ExtractModeToolGraph})
			},
			want: engine.ExtractResult{OK: true, TouchedDocIDs: []string{"doc"}, ModelDigest: "sha", PipelineRunID: "run"},
		},
		{
			method: "POST", path: "/search",
			body:     `{"scopes":[{"dataset_id":"ds & space","user_id":"user + symbol"}],"query":"text","filters":{"kind":"lesson"},"limit":5}`,
			response: `{"ok":true,"hits":[{"dataset_id":"ds & space","document_id":"doc","score":0.5}]}`,
			call: func(a *Adapter) (any, error) {
				return a.Search(ctx, engine.SearchRequest{Scopes: []engine.Scope{scope}, Query: "text",
					Filters: engine.SearchFilters{Kind: "lesson"}, Limit: 5})
			},
			want: engine.SearchResult{OK: true, Hits: []engine.SearchHit{{DatasetID: scope.DatasetID, DocumentID: "doc", Score: 0.5}}},
		},
		{
			method: "GET", path: "/items",
			query:    url.Values{"dataset_id": {scope.DatasetID}, "user_id": {scope.UserID}, "limit": {"3"}, "cursor": {"next+?="}},
			response: `{"ok":true,"items":[{"id":"doc","dataset_id":"ds & space"}],"next_cursor":"next","has_more":true}`,
			call: func(a *Adapter) (any, error) {
				return a.ListItems(ctx, engine.ListRequest{Scope: scope, Limit: 3, Cursor: "next+?="})
			},
			want: engine.ListResult{OK: true, Items: []engine.Item{{ID: "doc", DatasetID: scope.DatasetID}},
				NextCursor: "next", HasMore: true},
		},
		{
			method: "GET", path: "/items/" + url.PathEscape(itemID), query: scopeParams,
			response: `{"id":"doc","dataset_id":"ds & space","content":"body"}`,
			call:     func(a *Adapter) (any, error) { return a.GetItem(ctx, scope, itemID) },
			want:     engine.Item{ID: "doc", DatasetID: scope.DatasetID, Content: "body"},
		},
		{
			method: "DELETE", path: "/items/" + url.PathEscape(itemID), query: scopeParams,
			call: func(a *Adapter) (any, error) { return nil, a.DeleteItem(ctx, scope, itemID) },
		},
		{
			method: "GET", path: "/graph", query: url.Values{"dataset_id": {scope.DatasetID}, "filter": {"A+B"}},
			response: `{"ok":true,"nodes":[{"id":"node"}],"edges":[{"source":"node","target":"node"}]}`,
			call:     func(a *Adapter) (any, error) { return a.Graph(ctx, engine.GraphRequest{Scope: scope, Filter: "A+B"}) },
			want:     engine.GraphPayload{OK: true, Nodes: []engine.GraphNode{{ID: "node"}}, Edges: []engine.GraphEdge{{Source: "node", Target: "node"}}},
		},
		{
			method: "POST", path: "/model/apply",
			body:     `{"generation_config":{"temperature":0},"output_language":"zh-CN","operation_id":"switch"}`,
			response: `{"ok":true,"applied":true,"model_digest":"sha","operation_id":"switch"}`,
			call: func(a *Adapter) (any, error) {
				return a.ApplyModel(ctx, engine.ApplyModelRequest{GenerationConfig: map[string]any{"temperature": 0},
					OutputLanguage: "zh-CN", OperationID: "switch"})
			},
			want: engine.ApplyModelResult{OK: true, Applied: true, ModelDigest: "sha", OperationID: "switch"},
		},
		{
			method: "GET", path: "/model/status",
			response: `{"ok":true,"mode":"ready","model_digest":"sha","output_language":"zh-CN","active_tasks":3}`,
			call:     func(a *Adapter) (any, error) { return a.ModelStatus(ctx) },
			want:     engine.ModelStatusResult{OK: true, Mode: "ready", ModelDigest: "sha", OutputLanguage: "zh-CN", ActiveTasks: 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			a, _ := openTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.EscapedPath() != "/internal/v1"+tc.path {
					t.Errorf("wire request = %s %s, want %s /internal/v1%s", r.Method, r.URL.EscapedPath(), tc.method, tc.path)
				}
				if r.URL.Query().Encode() != tc.query.Encode() {
					t.Errorf("query = %v, want %v", r.URL.Query(), tc.query)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("private token was not sent as a bearer credential")
				}
				if tc.body != "" {
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("POST is missing JSON content type")
					}
					data, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read body: %v", err)
					}
					assertJSON(t, data, tc.body)
				}
				if tc.response == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if _, err := io.WriteString(w, tc.response); err != nil {
					t.Errorf("write test response: %v", err)
				}
			})
			got, err := tc.call(a)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("adapter = (%#v, %v), want %#v", got, err, tc.want)
			}
		})
	}
}

func TestQAContextDate(t *testing.T) {
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	loc := time.FixedZone("UTC+8", 8*60*60)
	tests := []struct {
		name, content, sourceDate, wantContext, wantDate string
	}{
		{"object", `{"prompt_time":"2026-11-23T20:00:00Z","time":"2026-11-22","context":{"n":9007199254740993}}`, "",
			`{"date":"2026-11-24","n":9007199254740993}`, "2026-11-24"},
		{"JSON object string", `{"time":"2026-11-24T01:00:00","context":"{ \"tag\": \"x\" }"}`, "",
			`"{\"date\":\"2026-11-24\",\"tag\":\"x\"}"`, "2026-11-24"},
		{"missing", `{}`, "", `"{\"date\":\"2026-11-26\"}"`, "2026-11-26"},
		{"empty", `{"time":"2026-11-24","context":"  "}`, "", `"{\"date\":\"2026-11-24\"}"`, "2026-11-24"},
		{"explicit date", `{"time":"2026-11-24","context":{"date":"2026-11-01"}}`, "",
			`{"date":"2026-11-01"}`, "2026-11-01"},
		{"explicit source", `{"time":"2026-11-24","context":null}`, "2026-11-02",
			`"{\"date\":\"2026-11-24\"}"`, "2026-11-02"},
		{"both explicit", `{"time":"2026-11-24","context":"{ \"date\": \"2026-11-01\" }"}`, "2026-11-02",
			`"{ \"date\": \"2026-11-01\" }"`, "2026-11-02"},
		{"plain string", `{"time":"bad timestamp","context":"  personal notes  "}`, "",
			`"  personal notes  "`, "2026-11-26"},
		{"array string", `{"time":"2026-11-23","context":"[1,2]"}`, "", `"[1,2]"`, "2026-11-23"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := engine.IngestRequest{
				Kind: engine.IngestKindCaptureQA, Content: json.RawMessage(tc.content),
				SourceMeta: engine.SourceMeta{ContextDate: tc.sourceDate},
			}
			got, err := qaWithContextDate(req, now, loc)
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceMeta.ContextDate != tc.wantDate {
				t.Errorf("context_date = %q, want %q", got.SourceMeta.ContextDate, tc.wantDate)
			}
			data, err := json.Marshal(got.Content)
			if err != nil {
				t.Fatal(err)
			}
			var content map[string]json.RawMessage
			if err := json.Unmarshal(data, &content); err != nil {
				t.Fatal(err)
			}
			if string(content["context"]) != tc.wantContext {
				t.Errorf("context = %s, want %s", content["context"], tc.wantContext)
			}
			if string(req.Content.(json.RawMessage)) != tc.content || req.SourceMeta.ContextDate != tc.sourceDate {
				t.Error("QA preparation modified the caller request")
			}
		})
	}
}

func TestApplyModelUsesExtractTimeout(t *testing.T) {
	a, _ := openTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiPrefix+"/model/apply" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(120 * time.Millisecond):
		}
		if _, err := io.WriteString(w, `{"ok":true,"applied":true}`); err != nil {
			t.Errorf("write test response: %v", err)
		}
	})
	a.client.http.Timeout = 50 * time.Millisecond
	a.client.extractHTTP.Timeout = 2 * time.Second
	got, err := a.ApplyModel(t.Context(), engine.ApplyModelRequest{})
	if err != nil || !got.OK || !got.Applied {
		t.Fatalf("ApplyModel = %+v, %v", got, err)
	}
}

func TestRunnerStatusMapping(t *testing.T) {
	cases := []struct {
		status     int
		body, code string
		cause      error
	}{
		{400, "", "invalid_argument", engine.ErrInvalidArgument},
		{401, "private upstream error", "unauthorized", engine.ErrUnauthorized},
		{403, "", "forbidden", engine.ErrForbidden},
		{404, "", "not_found", engine.ErrNotFound},
		{409, `{"code":"write_conflict"}`, "conflict", engine.ErrConflict},
		{409, `{"code":"model_digest_mismatch"}`, modelDigestMismatchCode, engine.ErrModelDigestMismatch},
		{409, `{"error":"model_digest_mismatch"}`, modelDigestMismatchCode, engine.ErrModelDigestMismatch},
		{409, `{"detail":"model_digest_mismatch: active model changed"}`, modelDigestMismatchCode, engine.ErrModelDigestMismatch},
		{409, `{"detail":{"code":"model_digest_mismatch"}}`, modelDigestMismatchCode, engine.ErrModelDigestMismatch},
		{409, "model_digest_mismatch: private upstream details", modelDigestMismatchCode, engine.ErrModelDigestMismatch},
		{422, "", "invalid_argument", engine.ErrInvalidArgument},
		{500, "", "unavailable", engine.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			a, _ := openTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Errorf("write test response: %v", err)
				}
			})
			_, err := a.Extract(context.Background(), engine.ExtractRequest{})
			var typed *engine.EngineError
			if !errors.Is(err, tc.cause) || !errors.As(err, &typed) ||
				typed.StatusCode != tc.status || typed.Code != tc.code {
				t.Fatalf("error = %#v, want HTTP %d with %s", err, tc.status, tc.code)
			}
			if tc.body != "" && strings.Contains(err.Error(), tc.body) {
				t.Error("engine error exposes response body")
			}
		})
	}
}

func TestAdapterRejectsInvalidToken(t *testing.T) {
	config := testConfig(t, "http://127.0.0.1:1")
	invalidPaths := map[string]string{"missing": filepath.Join(t.TempDir(), "missing")}
	for _, mode := range []struct {
		name string
		mode os.FileMode
		data string
	}{
		{"public", 0o644, "synthetic-token"},
		{"empty", 0o600, " \n"},
		{"multiline", 0o600, "test\nother"},
	} {
		path := filepath.Join(t.TempDir(), mode.name)
		if err := os.WriteFile(path, []byte(mode.data), mode.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode.mode); err != nil {
			t.Fatal(err)
		}
		invalidPaths[mode.name] = path
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(config.TokenFile, link); err != nil {
		t.Fatal(err)
	}
	invalidPaths["symlink"] = link
	for name, path := range invalidPaths {
		t.Run(name, func(t *testing.T) {
			bad := config
			bad.TokenFile = path
			if _, err := New(bad); !errors.Is(err, engine.ErrUnauthorized) {
				t.Fatalf("New with invalid token = %v, want unauthorized", err)
			}
		})
	}
	a, cfg := openTestAdapter(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("request sent after token became insecure")
	})
	if err := os.Chmod(cfg.TokenFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Health(context.Background()); !errors.Is(err, engine.ErrUnauthorized) {
		t.Fatalf("Health with invalidated token = %v, want unauthorized", err)
	}
}

func TestAdapterEndpointAndItemPaths(t *testing.T) {
	cfg := testConfig(t, "http://127.0.0.1:1")
	for _, endpoint := range []string{
		"http://example.net:1234", "http://localhost:1234", "https://127.0.0.1:1234",
		"http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:1234/elsewhere",
		"http://127.0.0.1:1234?", "http://127.0.0.1:1234#", "http://127.0.0.1:1234/%2F",
	} {
		bad := cfg
		bad.Endpoint = endpoint
		if _, err := New(bad); !errors.Is(err, engine.ErrInvalidArgument) {
			t.Errorf("New with endpoint %q = %v, want invalid argument", endpoint, err)
		}
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", " ", ".", "..", "parent..", "sub/item", "sub\\item"} {
		if _, err := a.GetItem(context.Background(), engine.Scope{}, id); !errors.Is(err, engine.ErrInvalidArgument) {
			t.Errorf("GetItem with ID %q = %v, want invalid argument", id, err)
		}
		if err := a.DeleteItem(context.Background(), engine.Scope{}, id); !errors.Is(err, engine.ErrInvalidArgument) {
			t.Errorf("DeleteItem with ID %q = %v, want invalid argument", id, err)
		}
	}
}

func TestAdapterRegistry(t *testing.T) {
	cfg := testConfig(t, "http://127.0.0.1:1")
	opened, err := engine.Open(context.Background(), engine.Config{
		Engine: "cognee", Endpoint: cfg.Endpoint, TokenFile: cfg.TokenFile,
		StorePath: cfg.StorePath, Options: map[string]any{"location": cfg.Location},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := opened.(*Adapter)
	if !ok || adapter.location != cfg.Location {
		t.Fatalf("factory returned %T with wrong location", opened)
	}
	if _, ok := engine.AsModelController(opened); !ok {
		t.Error("adapter does not expose its model controller capability")
	}
	if _, ok := engine.AsCatalogProjector(opened); !ok {
		t.Error("adapter does not expose its catalog projector capability")
	}
}

func TestAdapterTransportLimits(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/relocated" {
				t.Error("runner redirect was followed")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/relocated", http.StatusTemporaryRedirect)
		})
		_, err := a.Health(context.Background())
		var typed *engine.EngineError
		if !errors.Is(err, engine.ErrUnavailable) || !errors.As(err, &typed) || typed.StatusCode != 307 {
			t.Errorf("Health with redirect = %v, want unavailable with status 307", err)
		}
	})
	t.Run("oversized JSON", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			if _, err := io.WriteString(w, strings.Repeat(" ", maxRunnerResponseBytes)+`{"ok":true}`); err != nil {
				t.Errorf("write test response: %v", err)
			}
		})
		if _, err := a.Health(context.Background()); !errors.Is(err, engine.ErrUnavailable) {
			t.Errorf("Health with oversized response = %v, want unavailable", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("request was sent after context cancellation")
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := a.Health(ctx)
		if !errors.Is(err, engine.ErrUnavailable) || !errors.Is(err, context.Canceled) {
			t.Errorf("Health with canceled context = %v, want unavailable and canceled", err)
		}
	})
}

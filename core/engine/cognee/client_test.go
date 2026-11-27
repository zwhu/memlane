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

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "runner.token")
	requireNoError(t, os.WriteFile(tokenFile, []byte("synthetic-token\n"), 0o600))
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
		requireNoError(t, bindErr)
	}

	cfg := testConfig(t, endpoint)
	adapter, err := New(cfg)
	requireNoError(t, err)
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
	var values [2]any
	for i, data := range [][]byte{got, []byte(want)} {
		if err := json.Unmarshal(data, &values[i]); err != nil {
			t.Errorf("invalid JSON %q: %v", data, err)
			return
		}
	}
	assertWireResult(t, "JSON", values[0], nil, values[1])
}

const cogneeItemJSON = `{"id":"doc","name":"notes.md","label":"notes","mimeType":"text/markdown","rawDataLocation":"notes.md","createdAt":"2026-11-26T00:00:00Z","updatedAt":"2026-11-26T01:00:00Z","nodeSet":["notes"],"extension":"md"}`

func cogneeItem(datasetID string) engine.Item {
	return engine.Item{ID: "doc", DatasetID: datasetID, Name: "notes.md", Label: "notes",
		MimeType: "text/markdown", RawDataLocation: "notes.md", CreatedAt: "2026-11-26T00:00:00Z", UpdatedAt: "2026-11-26T01:00:00Z",
		Metadata: map[string]any{"node_set": []any{"notes"}, "extension": "md"}}
}

func writeTestResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write test response: %v", err)
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
	for _, tc := range []wireCall{
		{
			method: "GET", path: "/health",
			response: `{"ok":true,"engine":"cognee","mode":"ready","capabilities":{"tool_graph":true,"hybrid_recall":true,"session_context":true,"code_search":true,"search_modes":["hybrid","graph","chunks","code"]}}`,
			call:     func(a *Adapter) (any, error) { return a.Health(ctx) },
			want: engine.HealthStatus{OK: true, Engine: "cognee", Mode: engine.HealthModeReady,
				Capabilities: engine.Capabilities{
					ToolGraph: true, HybridRecall: true, SessionContext: true, CodeSearch: true,
					SearchModes: []string{"hybrid", "graph", "chunks", "code"},
				}},
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
			method: "POST", path: "/search",
			body:     `{"scopes":[{"dataset_id":"ds & space","user_id":"user + symbol"}],"query":"auth flow","mode":"hybrid","filters":{"kind":"document","node_sets":["auth"],"session_id":"sess-1","include_session_context":true},"limit":6}`,
			response: `{"ok":true,"hits":[{"dataset_id":"ds & space","document_id":"doc","title":"auth.md","snippet":"jwt check","score":0.15}],"graph_context":{"entities":[{"dataset_id":"ds & space","name":"AuthService","type":"Service","edges":[{"text":"AuthService validates JWT","source":"AuthService","target":"JWT","relationship":"validates","superseded":true,"authoritative":true}],"conflicts":["TTL conflict"]}],"facts":[{"dataset_id":"ds & space","text":"Refresh tokens rotate"}],"conflicts":[{"dataset_id":"ds & space","text":"TTL conflict"}],"session_guidance":"## Active session guidance","code_facts":[{"dataset_id":"ds & space","name":"VerifyToken","kind":"symbol","file":"auth.go","line":18,"relations":[{"type":"calls","target":"ParseJWT"}]}],"degraded_to_chunks":true}}`,
			call: func(a *Adapter) (any, error) {
				return a.Search(ctx, engine.SearchRequest{
					Scopes: []engine.Scope{scope},
					Query:  "auth flow",
					Mode:   engine.SearchModeHybrid,
					Filters: engine.SearchFilters{
						Kind:                  "document",
						NodeSets:              []string{"auth"},
						SessionID:             "sess-1",
						IncludeSessionContext: true,
					},
					Limit: 6,
				})
			},
			want: engine.SearchResult{
				OK: true,
				Hits: []engine.SearchHit{{
					DatasetID: scope.DatasetID, DocumentID: "doc", Title: "auth.md", Snippet: "jwt check", Score: 0.15,
				}},
				GraphContext: &engine.GraphSearchContext{
					Entities: []engine.GraphSearchEntity{{
						DatasetID: scope.DatasetID,
						Name:      "AuthService",
						Type:      "Service",
						Edges: []engine.GraphSearchEdge{{
							Text: "AuthService validates JWT", Source: "AuthService", Target: "JWT",
							Relationship: "validates", Superseded: true, Authoritative: true,
						}},
						Conflicts: []string{"TTL conflict"},
					}},
					Facts:           []engine.GraphSearchFact{{DatasetID: scope.DatasetID, Text: "Refresh tokens rotate"}},
					Conflicts:       []engine.GraphSearchConflict{{DatasetID: scope.DatasetID, Text: "TTL conflict"}},
					SessionGuidance: "## Active session guidance",
					CodeFacts: []engine.CodeSearchFact{{
						DatasetID: scope.DatasetID, Name: "VerifyToken", Kind: "symbol", File: "auth.go", Line: 18,
						Relations: []engine.CodeSearchRelation{{Type: "calls", Target: "ParseJWT"}},
					}},
					DegradedToChunks: true,
				},
			},
		},
		{
			method: "GET", path: "/items",
			query:    url.Values{"dataset_id": {scope.DatasetID}, "user_id": {scope.UserID}, "limit": {"3"}, "cursor": {"next+?="}},
			response: `{"ok":true,"items":[` + cogneeItemJSON + `],"next_cursor":"3"}`,
			call: func(a *Adapter) (any, error) {
				return a.ListItems(ctx, engine.ListRequest{Scope: scope, Limit: 3, Cursor: "next+?="})
			},
			want: engine.ListResult{OK: true, Items: []engine.Item{cogneeItem(scope.DatasetID)}, NextCursor: "3", HasMore: true},
		},
		{
			method: "GET", path: "/items/" + url.PathEscape(itemID), query: scopeParams,
			response: `{"ok":true,"item":` + cogneeItemJSON + `}`,
			call:     func(a *Adapter) (any, error) { return a.GetItem(ctx, scope, itemID) },
			want:     cogneeItem(scope.DatasetID),
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
	} {
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
				writeTestResponse(t, w, tc.response)
			})
			got, err := tc.call(a)
			assertWireResult(t, "adapter", got, err, tc.want)
		})
	}
}

func assertWireResult(t *testing.T, call string, got any, err error, want any) {
	t.Helper()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%s = (%#v, %v), want %#v", call, got, err, want)
	}
}

func TestAdapterItemWireDecoding(t *testing.T) {
	scope := engine.Scope{DatasetID: "scope"}
	camel := cogneeItem("camel")
	camel.ContentHash, camel.PipelineStatus = "hash", "ready"
	snake := engine.Item{ID: "doc", DatasetID: "snake", Kind: "note", Content: "body", ContentHash: "sha",
		MimeType: "text/plain", RawDataLocation: "raw.txt", CreatedAt: "created", UpdatedAt: "updated",
		PipelineStatus: "ready", Metadata: map[string]any{"extension": "stored", "node_set": []any{"set"}}}
	for _, tc := range []struct {
		name, item, cursorJSON, cursor string
		want                           engine.Item
	}{
		{"camelCase", `{"datasetId":"camel","contentHash":"hash","pipelineStatus":"ready",` + cogneeItemJSON[1:], `null`, "", camel},
		{"snake_case", `{"id":"doc","dataset_id":"snake","datasetId":"other","kind":"note","content":"body","content_hash":"sha","mime_type":"text/plain","raw_data_location":"raw.txt","created_at":"created","updated_at":"updated","pipeline_status":"ready","node_set":["set"],"extension":"top-level","metadata":{"extension":"stored"}}`, `"3"`, "3", snake},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := openTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiPrefix+"/items" {
					writeTestResponse(t, w, `{"ok":true,"items":[`+tc.item+`],"next_cursor":`+tc.cursorJSON+`}`)
					return
				}
				writeTestResponse(t, w, `{"ok":true,"item":`+tc.item+`}`)
			})
			list, err := a.ListItems(t.Context(), engine.ListRequest{Scope: scope})
			want := engine.ListResult{OK: true, Items: []engine.Item{tc.want}, NextCursor: tc.cursor, HasMore: tc.cursor != ""}
			assertWireResult(t, "ListItems", list, err, want)
			item, err := a.GetItem(t.Context(), scope, "doc")
			assertWireResult(t, "GetItem", item, err, tc.want)
		})
	}
	for _, body := range []string{`{"ok":false,"item":{"id":"doc"}}`, `{"ok":true,"item":null}`, `{"id":"doc"}`} {
		a, _ := openTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { writeTestResponse(t, w, body) })
		if _, err := a.GetItem(t.Context(), scope, "doc"); !errors.Is(err, engine.ErrUnavailable) {
			t.Errorf("GetItem with %s = %v, want unavailable", body, err)
		}
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
			req := engine.IngestRequest{Kind: engine.IngestKindCaptureQA, Content: json.RawMessage(tc.content),
				SourceMeta: engine.SourceMeta{ContextDate: tc.sourceDate}}
			got, err := qaWithContextDate(req, now, loc)
			requireNoError(t, err)
			if got.SourceMeta.ContextDate != tc.wantDate {
				t.Errorf("context_date = %q, want %q", got.SourceMeta.ContextDate, tc.wantDate)
			}
			data, err := json.Marshal(got.Content)
			requireNoError(t, err)
			var content map[string]json.RawMessage
			requireNoError(t, json.Unmarshal(data, &content))
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
		writeTestResponse(t, w, `{"ok":true,"applied":true}`)
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
		{404, "dataset_not_provisioned: non-json text", "not_found", engine.ErrNotFound},
		{404, `{"detail":"dataset_not_provisioned: extra"}`, "not_found", engine.ErrNotFound},
		{404, `{"detail":"dataset_not_provisioned"}`, datasetNotProvisionedCode, engine.ErrDatasetNotProvisioned},
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
				writeTestResponse(t, w, tc.body)
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
	emptyPathCfg := config
	emptyPathCfg.TokenFile = "  "
	if _, err := New(emptyPathCfg); !errors.Is(err, engine.ErrUnauthorized) {
		t.Fatalf("New with empty TokenFile = %v, want unauthorized", err)
	}

	// Missing token file is allowed at construction time (lazy check) and returns ErrUnavailable on request.
	missingCfg := config
	missingCfg.TokenFile = filepath.Join(config.StorePath, "missing")
	lazyAdapter, err := New(missingCfg)
	requireNoError(t, err)
	if _, err := lazyAdapter.Health(context.Background()); !errors.Is(err, engine.ErrUnavailable) {
		t.Fatalf("Health with missing token = %v, want unavailable", err)
	}

	invalidPaths := map[string]string{"symlink": filepath.Join(config.StorePath, "link")}
	requireNoError(t, os.Symlink(config.TokenFile, invalidPaths["symlink"]))
	for _, mode := range []struct {
		name string
		mode os.FileMode
		data string
	}{
		{"public", 0o644, "synthetic-token"},
		{"empty", 0o600, " \n"},
		{"multiline", 0o600, "test\nother"},
	} {
		path := filepath.Join(config.StorePath, mode.name)
		requireNoError(t, os.WriteFile(path, []byte(mode.data), mode.mode))
		requireNoError(t, os.Chmod(path, mode.mode))
		invalidPaths[mode.name] = path
	}
	for name, path := range invalidPaths {
		t.Run(name, func(t *testing.T) {
			bad := config
			bad.TokenFile = path
			a, err := New(bad)
			requireNoError(t, err)
			if _, err := a.Health(context.Background()); !errors.Is(err, engine.ErrUnauthorized) {
				t.Fatalf("Health with invalid token = %v, want unauthorized", err)
			}
		})
	}
	a, cfg := openTestAdapter(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("request sent after token became insecure")
	})
	requireNoError(t, os.Chmod(cfg.TokenFile, 0o644))
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
	requireNoError(t, err)
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
	requireNoError(t, err)
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
			writeTestResponse(t, w, strings.Repeat(" ", maxRunnerResponseBytes)+`{"ok":true}`)
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

type staticTimeoutNetError struct{}

func (staticTimeoutNetError) Error() string   { return "i/o timeout" }
func (staticTimeoutNetError) Timeout() bool   { return true }
func (staticTimeoutNetError) Temporary() bool { return true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestNewClientDefaultExtractTimeout(t *testing.T) {
	cfg := testConfig(t, "http://127.0.0.1:1")
	a, err := New(cfg)
	requireNoError(t, err)
	if a.client.extractHTTP.Timeout != 30*time.Minute {
		t.Fatalf("default extractHTTP.Timeout = %v, want 30m", a.client.extractHTTP.Timeout)
	}
	if a.client.http.Timeout != 10*time.Second {
		t.Fatalf("default http.Timeout = %v, want 10s", a.client.http.Timeout)
	}

	customCfg := cfg
	customCfg.ExtractTimeout = 45 * time.Second
	custom, err := New(customCfg)
	requireNoError(t, err)
	if custom.client.extractHTTP.Timeout != 45*time.Second {
		t.Fatalf("custom extractHTTP.Timeout = %v, want 45s", custom.client.extractHTTP.Timeout)
	}
}

func TestAdapterTimeoutClassification(t *testing.T) {
	t.Run("extract HTTP timeout", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		})
		a.client.extractHTTP.Timeout = 40 * time.Millisecond
		_, err := a.Extract(context.Background(), engine.ExtractRequest{})
		var typed *engine.EngineError
		if !errors.As(err, &typed) || typed.Code != extractTimeoutCode ||
			!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, engine.ErrUnavailable) {
			t.Fatalf("Extract timeout error = %#v (%v), want %s wrapping DeadlineExceeded & ErrUnavailable", err, err, extractTimeoutCode)
		}
		if strings.Contains(err.Error(), "contact engine runner") {
			t.Fatalf("Extract timeout error leaked misleading unavailable copy: %v", err)
		}
	})

	t.Run("extract context deadline", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := a.Extract(ctx, engine.ExtractRequest{})
		var typed *engine.EngineError
		if !errors.As(err, &typed) || typed.Code != extractTimeoutCode ||
			!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, engine.ErrUnavailable) {
			t.Fatalf("Extract deadline error = %#v (%v), want %s", err, err, extractTimeoutCode)
		}
	})

	t.Run("health timeout", func(t *testing.T) {
		a, _ := openTestAdapter(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		})
		a.client.http.Timeout = 40 * time.Millisecond
		_, err := a.Health(context.Background())
		var typed *engine.EngineError
		if !errors.As(err, &typed) || typed.Code != engineTimeoutCode ||
			!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, engine.ErrUnavailable) {
			t.Fatalf("Health timeout error = %#v (%v), want %s", err, err, engineTimeoutCode)
		}
	})

	t.Run("net.Error timeout", func(t *testing.T) {
		cfg := testConfig(t, "http://127.0.0.1:1")
		a, err := New(cfg)
		requireNoError(t, err)
		a.client.extractHTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, staticTimeoutNetError{}
		})
		_, err = a.Extract(context.Background(), engine.ExtractRequest{})
		var typed *engine.EngineError
		if !errors.As(err, &typed) || typed.Code != extractTimeoutCode ||
			!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, engine.ErrUnavailable) {
			t.Fatalf("net.Error timeout = %#v (%v), want %s wrapping DeadlineExceeded & ErrUnavailable", err, err, extractTimeoutCode)
		}
	})
}

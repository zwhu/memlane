package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type testEngine struct {
	engine.MemoryEngine
	ingest func(context.Context, engine.IngestRequest) (engine.IngestResult, error)
	search func(context.Context, engine.SearchRequest) (engine.SearchResult, error)
	delete func(context.Context, engine.Scope, string) error
}

func (e *testEngine) Health(context.Context) (engine.HealthStatus, error) {
	return engine.HealthStatus{OK: true, Engine: "test-engine", Mode: "ready", StorePath: "/private/storage"}, nil
}
func (e *testEngine) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	return e.ingest(ctx, req)
}
func (e *testEngine) Search(ctx context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	return e.search(ctx, req)
}
func (e *testEngine) DeleteItem(ctx context.Context, scope engine.Scope, id string) error {
	return e.delete(ctx, scope, id)
}

const activeToken = "agent-secret"

const activePrincipalID = "11111111-1111-4111-8111-111111111111"

func fixture(t *testing.T) (*domain.Store, *testEngine) {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "mcp.sqlite"))
	requireOK(t, err)
	t.Cleanup(func() { store.Close() })
	addAgent(t, store, domain.Agent{ID: "active", Name: "Active", PrincipalID: activePrincipalID, DefaultAccess: domain.AccessNone}, activeToken, 0o600)
	return store, &testEngine{}
}

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func addAgent(t *testing.T, store *domain.Store, agent domain.Agent, token string, mode os.FileMode) {
	t.Helper()
	agent.KeyFile = filepath.Join(t.TempDir(), "key")
	requireOK(t, os.WriteFile(agent.KeyFile, []byte(token+"\n"), mode))
	requireOK(t, store.UpsertAgent(t.Context(), agent))
}

func newTestServer(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	server, err := NewServer(cfg)
	requireOK(t, err)
	return server.Handler()
}

func request(h http.Handler, method, path, header, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if header != "" {
		r.Header.Set(header, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func status(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("HTTP %d, want %d: %s", rec.Code, want, rec.Body)
	}
}

func TestAuthenticatedDiscoveryFailClosed(t *testing.T) {
	store, eng := fixture(t)
	addAgent(t, store, domain.Agent{ID: "archived", Name: "Archived", Archived: true}, "archived-secret", 0o600)
	addAgent(t, store, domain.Agent{ID: "retired", Name: "Retired", RetiredAt: " 2026-11-26 "}, "retired-secret", 0o600)
	addAgent(t, store, domain.Agent{ID: "insecure", Name: "Insecure"}, "insecure-secret", 0o644)
	path := filepath.Join(t.TempDir(), "symlink")
	agent, err := store.GetAgent(t.Context(), "archived")
	requireOK(t, err)
	requireOK(t, os.Symlink(agent.KeyFile, path))
	requireOK(t, store.UpsertAgent(t.Context(), domain.Agent{ID: "link", Name: "Link", KeyFile: path}))
	if _, err := readSecret(path); err == nil {
		t.Fatal("followed a key symlink")
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})
	for _, tc := range []struct {
		api, token string
		want       int
	}{
		{"", "", 401}, {"X-Api-Key", "unknown", 401}, {"Authorization", "Basic " + activeToken, 401},
		{"Authorization", "Bearer " + activeToken, 200}, {"X-Api-Key", activeToken, 200},
		{"X-Api-Key", "archived-secret", 403}, {"X-Api-Key", "retired-secret", 403}, {"X-Api-Key", "insecure-secret", 401},
	} {
		t.Run(tc.token, func(t *testing.T) {
			rec := request(h, http.MethodGet, "/mcp", tc.api, tc.token, "")
			status(t, rec, tc.want)
			if tc.want == 200 {
				raw := rec.Body.String()
				for _, key := range []string{`"agent_id":"active"`, `"engine":"test-engine"`, `"protocolVersion"`, `"capabilities"`, `"serverInfo"`} {
					if !strings.Contains(raw, key) {
						t.Fatalf("discovery missing %s: %s", key, raw)
					}
				}
			}
		})
	}
	status(t, request(h, http.MethodPost, "/mcp", "", "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`), 401)
	requireOK(t, store.Close())
	status(t, request(h, http.MethodGet, "/mcp", "X-Api-Key", activeToken, ""), 503)
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func rpc(t *testing.T, h http.Handler, body string) rpcReply {
	t.Helper()
	rec := request(h, http.MethodPost, "/", "X-Api-Key", activeToken, body)
	status(t, rec, 200)
	var reply rpcReply
	requireOK(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	if reply.JSONRPC != "2.0" || len(reply.ID) == 0 {
		t.Fatalf("invalid envelope: %s", rec.Body)
	}
	return reply
}

func TestJSONRPCProtocol(t *testing.T) {
	store, eng := fixture(t)
	h := newTestServer(t, Config{Store: store, Engine: eng})
	for _, tc := range []struct {
		input  string
		result string
		code   int
	}{
		{`{"jsonrpc":"2.0","id":"one","method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, ProtocolVersion, 0},
		{`{"jsonrpc":"2.0","id":2,"method":"notifications/initialized"}`, `{}`, 0},
		{`{"jsonrpc":"2.0","id":3,"method":"ping"}`, `{}`, 0},
		{`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, `"inputSchema"`, 0},
		{`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"health","arguments":{}}}`, `"test-engine"`, 0},
		{`{"jsonrpc":"2.0",`, "", -32700},
		{`{"jsonrpc":"1.0","id":5,"method":"ping"}`, "", -32600},
		{`{"jsonrpc":"2.0","id":{},"method":"ping"}`, "", -32600},
		{`{"jsonrpc":"2.0","id":6,"method":"unknown"}`, "", -32601},
		{`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"forget","arguments":{"item_id":0}}}`, "", -32602},
		{`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"missing"}}`, "", -32602},
	} {
		reply := rpc(t, h, tc.input)
		if tc.code != 0 {
			if reply.Error == nil || reply.Error.Code != tc.code {
				t.Fatalf("%s returned %+v, want %d", tc.input, reply, tc.code)
			}
			continue
		}
		if reply.Error != nil || !strings.Contains(string(reply.Result), tc.result) {
			t.Fatalf("%s got %+v", tc.input, reply)
		}
	}
	notice := request(h, http.MethodPost, "/mcp", "Authorization", "Bearer "+activeToken, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	status(t, notice, 202)
	if notice.Body.Len() != 0 {
		t.Fatal("notification had a response body")
	}
	status(t, request(h, http.MethodPost, "/mcp", "X-Api-Key", activeToken, strings.Repeat(" ", requestLimit+1)), 413)
	reply := rpc(t, h, `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`)
	if string(reply.ID) != "9007199254740993" {
		t.Fatalf("lost request ID precision: %s", reply.ID)
	}
	reply = rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list struct{ Tools []toolDefinition }
	requireOK(t, json.Unmarshal(reply.Result, &list))
	if len(list.Tools) != 3 {
		t.Fatalf("wrong tool list (expected 3 without forget): %+v", list)
	}
	for _, tool := range list.Tools {
		if tool.Name == "forget" {
			t.Fatalf("forget tool must not be exposed via MCP: %+v", list.Tools)
		}
	}
}

func callTool(t *testing.T, h http.Handler, name, args string) (json.RawMessage, string) {
	t.Helper()
	reply := rpc(t, h, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, args))
	if reply.Error != nil {
		t.Fatalf("tools/call %s %s: %+v", name, args, reply.Error)
	}
	var result struct {
		IsError           bool
		StructuredContent json.RawMessage
		Content           []struct{ Type, Text string }
	}
	requireOK(t, json.Unmarshal(reply.Result, &result))
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("invalid MCP tool result: %s", reply.Result)
	}
	if result.IsError {
		return nil, result.Content[0].Text
	}
	if result.StructuredContent == nil {
		t.Fatalf("no structured output: %s", reply.Result)
	}
	return result.StructuredContent, ""
}

func allow(t *testing.T, store *domain.Store, kind, id string, access domain.AccessLevel) {
	t.Helper()
	requireOK(t, store.SetACLOverride(t.Context(), domain.ACLOverride{TargetKind: kind, TargetID: id, AgentID: "active", Access: &access}))
}

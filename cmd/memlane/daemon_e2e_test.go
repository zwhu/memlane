package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine/cognee"
)

const engineHarness = `
from contextlib import asynccontextmanager
import json
from pathlib import Path
import sys
from uuid import uuid4

from fastapi import FastAPI
import uvicorn
from engines.cognee.engine_api import CogneeBackend, install_engine_api
from engines.cognee.handoff import ModelHandoffController
from engines.cognee.runner import SetupOnlyMiddleware, _configure_store, scrub_environment

scrub_environment()
_configure_store({}, Path(sys.argv[2]))

class Backend(CogneeBackend):
    def __init__(self):
        super().__init__()
        self.items = {}

    # Use the pinned SDK for ACLs and trace capture. Keep QA indexing, extraction
    # and search deterministic without downloading a model.

    async def ingest(self, payload, content):
        if payload.kind == 'capture_trace':
            return await super().ingest(payload, content)
        await self._dataset(payload.scope, 'write', allow_provision=True)
        ds, doc = payload.scope.dataset_id, str(uuid4())
        snippet = json.dumps(content) if isinstance(content, dict) else content
        self.items.setdefault(ds, {})[doc] = {
            'id': doc, 'dataset_id': ds, 'title': payload.kind,
            'snippet': snippet, 'content': snippet, 'ready': payload.kind == 'document',
        }
        return {'entry_id': doc, 'document_id': doc}

    async def extract(self, payload):
        await self._dataset(payload.scope, 'write', allow_provision=True)
        assert payload.expected_model_digest == handoff.active_digest
        if payload.doc_ids == ['fresh-session']:
            return {'touched_doc_ids': []}
        for doc in payload.doc_ids:
            self.items[payload.scope.dataset_id][doc]['ready'] = True
        return {'touched_doc_ids': payload.doc_ids}

    async def search(self, payload, limit):
        for scope in payload.scopes:
            await self._dataset(scope)
        return [{'dataset_id': scope.dataset_id, 'document_id': item['id'],
                 'title': item['title'], 'snippet': item['snippet'], 'score': 1.0}
                for scope in payload.scopes
                for item in self.items.get(scope.dataset_id, {}).values()
                if item['ready'] and payload.query.lower() in item['snippet'].lower()][:limit]

class HarnessHandoff(ModelHandoffController):
    status_reads = 0

    def status(self):
        self.status_reads += 1
        return super().status()

    async def apply(self, operation_id, generation_config, *, output_language=None):
        key = generation_config.get('api_key', '')
        assert len(key) == 64 and all(c in '0123456789abcdef' for c in key)
        assert generation_config['endpoint'] == sys.argv[3] + '/v1'
        assert 'temperature' not in generation_config
        assert 'max_output_tokens' not in generation_config
        assert output_language == 'zh-CN' and operation_id.startswith('startup-')
        return await super().apply(operation_id, generation_config, output_language=output_language)

handoff = HarnessHandoff(background=lambda: [], factory=lambda env: env)

@asynccontextmanager
async def lifespan(app):
    from cognee.modules.engine.operations.setup import setup
    await setup()
    try:
        yield
    finally:
        await handoff.close()

app = FastAPI(lifespan=lifespan)
app.add_middleware(SetupOnlyMiddleware, is_setup_only=lambda: handoff.mode == 'setup_ready')
install_engine_api(app, handoff=handoff, store_path=sys.argv[2], token_file=sys.argv[1], backend=Backend())
uvicorn.run(app, fd=3, access_log=False, log_level='warning')
`

func TestLiveGoPythonGatewayE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := filepath.Abs("../..")
	require(t, err)
	python := filepath.Join(root, ".local", "cognee-python", "bin", "python")
	if _, err := os.Stat(python); errors.Is(err, os.ErrNotExist) {
		if _, linkErr := os.Lstat(python); errors.Is(linkErr, os.ErrNotExist) {
			t.Skip("managed Python executable does not exist")
		}
		t.Fatalf("managed Python cannot be resolved: %v", err)
	} else {
		require(t, err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	base := "http://" + listener.Addr().String()
	require(t, listener.Close())
	engineURL, engineKey := launchPythonHarness(t, ctx, client, python, root, base)
	storage := t.TempDir()
	official := filepath.Join(storage, "official-store")
	const dataset = "11111111-1111-4111-8111-111111111111"
	workspace := t.TempDir()
	seedOfficialCatalog(t, official, dataset)
	registry, err := json.Marshal(map[string]any{"schema_version": 2, "projects": map[string]any{
		"project": map[string]any{"name": "Live Project", "dataset_id": dataset, "aliases": []string{workspace}, "auto_capture": true},
	}})
	require(t, err)
	require(t, os.WriteFile(filepath.Join(storage, "project-registry.json"), registry, 0o600))
	require(t, os.WriteFile(filepath.Join(storage, "generation.json"), []byte(`{
  "schema_version":2,"revision":3,"active":{"id":"bridge","revision":1},"startup_error":"old_failure",
  "profiles":{"bridge":[{"revision":1,"digest":"legacy-digest","profile":{"mode":"agent_bridge","bridge_kind":"codex",
   "model":"test-model"}}]}
 }`), 0o600))
	const management = "management-token-for-loopback-e2e"
	const agentKey = "private-agent-token-for-loopback-e2e"
	agentFile := filepath.Join(storage, "agent.key")
	managementFile := filepath.Join(storage, "management.key")
	require(t, os.WriteFile(agentFile, []byte(agentKey+"\n"), 0o600))
	require(t, os.WriteFile(managementFile, []byte(management+"\n"), 0o600))
	rawEngineKey, err := os.ReadFile(engineKey)
	require(t, err)
	internal := func(method, path string, payload any, status int) map[string]any {
		return liveRequest(t, ctx, client, engineURL, method, "/internal/v1"+path, "Authorization", "Bearer "+strings.TrimSpace(string(rawEngineKey)), payload, status)
	}
	if h := internal("GET", "/health", nil, 200); h["mode"] != "setup_ready" {
		t.Fatal(h)
	}
	internal("POST", "/search", map[string]any{}, 503)
	daemonCtx, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		finished <- serveDaemon(daemonCtx, runtimeConfig{Path: filepath.Join(storage, "runtime.json"), StoreRoot: storage, OfficialData: official, EngineURL: engineURL,
			EngineTokenFile: engineKey, ManagementTokenFile: managementFile, GatewayURL: base, raw: []byte("{}")}, true, io.Discard)
	}()
	stopped := false
	defer func() {
		stop()
		if stopped {
			return
		}
		select {
		case err := <-finished:
			require(t, err)
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down")
		}
	}()
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/health", nil)
		require(t, err)
		req.Header.Set("Authorization", "Bearer "+management)
		if res, err := client.Do(req); err == nil {
			require(t, res.Body.Close())
			if res.StatusCode == 200 {
				break
			}
		}
		select {
		case err := <-finished:
			stopped = true
			t.Fatalf("daemon stopped during startup: %v", err)
		case <-ctx.Done():
			t.Fatal("daemon did not begin serving")
		case <-time.After(25 * time.Millisecond):
		}
	}
	db, err := domain.OpenStore(filepath.Join(storage, "memlane.sqlite3"))
	require(t, err)
	defer func() { require(t, db.Close()) }()
	state, err := db.GetGenerationState(ctx)
	require(t, err)
	model := internal("GET", "/health", nil, 200)
	digest, _ := model["model_digest"].(string)
	if state.ActiveProfileID != "bridge" || state.StartupError != "" || model["mode"] != "ready" ||
		digest == "" || digest == "legacy-digest" {
		t.Fatalf("startup model restore: state=%+v model=%+v", state, model)
	}

	manage := func(method, path string, body any, status int) map[string]any {
		return liveRequest(t, ctx, client, base, method, path, "Authorization", "Bearer "+management, body, status)
	}
	agent := func(path string, body any, status int) map[string]any {
		return liveRequest(t, ctx, client, base, "POST", path, "X-Api-Key", agentKey, body, status)
	}
	health := func() {
		t.Helper()
		h := manage("GET", "/api/health", nil, 200)
		if h["ok"] != true || h["engine_ok"] != true || h["status"] != "ready" {
			t.Fatal(h)
		}
		monitor := manage("GET", "/api/runtime/monitor", nil, 200)
		engine := asObject(t, monitor["engine"])
		if engine["ok"] != true || engine["engine"] != "cognee" {
			t.Fatal(engine)
		}
	}
	health()
	initial := manage("GET", "/api/overview/bootstrap", nil, 200)
	projected := asObject(t, initial["projects"].([]any)[0])
	if initial["total_knowledge_count"] != float64(1) || projected["knowledge_count"] != float64(1) || projected["session_count"] != float64(1) {
		t.Fatal(initial)
	}

	const common = "33333333-3333-4333-8333-333333333333"
	require(t, db.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "common", Name: "Common", DatasetID: common, DatasetName: "Common"}))
	registered := manage("POST", "/api/agents", map[string]any{"id": "agent", "kind": "codex", "name": "Test Codex", "key_file": agentFile}, 201)
	if registered["principal_id"] != "" {
		t.Fatal(registered)
	}
	manage("PUT", "/api/projects/project/acl/agent", map[string]any{"access": "maintain", "capture": true}, 200)
	manage("PUT", "/api/common/acl/agent", map[string]any{"access": "read"}, 200)
	commonDoc := manage("POST", "/api/common", map[string]any{"common_category": "experience", "title": "Common lesson", "content": "shared needle"}, 201)
	if commonDoc["dataset_id"] != common {
		t.Fatal(commonDoc)
	}
	accepted := agent("/ingest", map[string]any{"kind": "capture_qa", "workspace": workspace, "session_id": "session",
		"message_id": "turn-1", "content": map[string]string{"question": "Where is the needle?", "answer": "Project lesson"}}, 202)
	waitForCapture := func(accepted map[string]any, datasetID string) {
		t.Helper()
		inboxID, ok := accepted["inbox_id"].(string)
		if !ok || inboxID == "" {
			t.Fatal(accepted)
		}
		for {
			item, err := db.GetInboxItem(ctx, inboxID)
			require(t, err)
			if item.Status == "succeeded" {
				if item.DatasetID != datasetID {
					t.Fatalf("capture reached dataset %q, want %q", item.DatasetID, datasetID)
				}
				break
			}
			if item.Status == "failed" {
				t.Fatalf("capture failed: %s", item.LastError)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("capture did not complete: %+v", item)
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	waitForCapture(accepted, dataset)

	overview := manage("GET", "/api/overview/bootstrap", nil, 200)
	projects, ok := overview["projects"].([]any)
	if !ok || len(projects) != 1 || asObject(t, projects[0])["project_id"] != "project" || asObject(t, overview["inbox"])["succeeded"] != float64(1) {
		t.Fatal(overview)
	}
	health()
	if model := internal("GET", "/model/status", nil, 200); model["output_language"] != "zh-CN" || model["model_digest"] != digest {
		t.Fatal(model)
	}
	recall := agent("/recall", map[string]any{"workspace": workspace, "session_id": "session", "query": "needle", "channel": "automatic", "limit": 10}, 200)
	hits, ok := recall["hits"].([]any)
	if !ok || recall["common_included"] != true || len(hits) != 2 {
		t.Fatal(recall)
	}
	seen := map[string]bool{}
	for _, hit := range hits {
		h := asObject(t, hit)
		ds, doc := fmt.Sprint(h["dataset_id"]), fmt.Sprint(h["document_id"])
		seen[ds] = true
		tally, err := db.GetRecallDocCount(ctx, ds, doc)
		require(t, err)
		if tally.TotalHits != 1 {
			t.Fatal(tally)
		}
	}
	if !seen[dataset] || !seen[common] {
		t.Fatal(recall)
	}

	rpc := func(method string, params map[string]any) map[string]any {
		t.Helper()
		payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
		if params != nil {
			payload["params"] = params
		}
		reply := agent("/mcp", payload, 200)
		if reply["error"] != nil || reply["jsonrpc"] != "2.0" {
			t.Fatal(reply)
		}
		return asObject(t, reply["result"])
	}
	tool := func(name string, args map[string]any) map[string]any {
		t.Helper()
		out := rpc("tools/call", map[string]any{"name": name, "arguments": args})
		if out["isError"] == true {
			t.Fatal(out)
		}
		return asObject(t, out["structuredContent"])
	}
	if rpc("initialize", nil)["protocolVersion"] != "2025-03-26" {
		t.Fatal("invalid initialize result")
	}
	if list, ok := rpc("tools/list", nil)["tools"].([]any); !ok || len(list) != 4 {
		t.Fatal(list)
	} else {
		seenTools := map[string]bool{}
		for _, raw := range list {
			name := fmt.Sprint(asObject(t, raw)["name"])
			if name == "forget" {
				t.Fatalf("MCP must not expose forget tool: %v", list)
			}
			seenTools[name] = true
		}
		for _, want := range []string{"remember", "recall", "health", "remember_status"} {
			if !seenTools[want] {
				t.Fatalf("MCP tools/list missing %q: %v", want, list)
			}
		}
	}
	remembered := tool("remember", map[string]any{"project_path": workspace, "content": "live MCP persisted document"})
	if remembered["status"] != "saved" || remembered["saved"] != true || fmt.Sprint(remembered["task_id"]) == "" {
		t.Fatalf("expected fast-path saved remember result: %v", remembered)
	}
	taskStatus := tool("remember_status", map[string]any{"task_id": remembered["task_id"]})
	if taskStatus["status"] != "succeeded" || taskStatus["saved"] != true || taskStatus["document_id"] != remembered["document_id"] {
		t.Fatalf("unexpected remember_status result: %v", taskStatus)
	}
	memory := tool("recall", map[string]any{"project_path": workspace, "query": "live MCP persisted document"})
	found, ok := memory["hits"].([]any)
	if !ok || len(found) != 1 || asObject(t, found[0])["document_id"] != remembered["document_id"] {
		t.Fatalf("remember=%v recall=%v", remembered, memory)
	}
	freshWorkspace := t.TempDir()
	fresh := manage("POST", "/api/projects", map[string]any{"id": "fresh", "name": "Fresh project", "workspace": freshWorkspace}, 201)
	freshDataset := fmt.Sprint(fresh["dataset_id"])
	if _, err := uuid.Parse(freshDataset); err != nil || freshDataset == dataset || freshDataset == common {
		t.Fatalf("expected a fresh dataset UUID: %v", fresh)
	}
	if denied := internal("GET", "/items?dataset_id="+freshDataset, nil, 403); denied["detail"] != "dataset_access_required" {
		t.Fatal(denied)
	}
	manage("PUT", "/api/projects/fresh/acl/agent", map[string]any{"access": "maintain", "capture": true, "auto_process": false}, 200)
	for _, capture := range []struct {
		kind    string
		content map[string]string
	}{
		{"capture_trace", map[string]string{"origin_function": "read_file", "status": "success"}},
		{"capture_qa", map[string]string{"question": "Fresh question", "answer": "Fresh answer"}},
	} {
		accepted := agent("/ingest", map[string]any{"kind": capture.kind, "workspace": freshWorkspace, "session_id": "fresh-session",
			"message_id": capture.kind, "content": capture.content}, 202)
		waitForCapture(accepted, freshDataset)
	}
	internal("GET", "/items?dataset_id="+freshDataset, nil, 200)
	freshRecall := tool("recall", map[string]any{"project_path": freshWorkspace, "query": "Fresh question"})
	freshHits, ok := freshRecall["hits"].([]any)
	if !ok || len(freshHits) != 1 || asObject(t, freshHits[0])["dataset_id"] != freshDataset {
		t.Fatal(freshRecall)
	}

	h := asObject(t, tool("health", map[string]any{})["engine"])
	if h["engine"] != "cognee" || h["ok"] != true {
		t.Fatal(h)
	}
	if _, ok := manage("GET", "/api/profiles", nil, 200)["profiles"].([]any); !ok {
		t.Fatal("profiles list missing")
	}
}

func TestStartupCatalogProjection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := domain.OpenStore(filepath.Join(dir, "memlane.sqlite3"))
	require(t, err)
	defer func() { require(t, db.Close()) }()
	const dataset = "11111111-1111-4111-8111-111111111111"
	require(t, db.UpsertProject(ctx, domain.Project{ID: "project", Name: "Project", DatasetID: dataset}))
	official := filepath.Join(dir, "official-store")
	engineKey := filepath.Join(dir, "engine.token")
	require(t, os.WriteFile(engineKey, []byte("projector-test-private-token"), 0o600))
	const management = "projector-startup-test-management-token"
	stack, err := MountGatewayStack(StackConfig{Store: db, ManagementToken: management,
		Engine: cognee.Config{Endpoint: "http://127.0.0.1:12345", TokenFile: engineKey, StorePath: official}})
	require(t, err)
	defer func() { require(t, stack.Profiles.Close(ctx)) }()

	// A managed dataset can be registered before the official DB exists.
	require(t, prepareGatewayStartup(ctx, stack.Gateway))
	seedOfficialCatalog(t, official, dataset)
	require(t, prepareGatewayStartup(ctx, stack.Gateway))
	docs, err := db.ListCatalogDocs(ctx, dataset, "knowledge", 10)
	require(t, err)
	sessions, err := db.ListCatalogSessions(ctx, dataset, 10)
	require(t, err)
	if len(docs) != 1 || len(sessions) != 1 || sessions[0].SessionID != "imported-session" {
		t.Fatalf("initial projection has %d knowledge documents and %d sessions", len(docs), len(sessions))
	}
	require(t, db.UpsertProject(ctx, domain.Project{ID: "profiles", Name: "Profiles", DatasetID: uuid.NewString(), DatasetName: "ml-user-profiles"}))
	get := func(path string, out any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+management)
		response := httptest.NewRecorder()
		stack.Gateway.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
		require(t, json.Unmarshal(response.Body.Bytes(), out))
	}
	var profile struct {
		Protected bool `json:"protected"`
	}
	get("/api/projects/profiles", &profile)
	if !profile.Protected {
		t.Fatal("project directory did not mark profiles as protected")
	}
	var overview struct {
		FirstUseDays int `json:"first_use_days"`
		Projects     []struct {
			ID        string `json:"project_id"`
			Protected bool   `json:"protected"`
		}
		RecentItems []struct{ Title string } `json:"recent_items"`
	}
	get("/api/overview/bootstrap?days=10", &overview)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	want := int(today.Sub(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))/(24*time.Hour)) + 1
	if overview.FirstUseDays != want || len(overview.Projects) != 2 ||
		overview.Projects[0].ID != "profiles" || !overview.Projects[0].Protected ||
		len(overview.RecentItems) != 1 || overview.RecentItems[0].Title != "Imported knowledge" {
		t.Fatalf("overview = %+v, want %d days and a readable title", overview, want)
	}
}

func seedOfficialCatalog(t *testing.T, official, dataset string) {
	t.Helper()
	name := filepath.Join(official, "system", "databases", "cognee_db")
	require(t, os.MkdirAll(filepath.Dir(name), 0o700))
	data, err := sql.Open("sqlite", name)
	require(t, err)
	defer func() { require(t, data.Close()) }()
	_, err = data.Exec(`
  CREATE TABLE datasets(id TEXT,name TEXT);
  CREATE TABLE data(id TEXT,dataset_id TEXT,name TEXT,system_metadata TEXT,external_metadata TEXT,node_set TEXT,
   pipeline_status TEXT,created_at DATETIME,updated_at DATETIME);
  CREATE TABLE session_records(dataset_id TEXT,session_id TEXT,user_id TEXT,status TEXT,started_at DATETIME,last_activity_at DATETIME);
 `)
	require(t, err)
	_, err = data.Exec("INSERT INTO datasets VALUES(?, 'project')", dataset)
	require(t, err)
	_, err = data.Exec(`INSERT INTO data VALUES('55555555-5555-4555-8555-555555555555',?,
  'text_1234567890abcdef1234567890abcdef.txt',
  '{"title":"Imported knowledge","lesson_type":"state","state_key":"initial","first_source_date":"2025-01-01"}','{}','["session_learnings"]','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, dataset)
	require(t, err)
	_, err = data.Exec(`INSERT INTO session_records VALUES(?, 'imported-session','77777777-7777-4777-8777-777777777777',
  'active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, dataset)
	require(t, err)
}

func launchPythonHarness(t *testing.T, ctx context.Context, client *http.Client, python, root, gatewayURL string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script, key, logPath := filepath.Join(dir, "engine.py"), filepath.Join(dir, "engine.key"), filepath.Join(dir, "engine.log")
	const token = "private-python-engine-e2e"
	require(t, os.WriteFile(script, []byte(engineHarness), 0o600))
	require(t, os.WriteFile(key, []byte(token+"\n"), 0o600))
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require(t, err)
	addr := listener.Addr().String()
	fd, err := listener.File()
	require(t, errors.Join(err, listener.Close()))
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	require(t, err)
	cmd := exec.Command(python, script, key, dir, gatewayURL)
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(), "PYTHONPATH="+root, "PYTHONDONTWRITEBYTECODE=1", "LITELLM_LOCAL_MODEL_COST_MAP=true", "TELEMETRY_DISABLED=true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = []*os.File{fd}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Start()
	closeErr := errors.Join(fd.Close(), log.Close())
	require(t, err)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		select {
		case err := <-done:
			// Uvicorn may re-raise the handled SIGTERM after graceful shutdown.
			var exit *exec.ExitError
			if err != nil && (!errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			<-done
			t.Error("Python harness did not stop after SIGTERM")
		}
	})
	require(t, closeErr)
	endpoint := "http://" + addr
	ready, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ready, "GET", endpoint+"/internal/v1/health", nil)
		require(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := client.Do(req); err == nil {
			require(t, resp.Body.Close())
			if resp.StatusCode == 200 {
				return endpoint, key
			}
		}
		select {
		case <-ready.Done():
			output, err := os.ReadFile(logPath)
			t.Fatalf("Python engine did not become ready: %s (%v)", output, err)
		case <-time.After(30 * time.Millisecond):
		}
	}
}

func liveRequest(t *testing.T, ctx context.Context, client *http.Client, base, method, path, header, token string, body any, status int) map[string]any {
	t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require(t, err)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, payload)
	require(t, err)
	req.Header.Set(header, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require(t, err)
	if resp.StatusCode != status {
		t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, resp.StatusCode, status, strings.TrimSpace(string(raw)))
	}
	var out map[string]any
	require(t, json.Unmarshal(raw, &out))
	return out
}

func asObject(t *testing.T, value any) map[string]any {
	t.Helper()
	v, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %v", value)
	}
	return v
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

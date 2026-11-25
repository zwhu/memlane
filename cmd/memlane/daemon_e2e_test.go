package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine/cognee"
)

const engineHarness = `
from contextlib import asynccontextmanager
import json
import sys
from uuid import uuid4

from fastapi import FastAPI
import uvicorn
from engines.cognee.engine_api import install_engine_api
from engines.cognee.handoff import ModelHandoffController

class Backend:
    def __init__(self):
        self.items = {}

    async def ingest(self, payload, content):
        ds, doc = payload.scope.dataset_id, str(uuid4())
        snippet = json.dumps(content) if isinstance(content, dict) else content
        self.items.setdefault(ds, {})[doc] = {
            'id': doc, 'dataset_id': ds, 'title': payload.kind,
            'snippet': snippet, 'content': snippet, 'ready': payload.kind == 'document',
        }
        return {'entry_id': doc, 'document_id': doc}

    async def extract(self, payload):
        for doc in payload.doc_ids:
            self.items[payload.scope.dataset_id][doc]['ready'] = True
        return {'touched_doc_ids': payload.doc_ids}

    async def search(self, payload, limit):
        return [{'dataset_id': scope.dataset_id, 'document_id': item['id'],
                 'title': item['title'], 'snippet': item['snippet'], 'score': 1.0}
                for scope in payload.scopes
                for item in self.items.get(scope.dataset_id, {}).values()
                if item['ready'] and payload.query.lower() in item['snippet'].lower()][:limit]

    async def list_items(self, scope, limit, offset):
        return list(self.items.get(scope.dataset_id, {}).values())[offset:offset+limit]

    async def get_item(self, scope, item_id):
        return self.items[scope.dataset_id][item_id]

    async def delete_item(self, scope, item_id):
        del self.items[scope.dataset_id][item_id]
        return {'deleted': True}

    async def graph(self, dataset_id, filter_text):
        return {'nodes': [], 'edges': []}

handoff = ModelHandoffController(background=lambda: [])
handoff.mode, handoff.active_digest = 'ready', 'e2e-engine-digest'

@asynccontextmanager
async def lifespan(app):
    try:
        yield
    finally:
        await handoff.close()

app = FastAPI(lifespan=lifespan)
install_engine_api(app, handoff=handoff, store_path=sys.argv[2], token_file=sys.argv[1], backend=Backend())
uvicorn.run(app, fd=3, access_log=False, log_level='warning')
`

func TestLiveGoPythonGatewayE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	engineURL, engineKey := launchPythonHarness(t, ctx, client, python, root)
	storage := t.TempDir()
	db, err := domain.OpenStore(filepath.Join(storage, "memlane.sqlite3"))
	require(t, err)
	t.Cleanup(func() { require(t, db.Close()) })
	const management = "management-token-for-loopback-e2e"
	const agentKey = "private-agent-token-for-loopback-e2e"
	agentFile := filepath.Join(storage, "agent.key")
	require(t, os.WriteFile(agentFile, []byte(agentKey+"\n"), 0o600))
	stack, err := MountGatewayStack(StackConfig{Store: db, Engine: cognee.Config{Endpoint: engineURL, TokenFile: engineKey}, ManagementToken: management})
	require(t, err)
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require(t, errors.Join(stack.Gateway.Shutdown(stop), stack.Profiles.Close(stop)))
	})
	addr, err := stack.Gateway.StartLoopback("127.0.0.1:0")
	require(t, err)
	base := "http://" + addr
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
	workspace := t.TempDir()
	project := manage("POST", "/api/projects", map[string]any{"id": "project", "name": "Live Project", "aliases": []string{workspace}}, 201)
	dataset, ok := project["dataset_id"].(string)
	if !ok || dataset == "" {
		t.Fatal(project)
	}
	const common = "common-dataset"
	require(t, db.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "common", Name: "Common", DatasetID: common, DatasetName: "Common"}))
	manage("POST", "/api/agents", map[string]any{"id": "agent", "kind": "codex", "name": "Test Codex", "key_file": agentFile}, 201)
	manage("PUT", "/api/projects/project/acl/agent", map[string]any{"access": "maintain", "capture": true}, 200)
	manage("PUT", "/api/common/acl/agent", map[string]any{"access": "read"}, 200)
	commonDoc := manage("POST", "/api/common", map[string]any{"common_category": "experience", "title": "Common lesson", "content": "shared needle"}, 201)
	if commonDoc["dataset_id"] != common {
		t.Fatal(commonDoc)
	}
	accepted := agent("/ingest", map[string]any{"kind": "capture_qa", "workspace": workspace, "session_id": "session",
		"message_id": "turn-1", "content": map[string]string{"question": "Where is the needle?", "answer": "Project lesson"}}, 202)
	inboxID, ok := accepted["inbox_id"].(string)
	if !ok || inboxID == "" {
		t.Fatal(accepted)
	}
	for {
		item, err := db.GetInboxItem(ctx, inboxID)
		require(t, err)
		if item.Status == "succeeded" {
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
	overview := manage("GET", "/api/overview/bootstrap", nil, 200)
	projects, ok := overview["projects"].([]any)
	if !ok || len(projects) != 1 || asObject(t, projects[0])["project_id"] != "project" || asObject(t, overview["inbox"])["succeeded"] != float64(1) {
		t.Fatal(overview)
	}
	health()
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
	}
	remembered := tool("remember", map[string]any{"project_path": workspace, "content": "live MCP persisted document"})
	memory := tool("recall", map[string]any{"project_path": workspace, "query": "live MCP persisted document"})
	found, ok := memory["hits"].([]any)
	if !ok || len(found) != 1 || asObject(t, found[0])["document_id"] != remembered["document_id"] {
		t.Fatalf("remember=%v recall=%v", remembered, memory)
	}
	h := asObject(t, tool("health", map[string]any{})["engine"])
	if h["engine"] != "cognee" || h["ok"] != true {
		t.Fatal(h)
	}
	if _, ok := manage("GET", "/api/profiles", nil, 200)["profiles"].([]any); !ok {
		t.Fatal("profiles list missing")
	}
}

func launchPythonHarness(t *testing.T, ctx context.Context, client *http.Client, python, root string) (string, string) {
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
	cmd := exec.Command(python, script, key, dir)
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(), "PYTHONPATH="+root, "PYTHONDONTWRITEBYTECODE=1")
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
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
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

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func configureScopes(t *testing.T, store *domain.Store) {
	t.Helper()
	for _, p := range []domain.Project{
		{ID: "write", Name: "Write", DatasetID: "write-dataset", CodeDatasetID: "write-code", CodeEnabled: true, Aliases: []string{"/tmp/mcp/write"}},
		{ID: "read", Name: "Read", DatasetID: "read-dataset", CodeDatasetID: "disabled-code"},
		{ID: "none", Name: "Denied", DatasetID: "hidden-dataset", CodeDatasetID: "hidden-code", CodeEnabled: true},
		{ID: "protected", Name: "Protected", DatasetID: "private-profiles", DatasetName: "ml-user-profiles"},
	} {
		requireOK(t, store.UpsertProject(t.Context(), p))
	}
	allow(t, store, "project", "write", domain.AccessMaintain)
	allow(t, store, "project", "read", domain.AccessRead)
	allow(t, store, "project", "protected", domain.AccessMaintain)
	requireOK(t, store.UpsertCommonConfig(t.Context(), domain.CommonConfig{ID: "common", Name: "Common", DatasetID: "common-dataset"}))
}

func TestToolACLAndRememberReceipts(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	ctx := t.Context()
	var ingests []engine.IngestRequest
	eng.ingest = func(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		ingests = append(ingests, req)
		if req.IdempotencyKey == "abort" {
			return engine.IngestResult{}, engine.ErrUnavailable
		}
		r, err := store.GetCaptureReceipt(ctx, req.IdempotencyKey)
		if err != nil || r.Status != "pending" {
			t.Errorf("engine invoked without pending receipt: %+v (%v)", r, err)
		}
		return engine.IngestResult{OK: true, DocumentID: "entry"}, nil
	}
	var deleted string
	eng.delete = func(_ context.Context, scope engine.Scope, id string) error {
		if scope != (engine.Scope{DatasetID: "write-dataset", UserID: activePrincipalID}) {
			t.Errorf("delete scope: %+v", scope)
		}
		deleted = id
		return nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})
	for _, project := range []string{"none", "read", "protected"} {
		args := fmt.Sprintf(`{"project_id":%q,"content":"remember it"}`, project)
		if _, msg := callTool(t, h, "remember", args); msg != "Access denied" {
			t.Fatalf("remember %s: %s", project, msg)
		}
	}
	for _, project := range []string{"none", "protected"} {
		if _, msg := callTool(t, h, "recall", fmt.Sprintf(`{"project_id":%q,"query":"q"}`, project)); msg != "Access denied" {
			t.Fatalf("recall %s: %s", project, msg)
		}
	}
	if _, msg := callTool(t, h, "remember", `{"project_id":"write","auto_capture":true,"content":"blocked"}`); msg != "Access denied" || len(ingests) != 0 {
		t.Fatalf("auto capture should be denied: %q, %+v", msg, ingests)
	}

	const input = `{"project_path":"/tmp/mcp/write/subdir","content":" \nwrite it ","idempotency_key":"save","session_id":"sess","message_id":"msg","context_date":"2026-11-26","metadata":{"origin":"cli"}}`
	data, msg := callTool(t, h, "remember", input)
	if msg != "" || !strings.Contains(string(data), `"entry_id":"entry"`) || len(ingests) != 1 {
		t.Fatalf("explicit remember: %s %q, %+v", data, msg, ingests)
	}
	req := ingests[0]
	if req.Scope != (engine.Scope{DatasetID: "write-dataset", UserID: activePrincipalID}) || req.Kind != engine.IngestKindDocument ||
		req.Content != " \nwrite it " || req.SourceMeta.SessionID != "sess" || req.SourceMeta.ContextDate != "2026-11-26" ||
		req.SourceMeta.ExternalMetadata["origin"] != "cli" {
		t.Fatalf("ingest request: %+v", req)
	}
	r, err := store.GetCaptureReceipt(ctx, "save")
	if err != nil || r.Status != "saved" || r.EntryID != "entry" || r.MessageID != "msg" {
		t.Fatalf("receipt %+v (%v)", r, err)
	}
	data, msg = callTool(t, h, "remember", input)
	if msg != "" || !strings.Contains(string(data), `"deduplicated":true`) || len(ingests) != 1 {
		t.Fatalf("remember replay: %s %q, %+v", data, msg, ingests)
	}
	if _, msg := callTool(t, h, "remember", strings.Replace(input, "write it", "change it", 1)); !strings.Contains(msg, "conflicts") {
		t.Fatalf("changed replay: %s", msg)
	}
	if _, msg := callTool(t, h, "remember", `{"project_id":"write","content":"fails","idempotency_key":"abort"}`); msg != "Engine unavailable" {
		t.Fatalf("engine error: %s", msg)
	}
	if _, err := store.GetCaptureReceipt(ctx, "abort"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("failed receipt was not aborted: %v", err)
	}
	forgetReply := rpc(t, h, `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"forget","arguments":{"project_id":"write","item_id":"item"}}}`)
	if forgetReply.Error == nil || forgetReply.Error.Code != -32602 || deleted != "" {
		t.Fatalf("forget should be rejected as unknown tool without invoking delete: reply=%+v deleted=%q", forgetReply, deleted)
	}
}

func TestToolsUseOwnerForInvalidPrincipal(t *testing.T) {
	for _, principal := range []string{"", "not-a-uuid", " " + activePrincipalID + " "} {
		t.Run("principal="+principal, func(t *testing.T) {
			store, eng := fixture(t)
			agent := domain.Agent{ID: "active", Name: "Active", PrincipalID: principal}
			addAgent(t, store, agent, activeToken, 0o600)
			requireOK(t, store.UpsertProject(t.Context(), domain.Project{ID: "write", Name: "Write", DatasetID: "write-dataset"}))
			allow(t, store, "project", "write", domain.AccessMaintain)

			want := engine.Scope{DatasetID: "write-dataset"}
			receiptUser := agent.ID
			if principal == " "+activePrincipalID+" " {
				want.UserID, receiptUser = activePrincipalID, activePrincipalID
			}
			var captureKey string
			var ingested, deleted engine.Scope
			var searched []engine.Scope
			eng.ingest = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
				ingested, captureKey = req.Scope, req.IdempotencyKey
				return engine.IngestResult{OK: true, DocumentID: "entry"}, nil
			}
			eng.search = func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
				searched = req.Scopes
				return engine.SearchResult{OK: true}, nil
			}
			eng.delete = func(_ context.Context, scope engine.Scope, _ string) error {
				deleted = scope
				return nil
			}

			h := newTestServer(t, Config{Store: store, Engine: eng})
			if _, msg := callTool(t, h, "remember", `{"project_id":"write","content":"note","session_id":"session","message_id":"message"}`); msg != "" || ingested != want {
				t.Fatalf("remember: %q, scope %+v, want %+v", msg, ingested, want)
			}
			identity, err := json.Marshal([]string{receiptUser, want.DatasetID, "session", "message", string(engine.IngestKindDocument)})
			requireOK(t, err)
			receipt, err := store.GetCaptureReceipt(t.Context(), captureKey)
			requireOK(t, err)
			if captureKey != "mcp:"+hash(identity) || receipt.UserID != receiptUser {
				t.Fatalf("receipt = %+v, want user %q and a key scoped to that user", receipt, receiptUser)
			}
			if _, msg := callTool(t, h, "recall", `{"project_id":"write","query":"note"}`); msg != "" || !slices.Equal(searched, []engine.Scope{want}) {
				t.Fatalf("recall: %q, scopes %+v, want %+v", msg, searched, want)
			}
			forgetReply := rpc(t, h, `{"jsonrpc":"2.0","id":98,"method":"tools/call","params":{"name":"forget","arguments":{"project_id":"write","item_id":"entry"}}}`)
			if forgetReply.Error == nil || forgetReply.Error.Code != -32602 || deleted != (engine.Scope{}) {
				t.Fatalf("forget should be rejected as unknown tool: reply=%+v deleted=%+v", forgetReply, deleted)
			}
			data, msg := callTool(t, h, "health", "{}")
			if msg != "" {
				t.Fatal(msg)
			}
			var health struct {
				Scopes []authorizedScope
			}
			requireOK(t, json.Unmarshal(data, &health))
			if len(health.Scopes) != 1 || health.Scopes[0].Scope != want {
				t.Fatalf("health: scopes %+v, want %+v", health.Scopes, want)
			}
		})
	}
}

func TestRecallAuthorizationFilteringAndTelemetry(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	var searches []engine.SearchRequest
	var observations []RecallObservation
	eng.search = func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
		searches = append(searches, req)
		return engine.SearchResult{OK: true, Hits: []engine.SearchHit{
			{DatasetID: "hidden-dataset", DocumentID: "hidden"},
			{DatasetID: "private-profiles", DocumentID: "profile"},
			{DatasetID: "read-dataset", DocumentID: "read-doc"},
			{DatasetID: "read-dataset", DocumentID: "read-doc"},
			{DatasetID: "common-dataset", DocumentID: "common-doc"},
			{DatasetID: "write-code", DocumentID: "code-doc"},
			{DatasetID: "write-dataset", DocumentID: "write-doc"},
		}}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng, OnRecall: func(obs RecallObservation) { observations = append(observations, obs) }})
	check := func(args string, datasets, documents []string, limit int) {
		t.Helper()
		data, msg := callTool(t, h, "recall", args)
		if msg != "" {
			t.Fatal(msg)
		}
		var result struct {
			Channel string
			Hits    []engine.SearchHit
		}
		requireOK(t, json.Unmarshal(data, &result))
		req := searches[len(searches)-1]
		gotScopes := make([]string, 0, len(req.Scopes))
		for _, scope := range req.Scopes {
			gotScopes = append(gotScopes, scope.DatasetID)
			if scope.UserID != activePrincipalID {
				t.Fatalf("wrong scope principal: %+v", scope)
			}
		}
		ids := make([]string, 0, len(result.Hits))
		for _, hit := range result.Hits {
			ids = append(ids, hit.DocumentID)
		}
		if result.Channel != "mcp" || !slices.Equal(gotScopes, datasets) || !slices.Equal(ids, documents) || req.Limit != limit {
			t.Fatalf("recall request %+v response %s, want datasets %v documents %v limit %d", req, data, datasets, documents, limit)
		}
	}
	check(`{"query":"text","project_id":"read"}`, []string{"read-dataset"}, []string{"read-doc"}, 10)
	allow(t, store, "common", "common", domain.AccessRead)
	check(`{"query":"text","project_id":"read","limit":100}`, []string{"read-dataset", "common-dataset"}, []string{"read-doc", "common-doc"}, 50)
	check(`{"query":"text","kind":"code"}`, []string{"write-code"}, []string{"code-doc"}, 10)
	check(`{"query":"text"}`, []string{"read-dataset", "write-dataset", "common-dataset"}, []string{"read-doc", "common-doc", "write-doc"}, 10)
	check(`{"query":"text","project_id":"write"}`, []string{"write-dataset", "common-dataset"}, []string{"common-doc", "write-doc"}, 10)
	if len(observations) != 5 || observations[1].Channel != "mcp" || !strings.HasPrefix(observations[1].EventID, "mcp:") ||
		observations[1].AgentID != "active" || len(observations[1].Hits) != 2 {
		t.Fatalf("observations %+v", observations)
	}
	count, err := store.GetRecallDocCount(t.Context(), "read-dataset", "read-doc")
	if err != nil || count.TotalHits != 3 || count.ExplicitHits != 3 || count.AutoHits != 0 {
		t.Fatalf("counts %+v (%v)", count, err)
	}
	stats, err := store.GetRecallQueryStats(t.Context())
	if err != nil || len(stats) != 1 || stats[0].Channel != "explicit" || stats[0].TotalQueries != 5 || stats[0].TotalHits != 9 {
		t.Fatalf("query stats %+v (%v)", stats, err)
	}
}

func TestRecallTimeoutAndHealthScopePrivacy(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	allow(t, store, "common", "common", domain.AccessRead)
	const timeout = 50 * time.Millisecond
	eng.search = func(ctx context.Context, _ engine.SearchRequest) (engine.SearchResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > timeout {
			t.Errorf("search deadline %v (%t)", deadline, ok)
			return engine.SearchResult{}, errors.New("missing deadline")
		}
		<-ctx.Done()
		return engine.SearchResult{OK: true}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng, SearchTimeout: timeout})
	if _, msg := callTool(t, h, "recall", `{"query":"slow"}`); msg != "Operation timed out" {
		t.Fatalf("timeout: %q", msg)
	}
	data, msg := callTool(t, h, "health", "{}")
	if msg != "" {
		t.Fatal(msg)
	}
	var health struct {
		Engine engine.HealthStatus
		Scopes []authorizedScope
	}
	requireOK(t, json.Unmarshal(data, &health))
	var ids []string
	for _, scope := range health.Scopes {
		ids = append(ids, scope.DatasetID)
	}
	if !slices.Equal(ids, []string{"read-dataset", "write-dataset", "write-code", "common-dataset"}) ||
		!health.Engine.OK || health.Engine.StorePath != "" {
		t.Fatalf("health data: %s", data)
	}
	discovery := request(h, http.MethodGet, "/mcp", "X-Api-Key", activeToken, "")
	status(t, discovery, 200)
	for _, leak := range []string{"hidden", "private-profiles", "ml-user-profiles", "/private/storage", "write-dataset"} {
		if strings.Contains(discovery.Body.String(), leak) {
			t.Fatalf("discovery leaked %s: %s", leak, discovery.Body)
		}
	}
	allow(t, store, "common", "common", domain.AccessNone)
	data, msg = callTool(t, h, "health", "{}")
	if msg != "" || strings.Contains(string(data), "common-dataset") {
		t.Fatalf("health exposed common without permission: %s %s", data, msg)
	}
}

func TestRememberCaptureKinds(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	_, err := store.SetProjectAutoCapture(t.Context(), "write", true)
	requireOK(t, err)
	var ingests []engine.IngestRequest
	eng.ingest = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		ingests = append(ingests, req)
		return engine.IngestResult{OK: true, EntryID: string(req.Kind)}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})
	for _, kind := range []engine.IngestKind{engine.IngestKindCaptureQA, engine.IngestKindCaptureTrace} {
		const payload = `{"text":"record"}`
		args := fmt.Sprintf(`{"project_id":"write","kind":%q,"content":%q,"auto_capture":true,"idempotency_key":%q}`, kind, payload, kind)
		if _, msg := callTool(t, h, "remember", args); msg != "" {
			t.Fatalf("%s capture: %q", kind, msg)
		}
		req := ingests[len(ingests)-1]
		raw, ok := req.Content.(json.RawMessage)
		if !ok || string(raw) != payload || req.Kind != kind || req.SourceMeta.SessionID != string(kind) {
			t.Fatalf("capture request: %+v", req)
		}
		r, err := store.GetCaptureReceipt(t.Context(), string(kind))
		if err != nil || r.Status != "saved" || r.EntryType != string(kind) {
			t.Fatalf("capture receipt: %+v (%v)", r, err)
		}
	}
}

func TestRecallModesAndAuthorizedGraphContext(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	allow(t, store, "common", "common", domain.AccessRead)

	var requests []engine.SearchRequest
	var nextGC *engine.GraphSearchContext
	eng.search = func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
		requests = append(requests, req)
		return engine.SearchResult{
			OK: true,
			Hits: []engine.SearchHit{
				{DatasetID: "hidden-dataset", DocumentID: "secret"},
				{DatasetID: "write-dataset", DocumentID: "doc-1", Title: "Note", Snippet: "Body"},
				{DatasetID: "write-code", DocumentID: "code-1", Title: "Code", Snippet: "Func"},
			},
			GraphContext: nextGC,
		}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})

	// 1. Tool schema advertises recall mode enum and default hybrid.
	listReply := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list struct{ Tools []toolDefinition }
	requireOK(t, json.Unmarshal(listReply.Result, &list))
	foundModeSchema := false
	for _, tool := range list.Tools {
		if tool.Name != "recall" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		modeProp, _ := props["mode"].(map[string]any)
		if modeProp["default"] != "hybrid" {
			t.Fatalf("recall mode schema default = %+v, want hybrid", modeProp)
		}
		foundModeSchema = true
	}
	if !foundModeSchema {
		t.Fatal("recall tool schema missing mode property")
	}

	// 2. Default mode is hybrid; kind=code defaults to code; explicit modes pass through.
	for _, tc := range []struct {
		args      string
		wantMode  engine.SearchMode
		wantScope string
	}{
		{`{"project_id":"write","query":"auth"}`, engine.SearchModeHybrid, "write-dataset"},
		{`{"project_id":"write","query":"auth","kind":"code"}`, engine.SearchModeCode, "write-code"},
		{`{"project_id":"write","query":"auth","mode":"graph"}`, engine.SearchModeGraph, "write-dataset"},
		{`{"project_id":"write","query":"auth","mode":"chunks"}`, engine.SearchModeChunks, "write-dataset"},
		{`{"project_id":"write","query":"auth","mode":"code"}`, engine.SearchModeCode, "write-code"},
	} {
		data, msg := callTool(t, h, "recall", tc.args)
		if msg != "" {
			t.Fatalf("%s failed: %s", tc.args, msg)
		}
		req := requests[len(requests)-1]
		if req.Mode != tc.wantMode || len(req.Scopes) == 0 || req.Scopes[0].DatasetID != tc.wantScope {
			t.Fatalf("%s -> req=%+v, want mode=%q firstScope=%q", tc.args, req, tc.wantMode, tc.wantScope)
		}
		var out map[string]any
		requireOK(t, json.Unmarshal(data, &out))
		if out["mode"] != string(tc.wantMode) || out["degraded_to_chunks"] != false {
			t.Fatalf("%s -> out=%+v", tc.args, out)
		}
		if _, hasGC := out["graph_context"]; hasGC {
			t.Fatalf("%s should omit empty graph_context: %s", tc.args, data)
		}
	}

	// 3. Invalid mode returns JSON-RPC Invalid params (-32602) without invoking engine.
	beforeInvalid := len(requests)
	badReply := rpc(t, h, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"recall","arguments":{"project_id":"write","query":"auth","mode":"invalid"}}}`)
	if badReply.Error == nil || badReply.Error.Code != -32602 {
		t.Fatalf("invalid mode reply = %+v, want -32602", badReply)
	}
	if len(requests) != beforeInvalid {
		t.Fatal("invalid mode reached engine")
	}

	// 4. GraphContext strictly filters unauthorized datasets including chunk/graph conflicts.
	nextGC = &engine.GraphSearchContext{
		Entities: []engine.GraphSearchEntity{
			{DatasetID: "hidden-dataset", Name: "HiddenEntity", Conflicts: []string{"hidden-only-conflict"}},
			{DatasetID: "private-profiles", Name: "ProfileEntity"},
			{DatasetID: "write-dataset", Name: "AllowedEntity", Edges: []engine.GraphSearchEdge{{Text: "AllowedEntity handles auth"}}},
		},
		Facts: []engine.GraphSearchFact{
			{DatasetID: "hidden-dataset", Text: "hidden fact"},
			{DatasetID: "write-dataset", Text: "allowed fact"},
		},
		Conflicts: []engine.GraphSearchConflict{
			{DatasetID: "hidden-dataset", Text: "hidden-only-conflict"},
			{DatasetID: "hidden-dataset", Text: "hidden-chunk-conflict"},
			{DatasetID: "", Text: "unscoped-conflict"},
			{DatasetID: "write-dataset", Text: "allowed-conflict"},
			{DatasetID: "write-dataset", Text: "allowed-conflict"},
		},
		CodeFacts: []engine.CodeSearchFact{
			{DatasetID: "hidden-code", Name: "hiddenFn"},
			{DatasetID: "write-dataset", Name: "allowedFn"},
		},
		DegradedToChunks: true,
	}
	data, msg := callTool(t, h, "recall", `{"project_id":"write","query":"auth","mode":"hybrid"}`)
	if msg != "" {
		t.Fatalf("recall with graph_context failed: %s", msg)
	}
	raw := string(data)
	for _, forbidden := range []string{"hidden-dataset", "HiddenEntity", "private-profiles", "ProfileEntity", "hidden fact", "hidden-only-conflict", "hidden-chunk-conflict", "unscoped-conflict", "hidden-code", "hiddenFn"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("MCP recall leaked unauthorized %q: %s", forbidden, raw)
		}
	}
	var out struct {
		Channel          string                     `json:"channel"`
		Mode             engine.SearchMode          `json:"mode"`
		DegradedToChunks bool                       `json:"degraded_to_chunks"`
		GraphContext     *engine.GraphSearchContext `json:"graph_context"`
	}
	requireOK(t, json.Unmarshal(data, &out))
	if out.Channel != "mcp" || out.Mode != engine.SearchModeHybrid || !out.DegradedToChunks || out.GraphContext == nil {
		t.Fatalf("unexpected MCP recall envelope: %+v", out)
	}
	wantConflicts := []engine.GraphSearchConflict{{DatasetID: "write-dataset", Text: "allowed-conflict"}}
	if len(out.GraphContext.Entities) != 1 || out.GraphContext.Entities[0].Name != "AllowedEntity" ||
		len(out.GraphContext.Facts) != 1 || out.GraphContext.Facts[0].Text != "allowed fact" ||
		!slices.Equal(out.GraphContext.Conflicts, wantConflicts) ||
		len(out.GraphContext.CodeFacts) != 1 || out.GraphContext.CodeFacts[0].Name != "allowedFn" {
		t.Fatalf("unexpected filtered graph_context: %+v", out.GraphContext)
	}

	// 5. When all graph_context entries (including conflicts) are unauthorized, graph_context is omitted while degraded_to_chunks stays true.
	nextGC = &engine.GraphSearchContext{
		Entities:         []engine.GraphSearchEntity{{DatasetID: "hidden-dataset", Name: "OnlyHidden"}},
		Facts:            []engine.GraphSearchFact{{DatasetID: "hidden-dataset", Text: "OnlyHiddenFact"}},
		Conflicts:        []engine.GraphSearchConflict{{DatasetID: "hidden-dataset", Text: "OnlyHiddenConflict"}},
		DegradedToChunks: true,
	}
	data, msg = callTool(t, h, "recall", `{"project_id":"write","query":"auth"}`)
	if msg != "" {
		t.Fatalf("recall with only unauthorized graph_context failed: %s", msg)
	}
	var degradedOut map[string]any
	requireOK(t, json.Unmarshal(data, &degradedOut))
	if degradedOut["degraded_to_chunks"] != true {
		t.Fatalf("expected degraded_to_chunks=true, got %+v", degradedOut)
	}
	if _, hasGC := degradedOut["graph_context"]; hasGC {
		t.Fatalf("expected empty filtered graph_context to be omitted: %s", data)
	}
}

func TestRememberDeterministicIdempotencyKeyWithoutSessionOrMessage(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	var keys []string
	eng.ingest = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		keys = append(keys, req.IdempotencyKey)
		return engine.IngestResult{OK: true, DocumentID: "doc-det"}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})

	first, msg := callTool(t, h, "remember", `{"project_id":"write","content":"retry safe note","context_date":"2026-11-27","metadata":{"b":2,"a":1}}`)
	if msg != "" {
		t.Fatalf("first remember failed: %s", msg)
	}
	second, msg := callTool(t, h, "remember", `{"project_id":"write","content":"retry safe note","context_date":"2026-11-27","metadata":{"a":1,"b":2}}`)
	if msg != "" {
		t.Fatalf("second remember failed: %s", msg)
	}
	var out1, out2 map[string]any
	requireOK(t, json.Unmarshal(first, &out1))
	requireOK(t, json.Unmarshal(second, &out2))
	if out1["idempotency_key"] != out2["idempotency_key"] || !strings.HasPrefix(fmt.Sprint(out1["idempotency_key"]), "mcp:") {
		t.Fatalf("expected identical deterministic mcp key, got %v vs %v", out1["idempotency_key"], out2["idempotency_key"])
	}
	if out2["deduplicated"] != true || len(keys) != 1 {
		t.Fatalf("expected deduplicated replay with 1 ingest, got out2=%+v keys=%v", out2, keys)
	}

	third, msg := callTool(t, h, "remember", `{"project_id":"write","content":"different note","context_date":"2026-11-27","metadata":{"a":1,"b":2}}`)
	if msg != "" {
		t.Fatalf("third remember failed: %s", msg)
	}
	var out3 map[string]any
	requireOK(t, json.Unmarshal(third, &out3))
	if out3["idempotency_key"] == out1["idempotency_key"] || len(keys) != 2 {
		t.Fatalf("different content must produce distinct key: %v vs %v", out3["idempotency_key"], out1["idempotency_key"])
	}
}

func TestRememberStatusToolDirectACL(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	addAgent(t, store, domain.Agent{ID: "other", Name: "Other", DefaultAccess: domain.AccessMaintain}, "other-secret", 0o600)
	ctx := t.Context()
	item, err := store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID:          "task-direct-1",
		ProjectID:   "write",
		DatasetID:   "write-dataset",
		AgentID:     "active",
		SessionID:   "sess-1",
		MessageID:   "msg-1",
		CaptureKey:  "key-direct-1",
		Kind:        string(engine.IngestKindDocument),
		PayloadJSON: `{"content":"hello"}`,
	})
	requireOK(t, err)
	h := newTestServer(t, Config{Store: store, Engine: eng})

	// Empty task_id -> JSON-RPC Invalid params (-32602).
	bad := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember_status","arguments":{"task_id":"  "}}}`)
	if bad.Error == nil || bad.Error.Code != -32602 {
		t.Fatalf("expected -32602 for blank task_id, got %+v", bad)
	}
	// Owner with maintain access succeeds.
	data, msg := callTool(t, h, "remember_status", fmt.Sprintf(`{"task_id":%q}`, item.ID))
	if msg != "" {
		t.Fatalf("remember_status owner failed: %s", msg)
	}
	var statusOut TaskStatusResult
	requireOK(t, json.Unmarshal(data, &statusOut))
	if !statusOut.OK || statusOut.TaskID != item.ID || statusOut.Status != "pending" || statusOut.Saved {
		t.Fatalf("unexpected statusOut: %+v", statusOut)
	}
	// Another agent cannot read active's task even with maintain access.
	otherRec := request(h, http.MethodPost, "/", "X-Api-Key", "other-secret",
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember_status","arguments":{"task_id":%q}}}`, item.ID))
	status(t, otherRec, 200)
	if !strings.Contains(otherRec.Body.String(), "Access denied") {
		t.Fatalf("other agent should be denied: %s", otherRec.Body.String())
	}
	// Revoking active's project access denies status lookup.
	allow(t, store, "project", "write", domain.AccessNone)
	if _, msg := callTool(t, h, "remember_status", fmt.Sprintf(`{"task_id":%q}`, item.ID)); msg != "Access denied" {
		t.Fatalf("revoked agent should be denied, got %q", msg)
	}
}

func TestRememberR1SingleSidedSessionAndCanonicalJSON(t *testing.T) {
	store, eng := fixture(t)
	configureScopes(t, store)
	var seen []engine.IngestRequest
	eng.ingest = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		if req.IdempotencyKey == "engine-invalid" {
			return engine.IngestResult{}, engine.ErrInvalidArgument
		}
		seen = append(seen, req)
		return engine.IngestResult{OK: true, EntryID: fmt.Sprintf("e-%d", len(seen))}, nil
	}
	h := newTestServer(t, Config{Store: store, Engine: eng})

	// 1. Single-sided session_id (no message_id) produces distinct keys across different sessions.
	d1, msg1 := callTool(t, h, "remember", `{"project_id":"write","content":"same body","session_id":"sess-A"}`)
	d2, msg2 := callTool(t, h, "remember", `{"project_id":"write","content":"same body","session_id":"sess-B"}`)
	if msg1 != "" || msg2 != "" || len(seen) != 2 {
		t.Fatalf("single-sided session_id should not collide: %q / %q / len=%d", msg1, msg2, len(seen))
	}
	var out1, out2 map[string]any
	requireOK(t, json.Unmarshal(d1, &out1))
	requireOK(t, json.Unmarshal(d2, &out2))
	if out1["idempotency_key"] == out2["idempotency_key"] {
		t.Fatalf("distinct session_ids produced identical key: %v", out1["idempotency_key"])
	}

	// 2. JSON object content with different key orders produces identical key & canonical payload (no ErrConflict).
	beforeJSON := len(seen)
	qa1, m1 := callTool(t, h, "remember", `{"project_id":"write","kind":"capture_qa","content":"{\"b\":2,\"a\":1}"}`)
	qa2, m2 := callTool(t, h, "remember", `{"project_id":"write","kind":"capture_qa","content":"{\"a\":1,\"b\":2}"}`)
	if m1 != "" || m2 != "" || len(seen) != beforeJSON+1 {
		t.Fatalf("reordered JSON content should deduplicate cleanly: m1=%q m2=%q calls=%d", m1, m2, len(seen)-beforeJSON)
	}
	var qOut1, qOut2 map[string]any
	requireOK(t, json.Unmarshal(qa1, &qOut1))
	requireOK(t, json.Unmarshal(qa2, &qOut2))
	if qOut1["idempotency_key"] != qOut2["idempotency_key"] || qOut2["deduplicated"] != true {
		t.Fatalf("expected deduplicated canonical JSON replay: %+v vs %+v", qOut1, qOut2)
	}

	// 3. Engine invalid argument maps to JSON-RPC -32602 (Invalid params), not -32603.
	bad := rpc(t, h, `{"jsonrpc":"2.0","id":77,"method":"tools/call","params":{"name":"remember","arguments":{"project_id":"write","content":"bad","idempotency_key":"engine-invalid"}}}`)
	if bad.Error == nil || bad.Error.Code != -32602 {
		t.Fatalf("engine.ErrInvalidArgument should map to -32602, got %+v", bad)
	}
}

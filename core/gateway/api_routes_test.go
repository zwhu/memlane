package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/agents"
)

func TestHealthMonitorAndDelegates(t *testing.T) {
	delegate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"path": r.URL.Path})
	})
	s, _, _ := testServer(t, func(c *Config) {
		c.CompletionHandler, c.MCPHandler, c.ProfilesHandler = delegate, delegate, delegate
		c.DaemonStatus = func(context.Context) map[string]any { return map[string]any{"owner": "test-daemon"} }
	})
	for _, prefix := range []string{"/api", "/v1"} {
		health := decodeResponse[map[string]any](t, manage(t, s, "GET", prefix+"/health", "", 200))
		if health["ok"] != true || health["engine_ok"] != true || health["inbox"] == nil {
			t.Fatal(health)
		}
		caps := decodeResponse[map[string]any](t, manage(t, s, "GET", prefix+"/capabilities", "", 200))
		if caps["reading_contract"] != "catalog-cursor-v1" || len(caps["api_versions"].([]any)) != 2 {
			t.Fatal(caps)
		}
		monitor := decodeResponse[map[string]any](t, manage(t, s, "GET", prefix+"/runtime/monitor", "", 200))
		if monitor["daemon"].(map[string]any)["owner"] != "test-daemon" ||
			monitor["engine"].(map[string]any)["engine"] != "fake" || monitor["inbox"].(map[string]any)["running"] != false {
			t.Fatal(monitor)
		}
	}
	for _, path := range []string{"/v1/chat/completions", "/mcp", "/mcp/session"} {
		req := request(t, s, "POST", path, "", nil, 200)
		if got := decodeResponse[map[string]string](t, req)["path"]; got != path {
			t.Fatal(got)
		}
		requireCode(t, request(t, s, "POST", path, "", map[string]string{"Origin": "https://example.invalid"}, 403), "browser_origin_rejected")
	}
	for _, path := range []string{
		"//api/projects", "/./api/projects", "/api", "/v1", "//api/%2e%2e/projects",
		"/v1//chat/completions", "/v1/chat/./completions", "/v1/chat/%63ompletions",
	} {
		requireCode(t, request(t, s, "GET", path, "", nil, 401), "unauthorized")
	}
	manage(t, s, "GET", "//api/projects", "", http.StatusTemporaryRedirect)
	for _, tc := range []struct{ path, want string }{
		{"/api/profiles", "/api/profiles"},
		{"/api/profiles/", "/api/profiles/"},
		{"/api/profiles/codex", "/api/profiles/codex"},
		{"/v1/profiles", ""},
		{"/v1/profiles/", "/"},
		{"/v1/profiles/codex", "/codex"},
	} {
		request(t, s, "GET", tc.path, "", nil, http.StatusUnauthorized)
		res := manage(t, s, "GET", tc.path, "", http.StatusOK)
		if got := decodeResponse[map[string]string](t, res)["path"]; got != tc.want {
			t.Errorf("GET %s delegated path = %q, want %q", tc.path, got, tc.want)
		}
	}
	if _, err := s.StartLoopback("0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback accepted")
	}
	if _, err := s.StartLoopback("127.0.0.1:http"); err == nil {
		t.Fatal("service-name port accepted")
	}
}

func TestCompletionHealthRoute(t *testing.T) {
	unconfigured, _, _ := testServer(t, nil)
	for _, prefix := range []string{"/api", "/v1"} {
		requireCode(t, request(t, unconfigured, "GET", prefix+"/completion/health", "", nil, http.StatusUnauthorized), "unauthorized")
		requireCode(t, manage(t, unconfigured, "GET", prefix+"/completion/health", "", http.StatusNotFound), "not_configured")
	}

	var seenMethod, seenPath, seenRequestURI, seenAuth, seenMgmt string
	delegate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod, seenPath, seenRequestURI = r.Method, r.URL.Path, r.RequestURI
		seenAuth, seenMgmt = r.Header.Get("Authorization"), r.Header.Get("X-Memlane-Management")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "path": r.URL.Path})
	})
	configured, _, _ := testServer(t, func(c *Config) {
		c.CompletionHandler = delegate
	})
	for _, prefix := range []string{"/api", "/v1"} {
		requireCode(t, request(t, configured, "GET", prefix+"/completion/health", "", nil, http.StatusUnauthorized), "unauthorized")
		res := manage(t, configured, "GET", prefix+"/completion/health", "", http.StatusOK)
		got := decodeResponse[map[string]string](t, res)
		if seenMethod != http.MethodGet || seenPath != "/health" || seenRequestURI != "" ||
			seenAuth != "" || seenMgmt != "" || got["path"] != "/health" || got["status"] != "ready" {
			t.Fatalf("GET %s/completion/health delegated (%s %s uri=%q auth=%q mgmt=%q) body=%+v",
				prefix, seenMethod, seenPath, seenRequestURI, seenAuth, seenMgmt, got)
		}
	}
}

func TestLoopbackLifecycle(t *testing.T) {
	s, _, _ := testServer(t, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ListenAndServeLoopback(cancelled, "127.0.0.1:0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled listener start = %v", err)
	}
	addr, err := s.StartLoopback("127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("sandbox denies loopback bind: %v", err)
	}
	must(t, err)
	if _, err := s.StartLoopback("127.0.0.1:0"); err == nil {
		t.Fatal("second listener started")
	}
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/api/health", nil)
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+testManagement)
	response, err := http.DefaultClient.Do(req)
	must(t, err)
	must(t, response.Body.Close())
	if response.StatusCode != 200 {
		t.Fatalf("live /api/health HTTP status = %d", response.StatusCode)
	}
	summary, err := s.Inbox().Summary(ctx, "")
	must(t, err)
	if !summary.Running {
		t.Fatal("listener did not start the worker")
	}
	must(t, s.Shutdown(ctx))
	summary, err = s.Inbox().Summary(ctx, "")
	must(t, err)
	if summary.Running {
		t.Fatal("listener did not stop the worker")
	}
}

func TestJSONRequestBounds(t *testing.T) {
	s, _, _ := testServer(t, func(c *Config) { c.MaxBodyBytes = 32 })
	headers := map[string]string{"Authorization": "Bearer " + testManagement}
	for _, input := range []string{`null`, `[]`, `{"name":"one"} {}`} {
		requireCode(t, request(t, s, "POST", "/api/projects", input, headers, 400), "invalid_json")
	}
	requireCode(t, request(t, s, "POST", "/api/projects", `{"name":"x"}`,
		map[string]string{"Authorization": "Bearer " + testManagement, "Content-Type": "text/plain"}, 415), "unsupported_media_type")
	requireCode(t, request(t, s, "POST", "/api/projects", `{"name":"`+strings.Repeat("x", 35)+`"}`, headers, 413),
		"request_body_too_large")
}

func TestProjectsAndZeroDefaultGrant(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	if got := decodeResponse[projectView](t, manage(t, s, "POST", "/api/projects", `{"name":"Generated"}`, 201)); got.ProjectID == "" || !got.AutoCapture {
		t.Fatal(got)
	}
	requireCode(t, manage(t, s, "POST", "/api/projects", `{"name":"Malformed","aliases":["relative"]}`, 400), "invalid_argument")
	created := decodeResponse[projectView](t, manage(t, s, "POST", "/v1/projects", `{"project_id":"test","name":"Example","dataset_id":"example","aliases":["/work/example"],"auto_capture":false}`, 201))
	if created.ProjectID != "test" || created.AutoCapture || !reflect.DeepEqual(created.Aliases, []string{"/work/example"}) {
		t.Fatal(created)
	}
	requireCode(t, manage(t, s, "POST", "/api/projects", `{"id":"test","name":"Duplicate"}`, 409), "conflict")
	manage(t, s, "PUT", "/api/projects/test/name", `{"name":"Renamed"}`, 200)
	manage(t, s, "POST", "/v1/projects/test/aliases", `{"alias_path":"/work/other"}`, 200)
	manage(t, s, "PATCH", "/api/projects/test", `{"status":"ready","code_enabled":true,"code_dataset_id":"code"}`, 200)
	changed := decodeResponse[projectView](t, manage(t, s, "PUT", "/v1/projects/test/auto-capture", `{"auto_capture":true}`, 200))
	if changed.Name != "Renamed" || !changed.AutoCapture || changed.AutoCaptureVersion != 2 || !changed.CodeEnabled || len(changed.Aliases) != 2 {
		t.Fatal(changed)
	}
	toggle := "/v1/projects/test/auto-capture"
	for _, body := range []string{`{"enabled":false}`, `{"auto_capture":false,"enabled":false}`} {
		got := decodeResponse[projectView](t, manage(t, s, "PUT", toggle, body, 200))
		if got.AutoCapture || got.AutoCaptureVersion != 3 {
			t.Fatal(got)
		}
	}
	requireCode(t, manage(t, s, "PUT", toggle, `{"auto_capture":true,"enabled":false}`, 400), "invalid_argument")
	withWorkspace := decodeResponse[projectView](t, manage(t, s, "POST", "/v1/projects/test/aliases", `{"workspace":"/work/workspace"}`, 200))
	if withWorkspace.AutoCapture || !reflect.DeepEqual(withWorkspace.Aliases, []string{"/work/example", "/work/other", "/work/workspace"}) {
		t.Fatal(withWorkspace)
	}
	for _, access := range []domain.AccessLevel{domain.AccessNone, domain.AccessRead, domain.AccessMaintain} {
		out := decodeResponse[struct {
			Projects []projectView `json:"projects"`
		}](t, manage(t, s, "PUT", "/v1/projects/test/overrides", fmt.Sprintf(`{"agent_id":"agent","access":%q,"capture":false}`, access), 200))
		found := false
		for _, p := range out.Projects {
			if p.ID == "test" {
				override, ok := p.Overrides["agent"]
				found = ok && override.Access != nil && *override.Access == access
			}
		}
		if !found {
			t.Fatal("override missing from access directory")
		}
		got, err := store.EvaluateProjectACL(ctx, "test", "agent")
		must(t, err)
		if got.Access != access || got.CanCapture {
			t.Fatal(got)
		}
	}
	manage(t, s, "DELETE", "/api/projects/test/acl/agent", "", 204)
	manage(t, s, "PUT", "/api/projects/test/acl/unknown", `{"access":"read"}`, 404)
	manage(t, s, "POST", "/api/projects", `{"id":"protected","name":"ml-user-profiles","dataset_id":"profiles"}`, 201)
	a := decodeResponse[agentView](t, manage(t, s, "POST", "/v1/agents", `{"id":"new","kind":"pi","default_access":"maintain"}`, 201))
	if a.DefaultAccess != domain.AccessNone {
		t.Fatal(a)
	}
	for _, id := range []string{"protected", "test", "p"} {
		acl, err := store.EvaluateProjectACL(ctx, id, "new")
		must(t, err)
		if acl.Access != domain.AccessNone {
			t.Fatal(id, acl)
		}
	}
	acl, err := store.EvaluateCommonACL(ctx, "new")
	must(t, err)
	if acl.Access != domain.AccessNone {
		t.Fatal(acl)
	}
	api := manage(t, s, "GET", "/api/projects/test", "", 200).Body.String()
	if same := manage(t, s, "GET", "/v1/projects/test", "", 200).Body.String(); api != same {
		t.Fatalf("alias mismatch: %s vs %s", api, same)
	}
	manage(t, s, "GET", "/api/projects", "", 200)
	manage(t, s, "DELETE", "/api/projects/test", "", 204)
	manage(t, s, "GET", "/api/projects/test", "", 404)
}

func TestListProjectsReturnsRealDefaultsAndAgentDefaultAccess(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "legacy-maintain", Kind: "gemini", Name: "Gemini App", DefaultAccess: domain.AccessMaintain}))
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "trae-cn", Kind: "trae_ide", Name: "TRAE CN", DefaultAccess: domain.AccessNone}))
	must(t, store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access:      domain.AccessMaintain,
		Capture:     true,
		AutoProcess: true,
		Revision:    5,
	}))

	type projectsPayload struct {
		Defaults struct {
			Access      domain.AccessLevel `json:"access"`
			Capture     bool               `json:"capture"`
			AutoProcess bool               `json:"auto_process"`
		} `json:"defaults"`
		Agents   []accessAgent `json:"agents"`
		Revision int           `json:"revision"`
	}
	got := decodeResponse[projectsPayload](t, manage(t, s, "GET", "/api/projects", "", 200))
	if got.Defaults.Access != domain.AccessMaintain || !got.Defaults.Capture || !got.Defaults.AutoProcess || got.Revision != 5 {
		t.Fatalf("GET /api/projects returned wrong defaults: %+v", got)
	}
	agentDefaults := map[string]domain.AccessLevel{}
	for _, a := range got.Agents {
		agentDefaults[a.ID] = a.DefaultAccess
	}
	if agentDefaults["legacy-maintain"] != domain.AccessMaintain {
		t.Fatalf("expected legacy-maintain default_access=maintain, got %+v", agentDefaults)
	}
	if agentDefaults["trae-cn"] != domain.AccessNone {
		t.Fatalf("expected trae-cn default_access=none, got %+v", agentDefaults)
	}
	if agentDefaults["agent"] != domain.AccessNone {
		t.Fatalf("expected connected agent default_access=none, got %+v", agentDefaults)
	}

	must(t, store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access:      domain.AccessRead,
		Capture:     false,
		AutoProcess: true,
		Revision:    6,
	}))
	gotRead := decodeResponse[projectsPayload](t, manage(t, s, "GET", "/v1/projects", "", 200))
	if gotRead.Defaults.Access != domain.AccessRead || gotRead.Defaults.Capture || !gotRead.Defaults.AutoProcess || gotRead.Revision != 6 {
		t.Fatalf("GET /v1/projects did not reflect updated defaults: %+v", gotRead)
	}
}

func TestKnowledgeWaterfallAndBody(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	base := time.Now().Add(-4 * time.Hour)
	seedDoc(t, store, "ds", "old", "knowledge", "rules", base, -10)
	seedDoc(t, store, "ds", "middle", "knowledge", "tips", base.Add(time.Hour), -20)
	seedDoc(t, store, "ds", "new", "knowledge", "rules", base.Add(2*time.Hour), -30)
	seedDoc(t, store, "foreign", "leak", "knowledge", "rules", base, -100)
	seedDoc(t, store, "ds", "raw", "raw_doc", "rules", base, -100)
	must(t, store.RecordRecallQueryStat(ctx, "automatic", 1, ""))
	_, err := store.RecordRecallEvent(ctx, domain.RecallEventInput{AgentID: "agent", EventID: "seen", Channel: "automatic",
		Hits: []domain.RecallHit{{DatasetID: "ds", DocumentID: "old"}}})
	must(t, err)
	path := "/api/projects/p/knowledge?limit=1"
	first := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", path, "", 200))
	if len(first.Items) != 1 || first.Items[0].DocID != "new" || !first.HasMore || first.HasPrev {
		t.Fatal(first)
	}
	next := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", path+"&cursor="+url.QueryEscape(first.NextCursor), "", 200))
	if len(next.Items) != 1 || next.Items[0].DocID != "middle" || !next.HasPrev {
		t.Fatal(next)
	}
	prev := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", path+"&direction=prev&cursor="+url.QueryEscape(next.PrevCursor), "", 200))
	if len(prev.Items) != 1 || prev.Items[0].DocID != "new" || prev.HasPrev {
		t.Fatal(prev)
	}
	manage(t, s, "GET", path+"&cursor="+first.NextCursor+"a", "", 400)
	manage(t, s, "GET", path+"&category=rules&cursor="+first.NextCursor, "", 400)
	manage(t, s, "GET", "/v1/projects/p/knowledge?cursor="+first.NextCursor, "", 400)
	recall := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/api/knowledge?sort=recall&limit=1", "", 200))
	if recall.Items[0].DocID != "old" || recall.Items[0].RecallCount != 1 {
		t.Fatal(recall)
	}
	category := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/v1/projects/p/knowledge?category=rules&anchor_doc_id=old", "", 200))
	if len(category.Items) != 1 || category.Items[0].DocID != "old" || !category.HasPrev {
		t.Fatal(category)
	}
	event := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/api/knowledge?recall_agent_id=agent&recall_event_id=seen", "", 200))
	if len(event.Items) != 1 || event.Items[0].DocID != "old" {
		t.Fatal(event)
	}
	if countRows(t, store, "ui_read_scopes") != 0 {
		t.Fatal("GET inserted reading scopes")
	}
	must(t, store.UpsertReadScope(ctx, domain.ReadScope{ScopeHash: domain.KnowledgeReadScopeHash(readIdentity, "p", "ds"),
		ProjectID: "p", DatasetID: "ds", Initialized: true, ReadHorizonAtNS: domain.FormatUTCNS(base.Add(time.Hour))}))
	unread := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/api/knowledge?unread_only=true", "", 200))
	if len(unread.Items) != 1 || unread.Items[0].DocID != "new" || !unread.Items[0].IsUnread {
		t.Fatal(unread)
	}
	for _, route := range []string{"/api/knowledge/ds/new", "/api/datasets/ds/documents/new", "/v1/datasets/ds/documents/new/body"} {
		got := decodeResponse[map[string]any](t, manage(t, s, "GET", route, "", 200))
		if got["content"] != "projector body" || got["document_id"] != "new" {
			t.Fatal(got)
		}
	}
	if len(fake.local) != 3 || fake.local[2].MaxBytes != 1<<20 || fake.local[2].Location != "owned://new" {
		t.Fatal(fake.local)
	}
	manage(t, s, "GET", "/api/knowledge/foreign/leak/body", "", 403)
	manage(t, s, "DELETE", "/api/knowledge/ds/new", "", 204)
	if countRows(t, store, "catalog_docs") != 4 {
		t.Fatal("catalog did not update after deleting document")
	}
}

func TestRawRecordsCommonAndSearch(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	now := time.Now()
	seedDoc(t, store, "ds", "knowledge", "knowledge", "tips", now, -10)
	seedDoc(t, store, "ds", "raw", "raw_doc", "", now, -20)
	seedDoc(t, store, "common", "common", "common", "preference", now, -30)
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{DatasetID: "ds", SessionID: "match-session", UserID: "u", IsTopLevel: true, SortTSDescKey: -40}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{DatasetID: "ds", SessionID: "child", UserID: "u", ParentSessionID: "match-session", SortTSDescKey: -50}))
	path := "/api/raw-records?limit=1"
	raw := decodeResponse[pageResult[rawItem]](t, manage(t, s, "GET", path, "", 200))
	if len(raw.Items) != 1 || raw.Items[0].Kind != "session" || !raw.HasMore {
		t.Fatal(raw)
	}
	second := decodeResponse[pageResult[rawItem]](t, manage(t, s, "GET", path+"&cursor="+raw.NextCursor, "", 200))
	if len(second.Items) != 1 || second.Items[0].DocID != "raw" || second.HasMore {
		t.Fatal(second)
	}
	children := decodeResponse[pageResult[rawItem]](t, manage(t, s, "GET", "/v1/projects/p/raw-records/sessions/match-session/children", "", 200))
	if len(children.Items) != 1 || children.Items[0].SessionID != "child" {
		t.Fatal(children)
	}
	manage(t, s, "GET", "/v1/datasets/ds/sessions/child", "", 200)
	manage(t, s, "GET", "/api/datasets/unmanaged/sessions/child", "", 403)

	created := decodeResponse[map[string]any](t, manage(t, s, "POST", "/api/common", `{"common_category":"experience","label":"label","content":"hello"}`, 201))
	doc, _ := created["document_id"].(string)
	if doc == "" {
		t.Fatal(created)
	}
	detail := decodeResponse[map[string]any](t, manage(t, s, "GET", "/api/common/"+doc, "", 200))
	if detail["content"] != "hello" || detail["common_category"] != "experience" {
		t.Fatal(detail)
	}
	updated := decodeResponse[map[string]any](t, manage(t, s, "PATCH", "/api/common/"+doc, `{"content":"changed"}`, 200))
	id := updated["document_id"].(string)
	if id == doc || fake.deleted[doc] != "common" {
		t.Fatal(updated)
	}
	manage(t, s, "GET", "/api/common/"+doc, "", 404)
	common := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/v1/common/knowledge?common_category=experience", "", 200))
	if len(common.Items) != 1 || common.Items[0].DocID != id {
		t.Fatal(common)
	}
	manage(t, s, "DELETE", "/api/common/"+id, "", 204)
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"maintain"}`, 400)
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"read"}`, 200)

	fake.hits = []engine.SearchHit{{DatasetID: "ds", DocumentID: "knowledge"}, {DatasetID: "private", DocumentID: "leak"}}
	out := decodeResponse[map[string]any](t, manage(t, s, "POST", "/api/search", `{"q":"match","filter":"all","semantic":true}`, 200))
	if len(out["items"].([]any)) != 4 || len(out["semantic_hits"].([]any)) != 1 {
		t.Fatal(out)
	}
	semantic := decodeResponse[map[string]any](t, manage(t, s, "GET", "/api/search?q=match&filter=raw&semantic=true", "", 200))
	if len(semantic["semantic_hits"].([]any)) != 0 {
		t.Fatal("raw search returned a knowledge hit")
	}
	filtered := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/search/metadata?q=match&filter=raw", "", 200))
	if len(filtered["items"].([]any)) != 2 {
		t.Fatal(filtered)
	}
	history := decodeResponse[struct {
		Searches []searchEntry `json:"recent_searches"`
		Items    []catalogItem `json:"recent_items"`
	}](
		t, manage(t, s, "GET", "/api/search/recent", "", 200))
	if len(history.Searches) != 1 || history.Searches[0].Filter != "all" || len(history.Items) != 2 {
		t.Fatal(history)
	}
	for _, table := range []string{"recall_events", "recall_doc_counts", "recall_query_stats"} {
		if countRows(t, store, table) != 0 {
			t.Fatal("user search changed", table)
		}
	}
}

func TestReadingStateAndGraph(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	seedDoc(t, store, "ds", "a", "knowledge", "", time.Now().Add(time.Hour), -10)
	seedDoc(t, store, "ds", "b", "knowledge", "", time.Now().Add(2*time.Hour), -20)
	hash := domain.KnowledgeReadScopeHash(readIdentity, "p", "ds")
	must(t, store.UpsertReadScope(ctx, domain.ReadScope{ScopeHash: hash, ProjectID: "p", DatasetID: "ds", Initialized: true,
		ReadHorizonAtNS: domain.NowUTCNS()}))
	scopes := decodeResponse[struct{ Projects []readingStateView }](t, manage(t, s, "GET", "/v1/reading-state", "", 200))
	if len(scopes.Projects) != 1 || scopes.Projects[0].ScopeHash != hash || scopes.Projects[0].UnreadCount != 2 ||
		!domain.IsValidUTCNS(scopes.Projects[0].ReadHorizon) {
		t.Fatal(scopes)
	}
	request(t, s, "GET", "/api/reading-state", "", map[string]string{"Authorization": "Bearer " + testManagement}, 400)
	manage(t, s, "POST", "/api/reading-state/mark-read", `{"project_id":"p","dataset_id":"common","doc_ids":["a"]}`, 403)
	manage(t, s, "POST", "/api/reading-state/mark-read", `{"project_id":"p","dataset_id":"ds","doc_ids":["a","missing"]}`, 404)
	if countRows(t, store, "ui_read_items") != 0 {
		t.Fatal("invalid mark-read changed state")
	}
	body := `{"reading_identity":"desktop-reader","project_id":"p","dataset_id":"ds","doc_ids":["a"]}`
	marked := decodeResponse[readingStateView](t, manage(t, s, "POST", "/api/reading-state/mark-read", body, 200))
	if marked.ScopeHash != hash || marked.UnreadCount != 1 || marked.StateRev < 2 {
		t.Fatal(marked)
	}
	all := decodeResponse[readingStateView](t, manage(t, s, "POST", "/v1/reading-state/mark-all-read", `{"project_id":"p","dataset_id":"ds"}`, 200))
	if all.ScopeHash != hash || all.UnreadCount != 0 || !domain.IsValidUTCNS(all.ReadHorizon) {
		t.Fatal(all)
	}
	scope, err := store.GetReadScope(ctx, hash)
	must(t, err)
	if scope.HorizonMaxRowID != 20 {
		t.Fatal(scope)
	}
	graph := decodeResponse[engine.GraphPayload](t, manage(t, s, "GET", "/v1/graph?project_id=p&filter=topic", "", 200))
	if !graph.OK || len(graph.Nodes) != 1 || graph.Edges == nil || fake.graph[0].Filter != "topic" {
		t.Fatal(graph)
	}
	manage(t, s, "GET", "/api/graph?dataset_id=unmanaged", "", 403)
	manage(t, s, "GET", "/api/graph?project_id=p&dataset_id=common", "", 403)
}

func TestAgentConflictPreview(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".codex")
	must(t, os.Mkdir(profile, 0o700))
	must(t, os.WriteFile(filepath.Join(profile, "config.toml"), []byte(`
[mcp_servers.memlane-cognee]
command = "legacy"
[mcp_servers.custom-cognee]
command = "custom"
[mcp_servers.unrelated]
command = "ordinary"
`), 0o600))
	s, _, _ := testServer(t, func(c *Config) {
		c.DiscoveryOptions = agents.DiscoveryOptions{Home: home, Kinds: []string{"codex"}, AppDirs: []string{home},
			LookPath: func(string) (string, error) { return "", errors.New("not in test path") }}
	})
	listed := decodeResponse[map[string]any](t, manage(t, s, "GET", "/api/agents", "", 200))
	if len(listed["detected_hosts"].([]any)) != 1 || len(listed["registered_agents"].([]any)) != 1 {
		t.Fatal(listed)
	}
	for _, route := range []struct{ method, path, body string }{{"GET", "/api/agents/conflicts?kind=codex", ""}, {"POST", "/api/agents/conflicts", `{"kind":"codex"}`}} {
		w := manage(t, s, route.method, route.path, route.body, 200)
		out := decodeResponse[map[string]any](t, w)
		if len(out["managed_legacy"].([]any)) != 1 || len(out["third_party_conflicts"].([]any)) != 1 ||
			out["unrelated_count"] != float64(1) || out["blocked"] != true || strings.Contains(w.Body.String(), "api_key") {
			t.Fatal(out)
		}
	}
}

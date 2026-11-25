package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr[T any](value T) *T { return &value }

func testClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientHeadersAndTokenFile(t *testing.T) {
	const token = "management-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, value := range map[string]string{
			"Authorization": "Bearer " + token, "X-Memlane-Management": token,
			"X-Memlane-Reading-Identity": "reader", "Content-Type": "application/json",
		} {
			if got := r.Header.Get(key); got != value {
				t.Errorf("%s = %q, want %q", key, got, value)
			}
		}
		if _, ok := r.Header["Origin"]; ok {
			t.Error("sent Origin header")
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  "+token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{
		{BaseURL: server.URL, ManagementTokenFile: path, ReadingIdentity: "reader"},
		{BaseURL: server.URL, ManagementToken: token, ManagementTokenFile: "unused", ReadingIdentity: "reader"},
	} {
		resp, err := testClient(t, cfg).Search(context.Background(), SearchRequest{Query: "q"})
		if err != nil || !resp.OK {
			t.Fatalf("Search = %+v, %v", resp, err)
		}
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(Config{BaseURL: server.URL, ManagementTokenFile: link}); err == nil {
		t.Fatal("accepted a symlink as a token file")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(Config{BaseURL: server.URL, ManagementTokenFile: path}); err == nil {
		t.Fatal("accepted a publicly readable token file")
	}
}

func TestClientConfigAndRedirect(t *testing.T) {
	for _, origin := range []string{"", "https://example.com", "ftp://localhost", "http://127.0.0.1/path", "http://user@localhost", "http://localhost?x=1"} {
		if _, err := NewClient(Config{BaseURL: origin}); err == nil {
			t.Errorf("accepted invalid gateway URL %q", origin)
		}
	}
	client := testClient(t, Config{BaseURL: "http://[::1]:1234/"})
	if client.http.Timeout != 15*time.Second || client.maxResponseBytes != 24<<20 {
		t.Fatal("incorrect client defaults")
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("gateway client followed a redirect")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/health", http.StatusFound)
	}))
	defer source.Close()
	custom := &http.Client{Timeout: time.Second}
	client = testClient(t, Config{BaseURL: source.URL, ManagementToken: "secret", HTTPClient: custom})
	_, err := client.Health(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound || custom.CheckRedirect != nil {
		t.Fatalf("redirect or client ownership contract failed: %v", err)
	}
}

func TestAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		status              int
		body, code, message string
		helper              func(*APIError) bool
	}{
		{401, `{"error":"unauthorized","message":"management credentials required"}`, "unauthorized", "management credentials required", (*APIError).IsUnauthorized},
		{403, `{"error":"forbidden"}`, "forbidden", "Forbidden", (*APIError).IsForbidden},
		{404, `{"error_code":"profile_not_found"}`, "profile_not_found", "Not Found", (*APIError).IsNotFound},
		{409, `{"error_code":"profile_conflict"}`, "profile_conflict", "Conflict", (*APIError).IsConflict},
		{503, `{"error":{"unexpected":"details"},"message":"do not expose raw data"}`, "", "Service Unavailable", (*APIError).IsUnavailable},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := testClient(t, Config{BaseURL: server.URL})
			_, err := client.GetUserProfile(context.Background(), "codex")
			var got *APIError
			if !errors.As(err, &got) || !tc.helper(got) || got.Status != tc.status ||
				got.Code != tc.code || got.Message != tc.message || got.Endpoint != "/api/profiles/codex" {
				t.Fatalf("incorrect API error: %#v, %v", got, err)
			}
		})
	}
}

func TestCancellationAndResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
				t.Error("HTTP handler did not see cancellation")
			}
			return
		}
		fmt.Fprint(w, `{"reading_contract":"catalog-cursor-v1"}`)
	}))
	defer server.Close()
	client := testClient(t, Config{BaseURL: server.URL, MaxResponseBytes: 8})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := client.Health(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost context error: %v", err)
	}
	if _, err := client.Capabilities(context.Background()); err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("response limit was not enforced: %v", err)
	}
}

func TestRoutes(t *testing.T) {
	ctx := context.Background()
	acl := ACLOverride{Access: ptr(AccessMaintain), Capture: ptr(false)}
	model := SaveGenerationProfileRequest{GenerationProfile: GenerationProfile{Mode: "provider", Model: "m"}, APIKey: ptr("key")}
	selection := ProfileSelection{ProfileID: "g", Revision: 2}
	common := SaveCommonMemoryRequest{Content: ptr("body"), Category: ptr("preference"), Title: ptr("Title")}
	cases := []struct {
		method, uri    string
		status         int
		input, receipt string
		call           func(*Client) (any, error)
	}{
		{"GET", "/api/health", 200,
			``, `{"engine_ok":true,"inbox":{"failed":2,"recent_failures":[{"last_error":"detail"}]}}`,
			func(c *Client) (any, error) { return c.Health(ctx) }},
		{"GET", "/api/capabilities", 200,
			``, `{"capabilities":{"tool_graph":true},"features":{"profiles":true}}`,
			func(c *Client) (any, error) { return c.Capabilities(ctx) }},
		{"GET", "/api/runtime/monitor", 200,
			``, `{"daemon":{"pid":15},"engine":{"version":"1"},"inbox":{"running":true}}`,
			func(c *Client) (any, error) { return c.RuntimeMonitor(ctx) }},
		{"GET", "/api/overview/bootstrap?days=7&recall_limit=3&recent_limit=4&search_limit=5", 200,
			``, `{"day_buckets":[{"day":"2026-01-01","count":3}],"recall_events":[{"hits":[{"document_id":"d"}]}],"recall_query_stats":[{"hit_queries":1}]}`,
			func(c *Client) (any, error) {
				return c.OverviewBootstrap(ctx, OverviewOptions{Days: 7, RecentLimit: 4, RecallLimit: 3, SearchLimit: 5})
			}},
		{"GET", "/api/overview/day?day=2026-01-01&limit=10", 200,
			``, `{"day":"2026-01-01","total_filtered":1}`,
			func(c *Client) (any, error) { return c.OverviewDay(ctx, "2026-01-01", KnowledgePageOptions{Limit: 10}) }},
		{"GET", "/api/projects", 200,
			``, `{"projects":[{"id":"p","inbox_failed":1,"last_error":"failed"}]}`,
			func(c *Client) (any, error) { return c.ListProjects(ctx) }},
		{"GET", "/api/projects/%E6%B5%8B%20%2F%3F%23%25", 200,
			``, `{"auto_capture":true}`,
			func(c *Client) (any, error) { return c.GetProject(ctx, "测 /?#%") }},
		{"POST", "/api/projects", 201,
			`{"name":"Name","auto_capture":false}`, `{"project_id":"p"}`,
			func(c *Client) (any, error) {
				return c.CreateProject(ctx, CreateProjectRequest{Name: "Name", AutoCapture: ptr(false)})
			}},
		{"PATCH", "/api/projects/p", 200,
			`{"aliases":[],"code_enabled":false}`, `{"id":"p"}`,
			func(c *Client) (any, error) {
				return c.UpdateProject(ctx, "p", UpdateProjectRequest{Aliases: ptr([]string{}), CodeEnabled: ptr(false)})
			}},
		{"PUT", "/api/projects/p/name", 200,
			`{"name":"New name"}`, `{"name":"New name"}`,
			func(c *Client) (any, error) { return c.RenameProject(ctx, "p", "New name") }},
		{"PUT", "/api/projects/p/auto-capture", 200,
			`{"auto_capture":false}`, `{"auto_capture":false,"auto_capture_version":2}`,
			func(c *Client) (any, error) { return c.SetProjectAutoCapture(ctx, "p", false) }},
		{"POST", "/api/projects/p/aliases", 200,
			`{"path":"/workspace"}`, `{"aliases":["/workspace"]}`,
			func(c *Client) (any, error) { return c.AddProjectAlias(ctx, "p", "/workspace") }},
		{"DELETE", "/api/projects/p", 204,
			``, ``,
			func(c *Client) (any, error) { return nil, c.DeleteProject(ctx, "p") }},
		{"POST", "/api/projects/p/retry", 200,
			``, `{"project_id":"p","retried_count":1,"item_ids":["inbox-1"]}`,
			func(c *Client) (any, error) { return c.RetryProject(ctx, "p") }},
		{"PUT", "/api/projects/p/acl/a%2Fb", 200,
			`{"agent_id":"a/b","access":"maintain","capture":false}`, `{"agent_id":"a/b","access":"maintain","capture":false}`,
			func(c *Client) (any, error) { return c.SetProjectACL(ctx, "p", "a/b", acl) }},
		{"DELETE", "/api/projects/p/acl/a", 204,
			``, ``,
			func(c *Client) (any, error) { return nil, c.DeleteProjectACL(ctx, "p", "a") }},
		{"GET", "/api/knowledge?category=design&cursor=a%2Bb&direction=prev&project_id=p&recall_agent_id=a&recall_event_id=e&sort=recall&unread_only=true", 200,
			``, `{"items":[{"document_id":"d","is_unread":true}],"prev_cursor":"opaque","category_counts":{"design":4}}`,
			func(c *Client) (any, error) {
				return c.ListKnowledge(ctx, KnowledgePageOptions{Category: "design", Cursor: "a+b", Direction: "prev", ProjectID: "p", RecallAgentID: "a", RecallEventID: "e", Sort: "recall", UnreadOnly: true})
			}},
		{"GET", "/api/knowledge/ds/d", 200,
			``, `{"document_id":"d","content":"body","mime_type":"text/plain"}`,
			func(c *Client) (any, error) { return c.GetKnowledgeDocument(ctx, "ds", "d") }},
		{"GET", "/api/knowledge/ds/d/body", 200,
			``, `{"dataset_id":"ds","document_id":"d","content":"body","mime_type":"text/plain"}`,
			func(c *Client) (any, error) { return c.GetKnowledgeDocumentBody(ctx, "ds", "d") }},
		{"DELETE", "/api/knowledge/ds/d", 204,
			``, ``,
			func(c *Client) (any, error) { return nil, c.DeleteKnowledgeDocument(ctx, "ds", "d") }},
		{"GET", "/api/raw-records?anchor_doc_id=d&kind=session&project_id=p&user_id=reader", 200,
			``, `{"items":[{"session_id":"s","user_id":"reader","child_count":2}],"has_more":true}`,
			func(c *Client) (any, error) {
				return c.ListRawRecords(ctx, RawRecordsOptions{AnchorDocID: "d", Kind: "session", ProjectID: "p", UserID: "reader"})
			}},
		{"GET", "/api/projects/p/raw-records/sessions/parent%2Fid/children?limit=2", 200,
			``, `{"items":[{"parent_session_id":"parent/id"}]}`,
			func(c *Client) (any, error) {
				return c.ListSessionChildren(ctx, "p", "parent/id", RawRecordsOptions{Limit: 2})
			}},
		{"GET", "/api/datasets/ds/sessions/s?user_id=reader", 200,
			``, `{"session_id":"s","user_id":"reader","subagent_role":"worker"}`,
			func(c *Client) (any, error) { return c.GetSession(ctx, "ds", "s", "reader") }},
		{"GET", "/api/common?common_category=preference", 200,
			``, `{"items":[{"common_category":"preference"}]}`,
			func(c *Client) (any, error) {
				return c.ListCommonMemory(ctx, KnowledgePageOptions{CommonCategory: "preference"})
			}},
		{"GET", "/api/common/d", 200,
			``, `{"content":"body"}`,
			func(c *Client) (any, error) { return c.GetCommonMemory(ctx, "d") }},
		{"POST", "/api/common", 201,
			`{"common_category":"preference","title":"Title","content":"body"}`, `{"ok":true,"doc_id":"d","common_category":"preference"}`,
			func(c *Client) (any, error) { return c.CreateCommonMemory(ctx, common) }},
		{"PATCH", "/api/common/d", 200,
			`{"title":"Updated"}`, `{"title":"Updated"}`,
			func(c *Client) (any, error) {
				return c.UpdateCommonMemory(ctx, "d", SaveCommonMemoryRequest{Title: ptr("Updated")})
			}},
		{"DELETE", "/api/common/d", 204,
			``, ``,
			func(c *Client) (any, error) { return nil, c.DeleteCommonMemory(ctx, "d") }},
		{"PUT", "/api/common/acl/a", 200,
			`{"agent_id":"a","access":"read"}`, `{"agent_id":"a","access":"read"}`,
			func(c *Client) (any, error) { return c.SetCommonACL(ctx, "a", AccessRead) }},
		{"POST", "/api/search", 200,
			`{"q":"words","filter":"all","project_id":"p","dataset_id":"ds","limit":3,"semantic":true}`, `{"ok":true,"items":[{"doc_id":"d"},{"session_id":"s","user_id":"reader"}],"semantic_hits":[{"document_id":"d","score":0.42}]}`,
			func(c *Client) (any, error) {
				return c.Search(ctx, SearchRequest{Query: "words", Filter: "all", ProjectID: "p", DatasetID: "ds", Limit: 3, Semantic: true})
			}},
		{"GET", "/api/search/recent?limit=9", 200,
			``, `{"recent_searches":[{"query":"words","hit_count":2}],"recent_items":[{"doc_id":"d"}]}`,
			func(c *Client) (any, error) { return c.RecentSearch(ctx, 9) }},
		{"GET", "/api/reading-state?project_id=p", 200,
			``, `{"projects":[{"state_rev":2,"read_horizon_at_ns":"when"}]}`,
			func(c *Client) (any, error) { return c.ReadingState(ctx, "p") }},
		{"POST", "/api/reading-state/mark-read", 200,
			`{"project_id":"p","dataset_id":"ds","doc_ids":["d"]}`, `{"unread_count":2,"marked":1}`,
			func(c *Client) (any, error) {
				return c.MarkRead(ctx, MarkReadRequest{ProjectID: "p", DatasetID: "ds", DocIDs: []string{"d"}})
			}},
		{"POST", "/api/reading-state/mark-all-read", 200,
			`{"reading_identity":"reader","project_id":"p","dataset_id":"ds"}`, `{"unread_count":0,"marked":3}`,
			func(c *Client) (any, error) {
				return c.MarkAllRead(ctx, MarkReadRequest{ReadingIdentity: "reader", ProjectID: "p", DatasetID: "ds"})
			}},
		{"GET", "/api/graph?dataset_id=ds&filter=a+b&project_id=p", 200,
			``, `{"nodes":[{"id":"n"}],"edges":[{"source":"n","target":"m","relationship":"related"}]}`,
			func(c *Client) (any, error) { return c.Graph(ctx, "p", "ds", "a b") }},
		{"GET", "/api/generation", 200,
			``, `{"state":{"next_revision":2},"profiles":[{"profile_id":"g","temperature":0,"has_credential":true}],"model_status":{"active_tasks":5}}`,
			func(c *Client) (any, error) { return c.GetGeneration(ctx) }},
		{"POST", "/api/generation/profiles", 201,
			`{"mode":"provider","model":"m","api_key":"key"}`, `{"profile_id":"g","has_credential":true}`,
			func(c *Client) (any, error) { return c.CreateGenerationProfile(ctx, model) }},
		{"PUT", "/api/generation/profiles/g", 200,
			`{"mode":"provider","model":"m","api_key":"key"}`, `{"profile_id":"g","revision":2}`,
			func(c *Client) (any, error) { return c.SaveGenerationProfile(ctx, "g", model) }},
		{"DELETE", "/api/generation/profiles/g", 204,
			``, ``,
			func(c *Client) (any, error) { return nil, c.DeleteGenerationProfile(ctx, "g") }},
		{"POST", "/api/generation/validate", 200,
			`{"profile_id":"g","revision":2}`, `{"status":"passed","operation_id":"op","schema_passed":true}`,
			func(c *Client) (any, error) { return c.ValidateGenerationProfile(ctx, selection) }},
		{"POST", "/api/generation/activate", 200,
			`{"profile_id":"g","revision":2}`, `{"active_committed":true,"model_digest":"digest","revision":2}`,
			func(c *Client) (any, error) { return c.ActivateGenerationProfile(ctx, selection) }},
		{"GET", "/api/agents", 200,
			``, `{"detected_hosts":[{"supports_capture":true}],"registered_agents":[{"default_access":"none"}]}`,
			func(c *Client) (any, error) { return c.ListAgents(ctx) }},
		{"POST", "/api/agents", 201,
			`{"id":"a","agent_id":"","kind":"pi","name":"","principal_id":"","key_file":"","control_token_file":"","host_profile":"profile"}`, `{"id":"a","kind":"pi"}`,
			func(c *Client) (any, error) {
				return c.ConnectAgent(ctx, ConnectAgentRequest{ID: "a", Kind: "pi", HostProfile: "profile"})
			}},
		{"POST", "/api/agents/a/retire", 200,
			``, `{"retired_at":"when"}`,
			func(c *Client) (any, error) { return c.RetireAgent(ctx, "a") }},
		{"GET", "/api/agents/conflicts?kind=pi", 200,
			``, `{"managed_legacy":[{"entry_name":"managed"}],"third_party_conflicts":[{"blocked":true}],"blocked":true}`,
			func(c *Client) (any, error) { return c.AgentConflicts(ctx, "pi") }},
		{"GET", "/api/profiles", 200,
			``, `{"profiles":[{"source":"pi","document":{"dataset_id":"ds","data_id":"d"}}],"prompt":"Prompt"}`,
			func(c *Client) (any, error) { return c.ListUserProfiles(ctx) }},
		{"GET", "/api/profiles/pi", 200,
			``, `{"source":"pi","body":"profile body","progress":{"stage":"ready"}}`,
			func(c *Client) (any, error) { return c.GetUserProfile(ctx, "pi") }},
		{"POST", "/api/profiles/pi/import", 202,
			`{"operation_id":"op"}`, `{"operation_id":"op","replayed":false}`,
			func(c *Client) (any, error) { return c.StartUserProfileImport(ctx, "pi", "op") }},
		{"POST", "/api/profiles/chatgpt/manual", 200,
			`{"operation_id":"op","body":"profile body"}`, `{"source":"manual:chatgpt","body":"profile body"}`,
			func(c *Client) (any, error) { return c.SaveManualUserProfile(ctx, "chatgpt", "op", "profile body") }},
		{"POST", "/api/profiles/pi/cancel", 200,
			`{"operation_id":"op"}`, `{"cancel_requested":true}`,
			func(c *Client) (any, error) { return c.CancelUserProfileImport(ctx, "pi", "op") }},
		{"DELETE", "/api/profiles/pi", 200,
			``, `{"deleted":true}`,
			func(c *Client) (any, error) { return c.DeleteUserProfile(ctx, "pi") }},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.uri, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.uri {
					t.Errorf("got %s %s, want %s %s", r.Method, r.URL.RequestURI(), tc.method, tc.uri)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if tc.input != "" {
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing JSON content type")
					}
					if !reflect.DeepEqual(decodeTestJSON(t, string(body)), decodeTestJSON(t, tc.input)) {
						t.Errorf("body = %s, want %s", body, tc.input)
					}
				} else if len(body) != 0 {
					t.Errorf("unexpected request body %s", body)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.receipt)
			}))
			defer server.Close()
			out, err := tc.call(testClient(t, Config{BaseURL: server.URL}))
			if err != nil {
				t.Fatal(err)
			}
			if tc.receipt != "" {
				actual, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				if !jsonIncludes(decodeTestJSON(t, string(actual)), decodeTestJSON(t, tc.receipt)) {
					t.Fatalf("response fields lost: got %s, want %s", actual, tc.receipt)
				}
			}
		})
	}
}

func decodeTestJSON(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Errorf("invalid JSON %q: %v", raw, err)
	}
	return value
}

func jsonIncludes(got, want any) bool {
	switch want := want.(type) {
	case map[string]any:
		m, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range want {
			if actual, exists := m[key]; !exists || !jsonIncludes(actual, value) {
				return false
			}
		}
		return true
	case []any:
		list, ok := got.([]any)
		if !ok || len(list) != len(want) {
			return false
		}
		for i, value := range want {
			if !jsonIncludes(list[i], value) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}

func TestGenerationOptionalParametersPreserveExplicitZero(t *testing.T) {
	received := make(chan map[string]json.RawMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		fmt.Fprint(w, "{}")
	}))
	defer server.Close()
	client := testClient(t, Config{BaseURL: server.URL})
	profile := GenerationProfile{Mode: "provider", Model: "m"}
	empty := profile
	profile.Temperature = ptr(0.0)
	profile.MaxOutputTokens = ptr(0)
	profile.MaxConcurrentRequests = ptr(0)
	for _, p := range []GenerationProfile{profile, empty} {
		if _, err := client.SaveGenerationProfile(context.Background(), "g", SaveGenerationProfileRequest{GenerationProfile: p}); err != nil {
			t.Fatal(err)
		}
	}
	withZero, omitted := <-received, <-received
	for _, key := range []string{"temperature", "max_output_tokens", "max_concurrent_requests"} {
		if string(withZero[key]) != "0" || omitted[key] != nil {
			t.Errorf("%s: explicit zero = %q, omitted = %q", key, withZero[key], omitted[key])
		}
	}
}

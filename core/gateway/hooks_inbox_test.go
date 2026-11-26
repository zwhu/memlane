package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func agentRequest(t *testing.T, s *Server, path, body string, want int) map[string]any {
	t.Helper()
	return decodeResponse[map[string]any](t, request(t, s, "POST", path, body, map[string]string{"X-Api-Key": testAgent}, want))
}

func TestGatewayAuthentication(t *testing.T) {
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.AgentTokens["unregistered"] = "ghost-key"
		cfg.AgentTokens["archived"] = "archived-key"
		cfg.AgentTokens["retired"] = "retired-key"
	})
	ctx := context.Background()
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "archived", Name: "archived", Archived: true}))
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "retired", Name: "retired", RetiredAt: " 2026-01-01 "}))
	for _, path := range []string{"/ingest", "/recall"} {
		for _, tc := range []struct {
			key, code string
			status    int
		}{
			{"", "unauthorized", 401}, {"bad", "agent_not_registered", 403}, {"ghost-key", "agent_not_registered", 403},
			{"archived-key", "agent_retired", 403}, {"retired-key", "agent_retired", 403},
		} {
			requireCode(t, request(t, s, "POST", path, `{}`, map[string]string{"X-Api-Key": tc.key}, tc.status), tc.code)
		}
	}
	requireCode(t, request(t, s, "GET", "/v1/health", "", map[string]string{"Origin": "null", "Authorization": "Bearer " + testManagement}, 403), "browser_origin_rejected")
	requireCode(t, request(t, s, "GET", "/api/health", "", map[string]string{"X-Api-Key": testAgent}, 403), "management_required")
	requireCode(t, request(t, s, "GET", "/api/health", "", map[string]string{"Authorization": "Bearer wrong"}, 401), "unauthorized")
	request(t, s, "GET", "/api/health", "", map[string]string{"X-Memlane-Management": testManagement}, 200)

	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	control := filepath.Join(dir, "control")
	must(t, os.WriteFile(key, []byte("private-agent\n"), 0o600))
	must(t, os.Symlink(key, control))
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "file-agent", Name: "file-agent", KeyFile: control}))
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer private-agent"}, 403), "agent_not_registered")
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: "file-agent", Name: "file-agent", ControlTokenFile: key}))
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer private-agent"}, 400), "invalid_recall")
	must(t, os.Chmod(key, 0o644))
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer private-agent"}, 403), "agent_not_registered")
	must(t, os.Chmod(key, 0o600))
	must(t, os.WriteFile(key, []byte("rotated-agent\n"), 0o600))
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer private-agent"}, 403), "agent_not_registered")
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer rotated-agent"}, 400), "invalid_recall")
	manage(t, s, "POST", "/api/agents/file-agent/retire", "", 200)
	requireCode(t, request(t, s, "POST", "/recall", `{}`, map[string]string{"Authorization": "Bearer rotated-agent"}, 403), "agent_retired")
}

func TestCapturePauseAndIdempotency(t *testing.T) {
	s, store, _ := testServer(t, nil)
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/projects/p/auto-capture", `{"auto_capture":false}`, 200)
	body := `{"project_id":"p","session_id":"session","message_id":"msg","content":{"question":"q","answer":"a"}}`
	paused := agentRequest(t, s, "/ingest", body, 403)
	if paused["error"] != "capture_paused" || paused["capture_scope"] != "project" || paused["project_auto_capture"] != false {
		t.Fatal(paused)
	}
	agentRequest(t, s, "/recall", `{"project_id":"p","query":"q"}`, 200)
	manual := strings.Replace(body, `"project_id"`, `"mode":"manual","project_id"`, 1)
	accepted := agentRequest(t, s, "/ingest", manual, 202)
	if accepted["status"] != "pending" || accepted["deduplicated"] != false {
		t.Fatal(accepted)
	}
	dupe := agentRequest(t, s, "/ingest", manual, 200)
	if dupe["deduplicated"] != true || dupe["inbox_id"] != accepted["inbox_id"] {
		t.Fatal(dupe)
	}
	canonical := strings.Replace(manual, `"question":"q","answer":"a"`, `"answer":"a","question":"q"`, 1)
	agentRequest(t, s, "/ingest", canonical, 200)
	conflict := agentRequest(t, s, "/ingest", strings.Replace(manual, `"answer":"a"`, `"answer":"changed"`, 1), 409)
	if conflict["error"] != "capture_conflict" {
		t.Fatal(conflict)
	}
	request(t, s, "POST", "/ingest", `{"session_id":"session","message_id":"another","content":{"question":"q","answer":"a"}}`,
		map[string]string{"Authorization": "Bearer " + testAgent, "X-Memlane-Capture-Mode": "MANUAL"}, 202)
	if n := countRows(t, store, "capture_inbox"); n != 2 {
		t.Fatalf("inbox rows = %d", n)
	}
	manage(t, s, "PUT", "/api/projects/p/acl", `{"agent_id":"agent","access":"read"}`, 200)
	if denied := agentRequest(t, s, "/ingest", manual, 403); denied["error"] != "dataset_write_denied" {
		t.Fatal(denied)
	}
}

func TestIngestRejectsProtectedProjects(t *testing.T) {
	s, store, _ := testServer(t, nil)
	for _, body := range []string{
		`{"id":"protected","name":"ml-user-profiles","dataset_name":"Profiles","dataset_id":"profiles"}`,
		`{"id":"protected","name":"Profiles","dataset_name":"ml-user-profiles","dataset_id":"profiles"}`,
	} {
		manage(t, s, "POST", "/api/projects", body, 201)
		manage(t, s, "PUT", "/api/projects/protected/acl/agent", `{"access":"maintain","capture":true}`, 200)
		for _, mode := range []string{"automatic", "manual"} {
			body := fmt.Sprintf(`{"project_id":"protected","mode":%q,"content":"profile"}`, mode)
			if got := agentRequest(t, s, "/ingest", body, 403); got["error"] != "project_protected" {
				t.Fatal(got)
			}
		}
		manage(t, s, "DELETE", "/api/projects/protected", "", 204)
	}
	if countRows(t, store, "capture_inbox") != 0 || countRows(t, store, "capture_receipts") != 0 {
		t.Fatal("protected ingest persisted capture data")
	}
}

func TestRecallScopesAndMetrics(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	must(t, store.UpsertProject(ctx, domain.Project{ID: "private", Name: "Private", DatasetID: "private-ds"}))
	manage(t, s, "PATCH", "/api/projects/p", `{"code_enabled":true,"code_dataset_id":"code-ds"}`, 200)
	fake.hits = []engine.SearchHit{{DatasetID: "ds", DocumentID: "a"}, {DatasetID: "ds", DocumentID: "a"},
		{DatasetID: "common", DocumentID: "c"}, {DatasetID: "private-ds", DocumentID: "leak"}, {DatasetID: "code-ds", DocumentID: "code"}}
	manage(t, s, "PUT", "/v1/projects/p/overrides", `{"agent_id":"agent","access":"read"}`, 200)
	agentRequest(t, s, "/recall", `{"query":"q","dataset_ids":["ds","private-ds"]}`, 403)
	if len(fake.search) != 0 {
		t.Fatal("search reached the engine with a denied scope")
	}
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"read"}`, 200)
	out := agentRequest(t, s, "/recall", `{"query":"q","project_id":"p","channel":"explicit"}`, 200)
	if out["common_included"] != true || len(out["hits"].([]any)) != 2 || out["event_id"] == "" {
		t.Fatal(out)
	}
	for _, body := range []string{
		`{"query":"q","project_id":"p","kind":"CoDe"}`, `{"query":"q","project_id":"p","scope":"code"}`,
		`{"query":"q","dataset_ids":["code-ds"]}`,
	} {
		out := agentRequest(t, s, "/recall", body, 200)
		if out["common_included"] != false || len(out["hits"].([]any)) != 1 {
			t.Fatal(out)
		}
	}
	if fake.remaining < 14*time.Second || fake.remaining > 15*time.Second {
		t.Fatalf("search deadline = %s", fake.remaining)
	}
	count, err := store.GetRecallDocCount(ctx, "ds", "a")
	must(t, err)
	if count.TotalHits != 1 || count.ExplicitHits != 1 {
		t.Fatal(count)
	}
	if countRows(t, store, "recall_events") != 4 || countRows(t, store, "recall_hits") != 5 {
		t.Fatal("recall events and hits diverged")
	}
	stats, err := store.GetRecallQueryStats(ctx)
	must(t, err)
	if len(stats) != 2 || stats[0].TotalHits+stats[1].TotalHits != 5 {
		t.Fatal(stats)
	}
}

func TestCaptureQAContextDate(t *testing.T) {
	s, _, fake := testServer(t, nil)
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	for _, tc := range []struct {
		name, content, sourceDate, want string
	}{
		{"context object", `{"context":{"date":"2026-01-15"},"prompt_time":"2026-03-10T12:00:00Z","time":"2026-04-05T12:00:00Z"}`, "", "2026-01-15"},
		{"encoded context", `{"context":"{\"date\":\"2026-02-20\"}"}`, "", "2026-02-20"},
		{"prompt time", `{"context":{"date":"invalid"},"prompt_time":"2026-03-10T12:00:00Z","time":"2026-04-05T12:00:00Z"}`, " ", domain.LocalCalendarDate("2026-03-10T12:00:00Z", time.Local)},
		{"time", `{"time":"2026-04-05T12:00:00Z"}`, "", domain.LocalCalendarDate("2026-04-05T12:00:00Z", time.Local)},
		{"explicit source date", `{"context":{"date":"2026-01-15"}}`, "2026-05-01", "2026-05-01"},
		{"missing dates", `{"question":"q","answer":"a"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now().Format(time.DateOnly)
			body := fmt.Sprintf(`{"project_id":"p","content":%s,"source_meta":{"context_date":%q}}`, tc.content, tc.sourceDate)
			agentRequest(t, s, "/ingest", body, http.StatusAccepted)
			after := time.Now().Format(time.DateOnly)
			worked, err := s.Inbox().ProcessOnce(context.Background())
			must(t, err)
			if !worked {
				t.Fatal("no pending capture claimed")
			}
			got := fake.ingest[len(fake.ingest)-1].SourceMeta.ContextDate
			if tc.want == "" {
				if got != before && got != after {
					t.Fatalf("context date = %q, want today's local date (%q or %q)", got, before, after)
				}
			} else if got != tc.want {
				t.Fatalf("context date = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInboxFailureAndRetry(t *testing.T) {
	s, store, fake := testServer(t, func(c *Config) { c.OutputLanguage = "fr" })
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	fake.syncErr = errors.New("publish rejected Authorization: Bearer confidential-secret")
	out := agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s","message_id":"m","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
	id := out["inbox_id"].(string)
	ctx := context.Background()
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("no pending item claimed")
	}
	failed, err := store.GetInboxItem(ctx, id)
	must(t, err)
	if failed.Status != "failed" || failed.Attempts != 1 || strings.Contains(failed.LastError, "confidential-secret") ||
		!strings.Contains(failed.LastError, "REDACTED") {
		t.Fatal(failed)
	}
	receipt, err := store.GetCaptureReceipt(ctx, out["capture_key"].(string))
	must(t, err)
	if receipt.Status != "saved" || receipt.EntryID == "" {
		t.Fatal(receipt)
	}
	if fmt.Sprint(fake.calls) != "[ingest extract sync]" {
		t.Fatal(fake.calls)
	}
	if d := fake.extract[0]; d.OutputLanguage != "fr" || d.ExpectedModelDigest != "live-digest" || len(d.DocIDs) != 1 {
		t.Fatal(d)
	}
	fake.syncErr = nil
	if got := decodeResponse[map[string]any](t, manage(t, s, "POST", "/api/projects/p/retry", "", 200)); got["retried_count"] != float64(1) {
		t.Fatal(got)
	}
	s.Inbox().Start(ctx)
	stop, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stop))
	item, err := store.GetInboxItem(ctx, id)
	must(t, err)
	if item.Status != "succeeded" || item.Attempts != 2 || item.LastError != "" {
		t.Fatal(item)
	}
}

func TestRetryFailedProjectScope(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	for _, project := range []domain.Project{
		{ID: "p", DatasetID: "ds"}, {ID: "q", Name: "Q", DatasetID: "q"}, {ID: "r", Name: "R", DatasetID: "r"},
	} {
		if project.ID != "p" {
			must(t, store.UpsertProject(ctx, project))
		}
		item, err := store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{ID: project.ID, ProjectID: project.ID,
			DatasetID: project.DatasetID, AgentID: "agent", SessionID: "s", MessageID: "m", Kind: "document", PayloadJSON: "{}"})
		must(t, err)
		_, err = store.ClaimNextInboxItem(ctx)
		must(t, err)
		must(t, store.MarkInboxFailed(ctx, item.ID, "test failure"))
	}
	for _, tc := range []struct {
		project string
		want    []string
	}{{"p", []string{"p"}}, {"", []string{"q", "r"}}} {
		got, err := s.Inbox().RetryFailed(ctx, tc.project)
		must(t, err)
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("RetryFailed(%q) = %v, want %v", tc.project, got, tc.want)
		}
	}
	summary, err := s.Inbox().Summary(ctx, "")
	must(t, err)
	if summary.Pending != 3 || summary.Failed != 0 {
		t.Fatal(summary)
	}
}

func TestInboxBoundedWorkersAndDuplicateRequests(t *testing.T) {
	s, store, _ := testServer(t, func(c *Config) { c.InboxWorkers = 3 })
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	var group sync.WaitGroup
	ctx := context.Background()
	s.Inbox().Start(ctx)
	for range 12 {
		group.Go(func() {
			req := newCaptureTestRequest()
			recorder := httptest.NewRecorder()
			s.Handler().ServeHTTP(recorder, req)
			result := recorder.Code
			if result != 200 && result != 202 {
				t.Errorf("concurrent ingest status = %d", result)
			}
		})
	}
	group.Wait()
	stop, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stop))
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Succeeded != 1 || summary.Pending != 0 || summary.Processing != 0 || summary.Running || countRows(t, store, "capture_receipts") != 1 {
		t.Fatal(summary)
	}
}

func newCaptureTestRequest() *http.Request {
	req := httptest.NewRequest("POST", "/ingest", strings.NewReader(`{"project_id":"p","session_id":"s","message_id":"m","content":{"question":"q","answer":"a"}}`))
	req.Header.Set("X-Api-Key", testAgent)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestInboxWorkerUsesOwnerScopeDespiteAgentPrincipalID(t *testing.T) {
	s, store, eng := testServer(t, nil)
	ctx := context.Background()
	agent, err := store.GetAgent(ctx, "agent")
	must(t, err)
	agent.PrincipalID = "11111111-1111-4111-8111-111111111111"
	must(t, store.UpsertAgent(ctx, agent))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, newCaptureTestRequest())
	if recorder.Code != 202 {
		t.Fatalf("ingest status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected inbox worker to process queued item")
	}
	if len(eng.ingest) != 1 || len(eng.extract) != 1 {
		t.Fatalf("expected 1 ingest + 1 extract call, got %d / %d", len(eng.ingest), len(eng.extract))
	}
	if eng.ingest[0].Scope.UserID != "" || eng.extract[0].Scope.UserID != "" {
		t.Fatalf("expected empty owner scope UserID for engine writes, got ingest=%q extract=%q",
			eng.ingest[0].Scope.UserID, eng.extract[0].Scope.UserID)
	}
	if eng.ingest[0].Scope.DatasetID != "ds" || eng.extract[0].Scope.DatasetID != "ds" {
		t.Fatalf("unexpected dataset scope: ingest=%q extract=%q",
			eng.ingest[0].Scope.DatasetID, eng.extract[0].Scope.DatasetID)
	}
}

func TestIngestPersistsSessionRelations(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. Top-level parent_session_id and subagent_role.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"child-top","parent_session_id":"parent-1","subagent_role":"research",
		"message_id":"m1","kind":"capture_trace","content":{"origin_function":"Read"}
	}`, http.StatusAccepted)

	// 2. Subsequent event without subagent_role preserves the stored role.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"child-top","parent_session_id":"parent-1",
		"message_id":"m2","kind":"capture_trace","content":{"origin_function":"Grep"}
	}`, http.StatusAccepted)

	// 3. Fallback to source_meta.external_metadata.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"child-ext","message_id":"m3","kind":"capture_trace",
		"content":{"origin_function":"Bash"},
		"source_meta":{"external_metadata":{"parent_session_id":"parent-2","subagent_role":"worker"}}
	}`, http.StatusAccepted)

	// 4. Self-parent is ignored cleanly without failing /ingest.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"self-loop","parent_session_id":"self-loop",
		"message_id":"m4","kind":"capture_trace","content":{"origin_function":"Edit"}
	}`, http.StatusAccepted)

	rels, err := store.ListSessionRelations(ctx, "ds")
	must(t, err)
	if len(rels) != 2 {
		t.Fatalf("session relations = %+v, want 2", rels)
	}
	byID := map[string]domain.SessionRelation{}
	for _, r := range rels {
		byID[r.SessionID] = r
	}
	if r := byID["child-top"]; r.ParentSessionID != "parent-1" || r.SubagentRole != "research" {
		t.Fatalf("child-top relation = %+v", r)
	}
	if r := byID["child-ext"]; r.ParentSessionID != "parent-2" || r.SubagentRole != "worker" {
		t.Fatalf("child-ext relation = %+v", r)
	}
}

func TestInboxSkipsDefaultExtractForCaptureTrace(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. Default capture_trace ingests + marks receipt saved + syncs catalog, WITHOUT calling Extract.
	traceResp := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s1","message_id":"trace-1","kind":"capture_trace",
		"content":{"origin_function":"Bash","method_params":{"command":"go test"}}
	}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected trace item to be processed")
	}
	if fmt.Sprint(fake.calls) != "[ingest sync]" || len(fake.extract) != 0 {
		t.Fatalf("default capture_trace should skip Extract: calls=%v extract=%+v", fake.calls, fake.extract)
	}
	receipt, err := store.GetCaptureReceipt(ctx, traceResp["capture_key"].(string))
	must(t, err)
	if receipt.Status != "saved" || receipt.EntryID == "" {
		t.Fatalf("trace receipt not saved: %+v", receipt)
	}
	item, err := store.GetInboxItem(ctx, traceResp["inbox_id"].(string))
	must(t, err)
	if item.Status != "succeeded" {
		t.Fatalf("trace inbox item = %+v", item)
	}

	// 2. capture_trace with explicit tool_graph extract_mode still runs Extract.
	fake.calls = nil
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s1","message_id":"trace-tg","kind":"capture_trace",
		"extract_mode":"tool_graph","content":{"origin_function":"Edit"}
	}`, http.StatusAccepted)
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || fmt.Sprint(fake.calls) != "[ingest extract sync]" || len(fake.extract) != 1 || fake.extract[0].Mode != engine.ExtractModeToolGraph {
		t.Fatalf("tool_graph trace should call Extract: calls=%v extract=%+v", fake.calls, fake.extract)
	}

	// 3. capture_qa and document still trigger Extract.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"capture_qa", `{"project_id":"p","session_id":"s1","message_id":"qa-1","kind":"capture_qa","content":{"question":"q","answer":"a"}}`},
		{"document", `{"project_id":"p","session_id":"s1","message_id":"doc-1","kind":"document","content":"hello doc"}`},
	} {
		fake.calls = nil
		beforeExtract := len(fake.extract)
		agentRequest(t, s, "/ingest", tc.body, http.StatusAccepted)
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked || fmt.Sprint(fake.calls) != "[ingest extract sync]" || len(fake.extract) != beforeExtract+1 {
			t.Fatalf("%s should trigger Extract: calls=%v extracts=%d", tc.name, fake.calls, len(fake.extract)-beforeExtract)
		}
	}
}

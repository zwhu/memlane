package gateway

import (
	"context"
	"encoding/json"
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
	"unicode/utf8"

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
	for _, tc := range []struct {
		id   string
		body string
	}{
		{"protected-by-name", `{"id":"protected-by-name","name":"ml-user-profiles","dataset_name":"Profiles","dataset_id":"profiles-1"}`},
		{"protected-by-dataset", `{"id":"protected-by-dataset","name":"Profiles","dataset_name":"ml-user-profiles","dataset_id":"profiles-2"}`},
	} {
		manage(t, s, "POST", "/api/projects", tc.body, 201)
		manage(t, s, "PUT", "/api/projects/"+tc.id+"/acl/agent", `{"access":"maintain","capture":true}`, 200)
		for _, mode := range []string{"automatic", "manual"} {
			body := fmt.Sprintf(`{"project_id":%q,"mode":%q,"content":"profile"}`, tc.id, mode)
			if got := agentRequest(t, s, "/ingest", body, 403); got["error"] != "project_protected" {
				t.Fatal(got)
			}
		}
		manage(t, s, "DELETE", "/api/projects/"+tc.id, "", 403)
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
	fake.extractErr = errors.New("extract rejected Authorization: Bearer confidential-secret")
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
	if fmt.Sprint(fake.calls) != "[ingest extract]" {
		t.Fatal(fake.calls)
	}
	wantDigest, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	if d := fake.extract[0]; d.OutputLanguage != "fr" || d.ExpectedModelDigest != wantDigest || d.ExpectedModelDigest == "live-digest" || len(d.DocIDs) != 1 {
		t.Fatal(d)
	}
	fake.extractErr = nil
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

	// 4. Self-parent (even with subagent_role) is ignored cleanly without failing /ingest or leaking role.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"self-loop","parent_session_id":"self-loop","subagent_role":"bogus",
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
	if _, leaked := byID["self-loop"]; leaked {
		t.Fatalf("self-parent leaked into session_relations: %+v", byID["self-loop"])
	}
}

type relationCheckProjector struct {
	store    *domain.Store
	syncDone chan []domain.SessionRelation
}

func (p *relationCheckProjector) SnapshotCatalog(ctx context.Context, _ []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	rels, err := p.store.ListSessionRelations(ctx, "ds")
	if err != nil {
		return nil, err
	}
	select {
	case p.syncDone <- rels:
	default:
	}
	return &engine.CatalogSnapshot{}, nil
}

func (p *relationCheckProjector) ReadLocalBody(context.Context, engine.LocalBodyRef) (string, error) {
	return "", nil
}

func TestIngestPersistsSessionRelationBeforeInboxWorkerSync(t *testing.T) {
	proj := &relationCheckProjector{syncDone: make(chan []domain.SessionRelation, 1)}
	s, store, _ := testServer(t, func(c *Config) {
		c.Projector = proj
	})
	proj.store = store
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	s.Inbox().Start(ctx)
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"race-child","parent_session_id":"race-parent","subagent_role":"worker",
		"message_id":"race-1","kind":"capture_trace","content":{"origin_function":"Read"}
	}`, http.StatusAccepted)
	select {
	case rels := <-proj.syncDone:
		if len(rels) != 1 || rels[0].SessionID != "race-child" || rels[0].ParentSessionID != "race-parent" || rels[0].SubagentRole != "worker" {
			t.Fatalf("SyncCatalog observed missing/incomplete session_relations: %+v", rels)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for InboxWorker SyncCatalog")
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

type mintingIngestEngine struct {
	*fakeEngine
	mu          sync.Mutex
	ingestCalls int
}

func (m *mintingIngestEngine) Ingest(_ context.Context, in engine.IngestRequest) (engine.IngestResult, error) {
	m.mu.Lock()
	m.ingestCalls++
	seq := m.ingestCalls
	m.mu.Unlock()
	m.fakeEngine.mu.Lock()
	defer m.fakeEngine.mu.Unlock()
	m.fakeEngine.calls = append(m.fakeEngine.calls, "ingest")
	m.fakeEngine.ingest = append(m.fakeEngine.ingest, in)
	if m.fakeEngine.ingestErr != nil {
		return engine.IngestResult{}, m.fakeEngine.ingestErr
	}
	entryID := fmt.Sprintf("minted-entry-%d", seq)
	docID := ""
	if in.Kind == engine.IngestKindDocument {
		docID = entryID
	}
	return engine.IngestResult{OK: true, EntryID: entryID, DocumentID: docID}, nil
}

func TestInboxRetrySkipsIngestWhenReceiptAlreadySaved(t *testing.T) {
	var eng *mintingIngestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &mintingIngestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Attempt 1: Ingest succeeds (saving receipt with minted-entry-1), then Extract times out.
	fake.extractErr = &engine.EngineError{
		Code:    "extract_timeout",
		Message: "engine request timed out",
		Err:     errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
	}
	out := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-retry","message_id":"m-retry",
		"kind":"capture_qa","content":{"question":"q","answer":"a"}
	}`, http.StatusAccepted)
	inboxID := out["inbox_id"].(string)
	captureKey := out["capture_key"].(string)

	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected attempt 1 to claim inbox item")
	}
	if eng.ingestCalls != 1 || len(fake.extract) != 1 {
		t.Fatalf("attempt 1 calls: ingest=%d extract=%d", eng.ingestCalls, len(fake.extract))
	}
	receipt, err := store.GetCaptureReceipt(ctx, captureKey)
	must(t, err)
	if receipt.Status != "saved" || receipt.EntryID != "minted-entry-1" {
		t.Fatalf("unexpected saved receipt after attempt 1: %+v", receipt)
	}
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Failed != 1 || len(summary.RecentFailures) != 1 {
		t.Fatalf("expected 1 failed inbox item in summary: %+v", summary)
	}
	failure := summary.RecentFailures[0]
	if failure.ErrorCode != "extract_timeout" || strings.Contains(failure.Summary, "暂时无法连接") ||
		!strings.Contains(failure.Summary, "超时") || !strings.Contains(failure.NextAction, "一键重试") {
		t.Fatalf("unexpected timeout failure classification: %+v", failure)
	}

	// Attempt 2 after RetryFailed: must skip Ingest (avoiding receipt conflict) and extract using session ID.
	fake.extractErr = nil
	fake.calls = nil
	retried, err := s.Inbox().RetryFailed(ctx, "p")
	must(t, err)
	if len(retried) != 1 || retried[0] != inboxID {
		t.Fatalf("RetryFailed = %v, want [%s]", retried, inboxID)
	}
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected attempt 2 to claim retried inbox item")
	}
	if eng.ingestCalls != 1 {
		t.Fatalf("retry called Ingest again: ingestCalls=%d, want 1", eng.ingestCalls)
	}
	if fmt.Sprint(fake.calls) != "[extract sync]" || len(fake.extract) != 2 {
		t.Fatalf("attempt 2 calls = %v, extracts = %+v", fake.calls, fake.extract)
	}
	if !slices.Equal(fake.extract[1].DocIDs, []string{"sess-retry"}) {
		t.Fatalf("attempt 2 extract DocIDs = %v, want [sess-retry]", fake.extract[1].DocIDs)
	}
	item, err := store.GetInboxItem(ctx, inboxID)
	must(t, err)
	if item.Status != "succeeded" || item.Attempts != 2 || item.LastError != "" {
		t.Fatalf("unexpected item state after retry: %+v", item)
	}
	receiptAfter, err := store.GetCaptureReceipt(ctx, captureKey)
	must(t, err)
	if receiptAfter.Status != "saved" || receiptAfter.EntryID != "minted-entry-1" {
		t.Fatalf("receipt changed on retry: %+v", receiptAfter)
	}
}

func TestInboxRetrySavedCaptureTraceSkipsIngestAndExtract(t *testing.T) {
	var eng *mintingIngestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &mintingIngestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Attempt 1: capture_trace ingests + saves receipt, skips Extract, then fails in catalog publish.
	// Catalog publish failure is tracked independently in catalog_publish_state while inbox succeeds.
	fake.syncErr = errors.New("temporary catalog sync error")
	out := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-trace","message_id":"m-trace",
		"kind":"capture_trace","content":{"origin_function":"Read"}
	}`, http.StatusAccepted)
	inboxID := out["inbox_id"].(string)

	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || eng.ingestCalls != 1 || len(fake.extract) != 0 {
		t.Fatalf("attempt 1 trace: worked=%v ingest=%d extract=%d", worked, eng.ingestCalls, len(fake.extract))
	}
	item, err := store.GetInboxItem(ctx, inboxID)
	must(t, err)
	if item.Status != "succeeded" {
		t.Fatalf("trace inbox item should succeed when only catalog publish fails: %+v", item)
	}
	pubState, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if pubState.Status != "failed" || pubState.Attempts != 1 {
		t.Fatalf("catalog_publish_state = %+v, want failed attempts=1", pubState)
	}

	// Attempt 2: RetryFailed retries catalog publish directly with zero Ingest and zero Extract calls.
	fake.syncErr = nil
	fake.calls = nil
	retried, err := s.Inbox().RetryFailed(ctx, "p")
	must(t, err)
	if len(retried) != 1 || retried[0] != "catalog:ds" {
		t.Fatalf("RetryFailed = %v, want [catalog:ds]", retried)
	}
	if eng.ingestCalls != 1 || len(fake.extract) != 0 || fmt.Sprint(fake.calls) != "[sync]" {
		t.Fatalf("retried catalog publish should skip Ingest and Extract: ingest=%d extract=%d calls=%v", eng.ingestCalls, len(fake.extract), fake.calls)
	}
	pubStateAfter, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if pubStateAfter.Status != "ok" || pubStateAfter.LastError != "" {
		t.Fatalf("catalog_publish_state after retry = %+v, want ok", pubStateAfter)
	}
}

func TestDescribeInboxFailureExtractTimeout(t *testing.T) {
	for _, raw := range []string{
		"extract inbox: extract_timeout: engine request timed out",
		"extract_timeout: engine request timed out",
		"extract inbox: context deadline exceeded",
	} {
		code, summary, nextAction := describeInboxFailure(raw)
		if code != "extract_timeout" || strings.Contains(summary, "暂时无法连接") ||
			!strings.Contains(summary, "超时") || !strings.Contains(nextAction, "一键重试") {
			t.Errorf("describeInboxFailure(%q) = (%q, %q, %q), want extract_timeout", raw, code, summary, nextAction)
		}
	}
	for _, raw := range []string{
		"engine_timeout: engine request timed out",
		"ingest inbox: engine_timeout: engine request timed out",
		"engine request timed out",
		"context deadline exceeded",
	} {
		code, summary, nextAction := describeInboxFailure(raw)
		if code != "engine_timeout" || summary != "提炼服务连接超时" || nextAction == "" {
			t.Errorf("describeInboxFailure(%q) = (%q, %q, %q), want engine_timeout", raw, code, summary, nextAction)
		}
	}
	code, summary, _ := describeInboxFailure("dial tcp: connection refused")
	if code != "engine_unavailable" || summary != "提炼服务暂时无法连接" {
		t.Errorf("connection refused = (%q, %q), want engine_unavailable", code, summary)
	}
}

type hybridRecallTestEngine struct {
	*fakeEngine
	mu           sync.Mutex
	graphContext *engine.GraphSearchContext
	history      engine.SessionHistoryResult
	historyErr   error
	extractHook  func(context.Context, engine.ExtractRequest) (engine.ExtractResult, error)
}

func (h *hybridRecallTestEngine) Search(ctx context.Context, in engine.SearchRequest) (engine.SearchResult, error) {
	res, err := h.fakeEngine.Search(ctx, in)
	if err != nil {
		return res, err
	}
	h.mu.Lock()
	res.GraphContext = h.graphContext
	h.mu.Unlock()
	return res, nil
}

func (h *hybridRecallTestEngine) Extract(ctx context.Context, in engine.ExtractRequest) (engine.ExtractResult, error) {
	h.mu.Lock()
	hook := h.extractHook
	h.mu.Unlock()
	if hook != nil {
		return hook(ctx, in)
	}
	return h.fakeEngine.Extract(ctx, in)
}

func (h *hybridRecallTestEngine) HydrateSessionsFromCache(_ context.Context, _ []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	return map[engine.SessionRef]engine.SessionCacheInfo{}, nil
}

func (h *hybridRecallTestEngine) ReadSessionHistory(_ context.Context, _ engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.history, h.historyErr
}

func TestRecallHybridFormattingDenoisingAndACL(t *testing.T) {
	var eng *hybridRecallTestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &hybridRecallTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()
	must(t, store.UpsertProject(ctx, domain.Project{ID: "secret", Name: "Secret", DatasetID: "secret-ds"}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "sess-1", UserID: "u-1", SortTSDescKey: -10, IsTopLevel: true,
	}))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"read"}`, 200)
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"read"}`, 200)

	// Invalid mode / purpose rejected with 400 before hitting engine.
	requireCode(t, request(t, s, "POST", "/recall", `{"project_id":"p","query":"q","mode":"bogus"}`,
		map[string]string{"X-Api-Key": testAgent}, 400), "invalid_recall")
	requireCode(t, request(t, s, "POST", "/recall", `{"project_id":"p","query":"q","purpose":"unknown"}`,
		map[string]string{"X-Api-Key": testAgent}, 400), "invalid_recall")
	if len(fake.search) != 0 {
		t.Fatalf("invalid recall reached engine: %+v", fake.search)
	}

	fake.hits = []engine.SearchHit{
		{DatasetID: "secret-ds", DocumentID: "leak-doc", Title: "Secret", Snippet: "classified"},
		{DatasetID: "ds", DocumentID: "self-1", Title: "Current transcript", Snippet: "Session ID: sess-1\nUser: current question"},
		{DatasetID: "ds", DocumentID: "self-2", Title: "Current learning", Snippet: "# Session learning 2026-11-26 (session sess-1)\nEchoed learning"},
		{DatasetID: "ds", DocumentID: "prefix-ok", Title: "Other session", Snippet: "Session ID: sess-10\nUser: keep prefix neighbor", Metadata: map[string]any{"kind": "raw_doc"}},
		{DatasetID: "ds", DocumentID: "distilled-1", Title: "WAL rule", Snippet: "Always set PRAGMA journal_size_limit on SQLite WAL.", Metadata: map[string]any{"lesson_type": "convention"}},
		{DatasetID: "common", DocumentID: "common-1", Title: "General Go tip", Snippet: "Prefer concise error messages."},
	}
	eng.graphContext = &engine.GraphSearchContext{
		Entities: []engine.GraphSearchEntity{
			{
				DatasetID: "secret-ds",
				Name:      "SecretEntity",
				Type:      "Service",
				Edges:     []engine.GraphSearchEdge{{Text: "SecretEntity stores private keys."}},
				Conflicts: []string{"Secret conflict must not leak"},
			},
			{
				DatasetID: "ds",
				Name:      "OnlyNoiseEntity",
				Type:      "Concept",
				Edges: []engine.GraphSearchEdge{
					{Text: "OnlyNoiseEntity is a concept."},
					{Text: "OnlyNoiseEntity belongs to set session_learnings."},
				},
			},
			{
				DatasetID: "ds",
				Name:      "Gateway",
				Type:      "Component",
				Edges: []engine.GraphSearchEdge{
					{Text: "Gateway is a web service."},
					{Text: "Gateway belongs to set core"},
					{Text: "Gateway enforces project ACL before search.", Authoritative: true},
					{Text: "Gateway used legacy sync ingest.", Superseded: true},
				},
				Conflicts: []string{"Ingest switched from sync to async inbox."},
			},
		},
		Facts: []engine.GraphSearchFact{
			{DatasetID: "secret-ds", Text: "Secret fact must be dropped."},
			{DatasetID: "ds", Text: "Gateway is an application."},
			{DatasetID: "ds", Text: "Gateway enforces project ACL before search."},
			{DatasetID: "ds", Text: "Daemon holds flock on .service-owner."},
		},
		Conflicts: []engine.GraphSearchConflict{
			{DatasetID: "secret-ds", Text: "Secret conflict must not leak"},
			{DatasetID: "secret-ds", Text: "Chunk secret conflict must not leak"},
			{DatasetID: "", Text: "Unscoped conflict must not leak"},
			{DatasetID: "ds", Text: "Ingest switched from sync to async inbox."},
			{DatasetID: "ds", Text: "Ingest switched from sync to async inbox."},
		},
		SessionGuidance: "## Active session guidance\nKeep focus on recall ACL filtering.",
		CodeFacts: []engine.CodeSearchFact{
			{DatasetID: "secret-ds", Name: "secretFunc", Kind: "func", File: "secret.go", Line: 10},
			{DatasetID: "ds", Name: "recallScopes", Kind: "func", File: "core/gateway/hooks.go", Line: 304,
				Relations: []engine.CodeSearchRelation{{Type: "calls", Target: "EvaluateProjectACL"}}},
		},
		DegradedToChunks: true,
	}

	out := agentRequest(t, s, "/recall", `{
		"project_id":"p","session_id":"sess-1","query":"gateway acl",
		"include_session_context":true,"node_sets":["session_learnings","session_learnings","user_context"]
	}`, 200)

	// Verify SearchRequest fields passed to engine (one per authorized scope: project + common).
	if len(fake.search) != 2 {
		t.Fatalf("search count = %d, want 2", len(fake.search))
	}
	var primaryReq engine.SearchRequest
	for _, sr := range fake.search {
		if len(sr.Scopes) == 1 && sr.Scopes[0].DatasetID == "ds" {
			primaryReq = sr
		}
	}
	if primaryReq.Mode != engine.SearchModeHybrid {
		t.Fatalf("default mode = %q, want hybrid", primaryReq.Mode)
	}
	if !primaryReq.Filters.IncludeSessionContext || primaryReq.Filters.SessionID != "sess-1" ||
		!slices.Equal(primaryReq.Filters.NodeSets, []string{"session_learnings", "user_context"}) {
		t.Fatalf("unexpected search filters: %+v", primaryReq.Filters)
	}

	// Verify response metadata & ACL-filtered hits (current session excluded, prefix neighbor kept, secret excluded).
	if out["mode"] != "hybrid" || out["degraded_to_chunks"] != true {
		t.Fatalf("unexpected mode/degraded fields: %+v", out)
	}
	rawHits := out["hits"].([]any)
	hitIDs := make([]string, 0, len(rawHits))
	for _, item := range rawHits {
		hitIDs = append(hitIDs, item.(map[string]any)["document_id"].(string))
	}
	if !slices.Equal(hitIDs, []string{"prefix-ok", "distilled-1", "common-1"}) {
		t.Fatalf("filtered hit IDs = %v, want [prefix-ok distilled-1 common-1]", hitIDs)
	}

	// Verify graph_context strictly filtered to authorized scopes.
	rawGC, err := json.Marshal(out["graph_context"])
	must(t, err)
	gcJSON := string(rawGC)
	for _, forbidden := range []string{"secret-ds", "SecretEntity", "Secret fact", "Secret conflict", "Chunk secret conflict", "Unscoped conflict", "secretFunc"} {
		if strings.Contains(gcJSON, forbidden) {
			t.Fatalf("graph_context leaked unauthorized %q: %s", forbidden, gcJSON)
		}
	}

	// Verify rendered Markdown context denoising, self-filtering, dedupe, and section ordering.
	contextText := out["context"].(string)
	for _, absent := range []string{
		"SecretEntity", "Secret fact", "Secret conflict", "Chunk secret conflict", "Unscoped conflict", "secretFunc", "classified",
		"OnlyNoiseEntity", "is a web service", "belongs to set core", "Gateway is an application",
		"current question", "Echoed learning",
	} {
		if strings.Contains(contextText, absent) {
			t.Fatalf("context should not contain %q:\n%s", absent, contextText)
		}
	}
	if strings.Count(strings.ToLower(contextText), "## active session guidance") != 1 {
		t.Fatalf("active session guidance heading duplicated:\n%s", contextText)
	}
	if strings.Count(contextText, "Ingest switched from sync to async inbox.") != 1 {
		t.Fatalf("conflict was not deduplicated:\n%s", contextText)
	}
	if strings.Count(contextText, "Gateway enforces project ACL before search.") != 1 {
		t.Fatalf("entity edge duplicated in facts:\n%s", contextText)
	}
	for _, want := range []string{
		"## Conflicts & Updates",
		"## Relevant Entities",
		"- **Gateway** (Component)",
		"[authoritative]",
		"[superseded]",
		"## Related Facts",
		"Daemon holds flock on .service-owner.",
		"## Active Session Guidance",
		"Keep focus on recall ACL filtering.",
		"## Code Graph",
		"`recallScopes` (func, core/gateway/hooks.go:304): calls -> EvaluateProjectACL",
		"## Project Memories",
		"WAL rule",
		"## Project Transcripts",
		"keep prefix neighbor",
		"## Common Reference",
		"Project conventions take precedence over common reference.",
		"General Go tip",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
	// Check section ordering: Conflicts -> Entities -> Facts -> Guidance -> Code -> Project Memories -> Transcripts -> Common.
	orderMarkers := []string{
		"## Conflicts & Updates",
		"## Relevant Entities",
		"## Related Facts",
		"## Active Session Guidance",
		"## Code Graph",
		"## Project Memories",
		"## Project Transcripts",
		"## Common Reference",
	}
	prevIdx := -1
	for _, marker := range orderMarkers {
		idx := strings.Index(contextText, marker)
		if idx <= prevIdx {
			t.Fatalf("section %q out of priority order (idx=%d prev=%d):\n%s", marker, idx, prevIdx, contextText)
		}
		prevIdx = idx
	}

	// Verify recall metrics only counted authorized returned hits (3, not secret/self).
	if countRows(t, store, "recall_hits") != 3 {
		t.Fatalf("recall_hits = %d, want 3", countRows(t, store, "recall_hits"))
	}

	// When session_id is empty, include_session_context must be false even if requested true.
	beforeEmpty := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","query":"no session","include_session_context":true}`, 200)
	for _, sr := range fake.search[beforeEmpty:] {
		if sr.Filters.IncludeSessionContext {
			t.Fatal("IncludeSessionContext should be false when session_id is empty")
		}
	}
}

func TestRecallContextBudgetAndPrecompactKeepsCurrentSession(t *testing.T) {
	longSnippet := strings.Repeat("项目架构规范", 400) // 2400 runes (> 1200 per-passage cap)
	hits := []engine.SearchHit{
		{DatasetID: "ds", DocumentID: "curr", Title: "Current", Snippet: "Session ID: sess-now\nUser: active turn"},
		{DatasetID: "ds", DocumentID: "d1", Title: "Distilled 1", Snippet: longSnippet, Metadata: map[string]any{"lesson_type": "state"}},
		{DatasetID: "ds", DocumentID: "d2", Title: "Distilled 2", Snippet: strings.Repeat("b", 1500), Metadata: map[string]any{"lesson_type": "pitfall"}},
		{DatasetID: "ds", DocumentID: "d3", Title: "Distilled 3", Snippet: strings.Repeat("c", 1500), Metadata: map[string]any{"lesson_type": "convention"}},
		{DatasetID: "ds", DocumentID: "d4", Title: "Distilled 4", Snippet: strings.Repeat("d", 1500), Metadata: map[string]any{"lesson_type": "change"}},
		{DatasetID: "ds", DocumentID: "d5", Title: "Distilled 5", Snippet: strings.Repeat("e", 1500), Metadata: map[string]any{"lesson_type": "constraint"}},
		{DatasetID: "common", DocumentID: "c1", Title: "Common 1", Snippet: strings.Repeat("z", 1500)},
	}
	gc := &engine.GraphSearchContext{
		Conflicts:       []engine.GraphSearchConflict{{DatasetID: "ds", Text: "Critical migration conflict must survive tight budget."}},
		SessionGuidance: "Active guidance priority 3.",
	}

	normal := formatRecallContext(hits, gc, recallFormatOptions{
		CommonDatasetID: "common",
		SessionID:       "sess-now",
		Purpose:         "",
	})
	if utf8.RuneCountInString(normal) > defaultRecallContextMaxChars {
		t.Fatalf("context exceeded budget: %d > %d", utf8.RuneCountInString(normal), defaultRecallContextMaxChars)
	}
	if !utf8.ValidString(normal) {
		t.Fatal("truncated UTF-8 context became invalid")
	}
	if strings.Contains(normal, "active turn") {
		t.Fatalf("normal recall should exclude current-session hit:\n%s", normal)
	}
	if !strings.Contains(normal, "Critical migration conflict") || !strings.Contains(normal, "Active guidance priority 3.") {
		t.Fatalf("high-priority conflict/guidance dropped under budget:\n%s", normal)
	}
	// Each passage must be capped at <= 1200 runes.
	for _, line := range strings.Split(normal, "\n") {
		if strings.HasPrefix(line, "- **Distilled ") && utf8.RuneCountInString(line) > defaultRecallPassageMaxChars {
			t.Fatalf("passage exceeded %d runes: %d", defaultRecallPassageMaxChars, utf8.RuneCountInString(line))
		}
	}

	precompact := formatRecallContext(hits[:2], nil, recallFormatOptions{
		CommonDatasetID: "common",
		SessionID:       "sess-now",
		Purpose:         "precompact",
	})
	if !strings.Contains(precompact, "active turn") {
		t.Fatalf("precompact recall must keep current-session hit:\n%s", precompact)
	}
}

func TestRecallCompactEmptyQueryResolution(t *testing.T) {
	var eng *hybridRecallTestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &hybridRecallTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()

	// Unauthorized agent gets 403 and never stores pending/last prompt.
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-unauth","query":"secret prompt"}`, 403)
	if _, ok := s.prompts.last("agent", "s-unauth"); ok {
		t.Fatal("unauthorized recall stored prompt in memory")
	}

	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Empty query with precompact / compact_restore and no prior prompt returns clean 200 without calling engine.
	for _, purpose := range []string{"precompact", "compact_restore"} {
		out := agentRequest(t, s, "/recall", fmt.Sprintf(`{"project_id":"p","session_id":"s-empty","purpose":%q}`, purpose), 200)
		if out["ok"] != true || out["event_id"] != "" || out["context"] != "" || out["mode"] != "hybrid" || len(out["hits"].([]any)) != 0 {
			t.Fatalf("%s empty recall = %+v", purpose, out)
		}
	}
	if len(fake.search) != 0 {
		t.Fatalf("empty compact recall invoked engine: %+v", fake.search)
	}

	// Record prompt via normal /recall, then consume pending via /ingest; lastPrompt must remain for compact hooks.
	fake.hits = []engine.SearchHit{
		{DatasetID: "ds", DocumentID: "curr-doc", Title: "Current", Snippet: "Session ID: s-compact\nUser: keep for precompact"},
	}
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-compact","turn_id":"t1","query":"refactor auth middleware"}`, 200)
	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-compact","turn_id":"t1","kind":"capture_qa","content":{"question":"","answer":"done"}}`, 202)

	beforeSearches := len(fake.search)
	preOut := agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-compact","purpose":"precompact","limit":5}`, 200)
	if len(fake.search) != beforeSearches+1 || fake.search[beforeSearches].Query != "refactor auth middleware" {
		t.Fatalf("precompact did not resolve lastPrompt: %+v", fake.search[beforeSearches:])
	}
	if !strings.Contains(preOut["context"].(string), "keep for precompact") {
		t.Fatalf("precompact did not retain current-session hit: %+v", preOut)
	}

	restoreOut := agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-compact","purpose":"compact_restore","limit":8}`, 200)
	if len(fake.search) != beforeSearches+2 || fake.search[beforeSearches+1].Query != "refactor auth middleware" {
		t.Fatalf("compact_restore did not resolve lastPrompt: %+v", fake.search[beforeSearches+1:])
	}
	if !strings.Contains(restoreOut["context"].(string), "keep for precompact") {
		t.Fatalf("compact_restore context missing expected hit: %+v", restoreOut)
	}

	// When in-memory prompt is absent, fall back to SessionHistoryReader for authorized session.
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "s-hist", UserID: "u-hist", SortTSDescKey: -10, IsTopLevel: true,
	}))
	eng.mu.Lock()
	eng.history = engine.SessionHistoryResult{
		Label: "fallback label",
		QAs: []engine.SessionQA{
			{ID: "q1", Question: "older topic", Answer: "a1"},
			{ID: "q2", Question: "latest history question", Answer: "a2"},
		},
	}
	eng.mu.Unlock()
	beforeHist := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-hist","purpose":"compact_restore"}`, 200)
	if len(fake.search) != beforeHist+1 || fake.search[beforeHist].Query != "latest history question" {
		t.Fatalf("session history query resolution failed: %+v", fake.search[beforeHist:])
	}
}

func TestIngestPendingPromptPairingAndCaptureFilter(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. UserPromptSubmit (/recall) -> Stop (/ingest with empty question) pairs prompt & prompt_time.
	const promptTime = "2026-11-20T08:30:00Z"
	agentRequest(t, s, "/recall", fmt.Sprintf(
		`{"project_id":"p","session_id":"s-pair","turn_id":"turn-1","prompt_time":%q,"query":"how do we run check.py"}`, promptTime), 200)

	// Mismatched turn_id must not consume turn-1's pending prompt.
	mismatched := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-pair","turn_id":"turn-other","kind":"capture_qa",
		"content":{"question":"","answer":"wrong turn answer"}
	}`, 200)
	if mismatched["accepted"] != false || mismatched["status"] != "skipped_missing_question" {
		t.Fatalf("mismatched turn_id = %+v", mismatched)
	}

	// Matching turn_id recovers question + prompt_time and enqueues 202.
	paired := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-pair","turn_id":"turn-1","message_id":"turn-1","kind":"capture_qa",
		"content":{"question":"","answer":"run python3 scripts/task.py -- python3 scripts/check.py --staged"}
	}`, 202)
	if paired["accepted"] != true || paired["status"] != "pending" {
		t.Fatalf("paired ingest = %+v", paired)
	}
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.ingest) != 1 {
		t.Fatalf("paired item not processed: worked=%v ingests=%d", worked, len(fake.ingest))
	}
	var ingestedContent map[string]any
	must(t, json.Unmarshal(fake.ingest[0].Content.(json.RawMessage), &ingestedContent))
	if ingestedContent["question"] != "how do we run check.py" || ingestedContent["prompt_time"] != promptTime {
		t.Fatalf("recovered content mismatch: %+v", ingestedContent)
	}
	wantDate := domain.LocalCalendarDate(promptTime, time.Local)
	if fake.ingest[0].SourceMeta.ContextDate != wantDate {
		t.Fatalf("context date = %q, want %q", fake.ingest[0].SourceMeta.ContextDate, wantDate)
	}

	// 2. Subsequent empty-question Stop with no pending prompt returns skipped_missing_question cleanly.
	unpaired := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-pair","kind":"capture_qa",
		"content":{"question":"","answer":"orphan answer"}
	}`, 200)
	if unpaired["ok"] != true || unpaired["accepted"] != false || unpaired["status"] != "skipped_missing_question" {
		t.Fatalf("unpaired ingest = %+v", unpaired)
	}
	if countRows(t, store, "capture_inbox") != 1 {
		t.Fatalf("unpaired ingest leaked into capture_inbox: %d", countRows(t, store, "capture_inbox"))
	}

	// 3. Sensitive recovered prompt must still be blocked by filterCapture!
	agentRequest(t, s, "/recall", `{
		"project_id":"p","session_id":"s-secret","turn_id":"turn-sec",
		"query":"please rotate sk-abcdefghijklmnopqrstuvwxyz01234 immediately"
	}`, 200)
	filtered := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-secret","turn_id":"turn-sec","kind":"capture_qa",
		"content":{"question":"","answer":"rotated"}
	}`, 200)
	if filtered["accepted"] != false || filtered["filtered"] != true || filtered["filter_rule"] != "api_token" {
		t.Fatalf("recovered secret prompt bypassed capture filter: %+v", filtered)
	}
	if countRows(t, store, "capture_inbox") != 1 || countRows(t, store, "capture_receipts") != 1 {
		t.Fatal("filtered recovered prompt reached capture_inbox or capture_receipts")
	}

	// 4. Bounded LRU (<= 256) and TTL (<= 2h) on pendingPromptStore.
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ps := &pendingPromptStore{now: func() time.Time { return now }}
	for i := range 270 {
		ps.record("agent", fmt.Sprintf("sess-%03d", i), pendingPrompt{Prompt: fmt.Sprintf("p-%03d", i), DatasetIDs: []string{"ds"}})
	}
	if len(ps.entries) != maxPendingPromptSessions {
		t.Fatalf("pendingPromptStore size = %d, want %d", len(ps.entries), maxPendingPromptSessions)
	}
	if _, ok := ps.last("agent", "sess-000"); ok {
		t.Fatal("oldest session was not evicted by LRU")
	}
	if got, ok := ps.last("agent", "sess-269"); !ok || got.Prompt != "p-269" {
		t.Fatalf("newest session missing: %+v (%v)", got, ok)
	}
	now = now.Add(pendingPromptTTL + time.Minute)
	if _, ok := ps.last("agent", "sess-269"); ok {
		t.Fatal("expired prompt survived TTL")
	}
	if _, ok := ps.popPending("agent", "sess-269", "ds", "", ""); ok {
		t.Fatal("expired pending prompt survived TTL")
	}
}

func TestRecallSessionOwnershipACLAndPromptScopeIsolation(t *testing.T) {
	var eng *hybridRecallTestEngine
	var historyCalls int
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &hybridRecallTestEngine{
			fakeEngine: cfg.Engine.(*fakeEngine),
			history: engine.SessionHistoryResult{
				Label: "secret history",
				QAs:   []engine.SessionQA{{ID: "q1", Question: "secret project B prompt", Answer: "secret answer"}},
			},
		}
		cfg.Engine = &countingSessionHistoryEngine{
			hybridRecallTestEngine: eng,
			onRead:                 func() { historyCalls++ },
		}
	})
	ctx := context.Background()

	// Give agent a PrincipalID to prove unchecked principalID fallback is removed.
	agent, err := store.GetAgent(ctx, "agent")
	must(t, err)
	agent.PrincipalID = "11111111-1111-4111-8111-111111111111"
	must(t, store.UpsertAgent(ctx, agent))

	// Project B is unauthorized for agent; project C is authorized.
	must(t, store.UpsertProject(ctx, domain.Project{ID: "proj-b", Name: "ProjB", DatasetID: "ds-b"}))
	must(t, store.UpsertProject(ctx, domain.Project{ID: "proj-c", Name: "ProjC", DatasetID: "ds-c", AutoCapture: true}))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/projects/proj-c/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"read"}`, 200)

	// Seed catalog_sessions for unauthorized project B and authorized project p.
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds-b", SessionID: "sess-b", UserID: "u-owner", SortTSDescKey: -20, IsTopLevel: true,
	}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "sess-p-cat", UserID: "u-owner", SortTSDescKey: -10, IsTopLevel: true,
	}))
	// Seed session_binding for authorized project p (no catalog_sessions row yet).
	must(t, store.UpsertSessionBinding(ctx, sessionBindingKey("agent", "sess-p-bound"), "p", 100))

	// 1. Forged session_id belonging to unauthorized project B must NOT enable IncludeSessionContext, store prompt, or call SessionHistoryReader.
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-b","query":"probe","include_session_context":true}`, 200)
	if len(fake.search) != 2 {
		t.Fatalf("expected 2 per-scope searches (project+common), got %d", len(fake.search))
	}
	for _, sr := range fake.search {
		if sr.Filters.IncludeSessionContext || sr.Filters.SessionID != "" {
			t.Fatalf("forged project-B session leaked into search filters: %+v", sr.Filters)
		}
	}
	afterForged := len(fake.search)
	forgedCompact := agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-b","purpose":"compact_restore","include_session_context":true}`, 200)
	if len(fake.search) != afterForged || forgedCompact["context"] != "" || len(forgedCompact["hits"].([]any)) != 0 || historyCalls != 0 {
		t.Fatalf("forged project-B compact_restore leaked history or ran search: searches=%d out=%+v historyCalls=%d", len(fake.search), forgedCompact, historyCalls)
	}

	// 2. Unbound/unknown session_id (even with PrincipalID set) must NOT enable IncludeSessionContext or SessionHistoryReader.
	unknownCompact := agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-unknown","purpose":"precompact","include_session_context":true}`, 200)
	if len(fake.search) != afterForged || unknownCompact["context"] != "" || historyCalls != 0 {
		t.Fatalf("unbound session called SessionHistoryReader or search: searches=%d out=%+v historyCalls=%d", len(fake.search), unknownCompact, historyCalls)
	}
	beforeUnknown := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-unknown","query":"probe","include_session_context":true}`, 200)
	for _, sr := range fake.search[beforeUnknown:] {
		if sr.Filters.IncludeSessionContext || sr.Filters.SessionID != "" {
			t.Fatalf("unbound session enabled session context: %+v", sr.Filters)
		}
	}

	// 3. Authorized session via session_bindings allows IncludeSessionContext=true and passes SessionID on primary scope.
	beforeBound := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-p-bound","query":"probe","include_session_context":true}`, 200)
	var sawBoundPrimary bool
	for _, sr := range fake.search[beforeBound:] {
		if len(sr.Scopes) == 1 && sr.Scopes[0].DatasetID == "ds" && sr.Filters.IncludeSessionContext && sr.Filters.SessionID == "sess-p-bound" {
			sawBoundPrimary = true
		}
	}
	if !sawBoundPrimary {
		t.Fatalf("session_bindings authorized session was blocked: %+v", fake.search[beforeBound:])
	}

	// 4. Authorized session via catalog_sessions allows IncludeSessionContext=true AND SessionHistoryReader.
	beforeCat := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-p-cat","purpose":"compact_restore","include_session_context":true}`, 200)
	var sawCatPrimary bool
	for _, sr := range fake.search[beforeCat:] {
		if len(sr.Scopes) == 1 && sr.Scopes[0].DatasetID == "ds" &&
			sr.Filters.IncludeSessionContext && sr.Filters.SessionID == "sess-p-cat" &&
			sr.Query == "secret project B prompt" {
			sawCatPrimary = true
		}
	}
	if historyCalls != 1 || len(fake.search) != beforeCat+2 || !sawCatPrimary {
		t.Fatalf("catalog_sessions authorized compact_restore failed: historyCalls=%d search=%+v", historyCalls, fake.search[beforeCat:])
	}

	// 5. Prompt recorded under project p must NOT leak into compact_restore or /ingest scoped to project proj-c.
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-cross","turn_id":"t-cross","query":"project p only prompt"}`, 200)
	crossCompact := agentRequest(t, s, "/recall", `{"project_id":"proj-c","session_id":"sess-cross","purpose":"compact_restore"}`, 200)
	if crossCompact["context"] != "" || len(crossCompact["hits"].([]any)) != 0 {
		t.Fatalf("project p prompt leaked into proj-c compact_restore: %+v", crossCompact)
	}
	crossIngest := agentRequest(t, s, "/ingest", `{
		"project_id":"proj-c","session_id":"sess-cross","turn_id":"t-cross","kind":"capture_qa",
		"content":{"question":"","answer":"cross project answer"}
	}`, 200)
	if crossIngest["accepted"] != false || crossIngest["status"] != "skipped_missing_question" {
		t.Fatalf("project p prompt leaked into proj-c ingest: %+v", crossIngest)
	}

	// 6. R2-B1 two-step active-session bypass via capture_receipts / capture_inbox (before catalog_sessions exists).
	// Victim has an active session in unauthorized project proj-b (ds-b) recorded in capture_receipts & capture_inbox only.
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "victim-receipt-1", UserID: "victim", DatasetID: "ds-b",
		SessionID: "sess-active-b", MessageID: "vm1", PayloadSHA256: "sha-v1", EntryType: "capture_trace",
	})
	must(t, err)
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: "victim-inbox-1", ProjectID: "proj-b", DatasetID: "ds-b", AgentID: "victim-agent",
		SessionID: "sess-active-b", MessageID: "vm1", CaptureKey: "victim-receipt-1",
		Kind: "capture_trace", PayloadJSON: `{"content":{"origin_function":"Read"}}`,
	})
	must(t, err)
	// Attacker (maintain on p) self-creates session_bindings(["agent", "sess-active-b"] -> p) via /ingest, then calls /recall.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-active-b","message_id":"atk-1","kind":"capture_trace",
		"content":{"origin_function":"Bash"}
	}`, 202)
	beforeAttack := len(fake.search)
	beforeHistory := historyCalls
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-active-b","query":"steal","include_session_context":true}`, 200)
	if len(fake.search) != beforeAttack+2 {
		t.Fatalf("expected 2 per-scope searches, got %d", len(fake.search)-beforeAttack)
	}
	for _, sr := range fake.search[beforeAttack:] {
		if sr.Filters.IncludeSessionContext || sr.Filters.SessionID != "" {
			t.Fatalf("two-step capture_receipts/inbox bypass succeeded: %+v", sr.Filters)
		}
	}
	afterAttack := len(fake.search)
	attackCompact := agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-active-b","purpose":"compact_restore","include_session_context":true}`, 200)
	if len(fake.search) != afterAttack || attackCompact["context"] != "" || historyCalls != beforeHistory {
		t.Fatalf("two-step compact_restore bypass leaked history/prompt: searches=%d out=%+v history=%d", len(fake.search), attackCompact, historyCalls)
	}

	// 7. Cross-agent session_bindings conflict: victim's binding to unauthorized proj-b blocks attacker even with no receipts/catalog rows.
	must(t, store.UpsertSessionBinding(ctx, sessionBindingKey("victim-agent", "sess-victim-bound"), "proj-b", 200))
	must(t, store.UpsertSessionBinding(ctx, sessionBindingKey("agent", "sess-victim-bound"), "p", 201))
	beforeCrossBind := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-victim-bound","query":"steal","include_session_context":true}`, 200)
	if len(fake.search) != beforeCrossBind+2 {
		t.Fatalf("expected 2 per-scope searches, got %d", len(fake.search)-beforeCrossBind)
	}
	for _, sr := range fake.search[beforeCrossBind:] {
		if sr.Filters.IncludeSessionContext || sr.Filters.SessionID != "" {
			t.Fatalf("cross-agent session_bindings bypass succeeded: %+v", sr.Filters)
		}
	}

	// 8. Positive ownership via capture_receipts alone (only authorized dataset ds) enables IncludeSessionContext.
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "auth-receipt-only", UserID: "agent", DatasetID: "ds",
		SessionID: "sess-receipt-only", MessageID: "am1", PayloadSHA256: "sha-a1", EntryType: "capture_trace",
	})
	must(t, err)
	beforeReceiptOnly := len(fake.search)
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"sess-receipt-only","query":"legit","include_session_context":true}`, 200)
	var sawReceiptPrimary bool
	for _, sr := range fake.search[beforeReceiptOnly:] {
		if len(sr.Scopes) == 1 && sr.Scopes[0].DatasetID == "ds" && sr.Filters.IncludeSessionContext && sr.Filters.SessionID == "sess-receipt-only" {
			sawReceiptPrimary = true
		}
	}
	if len(fake.search) != beforeReceiptOnly+2 || !sawReceiptPrimary {
		t.Fatalf("authorized capture_receipts session was blocked: %+v", fake.search[beforeReceiptOnly:])
	}
}

type countingSessionHistoryEngine struct {
	*hybridRecallTestEngine
	onRead func()
}

func (c *countingSessionHistoryEngine) ReadSessionHistory(ctx context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	if c.onRead != nil {
		c.onRead()
	}
	return c.hybridRecallTestEngine.ReadSessionHistory(ctx, req)
}

func TestSessionEndACLFlushDeduplicationAndStopSafety(t *testing.T) {
	var eng *hybridRecallTestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		eng = &hybridRecallTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()

	// Auth & validation checks.
	requireCode(t, request(t, s, "POST", "/session-end", `{"project_id":"p","session_id":"s1"}`, nil, 401), "unauthorized")
	requireCode(t, request(t, s, "POST", "/session-end", `{"project_id":"p","session_id":"  "}`,
		map[string]string{"X-Api-Key": testAgent}, 400), "invalid_session_end")

	// Protected project rejected.
	manage(t, s, "POST", "/api/projects", `{"id":"prot","name":"ml-user-profiles","dataset_name":"ml-user-profiles","dataset_id":"prot-ds"}`, 201)
	manage(t, s, "PUT", "/api/projects/prot/acl/agent", `{"access":"maintain","capture":true}`, 200)
	if got := agentRequest(t, s, "/session-end", `{"project_id":"prot","session_id":"s1"}`, 403); got["error"] != "project_protected" {
		t.Fatalf("protected session-end = %+v", got)
	}

	// Read-only ACL rejected.
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"read"}`, 200)
	if got := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s1"}`, 403); got["error"] != "dataset_write_denied" {
		t.Fatalf("read-only session-end = %+v", got)
	}

	// Paused auto-capture rejected unless manual.
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/projects/p/auto-capture", `{"auto_capture":false}`, 200)
	if got := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-empty"}`, 403); got["error"] != "capture_paused" {
		t.Fatalf("paused session-end = %+v", got)
	}
	manualEnd := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-empty","mode":"manual"}`, 202)
	if manualEnd["ok"] != true || manualEnd["queued"] != false || manualEnd["session_id"] != "s-empty" {
		t.Fatalf("empty manual session-end = %+v", manualEnd)
	}
	manage(t, s, "PUT", "/api/projects/p/auto-capture", `{"auto_capture":true}`, 200)

	// Session with pending prompt + captured trace: /session-end clears prompt cache, deduplicates flush, and triggers Extract once.
	agentRequest(t, s, "/recall", `{"project_id":"p","session_id":"s-flush","query":"trace session prompt"}`, 200)
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-flush","message_id":"trace-1","kind":"capture_trace",
		"content":{"origin_function":"Read","method_params":{"path":"a.go"}}
	}`, 202)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.extract) != 0 {
		t.Fatalf("trace setup failed: worked=%v extracts=%d", worked, len(fake.extract))
	}

	firstEnd := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-flush","host":"codex"}`, 202)
	if firstEnd["ok"] != true || firstEnd["queued"] != true || firstEnd["project_id"] != "p" ||
		firstEnd["dataset_id"] != "ds" || firstEnd["session_id"] != "s-flush" {
		t.Fatalf("first session-end = %+v", firstEnd)
	}
	if _, ok := s.prompts.last("agent", "s-flush"); ok {
		t.Fatal("session-end did not clear in-memory prompt state")
	}
	// Duplicate call while flush is queued must deduplicate.
	dupEnd := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-flush","host":"codex"}`, 202)
	if dupEnd["queued"] != false {
		t.Fatalf("duplicate session-end should not enqueue again: %+v", dupEnd)
	}

	// Start InboxWorker and Stop cleanly; queued flush drains and calls Extract strictly once.
	s.Inbox().Start(ctx)
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stopCtx))
	if len(fake.extract) != 1 || !slices.Equal(fake.extract[0].DocIDs, []string{"s-flush"}) || fake.extract[0].Scope.DatasetID != "ds" {
		t.Fatalf("session-end extract calls = %+v", fake.extract)
	}

	// Verify Stop cancels an in-flight blocked session-end flush cleanly with zero goroutine leaks.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-cancel","message_id":"trace-cancel","kind":"capture_trace",
		"content":{"origin_function":"Bash"}
	}`, 202)
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected trace-cancel to be processed")
	}
	extractStarted := make(chan struct{})
	extractCanceled := make(chan struct{})
	eng.mu.Lock()
	eng.extractHook = func(ectx context.Context, _ engine.ExtractRequest) (engine.ExtractResult, error) {
		close(extractStarted)
		<-ectx.Done()
		close(extractCanceled)
		return engine.ExtractResult{}, ectx.Err()
	}
	eng.mu.Unlock()

	s.Inbox().Start(ctx)
	cancelEnd := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-cancel"}`, 202)
	if cancelEnd["queued"] != true {
		t.Fatalf("expected s-cancel queued=true, got %+v", cancelEnd)
	}
	select {
	case <-extractStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session-end flush to start")
	}

	shortStop, shortCancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer shortCancel()
	if err := s.Inbox().Stop(shortStop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected Stop deadline exceeded on blocked extract, got %v", err)
	}
	select {
	case <-extractCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked session-end extract did not honor worker context cancellation")
	}
	// Ensure worker goroutine fully exits after cancellation.
	s.Inbox().mu.Lock()
	doneCh := s.Inbox().run.done
	s.Inbox().mu.Unlock()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("inbox worker did not exit after cancellation")
	}
	_ = store
}

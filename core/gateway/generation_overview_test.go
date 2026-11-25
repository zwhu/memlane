package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func TestGenerationValidationAndActivation(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	secret := "privatecredentialabc123"
	profile := fmt.Sprintf(`{"mode":"provider","provider":"openai","model":"model","endpoint":"https://provider.invalid/v1","api_key":%q,"temperature":0}`, secret)
	save := manage(t, s, "PUT", "/api/generation/profiles/p1", profile, 200)
	var encoded map[string]json.RawMessage
	must(t, json.Unmarshal(save.Body.Bytes(), &encoded))
	if string(encoded["temperature"]) != "0" || encoded["max_output_tokens"] != nil ||
		encoded["max_concurrent_requests"] != nil || string(encoded["has_credential"]) != "true" {
		t.Fatal(save.Body)
	}
	saved := decodeResponse[generationView](t, save)
	credential := filepath.Join(filepath.Dir(store.Path()), "generation-credentials", saved.CredentialRef+"-"+saved.CredentialVersion)
	info, err := os.Stat(credential)
	must(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	key, err := os.ReadFile(credential)
	must(t, err)
	if string(key) != secret {
		t.Fatal("credential differs")
	}
	initial, err := store.GetGenerationState(ctx)
	must(t, err)
	if initial.ActiveProfileID != "" || initial.ActiveRevision != 0 || initial.Revision != 0 {
		t.Fatal(initial)
	}

	candidate := `{"profile_id":"p1"}`
	requireCode(t, manage(t, s, "POST", "/api/generation/activate", candidate, 409), "validation_required")
	valid := manage(t, s, "POST", "/v1/generation/validations", candidate, 200)
	if got := decodeResponse[map[string]any](t, valid)["status"]; got != "passed" {
		t.Fatal(got)
	}
	if len(fake.probe) != 1 || fake.probe[0].Environment["LLM_API_KEY"] != secret || fake.probe[0].Environment["LLM_TEMPERATURE"] != "0" ||
		fake.probe[0].Environment["LLM_MAX_COMPLETION_TOKENS"] != "" {
		t.Fatal("probe did not receive the selected options")
	}
	var errorCode, payload string
	must(t, store.DB().QueryRow("SELECT error_code,payload_json FROM generation_validations").Scan(&errorCode, &payload))
	if strings.Contains(errorCode+payload, secret) {
		t.Fatal("validation leaked a key")
	}
	fake.applyErr = errors.New("upstream rejected " + secret)
	failed := manage(t, s, "PUT", "/v1/generation/next", candidate, 502)
	state, err := store.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != "" {
		t.Fatal(state)
	}
	fake.applyErr = nil
	applied := manage(t, s, "POST", "/api/generation/activate", candidate, 200)
	if got := decodeResponse[map[string]any](t, applied)["active_committed"]; got != true {
		t.Fatal(got)
	}
	state, err = store.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != "p1" || state.ActiveRevision != 1 || state.Revision != 1 {
		t.Fatal(state)
	}
	config := fake.apply[1].GenerationConfig
	if config["temperature"] != float64(0) || config["max_output_tokens"] != nil || config["api_key"] != secret {
		t.Fatal("apply request fields differ")
	}

	remove := manage(t, s, "DELETE", "/api/generation/profiles/p1", "", 409)
	requireCode(t, remove, "active_profile_cannot_be_removed")
	unset := strings.Replace(profile, `,"temperature":0`, "", 1)
	second := manage(t, s, "PUT", "/api/generation/profiles/p1", unset, 200)
	state, err = store.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveRevision != 1 || state.Revision != 1 || strings.Contains(second.Body.String(), "temperature") {
		t.Fatal("saving selected a profile or added unset options")
	}
	requireCode(t, manage(t, s, "POST", "/api/generation/activate", candidate, 409), "validation_required")
	fake.modelDigest = secret
	get := manage(t, s, "GET", "/v1/generation", "", 200)
	for _, body := range []string{save.Body.String(), valid.Body.String(), failed.Body.String(), applied.Body.String(), get.Body.String(), second.Body.String()} {
		if strings.Contains(body, secret) || strings.Contains(body, `"api_key"`) {
			t.Fatal("generation response leaked a key")
		}
	}
	handoffs, err := store.ListGenerationHandoffs(ctx)
	must(t, err)
	if len(handoffs) != 2 {
		t.Fatal(handoffs)
	}
	states := map[string]bool{}
	for _, h := range handoffs {
		states[h.Status] = h.ActiveCommitted
	}
	if !states["applied"] || states["failed"] || strings.Contains(fmt.Sprint(handoffs), secret) {
		t.Fatal("invalid handoff receipts")
	}

	manage(t, s, "PUT", "/api/generation/profiles/p2", `{"mode":"agent_bridge","bridge_kind":"codex","model":"bridge-model"}`, 200)
	manage(t, s, "POST", "/v1/generation/profiles/p2/delete", "", 204)
	manage(t, s, "POST", "/api/generation/validate", `{"profile_id":"p2"}`, 403)
}

func TestGenerationModelStatusOutputLanguage(t *testing.T) {
	const secret, bridgeToken = "abc123", "def456"
	bridgeTokenFile := filepath.Join(t.TempDir(), "bridge-token")
	must(t, os.WriteFile(bridgeTokenFile, []byte(bridgeToken), 0o600))
	s, _, fake := testServer(t, func(c *Config) {
		c.BridgeEndpoint = "http://127.0.0.1:12345"
		c.BridgeTokenFile = bridgeTokenFile
	})
	profile := fmt.Sprintf(`{"mode":"provider","provider":"openai","model":"model","endpoint":"https://provider.invalid/v1","api_key":%q}`, secret)
	manage(t, s, "PUT", "/api/generation/profiles/p1", profile, 200)
	for _, tc := range []struct{ name, language, want string }{
		{"valid tag", "zh-CN", "zh-CN"},
		{"invalid tag", "zh_CN", ""},
		{"tag containing a credential", "zh-CN-" + secret, ""},
		{"tag containing the bridge token", "zh-CN-" + bridgeToken, ""},
		{"credential-shaped tag", "sk-abcdef12-xyz", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake.outputLanguage = tc.language
			res := manage(t, s, "GET", "/api/generation", "", http.StatusOK)
			got := decodeResponse[struct {
				ModelStatus engine.ModelStatusResult `json:"model_status"`
			}](t, res).ModelStatus
			body := res.Body.String()
			if !got.OK || got.OutputLanguage != tc.want || strings.Contains(body, secret) || strings.Contains(body, bridgeToken) {
				t.Fatalf("unexpected model status for %s", tc.name)
			}
		})
	}
}

func TestOverviewSnapshotParityAndLatency(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	now := time.Now()
	hash := domain.KnowledgeReadScopeHash(readIdentity, "p", "ds")
	must(t, store.UpsertReadScope(ctx, domain.ReadScope{ScopeHash: hash, ProjectID: "p", DatasetID: "ds", Initialized: true,
		ReadHorizonAtNS: domain.FormatUTCNS(now.Add(-time.Hour)), StateRev: 7}))
	day := now.Format(time.DateOnly)
	must(t, store.WithTx(ctx, func(tx *sql.Tx) error {
		for i := 1; i <= 800; i++ {
			kind, isKnowledge := "knowledge", true
			at := now
			if i > 600 {
				kind, isKnowledge = "raw_doc", false
			}
			if i <= 500 {
				at = now.Add(-2 * time.Hour)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_docs(dataset_id,doc_id,cognee_rowid,kind,is_knowledge,doc_label,
				created_at_ns,created_at_raw,effective_date,category,day_desc_key,sort_ts_desc_key)
				VALUES('ds',?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("doc-%04d", i), i, kind, isKnowledge, "snap match",
				domain.FormatUTCNS(at), domain.FormatUTCNS(at), day, "tips", -i, -i)
			if err != nil {
				return err
			}
		}
		for i := 1; i <= 240; i++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_sessions(dataset_id,session_id,is_top_level) VALUES('ds',?,1)`, fmt.Sprintf("session-%d", i)); err != nil {
				return err
			}
		}
		return nil
	}))
	seedDoc(t, store, "common", "general01", "common", "preference", now, -1000)
	seedDoc(t, store, "outside", "foreign", "knowledge", "tips", now, -2000)
	must(t, store.SetReadItem(ctx, hash, "doc-0510", true))
	must(t, store.UpsertCatalogSyncState(ctx, domain.CatalogSyncState{DatasetID: "ds", CatalogVersion: "sync-version"}))
	for i := range 10 {
		_, err := store.RecordRecallEvent(ctx, domain.RecallEventInput{AgentID: "agent", EventID: fmt.Sprint("recall-", i), Channel: "automatic",
			Hits: []domain.RecallHit{{DatasetID: "ds", DocumentID: "doc-0600"}}})
		must(t, err)
		must(t, store.RecordRecallQueryStat(ctx, "automatic", 1, ""))
	}
	queued, err := store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{ProjectID: "p", DatasetID: "ds", AgentID: "agent",
		SessionID: "s", MessageID: "m", Kind: "capture_qa", PayloadJSON: `{}`})
	must(t, err)
	_, err = store.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, store.MarkInboxFailed(ctx, queued.ID, "pipeline rejected"))
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{ProjectID: "p", DatasetID: "ds", AgentID: "agent",
		SessionID: "s", MessageID: "m2", Kind: "capture_qa", PayloadJSON: `{}`})
	must(t, err)
	manage(t, s, "GET", "/api/search?q=snap&limit=2", "", 200)
	before := overviewTableCounts(t, store)
	var changesBefore, changesAfter int
	must(t, store.DB().QueryRow("SELECT total_changes()").Scan(&changesBefore))
	start := time.Now()
	response := manage(t, s, "GET", "/api/overview/bootstrap?days=10&recent_limit=4&recall_limit=3&search_limit=1", "", 200)
	d := time.Since(start)
	if d >= 150*time.Millisecond {
		t.Fatalf("bootstrap exceeded 150ms for seeded catalog: %s", d)
	}
	t.Logf("bootstrap for 802 documents and 240 sessions took %s", d)
	out := decodeResponse[overviewSnapshot](t, response)
	if out.LocalTZName == "" || out.TotalKnowledge != 601 || out.TotalRaw != 440 || out.TotalUnread != 99 ||
		len(out.DayBuckets) != 1 || out.DayBuckets[0] != (dayBucket{day, 601}) || len(out.Projects) != 1 ||
		out.Common == nil || out.Common.KnowledgeCount != 1 || len(out.RecentItems) != 4 || out.RecentItems[1].DocID != "doc-0600" ||
		out.RecentItems[1].RecallCount != 10 || !out.RecentItems[1].IsUnread {
		t.Fatal(out)
	}
	p := out.Projects[0]
	if p.UnreadStateRev != 8 || p.UnreadCount != 99 || p.CatalogVersion != "sync-version" || p.InboxPending != 1 || p.InboxFailed != 1 ||
		p.SessionCount != 240 || p.LastError != "pipeline rejected" || out.Inbox.Pending != 1 || out.Inbox.Failed != 1 {
		t.Fatal(p, out.Inbox)
	}
	if len(out.RecallEvents) != 3 || !out.RecallEventsMore || len(out.RecallStats) != 1 || out.RecallStats[0].TotalQueries != 10 ||
		len(out.Searches) != 1 || out.Searches[0].Query != "snap" {
		t.Fatal(out)
	}
	page := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/v1/overview/day?day="+day+"&limit=3", "", 200))
	if len(page.Items) != 3 || !page.HasMore {
		t.Fatal(page)
	}
	manage(t, s, "GET", "/api/overview/day?day=bad", "", 400)
	must(t, store.DB().QueryRow("SELECT total_changes()").Scan(&changesAfter))
	if after := overviewTableCounts(t, store); after != before || changesAfter != changesBefore {
		t.Fatal("GET changed the domain store", before, after)
	}
}

func overviewTableCounts(t *testing.T, store *domain.Store) [5]int {
	t.Helper()
	return [5]int{countRows(t, store, "ui_read_scopes"), countRows(t, store, "ui_read_items"),
		countRows(t, store, "capture_inbox"), countRows(t, store, "recall_events"), countRows(t, store, "recall_query_stats")}
}

func TestOverviewImportedDailyShadow(t *testing.T) {
	daily := filepath.Join("..", "..", ".local", "daily")
	if info, err := os.Stat(daily); errors.Is(err, os.ErrNotExist) {
		t.Skip("legacy daily store is absent")
	} else if err != nil || !info.IsDir() {
		t.Fatalf("cannot inspect daily store directory: %v", err)
	}
	temp := t.TempDir()
	shadow := filepath.Join(temp, "legacy")
	must(t, os.Mkdir(shadow, 0o700))
	names := strings.Fields("project-registry.json generation.json generation-handoff-runtime.json user-profiles.json " +
		"tool-graph-control-jobs.json tool-graph-service-jobs.json tool-graph-trace-watermarks.json")
	for _, db := range strings.Fields("registry-sessions.sqlite3 lesson-provenance.sqlite3 knowledge-recall.sqlite3 " +
		"ui-reading-state.sqlite3 capture-receipts.sqlite3 session-drift.sqlite3") {
		names = append(names, db, db+"-wal", db+"-shm")
	}
	for _, name := range names {
		copyDailyFile(t, filepath.Join(daily, name), filepath.Join(shadow, name))
	}
	path := filepath.Join(temp, "shadow.sqlite3")
	report, err := domain.ImportLegacyDailyStore(shadow, path)
	must(t, err)
	if !report.InvariantsVerified {
		t.Fatal("shadow migration did not verify its invariants")
	}
	store, err := domain.OpenStore(path)
	must(t, err)
	t.Cleanup(func() { must(t, store.Close()) })
	s, err := NewServer(Config{Store: store, Engine: newFakeEngine(), ManagementToken: testManagement})
	must(t, err)
	before := overviewTableCounts(t, store)
	projects, err := store.ListProjects(context.Background())
	must(t, err)
	out := decodeResponse[overviewSnapshot](t, manage(t, s, "GET", "/api/overview/bootstrap?recent_limit=5", "", 200))
	if len(out.Projects) != len(projects) || overviewTableCounts(t, store) != before {
		t.Fatal("shadow overview differs from the imported registry")
	}
}

func copyDailyFile(t *testing.T, source, dest string) {
	t.Helper()
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	must(t, err)
	if !info.Mode().IsRegular() {
		t.Fatalf("daily source %s is not a regular file", filepath.Base(source))
	}
	in, err := os.Open(source)
	must(t, err)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		must(t, errors.Join(err, in.Close()))
	}
	n, copyErr := io.Copy(out, in)
	after, statErr := in.Stat()
	must(t, errors.Join(copyErr, statErr, in.Close(), out.Close()))
	if n != info.Size() || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		t.Fatalf("daily source %s changed during read-only copy", filepath.Base(source))
	}
}

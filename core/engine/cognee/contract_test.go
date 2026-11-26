package cognee

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway"
)

type contractProjectorAdapter struct {
	*Projector
}

func (a contractProjectorAdapter) ReadLocalBody(ctx context.Context, ref engine.LocalBodyRef) (string, error) {
	res, err := a.bodyReader.ReadBody(ctx, ref)
	return res.Body, err
}

type noopContractEngine struct{}

func (noopContractEngine) Health(context.Context) (engine.HealthStatus, error) {
	return engine.HealthStatus{OK: true, Engine: "cognee", Version: "1", Mode: engine.HealthModeReady}, nil
}
func (noopContractEngine) Ingest(context.Context, engine.IngestRequest) (engine.IngestResult, error) {
	return engine.IngestResult{OK: true}, nil
}
func (noopContractEngine) Extract(context.Context, engine.ExtractRequest) (engine.ExtractResult, error) {
	return engine.ExtractResult{OK: true}, nil
}
func (noopContractEngine) Search(context.Context, engine.SearchRequest) (engine.SearchResult, error) {
	return engine.SearchResult{OK: true}, nil
}
func (noopContractEngine) ListItems(context.Context, engine.ListRequest) (engine.ListResult, error) {
	return engine.ListResult{OK: true}, nil
}
func (noopContractEngine) GetItem(context.Context, engine.Scope, string) (engine.Item, error) {
	return engine.Item{}, engine.ErrNotFound
}
func (noopContractEngine) DeleteItem(context.Context, engine.Scope, string) error { return nil }
func (noopContractEngine) Graph(context.Context, engine.GraphRequest) (engine.GraphPayload, error) {
	return engine.GraphPayload{OK: true}, nil
}

func TestContractCleanSessionLabel(t *testing.T) {
	longText := strings.Repeat("甲", 140)
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "USER_REQUEST with ADDITIONAL_METADATA",
			raw:  "<USER_REQUEST>\n/swarm 修复问题\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\n{\"workspace\":\"/synthetic/demo\"}\n</ADDITIONAL_METADATA>",
			want: "/swarm 修复问题",
		},
		{
			name: "USER_SETTINGS_CHANGE prefix stripped",
			raw:  "<USER_SETTINGS_CHANGE>\n{\"theme\":\"dark\"}\n</USER_SETTINGS_CHANGE>\n真实用户提问",
			want: "真实用户提问",
		},
		{
			name: "unclosed ADDITIONAL_METADATA tail stripped",
			raw:  "<USER_REQUEST>\n排查重试冲突\n<ADDITIONAL_METADATA>\n{\"truncated\":true",
			want: "排查重试冲突",
		},
		{
			name: "unclosed USER_SETTINGS_CHANGE tail stripped",
			raw:  "检查图谱同步\n<USER_SETTINGS_CHANGE>\n{\"model\":\"gpt\"",
			want: "检查图谱同步",
		},
		{
			name: "metadata-only payload becomes empty",
			raw:  "<ADDITIONAL_METADATA>\nonly metadata\n</ADDITIONAL_METADATA>",
			want: "",
		},
		{
			name: "truncates cleanly at 120 runes",
			raw:  "<USER_REQUEST>\n" + longText + "\n</USER_REQUEST>",
			want: strings.Repeat("甲", 120),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanSessionLabel(tc.raw); got != tc.want {
				t.Fatalf("cleanSessionLabel(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestContractProjectorCacheHydrationSubagentFoldingAndHistory(t *testing.T) {
	ctx := context.Background()
	storeRoot := t.TempDir()
	projector := NewProjector(storeRoot, time.UTC)
	if err := os.MkdirAll(filepath.Dir(projector.database("cognee_db")), 0o700); err != nil {
		t.Fatalf("mkdir cognee databases: %v", err)
	}
	store := openProjectionStore(t)

	const (
		dsID         = "11111111-2222-4333-8444-555555555555"
		userID       = "aaaa1111-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		parentSID    = "antigravity-parent-001"
		verifierSID  = "antigravity-worker-verifier"
		monitorSID   = "antigravity-worker-monitor"
		orphanSID    = "antigravity-worker-orphan"
		missingParID = "antigravity-parent-missing"
		mgmtToken    = "cognee-contract-management-token-2026"
	)

	if err := store.UpsertProject(ctx, domain.Project{
		ID:          "proj-cognee",
		Name:        "memlane",
		DatasetID:   dsID,
		DatasetName: "memlane",
		AutoCapture: true,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Seed real cognee_db with datasets, data, and session_records.
	cogneeDB := openProjectionTestDB(t, projector.database("cognee_db"), `
		CREATE TABLE datasets(id TEXT, name TEXT);
		CREATE TABLE data(id TEXT, dataset_id TEXT, name TEXT, system_metadata TEXT, external_metadata TEXT, node_set TEXT,
			pipeline_status TEXT, created_at DATETIME, updated_at DATETIME);
		CREATE TABLE session_records(dataset_id TEXT, session_id TEXT, user_id TEXT, status TEXT, started_at DATETIME,
			last_activity_at DATETIME, parent_session_id TEXT);
	`)
	execProjectionTestSQL(t, cogneeDB, `INSERT INTO datasets VALUES(?, ?)`, uuidHex(dsID), "memlane")
	execProjectionTestSQL(t, cogneeDB, `INSERT INTO session_records VALUES
		(?, ?, ?, 'active', '2026-11-26 10:00:00', '2026-11-26 10:10:00', ''),
		(?, ?, ?, 'active', '2026-11-26 10:01:00', '2026-11-26 10:08:00', ''),
		(?, ?, ?, 'active', '2026-11-26 10:02:00', '2026-11-26 10:09:00', ''),
		(?, ?, ?, 'active', '2026-11-26 10:03:00', '2026-11-26 10:04:00', '')`,
		uuidHex(dsID), parentSID, uuidHex(userID),
		uuidHex(dsID), verifierSID, uuidHex(userID),
		uuidHex(dsID), monitorSID, uuidHex(userID),
		uuidHex(dsID), orphanSID, uuidHex(userID),
	)

	// Record real session_relations in Memlane store (2 attached Swarm workers + 1 orphan worker).
	for _, rel := range []domain.SessionRelation{
		{DatasetID: dsID, SessionID: verifierSID, ParentSessionID: parentSID, SubagentRole: "verifier"},
		{DatasetID: dsID, SessionID: monitorSID, ParentSessionID: parentSID, SubagentRole: "monitor"},
		{DatasetID: dsID, SessionID: orphanSID, ParentSessionID: missingParID, SubagentRole: "explorer"},
	} {
		if err := store.UpsertSessionRelation(ctx, rel); err != nil {
			t.Fatalf("UpsertSessionRelation %s: %v", rel.SessionID, err)
		}
	}

	// Seed real cache.db with wrapped XML question on parent and tool traces on subagents.
	cacheDB, err := domain.OpenPrivateSQLite(filepath.Join(storeRoot, "system", "databases", "cache.db"))
	if err != nil {
		t.Fatalf("open cache.db: %v", err)
	}
	t.Cleanup(func() { _ = cacheDB.Close() })

	execProjectionTestSQL(t, cacheDB, `
		CREATE TABLE cache_qa_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, qa_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		CREATE TABLE cache_trace_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
	`)
	parentPayload, _ := json.Marshal(map[string]any{
		"question": "<USER_REQUEST>\n/swarm 修复问题\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\n{\"theme\":\"dark\"}\n</USER_SETTINGS_CHANGE>\n<ADDITIONAL_METADATA>\n{\"truncated\":true",
		"answer":   "已完成修复并验证通过。",
	})
	execProjectionTestSQL(t, cacheDB,
		`INSERT INTO cache_qa_entries VALUES (1, ?, ?, 'qa-parent-1', ?, '2026-11-26 10:00:05', '2099-01-01 00:00:00')`,
		userID, parentSID, string(parentPayload),
	)
	verifierQA, _ := json.Marshal(map[string]any{
		"question": "<USER_REQUEST>\n验证子会话回放\n</USER_REQUEST>",
		"answer":   "5 条工具轨迹已核对。",
	})
	execProjectionTestSQL(t, cacheDB,
		`INSERT INTO cache_qa_entries VALUES (2, ?, ?, 'qa-verifier-1', ?, '2026-11-26 10:01:05', '2099-01-01 00:00:00')`,
		userID, verifierSID, string(verifierQA),
	)
	seq := 1
	insertTraces := func(sid string, count int, tool string) {
		for i := 1; i <= count; i++ {
			payload, _ := json.Marshal(map[string]any{
				"trace_id":            sid + "-t" + string(rune('0'+i)),
				"origin_function":     tool,
				"status":              "success",
				"method_params":       map[string]any{"step": i},
				"method_return_value": "ok",
			})
			execProjectionTestSQL(t, cacheDB,
				`INSERT INTO cache_trace_entries VALUES (?, ?, ?, ?, '2026-11-26 10:02:00', '2099-01-01 00:00:00')`,
				seq, userID, sid, string(payload),
			)
			seq++
		}
	}
	insertTraces(verifierSID, 5, "run_command")
	insertTraces(monitorSID, 3, "read_file")
	insertTraces(orphanSID, 1, "search_web")

	// 1. Real Projector.SyncCatalog writes folded catalog_sessions from cognee_db + session_relations.
	stats, err := projector.SyncCatalog(ctx, store)
	if err != nil {
		t.Fatalf("SyncCatalog: %v", err)
	}
	if stats.SessionsProjected != 4 {
		t.Fatalf("SessionsProjected = %d, want 4", stats.SessionsProjected)
	}
	projected, err := store.ListCatalogSessions(ctx, dsID, 20)
	if err != nil {
		t.Fatalf("ListCatalogSessions: %v", err)
	}
	bySession := map[string]domain.CatalogSession{}
	for _, s := range projected {
		bySession[s.SessionID] = s
	}
	if p := bySession[parentSID]; !p.IsTopLevel || p.ChildCount != 2 || p.NestingDepth != 0 {
		t.Fatalf("unexpected parent projection: %+v", p)
	}
	if v := bySession[verifierSID]; v.IsTopLevel || v.ParentSessionID != parentSID || v.RootSessionID != parentSID || v.SubagentRole != "verifier" || v.NestingDepth != 1 {
		t.Fatalf("unexpected verifier projection: %+v", v)
	}
	if m := bySession[monitorSID]; m.IsTopLevel || m.ParentSessionID != parentSID || m.RootSessionID != parentSID || m.SubagentRole != "monitor" || m.NestingDepth != 1 {
		t.Fatalf("unexpected monitor projection: %+v", m)
	}
	if o := bySession[orphanSID]; !o.IsTopLevel || o.ParentSessionID != missingParID || o.SubagentRole != "explorer" || o.NestingDepth != 1 {
		t.Fatalf("unexpected orphan projection: %+v", o)
	}

	// 2. Real Projector.HydrateSessionsFromCache strips XML tags and counts QAs & tool traces.
	hydrated, err := projector.HydrateSessionsFromCache(ctx, []SessionRef{
		{UserID: userID, SessionID: parentSID},
		{UserID: userID, SessionID: verifierSID},
		{UserID: userID, SessionID: monitorSID},
		{UserID: userID, SessionID: orphanSID},
	})
	if err != nil {
		t.Fatalf("HydrateSessionsFromCache: %v", err)
	}
	if got := hydrated[SessionRef{UserID: userID, SessionID: parentSID}]; got.Label != "/swarm 修复问题" || got.MsgCount != 1 || got.ToolCalls != 0 {
		t.Fatalf("unexpected hydrated parent: %+v", got)
	}
	if got := hydrated[SessionRef{UserID: userID, SessionID: verifierSID}]; got.Label != "验证子会话回放" || got.MsgCount != 1 || got.ToolCalls != 5 {
		t.Fatalf("unexpected hydrated verifier: %+v", got)
	}
	if got := hydrated[SessionRef{UserID: userID, SessionID: monitorSID}]; got.MsgCount != 0 || got.ToolCalls != 3 {
		t.Fatalf("unexpected hydrated monitor: %+v", got)
	}
	if got := hydrated[SessionRef{UserID: userID, SessionID: orphanSID}]; got.ToolCalls != 1 {
		t.Fatalf("unexpected hydrated orphan: %+v", got)
	}

	// 3. Real Projector.ReadSessionHistory reads both parent and subagent histories cleanly.
	parentHist, err := projector.ReadSessionHistory(ctx, engine.SessionHistoryRequest{UserID: userID, SessionID: parentSID, Limit: 10})
	if err != nil {
		t.Fatalf("ReadSessionHistory(parent): %v", err)
	}
	if parentHist.Label != "/swarm 修复问题" || parentHist.MsgCount != 1 || len(parentHist.QAs) != 1 {
		t.Fatalf("unexpected parent history: %+v", parentHist)
	}
	verifierHist, err := projector.ReadSessionHistory(ctx, engine.SessionHistoryRequest{UserID: userID, SessionID: verifierSID, Limit: 10})
	if err != nil {
		t.Fatalf("ReadSessionHistory(verifier): %v", err)
	}
	if verifierHist.Label != "验证子会话回放" || verifierHist.ToolCalls != 5 || len(verifierHist.Traces) != 5 || verifierHist.Traces[0].Name != "run_command" {
		t.Fatalf("unexpected verifier subagent history: %+v", verifierHist)
	}

	// 4. End-to-end real Projector + real gateway.NewServer GET /api/raw-records projects
	// top-level folding, clean parent label, child_count, and children_preview.
	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          noopContractEngine{},
		Projector:       contractProjectorAdapter{Projector: projector},
		ManagementToken: mgmtToken,
	})
	if err != nil {
		t.Fatalf("gateway.NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/projects/proj-cognee/raw-records", nil)
	req.Header.Set("Authorization", "Bearer "+mgmtToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects/proj-cognee/raw-records = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			SessionID       string `json:"session_id"`
			Label           string `json:"label"`
			IsTopLevel      bool   `json:"is_top_level"`
			IsSubagent      bool   `json:"is_subagent"`
			SubagentRole    string `json:"subagent_role"`
			ChildCount      int    `json:"child_count"`
			ChildrenPreview []struct {
				SessionID    string `json:"session_id"`
				SubagentRole string `json:"subagent_role"`
				ToolCalls    int    `json:"tool_calls"`
			} `json:"children_preview"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode raw-records: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 top-level sessions (parent + orphan), got %d: %s", len(payload.Items), rec.Body.String())
	}
	var sawParent, sawOrphan bool
	for _, item := range payload.Items {
		switch item.SessionID {
		case parentSID:
			sawParent = true
			if item.Label != "/swarm 修复问题" || item.ChildCount != 2 || len(item.ChildrenPreview) != 2 {
				t.Fatalf("unexpected parent wire item: %+v", item)
			}
			childTools := map[string]int{}
			for _, ch := range item.ChildrenPreview {
				childTools[ch.SubagentRole] = ch.ToolCalls
			}
			if childTools["verifier"] != 5 || childTools["monitor"] != 3 {
				t.Fatalf("unexpected children_preview roles/traces: %+v", item.ChildrenPreview)
			}
		case orphanSID:
			sawOrphan = true
			if !item.IsSubagent || item.SubagentRole != "explorer" {
				t.Fatalf("unexpected orphan wire item: %+v", item)
			}
		default:
			t.Fatalf("nested worker %s leaked into top-level raw-records", item.SessionID)
		}
	}
	if !sawParent || !sawOrphan {
		t.Fatalf("missing parent=%v or orphan=%v in raw-records", sawParent, sawOrphan)
	}
}

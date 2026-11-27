package cognee

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

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

	const (
		dsID        = "11111111-2222-4333-8444-555555555555"
		userID      = "aaaa1111-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		parentSID   = "antigravity-parent-001"
		verifierSID = "antigravity-worker-verifier"
		monitorSID  = "antigravity-worker-monitor"
		orphanSID   = "antigravity-worker-orphan"
	)

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

	// Seed real cache.db with wrapped XML question on parent and tool traces on subagents.
	cacheDB := openProjectionTestDB(t, filepath.Join(storeRoot, "system", "databases", "cache.db"), `
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

	// 1. Real Projector.SnapshotCatalog returns all 4 sessions without importing core/domain.
	snap, err := projector.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{{DatasetID: dsID}}, engine.CatalogSnapshotOptions{})
	if err != nil {
		t.Fatalf("SnapshotCatalog: %v", err)
	}
	dsSnap := findDatasetSnapshot(t, snap, dsID)
	if len(dsSnap.Sessions) != 4 {
		t.Fatalf("Sessions = %d, want 4", len(dsSnap.Sessions))
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
}

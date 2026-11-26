package cognee

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func TestReadSessionHistoryPaginatesQAAndTraces(t *testing.T) {
	storeRoot := t.TempDir()
	cachePath := filepath.Join(storeRoot, "system", "databases", "cache.db")
	db, err := domain.OpenPrivateSQLite(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE cache_qa_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, qa_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		CREATE TABLE cache_trace_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		INSERT INTO cache_qa_entries VALUES
			(1, 'cfff15f2-b69c-4f02-bfb3-6e64ea5de031', 'antigravity-s1', 'qa1', '{"question":"本次重构的目标是什么","answer":"高内聚低耦合"}', '2026-11-26', '2099-01-01 00:00:00'),
			(2, 'cfff15f2-b69c-4f02-bfb3-6e64ea5de031', 'antigravity-s1', 'qa2', '{"question":"第二步做什么","answer":"验收并替换本地App"}', '2026-11-26', '2099-01-01 00:00:00'),
			(3, 'cfff15f2-b69c-4f02-bfb3-6e64ea5de031', 'antigravity-s1', 'qa3', '{"question":"expired","answer":"ignored"}', '2026-11-26', '2000-01-01 00:00:00');
		INSERT INTO cache_trace_entries VALUES
			(1, 'cfff15f2-b69c-4f02-bfb3-6e64ea5de031', 'antigravity-s1', '{"trace_id":"t1","origin_function":"Bash","status":"success","method_params":{"command":"pwd"},"method_return_value":"/tmp"}', '2026-11-26', '2099-01-01 00:00:00'),
			(2, 'cfff15f2-b69c-4f02-bfb3-6e64ea5de031', 'antigravity-s1', '{"trace_id":"t2","origin_function":"Edit","status":"error","error_message":"failed"}', '2026-11-26', '2099-01-01 00:00:00');
	`)
	if err != nil {
		t.Fatal(err)
	}

	projector := NewProjector(storeRoot, nil)
	page, err := projector.ReadSessionHistory(context.Background(), engine.SessionHistoryRequest{
		UserID:      "cfff15f2-b69c-4f02-bfb3-6e64ea5de031",
		SessionID:   "antigravity-s1",
		QAOffset:    0,
		TraceOffset: 1,
		Limit:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Label != "本次重构的目标是什么" || page.MsgCount != 2 || page.ToolCalls != 2 {
		t.Fatalf("unexpected totals/label: %+v", page)
	}
	if len(page.QAs) != 1 || page.QAs[0].ID != "qa1" || page.QAs[0].Answer != "高内聚低耦合" {
		t.Fatalf("unexpected QAs: %+v", page.QAs)
	}
	if len(page.Traces) != 1 || page.Traces[0].ID != "t2" || page.Traces[0].Name != "Edit" || page.Traces[0].Error != "failed" {
		t.Fatalf("unexpected Traces: %+v", page.Traces)
	}
}

func TestCacheStripsAntigravityRequestTagsFromSessionLabel(t *testing.T) {
	storeRoot := t.TempDir()
	cachePath := filepath.Join(storeRoot, "system", "databases", "cache.db")
	db, err := domain.OpenPrivateSQLite(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	uid := "cfff15f2-b69c-4f02-bfb3-6e64ea5de031"
	_, err = db.Exec(`
		CREATE TABLE cache_qa_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, qa_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		CREATE TABLE cache_trace_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		INSERT INTO cache_qa_entries VALUES
			(1, ?, 's-wrapped', 'q1', '{"question":"<USER_REQUEST>\n实现子会话聚合后端\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nworkspace: /tmp\n</ADDITIONAL_METADATA>","answer":"好的"}', '2026-11-26', '2099-01-01 00:00:00'),
			(2, ?, 's-meta-first', 'q2', '{"question":"<ADDITIONAL_METADATA>\nfoo: bar\n</ADDITIONAL_METADATA>\n<USER_REQUEST>\n  排查缓存标签  \n</USER_REQUEST>","answer":"ok"}', '2026-11-26', '2099-01-01 00:00:00'),
			(3, ?, 's-truncated', 'q3', '{"question":"<USER_REQUEST>\n未闭合的提问内容","answer":"ok"}', '2026-11-26', '2099-01-01 00:00:00'),
			(4, ?, 's-settings', 'q4', '{"question":"<USER_SETTINGS_CHANGE>\nmodel: gemini\n</USER_SETTINGS_CHANGE>\n真正的问题内容","answer":"ok"}', '2026-11-26', '2099-01-01 00:00:00');
	`, uid, uid, uid, uid)
	if err != nil {
		t.Fatal(err)
	}

	projector := NewProjector(storeRoot, nil)
	ctx := context.Background()
	want := map[string]string{
		"s-wrapped":    "实现子会话聚合后端",
		"s-meta-first": "排查缓存标签",
		"s-truncated":  "未闭合的提问内容",
		"s-settings":   "真正的问题内容",
	}
	refs := []SessionRef{
		{UserID: uid, SessionID: "s-wrapped"},
		{UserID: uid, SessionID: "s-meta-first"},
		{UserID: uid, SessionID: "s-truncated"},
		{UserID: uid, SessionID: "s-settings"},
	}
	hydrated, err := projector.HydrateSessionsFromCache(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for sid, wantLabel := range want {
		got := hydrated[SessionRef{UserID: uid, SessionID: sid}]
		if got.Label != wantLabel {
			t.Fatalf("HydrateSessionsFromCache(%s) label = %q, want %q", sid, got.Label, wantLabel)
		}
		page, err := projector.ReadSessionHistory(ctx, engine.SessionHistoryRequest{UserID: uid, SessionID: sid, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if page.Label != wantLabel {
			t.Fatalf("ReadSessionHistory(%s) label = %q, want %q", sid, page.Label, wantLabel)
		}
	}
}

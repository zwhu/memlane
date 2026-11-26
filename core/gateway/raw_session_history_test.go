package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type fakeSessionHistoryEngine struct {
	*fakeEngine
	cache   map[engine.SessionRef]engine.SessionCacheInfo
	history engine.SessionHistoryResult
}

func (f *fakeSessionHistoryEngine) HydrateSessionsFromCache(_ context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	out := make(map[engine.SessionRef]engine.SessionCacheInfo, len(refs))
	for _, ref := range refs {
		out[ref] = f.cache[ref]
	}
	return out, nil
}

func (f *fakeSessionHistoryEngine) ReadSessionHistory(_ context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	out := f.history
	qaEnd := min(len(out.QAs), req.QAOffset+req.Limit)
	traceEnd := min(len(out.Traces), req.TraceOffset+req.Limit)
	if req.QAOffset < len(out.QAs) {
		out.QAs = out.QAs[req.QAOffset:qaEnd]
	} else {
		out.QAs = []engine.SessionQA{}
	}
	if req.TraceOffset < len(out.Traces) {
		out.Traces = out.Traces[req.TraceOffset:traceEnd]
	} else {
		out.Traces = []engine.SessionTrace{}
	}
	return out, nil
}

func TestRawRecordsHydratesCacheAndServesSessionHistory(t *testing.T) {
	ctx := context.Background()
	var histEng *fakeSessionHistoryEngine
	s, store, _ := testServer(t, func(c *Config) {
		base := c.Engine.(*fakeEngine)
		histEng = &fakeSessionHistoryEngine{
			fakeEngine: base,
			cache: map[engine.SessionRef]engine.SessionCacheInfo{
				{UserID: "u1", SessionID: "antigravity-s1"}: {Label: "重构目标与边界", MsgCount: 4, ToolCalls: 26},
			},
			history: engine.SessionHistoryResult{
				Label:     "重构目标与边界",
				MsgCount:  2,
				ToolCalls: 1,
				QAs: []engine.SessionQA{
					{ID: "q1", Question: "重构目标与边界", Answer: "高内聚低耦合"},
					{ID: "q2", Question: "如何验证", Answer: "实机验收"},
				},
				Traces: []engine.SessionTrace{
					{ID: "t1", Name: "Bash", Status: "success", Params: json.RawMessage(`{"command":"go test"}`), Result: json.RawMessage(`"ok"`)},
				},
			},
		}
		c.Engine = histEng
	})

	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "antigravity-s1", UserID: "u1",
		LastActivityAt: "2026-11-26T03:00:00Z", IsTopLevel: true, SortTSDescKey: -100,
	}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "antigravity-empty", UserID: "u1",
		LastActivityAt: "2026-11-26T02:00:00Z", IsTopLevel: true, SortTSDescKey: -50,
	}))

	rawRec := manage(t, s, "GET", "/v1/projects/p/raw-records?kind=session&limit=10", "", 200)
	var raw map[string]any
	must(t, json.Unmarshal(rawRec.Body.Bytes(), &raw))
	if raw["cache_ready"] != true {
		t.Fatalf("expected cache_ready=true, got %+v", raw)
	}
	items, _ := raw["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 sessions, got %+v", raw)
	}
	first := items[0].(map[string]any)["session"].(map[string]any)
	if first["label"] != "重构目标与边界" || first["msg_count"] != float64(4) || first["tool_calls"] != float64(26) {
		t.Fatalf("expected hydrated session metadata, got %+v", first)
	}
	second := items[1].(map[string]any)["session"].(map[string]any)
	if label, _ := second["label"].(string); label != "" {
		t.Fatalf("expected empty label when cache has no question (not raw session_id), got %q", label)
	}

	histRec := manage(t, s, "GET", "/v1/raw-records/session-history?dataset_id=ds&session_id=antigravity-s1&user_id=u1&qa_offset=0&trace_offset=0&limit=20", "", 200)
	var hist map[string]any
	must(t, json.Unmarshal(histRec.Body.Bytes(), &hist))
	if hist["history_paging"] != true || hist["page_limit"] != float64(20) || hist["msg_count"] != float64(2) || hist["tool_calls"] != float64(1) {
		t.Fatalf("unexpected history envelope: %+v", hist)
	}
	if hist["label"] != "重构目标与边界" || hist["qa_more"] != false || hist["trace_more"] != false {
		t.Fatalf("unexpected history fields: %+v", hist)
	}
	qas, _ := hist["qas"].([]any)
	traces, _ := hist["traces"].([]any)
	if len(qas) != 2 || len(traces) != 1 {
		t.Fatalf("expected 2 qas and 1 trace, got qas=%d traces=%d", len(qas), len(traces))
	}
}

func TestRawRecordsChildrenPreviewAndSubagentHistory(t *testing.T) {
	ctx := context.Background()
	s, store, _ := testServer(t, func(c *Config) {
		base := c.Engine.(*fakeEngine)
		c.Engine = &fakeSessionHistoryEngine{
			fakeEngine: base,
			cache: map[engine.SessionRef]engine.SessionCacheInfo{
				{UserID: "u1", SessionID: "parent-many"}: {Label: "主会话规划", MsgCount: 3, ToolCalls: 5},
				{UserID: "u1", SessionID: "child-1"}:     {Label: "子任务 1", MsgCount: 1, ToolCalls: 4},
				{UserID: "u2", SessionID: "child-2"}:     {Label: "跨用户子任务", MsgCount: 0, ToolCalls: 8},
				{UserID: "u1", SessionID: "child-3"}:     {Label: "子任务 3", MsgCount: 1, ToolCalls: 1},
				{UserID: "u1", SessionID: "child-4"}:     {Label: "子任务 4", MsgCount: 0, ToolCalls: 2},
				{UserID: "u1", SessionID: "child-5"}:     {Label: "子任务 5", MsgCount: 0, ToolCalls: 3},
				{UserID: "u1", SessionID: "child-6"}:     {Label: "子任务 6", MsgCount: 0, ToolCalls: 1},
				{UserID: "u1", SessionID: "parent-few"}:  {Label: "小主会话", MsgCount: 1, ToolCalls: 0},
				{UserID: "u1", SessionID: "few-child-1"}: {Label: "唯一子会话", MsgCount: 1, ToolCalls: 2},
				{UserID: "u1", SessionID: "orphan-1"}:    {Label: "孤立子会话", MsgCount: 0, ToolCalls: 6},
			},
			history: engine.SessionHistoryResult{Label: "主会话规划", MsgCount: 1, ToolCalls: 1},
		}
	})

	// Parent with 6 children (exceeds preview limit of 5).
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "parent-many", UserID: "u1",
		LastActivityAt: "2026-11-26T05:00:00Z", IsTopLevel: true, ChildCount: 6, SortTSDescKey: -500,
	}))
	for i, child := range []struct {
		id, user, role string
		sortKey        int64
	}{
		{"child-1", "u1", "research", -490},
		{"child-2", "u2", "worker", -480},
		{"child-3", "u1", "reviewer", -470},
		{"child-4", "u1", "worker", -460},
		{"child-5", "u1", "worker", -450},
		{"child-6", "u1", "worker", -440},
	} {
		_ = i
		must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
			DatasetID: "ds", SessionID: child.id, UserID: child.user,
			ParentSessionID: "parent-many", RootSessionID: "parent-many", SubagentRole: child.role,
			NestingDepth: 1, IsTopLevel: false, SortTSDescKey: child.sortKey,
		}))
	}
	// Parent with 1 child (within preview limit -> children_has_more=false).
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "parent-few", UserID: "u1",
		LastActivityAt: "2026-11-26T04:00:00Z", IsTopLevel: true, ChildCount: 1, SortTSDescKey: -300,
	}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "few-child-1", UserID: "u1",
		ParentSessionID: "parent-few", RootSessionID: "parent-few", SubagentRole: "helper",
		NestingDepth: 1, IsTopLevel: false, SortTSDescKey: -290,
	}))
	// Orphan subagent stays top-level and is still marked is_subagent=true.
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", SessionID: "orphan-1", UserID: "u1",
		ParentSessionID: "missing-parent", SubagentRole: "explorer",
		NestingDepth: 1, IsTopLevel: true, SortTSDescKey: -100,
	}))

	rawRec := manage(t, s, "GET", "/v1/projects/p/raw-records?kind=session&limit=10", "", 200)
	var raw map[string]any
	must(t, json.Unmarshal(rawRec.Body.Bytes(), &raw))
	items, _ := raw["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 top-level items (parent-many, parent-few, orphan-1), got %d: %+v", len(items), items)
	}

	parentMany := items[0].(map[string]any)["session"].(map[string]any)
	previewMany, _ := parentMany["children_preview"].([]any)
	if parentMany["child_count"] != float64(6) || parentMany["children_has_more"] != true || len(previewMany) != 5 {
		t.Fatalf("parent-many preview mismatch: %+v", parentMany)
	}
	firstChild := previewMany[0].(map[string]any)
	secondChild := previewMany[1].(map[string]any)
	if firstChild["session_id"] != "child-1" || firstChild["label"] != "子任务 1" || firstChild["tool_calls"] != float64(4) || firstChild["is_subagent"] != true {
		t.Fatalf("first preview child = %+v", firstChild)
	}
	if secondChild["session_id"] != "child-2" || secondChild["label"] != "跨用户子任务" || secondChild["tool_calls"] != float64(8) {
		t.Fatalf("second preview child = %+v", secondChild)
	}

	parentFew := items[1].(map[string]any)["session"].(map[string]any)
	previewFew, _ := parentFew["children_preview"].([]any)
	if parentFew["child_count"] != float64(1) || parentFew["children_has_more"] != false || len(previewFew) != 1 {
		t.Fatalf("parent-few preview mismatch: %+v", parentFew)
	}

	orphan := items[2].(map[string]any)["session"].(map[string]any)
	if orphan["session_id"] != "orphan-1" || orphan["is_subagent"] != true || orphan["subagent_role"] != "explorer" {
		t.Fatalf("orphan = %+v", orphan)
	}

	// Session history for parent returns all 6 hydrated subagents.
	histParent := decodeResponse[map[string]any](t, manage(t, s, "GET",
		"/v1/raw-records/session-history?dataset_id=ds&session_id=parent-many&user_id=u1", "", 200))
	subagents, _ := histParent["subagents"].([]any)
	if len(subagents) != 6 {
		t.Fatalf("expected 6 subagents in parent history, got %d: %+v", len(subagents), subagents)
	}

	// Session history for cross-user child resolves parent_label cleanly.
	histChild := decodeResponse[map[string]any](t, manage(t, s, "GET",
		"/v1/raw-records/session-history?dataset_id=ds&session_id=child-2&user_id=u2", "", 200))
	if histChild["parent_label"] != "主会话规划" || histChild["parent_session_id"] != "parent-many" || histChild["is_subagent"] != true {
		t.Fatalf("child history = %+v", histChild)
	}
}

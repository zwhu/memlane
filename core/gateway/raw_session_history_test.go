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

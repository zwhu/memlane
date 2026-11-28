package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func TestRawRecordsAndKnowledgeBackfillBareUUIDAgentID(t *testing.T) {
	ctx := context.Background()
	s, store, _ := testServer(t, func(c *Config) {
		base := c.Engine.(*fakeEngine)
		base.documents["doc-from-trae"] = engine.Item{
			ID: "doc-from-trae", DatasetID: "ds", Content: "TRAE CLI lesson body", MimeType: "text/markdown",
		}
		c.Engine = &fakeSessionHistoryEngine{
			fakeEngine: base,
			cache: map[engine.SessionRef]engine.SessionCacheInfo{
				{UserID: "u-shared", SessionID: "01a2201c-ff97-7430-8035-dd1c06201848"}: {Label: "TRAE CLI 原生会话", MsgCount: 3, ToolCalls: 5},
				{UserID: "u-shared", SessionID: "01a2201c-ff97-7430-8035-dd1c06201849"}: {Label: "TRAE CLI 子会话", MsgCount: 1, ToolCalls: 2},
				{UserID: "u-codex", SessionID: "01a2201c-ff97-7430-8035-dd1c06202222"}:  {Label: "Codex 原生会话", MsgCount: 2, ToolCalls: 1},
			},
			histories: map[string]engine.SessionHistoryResult{
				"01a2201c-ff97-7430-8035-dd1c06201848": {
					Label:     "TRAE CLI 原生会话",
					MsgCount:  1,
					ToolCalls: 0,
					QAs:       []engine.SessionQA{{ID: "qa-1", Question: "修复图标", Answer: "已定位根因"}},
				},
			},
		}
	})

	const (
		traeAgentID  = "traex-7859e85d2cf88d393366e77e"
		codexAgentID = "codex-2564787a1122334455667788"
		traeSID      = "01a2201c-ff97-7430-8035-dd1c06201848"
		traeChildSID = "01a2201c-ff97-7430-8035-dd1c06201849"
		codexSID     = "01a2201c-ff97-7430-8035-dd1c06202222"
	)

	must(t, store.UpsertAgent(ctx, domain.Agent{ID: traeAgentID, Kind: "traex", Name: "TRAE CLI"}))
	must(t, store.UpsertAgent(ctx, domain.Agent{ID: codexAgentID, Kind: "codex", Name: "Codex", PrincipalID: "u-codex"}))

	// Seed catalog_sessions with EMPTY agent_id (simulating Cognee projection before backfill).
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", UserID: "u-shared", SessionID: traeSID, AgentID: "",
		LastActivityAt: "2026-11-28T03:00:00Z", IsTopLevel: true, ChildCount: 1, SortTSDescKey: -300,
	}))
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", UserID: "u-shared", SessionID: traeChildSID, AgentID: "",
		ParentSessionID: traeSID, RootSessionID: traeSID, SubagentRole: "worker",
		LastActivityAt: "2026-11-28T02:59:00Z", IsTopLevel: false, NestingDepth: 1, SortTSDescKey: -290,
	}))
	// Codex session has NO capture_inbox row, only unique agents.principal_id == u-codex.
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", UserID: "u-codex", SessionID: codexSID, AgentID: "",
		LastActivityAt: "2026-11-28T02:00:00Z", IsTopLevel: true, SortTSDescKey: -200,
	}))

	// Seed capture_inbox for TRAE CLI parent and child.
	for _, row := range []domain.CaptureInboxItem{
		{ID: "ib-trae-parent", ProjectID: "p", DatasetID: "ds", AgentID: traeAgentID, SessionID: traeSID, MessageID: "m1", Kind: "capture_qa", PayloadJSON: `{"question":"q","answer":"a"}`},
		{ID: "ib-trae-child", ProjectID: "p", DatasetID: "ds", AgentID: traeAgentID, SessionID: traeChildSID, MessageID: "m2", Kind: "capture_qa", PayloadJSON: `{"question":"cq","answer":"ca"}`},
	} {
		_, err := store.EnqueueInboxItem(ctx, row)
		must(t, err)
	}

	// Seed knowledge doc linked to the bare-UUID TRAE CLI session.
	now := time.Now().UTC()
	must(t, store.UpsertCatalogDoc(ctx, domain.CatalogDoc{
		DatasetID: "ds", DocID: "doc-from-trae", Kind: "knowledge", IsKnowledge: true,
		Category: "踩坑经验", SessionID: traeSID, DocName: "trae-lesson", DocLabel: "TRAE CLI UUID 会话提炼记忆",
		EffectiveDate: now.Format(time.DateOnly), DayDescKey: -20261128, SortTSDescKey: -100,
		CreatedAtRaw: domain.FormatUTCNS(now), CreatedAtNS: domain.FormatUTCNS(now),
	}))

	// 1. GET /api/projects/p/raw-records -> top-level & children_preview both backfilled.
	apiRaw := decodeResponse[pageResult[rawItem]](t, manage(t, s, "GET", "/api/projects/p/raw-records?kind=session&limit=10", "", 200))
	if len(apiRaw.Items) != 2 {
		t.Fatalf("/api raw-records count = %d, want 2", len(apiRaw.Items))
	}
	if apiRaw.Items[0].SessionID != traeSID || apiRaw.Items[0].AgentID != traeAgentID {
		t.Fatalf("TRAE session = %+v, want agent_id=%q", apiRaw.Items[0], traeAgentID)
	}
	if len(apiRaw.Items[0].ChildrenPreview) != 1 || apiRaw.Items[0].ChildrenPreview[0].AgentID != traeAgentID {
		t.Fatalf("TRAE children_preview = %+v, want agent_id=%q", apiRaw.Items[0].ChildrenPreview, traeAgentID)
	}
	if apiRaw.Items[1].SessionID != codexSID || apiRaw.Items[1].AgentID != codexAgentID {
		t.Fatalf("Codex session = %+v, want agent_id=%q", apiRaw.Items[1], codexAgentID)
	}

	// 2. GET /v1/projects/p/raw-records -> nested session.agent_id & children_preview backfilled.
	v1Raw := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/projects/p/raw-records?kind=session&limit=10", "", 200))
	v1Items, _ := v1Raw["items"].([]any)
	firstEntry := v1Items[0].(map[string]any)
	firstSess := firstEntry["session"].(map[string]any)
	if firstSess["agent_id"] != traeAgentID {
		t.Fatalf("/v1 session.agent_id = %v, want %q", firstSess["agent_id"], traeAgentID)
	}
	preview := firstSess["children_preview"].([]any)
	if preview[0].(map[string]any)["agent_id"] != traeAgentID {
		t.Fatalf("/v1 children_preview[0].agent_id = %v, want %q", preview[0].(map[string]any)["agent_id"], traeAgentID)
	}

	// 3. GET /api/projects/p/raw-records/sessions/{sid}/children -> child agent_id backfilled.
	children := decodeResponse[pageResult[rawItem]](t, manage(t, s, "GET", "/api/projects/p/raw-records/sessions/"+traeSID+"/children?limit=10", "", 200))
	if len(children.Items) != 1 || children.Items[0].AgentID != traeAgentID {
		t.Fatalf("/children = %+v, want agent_id=%q", children.Items, traeAgentID)
	}

	// 4. GET /v1/raw-records/session-history when catalog_sessions row EXISTS with agent_id="" -> backfilled for session & subagents.
	hist := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/raw-records/session-history?dataset_id=ds&session_id="+traeSID+"&user_id=u-shared", "", 200))
	if hist["agent_id"] != traeAgentID {
		t.Fatalf("session-history agent_id = %v, want %q", hist["agent_id"], traeAgentID)
	}
	subs, _ := hist["subagents"].([]any)
	if len(subs) != 1 || subs[0].(map[string]any)["agent_id"] != traeAgentID {
		t.Fatalf("session-history subagents = %+v, want agent_id=%q", subs, traeAgentID)
	}

	// 5. GET /api/datasets/ds/sessions/{sid} -> source session jump backfilled.
	sessLookup := decodeResponse[rawItem](t, manage(t, s, "GET", "/api/datasets/ds/sessions/"+traeSID, "", 200))
	if sessLookup.AgentID != traeAgentID {
		t.Fatalf("/api/datasets/ds/sessions agent_id = %q, want %q", sessLookup.AgentID, traeAgentID)
	}

	// 6. GET /api/projects/p/knowledge, /v1/overview/bootstrap, /api/datasets/ds/documents/{doc} -> knowledge items include source session agent_id.
	knowledgePage := decodeResponse[pageResult[catalogItem]](t, manage(t, s, "GET", "/api/projects/p/knowledge?limit=10", "", 200))
	if len(knowledgePage.Items) != 1 || knowledgePage.Items[0].AgentID != traeAgentID {
		t.Fatalf("/api/projects/p/knowledge items = %+v, want agent_id=%q", knowledgePage.Items, traeAgentID)
	}
	ov := decodeResponse[overviewSnapshot](t, manage(t, s, "GET", "/v1/overview/bootstrap?recent_limit=10", "", 200))
	if len(ov.RecentItems) != 1 || ov.RecentItems[0].AgentID != traeAgentID {
		t.Fatalf("/v1/overview/bootstrap recent_items = %+v, want agent_id=%q", ov.RecentItems, traeAgentID)
	}
	docDetail := decodeResponse[map[string]any](t, manage(t, s, "GET", "/api/datasets/ds/documents/doc-from-trae", "", 200))
	if docDetail["agent_id"] != traeAgentID {
		t.Fatalf("/api/datasets/ds/documents agent_id = %v, want %q", docDetail["agent_id"], traeAgentID)
	}

	// 7. GET /api/search & /v1/search/recent -> both session and knowledge items carry agent_id.
	searchRes := decodeResponse[struct {
		Items []json.RawMessage `json:"items"`
	}](t, manage(t, s, "GET", "/api/search?q="+traeSID+"&filter=all", "", 200))
	if len(searchRes.Items) < 2 {
		t.Fatalf("/api/search expected doc + session items, got %d", len(searchRes.Items))
	}
	for _, raw := range searchRes.Items {
		var m map[string]any
		must(t, json.Unmarshal(raw, &m))
		if m["agent_id"] != traeAgentID {
			t.Fatalf("/api/search item missing agent_id=%q: %+v", traeAgentID, m)
		}
	}
	recentRes := decodeResponse[struct {
		KnowledgeItems []catalogItem `json:"knowledge_items"`
		SessionItems   []rawItem     `json:"session_items"`
	}](t, manage(t, s, "GET", "/v1/search/recent?limit=10", "", 200))
	if len(recentRes.KnowledgeItems) != 1 || recentRes.KnowledgeItems[0].AgentID != traeAgentID {
		t.Fatalf("/v1/search/recent knowledge_items = %+v", recentRes.KnowledgeItems)
	}
	if len(recentRes.SessionItems) != 2 || recentRes.SessionItems[0].AgentID != traeAgentID || recentRes.SessionItems[1].AgentID != codexAgentID {
		t.Fatalf("/v1/search/recent session_items = %+v", recentRes.SessionItems)
	}
}

func TestSyncCatalogAndSessionsPersistBackfilledAgentIDs(t *testing.T) {
	ctx := context.Background()
	const (
		traeAgentID = "traex-7859e85d2cf88d393366e77e"
		traeSID     = "01a2201c-ff97-7430-8035-dd1c06201848"
	)
	proj := &wave3aRecordingProjector{
		onCall: func(targets []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
			ds := targets[0].DatasetID
			return &engine.CatalogSnapshot{
				Datasets: []engine.CatalogDatasetSnapshot{{
					DatasetID: ds,
					Exists:    true,
					Sessions: []engine.CatalogSnapshotSession{{
						DatasetID: ds, UserID: "u1", SessionID: traeSID, AgentID: "", Status: "active", SortTSDescKey: -100,
					}},
				}},
			}, nil
		},
	}
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.Projector = proj
	})
	_, err := store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: "ib-sync-1", ProjectID: "p", DatasetID: "ds", AgentID: traeAgentID,
		SessionID: traeSID, MessageID: "m1", Kind: "capture_qa", PayloadJSON: `{}`,
	})
	must(t, err)

	// Startup full SyncCatalog persists backfilled agent_id into catalog_sessions.
	_, err = s.SyncCatalog(ctx)
	must(t, err)
	sessions, err := store.ListCatalogSessions(ctx, "ds", 10)
	must(t, err)
	if len(sessions) != 1 || sessions[0].AgentID != traeAgentID {
		t.Fatalf("SyncCatalog catalog_sessions = %+v, want agent_id=%q", sessions, traeAgentID)
	}

	// Incremental SyncDatasetCatalog preserves backfilled agent_id.
	_, err = s.SyncDatasetCatalog(ctx, "ds")
	must(t, err)
	sessions, err = store.ListCatalogSessions(ctx, "ds", 10)
	must(t, err)
	if len(sessions) != 1 || sessions[0].AgentID != traeAgentID {
		t.Fatalf("SyncDatasetCatalog catalog_sessions = %+v, want agent_id=%q", sessions, traeAgentID)
	}

	// Session-only SyncDatasetSessions preserves backfilled agent_id.
	_, err = s.SyncDatasetSessions(ctx, "ds")
	must(t, err)
	sessions, err = store.ListCatalogSessions(ctx, "ds", 10)
	must(t, err)
	if len(sessions) != 1 || sessions[0].AgentID != traeAgentID {
		t.Fatalf("SyncDatasetSessions catalog_sessions = %+v, want agent_id=%q", sessions, traeAgentID)
	}

	// Searching by agent_id ("traex") now matches the persisted catalog_sessions row!
	searchRes := decodeResponse[struct {
		Items []rawItem `json:"items"`
	}](t, manage(t, s, "GET", "/api/search?q=traex&filter=raw", "", 200))
	if len(searchRes.Items) != 1 || searchRes.Items[0].SessionID != traeSID || searchRes.Items[0].AgentID != traeAgentID {
		t.Fatalf("/api/search?q=traex = %+v, want session %q with agent_id %q", searchRes.Items, traeSID, traeAgentID)
	}
}

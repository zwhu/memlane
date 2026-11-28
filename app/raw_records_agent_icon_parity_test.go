package app

import (
	"context"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	appsearch "github.com/zwhu/memlane/app/search"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway"
)

type rawAgentParityEngine struct {
	wireEngine
	cache   map[engine.SessionRef]engine.SessionCacheInfo
	history map[string]engine.SessionHistoryResult
}

func (e rawAgentParityEngine) HydrateSessionsFromCache(_ context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	out := make(map[engine.SessionRef]engine.SessionCacheInfo, len(refs))
	for _, ref := range refs {
		out[ref] = e.cache[ref]
	}
	return out, nil
}

func (e rawAgentParityEngine) ReadSessionHistory(_ context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	if h, ok := e.history[req.SessionID]; ok {
		return h, nil
	}
	return engine.SessionHistoryResult{QAs: []engine.SessionQA{}, Traces: []engine.SessionTrace{}}, nil
}

func TestRawRecordsBareUUIDAgentIconGatewayParity(t *testing.T) {
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	store, err := domain.OpenStore(filepath.Join(dir, "memlane.sqlite3"))
	check(err)
	t.Cleanup(func() { check(store.Close()) })

	const (
		token        = "raw-agent-icon-parity-management-token"
		gatewayURL   = "http://127.0.0.1:61256"
		identity     = "desktop-agent-icon-parity"
		projectID    = "proj-native-hooks"
		datasetID    = "11111111-1111-4111-8111-111111111111"
		traeAgentID  = "traex-7859e85d2cf88d393366e77e"
		codexAgentID = "codex-2564787a1122334455667788"
		traeUUID     = "01a2201c-ff97-7430-8035-dd1c06201848"
		codexUUID    = "01a2201c-ff97-7430-8035-dd1c06209999"
	)

	tokenFile := filepath.Join(dir, "management.token")
	check(os.WriteFile(tokenFile, []byte(token), 0o600))
	check(store.UpsertProject(ctx, domain.Project{ID: projectID, Name: "Memlane", DatasetID: datasetID}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: traeAgentID, Kind: "traex", Name: "TRAE CLI"}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: codexAgentID, Kind: "codex", Name: "Codex"}))

	// Seed capture_inbox with real agent_ids + bare UUID session_ids, while catalog_sessions.agent_id is EMPTY.
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: "inbox-trae-1", ProjectID: projectID, DatasetID: datasetID,
		AgentID: traeAgentID, SessionID: traeUUID, MessageID: "m-trae-1",
		Kind: "capture_qa", PayloadJSON: `{"question":"检查 TRAE 图标","answer":"已核实"}`,
	})
	check(err)
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: "inbox-codex-1", ProjectID: projectID, DatasetID: datasetID,
		AgentID: codexAgentID, SessionID: codexUUID, MessageID: "m-codex-1",
		Kind: "capture_qa", PayloadJSON: `{"question":"检查 Codex 图标","answer":"已核实"}`,
	})
	check(err)

	now := time.Now().UTC()
	check(store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: datasetID, UserID: "u-default", SessionID: traeUUID, AgentID: "",
		Status: "active", StartedAt: domain.FormatUTCNS(now), LastActivityAt: domain.FormatUTCNS(now),
		IsTopLevel: true, SortTSDescKey: -200,
	}))
	check(store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: datasetID, UserID: "u-default", SessionID: codexUUID, AgentID: "",
		Status: "active", StartedAt: domain.FormatUTCNS(now), LastActivityAt: domain.FormatUTCNS(now),
		IsTopLevel: true, SortTSDescKey: -100,
	}))
	check(store.UpsertCatalogDoc(ctx, domain.CatalogDoc{
		DatasetID: datasetID, DocID: "doc-trae-lesson", Kind: "knowledge", IsKnowledge: true,
		Category: "踩坑经验", SessionID: traeUUID, DocName: "text_0123456789abcdef0123456789abcdef",
		DocLabel: "TRAE CLI 原生 Hook 会话归属修复", EffectiveDate: now.Format(time.DateOnly),
		DayDescKey: -20261128, SortTSDescKey: -50, CreatedAtRaw: domain.FormatUTCNS(now), CreatedAtNS: domain.FormatUTCNS(now),
	}))
	_, err = store.EnsureReadScope(ctx, identity, projectID, datasetID)
	check(err)

	eng := rawAgentParityEngine{
		cache: map[engine.SessionRef]engine.SessionCacheInfo{
			{UserID: "u-default", SessionID: codexUUID}: {Label: "Codex 原生 UUID 会话标题", MsgCount: 2, ToolCalls: 1},
		},
		history: map[string]engine.SessionHistoryResult{
			traeUUID: {
				Label:     "",
				MsgCount:  1,
				ToolCalls: 0,
				QAs:       []engine.SessionQA{{ID: "qa-1", Question: "为什么显示文档图标", Answer: "因为 agent_id 为空"}},
			},
			codexUUID: {
				Label:     "Codex 原生 UUID 会话标题",
				MsgCount:  2,
				ToolCalls: 1,
				QAs:       []engine.SessionQA{{ID: "qa-2", Question: "Codex 原生 UUID 会话标题", Answer: "已修复"}},
			},
		},
	}
	server, err := gateway.NewServer(gateway.Config{
		Store:           store,
		ManagementToken: token,
		Engine:          eng,
	})
	check(err)

	httpClient := &http.Client{Transport: queueTestTransport(server.Handler().ServeHTTP)}
	control := ProjectControlClient(settings.ProjectControlClient{
		Config: settings.ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
		HTTP:   httpClient,
	})
	apiClient, err := api.NewClient(api.Config{
		BaseURL: gatewayURL, ManagementToken: token, ReadingIdentity: identity, HTTPClient: httpClient,
	})
	check(err)

	traeIcon := agentBrandIcon("traex")
	codexIcon := agentBrandIcon("codex")
	if traeIcon == homeDocument || codexIcon == homeDocument || traeIcon == codexIcon {
		t.Fatal("brand icon fixtures invalid")
	}

	// 1. Raw records list via real gateway -> app/api -> v2RawRecordsToItems.
	rawResp, err := control.rawPage(ctx, projectID, "会话", "")
	check(err)
	rawItems, err := v2RawRecordsToItems(datasetID, rawResp)
	check(err)
	if len(rawItems) != 2 {
		t.Fatalf("raw items count = %d, want 2", len(rawItems))
	}
	traeItem, codexItem := rawItems[0], rawItems[1]
	if traeItem.Session == nil || traeItem.Session.Agent != traeAgentID {
		t.Fatalf("trae raw item agent = %+v, want %q", traeItem.Session, traeAgentID)
	}
	if codexItem.Session == nil || codexItem.Session.Agent != codexAgentID {
		t.Fatalf("codex raw item agent = %+v, want %q", codexItem.Session, codexAgentID)
	}
	if recentAgentIcon(traeItem) != traeIcon || sourceAgentName(traeItem) != "TRAE CLI" ||
		itemSessionIdentity(traeItem) != "TRAE CLI 会话 · 01a2201c…06201848" {
		t.Fatalf("trae raw item presentation mismatch: name=%q id=%q", sourceAgentName(traeItem), itemSessionIdentity(traeItem))
	}
	if recentAgentIcon(codexItem) != codexIcon || sourceAgentName(codexItem) != "Codex" ||
		itemSessionIdentity(codexItem) != "Codex 会话 · 01a2201c…06209999" {
		t.Fatalf("codex raw item presentation mismatch: name=%q id=%q", sourceAgentName(codexItem), itemSessionIdentity(codexItem))
	}

	// 2. Session history detail via real gateway.
	histPage, err := control.rawSessionHistory(ctx, traeItem, 0, 0)
	check(err)
	detailItem, _ := sessionItemFromHistoryPage(traeItem, histPage)
	if detailItem.Session == nil || detailItem.Session.Agent != traeAgentID ||
		recentAgentIcon(detailItem) != traeIcon || sessionSourceAgentDisplay(detailItem) != "TRAE CLI" {
		t.Fatalf("session history presentation mismatch: %+v", detailItem.Session)
	}

	// 3. Source session jump via real gateway.
	sourceSess, err := control.readingSourceSession(ctx, datasetID, traeUUID, "")
	check(err)
	sourceItem := Item{ID: sourceSess.ID, Dataset: sourceSess.Dataset, Kind: "会话", Session: &sourceSess}
	if sourceSess.Agent != traeAgentID || recentAgentIcon(sourceItem) != traeIcon || itemSessionIdentity(sourceItem) != "TRAE CLI 会话 · 01a2201c…06201848" {
		t.Fatalf("readingSourceSession mismatch: %+v", sourceSess)
	}

	// 4. Knowledge list + Overview bootstrap -> bare-UUID knowledge item resolves TRAE icon & source identity.
	kPage, err := control.listKnowledgePage(ctx, identity, projectID, "date", "", false, "", "", 10, v2KnowledgePageOpts{})
	check(err)
	if len(kPage.Items) != 1 {
		t.Fatalf("knowledge items = %d, want 1", len(kPage.Items))
	}
	kItem := kPage.Items[0].toItem("")
	if kItem.SourceAgent != traeAgentID || recentAgentIcon(kItem) != traeIcon || sourceAgentName(kItem) != "TRAE CLI" ||
		sessionIdentityWithAgent(kItem.Source, itemSourceAgentID(kItem)) != "TRAE CLI 会话 · 01a2201c…06201848" {
		t.Fatalf("knowledge item source agent mismatch: source=%q agent=%q name=%q", kItem.Source, kItem.SourceAgent, sourceAgentName(kItem))
	}

	ovResp, err := control.overviewBootstrap(ctx, identity, 30, 10, 10, 10)
	check(err)
	ovSnap, _, _, _ := buildOverviewFromV2Bootstrap([]Project{{ID: projectID, Name: "Memlane", Dataset: datasetID}}, ovResp, 10)
	if len(ovSnap.Knowledge) != 1 || recentAgentIcon(ovSnap.Knowledge[0]) != traeIcon || sourceAgentName(ovSnap.Knowledge[0]) != "TRAE CLI" {
		t.Fatalf("overview recent knowledge mismatch: %+v", ovSnap.Knowledge)
	}

	// 5. Unified search via real gateway -> app/search -> desktopSearchItems preserves TRAE icon & identity.
	searchResp, err := apiClient.Search(ctx, api.SearchRequest{Query: traeUUID, Filter: "all", Limit: 10})
	check(err)
	mapped := appsearch.MapSearchResponse(searchResp, traeUUID, map[string]bool{datasetID: true})
	desktopItems := desktopSearchItems(mapped)
	if len(desktopItems) < 2 {
		t.Fatalf("unified search desktop items = %d, want >=2", len(desktopItems))
	}
	for _, item := range desktopItems {
		if recentAgentIcon(item) != traeIcon || sourceAgentName(item) != "TRAE CLI" {
			t.Fatalf("unified search item (%s %s) lost TRAE brand: name=%q session=%+v sourceAgent=%q",
				item.Kind, item.ID, sourceAgentName(item), item.Session, item.SourceAgent)
		}
		if item.Session != nil {
			if topic, _ := searchItemPrimaryText(item, traeUUID); topic != "TRAE CLI 会话 · 01a2201c…06201848" {
				t.Fatalf("unified search session topic = %q, want TRAE CLI 会话 · 01a2201c…06201848", topic)
			}
		}
	}

	// 6. Render raw records list & detail UI to confirm TRAE CLI identity text (never "Agent 会话").
	a := projectUIFixture()
	a.kind = "原始记录"
	s := a.state()
	s.Loaded = true
	s.Items = rawItems
	s.Selected = &detailItem
	darkMode := false
	tt := ui.NewTester(func(c *ui.Context) {
		applyAppearance(darkMode)
		ui.Themed(c, homeTheme).Fill().Background(tok.Bg).Children(func() {
			ui.Row(c).Fill().Children(func() {
				ui.Column(c).Width(360).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Children(func() { a.itemList(c, s, false) })
				a.projectDetail(c, s)
			})
		})
	}, 1100, 720)
	t.Cleanup(func() { applyAppearance(false) })
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if _, ok := tt.Find("TRAE CLI 会话 · 01a2201c…06201848"); !ok {
		t.Fatal("rendered raw records UI missing TRAE CLI session identity")
	}
	if _, ok := tt.Find("Agent 会话 · 01a2201c…06201848"); ok {
		t.Fatal("rendered raw records UI must not fall back to generic Agent 会话")
	}

	// Optional @2x light/dark visual check capture when MEMLANE_CAPTURE_UI_DIR is set.
	if outDir := os.Getenv("MEMLANE_CAPTURE_UI_DIR"); outDir != "" {
		check(os.MkdirAll(outDir, 0o755))
		save := func(name string, tester *ui.Tester, dark bool) {
			darkMode = dark
			applyAppearance(dark)
			tester.SetScale(2)
			tester.SetDark(dark)
			tester.SetPreferences(ui.Preferences{ReduceMotion: true})
			tester.Advance(time.Second)
			tester.Frame()
			suffix := "light"
			if dark {
				suffix = "dark"
			}
			f, err := os.Create(filepath.Join(outDir, name+"-"+suffix+".png"))
			check(err)
			check(png.Encode(f, tester.Image()))
			check(f.Close())
		}
		save("raw-records-uuid-agent-icon", tt, false)
		save("raw-records-uuid-agent-icon", tt, true)

		ka := projectUIFixture()
		ka.kind = "知识"
		ks := ka.state()
		ks.Loaded = true
		kItem.Body = "# Session learning (session " + traeUUID + ")\n\nTRAE CLI 原生 Hook 会话归属修复已完成，原始记录列表与知识出处均显示 TRAE 品牌图标。"
		kItem.BodyLoaded = true
		ks.Items = []Item{kItem}
		ks.Selected = &kItem
		ktt := ui.NewTester(func(c *ui.Context) {
			applyAppearance(darkMode)
			ui.Themed(c, homeTheme).Fill().Background(tok.Bg).Children(func() {
				ui.Row(c).Fill().Children(func() {
					ui.Column(c).Width(360).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Children(func() { ka.knowledgeList(c, ks) })
					ka.projectDetail(c, ks)
				})
			})
		}, 1100, 720)
		ktt.SetPreferences(ui.Preferences{ReduceMotion: true})
		save("knowledge-source-uuid-agent-icon", ktt, false)
		save("knowledge-source-uuid-agent-icon", ktt, true)
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/overview"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/search"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/app/shell"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway"
	"github.com/zwhu/memlane/core/gateway/completion"
	"github.com/zwhu/memlane/core/profiles"
	"github.com/zwhu/memlane/kit/agents"
)

type wireEngine struct {
	engine.MemoryEngine
	engine.ModelController
}

func (wireEngine) GetItem(_ context.Context, scope engine.Scope, id string) (engine.Item, error) {
	return engine.Item{ID: id, DatasetID: scope.DatasetID, Content: "Document text", MimeType: "text/markdown"}, nil
}

func (wireEngine) Health(context.Context) (engine.HealthStatus, error) {
	return engine.HealthStatus{OK: true, Engine: "cognee", Mode: "ready", ModelDigest: "live-digest"}, nil
}

func (wireEngine) ModelStatus(context.Context) (engine.ModelStatusResult, error) {
	return engine.ModelStatusResult{OK: true, Mode: "ready", ModelDigest: "live-digest", OutputLanguage: "zh-CN"}, nil
}

func TestGatewayWireParity(t *testing.T) {
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
	const token = "wire-parity-management-token-private"
	const key = "wire-parity-provider-key-private"
	const bridge = "wire-parity-bridge-token-private"
	tokenFile := filepath.Join(dir, "management.token")
	bridgeFile := filepath.Join(dir, "bridge.token")
	check(os.WriteFile(tokenFile, []byte(token), 0o600))
	check(os.WriteFile(bridgeFile, []byte(bridge), 0o600))
	check(store.UpsertProject(ctx, domain.Project{ID: "project", Name: "Project", DatasetID: "project-ds", Aliases: []string{dir}, DesiredRevision: 3}))
	_, err = store.SetProjectAutoCaptureWithMeta(ctx, "project", true, "system", "policy_resume")
	check(err)
	filter, err := store.RecordCaptureFilterEvent(ctx, domain.CaptureFilterEvent{
		ProjectID: "project", AgentID: "agent", HostKind: "traex", Rule: "*.key", Category: "sensitive_path", Reason: "TRAE CLI 补丁路径命中",
	})
	check(err)
	check(store.UpsertProject(ctx, domain.Project{ID: "second", Name: "Other", DatasetID: "other-ds"}))
	check(store.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "common", Name: "Common", DatasetID: "common-ds"}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: "agent", Kind: "codex", Name: "Codex"}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: "retired", Kind: "pi", Name: "Retired", RetiredAt: domain.NowUTCNS()}))
	read := domain.AccessRead
	check(store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "project", AgentID: "agent", Access: &read}))
	check(store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "common", TargetID: "common", AgentID: "agent", Access: &read}))
	now := time.Now()
	day := now.Format(time.DateOnly)
	for _, d := range []domain.CatalogDoc{
		{DatasetID: "project-ds", DocID: "lesson", Kind: "knowledge", DocName: "Needle lesson", DocLabel: "Needle lesson", IsKnowledge: true, EffectiveDate: day},
		{DatasetID: "common-ds", DocID: "shared", Kind: "common", DocName: "Shared lesson", IsKnowledge: true, EffectiveDate: day, CommonCategory: "experience"},
		{DatasetID: "project-ds", DocID: "material", Kind: "raw_doc", DocName: "Material", EffectiveDate: day},
	} {
		d.CreatedAtNS, d.CreatedAtRaw, d.ContentHash = domain.FormatUTCNS(now), domain.FormatUTCNS(now), "body-hash"
		check(store.UpsertCatalogDoc(ctx, d))
	}
	check(store.UpsertCatalogSyncState(ctx, domain.CatalogSyncState{DatasetID: "project-ds", CatalogVersion: "catalog-1"}))
	check(store.UpsertCatalogSession(ctx, domain.CatalogSession{DatasetID: "project-ds", UserID: "user", AgentID: "agent", SessionID: "root", IsTopLevel: true, ChildCount: 1, LastActivityAt: domain.FormatUTCNS(now)}))
	check(store.UpsertCatalogSession(ctx, domain.CatalogSession{DatasetID: "project-ds", UserID: "user", AgentID: "agent", SessionID: "child", ParentSessionID: "root", LastActivityAt: domain.FormatUTCNS(now)}))
	_, err = store.RecordRecallEvent(ctx, domain.RecallEventInput{AgentID: "agent", EventID: "event", Channel: "explicit", Hits: []domain.RecallHit{{DatasetID: "project-ds", DocumentID: "lesson"}}})
	check(err)
	const identity = "desktop-reader"
	_, err = store.EnsureReadScope(ctx, identity, "project", "project-ds")
	check(err)

	model := wireEngine{}
	profileService, err := profiles.NewService(profiles.Config{Store: store, Engine: model})
	check(err)
	t.Cleanup(func() { check(profileService.Close(ctx)) })
	server, err := gateway.NewServer(gateway.Config{Store: store, ManagementToken: token, BridgeTokenFile: bridgeFile,
		Engine: model, ProfilesHandler: profileService.Handler()})
	check(err)
	const gatewayURL = "http://127.0.0.1:61255"
	httpClient := &http.Client{Transport: queueTestTransport(server.Handler().ServeHTTP)}
	control := settings.ProjectControlClient{Config: settings.ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile}, HTTP: httpClient}
	desktop := ProjectControlClient(control)
	apiClient, err := api.NewClient(api.Config{BaseURL: gatewayURL, ManagementToken: token, ReadingIdentity: identity, HTTPClient: httpClient})
	check(err)
	directory, err := desktop.directory(ctx)
	check(err)
	if len(directory.Projects) != 2 || len(directory.Agents) != 1 || directory.Common == nil ||
		directory.Common.Overrides["agent"].Access == nil || directory.Defaults.Access != "maintain" ||
		!directory.Defaults.Capture || !directory.Defaults.AutoProcess || directory.Agents[0].DefaultAccess != "none" ||
		directory.Projects[0].Overrides["agent"].Access == nil || !directory.Projects[0].PermissionsReady(directory.Agents...) ||
		len(directory.Projects[0].Aliases) != 1 || directory.Projects[1].Overrides == nil {
		t.Fatalf("desktop directory: %+v", directory)
	}
	var updated settings.AccessDirectory
	check(control.RequestWithHeaders(ctx, "PUT", "/v1/projects/project/overrides", nil, map[string]any{
		"expected_revision": directory.Revision, "agent_id": "agent", "fields": map[string]bool{"capture": false},
	}, &updated, 0))
	override := updated.Projects[0].Overrides["agent"]
	if updated.Revision <= directory.Revision || override.Capture == nil || *override.Capture || override.Access == nil || *override.Access != "read" {
		t.Fatalf("ACL receipt: %+v", override)
	}
	projects, err := apiClient.ListProjects(ctx)
	check(err)
	if len(projects.Projects) != 2 || projects.Common.DatasetID != "common-ds" || projects.Projects[0].Applied["agent"].Revision != 3 {
		t.Fatalf("API directory: %+v", projects)
	}
	typedProject := projects.Projects[0]
	desktopProject := directory.Projects[0]
	if typedProject.AutoCaptureUpdatedAt == "" || typedProject.AutoCaptureUpdatedAt != desktopProject.AutoCaptureUpdatedAt ||
		typedProject.AutoCaptureActor != "system" || desktopProject.AutoCaptureReason != "policy_resume" ||
		typedProject.FilteredCount != 1 || desktopProject.FilteredCount != 1 ||
		len(typedProject.RecentFiltered) != 1 || len(desktopProject.RecentFiltered) != 1 ||
		typedProject.RecentFiltered[0].OccurredAt != filter.OccurredAt || desktopProject.RecentFiltered[0].ID != filter.ID {
		t.Fatalf("capture metadata differs: %+v / %+v", typedProject, desktopProject)
	}
	var connections settings.ConnectionsDirectory
	check(control.RequestWithHeaders(ctx, "GET", "/v1/connections", nil, nil, &connections, 0))
	if len(connections.Connections) == 0 || connections.Connections[0].ID != "agent" || !connections.Connections[0].AgentRegistered {
		t.Fatalf("connections: %+v", connections)
	}

	zero, slots := 0.0, 1
	var save struct{ Result settings.GenerationRef }
	check(control.RequestWithHeaders(ctx, "PUT", "/v1/generation/profiles/provider", nil, map[string]any{
		"profile": settings.GenerationProfile{Mode: "provider", Provider: "openai", Model: "test", Endpoint: "https://provider.invalid/v1", Temperature: &zero, MaxConcurrentRequests: &slots},
		"api_key": key, "expected_revision": 0, "operation_id": "save-provider",
	}, &save, 0))
	if save.Result.ID != "provider" || save.Result.Revision != 1 {
		t.Fatal(save)
	}
	var receipt struct {
		Recorded bool
		Result   settings.GenerationRef
	}
	check(control.RequestWithHeaders(ctx, "GET", "/v1/generation/operations/save-provider", nil, nil, &receipt, 0))
	if !receipt.Recorded || receipt.Result != save.Result {
		t.Fatal(receipt)
	}
	profile, err := store.GetGenerationProfile(ctx, "provider", 1)
	check(err)
	_, err = store.PutGenerationProfile(ctx, domain.GenerationProfile{ProfileID: "another", Mode: "provider", Provider: "ollama", Model: "local", Endpoint: "http://localhost:11434"})
	check(err)
	check(store.SetActiveGeneration(ctx, "provider", 1))
	check(store.RecordGenerationValidation(ctx, domain.GenerationValidation{OperationID: "passed", ProfileID: "provider", ProfileRevision: 1,
		Digest: profile.Digest, RegisteredRevision: 1, Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true,
		ErrorCode: key, PayloadJSON: `{"extra":"wire-parity-bridge-token-private"}`}))
	var generation settings.GenerationDirectory
	check(control.RequestWithHeaders(ctx, "GET", "/v1/generation", nil, nil, &generation, 0))
	p := generation.Profiles["provider"][0].Profile
	if generation.Active == nil || *generation.Active != save.Result || len(generation.Profiles) != 2 || generation.OutputLanguage != "zh-CN" ||
		generation.ModelStatus == nil || generation.ModelStatus.ModelDigest != "live-digest" || !generation.HandoffAvailable ||
		p.Temperature == nil || *p.Temperature != 0 || p.MaxConcurrentRequests == nil || *p.MaxConcurrentRequests != slots || p.MaxOutputTokens != nil ||
		!p.CredentialConfigured || generation.Validations["passed"].Digest != profile.Digest {
		t.Fatal("desktop generation receipt differs")
	}
	typed, err := apiClient.GetGeneration(ctx)
	check(err)
	if len(typed.Profiles) != 2 || typed.Validations["passed"].ErrorCode != "" || typed.OutputLanguage != "zh-CN" {
		t.Fatal("API generation receipt differs")
	}
	for _, path := range []string{"/api/generation", "/v1/generation"} {
		req, err := http.NewRequestWithContext(ctx, "GET", gatewayURL+path, nil)
		check(err)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := httpClient.Do(req)
		check(err)
		raw, err := io.ReadAll(res.Body)
		check(err)
		check(res.Body.Close())
		if res.StatusCode != 200 || !json.Valid(raw) || strings.Contains(string(raw), key) || strings.Contains(string(raw), bridge) {
			t.Fatal("invalid or unsafe generation response")
		}
	}

	snapshot, err := desktop.overviewBootstrap(ctx, identity, 7, 10, 10, 10)
	check(err)
	if snapshot.Generation == 0 || snapshot.TotalKnowledgeCount != 2 || len(snapshot.DayBuckets) != 1 ||
		len(snapshot.RecentItems) != 2 || len(snapshot.RecallEvents) != 1 || len(snapshot.RecallEvents[0].Hits) != 1 {
		t.Fatalf("desktop overview: %+v", snapshot)
	}
	events, _, err := desktop.recallEvents(ctx, 10)
	check(err)
	recalls, err := desktop.knowledgeRecall(ctx, Item{ID: "lesson", Dataset: "project-ds"})
	check(err)
	if len(events) != 1 || events[0].Hits[0].Document != "lesson" || recalls.Count != 1 {
		t.Fatalf("recall receipts: %+v, %+v", events, recalls)
	}
	typedOverview, err := apiClient.OverviewBootstrap(ctx, api.OverviewOptions{})
	check(err)
	hydrated := overview.FromBootstrap(typedOverview, []shell.ProjectRef{{ID: "project", DatasetID: "project-ds"}})
	if typedOverview.Generation == 0 || hydrated.TotalKnowledge != 2 || hydrated.Buckets[day] != 2 || len(hydrated.Events) != 1 {
		t.Fatalf("API overview: %+v", hydrated)
	}
	knowledge, err := desktop.projectKnowledgeV2(ctx, identity, "project", "date", "", false, "", "", 10, "")
	check(err)
	if knowledge.Generation == 0 || knowledge.TotalFiltered != 1 || knowledge.Items[0].DocID != "lesson" || knowledge.Items[0].RecallCount != 1 {
		t.Fatalf("knowledge: %+v", knowledge)
	}
	common, err := desktop.commonKnowledgeV2(ctx, "common-ds", "experience", 10, "")
	check(err)
	if common.DatasetID != "common-ds" || common.TotalFiltered != 1 || common.Items[0].DocID != "shared" {
		t.Fatal(common)
	}
	body, err := desktop.documentBodyV2(ctx, "project-ds", "lesson")
	check(err)
	if body.ID != "lesson" || body.Body == nil || *body.Body != "Document text" || body.ContentHash != "body-hash" {
		t.Fatal(body)
	}
	typedBody, err := apiClient.GetKnowledgeDocumentBody(ctx, "project-ds", "lesson")
	check(err)
	if typedBody.DocumentID != "lesson" || typedBody.Content != "Document text" {
		t.Fatal(typedBody)
	}
	raw, err := desktop.rawRecordsV2(ctx, "project", "", 10, "")
	check(err)
	items, err := v2RawRecordsToItems("project-ds", raw)
	check(err)
	if raw.DatasetID != "project-ds" || !raw.CacheReady || len(items) != 2 {
		t.Fatal(raw)
	}
	children, err := desktop.sessionChildrenV2(ctx, "project", "root", "user", 10, "")
	check(err)
	if children.TotalCount != 1 || children.Items[0].ID != "child" {
		t.Fatal(children)
	}
	rawController := reading.RawRecordsController{Client: apiClient}
	rawPage, err := rawController.Page(ctx, "", api.RawRecordsOptions{ProjectID: "project"})
	check(err)
	if len(rawPage.Items) != 2 {
		t.Fatal(rawPage)
	}
	mark, err := desktop.readingStateMarkReadV2(ctx, v2ReadingStateMarkReadRequest{ReadingIdentity: identity, ProjectID: "project", DatasetID: "project-ds", DocIDs: []string{"lesson"}})
	check(err)
	state, err := apiClient.ReadingState(ctx, "project")
	check(err)
	if mark.Marked != 1 || len(state.Projects) != 1 || mark.ScopeHash != state.Projects[0].ScopeHash || mark.StateRev != state.Projects[0].StateRev {
		t.Fatal(mark, state)
	}
	recent, err := desktop.searchRecentV2(ctx, identity, 10, "", "")
	check(err)
	metadata, err := desktop.searchMetadataV2(ctx, identity, "needle", "knowledge", 10, "project", "")
	check(err)
	results, err := apiClient.Search(ctx, api.SearchRequest{Query: "needle", Filter: "knowledge"})
	check(err)
	if recent.Generation == 0 || len(recent.KnowledgeItems) != 2 || len(recent.SessionItems) != 1 ||
		metadata.Generation == 0 || len(metadata.Items) != 1 || len(search.MapSearchResponse(results, "needle", map[string]bool{"project-ds": true})) != 1 {
		t.Fatal(recent, metadata, results)
	}
	profileController := reading.ProfilesController{Client: apiClient}
	profileDirectory, err := profileController.List(ctx)
	check(err)
	if len(profileDirectory.Profiles) == 0 || profileDirectory.Prompt == "" {
		t.Fatal("profiles not decoded")
	}
	var monitor settings.ServiceMonitorSnapshot
	check(control.RequestWithHeaders(ctx, "GET", "/v1/runtime/monitor", nil, nil, &monitor, 0))
	typedMonitor, err := apiClient.RuntimeMonitor(ctx)
	check(err)
	if typedMonitor.CheckedAtISO == "" || typedMonitor.LastOKAtISO != typedMonitor.CheckedAtISO || typedMonitor.StatusSummary == "" ||
		typedMonitor.Summary.FilteredCount != 1 || len(typedMonitor.Summary.RecentFiltered) != 1 || typedMonitor.Summary.RecentFiltered[0].ID != filter.ID {
		t.Fatalf("monitor metadata: %+v", typedMonitor)
	}
	if monitor.State != "ready" || monitor.OwnerPID <= 0 || monitor.Summary == nil || monitor.Summary.ProjectsCount != 2 || !typedMonitor.Engine.OK ||
		monitor.CheckedAtISO == "" || monitor.StatusSummary != typedMonitor.StatusSummary ||
		monitor.Summary.FilteredCount != 1 || len(monitor.Summary.RecentFiltered) != 1 ||
		monitor.Summary.RecentFiltered[0].ID != filter.ID {
		t.Fatal(monitor, typedMonitor)
	}
}

// wireSessionEngine is a pure engine-agnostic wire stub supplying pre-cleaned
// session labels and history fixtures so this test focuses strictly on
// core/gateway JSON wire -> app/api & app/settings decoding -> MyGo ui.NewTester
// rendering parity. Real Cognee label cleaning (cleanSessionLabel), cache
// hydration (HydrateSessionsFromCache), and relation projection
// (SyncCatalog / applySessionRelations) are covered in
// core/engine/cognee/contract_test.go (arch_guard_test.go forbids importing
// core/engine/cognee from app/).
type wireSessionEngine struct {
	wireEngine
	histories map[engine.SessionRef]engine.SessionHistoryResult
}

var _ engine.SessionHistoryReader = (*wireSessionEngine)(nil)

func (e *wireSessionEngine) HydrateSessionsFromCache(_ context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	out := make(map[engine.SessionRef]engine.SessionCacheInfo, len(refs))
	for _, ref := range refs {
		h := e.histories[ref]
		out[ref] = engine.SessionCacheInfo{
			Label:     h.Label,
			MsgCount:  len(h.QAs),
			ToolCalls: len(h.Traces),
		}
	}
	return out, nil
}

func (e *wireSessionEngine) ReadSessionHistory(_ context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	h := e.histories[engine.SessionRef{UserID: req.UserID, SessionID: req.SessionID}]
	res := engine.SessionHistoryResult{
		Label:     h.Label,
		MsgCount:  len(h.QAs),
		ToolCalls: len(h.Traces),
		QAs:       []engine.SessionQA{},
		Traces:    []engine.SessionTrace{},
	}
	if req.QAOffset < len(h.QAs) {
		end := min(len(h.QAs), req.QAOffset+limit)
		res.QAs = append(res.QAs, h.QAs[req.QAOffset:end]...)
	}
	if req.TraceOffset < len(h.Traces) {
		end := min(len(h.Traces), req.TraceOffset+limit)
		res.Traces = append(res.Traces, h.Traces[req.TraceOffset:end]...)
	}
	return res, nil
}

func TestGatewayWireParityRawRecordsSubagentsAndLabelCleanup(t *testing.T) {
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
		token     = "wire-parity-subagent-management-token"
		projectID = "memlane"
		datasetID = "memlane-ds"
		userID    = "owner-user-1"
		parentSID = "antigravity-parent-1"
		verifSID  = "antigravity-verifier-1"
		monSID    = "antigravity-monitor-1"
		orphanSID = "antigravity-orphan-1"
	)
	tokenFile := filepath.Join(dir, "management.token")
	check(os.WriteFile(tokenFile, []byte(token), 0o600))

	check(store.UpsertProject(ctx, domain.Project{
		ID: projectID, Name: "memlane", DatasetID: datasetID, DatasetName: "memlane",
		Aliases: []string{filepath.Join(dir, "memlane")}, Status: "verified", DesiredRevision: 1, AutoCapture: true,
	}))
	check(store.UpsertAgent(ctx, domain.Agent{
		ID: "antigravity-1", Kind: "antigravity", Name: "Antigravity", DefaultAccess: domain.AccessMaintain,
	}))

	// Seed projected catalog_sessions directly in SQLite (parent + 2 linked Swarm
	// workers + 1 orphan subagent); real SyncCatalog/applySessionRelations
	// projection from session_relations is tested in core/engine/cognee/contract_test.go.
	baseTime := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, row := range []domain.CatalogSession{
		{
			DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: parentSID, Status: "active",
			StartedAt: domain.FormatUTCNS(baseTime), LastActivityAt: domain.FormatUTCNS(baseTime.Add(4 * time.Minute)),
			SortTSDescKey: -baseTime.Add(4 * time.Minute).UnixNano(), IsTopLevel: true, ChildCount: 2,
		},
		{
			DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: verifSID, Status: "active",
			StartedAt: domain.FormatUTCNS(baseTime.Add(time.Minute)), LastActivityAt: domain.FormatUTCNS(baseTime.Add(3 * time.Minute)),
			SortTSDescKey: -baseTime.Add(3 * time.Minute).UnixNano(),
			ParentSessionID: parentSID, RootSessionID: parentSID, SubagentRole: "verifier", NestingDepth: 1, IsTopLevel: false,
		},
		{
			DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: monSID, Status: "active",
			StartedAt: domain.FormatUTCNS(baseTime.Add(2 * time.Minute)), LastActivityAt: domain.FormatUTCNS(baseTime.Add(2 * time.Minute)),
			SortTSDescKey: -baseTime.Add(2 * time.Minute).UnixNano(),
			ParentSessionID: parentSID, RootSessionID: parentSID, SubagentRole: "monitor", NestingDepth: 1, IsTopLevel: false,
		},
		{
			DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: orphanSID, Status: "active",
			StartedAt: domain.FormatUTCNS(baseTime), LastActivityAt: domain.FormatUTCNS(baseTime.Add(time.Minute)),
			SortTSDescKey: -baseTime.Add(time.Minute).UnixNano(),
			ParentSessionID: "missing-parent-999", RootSessionID: "missing-parent-999", SubagentRole: "explorer", NestingDepth: 1, IsTopLevel: true,
		},
	} {
		check(store.UpsertCatalogSession(ctx, row))
	}
	check(store.UpsertCatalogSyncState(ctx, domain.CatalogSyncState{DatasetID: datasetID, CatalogVersion: "wire-sync-1"}))

	makeTraces := func(sid string, tools []string) []engine.SessionTrace {
		out := make([]engine.SessionTrace, 0, len(tools))
		for i, tool := range tools {
			out = append(out, engine.SessionTrace{
				ID:     fmt.Sprintf("%s-t%d", sid, i+1),
				Name:   tool,
				Status: "success",
				Params: json.RawMessage(fmt.Sprintf(`{"step":%d}`, i+1)),
				Result: json.RawMessage(`"ok"`),
			})
		}
		return out
	}

	eng := &wireSessionEngine{
		histories: map[engine.SessionRef]engine.SessionHistoryResult{
			{UserID: userID, SessionID: parentSID}: {
				Label: "/swarm 修复问题",
				QAs: []engine.SessionQA{{
					ID:       "qa-parent-1",
					Question: "/swarm 修复问题",
					Answer:   "已完成修复并验证通过",
				}},
			},
			{UserID: userID, SessionID: verifSID}: {
				Traces: makeTraces(verifSID, []string{"Read", "Grep", "Bash", "Edit", "Bash"}),
			},
			{UserID: userID, SessionID: monSID}: {
				Traces: makeTraces(monSID, []string{"Read", "Bash", "Bash"}),
			},
			{UserID: userID, SessionID: orphanSID}: {
				Traces: makeTraces(orphanSID, []string{"Glob"}),
			},
		},
	}

	server, err := gateway.NewServer(gateway.Config{
		Store: store, Engine: eng, ManagementToken: token,
	})
	check(err)

	const gatewayURL = "http://127.0.0.1:61256"
	httpClient := &http.Client{Transport: queueTestTransport(server.Handler().ServeHTTP)}
	control := ProjectControlClient{
		Config: settings.ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
		HTTP:   httpClient,
	}

	// 1. Fetch through real app client control.rawPage -> GET /api/raw-records.
	rawResp, err := control.rawPage(ctx, projectID, "all", "")
	check(err)
	items, err := v2RawRecordsToItems(datasetID, rawResp)
	check(err)
	if len(items) != 2 {
		t.Fatalf("expected 2 top-level sessions (parent + orphan, hiding 2 swarm workers), got %d: %+v", len(items), items)
	}

	var parentRaw *v2RawSessionEntry
	for _, entry := range rawResp.Items {
		if entry.Session != nil && entry.Session.ID == parentSID {
			parentRaw = entry.Session
		}
	}
	if parentRaw == nil {
		t.Fatalf("parent session %s missing from /api/raw-records response: %+v", parentSID, rawResp.Items)
	}
	if parentRaw.Label != "/swarm 修复问题" || strings.Contains(parentRaw.Label, "<USER_REQUEST>") {
		t.Fatalf("parent raw label = %q, want clean \"/swarm 修复问题\"", parentRaw.Label)
	}
	if parentRaw.MsgCount < 1 {
		t.Fatalf("parent MsgCount = %d, want >= 1", parentRaw.MsgCount)
	}

	var parentItem, orphanItem *Item
	for i := range items {
		switch items[i].ID {
		case parentSID:
			parentItem = &items[i]
		case orphanSID:
			orphanItem = &items[i]
		default:
			t.Fatalf("unexpected top-level session %q (Swarm workers should be hidden under parent)", items[i].ID)
		}
	}
	if parentItem == nil || orphanItem == nil {
		t.Fatalf("missing parent or orphan item: %+v", items)
	}
	if parentItem.Title != "/swarm 修复问题" || strings.Contains(parentItem.Title, "<USER_REQUEST>") {
		t.Fatalf("parent Item.Title = %q, want \"/swarm 修复问题\"", parentItem.Title)
	}
	if parentItem.SubagentTotalCount != 2 || len(parentItem.Subagents) != 2 {
		t.Fatalf("parent subagents = total %d len %d, want 2/2 (populated from children_preview): %+v",
			parentItem.SubagentTotalCount, len(parentItem.Subagents), parentItem.Subagents)
	}
	roles := map[string]bool{}
	for _, sub := range parentItem.Subagents {
		if sub.Session == nil || !sub.Session.IsSubagent {
			t.Fatalf("preview child missing IsSubagent: %+v", sub)
		}
		roles[sub.Session.SubagentRole] = true
	}
	if !roles["verifier"] || !roles["monitor"] {
		t.Fatalf("expected child roles verifier & monitor, got %+v", roles)
	}
	if orphanItem.Session == nil || !orphanItem.Session.IsSubagent || orphanItem.Session.SubagentRole != "explorer" {
		t.Fatalf("orphan subagent lost IsSubagent/role: %+v", orphanItem.Session)
	}

	// 2. Fetch via control.rawSessionHistory (/v1/raw-records/session-history) AND direct /api/raw-records/session-history.
	histPage, err := control.rawSessionHistory(ctx, *parentItem, 0, 0)
	check(err)
	if histPage.Label != "/swarm 修复问题" || strings.Contains(histPage.Label, "<USER_REQUEST>") || histPage.Count != 1 {
		t.Fatalf("rawSessionHistory label/count = %q/%d", histPage.Label, histPage.Count)
	}
	if len(histPage.Subagents) != 2 {
		t.Fatalf("rawSessionHistory Subagents = %+v, want 2", histPage.Subagents)
	}
	histRoles := map[string]int{}
	for _, sub := range histPage.Subagents {
		histRoles[sub.SubagentRole] = sub.ToolCalls
	}
	if histRoles["verifier"] != 5 || histRoles["monitor"] != 3 {
		t.Fatalf("rawSessionHistory subagent roles/tool_calls = %+v, want verifier=5 monitor=3", histRoles)
	}

	var apiHist SessionHistoryPage
	check(control.request(ctx, "GET",
		fmt.Sprintf("/api/raw-records/session-history?dataset_id=%s&session_id=%s&user_id=%s&limit=20", datasetID, parentSID, userID),
		nil, &apiHist))
	if apiHist.Label != "/swarm 修复问题" || len(apiHist.Subagents) != 2 {
		t.Fatalf("/api/raw-records/session-history = %+v", apiHist)
	}

	// 3. Fetch via /v1/projects/{id}/raw-records/sessions/{sid}/children.
	children, err := control.sessionChildrenV2(ctx, projectID, parentSID, userID, 10, "")
	check(err)
	if children.TotalCount != 2 || len(children.Items) != 2 {
		t.Fatalf("sessionChildrenV2 = %+v, want 2", children)
	}
	childTools := map[string]int{}
	for _, ch := range children.Items {
		childTools[ch.SubagentRole] = ch.ToolCalls
	}
	if childTools["verifier"] != 5 || childTools["monitor"] != 3 {
		t.Fatalf("sessionChildrenV2 roles/tools = %+v, want verifier=5 monitor=3", childTools)
	}

	// 4. Render parent session in real MyGo App via ui.NewTester -> verify "子会话 (2)" tab.
	a := New(Config{
		ServiceURL:     gatewayURL,
		ProjectControl: &ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
		Projects:       []Project{{ID: projectID, Name: "memlane", Path: filepath.Join(dir, "memlane"), Dataset: datasetID}},
	}, nil)
	a.Client.HTTP = httpClient
	a.syncSettingsTransport()
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.monitor.EngineStatus = "已连接"
	updates := make(chan func(), 64)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })

	a.navigateKind("知识", 0, "原始记录")
	s := a.state()
	drainReading(t, updates, func() bool {
		return s.Loaded && !s.Loading && !s.Reading && s.Selected != nil && s.SourcePage != nil
	})
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	if len(s.Items) != 2 {
		t.Fatalf("UI raw list len = %d, want 2", len(s.Items))
	}
	if s.Selected == nil || s.Selected.ID != parentSID || s.Selected.Title != "/swarm 修复问题" || strings.Contains(s.Selected.Title, "<USER_REQUEST>") {
		t.Fatalf("UI selected parent session unexpected: %+v", s.Selected)
	}
	if !tt.HasText("/swarm 修复问题") || !tt.HasText("含 2 子会话") || !tt.HasText("子会话 · explorer") {
		t.Fatalf("UI missing clean parent label or subagent badges: %v", tt.Texts())
	}
	if !tt.HasText("子会话 (2)") {
		t.Fatalf("UI missing 子会话 (2) tab on parent session: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "子会话 (2)")
	if !s.SessionSubagents || !tt.HasText("已保存 0 轮问答 · 5 条工具记录") || !tt.HasText("已保存 0 轮问答 · 3 条工具记录") {
		t.Fatalf("UI subagents tab missing verifier(5) / monitor(3) cards: %v", tt.Texts())
	}
}

func TestGatewayWireParityProjectAndCommonACLMatrix(t *testing.T) {
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

	const token = "wire-parity-acl-matrix-management-token"
	tokenFile := filepath.Join(dir, "management.token")
	check(os.WriteFile(tokenFile, []byte(token), 0o600))

	defaults, err := store.GetRegistryDefaults(ctx)
	check(err)
	defaults.Access = domain.AccessMaintain
	defaults.Capture = true
	defaults.AutoProcess = true
	check(store.UpsertRegistryDefaults(ctx, defaults))

	// Seed projects BEFORE agents so UpsertProject does not auto-insert synthetic 'none' overrides,
	// exercising genuine defaults + agent.default_access + protected-project evaluation.
	check(store.UpsertProject(ctx, domain.Project{
		ID: "memlane", Name: "memlane", DatasetID: "memlane-ds", DatasetName: "memlane",
		Aliases: []string{filepath.Join(dir, "memlane")}, Status: "verified", DesiredRevision: 1, AutoCapture: true,
	}))
	check(store.UpsertProject(ctx, domain.Project{
		ID: "ml-user-profiles", Name: "ml-user-profiles", DatasetID: "profiles-ds", DatasetName: "ml-user-profiles",
		Status: "verified", DesiredRevision: 1, AutoCapture: false,
	}))
	check(store.UpsertCommonConfig(ctx, domain.CommonConfig{
		ID: "common", Name: "Common", DatasetID: "common-ds", Status: "verified", DesiredRevision: 1,
	}))

	check(store.UpsertAgent(ctx, domain.Agent{ID: "antigravity-1", Kind: "antigravity", Name: "Antigravity", DefaultAccess: domain.AccessMaintain}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: "trae-cn-1", Kind: "trae_ide", Name: "TRAE CN", DefaultAccess: domain.AccessNone}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: "codex-1", Kind: "codex", Name: "Codex", DefaultAccess: domain.AccessMaintain}))
	check(store.UpsertAgent(ctx, domain.Agent{ID: "trae-cli-1", Kind: "traex", Name: "TRAE CLI", DefaultAccess: domain.AccessNone}))

	read := domain.AccessRead
	none := domain.AccessNone
	maintain := domain.AccessMaintain
	check(store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "memlane", AgentID: "codex-1", Access: &read}))
	check(store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "common", TargetID: "common", AgentID: "codex-1", Access: &none}))
	check(store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "memlane", AgentID: "trae-cli-1", Access: &maintain}))

	server, err := gateway.NewServer(gateway.Config{
		Store: store, Engine: wireEngine{}, ManagementToken: token,
	})
	check(err)

	const gatewayURL = "http://127.0.0.1:61257"
	httpClient := &http.Client{Transport: queueTestTransport(server.Handler().ServeHTTP)}
	settingsClient := settings.ProjectControlClient{
		Config: settings.ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
		HTTP:   httpClient,
	}

	// Load directory through real app/settings client hitting GET /api/projects.
	var directory settings.AccessDirectory
	check(settingsClient.RequestWithHeaders(ctx, "GET", "/api/projects", nil, nil, &directory, 0))

	if directory.Defaults.Access != "maintain" || !directory.Defaults.Capture || !directory.Defaults.AutoProcess {
		t.Fatalf("unexpected defaults from /api/projects: %+v", directory.Defaults)
	}
	if len(directory.Agents) != 4 || len(directory.Projects) != 2 || directory.Common == nil {
		t.Fatalf("unexpected directory cardinality: agents=%d projects=%d common=%v",
			len(directory.Agents), len(directory.Projects), directory.Common != nil)
	}

	// Verify 1:1 exact parity across every project x every active agent.
	wantProjectAccess := map[string]map[string]string{
		"memlane": {
			"antigravity-1": "maintain",
			"trae-cn-1":     "none",
			"codex-1":       "read",
			"trae-cli-1":    "maintain",
		},
		"ml-user-profiles": {
			"antigravity-1": "none",
			"trae-cn-1":     "none",
			"codex-1":       "none",
			"trae-cli-1":    "none",
		},
	}
	for _, project := range directory.Projects {
		if project.ID == "memlane" {
			if _, hasOverride := project.Overrides["antigravity-1"]; hasOverride {
				t.Fatalf("expected antigravity-1 to have NO memlane override, got %+v", project.Overrides["antigravity-1"])
			}
			if _, hasOverride := project.Overrides["trae-cn-1"]; hasOverride {
				t.Fatalf("expected trae-cn-1 to have NO memlane override, got %+v", project.Overrides["trae-cn-1"])
			}
		}
		for _, agent := range directory.Agents {
			uiPolicy := project.EffectivePolicy(directory.Defaults, agent)
			backendACL, err := store.EvaluateProjectACL(ctx, project.ID, agent.ID)
			check(err)
			if uiPolicy.Access != string(backendACL.Access) ||
				uiPolicy.Capture != backendACL.Capture ||
				uiPolicy.AutoProcess != backendACL.AutoProcess {
				t.Fatalf("project=%s agent=%s ACL mismatch: UI=%+v backend=(access=%s capture=%v auto_process=%v)",
					project.ID, agent.ID, uiPolicy, backendACL.Access, backendACL.Capture, backendACL.AutoProcess)
			}
			if want := wantProjectAccess[project.ID][agent.ID]; uiPolicy.Access != want {
				t.Fatalf("project=%s agent=%s access=%q, want %q", project.ID, agent.ID, uiPolicy.Access, want)
			}
		}
	}

	// Verify 1:1 exact parity across common x every active agent.
	wantCommonAccess := map[string]string{
		"antigravity-1": "read",
		"trae-cn-1":     "none",
		"codex-1":       "none",
		"trae-cli-1":    "none",
	}
	for _, agent := range directory.Agents {
		uiCommon := settings.CommonAgentAccessForAgent(*directory.Common, directory.Defaults, agent)
		backendCommon, err := store.EvaluateCommonACL(ctx, agent.ID)
		check(err)
		if uiCommon != string(backendCommon.Access) {
			t.Fatalf("common agent=%s ACL mismatch: UI=%q backend=%q", agent.ID, uiCommon, backendCommon.Access)
		}
		if want := wantCommonAccess[agent.ID]; uiCommon != want {
			t.Fatalf("common agent=%s access=%q, want %q", agent.ID, uiCommon, want)
		}
	}

	// Render memlane management page in real MyGo App via ui.NewTester and assert expected labels.
	a := New(Config{
		ServiceURL:     gatewayURL,
		ProjectControl: &ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
	}, nil)
	a.Client.HTTP = httpClient
	a.syncSettingsTransport()
	a.applyAccessDirectory(directory)
	a.access.Next = time.Now().Add(time.Hour)
	a.monitor.EngineStatus = "已连接"
	updates := make(chan func(), 64)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })

	if len(a.Config.Projects) != 1 || a.Config.Projects[0].ID != "memlane" {
		t.Fatalf("expected protected ml-user-profiles filtered out leaving only memlane, got %+v", a.Config.Projects)
	}
	a.openProjectSettings(0)
	// Use tall viewport so all 4 agent permission cards in the management page render without scroll clipping.
	tt := ui.NewTester(a.View, 1200, 1400)
	tt.Frame()

	for _, wantText := range []string{
		"Antigravity", "TRAE CN", "Codex", "TRAE CLI",
		"读写", "只读", "不可访问",
		"读取、写入并维护该项目记忆（含删除）",
		"仅允许检索与读取已有记忆，不可写入",
		"禁止访问此项目的记忆与原始记录",
	} {
		if !tt.HasText(wantText) {
			t.Fatalf("memlane management page missing expected text %q; texts=%v", wantText, tt.Texts())
		}
	}
	// Verify each rendered agent permission card matches its backend ACL tier in order.
	texts := tt.Texts()
	wantCardLabels := []struct {
		name, desc, tier string
	}{
		{"Antigravity", "读取、写入并维护该项目记忆（含删除）", "读写"},
		{"TRAE CN", "禁止访问此项目的记忆与原始记录", "不可访问"},
		{"Codex", "仅允许检索与读取已有记忆，不可写入", "只读"},
		{"TRAE CLI", "读取、写入并维护该项目记忆（含删除）", "读写"},
	}
	for _, card := range wantCardLabels {
		idx := -1
		for i, txt := range texts {
			if txt == card.name {
				idx = i
				break
			}
		}
		if idx < 0 || idx+2 >= len(texts) {
			t.Fatalf("agent card %q not found cleanly in rendered texts: %v", card.name, texts)
		}
		window := texts[idx : min(len(texts), idx+5)]
		foundDesc, foundTier := false, false
		for _, txt := range window {
			if txt == card.desc {
				foundDesc = true
			}
			if txt == card.tier {
				foundTier = true
			}
		}
		if !foundDesc || !foundTier {
			t.Fatalf("agent card %q window %v missing expected desc=%q tier=%q", card.name, window, card.desc, card.tier)
		}
	}
}

type timeoutRetryEngine struct {
	wireEngine
	ingestCalls  atomic.Int32
	extractCalls atomic.Int32
}

func (e *timeoutRetryEngine) Ingest(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
	n := e.ingestCalls.Add(1)
	if n > 1 {
		return engine.IngestResult{}, fmt.Errorf("duplicate ingest call %d: %w", n, engine.ErrConflict)
	}
	id := fmt.Sprintf("minted-entry-%d", n)
	return engine.IngestResult{OK: true, EntryID: id}, nil
}

func (e *timeoutRetryEngine) Extract(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
	if e.extractCalls.Add(1) == 1 {
		return engine.ExtractResult{}, &engine.EngineError{
			StatusCode: http.StatusGatewayTimeout,
			Code:       "extract_timeout",
			Message:    "engine request timed out",
			Err:        errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
		}
	}
	return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
}

func TestGatewayWireParityInboxTimeoutAndRetryRecovery(t *testing.T) {
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
		token     = "wire-parity-inbox-retry-management-token"
		agentKey  = "wire-parity-inbox-retry-agent-secret-key"
		projectID = "memlane"
	)
	check(store.UpsertProject(ctx, domain.Project{
		ID: projectID, Name: "memlane", DatasetID: "memlane-ds", DatasetName: "memlane",
		Aliases: []string{filepath.Join(dir, "memlane")}, Status: "verified", DesiredRevision: 1, AutoCapture: true,
	}))
	check(store.UpsertAgent(ctx, domain.Agent{
		ID: "antigravity-1", Kind: "antigravity", Name: "Antigravity", DefaultAccess: domain.AccessMaintain,
	}))

	eng := &timeoutRetryEngine{}
	server, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: token,
		AgentTokens:     map[string]string{"antigravity-1": agentKey},
	})
	check(err)

	const gatewayURL = "http://127.0.0.1:61258"
	httpClient := &http.Client{Transport: queueTestTransport(server.Handler().ServeHTTP)}
	apiClient, err := api.NewClient(api.Config{
		BaseURL: gatewayURL, ManagementToken: token, HTTPClient: httpClient,
	})
	check(err)

	// 1. Enqueue capture_qa via real POST /ingest authenticated with authorized agent key.
	ingestBody := `{"project_id":"memlane","session_id":"antigravity-retry-1","message_id":"msg-1","kind":"capture_qa","content":{"question":"修复重试冲突","answer":"跳过重复 ingest"}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayURL+"/ingest", bytes.NewBufferString(ingestBody))
	check(err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", agentKey)
	res, err := httpClient.Do(req)
	check(err)
	rawIngest, err := io.ReadAll(res.Body)
	check(err)
	check(res.Body.Close())
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /ingest status = %d, want 202; body=%s", res.StatusCode, string(rawIngest))
	}

	// 2. Process attempt 1: Ingest succeeds (receipt saved), Extract fails with extract_timeout.
	worked, err := server.Inbox().ProcessOnce(ctx)
	check(err)
	if !worked {
		t.Fatal("attempt 1 did not claim queued inbox item")
	}
	if got := eng.ingestCalls.Load(); got != 1 {
		t.Fatalf("attempt 1 ingestCalls = %d, want 1", got)
	}
	if got := eng.extractCalls.Load(); got != 1 {
		t.Fatalf("attempt 1 extractCalls = %d, want 1", got)
	}

	// Verify GET /api/projects/{id} reports extract_timeout with "超时" summary (NOT "提炼服务暂时无法连接").
	projFailed, err := apiClient.GetProject(ctx, projectID)
	check(err)
	if projFailed.Inbox.Failed != 1 || len(projFailed.Inbox.RecentFailures) != 1 {
		t.Fatalf("expected inbox.failed == 1 after attempt 1, got %+v", projFailed.Inbox)
	}
	failure := projFailed.Inbox.RecentFailures[0]
	if failure.ErrorCode != "extract_timeout" {
		t.Fatalf("failure.ErrorCode = %q, want \"extract_timeout\"", failure.ErrorCode)
	}
	if !strings.Contains(failure.Summary, "超时") || strings.Contains(failure.Summary, "提炼服务暂时无法连接") {
		t.Fatalf("failure.Summary = %q, want contains \"超时\" and NOT \"提炼服务暂时无法连接\"", failure.Summary)
	}

	// 3. Call real POST /api/projects/{id}/retry and process attempt 2.
	retryResp, err := apiClient.RetryProject(ctx, projectID)
	check(err)
	if retryResp.RetriedCount != 1 {
		t.Fatalf("POST /api/projects/%s/retry retried_count = %d, want 1", projectID, retryResp.RetriedCount)
	}

	worked, err = server.Inbox().ProcessOnce(ctx)
	check(err)
	if !worked {
		t.Fatal("attempt 2 did not claim retried inbox item")
	}

	// Verify GET /api/projects/{id} reports failed==0, succeeded==1, zero conflict, and ingestCalls==1.
	projRecovered, err := apiClient.GetProject(ctx, projectID)
	check(err)
	if projRecovered.Inbox.Failed != 0 || projRecovered.Inbox.Succeeded != 1 || len(projRecovered.Inbox.RecentFailures) != 0 {
		t.Fatalf("unexpected inbox state after retry recovery: %+v", projRecovered.Inbox)
	}
	if strings.Contains(strings.ToLower(projRecovered.LastError), "conflict") || projRecovered.LastError != "" {
		t.Fatalf("unexpected LastError after retry recovery: %q", projRecovered.LastError)
	}
	if got := eng.ingestCalls.Load(); got != 1 {
		t.Fatalf("retry re-ingested item: ingestCalls = %d, want 1", got)
	}
	if got := eng.extractCalls.Load(); got != 2 {
		t.Fatalf("extractCalls = %d, want 2", got)
	}
}

type wireGateFunc func(context.Context) (*completion.ActiveProfile, error)

func (f wireGateFunc) ResolveActive(ctx context.Context) (*completion.ActiveProfile, error) {
	return f(ctx)
}

func TestGatewayWireParityCompletionHealthMonitorAndUI(t *testing.T) {
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
		token       = "wire-parity-completion-health-mgmt-token"
		officialURL = "http://127.0.0.1:61258"
		gatewayURL  = "http://127.0.0.1:61259"
		staleURL    = "http://127.0.0.1:18173"
	)
	tokenFile := filepath.Join(dir, "management.token")
	check(os.WriteFile(tokenFile, []byte(token), 0o600))

	authHome := filepath.Join(dir, "codex-home")
	check(os.MkdirAll(authHome, 0o700))
	cliPath := filepath.Join(authHome, "codex-cli")
	check(os.WriteFile(cliPath, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	check(os.WriteFile(filepath.Join(authHome, "auth.json"), []byte("{}"), 0o600))
	check(os.WriteFile(filepath.Join(authHome, "config.toml"), []byte(`model = "gpt-wire"`), 0o600))

	readyProfile := &completion.ActiveProfile{
		Mode:       "agent_bridge",
		BridgeKind: "codex",
		Model:      "gpt-wire",
		Executable: cliPath,
		AuthHome:   authHome,
		Defaults:   &agents.ModelDefaults{Model: "gpt-wire", Provider: "openai", Source: "agent_config"},
	}
	var active atomic.Pointer[completion.ActiveProfile]
	active.Store(readyProfile)

	compSrv, err := completion.NewServer(completion.Config{
		Token: "wire-parity-completion-bearer-token-2026",
		Gate:  wireGateFunc(func(context.Context) (*completion.ActiveProfile, error) { return active.Load(), nil }),
	})
	check(err)

	gwSrv, err := gateway.NewServer(gateway.Config{
		Store:             store,
		Engine:            wireEngine{},
		CompletionHandler: compSrv.Handler(),
		ManagementToken:   token,
	})
	check(err)

	var activeGateway atomic.Pointer[http.Handler]
	gwHandler := gwSrv.Handler()
	activeGateway.Store(&gwHandler)

	var deadPortHits atomic.Int32
	httpClient := &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "127.0.0.1:61258":
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			http.NotFound(w, r)
		case "127.0.0.1:61259":
			(*activeGateway.Load()).ServeHTTP(w, r)
		case "127.0.0.1:18173":
			deadPortHits.Add(1)
			http.Error(w, "connection refused", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	})}

	a := New(Config{
		ServiceURL:     officialURL,
		CompletionURL:  staleURL,
		ProjectControl: &ProjectControlConfig{URL: gatewayURL, ManagementTokenFile: tokenFile},
	}, nil)
	a.Client.HTTP = httpClient
	a.syncSettingsTransport()
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })

	poll := func() {
		t.Helper()
		a.pollServiceMonitor(true)
		drainReading(t, updates, func() bool { return !a.monitor.Polling })
	}
	renderOverview := func() *ui.Tester {
		return ui.NewTester(func(c *ui.Context) {
			ui.Column(c).FillWidth().Padding(24).Children(func() { a.settingsController.ViewServiceOverview(c) })
		}, 1000, 700)
	}

	// 1. Active agent_bridge -> ok / 已连接, displays gatewayURL (never stale :18173).
	poll()
	if deadPortHits.Load() != 0 {
		t.Fatalf("probed stale port :18173 (%d times) instead of gateway /v1/completion/health", deadPortHits.Load())
	}
	if dot, metric, col := a.trayChannelStatus(); dot != "ok" || !strings.HasPrefix(metric, "已连接") || col != tok.Text2 {
		t.Fatalf("active agent_bridge trayChannelStatus = (%q, %q, %v), want ok/已连接", dot, metric, col)
	}
	ttReady := renderOverview()
	if !ttReady.HasText("Agent 模型代理") || !ttReady.HasText("已连接") || !ttReady.HasText(gatewayURL) {
		t.Fatalf("active agent_bridge overview missing expected gateway endpoint/status: %v", ttReady.Texts())
	}
	if ttReady.HasText("18173") || ttReady.HasText("待检查") {
		t.Fatalf("active agent_bridge overview leaked stale :18173 or 待检查: %v", ttReady.Texts())
	}
	a.servicePopover.PopoverOpen = true
	ttPop := ui.NewTester(a.View, 1200, 820)
	if !ttPop.HasText(gatewayURL) || ttPop.HasText("18173") {
		t.Fatalf("service popover should show %s instead of :18173: %v", gatewayURL, ttPop.Texts())
	}
	a.servicePopover.PopoverOpen = false

	// 2. Direct provider (503 bridge_inactive) -> neutral / 未启用 (NOT warn / 待检查).
	inactiveProfile := *readyProfile
	inactiveProfile.Mode = "provider"
	active.Store(&inactiveProfile)
	poll()
	if a.monitor.CompletionStatus != settings.CompletionInactiveStatus {
		t.Fatalf("direct provider CompletionStatus = %q, want %q", a.monitor.CompletionStatus, settings.CompletionInactiveStatus)
	}
	if dot, metric, col := a.trayChannelStatus(); dot != "neutral" || metric != "未启用" || col != tok.Text3 {
		t.Fatalf("direct provider trayChannelStatus = (%q, %q, %v), want neutral/未启用/Text3", dot, metric, col)
	}
	ttInactive := renderOverview()
	if !ttInactive.HasText("未启用") || !ttInactive.HasText(gatewayURL) || ttInactive.HasText("18173") || ttInactive.HasText("待检查") {
		t.Fatalf("direct provider overview unexpected texts: %v", ttInactive.Texts())
	}

	// 3. Profile mismatch (409 bridge_profile_mismatch) -> warn / 待检查.
	mismatchProfile := *readyProfile
	mismatchProfile.Model = "wrong-model"
	active.Store(&mismatchProfile)
	poll()
	if dot, metric, col := a.trayChannelStatus(); dot != "warn" || metric != "待检查" || col != tok.Warning {
		t.Fatalf("mismatch trayChannelStatus = (%q, %q, %v), want warn/待检查/Warning", dot, metric, col)
	}
	ttMismatch := renderOverview()
	if !ttMismatch.HasText("待检查") || !ttMismatch.HasText(gatewayURL) || ttMismatch.HasText("18173") {
		t.Fatalf("mismatch overview unexpected texts: %v", ttMismatch.Texts())
	}

	// 4. CompletionHandler == nil (404 not_configured) -> neutral / 未配置 across all surfaces, zero probe to :18173.
	unconfiguredSrv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          wireEngine{},
		ManagementToken: token,
	})
	check(err)
	unconfiguredHandler := unconfiguredSrv.Handler()
	activeGateway.Store(&unconfiguredHandler)
	poll()
	if a.monitor.CompletionStatus != "未配置" || a.monitor.CompletionEndpoint != "" {
		t.Fatalf("not_configured state = (%q, %q), want (未配置, \"\")", a.monitor.CompletionStatus, a.monitor.CompletionEndpoint)
	}
	if dot, metric, col := a.trayChannelStatus(); dot != "neutral" || metric != "未配置" || col != tok.Text3 {
		t.Fatalf("not_configured trayChannelStatus = (%q, %q, %v), want neutral/未配置/Text3", dot, metric, col)
	}
	ttUnconfigured := renderOverview()
	if !ttUnconfigured.HasText("未配置") || ttUnconfigured.HasText("待检查") {
		t.Fatalf("not_configured overview unexpected texts: %v", ttUnconfigured.Texts())
	}
	a.servicePopover.PopoverOpen = true
	ttUnconfiguredPop := ui.NewTester(a.View, 1200, 820)
	if !ttUnconfiguredPop.HasText("未配置") || ttUnconfiguredPop.HasText("待检查") {
		t.Fatalf("not_configured popover unexpected texts: %v", ttUnconfiguredPop.Texts())
	}
	a.servicePopover.PopoverOpen = false

	// 5. Generic 503 (non-bridge_inactive) -> warn / 待检查 (must NOT be misclassified as 未启用).
	generic503Srv, err := gateway.NewServer(gateway.Config{
		Store:  store,
		Engine: wireEngine{},
		CompletionHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"service_unavailable","message":"overloaded"}}`))
		}),
		ManagementToken: token,
	})
	check(err)
	generic503Handler := generic503Srv.Handler()
	activeGateway.Store(&generic503Handler)
	poll()
	if a.monitor.CompletionStatus == settings.CompletionInactiveStatus {
		t.Fatalf("generic 503 misclassified as CompletionInactiveStatus: %q", a.monitor.CompletionStatus)
	}
	if dot, metric, col := a.trayChannelStatus(); dot != "warn" || metric != "待检查" || col != tok.Warning {
		t.Fatalf("generic 503 trayChannelStatus = (%q, %q, %v), want warn/待检查/Warning", dot, metric, col)
	}
	ttGeneric503 := renderOverview()
	if !ttGeneric503.HasText("待检查") || !ttGeneric503.HasText(gatewayURL) || ttGeneric503.HasText("未启用") || ttGeneric503.HasText("18173") {
		t.Fatalf("generic 503 overview unexpected texts: %v", ttGeneric503.Texts())
	}

	if deadPortHits.Load() != 0 {
		t.Fatalf("expected zero hits to stale :18173, got %d", deadPortHits.Load())
	}
}

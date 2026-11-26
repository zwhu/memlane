package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	"github.com/zwhu/memlane/core/profiles"
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

var (
	wireReUserRequestBlock        = regexp.MustCompile(`(?is)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	wireReAdditionalMetadataBlock = regexp.MustCompile(`(?is)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
	wireReUserRequestTag          = regexp.MustCompile(`(?i)</?USER_REQUEST>`)
)

func wireCleanCacheLabel(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if m := wireReUserRequestBlock.FindStringSubmatch(text); len(m) == 2 {
		text = m[1]
	}
	text = wireReAdditionalMetadataBlock.ReplaceAllString(text, "")
	text = wireReUserRequestTag.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

type wireCacheSessionRecord struct {
	DatasetID      string
	UserID         string
	AgentID        string
	SessionID      string
	Status         string
	StartedAt      string
	LastActivityAt string
}

// wireSessionEngine wires real SQLite cache_qa_entries / cache_trace_entries and
// store.ListSessionRelations into gateway.Server through the engine-agnostic
// CatalogProjector + SessionHistoryReader interfaces (without importing core/engine/cognee).
type wireSessionEngine struct {
	wireEngine
	cacheDB  *sql.DB
	sessions []wireCacheSessionRecord
}

var (
	_ engine.CatalogProjector     = (*wireSessionEngine)(nil)
	_ engine.SessionHistoryReader = (*wireSessionEngine)(nil)
)

func (e *wireSessionEngine) ReadLocalBody(context.Context, engine.LocalBodyRef) (string, error) {
	return "", engine.ErrNotFound
}

func (e *wireSessionEngine) SyncCatalog(ctx context.Context, store *domain.Store) (*engine.SyncStats, error) {
	byDataset := map[string][]wireCacheSessionRecord{}
	for _, s := range e.sessions {
		byDataset[s.DatasetID] = append(byDataset[s.DatasetID], s)
	}
	projected := 0
	for ds, list := range byDataset {
		rels, err := store.ListSessionRelations(ctx, ds)
		if err != nil {
			return nil, err
		}
		relBySID := make(map[string]domain.SessionRelation, len(rels))
		for _, r := range rels {
			relBySID[r.SessionID] = r
		}
		present := make(map[string]bool, len(list))
		for _, s := range list {
			present[s.SessionID] = true
		}
		childCounts := map[string]int{}
		for _, s := range list {
			if rel, ok := relBySID[s.SessionID]; ok && rel.ParentSessionID != "" && present[rel.ParentSessionID] {
				childCounts[rel.ParentSessionID]++
			}
		}
		for _, s := range list {
			var sortDescKey int64
			if parsedTS, err := time.Parse(time.RFC3339Nano, s.LastActivityAt); err == nil {
				sortDescKey = -parsedTS.UnixNano()
			}
			row := domain.CatalogSession{
				DatasetID:      s.DatasetID,
				UserID:         s.UserID,
				AgentID:        s.AgentID,
				SessionID:      s.SessionID,
				Status:         s.Status,
				StartedAt:      s.StartedAt,
				LastActivityAt: s.LastActivityAt,
				SortTSDescKey:  sortDescKey,
				IsTopLevel:     true,
				ChildCount:     childCounts[s.SessionID],
			}
			if rel, ok := relBySID[s.SessionID]; ok && rel.ParentSessionID != "" {
				row.ParentSessionID = rel.ParentSessionID
				row.RootSessionID = rel.ParentSessionID
				row.SubagentRole = rel.SubagentRole
				row.NestingDepth = 1
				if present[rel.ParentSessionID] {
					row.IsTopLevel = false
				}
			}
			if err := store.UpsertCatalogSession(ctx, row); err != nil {
				return nil, err
			}
			projected++
		}
		if err := store.UpsertCatalogSyncState(ctx, domain.CatalogSyncState{DatasetID: ds, CatalogVersion: "wire-sync-1"}); err != nil {
			return nil, err
		}
	}
	return &engine.SyncStats{Datasets: len(byDataset), SessionsProjected: projected}, nil
}

func (e *wireSessionEngine) HydrateSessionsFromCache(ctx context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	out := make(map[engine.SessionRef]engine.SessionCacheInfo, len(refs))
	for _, ref := range refs {
		var info engine.SessionCacheInfo
		rows, err := e.cacheDB.QueryContext(ctx,
			`SELECT payload FROM cache_qa_entries WHERE user_id=? AND session_id=? ORDER BY seq`,
			ref.UserID, ref.SessionID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var payload string
			if err := rows.Scan(&payload); err != nil {
				_ = rows.Close()
				return nil, err
			}
			info.MsgCount++
			if info.Label == "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(payload), &parsed); err == nil {
					if q, ok := parsed["question"].(string); ok {
						info.Label = wireCleanCacheLabel(q)
					}
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := e.cacheDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM cache_trace_entries WHERE user_id=? AND session_id=?`,
			ref.UserID, ref.SessionID).Scan(&info.ToolCalls); err != nil {
			return nil, err
		}
		out[ref] = info
	}
	return out, nil
}

func (e *wireSessionEngine) ReadSessionHistory(ctx context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	res := engine.SessionHistoryResult{
		QAs:    []engine.SessionQA{},
		Traces: []engine.SessionTrace{},
	}
	qaRows, err := e.cacheDB.QueryContext(ctx,
		`SELECT qa_id, payload FROM cache_qa_entries WHERE user_id=? AND session_id=? ORDER BY seq`,
		req.UserID, req.SessionID)
	if err != nil {
		return res, err
	}
	var allQAs []engine.SessionQA
	for qaRows.Next() {
		var id, payload string
		if err := qaRows.Scan(&id, &payload); err != nil {
			_ = qaRows.Close()
			return res, err
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			_ = qaRows.Close()
			return res, err
		}
		q, _ := parsed["question"].(string)
		a, _ := parsed["answer"].(string)
		if res.Label == "" {
			res.Label = wireCleanCacheLabel(q)
		}
		allQAs = append(allQAs, engine.SessionQA{ID: id, Question: q, Answer: a})
	}
	if err := qaRows.Close(); err != nil {
		return res, err
	}
	res.MsgCount = len(allQAs)
	if req.QAOffset < len(allQAs) {
		end := min(len(allQAs), req.QAOffset+limit)
		res.QAs = append(res.QAs, allQAs[req.QAOffset:end]...)
	}

	traceRows, err := e.cacheDB.QueryContext(ctx,
		`SELECT payload FROM cache_trace_entries WHERE user_id=? AND session_id=? ORDER BY seq`,
		req.UserID, req.SessionID)
	if err != nil {
		return res, err
	}
	var allTraces []engine.SessionTrace
	for traceRows.Next() {
		var payload string
		if err := traceRows.Scan(&payload); err != nil {
			_ = traceRows.Close()
			return res, err
		}
		var trace engine.SessionTrace
		if err := json.Unmarshal([]byte(payload), &trace); err != nil {
			_ = traceRows.Close()
			return res, err
		}
		allTraces = append(allTraces, trace)
	}
	if err := traceRows.Close(); err != nil {
		return res, err
	}
	res.ToolCalls = len(allTraces)
	if req.TraceOffset < len(allTraces) {
		end := min(len(allTraces), req.TraceOffset+limit)
		res.Traces = append(res.Traces, allTraces[req.TraceOffset:end]...)
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

	cacheDB, err := domain.OpenPrivateSQLite(filepath.Join(dir, "cache.db"))
	check(err)
	t.Cleanup(func() { check(cacheDB.Close()) })

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

	// Persist real parent/child and orphan subagent relations in SQLite.
	for _, rel := range []domain.SessionRelation{
		{DatasetID: datasetID, SessionID: verifSID, ParentSessionID: parentSID, SubagentRole: "verifier"},
		{DatasetID: datasetID, SessionID: monSID, ParentSessionID: parentSID, SubagentRole: "monitor"},
		{DatasetID: datasetID, SessionID: orphanSID, ParentSessionID: "missing-parent-999", SubagentRole: "explorer"},
	} {
		check(store.UpsertSessionRelation(ctx, rel))
	}

	// Seed real SQLite cache_qa_entries and cache_trace_entries with Antigravity XML tags.
	_, err = cacheDB.ExecContext(ctx, `
		CREATE TABLE cache_qa_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, qa_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
		CREATE TABLE cache_trace_entries (seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT, payload TEXT, created_at TEXT, expires_at TEXT);
	`)
	check(err)

	parentPayload, err := json.Marshal(map[string]any{
		"question": "<USER_REQUEST>\n/swarm 修复问题\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nworkspace: /tmp/memlane\n</ADDITIONAL_METADATA>",
		"answer":   "已完成修复并验证通过",
	})
	check(err)
	_, err = cacheDB.ExecContext(ctx,
		`INSERT INTO cache_qa_entries VALUES (1, ?, ?, 'qa-parent-1', ?, '2026-11-26', '2099-01-01 00:00:00')`,
		userID, parentSID, string(parentPayload))
	check(err)

	seq := 1
	insertTraces := func(sid string, tools []string) {
		t.Helper()
		for i, tool := range tools {
			body, err := json.Marshal(map[string]any{
				"trace_id":            fmt.Sprintf("%s-t%d", sid, i+1),
				"origin_function":     tool,
				"status":              "success",
				"method_params":       map[string]any{"step": i + 1},
				"method_return_value": "ok",
			})
			check(err)
			_, err = cacheDB.ExecContext(ctx,
				`INSERT INTO cache_trace_entries VALUES (?, ?, ?, ?, '2026-11-26', '2099-01-01 00:00:00')`,
				seq, userID, sid, string(body))
			check(err)
			seq++
		}
	}
	insertTraces(verifSID, []string{"Read", "Grep", "Bash", "Edit", "Bash"})
	insertTraces(monSID, []string{"Read", "Bash", "Bash"})
	insertTraces(orphanSID, []string{"Glob"})

	baseTime := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	eng := &wireSessionEngine{
		cacheDB: cacheDB,
		sessions: []wireCacheSessionRecord{
			{DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: parentSID, Status: "active",
				StartedAt: domain.FormatUTCNS(baseTime), LastActivityAt: domain.FormatUTCNS(baseTime.Add(4 * time.Minute))},
			{DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: verifSID, Status: "active",
				StartedAt: domain.FormatUTCNS(baseTime.Add(time.Minute)), LastActivityAt: domain.FormatUTCNS(baseTime.Add(3 * time.Minute))},
			{DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: monSID, Status: "active",
				StartedAt: domain.FormatUTCNS(baseTime.Add(2 * time.Minute)), LastActivityAt: domain.FormatUTCNS(baseTime.Add(2 * time.Minute))},
			{DatasetID: datasetID, UserID: userID, AgentID: "antigravity-1", SessionID: orphanSID, Status: "active",
				StartedAt: domain.FormatUTCNS(baseTime), LastActivityAt: domain.FormatUTCNS(baseTime.Add(time.Minute))},
		},
	}

	stats, err := eng.SyncCatalog(ctx, store)
	check(err)
	if stats.SessionsProjected != 4 {
		t.Fatalf("SyncCatalog projected %d sessions, want 4", stats.SessionsProjected)
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

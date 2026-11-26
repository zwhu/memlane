package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		directory.Common.Overrides["agent"].Access == nil || directory.Defaults.Access != "none" ||
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

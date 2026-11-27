package settings

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func writeRemoveTestToken(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(path, []byte("test-management-token-value-1234567890"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectRemoveModalSemanticsPendingCountsAndSuccessFlow(t *testing.T) {
	tokenFile := writeRemoveTestToken(t)
	var deleteCalls, summaryCalls int
	failSummary := true
	failDelete := true
	summaryBody := `{"id":"p-rem","dataset_id":"ds-rem","inbox_pending":2,"inbox_processing":1,"inbox":{"pending":2,"processing":1}}`
	cfg := Config{
		ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:1", ManagementTokenFile: tokenFile},
		Projects:       []Project{{ID: "p-rem", Name: "Demo Project", Dataset: "ds-rem"}},
	}
	a := newTestHost(cfg, nil)
	var invalidated []string
	var navigatedOverview bool
	a.host.InvalidateDataset = func(ds string) {
		invalidated = append(invalidated, ds)
		a.ClearProjectQueues(ds)
	}
	a.host.Router = testRouterFunc{onOverview: func() { navigatedOverview = true }}
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/p-rem":
			summaryCalls++
			if failSummary {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"unavailable","message":"summary unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(summaryBody))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/p-rem":
			if gotDS := r.URL.Query().Get("dataset_id"); gotDS != "ds-rem" {
				t.Errorf("DELETE /v1/projects/p-rem dataset_id = %q, want ds-rem", gotDS)
			}
			deleteCalls++
			if failDelete {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal","message":"delete failed"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			_, _ = w.Write([]byte(`{"revision":2,"defaults":{"access":"maintain","capture":true,"auto_process":true},"agents":[],"projects":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	updates := make(chan func(), 32)
	a.dispatch = func(fn func()) { updates <- fn }

	p := AccessProject{ID: "p-rem", Name: "Demo Project", Dataset: "ds-rem", Status: "verified"}
	a.ApplyAccessDirectory(AccessDirectory{Revision: 1, Projects: []AccessProject{p}})
	a.access.OpenProject = p.ID
	q := a.ProjectQueue(p)
	q.Loaded = true
	q.Next = time.Now().Add(time.Hour)

	render := func(c *ui.Context) {
		a.projectSettingsPage(c)
		a.ViewDialogs(c)
	}
	// Verify light & dark token rendering.
	for _, tok := range []*kitui.Tokens{kitui.LightTokens(), kitui.DarkTokens()} {
		a.fallbackTokens = tok
		tt := ui.NewTester(render, 1200, 1200)
		tt.Frame()
		if !tt.HasText("危险操作") || !tt.HasText("移除项目") {
			t.Fatalf("missing remove project section in projectSettingsPage (dark=%v): %v", tok.Dark, tt.Texts())
		}
	}

	a.fallbackTokens = kitui.LightTokens()
	tt := ui.NewTester(render, 1200, 1200)
	tt.Frame()
	clickProjectUI(t, tt, "移除项目")
	tt.Frame()

	// 1. While summary is loading, show loading message and keep confirm disabled (clicking does not call DELETE).
	if !a.access.RemoveSummaryLoading || !tt.HasText("正在获取当前待处理任务数…") {
		t.Fatalf("expected loading state on modal open: loading=%v texts=%v", a.access.RemoveSummaryLoading, tt.Texts())
	}
	a.confirmRemoveProject()
	if deleteCalls != 0 {
		t.Fatalf("confirm must be blocked while summary is loading, got %d delete calls", deleteCalls)
	}

	// 2. When summary load fails, show error + Retry button and keep confirm disabled.
	drainReading(t, updates, func() bool { return !a.access.RemoveSummaryLoading })
	tt.Frame()
	if a.access.RemoveSummaryLoaded || a.access.RemoveSummaryError == "" || !tt.HasText("重试") {
		t.Fatalf("expected summary failure state with retry button: loaded=%v err=%q texts=%v",
			a.access.RemoveSummaryLoaded, a.access.RemoveSummaryError, tt.Texts())
	}
	a.confirmRemoveProject()
	if deleteCalls != 0 {
		t.Fatalf("confirm must be blocked when summary failed, got %d delete calls", deleteCalls)
	}

	// 3. Clicking Retry loads accurate pending/processing counts and enables confirm.
	failSummary = false
	clickProjectUI(t, tt, "重试")
	drainReading(t, updates, func() bool { return !a.access.RemoveSummaryLoading })
	tt.Frame()
	for _, want := range []string{
		"移除项目『Demo Project』？",
		"后续可在「新建项目」中选择已有数据组重新接回。",
		"待提炼 2 条：移除后将取消，不再启动提炼。",
		"正在提炼 1 条：已发出给模型的在途请求会自然结束，不会再写入当前项目目录。",
		"确认移除",
	} {
		if !tt.HasText(want) {
			t.Fatalf("remove modal missing %q; texts=%v", want, tt.Texts())
		}
	}
	if summaryCalls != 2 {
		t.Fatalf("expected 2 summary calls, got %d", summaryCalls)
	}

	// 4. Failure path keeps modal open and surfaces error.
	clickProjectUI(t, tt, "确认移除")
	drainReading(t, updates, func() bool { return !a.access.RemoveBusy })
	tt.Frame()
	if !a.access.RemoveOpen || a.access.RemoveError == "" || deleteCalls != 1 {
		t.Fatalf("failed delete should keep modal open with error: open=%v err=%q calls=%d", a.access.RemoveOpen, a.access.RemoveError, deleteCalls)
	}

	// 5. Success path closes modal, invalidates dataset, clears OpenProject, refreshes directory, and navigates overview.
	invalidated = nil
	failDelete = false
	clickProjectUI(t, tt, "确认移除")
	drainReading(t, updates, func() bool { return !a.access.RemoveBusy })
	tt.Frame()
	if a.access.RemoveOpen || a.access.RemoveTarget != nil || a.access.OpenProject != "" || len(a.access.Directory.Projects) != 0 || !navigatedOverview {
		t.Fatalf("successful delete did not reset state cleanly: open=%v target=%v openProj=%q projs=%d overview=%v",
			a.access.RemoveOpen, a.access.RemoveTarget, a.access.OpenProject, len(a.access.Directory.Projects), navigatedOverview)
	}
	if len(invalidated) == 0 || invalidated[0] != "ds-rem" {
		t.Fatalf("expected invalidateReading(ds-rem), got %v", invalidated)
	}

	// 6. Zero pending/processing shows explicit zero-task message; authoritative directory removal auto-closes modal (P2 #1).
	summaryBody = `{"id":"p-rem","dataset_id":"ds-rem","inbox_pending":0,"inbox_processing":0,"inbox":{"pending":0,"processing":0}}`
	a.ApplyAccessDirectory(AccessDirectory{Revision: 3, Projects: []AccessProject{p}})
	a.openProjectRemoveDialog(p)
	drainReading(t, updates, func() bool { return !a.access.RemoveSummaryLoading })
	tt.Frame()
	if !tt.HasText("当前无待提炼或进行中的任务。") {
		t.Fatalf("expected explicit zero-task copy: %v", tt.Texts())
	}
	a.ApplyAccessDirectory(AccessDirectory{Revision: 4, Projects: []AccessProject{{ID: "p-rem", Name: "Demo Project", Dataset: "ds-replaced"}}})
	if a.access.RemoveOpen || a.access.RemoveTarget != nil {
		t.Fatalf("modal should auto-close when authoritative directory replaces target project's dataset_id")
	}

	// 7. Protected project must NOT render the remove section.
	prot := AccessProject{ID: "ml-user-profiles", Name: "ml-user-profiles", Dataset: "prof-ds", Protected: true}
	a.access.Directory.Projects = []AccessProject{prot}
	a.access.OpenProject = prot.ID
	a.ProjectQueue(prot).Loaded = true
	a.ProjectQueue(prot).Next = time.Now().Add(time.Hour)
	ttProt := ui.NewTester(render, 1200, 1200)
	ttProt.Frame()
	if ttProt.HasText("移除项目") {
		t.Fatalf("protected project must not display 移除项目: %v", ttProt.Texts())
	}
}

type testRouterFunc struct {
	shell.Router
	onOverview func()
}

func (r testRouterFunc) NavigateOverview() {
	if r.onOverview != nil {
		r.onOverview()
	}
}

func TestProjectQueueUIProcessingAndCatalogPublishStates(t *testing.T) {
	tokenFile := writeRemoveTestToken(t)
	var retryCalls int
	a := newTestHost(Config{
		ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:1", ManagementTokenFile: tokenFile},
		Projects:       []Project{{ID: "p-q", Name: "QueueProj", Dataset: "ds-q"}},
	}, nil)
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/retry") {
			retryCalls++
			_, _ = w.Write([]byte(`{"project_id":"p-q","retried_count":1,"item_ids":["pub-1"]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})}
	updates := make(chan func(), 16)
	a.dispatch = func(fn func()) { updates <- fn }

	p := AccessProject{ID: "p-q", Name: "QueueProj", Dataset: "ds-q", Status: "verified"}
	a.ApplyAccessDirectory(AccessDirectory{Revision: 1, Projects: []AccessProject{p}})
	q := a.ProjectQueue(p)
	q.Loaded = true
	q.Next = time.Now().Add(time.Hour)
	q.Loading = true
	q.Request = 1

	meta := projectQueueApplyMeta{request: 1, authEpoch: a.authEpoch(), baseIdentity: a.cachedBaseReadingIdentity()}
	snap := projectQueueSnapshot{
		gateway: true,
		inbox: &api.ProjectView{
			ID: p.ID,
			ProjectSummary: api.ProjectSummary{
				InboxPending:    2,
				InboxProcessing: 1,
			},
			Inbox: api.InboxSummary{
				Pending:               2,
				Processing:            1,
				CatalogPublishPending: 1,
				CatalogPublishFailed:  1,
				CatalogPublishError:   "sqlite busy",
			},
		},
	}
	a.applyProjectQueueResult(q, p, meta, snap, reconcileProjectQueueState(p, snap))

	tt := ui.NewTester(func(c *ui.Context) { a.projectQueueSection(c, p) }, 1200, 820)
	tt.Frame()
	for _, want := range []string{
		"正在提炼 1 条，另有 2 条等待提炼",
		"记忆已生成，目录待更新",
		"记忆已保存，列表更新失败",
		"sqlite busy",
		"重试更新列表",
	} {
		if !tt.HasText(want) {
			t.Fatalf("queue UI missing %q; texts=%v", want, tt.Texts())
		}
	}

	tray := a.TrayQueueSummary()
	if !tray.HasErrorAlert || len(tray.Progress) != 1 || tray.Progress[0].Count != 4 {
		t.Fatalf("TrayQueueSummary did not reflect InboxProcessing/CatalogPublish states: %+v", tray)
	}

	// Clicking "重试更新列表" invokes RetryProjectInbox -> POST /api/projects/p-q/retry.
	clickProjectUI(t, tt, "重试更新列表")
	drainReading(t, updates, func() bool { return !q.InboxRetryBusy })
	if retryCalls != 1 {
		t.Fatalf("expected 1 retry call from 重试更新列表, got %d", retryCalls)
	}

	// Processing-only state renders exact copy.
	q.InboxPending = 0
	q.InboxProcessing = 2
	q.CatalogPublishPending = 0
	q.CatalogPublishFailed = 0
	tt.Frame()
	if !tt.HasText("有 2 条原始记录正在提炼") {
		t.Fatalf("processing-only copy missing: %v", tt.Texts())
	}
}

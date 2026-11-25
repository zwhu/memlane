package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func commonUIFixture() *App {
	a := New(Config{Common: []Project{{ID: "common", Name: "通用知识", Dataset: "common"}}, ProjectControl: &ProjectControlConfig{}}, nil)
	a.page = "通用知识"
	a.service = "已连接"
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.Update = func(func()) {}
	for n, title := range []string{"代码示例使用 TypeScript", "提交前运行项目约定的检查", "技术结论说明适用边界"} {
		category := "preference"
		if n == 2 {
			category = "experience"
		}
		draft := CommonDraft{Title: title, Body: "先检查项目已有的约定与工具，再选择具体实现方式。\n\n- 保留原有命名与结构\n- 给出可操作的验证步骤", Category: category, Applicability: "适用于编程任务；项目明确约定优先。", Source: "用户明确约定 · 人工示例"}
		body, _ := draft.markdown()
		d := Document{ID: fmt.Sprint(n), Dataset: "common", Name: title, Created: "2026-11-13T16:56:00Z", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": category, "source": draft.Source}}
		i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: title, Date: d.Created, Document: &d, CommonCategory: category, Body: body, BodyLoaded: true}
		a.state().Items = append(a.state().Items, i)
	}
	s := a.state()
	s.Loaded = true
	i := s.Items[0]
	s.Selected = &i
	return a
}
func commonFixtureImage(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	fixtureImage(t, tt, "common-"+name)
}
func TestCommonPageCompactEditorAndDraftContinuity(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := commonUIFixture()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			size := fmt.Sprintf("%dx%d", wh[0], wh[1])
			commonFixtureImage(t, tt, size+"-reading")
			if tt.HasText("全部分类") || tt.HasText("管理通用记忆") {
				t.Fatal("redundant old controls remain")
			}
			clickProjectUI(t, tt, "新增通用记忆")
			if !a.commonEdit.Open || a.commonEdit.Existing != nil {
				t.Fatal("new editor not opened")
			}
			if tt.HasText("通用记忆适用条件") {
				t.Fatal("optional fields expanded by default")
			}
			clickProjectUI(t, tt, "通用记忆标题")
			tt.Type("保留编辑草稿")
			clickProjectUI(t, tt, "通用记忆正文")
			tt.Type("阅读其他内容后仍可继续编辑。")
			generation := a.commonEdit.Generation
			footer, ok := tt.Find("保存")
			if !ok || footer.Y+footer.H > float32(wh[1]) {
				t.Fatal("save action below viewport")
			}
			commonFixtureImage(t, tt, size+"-new")
			clickProjectUI(t, tt, "补充适用条件与来源（可选）")
			commonFixtureImage(t, tt, size+"-optional")
			tt.Scroll(float32(wh[0]-100), 400, 0, 280)
			after, _ := tt.Find("保存")
			if after != footer {
				t.Fatal("editor content scroll moved footer")
			}
			clickProjectUI(t, tt, "返回阅读")
			if a.commonEdit.Open {
				t.Fatal("editor did not close")
			}
			clickProjectUI(t, tt, "继续草稿")
			if a.commonEdit.Draft.Title != "保留编辑草稿" || a.commonEdit.Draft.Body != "阅读其他内容后仍可继续编辑。" || a.commonEdit.Generation != generation {
				t.Fatal("return discarded or replaced draft")
			}
			clickProjectUI(t, tt, "放弃草稿")
			if a.commonEdit.Draft.Body == "" {
				t.Fatal("discarded without confirmation")
			}
			clickProjectUI(t, tt, "继续编辑")
			if !a.commonEdit.Open {
				t.Fatal("discard cancellation closed editor")
			}
			clickProjectUI(t, tt, "放弃草稿")
			clickProjectUI(t, tt, "放弃并返回")
			if a.commonEdit.Open || commonHasDraft(a.commonEdit) {
				t.Fatal("explicit discard failed")
			}
		})
	}
}
func TestCommonEditorSubmittedLockedAndNeverReplaced(t *testing.T) {
	a := commonUIFixture()
	i := a.state().Items[0]
	a.beginCommonDraft(&i)
	a.commonEdit.Submitted = true
	a.commonEdit.Receipt = CommonWriteReceipt{PipelineRunID: "original-operation", OutcomeUnknown: true}
	a.commonEdit.Message = "保存结果未知，请核对原操作，不要重复提交"
	a.commonEdit.Open = false
	a.beginCommonDraft(&a.state().Items[1])
	if a.commonEdit.Receipt.PipelineRunID != "original-operation" || a.commonEdit.Draft.Title != i.Title {
		t.Fatal("unresolved operation overwritten by another editor")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	before := a.commonEdit.Draft
	clickProjectUI(t, tt, "通用记忆标题")
	tt.Type("should not edit")
	hasSave := false
	for _, label := range tt.Texts() {
		if label == "保存" {
			hasSave = true
		}
	}
	if a.commonEdit.Draft != before || tt.HasText("放弃草稿") || hasSave {
		t.Fatal("submitted draft can change or resubmit")
	}
	commonFixtureImage(t, tt, "1200x820-unknown-save")
	a.commonEdit.SaveVerified = true
	a.commonEdit.Processing = "STARTED"
	a.commonEdit.Message = "官方正文已核对；提炼状态：STARTED"
	tt.Frame()
	commonFixtureImage(t, tt, "1200x820-saved-processing")
	if tt.HasText("完成") {
		t.Fatal("running processing shown as complete")
	}
	a.commonEdit.Processing = "已完成"
	tt.Frame()
	clickProjectUI(t, tt, "完成")
	if commonHasDraft(a.commonEdit) || a.commonEdit.Open {
		t.Fatal("known result cannot finish")
	}
}
func TestCommonCategoryPagingFiltersBeforeSlicing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/common/knowledge":
			q := r.URL.Query()
			if q.Get("dataset_id") != "common" || q.Get("common_category") != "preference" || q.Get("limit") != "30" {
				t.Error("category must be part of the paginated request", q)
			}
			start := 0
			if q.Get("cursor") == "preferences-30" {
				start = 30
			} else if q.Has("cursor") {
				t.Error("unexpected cursor", q)
			}
			resp := v2CommonKnowledgeResponse{TotalFiltered: 35, HasMore: start == 0}
			if resp.HasMore {
				resp.NextCursor = "preferences-30"
			}
			for n := start; n < min(start+30, 35); n++ {
				resp.Items = append(resp.Items, v2KnowledgeItem{DocID: fmt.Sprintf("%03d", n+30), DatasetID: "common", CommonCategory: "preference"})
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	control := ProjectControlClient{Config: *readingControlConfig(t, server.URL)}
	rows, err := control.commonKnowledgePageV2(context.Background(), "common", "preference", 30, v2KnowledgePageOpts{})
	if err != nil || len(rows.Items) != 30 || !rows.HasMore || rows.TotalFiltered != 35 || rows.Items[0].DocID != "030" {
		t.Fatalf("filtered first page: %+v %v", rows, err)
	}
	next, err := control.commonKnowledgePageV2(context.Background(), "common", "preference", 30, v2KnowledgePageOpts{Cursor: rows.NextCursor})
	if err != nil || len(next.Items) != 5 || next.HasMore || next.Items[0].DocID != "060" {
		t.Fatalf("filtered second page: %+v %v", next, err)
	}
}
func TestCommonPageEmptyAndLoadingDiffer(t *testing.T) {
	a := commonUIFixture()
	s := a.state()
	s.Selected = nil
	s.Items = nil
	tt := ui.NewTester(a.View, 1200, 820)
	commonFixtureImage(t, tt, "1200x820-empty")
	s.Loaded = false
	s.Loading = true
	tt.Frame()
	commonFixtureImage(t, tt, "1200x820-loading")
	if tt.HasText("已载入 0 条") {
		t.Fatal("loading displayed false zero")
	}
}
func TestCommonSaveAndConfirmPreserveOriginalOperation(t *testing.T) {
	var writes atomic.Int32
	draft := CommonDraft{Title: "A reusable preference", Body: "Use the applicable project command.", Category: "preference"}
	body, _ := draft.markdown()
	d := Document{ID: "saved", Dataset: "common", Name: "Saved preference", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/remember":
			writes.Add(1)
			json.NewEncoder(w).Encode(CommonWriteReceipt{Status: "running", DatasetID: "common", DataID: "saved", PipelineRunID: "same-run"})
		case r.URL.Path == "/v1/datasets/common/documents/saved/body":
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: d.ID, DatasetID: d.Dataset, Body: &body})
		case strings.Contains(r.URL.Path, "pipeline-runs"):
			json.NewEncoder(w).Encode([]map[string]string{{"kind": "pipeline", "dataset_id": "common", "pipeline_run_id": "same-run", "status": "STARTED"}})
		case r.URL.Path == "/v1/datasets/common/documents/saved":
			json.NewEncoder(w).Encode(documentLookupV2Response{Document: d})
		case r.URL.Path == "/v1/common/knowledge":
			json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{})
		case r.URL.Path == "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := commonUIFixture()
	credential := filepath.Join(t.TempDir(), "official.key")
	os.WriteFile(credential, []byte("synthetic-key"), 0600)
	a.Config.ServiceURL = server.URL
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
	a.Config.TokenFile = credential
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 100)
	a.Update = func(fn func()) { updates <- fn }
	a.beginCommonDraft(nil)
	a.commonEdit.Draft = draft
	a.saveCommonKnowledge()
	a.saveCommonKnowledge()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if writes.Load() != 1 || !a.commonEdit.Submitted || a.commonEdit.SaveVerified {
		t.Fatal("duplicate write or accepted confused with verified")
	}
	a.confirmCommonKnowledge()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if !a.commonEdit.SaveVerified || a.commonEdit.Processing != "STARTED" || writes.Load() != 1 {
		t.Fatalf("confirmation resubmitted or confused processing with saved content: writes=%d message=%q processing=%q", writes.Load(), a.commonEdit.Message, a.commonEdit.Processing)
	}
}
func TestCommonDeleteUnknownOnlyChecksOriginal(t *testing.T) {
	var deletes atomic.Int32
	present := true
	d := Document{ID: "0", Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/common/knowledge":
			json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{})
		case "/v1/datasets/common/documents/0":
			if present {
				json.NewEncoder(w).Encode(documentLookupV2Response{Document: d})
			} else {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "document_not_found"})
			}
		default:
			t.Error("unexpected verification request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := commonUIFixture()
	a.Client.Config.ServiceURL = server.URL
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.deleteCommonKnowledge(d)
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	a.deleteCommonKnowledge(d)
	if deletes.Load() != 1 || !a.commonEdit.DeleteSubmitted {
		t.Fatal("unknown deletion automatically retried")
	}
	a.confirmCommonDeletion()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if deletes.Load() != 1 || a.commonEdit.DeleteSubmitted {
		t.Fatal("read-only verification did not restore explicit confirmation after finding original")
	}
	// Simulate a still-unknown original whose target is now absent. No second DELETE.
	a.commonEdit.DeleteSubmitted = true
	a.commonEdit.Delete = &d
	a.commonEdit.DeleteTarget = Project{Dataset: "common"}
	present = false
	a.confirmCommonDeletion()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if a.commonEdit.Delete != nil || a.state().Selected != nil || deletes.Load() != 1 {
		t.Fatal("verified absence kept stale content or resent deletion")
	}
}

func TestCommonConfirmKnownOlderDocumentOutsideFirstPage(t *testing.T) {
	draft := CommonDraft{Title: "Older saved preference", Body: "An edited document retains its creation date.", Category: "preference"}
	body, _ := draft.markdown()
	rawReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("confirmation wrote data")
		}
		if strings.HasSuffix(r.URL.Path, "/raw") {
			rawReads++
			w.Write([]byte(body))
			return
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/datasets/common/documents/d44":
			json.NewEncoder(w).Encode(documentLookupV2Response{
				Document:       Document{ID: "d44", Dataset: "common", Created: "2026-11-06T00:00:00Z"},
				CommonCategory: "preference",
			})
		default:
			t.Error("known ID must use point lookup", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := Client{Config: Config{ServiceURL: server.URL}}
	d, _, err := c.confirmCommon(context.Background(), ProjectControlClient{Config: *readingControlConfig(t, server.URL)}, true, Project{Dataset: "common"}, CommonWriteReceipt{DataID: "d44", Status: "completed"}, draft)
	if err != nil || d.ID != "d44" || rawReads != 1 {
		t.Fatalf("known saved ID lost beyond first list page: %s %d %v", d.ID, rawReads, err)
	}
}

func TestCommonDeleteAndDiscardV4DialogsIgnoreEnterAndCancelOnEsc(t *testing.T) {
	a := commonUIFixture()
	tt := ui.NewTester(a.View, 1200, 820)

	// Delete confirmation dialog must be danger (no Enter confirm) and cancel on Esc.
	clickProjectUI(t, tt, "删除")
	if a.commonEdit.Delete == nil || !tt.HasText("删除这条通用记忆？") || !tt.HasText("确认删除此条") {
		t.Fatal("delete v4Dialog did not open")
	}
	tt.Key(0, ui.KeyEnter)
	if a.commonEdit.DeleteSubmitted || a.commonEdit.Busy {
		t.Fatal("Enter unexpectedly confirmed destructive common knowledge delete")
	}
	tt.Key(0, ui.KeyEscape)
	if a.commonEdit.Delete != nil {
		t.Fatal("Escape did not cancel common knowledge delete dialog")
	}

	// Discard draft confirmation dialog must also ignore Enter and cancel on Esc.
	clickProjectUI(t, tt, "新增通用记忆")
	clickProjectUI(t, tt, "通用记忆正文")
	tt.Type("草稿内容")
	clickProjectUI(t, tt, "放弃草稿")
	if !a.commonPage.Discard || !tt.HasText("放弃未保存的草稿？") {
		t.Fatal("discard v4Dialog did not open")
	}
	tt.Key(0, ui.KeyEnter)
	if !a.commonPage.Discard || !a.commonEdit.Open || !commonHasDraft(a.commonEdit) {
		t.Fatal("Enter unexpectedly confirmed or dismissed destructive draft discard")
	}
	tt.Key(0, ui.KeyEscape)
	if a.commonPage.Discard || !a.commonEdit.Open || !commonHasDraft(a.commonEdit) {
		t.Fatal("Escape did not cancel discard dialog cleanly")
	}
}

func TestV4DangerDialogConsumesEnterWithoutLeakingToBackground(t *testing.T) {
	open := true
	bgClicks := 0
	bgShortcuts := 0
	dangerConfirms := 0
	dangerCancels := 0

	tt := ui.NewTester(func(c *ui.Context) {
		if c.Shortcut(0, ui.KeyEnter) {
			bgShortcuts++
		}
		ui.Column(c).Fill().Children(func() {
			ui.Button(c, "背景按钮").OnClick(func() { bgClicks++ })
		})
		v4Dialog(c, &open, v4DialogOptions{
			Key:         "danger-enter-test",
			Title:       "危险确认",
			Danger:      true,
			ConfirmText: "确认删除",
			OnCancel:    func() { dangerCancels++ },
			OnConfirm:   func() { dangerConfirms++ },
			Body: func() {
				ui.Text(c, "此操作不可恢复")
			},
		})
	}, 800, 600)

	tt.Key(0, ui.KeyEnter)
	if !open || dangerConfirms != 0 || dangerCancels != 0 || bgClicks != 0 || bgShortcuts != 0 {
		t.Fatalf("danger dialog leaked Enter: open=%v confirms=%d cancels=%d bgClicks=%d bgShortcuts=%d",
			open, dangerConfirms, dangerCancels, bgClicks, bgShortcuts)
	}
}

func TestCommonKnowledgeV4ThreePaneAndEditImmutability(t *testing.T) {
	a := commonUIFixture()
	tt := ui.NewTester(a.View, 1200, 820)

	// Verify V4 three-pane chrome: list segmented filter, reading pane hairline sections & copy footer.
	for _, want := range []string{"记忆库", "全部", "个人偏好", "通用经验", "适用条件", "原始来源", "复制正文", "跨项目参考 · 项目明确约定优先"} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected V4 common knowledge text %q", want)
		}
	}

	// Editing an existing memory must preserve its category even if segmented control is clicked.
	clickProjectUI(t, tt, "编辑")
	if !a.commonEdit.Open || a.commonEdit.Existing == nil {
		t.Fatal("edit mode did not open for existing common knowledge")
	}
	beforeCat := a.commonEdit.Draft.Category
	if beforeCat != "preference" {
		t.Fatalf("unexpected initial category: %q", beforeCat)
	}
	if el, ok := tt.Find("记忆分类"); ok {
		tt.ClickAt(el.X+el.W*0.75, el.Y+el.H/2)
	}
	if a.commonEdit.Draft.Category != beforeCat {
		t.Fatalf("editing existing memory mutated category: %q -> %q", beforeCat, a.commonEdit.Draft.Category)
	}

	// Empty state renders V4 empty state with primary CTA "添加记忆".
	a.commonEdit.Open = false
	a.discardCommonDraft()
	s := a.state()
	s.Items = nil
	s.Selected = nil
	s.Loaded = true
	s.Loading = false
	tt.Frame()
	if !tt.HasText("还没有通用记忆") || !tt.HasText("添加记忆") {
		t.Fatal("empty common knowledge state missing V4 empty state or primary CTA")
	}
}

func TestCommonKnowledgeRefreshNoTransientTextOrLayoutShift(t *testing.T) {
	// 1. Existing items & body: background reload must not flash transient labels or shift geometry.
	a := commonUIFixture()
	s := a.state()
	tt := ui.NewTester(a.View, 1200, 820)
	firstItemBefore, ok := tt.Find("提交前运行项目约定的检查")
	if !ok {
		t.Fatal("missing second common item before refresh")
	}
	readerTitleBefore, ok := tt.Find("代码示例使用 TypeScript")
	if !ok {
		t.Fatal("missing reader title before refresh")
	}

	s.Loading = true
	s.Reading = true
	tt.Frame()
	for _, unwanted := range []string{"正在刷新，保留现有内容", "正在读取正文…", "正在刷新…", "正在读取…"} {
		if tt.HasText(unwanted) {
			t.Fatalf("common knowledge flashed transient loading text %q during background refresh; texts=%v", unwanted, tt.Texts())
		}
	}
	firstItemAfter, _ := tt.Find("提交前运行项目约定的检查")
	readerTitleAfter, _ := tt.Find("代码示例使用 TypeScript")
	if firstItemBefore != firstItemAfter {
		t.Fatalf("background refresh shifted list geometry: %+v -> %+v", firstItemBefore, firstItemAfter)
	}
	if readerTitleBefore != readerTitleAfter {
		t.Fatalf("background refresh shifted reader geometry: %+v -> %+v", readerTitleBefore, readerTitleAfter)
	}

	// 2. Empty common library revisiting while background refresh runs: keeps empty state cleanly.
	s.Selected = nil
	s.Items = nil
	s.Loaded = true
	s.Loading = true
	s.Reading = false
	s.Error = ""
	tt.Frame()
	if !tt.HasText("还没有通用记忆") || tt.HasText("正在刷新…") || tt.HasText("正在读取…") {
		t.Fatalf("empty common page flashed refresh state: texts=%v", tt.Texts())
	}

	// 3. Initial load before Loaded=true: no transient "正在读取…" text or false "已载入 0 条".
	s.Loaded = false
	s.Loading = true
	tt.Frame()
	if tt.HasText("正在读取…") || tt.HasText("正在刷新") || tt.HasText("已载入 0 条") {
		t.Fatalf("initial common load showed unwanted text: texts=%v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if txt == "正在读取" {
			t.Fatalf("initial common load showed unwanted status text %q: texts=%v", txt, tt.Texts())
		}
	}

	// 4. Unhydrated item title uses silent placeholder bars rather than "正在读取…".
	s.Loaded = true
	s.Loading = false
	s.Items = []Item{{ID: "pending-doc", Dataset: "common", Kind: "知识", CommonCategory: "preference"}}
	tt.Frame()
	if tt.HasText("正在读取…") {
		t.Fatalf("unhydrated common list card showed raw loading text: texts=%v", tt.Texts())
	}
}

func TestCommonKnowledgePerAgentReadAccessToggleAndUnconfirmedRollback(t *testing.T) {
	noneAccess := "none"
	readAccess := "read"
	var mu sync.Mutex
	directory := AccessDirectory{
		Revision: 1,
		Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true},
		Agents: []AccessAgent{
			{ID: "codex", Name: "Codex"},
			{ID: "claude", Name: "Claude Code"},
		},
		Common: &AccessProject{
			ID:              "common-1",
			Name:            "通用记忆",
			Dataset:         "common",
			Status:          "verified",
			DesiredRevision: 1,
			Overrides: map[string]ProjectOverride{
				"codex":  {Access: &noneAccess},
				"claude": {Access: &readAccess},
			},
			Applied: map[string]AccessApplied{
				"codex":  {Status: "verified", Revision: 1},
				"claude": {Status: "verified", Revision: 1},
			},
		},
	}

	var writes atomic.Int32
	var failStatus atomic.Int32
	var lastAgent, lastAccess string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			writes.Add(1)
			if r.URL.Path != "/v1/projects/common-1/overrides" {
				t.Errorf("unexpected common override path: %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			var input struct {
				Revision int64          `json:"expected_revision"`
				Agent    string         `json:"agent_id"`
				Fields   map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode override payload: %v", err)
				w.WriteHeader(400)
				return
			}
			if input.Revision != directory.Revision {
				w.WriteHeader(409)
				return
			}
			// Enforce project_control.py common_read_only contract: only "access" in {"none","read"}.
			accessVal, ok := input.Fields["access"].(string)
			if len(input.Fields) != 1 || !ok || (accessVal != "none" && accessVal != "read") {
				t.Errorf("invalid common override fields (must be access-only none|read): %+v", input.Fields)
				w.WriteHeader(400)
				return
			}
			lastAgent = input.Agent
			lastAccess = accessVal
			if code := int(failStatus.Load()); code != 0 {
				w.WriteHeader(code)
				return
			}
			valCopy := accessVal
			directory.Common.Overrides[input.Agent] = ProjectOverride{Access: &valCopy}
			directory.Revision++
			directory.Common.DesiredRevision = directory.Revision
			for _, ag := range directory.Agents {
				directory.Common.Applied[ag.ID] = AccessApplied{Status: "verified", Revision: directory.Revision}
			}
			_ = json.NewEncoder(w).Encode(directory)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/projects" {
			_ = json.NewEncoder(w).Encode(directory)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}

	a := commonUIFixture()
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}
	encodedDir, _ := json.Marshal(directory)
	var initialDir AccessDirectory
	_ = json.Unmarshal(encodedDir, &initialDir)
	a.applyAccessDirectory(initialDir)
	a.page = "通用知识"
	a.state().Loaded = true
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }

	// Verify light and dark mode rendering of titlebar badge and popover controls.
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		a.commonPage.MenuOpen = false
		tt := ui.NewTester(a.View, 1200, 820)
		if !tt.HasText("Agent 权限 · 1/2 只读") {
			t.Fatalf("%s mode missing titlebar common access badge; texts=%v", pref, tt.Texts())
		}
		clickProjectUI(t, tt, "Agent 权限 · 1/2 只读")
		if !a.commonPage.MenuOpen {
			t.Fatalf("%s mode clicking titlebar badge did not open common menu popover", pref)
		}
		for _, want := range []string{"通用记忆数据组", "Agent 访问权限", "Codex", "Claude Code", "不可访问", "只读"} {
			if !tt.HasText(want) {
				t.Fatalf("%s mode popover missing %q; texts=%v", pref, want, tt.Texts())
			}
		}
	}
	a.setAppearancePreference(appearanceLight)
	tt := ui.NewTester(a.View, 1200, 820)
	a.commonPage.MenuOpen = true
	tt.Frame()

	// 1. Grant read access to newly connected Codex agent (none -> read) via UI segmented control.
	codexSeg, ok := tt.Find("Codex 通用记忆访问权限")
	if !ok {
		t.Fatal("missing Codex common access segmented control")
	}
	tt.ClickAt(codexSeg.X+codexSeg.W*0.75, codexSeg.Y+codexSeg.H/2)
	drainReading(t, updates, func() bool { return !a.access.Busy })
	tt.Frame()
	mu.Lock()
	gotAgent, gotAccess := lastAgent, lastAccess
	mu.Unlock()
	if writes.Load() != 1 || gotAgent != "codex" || gotAccess != "read" {
		t.Fatalf("grant read sent unexpected request: writes=%d agent=%q access=%q", writes.Load(), gotAgent, gotAccess)
	}
	if commonAgentAccess(*a.access.Directory.Common, "codex") != "read" {
		t.Fatal("directory state did not update codex common access to read")
	}
	if a.access.CheckboxPending["common-1/codex"] != nil {
		t.Fatal("verified common access grant stayed pending")
	}
	if !tt.HasText("Agent 权限 · 2/2 只读") || !tt.HasText("已保存") {
		t.Fatalf("UI did not reflect 2/2 readable after grant; texts=%v", tt.Texts())
	}

	// 2. Revoke Claude Code common access (read -> none) via UI segmented control.
	claudeSeg, ok := tt.Find("Claude Code 通用记忆访问权限")
	if !ok {
		t.Fatal("missing Claude Code common access segmented control")
	}
	tt.ClickAt(claudeSeg.X+claudeSeg.W*0.25, claudeSeg.Y+claudeSeg.H/2)
	drainReading(t, updates, func() bool { return !a.access.Busy })
	tt.Frame()
	mu.Lock()
	gotAgent, gotAccess = lastAgent, lastAccess
	mu.Unlock()
	if writes.Load() != 2 || gotAgent != "claude" || gotAccess != "none" {
		t.Fatalf("revoke read sent unexpected request: writes=%d agent=%q access=%q", writes.Load(), gotAgent, gotAccess)
	}
	if commonAgentAccess(*a.access.Directory.Common, "claude") != "none" || !tt.HasText("Agent 权限 · 1/2 只读") {
		t.Fatalf("UI did not reflect 1/2 readable after revoke; texts=%v", tt.Texts())
	}

	// 3. Client-side guard must reject invalid "maintain" tier for common memory without network write.
	a.saveCommonAgentAccess("common-1", "codex", "maintain")
	if writes.Load() != 2 {
		t.Fatalf("maintain tier unexpectedly triggered write: writes=%d", writes.Load())
	}

	// 4. Definitive 400/403/404 rejection releases pending lock immediately (nothing mutated).
	failStatus.Store(404)
	a.saveCommonAgentAccess("common-1", "claude", "read")
	drainReading(t, updates, func() bool { return !a.access.Busy })
	if writes.Load() != 3 || a.access.CheckboxPending["common-1/claude"] != nil {
		t.Fatalf("definite 404 rejection kept pending locked: writes=%d pending=%v", writes.Load(), a.access.CheckboxPending["common-1/claude"])
	}

	// 5. Failed/unconfirmed 500 write must lock control, preserve server-confirmed value, and never auto-retry.
	failStatus.Store(500)
	claudeSeg, _ = tt.Find("Claude Code 通用记忆访问权限")
	tt.ClickAt(claudeSeg.X+claudeSeg.W*0.75, claudeSeg.Y+claudeSeg.H/2)
	drainReading(t, updates, func() bool { return !a.access.Busy })
	tt.Frame()
	if writes.Load() != 4 || a.access.CheckboxPending["common-1/claude"] == nil {
		t.Fatalf("failed write did not stay locked pending verification: writes=%d pending=%v", writes.Load(), a.access.CheckboxPending["common-1/claude"])
	}
	if commonAgentAccess(*a.access.Directory.Common, "claude") != "none" {
		t.Fatal("failed write falsely mutated displayed common access")
	}
	if !tt.HasText("待核对") || !tt.HasText("重新核对") {
		t.Fatalf("failed write missing unconfirmed rollback hint or verify action; texts=%v", tt.Texts())
	}
	// Clicking again while unconfirmed must not send a duplicate PUT.
	a.saveCommonAgentAccess("common-1", "claude", "read")
	if writes.Load() != 4 {
		t.Fatalf("unconfirmed common access write was retried: writes=%d", writes.Load())
	}

	// 6. Explicit GET verification resolves pending state once backend revision advances.
	failStatus.Store(0)
	mu.Lock()
	directory.Revision++
	directory.Common.DesiredRevision = directory.Revision
	for _, ag := range directory.Agents {
		directory.Common.Applied[ag.ID] = AccessApplied{Status: "verified", Revision: directory.Revision}
	}
	mu.Unlock()
	clickProjectUI(t, tt, "重新核对")
	drainReading(t, updates, func() bool { return !a.access.Loading })
	if a.access.CheckboxPending["common-1/claude"] != nil || writes.Load() != 4 {
		t.Fatalf("explicit GET verification did not clear pending without extra write: pending=%v writes=%d", a.access.CheckboxPending["common-1/claude"], writes.Load())
	}
}

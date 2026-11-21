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
	a.commonEdit.Message = "官方正文已核对；处理状态：STARTED"
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
		docs := []Document{}
		for n := 0; n < 65; n++ {
			category := "experience"
			if n >= 30 {
				category = "preference"
			}
			docs = append(docs, Document{ID: fmt.Sprintf("%03d", n), Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": category}})
		}
		json.NewEncoder(w).Encode(docs)
	}))
	defer server.Close()
	c := Client{Config: Config{ServiceURL: server.URL}}
	rows, more, err := c.commonDocuments(context.Background(), Project{Dataset: "common"}, 0, "preference")
	if err != nil || len(rows) != 30 || !more {
		t.Fatalf("filtered first page: %d %v %v", len(rows), more, err)
	}
	next, more, err := c.commonDocuments(context.Background(), Project{Dataset: "common"}, 30, "preference")
	if err != nil || len(next) != 5 || more {
		t.Fatalf("filtered second page: %d %v %v", len(next), more, err)
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
		case r.Method == "POST":
			writes.Add(1)
			json.NewEncoder(w).Encode(CommonWriteReceipt{Status: "running", DatasetID: "common", DataID: "saved", PipelineRunID: "same-run"})
		case strings.HasSuffix(r.URL.Path, "/raw"):
			fmt.Fprint(w, body)
		case strings.Contains(r.URL.Path, "pipeline-runs"):
			json.NewEncoder(w).Encode([]map[string]string{{"kind": "pipeline", "dataset_id": "common", "pipeline_run_id": "same-run", "status": "STARTED"}})
		default:
			json.NewEncoder(w).Encode([]Document{d})
		}
	}))
	defer server.Close()
	a := commonUIFixture()
	credential := filepath.Join(t.TempDir(), "official.key")
	os.WriteFile(credential, []byte("synthetic-key"), 0600)
	a.Config.ServiceURL = server.URL
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
		t.Fatal("confirmation resubmitted or confused processing with saved content")
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
		docs := []Document{}
		if present {
			docs = append(docs, d)
		}
		json.NewEncoder(w).Encode(docs)
	}))
	defer server.Close()
	a := commonUIFixture()
	a.Client.Config.ServiceURL = server.URL
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
		docs := []Document{}
		for n := 0; n < 45; n++ {
			docs = append(docs, Document{ID: fmt.Sprintf("d%02d", n), Dataset: "common", Created: fmt.Sprintf("2026-11-%02dT00:00:00Z", 28-n/2), Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}})
		}
		json.NewEncoder(w).Encode(docs)
	}))
	defer server.Close()
	c := Client{Config: Config{ServiceURL: server.URL}}
	d, _, err := c.confirmCommon(context.Background(), Project{Dataset: "common"}, CommonWriteReceipt{DataID: "d44", Status: "completed"}, draft)
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
	if !a.commonEdit.Open || !commonHasDraft(a.commonEdit) {
		t.Fatal("Enter unexpectedly confirmed destructive draft discard")
	}
	if !a.commonPage.Discard {
		clickProjectUI(t, tt, "放弃草稿")
	}
	tt.Key(0, ui.KeyEscape)
	if a.commonPage.Discard || !a.commonEdit.Open || !commonHasDraft(a.commonEdit) {
		t.Fatal("Escape did not cancel discard dialog cleanly")
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

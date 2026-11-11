package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func preferenceUIRecords(t *testing.T) (*store.Store, records.ProjectPreferenceComparison) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	scopes, err := db.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pref := ""
	for _, s := range scopes {
		if s.Kind == "preference" {
			pref = s.ID
		}
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "测试项目", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	vs := []records.Version{}
	for i, id := range []string{project.ID, pref} {
		v, err := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: id, Title: []string{"项目明确约定", "个人默认偏好"}[i], Text: []string{"本项目按明确评审约定实施。\n项目全文尾。", "个人偏好仅供通常情况参考。\n偏好全文尾。"}[i], Source: "用户手动保存"})
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	c, err := db.ProjectPreferenceComparison(ctx, records.ProjectPreferenceQuery{Project: project.ID, ProjectRecord: preferenceTarget(vs[0]), PreferenceRecord: preferenceTarget(vs[1]), Datasets: []string{project.ID, pref}})
	if err != nil {
		t.Fatal(err)
	}
	return db, c
}
func preferenceUIPanel(a *App, c records.ProjectPreferenceComparison) *ProjectPreferencePanel {
	item := displayVersion(c.ProjectRecord)
	a.page, a.scope, a.selectedID = "项目", c.Project.ID, item.ID
	a.items = []MemoryItem{item}
	a.listTotal = 1
	a.selectedItem = &item
	a.browsing.View = "memories"
	a.visualFixture = true
	a.projects = []ProjectItem{{ID: c.Project.ID, Name: c.Project.Name}}
	a.scopes = []records.Scope{c.Project}
	s := &ProjectPreferencePanel{Root: a.library.Root, IntentRoot: a.library.Root, Project: preferenceTarget(c.ProjectRecord), Identity: c.LibraryIdentity, Open: true, Comparison: &c, Preference: preferenceTarget(c.PreferenceRecord), Drafts: map[string]*PreferenceDecisionDraft{}}
	s.Draft = &PreferenceDecisionDraft{}
	s.Drafts[preferenceKey(s.Preference)] = s.Draft
	a.projectPreference = ProjectPreferenceUI{Panel: s, Panels: map[string]*ProjectPreferencePanel{preferenceKey(s.Project): s}}
	return s
}
func preferenceUIRequest(c records.ProjectPreferenceComparison) records.ProjectPreferenceConfirmation {
	return records.ProjectPreferenceConfirmation{OperationID: uuid.NewString(), LibraryIdentity: c.LibraryIdentity, Query: records.ProjectPreferenceQuery{LibraryIdentity: c.LibraryIdentity, Project: c.Project.ID, ProjectRecord: preferenceTarget(c.ProjectRecord), PreferenceRecord: preferenceTarget(c.PreferenceRecord), Datasets: []string{c.Project.ID, c.PreferenceRecord.Dataset}}, ProjectQuote: c.ProjectRecord.Text, PreferenceQuote: c.PreferenceRecord.Text, Conditions: "仅适用于本次明确选择的项目与版本。", ConfirmProjectRule: true, Decision: "use_project"}
}
func TestProjectPreferenceUIExplicitInputsAndReadOnlyFrames(t *testing.T) {
	_, c := preferenceUIRecords(t)
	for _, width := range []int{1200, 800} {
		a := New("test", nil)
		s := preferenceUIPanel(a, c)
		a.query = "保留查询"
		a.draft = "原有草稿"
		a.detailScroll.Y = 31
		tt := ui.NewTester(a.View, width, 640)
		if s.Draft.Rule || s.Preference != preferenceTarget(c.PreferenceRecord) || validPreferenceDraft(s) {
			t.Fatal("selection invented explicit confirmation")
		}
		if tt.HasText("本次确认依据") || tt.HasText("确认本次按项目约定参考") || !tt.HasText("记录本次适用关系…") {
			t.Fatal("ordinary read-only comparison was replaced by an unsolicited confirmation form")
		}
		writeScreenshot(t, tt, fmt.Sprintf("e06-readonly-%d.png", width))
		if err := tt.Click("记录本次适用关系…"); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{c.ProjectRecord.Text, c.PreferenceRecord.Text, "该版本未单独记录适用条件，可结合正文核对。", "本次适用说明"} {
			if !tt.HasText(text) {
				t.Fatalf("%d missing full evidence %q", width, text)
			}
		}
		n, ok := tt.Find("确认本次按项目约定参考")
		if !ok || n.Y+n.H > 640 || n.X+n.W > float32(width) {
			t.Fatal("fixed confirm unavailable", n)
		}
		writeScreenshot(t, tt, fmt.Sprintf("e06-comparison-%d.png", width))
		s.Scroll.Y = s.Scroll.MaxY
		tt.Frame()
		if !tt.HasText("该条为本项目明确约定") {
			t.Fatal("explicit checkbox missing")
		}
		n, ok = tt.Find("该条为本项目明确约定")
		if !ok || n.Y < 0 || n.Y+n.H > 610 {
			t.Fatal("checkbox cannot be reached", n)
		}
		writeScreenshot(t, tt, fmt.Sprintf("e06-comparison-bottom-%d.png", width))
		s.Draft.ProjectQuote = c.ProjectRecord.Text
		s.Draft.PreferenceQuote = c.PreferenceRecord.Text
		s.Draft.Conditions = "明确适用说明"
		if validPreferenceDraft(s) {
			t.Fatal("unchecked project rule accepted")
		}
		s.Draft.Rule = true
		if !validPreferenceDraft(s) {
			t.Fatal("valid exact evidence rejected")
		}
		s.Draft.ProjectQuote = "不存在的引文"
		if validPreferenceDraft(s) {
			t.Fatal("invented quote accepted")
		}
		a.closeProjectPreference()
		if a.query != "保留查询" || a.draft != "原有草稿" || a.detailScroll.Y != 31 || a.selectedID != c.ProjectRecord.Memory {
			t.Fatal("return changed original context")
		}
	}
}
func TestProjectPreferenceUIUnknownReceiptReplaysDurableExactRequest(t *testing.T) {
	db, c := preferenceUIRecords(t)
	ctx := context.Background()
	requests := []records.ProjectPreferenceConfirmation{}
	journalRoot := ""
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "project_preference_confirm" {
			return serviceipc.Response{Error: "unexpected operation"}
		}
		q := *r.ProjectPreferenceConfirmation
		saved, err := preferenceIntent(ctx, journalRoot, q.LibraryIdentity, q.Query.ProjectRecord, "read", nil)
		if err != nil || saved == nil || !reflect.DeepEqual(*saved, q) {
			return serviceipc.Response{Error: "not durable before send"}
		}
		requests = append(requests, q)
		d, err := db.ConfirmProjectPreference(ctx, q)
		if err != nil {
			return serviceipc.Response{Code: "invalid_request", Error: err.Error()}
		}
		if len(requests) == 1 {
			return serviceipc.Response{Error: "synthetic receipt lost"}
		}
		return serviceipc.Response{OK: true, Result: d}
	})
	// Only the test handler reads this root, assigned before any request starts.
	journalRoot = a.library.Root
	s := preferenceUIPanel(a, c)
	q := preferenceUIRequest(c)
	s.Draft = &PreferenceDecisionDraft{ProjectQuote: q.ProjectQuote, PreferenceQuote: q.PreferenceQuote, Conditions: q.Conditions, Rule: true}
	a.confirmPreferenceDecision(s)
	awaitProcessingDispatch(t, updates)
	if s.Pending == nil || s.Receipt != nil || s.KnownRejected || s.Error == "" {
		t.Fatal("unknown receipt lost pending request")
	}
	first := *s.Pending
	s.Draft.Conditions = "changed UI must not change frozen request"
	a.closeProjectPreference()
	a.confirmPreferenceDecision(s)
	awaitProcessingDispatch(t, updates)
	if s.Receipt == nil || len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) || !reflect.DeepEqual(first, requests[1]) {
		t.Fatal("retry changed operation or duplicated intent")
	}
	var count int
	if err := db.DB.QueryRow("SELECT count(*) FROM project_preference_decisions").Scan(&count); err != nil || count != 1 {
		t.Fatal("decision duplicate", err, count)
	}
	for _, v := range []records.Version{c.ProjectRecord, c.PreferenceRecord} {
		current, err := db.Current(ctx, v.Memory)
		if err != nil || current.Revision != v.Revision || current.Text != v.Text {
			t.Fatal("confirmation modified knowledge", err)
		}
	}
}

func TestProjectPreferenceUIEpochRejectsLateConfirmation(t *testing.T) {
	_, c := preferenceUIRecords(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		q := *r.ProjectPreferenceConfirmation
		close(entered)
		<-release
		return serviceipc.Response{OK: true, Result: records.ProjectPreferenceDecision{ID: q.OperationID, LibraryIdentity: q.LibraryIdentity, Project: q.Query.Project, ProjectRecord: q.Query.ProjectRecord, PreferenceRecord: q.Query.PreferenceRecord, ProjectQuote: q.ProjectQuote, PreferenceQuote: q.PreferenceQuote, Conditions: q.Conditions, Source: "user", State: "confirmed", Decision: "use_project", ConfirmedMS: time.Now().UnixMilli()}}
	})
	s := preferenceUIPanel(a, c)
	q := preferenceUIRequest(c)
	s.Draft = &PreferenceDecisionDraft{ProjectQuote: q.ProjectQuote, PreferenceQuote: q.PreferenceQuote, Conditions: q.Conditions, Rule: true}
	a.confirmPreferenceDecision(s)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request absent")
	}
	a.libraryEpoch++
	a.retireProjectPreference()
	a.draft = "新库草稿"
	close(release)
	awaitProcessingDispatch(t, updates)
	if a.projectPreference.Panel != nil || a.draft != "新库草稿" || s.Receipt != nil {
		t.Fatal("late receipt polluted new library")
	}
}
func TestProjectPreferenceIntentArchiveAndStrictIdentity(t *testing.T) {
	_, c := preferenceUIRecords(t)
	root := t.TempDir()
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		q := preferenceUIRequest(c)
		if _, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "save", &q); err != nil {
			t.Fatal(err)
		}
		changed := q
		changed.Conditions = "changed"
		if _, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "save", &changed); err == nil {
			t.Fatal("same operation changed payload")
		}
		if _, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "retire", &q); err != nil {
			t.Fatal(err)
		}
		if _, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "retire", &q); err != nil {
			t.Fatal("uncertain retirement not replayable", err)
		}
		if _, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "save", &q); err == nil {
			t.Fatal("archived operation reactivated")
		}
	}
	q := preferenceUIRequest(c)
	_, err := preferenceIntent(ctx, root, c.LibraryIdentity, q.Query.ProjectRecord, "save", &q)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	got, err := preferenceIntent(ctx, root, other, q.Query.ProjectRecord, "read", nil)
	if err != nil || got != nil {
		t.Fatal("cross-library request recovered", err)
	}
	files, err := os.ReadDir(filepath.Join(root, "project-preference-intents"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil || info.Mode().Perm() != 0600 || info.Size() > preferenceIntentLimit {
			t.Fatal("nonprivate/unbounded journal", err, info)
		}
	}
	directory := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(directory, "project-preference-intents")); err != nil {
		t.Fatal(err)
	}
	if _, err := preferenceIntent(ctx, directory, c.LibraryIdentity, q.Query.ProjectRecord, "read", nil); err == nil {
		t.Fatal("symlink directory followed")
	}
}
func TestProjectPreferenceDraftByteBoundsAndUserBadge(t *testing.T) {
	_, c := preferenceUIRecords(t)
	s := &ProjectPreferencePanel{Comparison: &c, Draft: &PreferenceDecisionDraft{ProjectQuote: c.ProjectRecord.Text, PreferenceQuote: c.PreferenceRecord.Text, Conditions: strings.Repeat("中", 1366), Rule: true}}
	if validPreferenceDraft(s) {
		t.Fatal("byte limit treated as rune limit")
	}
	q := preferenceUIRequest(c)
	d := records.ProjectPreferenceDecision{ID: q.OperationID, LibraryIdentity: c.LibraryIdentity, Project: c.Project.ID, ProjectRecord: q.Query.ProjectRecord, PreferenceRecord: q.Query.PreferenceRecord, ProjectQuote: q.ProjectQuote, PreferenceQuote: q.PreferenceQuote, Conditions: q.Conditions, Source: "agent", Decision: "use_project", State: "confirmed", ConfirmedMS: 1}
	if validPreferenceDecision(d, c, &q) {
		t.Fatal("agent fact promoted to user confirmation")
	}
	d.Source = "user"
	if !validPreferenceDecision(d, c, &q) {
		t.Fatal("exact user receipt rejected")
	}
	d.PreferenceRecord.Revision++
	if validPreferenceDecision(d, c, &q) {
		t.Fatal("old badge moved to new version")
	}
	if preferenceKnownRejection(errors.New("socket failed")) {
		t.Fatal("unknown transport became known rejection")
	}
}

func TestProjectPreferenceUIRealChoicesPagingAndExplicitRecovery(t *testing.T) {
	db, c := preferenceUIRecords(t)
	ctx := context.Background()
	for i := 0; i < 31; i++ {
		if _, err := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: c.PreferenceRecord.Dataset, Text: fmt.Sprintf("可选择偏好 %d", i), Title: fmt.Sprintf("偏好 %d", i), Source: "测试用户保存"}); err != nil {
			t.Fatal(err)
		}
	}
	confirms := 0
	lists := []records.ListQuery{}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		var result any
		var err error
		switch r.Op {
		case "scopes":
			result, err = db.Scopes(ctx)
		case "list":
			lists = append(lists, r.ListQuery)
			result, err = db.List(ctx, r.ListQuery)
		case "project_preference_decisions":
			result, err = db.ProjectPreferenceDecisions(ctx, *r.ProjectPreferenceDecisionsQuery)
		case "project_preference_comparison":
			result, err = db.ProjectPreferenceComparison(ctx, *r.ProjectPreferenceQuery)
		case "project_preference_confirm":
			confirms++
			return serviceipc.Response{Error: "opening must never confirm"}
		default:
			return serviceipc.Response{Error: "unexpected"}
		}
		if err != nil {
			return serviceipc.Response{Code: "invalid_request", Error: err.Error()}
		}
		return serviceipc.Response{OK: true, Result: result}
	})
	s := preferenceUIPanel(a, c)
	s.Comparison = nil
	s.Preference = records.UsageTarget{}
	a.openProjectPreference(*a.selectedItem)
	awaitProcessingDispatch(t, updates)
	if len(s.Items) != 30 || s.Next == "" || s.Comparison != nil || s.Preference.Memory != "" || s.Draft.Rule || confirms != 0 {
		t.Fatal("choice auto-selected or ignored actual pagination", len(s.Items), s.Error)
	}
	s.Cursor = s.Next
	a.loadPreferenceChoices(s)
	awaitProcessingDispatch(t, updates)
	if len(s.Items) != 2 || s.Next != "" || len(lists) != 2 || lists[1].Cursor == "" || lists[1].Kind != "knowledge" || lists[1].ScopeKind != "preference" || !reflect.DeepEqual(lists[1].Datasets, []string{c.PreferenceRecord.Dataset}) {
		t.Fatal("paging widened kind/scope", lists, s.Error)
	}
	s.Submitted = "不存在的搜索匹配"
	s.Cursor = ""
	a.loadPreferenceChoices(s)
	awaitProcessingDispatch(t, updates)
	if len(s.Items) != 0 || s.Comparison != nil {
		t.Fatal("empty search fabricated pair")
	}
	// A durable unknown request is restored only after authoritative identity read;
	// reopening issues Comparison reads, never Confirm.
	q := preferenceUIRequest(c)
	if _, err := preferenceIntent(ctx, s.IntentRoot, c.LibraryIdentity, s.Project, "save", &q); err != nil {
		t.Fatal(err)
	}
	a.retireProjectPreference()
	a.openProjectPreference(*a.selectedItem)
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	s = a.projectPreference.Panel
	if s.Pending == nil || !reflect.DeepEqual(*s.Pending, q) || s.Comparison == nil || s.Receipt != nil || confirms != 0 {
		t.Fatal("reopen lost frozen request or silently sent", s.Error, confirms)
	}
	// Known server confirmation is a read-only independent badge, never an adopt.
	if _, err := db.ConfirmProjectPreference(ctx, q); err != nil {
		t.Fatal(err)
	}
	a.openProjectPreference(*a.selectedItem)
	awaitProcessingDispatch(t, updates)
	if s.Receipt == nil || s.Receipt.Source != "user" || confirms != 0 {
		t.Fatal("committed unknown receipt not recovered via read")
	}
	// Preference edit retires the old pair; old operation cannot move to v2.
	if _, err := db.Apply(ctx, records.Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: q.Query.PreferenceRecord.Memory, Dataset: q.Query.PreferenceRecord.Dataset, ExpectedRevision: 1, Text: "偏好新版本"}); err != nil {
		t.Fatal(err)
	}
	a.openProjectPreference(*a.selectedItem)
	if s.Receipt != nil || s.Comparison != nil {
		t.Fatal("old badge shown during current-version verification")
	}
	awaitProcessingDispatch(t, updates)
	if s.Receipt != nil || s.Comparison != nil || !s.KnownRejected || s.Pending == nil {
		t.Fatal("old confirmation promoted after source change", s.Error)
	}
}

func TestProjectPreferenceUILocalFailureNeverSendsAndRetainsOperation(t *testing.T) {
	_, c := preferenceUIRecords(t)
	calls := 0
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		calls++
		return serviceipc.Response{Error: "must not send"}
	})
	s := preferenceUIPanel(a, c)
	s.IntentRoot = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(s.IntentRoot, []byte("block directory"), 0600); err != nil {
		t.Fatal(err)
	}
	q := preferenceUIRequest(c)
	s.Draft = &PreferenceDecisionDraft{ProjectQuote: q.ProjectQuote, PreferenceQuote: q.PreferenceQuote, Conditions: q.Conditions, Rule: true}
	a.confirmPreferenceDecision(s)
	awaitProcessingDispatch(t, updates)
	first := s.Pending
	if calls != 0 || first == nil || s.Error == "" || s.KnownRejected {
		t.Fatal("local failure dropped frozen intent or sent request")
	}
	a.confirmPreferenceDecision(s)
	awaitProcessingDispatch(t, updates)
	if s.Pending != first || calls != 0 {
		t.Fatal("retry after local failure replaced original operation")
	}
}
func TestProjectPreferenceIntentRejectsMalformedPrivateFile(t *testing.T) {
	_, c := preferenceUIRecords(t)
	root := t.TempDir()
	directory := filepath.Join(root, "project-preference-intents")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s-%s-%d.json", c.LibraryIdentity, c.ProjectRecord.Memory, c.ProjectRecord.Revision)
	path := filepath.Join(directory, name)
	for _, raw := range [][]byte{[]byte(`{"format":1,"requests":null}`), []byte(`{"format":1,"format":1}`), []byte(strings.Repeat("x", preferenceIntentLimit+1)), {0xff}} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := preferenceIntent(context.Background(), root, c.LibraryIdentity, preferenceTarget(c.ProjectRecord), "read", nil); err == nil {
			t.Fatal("malformed intent accepted")
		}
		preserved, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(raw, preserved) {
			t.Fatal("failed read touched original intent", err)
		}
	}
}
func TestProjectPreferenceUIManualPreferenceEditorPreservesProjectContext(t *testing.T) {
	_, c := preferenceUIRecords(t)
	a := New("test", nil)
	s := preferenceUIPanel(a, c)
	a.memoryRoute = "项目:" + c.Project.ID
	a.recordKind = "knowledge"
	a.detailMode = "view"
	a.readingSearch = "项目内查询"
	a.readingFilter = "全部"
	a.detailScroll.Y = 91
	a.drafts[c.ProjectRecord.Memory] = Draft{Title: "项目未存草稿", Text: "项目独立草稿", Revision: 1}
	a.editComparedPreference(s)
	if a.page != "通用知识" || !a.editing || a.selectedID != c.PreferenceRecord.Memory || a.draft != c.PreferenceRecord.Text || a.drafts[c.PreferenceRecord.Memory].Revision != c.PreferenceRecord.Revision {
		t.Fatal("preference editing bypassed manual versioned editor")
	}
	a.draft = "偏好编辑待保存"
	tt := ui.NewTester(a.View, 800, 640)
	if err := tt.Click("返回项目对照（保留偏好草稿）"); err != nil {
		t.Fatal(err)
	}
	if a.page != "项目" || a.scope != c.Project.ID || a.selectedID != c.ProjectRecord.Memory || a.readingSearch != "项目内查询" || a.detailScroll.Y != 91 || a.drafts[c.ProjectRecord.Memory].Text != "项目独立草稿" || a.drafts[c.PreferenceRecord.Memory].Text != "偏好编辑待保存" {
		t.Fatal("editor return lost query, selection, scroll or independent drafts")
	}
}

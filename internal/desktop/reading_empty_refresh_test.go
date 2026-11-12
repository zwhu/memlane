package desktop

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type emptyListReadFixture struct {
	Code             string
	Started, Release chan struct{}
}

func TestConfirmedEmptyListRefreshKeepsTextAndGeometry(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		for _, branch := range []string{"editor", "project_guide", "filtered"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], branch), func(t *testing.T) {
				var state atomic.Pointer[emptyListReadFixture]
				state.Store(&emptyListReadFixture{})
				zero, knowledge := 0, 0
				if branch == "filtered" {
					knowledge = 1
				}
				a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
					switch r.Op {
					case "scopes":
						return serviceipc.Response{OK: true, Result: []records.Scope{{ID: "empty-project", Name: "Empty project", Kind: "project"}}}
					case "list":
						s := state.Load()
						if s.Started != nil {
							close(s.Started)
							<-s.Release
						}
						if s.Code != "" {
							return serviceipc.Response{Code: s.Code, Error: "synthetic list failure"}
						}
						return serviceipc.Response{OK: true, Result: records.Page{KnowledgeTotal: &knowledge, RawTotal: &zero, Items: []records.Version{}, Total: 0}}
					case "activities":
						return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
					case "cognee_bindings":
						return serviceipc.Response{OK: true, Result: []records.CogneeBinding{}}
					}
					return serviceipc.Response{Code: "unsupported"}
				})
				a.page, a.scope, a.recordKind, a.detailMode = "项目", "empty-project", "knowledge", "view"
				a.browsing.View = "memories"
				a.projects = []ProjectItem{{ID: a.scope, Name: "Empty project"}}
				message := "列表为空"
				if branch == "editor" {
					a.recordKind = "raw"
					a.editing = true
					a.detailMode = "new"
					a.draft = "Unsaved local draft"
				}
				if branch == "project_guide" {
					message = "还没有项目知识"
				}
				if branch == "filtered" {
					a.readingFilter = "no-match"
					message = "没有匹配的内容"
				}
				a.refresh(false)
				tt := ui.NewTester(a.View, size[0], size[1])
				if !tt.HasText("正在读取内容") || tt.HasText(message) {
					t.Fatal("unknown first read incorrectly shown as confirmed empty")
				}
				awaitProcessingDispatch(t, dispatch)
				tt.Frame()
				anchor, ok := tt.Find(message)
				if !ok || !a.hasCurrentListSnapshot() {
					t.Fatal("verified empty result absent")
				}
				preview := a.projectKnowledgePreviewVisible()
				check := func() {
					t.Helper()
					tt.Frame()
					rect, ok := tt.Find(message)
					if !ok || !reflect.DeepEqual(rect, anchor) || tt.HasText("正在读取内容") || a.projectKnowledgePreviewVisible() != preview {
						t.Fatalf("empty state changed: present=%v rect=%+v want=%+v loading=%v error=%q snapshot=%v preview=%v wantPreview=%v", ok, rect, anchor, a.loading, a.libraryError, a.hasCurrentListSnapshot(), a.projectKnowledgePreviewVisible(), preview)
					}
				}
				for n := 0; n < 3; n++ {
					s := &emptyListReadFixture{Started: make(chan struct{}), Release: make(chan struct{})}
					state.Store(s)
					a.refresh(false)
					select {
					case <-s.Started:
					case <-time.After(5 * time.Second):
						t.Fatal("controlled list request absent")
					}
					check()
					close(s.Release)
					awaitProcessingDispatch(t, dispatch)
					check()
				}
				state.Store(&emptyListReadFixture{Code: "service_unavailable"})
				a.refresh(false)
				awaitProcessingDispatch(t, dispatch)
				check()
				if !tt.HasText("刷新未完成") {
					t.Fatal("retained empty result concealed failed refresh")
				}
				state.Store(&emptyListReadFixture{})
				a.refresh(false)
				awaitProcessingDispatch(t, dispatch)
				check()
				if tt.HasText("刷新未完成") {
					t.Fatal("successful refresh did not clear fault feedback")
				}
				state.Store(&emptyListReadFixture{Code: "scope_denied"})
				a.refresh(false)
				awaitProcessingDispatch(t, dispatch)
				tt.Frame()
				if a.hasCurrentListSnapshot() || tt.HasText(message) || a.readingKnowledgeTotal != nil || !tt.HasText("读取未完成") {
					t.Fatal("definite access failure retained a confirmed-empty claim")
				}
				if branch == "editor" && a.draft != "Unsaved local draft" {
					t.Fatal("access withdrawal deleted unsaved user draft")
				}
			})
		}
	}
}

func TestEmptyListSnapshotCannotCrossQueryOrLibrary(t *testing.T) {
	a := New("test", nil)
	a.page, a.scope, a.recordKind = "项目", "project-a", "knowledge"
	a.listReadSnapshot = a.readingListSnapshotKey()
	if !a.hasCurrentListSnapshot() {
		t.Fatal("snapshot fixture invalid")
	}
	for _, change := range []func(){func() { a.scope = "project-b" }, func() { a.readingFilter = "other query" }, func() { a.recordKind = "raw" }, func() { a.library.Root = "another-root" }} {
		change()
		if a.hasCurrentListSnapshot() {
			t.Fatal("empty result reused across a different query boundary")
		}
		a.listReadSnapshot = a.readingListSnapshotKey()
	}
}

func TestFirstListFailureDoesNotClaimAnEmptyResult(t *testing.T) {
	for _, code := range []string{"service_unavailable", "scope_denied"} {
		t.Run(code, func(t *testing.T) {
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op == "scopes" {
					return serviceipc.Response{OK: true, Result: []records.Scope{{ID: "first-project", Name: "First project", Kind: "project"}}}
				}
				return serviceipc.Response{Code: code, Error: "synthetic first-read failure"}
			})
			a.page, a.scope, a.recordKind, a.detailMode = "项目", "first-project", "knowledge", "view"
			a.browsing.View = "memories"
			a.refresh(false)
			awaitProcessingDispatch(t, dispatch)
			tt := ui.NewTester(a.View, 1200, 820)
			if a.hasCurrentListSnapshot() || tt.HasText("列表为空") || tt.HasText("还没有项目知识") || !tt.HasText("读取未完成") {
				t.Fatal("failed first read was presented as verified empty")
			}
		})
	}
}

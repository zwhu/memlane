package desktop

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func officialDocumentTestState(a *App) (records.OfficialDocument, *officialDocumentView) {
	conversationTestState(a)
	row := records.OfficialDocument{ID: "synthetic-document", Dataset: a.conversations.Binding.Dataset, Name: "Official synthetic.txt", MIMEType: "text/plain"}
	content := records.OfficialDocumentText{Document: row, Text: strings.Repeat("SYNTHETIC official original paragraph.\n\n", 40)}
	v := &officialDocumentView{Binding: *a.conversations.Binding, Content: &content, Window: newTextWindow(content.Text)}
	a.conversations.Documents = &officialDocumentsUI{Loaded: true, Selected: row.ID, SelectedRow: &row, Rows: []records.OfficialDocument{row}, Views: map[string]*officialDocumentView{row.ID: v}, Pages: map[int]records.OfficialDocumentPage{}}
	return row, v
}

func TestOfficialDocumentCatalogPaginationAndChangedMetadataWithdrawOldCopy(t *testing.T) {
	var binding records.CogneeBinding
	var first atomic.Pointer[records.OfficialDocumentPage]
	var last records.OfficialDocumentPage
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{binding}}
		case "official_documents":
			if r.DocumentQuery.Offset == 0 {
				return serviceipc.Response{OK: true, Result: *first.Load()}
			}
			return serviceipc.Response{OK: true, Result: last}
		case "official_document_text":
			return serviceipc.Response{OK: true, Result: records.OfficialDocumentText{Document: *r.DocumentQuery.Selected, Text: "New official source body"}}
		}
		return serviceipc.Response{Code: "unsupported"}
	})
	row, view := officialDocumentTestState(a)
	binding = *a.conversations.Binding
	d := a.conversations.Documents
	page := records.OfficialDocumentPage{Limit: 20, HasMore: true, Documents: []records.OfficialDocument{row}}
	for i := 1; i < 20; i++ {
		page.Documents = append(page.Documents, records.OfficialDocument{ID: fmt.Sprintf("document-%d", i), Dataset: row.Dataset, Name: fmt.Sprintf("doc%d.txt", i)})
	}
	first.Store(&page)
	last = records.OfficialDocumentPage{Limit: 20, Offset: 20, Documents: []records.OfficialDocument{{ID: "last-document", Dataset: row.Dataset, Name: "Last.txt"}}}
	a.refreshOfficialDocuments(false)
	awaitProcessingDispatch(t, dispatch)
	if len(d.Rows) != 20 || d.Next != 20 || !d.HasMore {
		t.Fatal("initial catalog cursor was lost")
	}
	a.refreshOfficialDocuments(true)
	awaitProcessingDispatch(t, dispatch)
	if len(d.Rows) != 21 || d.Next != 40 || d.HasMore {
		t.Fatal("official document pagination was not preserved")
	}
	updated := page
	updated.Documents = append([]records.OfficialDocument(nil), page.Documents...)
	updated.Documents[0].Name = "Changed official.txt"
	first.Store(&updated)
	a.refreshOfficialDocuments(false)
	awaitProcessingDispatch(t, dispatch)
	if view.Content != nil || view.Window != nil {
		t.Fatal("changed catalog metadata left old source copy available")
	}
	if _, available := a.currentOfficialDocument(); available {
		t.Fatal("changed source remained copyable")
	}
	awaitProcessingDispatch(t, dispatch)
	if view.Content == nil || view.Content.Text != "New official source body" || view.Content.Document.Name != "Changed official.txt" {
		t.Fatal("verified new source did not replace the old document")
	}
}

func TestOfficialDocumentQueuedCatalogCannotRestoreRevokedAccess(t *testing.T) {
	var binding records.CogneeBinding
	var row records.OfficialDocument
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{binding}}
		case "official_documents":
			return serviceipc.Response{OK: true, Result: records.OfficialDocumentPage{Documents: []records.OfficialDocument{row}, Limit: 20}}
		default:
			return serviceipc.Response{Code: "scope_denied", Error: "revoked"}
		}
	})
	row, _ = officialDocumentTestState(a)
	binding = *a.conversations.Binding
	d := a.conversations.Documents
	a.refreshOfficialDocuments(false)
	var oldCatalog func()
	select {
	case oldCatalog = <-dispatch:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog response absent")
	}
	a.refreshSelectedOfficialDocument()
	awaitProcessingDispatch(t, dispatch)
	oldCatalog()
	if a.conversations.Binding != nil || len(d.Rows) != 0 || d.Selected != "" {
		t.Fatal("queued catalog restored an explicitly revoked document grant")
	}
}

func TestOfficialDocumentViewIsReadOnlyRawAndCopiesOriginalAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		a := New("test", nil)
		_, view := officialDocumentTestState(a)
		tt := ui.NewTester(a.View, size[0], size[1])
		if !tt.HasText("原始文档") || !tt.HasText("Official synthetic.txt") || tt.HasText("编辑知识") || tt.HasText("保存知识") || !tt.HasText("官方文档不等于已提炼知识，当前未提供处理状态。") {
			t.Fatal("raw document was promoted into knowledge")
		}
		if err := tt.Click("复制文档"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != view.Content.Text {
			t.Fatal("document original changed during copy")
		}
	}
}

func TestOfficialDocumentRefreshKeepsWindowButRevocationWithdrawsBothModes(t *testing.T) {
	var code atomic.Value
	code.Store("")
	var response records.OfficialDocumentText
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if failure := code.Load().(string); failure != "" {
			return serviceipc.Response{Code: failure, Error: "private diagnostic must not enter document"}
		}
		return serviceipc.Response{OK: true, Result: response}
	})
	_, view := officialDocumentTestState(a)
	view.Content.Text = strings.Repeat(view.Content.Text, 12)
	view.Window = newTextWindow(view.Content.Text)
	view.Window.Page = 1
	response = *view.Content
	d := a.conversations.Documents
	d.Query = "Official"
	view.Info = true
	tt := ui.NewTester(a.View, 800, 620)
	view.Scroll.Y = 80
	tt.Frame()
	if view.Scroll.Y != 80 {
		t.Fatal("document fixture not scrollable")
	}
	window := view.Window
	for _, failure := range []string{"", "timeout", "scope_denied"} {
		code.Store(failure)
		a.refreshSelectedOfficialDocument()
		awaitProcessingDispatch(t, dispatch)
		tt.Frame()
		if failure == "timeout" {
			a.refreshOfficialDocuments(false)
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if !tt.HasText("已载入 1 篇文档") {
				t.Fatal("temporary catalog failure hid the loaded document count")
			}
			if !tt.HasText(fmt.Sprintf("第 2 / %d 段", len(window.Offsets)-1)) {
				t.Fatal("temporary document failure hid the current reading segment")
			}
		}
		if failure == "scope_denied" {
			if d.Selected != "" || len(d.Rows) != 0 || a.conversations.Binding != nil || len(a.conversations.Views) != 0 {
				t.Fatal("revoked raw or hidden session data retained")
			}
			fresh := ui.NewTester(a.View, 800, 620)
			_ = fresh.Click("复制文档")
			if fresh.Clipboard() != "" {
				t.Fatal("revoked document was copied")
			}
		} else if view.Window != window || view.Scroll.Y != 80 || view.Content == nil || !view.Info || d.Query != "Official" {
			t.Fatal("refresh replaced reading state")
		}
	}
}

func TestOfficialDocumentChangedReceiptAndLateResponseCannotRestoreText(t *testing.T) {
	for _, late := range []bool{false, true} {
		var response records.OfficialDocumentText
		a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response { return serviceipc.Response{OK: true, Result: response} })
		_, view := officialDocumentTestState(a)
		response = *view.Content
		if !late {
			response.Document.Name = "changed upstream.txt"
		}
		a.refreshSelectedOfficialDocument()
		if late {
			var callback func()
			select {
			case callback = <-dispatch:
			case <-time.After(5 * time.Second):
				t.Fatal("document response absent")
			}
			a.retireConversations()
			callback()
			if a.conversations.Documents != nil {
				t.Fatal("late success restored retired document view")
			}
		} else {
			awaitProcessingDispatch(t, dispatch)
			if view.Content != nil || view.Window != nil || view.ErrorCode != "stale_source" {
				t.Fatal("changed source was accepted")
			}
		}
	}
}

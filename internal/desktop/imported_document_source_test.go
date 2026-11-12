package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestImportedDocumentSourceReadingReturnRefreshAndRevocation(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			var failure atomic.Value
			failure.Store("")
			var receipt records.OfficialDocumentSource
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op != "official_document_source" || r.DocumentQuery == nil || r.DocumentQuery.SourceDocument != receipt.Content.Document.ID {
					return serviceipc.Response{Code: "unsupported"}
				}
				if code := failure.Load().(string); code != "" {
					return serviceipc.Response{Code: code, Error: "private diagnostic"}
				}
				return serviceipc.Response{OK: true, Result: receipt}
			})
			d, view := officialKnowledgeTestState(a)
			const library = "55dbbc14-3185-47e7-a870-547463c2ab5f"
			const project = "39dac5b0-dca9-4efb-915d-ef572c81904c"
			const target = "e6a5ca85-2360-4d11-a4aa-78161f632096"
			view.Content.SessionReference = ""
			hash := sha256.Sum256([]byte(view.Content.Text))
			ref := records.OfficialDocumentReference{Document: target, Memory: target, Project: project, Revision: 1, Digest: strings.Repeat("a", 64)}
			view.Content.Document.Metadata, _ = json.Marshal(map[string]any{"memlane_import_v1": map[string]any{"library": library, "project": project, "memory": library, "import_id": library, "revision": 1, "origin": "knowledge", "digest": hex.EncodeToString(hash[:]), "sources": []records.OfficialDocumentReference{ref}}})
			view.Knowledge.Content = *view.Content
			row := view.Content.Document
			d.SelectedRow, d.Rows = &row, []records.OfficialDocument{row}
			receipt = records.OfficialDocumentSource{Document: row, Reference: ref, Content: records.OfficialDocumentText{Document: records.OfficialDocument{ID: target, Dataset: row.Dataset, Name: "Preserved source"}, Text: strings.Repeat("IMPORTED_SOURCE_BODY\n\n", 100)}}
			title := "Original preserved title"
			receipt.Content.Document.Label = &title
			tt := ui.NewTester(a.View, size[0], size[1])
			view.Scroll.Y, d.Query = 100, "atomic"
			tt.Frame()
			if view.Scroll.Y != 100 {
				t.Fatal("knowledge fixture not scrollable")
			}
			a.openImportedDocumentSource(target)
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			source := d.Source
			if source == nil || source.Imported == nil || !tt.HasText("IMPORTED_SOURCE_BODY") || !tt.HasText(title) || tt.HasText("Preserved source") {
				t.Fatal("preserved source not displayed")
			}
			if err := tt.Click("复制原文"); err != nil || tt.Clipboard() != receipt.Content.Text {
				t.Fatal("wrong source copy", err)
			}
			source.Imported.Scroll.Y = 80
			reading := source.Imported
			failure.Store("timeout")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if source.Imported != reading || reading.Scroll.Y != 80 || !tt.HasText("IMPORTED_SOURCE_BODY") {
				t.Fatal("transient failure lost reading")
			}
			if err := tt.Click("返回知识"); err != nil {
				t.Fatal(err)
			}
			if d.Source != nil || view.Scroll.Y != 100 || d.Query != "atomic" {
				t.Fatal("return lost knowledge context")
			}
			failure.Store("")
			a.openImportedDocumentSource(target)
			awaitProcessingDispatch(t, dispatch)
			failure.Store("source_not_found")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if d.Source.Imported != nil || tt.HasText("IMPORTED_SOURCE_BODY") {
				t.Fatal("invalid source retained")
			}
			failure.Store("connection_revoked")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if d.Source != nil || len(d.Views) != 0 || len(d.Rows) != 0 || len(d.KnowledgePages) != 0 || tt.HasText("IMPORTED_SOURCE_BODY") {
				t.Fatal("revoked data retained")
			}
		})
	}
}

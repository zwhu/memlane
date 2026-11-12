package desktop

import (
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestRestoredProjectSelectionKeepsListAndDetailContentTypeTogether(t *testing.T) {
	for _, origin := range []string{"raw", "knowledge"} {
		t.Run(origin, func(t *testing.T) {
			kinds := make(chan string, 4)
			v := records.Version{Memory: "selected", Dataset: "project", Origin: origin, Revision: 1, State: "active", Text: "retained material"}
			a, updates := processingUIFixture(t, func(req serviceipc.Request) serviceipc.Response {
				var result any = map[string]any{}
				switch req.Op {
				case "scopes":
					result = []records.Scope{{ID: "project", Kind: "project", Name: "Project"}}
				case "list":
					kinds <- req.ListQuery.Kind
					result = records.Page{Items: []records.Version{v}}
				case "get":
					result = v
				case "history":
					result = records.HistoryPage{}
				case "organization_sources":
					result = []records.OrganizationSource{}
				case "source_read":
					result = records.SourceRecord{State: "available", Record: v, QuoteOffset: -1}
				}
				return serviceipc.Response{OK: true, Result: result}
			})
			a.page, a.scope, a.selectedID, a.detailMode = "项目", "project", "selected", "view"
			a.recordKind = "raw"
			if origin == "raw" {
				a.recordKind = "knowledge"
			}
			a.refresh(false)
			awaitProcessingDispatch(t, updates)
			if a.libraryError != "" {
				t.Fatal(a.libraryError)
			}
			awaitProcessingDispatch(t, updates)
			if origin == "raw" {
				awaitProcessingDispatch(t, updates)
				if !a.reader.DirectRaw || !a.reader.Open || a.reader.Query.Memory != "selected" || a.reader.Query.Owner != "selected" || a.reader.Window == nil || a.reader.Window.Body != v.Text {
					t.Fatal("raw selection bypassed the verified reader")
				}
			}
			if a.recordKind != origin || a.selectedID != "selected" || a.current() == nil || a.current().Origin != origin {
				t.Fatalf("restored list/detail mismatch: kind=%s selected=%s item=%+v", a.recordKind, a.selectedID, a.current())
			}
			<-kinds
			if got := <-kinds; got != origin {
				t.Fatalf("reloaded wrong list: %s", got)
			}
		})
	}
}

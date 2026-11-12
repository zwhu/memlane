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

func TestOfficialDocumentSourceKeepsDocumentContextAndRechecksAccess(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			var failure atomic.Value
			failure.Store("")
			var receipt records.OfficialDocumentSession
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op != "official_document_session" || r.DocumentQuery == nil {
					return serviceipc.Response{Code: "unsupported"}
				}
				if code := failure.Load().(string); code != "" {
					return serviceipc.Response{Code: code, Error: "private diagnostic"}
				}
				return serviceipc.Response{OK: true, Result: receipt}
			})
			row, document := officialDocumentTestState(a)
			d := a.conversations.Documents
			d.Query, document.Info = "Official", true
			document.Content.Text = strings.Repeat(document.Content.Text, 12)
			document.Window = newTextWindow(document.Content.Text)
			document.Window.Page = 1
			tt := ui.NewTester(a.View, size[0], size[1])
			document.Scroll.Y = 80
			tt.Frame()
			if document.Scroll.Y != 80 || tt.HasText("阅读来源会话") {
				t.Fatal("fixture not scrollable or unmarked document gained a source")
			}
			a.openOfficialDocumentSource()
			if d.Source != nil {
				t.Fatal("unmarked document opened a guessed source")
			}
			document.Content.SessionReference = "official-source-session"
			receipt = records.OfficialDocumentSession{Document: row, Detail: records.SessionDetail{
				SessionSummary: records.SessionSummary{Session: document.Content.SessionReference, Dataset: row.Dataset, User: "official-user"},
				QAs:            []records.SessionQA{{ID: "source-qa", Question: "SOURCE_QUESTION", Answer: strings.Repeat("Source answer paragraph.\n\n", 50)}},
				Traces:         []records.SessionTrace{{ID: "source-tool", Function: "Source tool", Status: "success"}},
			}}
			window := document.Window
			a.openOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			source := d.Source
			reading := source.Reading.Views[source.Reading.Selected]
			if reading == nil || !tt.HasText("SOURCE_QUESTION") || !tt.HasText("返回文档") || !tt.HasText("仅关联来源会话，未定位具体问答或工具。") {
				t.Fatal("authorized source not displayed with its actual attribution boundary")
			}
			reading.Scroll.Y, reading.Tools = 80, true
			tt.Frame()
			if reading.Scroll.Y != 80 || !tt.HasText("Source tool") {
				t.Fatal("source reading fixture not scrollable or expandable")
			}
			if err := tt.Click("复制已载入内容"); err != nil || !strings.Contains(tt.Clipboard(), "SOURCE_QUESTION") {
				t.Fatal("source copy missing")
			}
			failure.Store("timeout")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if source.Reading.Views[source.Reading.Selected] != reading || reading.Scroll.Y != 80 || !reading.Tools || !tt.HasText("SOURCE_QUESTION") {
				t.Fatal("temporary source failure replaced valid reading state")
			}
			failure.Store("source_not_found")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if tt.HasText("SOURCE_QUESTION") || !tt.HasText("来源会话缺失、已过期或无法核验。文档原文保留，可返回文档查看。") {
				t.Fatal("missing source remained readable")
			}
			if err := tt.Click("返回文档"); err != nil {
				t.Fatal(err)
			}
			if d.Source != nil || document.Window != window || document.Window.Page != 1 || document.Scroll.Y != 80 || !document.Info || d.Query != "Official" {
				t.Fatal("return lost the original document context")
			}
			failure.Store("")
			a.openOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			failure.Store("scope_denied")
			a.refreshOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if d.Source != nil || len(d.Rows) != 0 || a.conversations.Binding != nil {
				t.Fatal("revoked grant retained source or original document access")
			}
		})
	}
}

func TestOfficialDocumentSourceRejectsQueuedReplyAfterReturn(t *testing.T) {
	var receipt records.OfficialDocumentSession
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: receipt}
	})
	row, document := officialDocumentTestState(a)
	document.Content.SessionReference = "source-session"
	receipt = records.OfficialDocumentSession{Document: row, Detail: records.SessionDetail{SessionSummary: records.SessionSummary{Session: "source-session", Dataset: row.Dataset, User: "source-user"}}}
	d := a.conversations.Documents
	a.openOfficialDocumentSource()
	var late func()
	select {
	case late = <-dispatch:
	case <-time.After(5 * time.Second):
		t.Fatal("source callback absent")
	}
	old := d.Source
	d.closeSource()
	late()
	if d.Source != nil || old.Reading.Active || len(old.Reading.Views) != 0 {
		t.Fatal("late source callback revived a closed route")
	}
}

func TestOfficialDocumentSourceRejectsMismatchedIdentityWithoutLosingOriginal(t *testing.T) {
	for _, mismatch := range []string{"document", "dataset", "session", "user"} {
		t.Run(mismatch, func(t *testing.T) {
			var receipt records.OfficialDocumentSession
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				return serviceipc.Response{OK: true, Result: receipt}
			})
			row, document := officialDocumentTestState(a)
			document.Content.SessionReference = "source-session"
			receipt = records.OfficialDocumentSession{Document: row, Detail: records.SessionDetail{
				SessionSummary: records.SessionSummary{Session: "source-session", Dataset: row.Dataset, User: "source-user"},
				QAs:            []records.SessionQA{{Question: "MISMATCH_MUST_NOT_DISPLAY"}},
			}}
			switch mismatch {
			case "document":
				receipt.Document.Name = "changed document"
			case "dataset":
				receipt.Detail.Dataset = "foreign-dataset"
			case "session":
				receipt.Detail.Session = "foreign-session"
			case "user":
				receipt.Detail.User = ""
			}
			a.openOfficialDocumentSource()
			awaitProcessingDispatch(t, dispatch)
			tt := ui.NewTester(a.View, 800, 620)
			if tt.HasText("MISMATCH_MUST_NOT_DISPLAY") || a.conversations.Documents.Source.Reading.SourceError != "stale_source" {
				t.Fatal("unverified source identity accepted")
			}
			if _, valid := a.currentOfficialDocument(); !valid {
				t.Fatal("source failure removed independently valid original")
			}
		})
	}
}

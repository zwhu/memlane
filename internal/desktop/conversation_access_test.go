package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
	"time"
)

func TestConversationReadCandidateRequiresExistingCurrentProjectGrant(t *testing.T) {
	b := records.CogneeBinding{Project: "project", ConnectionID: "connection", Generation: 1}
	c := records.Connection{ID: b.ConnectionID, Agent: "codex", Enabled: true, Generation: 3, AllowedScopes: []string{b.Project}}
	if conversationReadCandidateFor(b, []records.Connection{c}) == nil {
		t.Fatal("missing current project review")
	}
	for _, mode := range []string{"disabled", "same_generation", "other_connection", "other_agent", "no_scope", "submission"} {
		old, next := b, c
		switch mode {
		case "disabled":
			next.Enabled = false
		case "same_generation":
			next.Generation = 1
		case "other_connection":
			next.ID = "other"
		case "other_agent":
			next.Agent = "pi"
		case "no_scope":
			next.AllowedScopes = nil
		case "submission":
			old.MaterialSubmission = true
		}
		if conversationReadCandidateFor(old, []records.Connection{next}) != nil {
			t.Fatal("invalid read review candidate", mode)
		}
	}
}

func installConversationReviewFixture(a *App) {
	conversationTestState(a)
	s := &a.conversations
	old := *s.Binding
	old.Operation = "synthetic-prior-operation"
	old.Endpoint = "http://127.0.0.1:18099"
	old.ConnectionID = "synthetic-connection"
	old.Generation = 1
	s.Binding = nil
	s.Rows = nil
	s.Views = map[string]*conversationReadingState{}
	s.Selected = ""
	s.ErrorCode = "scope_denied"
	s.Loaded = true
	s.ReadCandidate = &conversationReadCandidate{Previous: old, Connection: records.Connection{ID: old.ConnectionID, Name: "Synthetic Codex", Agent: "codex", Generation: 3, Enabled: true, AllowedScopes: []string{old.Project}}}
}

func reviewedBinding(q records.CogneeReadReauthorization) records.CogneeBinding {
	b := q.Previous
	b.Operation = q.Operation
	b.Generation = q.Generation
	b.ReadPreviousOperation = q.Previous.Operation
	b.ReadReviewDigest = records.MergeDigest(q)
	return b
}

func TestConversationReadReviewDiscoveredFromActualGenerationError(t *testing.T) {
	old := records.CogneeBinding{Project: "synthetic-project", ConnectionID: "synthetic-connection", Instance: "synthetic-instance", Dataset: "synthetic-dataset", Generation: 1}
	fresh := records.Connection{ID: old.ConnectionID, Agent: "codex", Name: "Synthetic Codex", Enabled: true, Generation: 3, AllowedScopes: []string{old.Project}}
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{old}}
		case "official_sessions":
			return serviceipc.Response{Code: "reload_required", Error: "connection changed"}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{fresh}}
		default:
			t.Error("unexpected discovery operation", r.Op)
			return serviceipc.Response{Code: "invalid_request"}
		}
	})
	conversationTestState(a)
	a.refreshConversationCatalog(false)
	awaitProcessingDispatch(t, dispatch)
	if a.conversations.ReadCandidate == nil || a.conversations.ReadCandidate.Previous != old || a.conversations.Binding != nil || len(a.conversations.Rows) != 0 {
		t.Fatal("generation error did not withdraw body and offer exact review")
	}
	tt := ui.NewTester(a.View, 800, 620)
	if !tt.HasText("重新确认读取范围") {
		t.Fatal("review entry absent")
	}
}

func TestConversationReadReviewWaitsForConfirmationAndRetriesSameIntent(t *testing.T) {
	requests := make(chan serviceipc.Request, 4)
	attempt := 0
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "cognee_read_reauthorize" {
			return serviceipc.Response{Code: "service_unavailable", Error: "synthetic"}
		}
		requests <- r
		attempt++
		if attempt == 1 {
			return serviceipc.Response{Code: "cognee_unavailable", Error: "never show raw diagnostic"}
		}
		return serviceipc.Response{OK: true, Result: reviewedBinding(*r.CogneeReadReauthorization)}
	})
	installConversationReviewFixture(a)
	tt := ui.NewTester(a.View, 800, 620)
	if err := tt.Click("重新确认读取范围"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("确认会话读取范围") || !tt.HasText("Synthetic Codex") {
		t.Fatal("review scope absent")
	}
	select {
	case <-requests:
		t.Fatal("opening review changed authorization")
	default:
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.conversations.ReadReview != nil {
		t.Fatal("cancel did not close review")
	}
	_ = tt.Click("重新确认读取范围")
	intent := a.conversations.ReadReview.Intent
	_ = tt.Click("确认恢复读取")
	awaitProcessingDispatch(t, dispatch)
	first := <-requests
	if !first.UserConfirmed || a.conversations.ReadReview.Intent != intent || a.conversations.ReadReview.Busy || tt.HasText("never show raw diagnostic") {
		t.Fatal("failed confirmation lost reviewed scope")
	}
	tt.Frame()
	_ = tt.Click("确认恢复读取")
	awaitProcessingDispatch(t, dispatch)
	second := <-requests
	if *second.CogneeReadReauthorization != *first.CogneeReadReauthorization {
		t.Fatal("retry created another intent")
	}
	if a.conversations.ReadReview != nil || a.conversations.Binding == nil || *a.conversations.Binding != reviewedBinding(intent) {
		t.Fatal("verified read receipt not adopted")
	}
}

func TestConversationReadReviewRejectsLateAndMismatchedReceipt(t *testing.T) {
	for _, mode := range []string{"late", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				b := reviewedBinding(*r.CogneeReadReauthorization)
				if mode == "mismatch" {
					b.Generation++
				}
				return serviceipc.Response{OK: true, Result: b}
			})
			installConversationReviewFixture(a)
			a.openConversationReadReview()
			a.confirmConversationReadReview()
			var receipt func()
			select {
			case receipt = <-dispatch:
			case <-time.After(5 * time.Second):
				t.Fatal("review response absent")
			}
			if mode == "late" {
				a.retireConversations()
				a.page, a.scope = "总览", ""
			}
			receipt()
			if a.conversations.Binding != nil {
				t.Fatal("unverified or late receipt adopted")
			}
			if mode == "mismatch" && (a.conversations.ReadReview == nil || a.conversations.ReadReview.Message == "") {
				t.Fatal("mismatched receipt not explained")
			}
		})
	}
}

func TestCapturedReadReviewPreservesBindingAndDoesNotEnableCapture(t *testing.T) {
	requests := make(chan serviceipc.Request, 2)
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "cognee_read_reauthorize" {
			return serviceipc.Response{Code: "service_unavailable"}
		}
		requests <- r
		return serviceipc.Response{OK: true, Result: r.CogneeReadReauthorization.Previous}
	})
	installConversationReviewFixture(a)
	s := &a.conversations
	s.ReadCandidate.Previous.MaterialSubmission = true
	s.ReadCandidate.Connection.Agent = "pi"
	old := s.ReadCandidate.Previous
	if conversationReadCandidateFor(old, []records.Connection{s.ReadCandidate.Connection}) == nil {
		t.Fatal("current pi project could not review saved reads")
	}
	tt := ui.NewTester(a.View, 800, 620)
	_ = tt.Click("重新确认读取范围")
	if !tt.HasText("仅恢复读取本项目已确认保存的问答和工具记录；不会开启后续采集或读取其他历史会话。") {
		t.Fatal("captured-only review scope missing")
	}
	select {
	case <-requests:
		t.Fatal("opening review dispatched permission change")
	default:
	}
	_ = tt.Click("确认恢复读取")
	awaitProcessingDispatch(t, dispatch)
	r := <-requests
	if !r.UserConfirmed || r.Op != "cognee_read_reauthorize" || r.CogneeReadReauthorization.Previous != old || s.Binding == nil || *s.Binding != old {
		t.Fatal("captured read review changed capture binding")
	}
	if s.ReadReview != nil {
		t.Fatal("review did not close after exact receipt")
	}
	select {
	case r := <-requests:
		t.Fatal("review dispatched another operation", r.Op)
	default:
	}
}

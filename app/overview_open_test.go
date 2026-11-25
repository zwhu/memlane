package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestOverviewOpenLocatesSecondPageAndScrollsToTarget(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/datasets/ds/documents/58/body" {
			body := "Target knowledge body."
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: "58", DatasetID: "ds", Body: &body})
			return
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/projects/p/knowledge":
			if r.URL.Query().Get("anchor_doc_id") == "" {
				json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{})
				return
			}
			pages.Add(1)
			if r.URL.Query().Get("anchor_doc_id") != "58" || r.URL.Query().Get("limit") != "30" {
				t.Error("locator did not request the target's bounded page", r.URL)
			}
			resp := v2ProjectKnowledgeResponse{DatasetID: "ds", HasPrev: true, PrevCursor: "page-1", TotalFiltered: 60}
			for n := 30; n < 60; n++ {
				resp.Items = append(resp.Items, v2KnowledgeItem{DocID: fmt.Sprint(n), DatasetID: "ds", Title: fmt.Sprintf("Knowledge %d", n), PreviewText: fmt.Sprintf("Knowledge %d", n)})
			}
			json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Name: "project", Dataset: "ds"}}}, nil)
	a.access.Loaded = true
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.access.Next = time.Now().Add(time.Hour)
	d := Document{ID: "58", Dataset: "ds", Name: "Knowledge 58", Metadata: map[string]any{"node_set": []string{"session_learnings"}}}
	a.openOverviewItem(Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.Name, Document: &d})
	s := a.state()
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if !s.Knowledge.HasPrev || s.Knowledge.PrevCursor != "page-1" || s.Selected == nil || s.Selected.ID != "58" || s.Error != "" || !containsReadingItem(s.Items, s.Selected.Key()) {
		t.Fatalf("overview did not locate the target page: offset=%d selected=%v error=%s", s.Offset, s.Selected, s.Error)
	}
	if pages.Load() != 1 || s.Selected.Body != "Target knowledge body." {
		t.Fatal("target page was never read")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Advance(time.Second)
	tt.Frame()
	r, ok := tt.Find("Knowledge 58")
	if !ok || r.H <= 0 || r.Y < 96 || r.Y+r.H > 800 || s.ListSelection != 28 {
		t.Fatalf("target is not visible and selected: rect=%+v listSelection=%d texts=%v", r, s.ListSelection, tt.Texts())
	}
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if s.Selected == nil || s.Selected.ID != "58" || s.Error != "" {
		t.Fatal("refresh lost a located target", s.Error)
	}
}

func TestOverviewTitlesCoverAllTwentyDisplayedRows(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		fmt.Fprint(w, "A real readable knowledge title.")
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	updates := make(chan func(), 25)
	a.Update = func(fn func()) { updates <- fn }
	for n := 0; n < 20; n++ {
		d := Document{ID: fmt.Sprint(n), Dataset: "ds", Name: "text_0123456789abcdef0123456789abcdef"}
		a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d})
	}
	a.startRecentTitles(a.overviewState.Snapshot.Knowledge)
	drainReading(t, updates, func() bool {
		for _, row := range a.overviewState.Snapshot.Knowledge {
			if row.ReadingState != "ready" {
				return false
			}
		}
		return true
	})
	if reads.Load() != 20 || a.overviewState.Snapshot.Knowledge[19].Title != "A real readable knowledge title." {
		t.Fatal("displayed titles beyond the eighth row were skipped", reads.Load())
	}
}

func TestDetailTitleBackfillsOnlyMatchingOverviewVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "Resolved title from the body.") }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	d := Document{ID: "k", Dataset: "ds", Name: "text_0123456789abcdef0123456789abcdef", ContentHash: "version-a"}
	i := Item{ID: "k", Dataset: "ds", Kind: "知识", Title: "知识", Document: &d}
	a.overviewState.Snapshot.Knowledge = []Item{i}
	s := a.state()
	a.selectItem(s, i)
	drainReading(t, updates, func() bool { return !s.Reading })
	if a.overviewState.Snapshot.Knowledge[0].Title != "Resolved title from the body." {
		t.Fatal("detail title was not published to overview")
	}
	changed := d
	changed.ContentHash = "version-b"
	stale := i
	stale.Document, stale.Body, stale.BodyLoaded = &changed, "Different content.", true
	a.publishOverviewTitle(stale)
	if a.overviewState.Snapshot.Knowledge[0].Title != "Resolved title from the body." {
		t.Fatal("different-version title overwrote overview")
	}
}

func TestOverviewLocateReplyCannotOverrideANewSelection(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
			return
		}
		if r.URL.Path == "/v1/datasets/ds/documents/new/body" {
			body := "New selected content."
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: "new", DatasetID: "ds", Body: &body})
			return
		}
		if r.URL.Path != "/v1/projects/p/knowledge" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("anchor_doc_id") != "old" {
			json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{})
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{Items: []v2KnowledgeItem{{DocID: "old", DatasetID: "ds", Title: "old"}}})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	a.access.Loaded = true
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	d := Document{ID: "old", Dataset: "ds", Name: "old"}
	a.openOverviewItem(Item{ID: "old", Dataset: "ds", Kind: "知识", Document: &d})
	s := a.state()
	<-started
	newDoc := Document{ID: "new", Dataset: "ds", Name: "new"}
	a.selectItem(s, Item{ID: "new", Dataset: "ds", Kind: "知识", Document: &newDoc})
	close(release)
	drainReading(t, updates, func() bool { return !s.Reading })
	deadline := time.NewTimer(30 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			if s.Selected == nil || s.Selected.ID != "new" || s.Selected.Body != "New selected content." || s.HomeAnchor != nil {
				t.Fatal("late locator replaced explicit new selection")
			}
			return
		}
	}
}

func TestOverviewLocateIgnoresCancelledDetailReply(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var rawCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
			return
		}
		if r.URL.Path == "/v1/datasets/ds/documents/target/body" {
			if rawCount.Add(1) == 1 {
				close(started)
				<-r.Context().Done()
				return
			}
			body := "Located content."
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: "target", DatasetID: "ds", Body: &body})
			return
		}
		if r.URL.Path != "/v1/projects/p/knowledge" {
			http.NotFound(w, r)
			return
		}
		<-release
		json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{Items: []v2KnowledgeItem{{DocID: "target", DatasetID: "ds", Title: "target"}}})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	a.page, a.kind = "知识", "知识"
	a.access.Loaded = true
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	d := Document{ID: "target", Dataset: "ds", Name: "Target"}
	i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d}
	s := a.state()
	a.selectItem(s, i)
	<-started
	a.locateOverviewItem(s, i)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cancelled detail did not settle")
	}
	oldError := s.Error
	close(release)
	if oldError != "" {
		t.Fatal("cancelled detail polluted the locator", oldError)
	}
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if s.Error != "" || s.Selected == nil || s.Selected.ID != "target" {
		t.Fatal("location failed after cancelling detail", s.Error)
	}
}

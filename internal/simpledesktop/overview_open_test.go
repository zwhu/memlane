package simpledesktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestOverviewOpenLocatesSecondPageAndScrollsToTarget(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "Target knowledge body.")
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		pages.Add(1)
		rows := []Document{}
		for n := offset; n < min(offset+31, 60); n++ {
			rows = append(rows, Document{ID: fmt.Sprint(n), Dataset: "ds", Name: fmt.Sprintf("Knowledge %d", n), Metadata: map[string]any{"node_set": []string{"session_learnings"}}})
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Name: "project", Dataset: "ds"}}}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.access.Next = time.Now().Add(time.Hour)
	d := Document{ID: "58", Dataset: "ds", Name: "Knowledge 58", Metadata: map[string]any{"node_set": []string{"session_learnings"}}}
	a.openOverviewItem(Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.Name, Document: &d})
	s := a.state()
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if s.Offset != 30 || s.Selected == nil || s.Selected.ID != "58" || s.Error != "" || !containsReadingItem(s.Items, s.Selected.key()) {
		t.Fatalf("overview did not locate the target page: offset=%d selected=%v error=%s", s.Offset, s.Selected, s.Error)
	}
	if pages.Load() < 2 {
		t.Fatal("target page was never read")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	r, ok := tt.Find("Knowledge 58")
	if !ok || r.H <= 0 || r.Y < 120 || r.Y+r.H > 800 {
		t.Fatal("target is not visible and selected", r)
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
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "New selected content.")
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		fmt.Fprint(w, `[{"id":"old","datasetId":"ds","name":"old","externalMetadata":{"node_set":["session_learnings"]}}]`)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
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
			if s.Selected == nil || s.Selected.ID != "new" || s.HomeAnchor != nil {
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
		if strings.HasSuffix(r.URL.Path, "/raw") {
			if rawCount.Add(1) == 1 {
				close(started)
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, "Located content.")
			return
		}
		<-release
		fmt.Fprint(w, `[{"id":"target","datasetId":"ds","name":"Target","externalMetadata":{"node_set":["session_learnings"]}}]`)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	a.page, a.kind = "知识", "知识"
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

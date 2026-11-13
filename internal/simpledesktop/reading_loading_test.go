package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func drainReading(t *testing.T, updates chan func(), ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			t.Fatal("reading update deadline")
		}
	}
}
func TestReadingPaneAutomaticBoundedAndContinuous(t *testing.T) {
	var rawCalls atomic.Int32
	var deny atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/raw") {
			rawCalls.Add(1)
			if deny.Load() {
				w.WriteHeader(403)
				return
			}
			fmt.Fprint(w, "# Session learning (session s)\n\nReadable knowledge. More content.")
			return
		}
		if req.URL.Query().Get("limit") != "31" {
			t.Errorf("catalog unbounded: %s", req.URL.RawQuery)
		}
		rows := []Document{}
		for n := 0; n < 31; n++ {
			d := lessonDocument()
			d.ID = fmt.Sprint(n)
			d.ContentHash = "v"
			rows = append(rows, d)
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	updates := make(chan func(), 100)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	s := a.state()
	s.ListScroll.Y = 45
	s.Technical["saved"] = true
	s.Tools["tool"] = true
	a.load(s)
	drainReading(t, updates, func() bool {
		if !s.Loaded {
			return false
		}
		for _, i := range s.Items {
			if i.ReadingState != "ready" {
				return false
			}
		}
		return true
	})
	if len(s.Items) != 30 || rawCalls.Load() != 30 || !s.More || s.ListScroll.Y != 45 || !s.Technical["saved"] || !s.Tools["tool"] {
		t.Fatalf("auto title altered continuity/count: %d calls%d", len(s.Items), rawCalls.Load())
	}
	for _, i := range s.Items {
		if i.Title != "Readable knowledge." || i.Body != "" {
			t.Fatal("title incomplete or raw copied into row")
		}
	}
	a.selectItem(s, s.Items[0])
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected == nil || !s.Selected.BodyLoaded || itemPresentation(*s.Selected).VisibleBody == s.Selected.Body {
		t.Fatal("detail/visible copy missing")
	}
	s.DetailScroll[s.Selected.key()] = &ui.ScrollState{Y: 80}
	selectedKey := s.Selected.key()
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if s.Selected == nil || s.Selected.key() != selectedKey || s.DetailScroll[selectedKey].Y != 80 {
		t.Fatal("refresh lost reader")
	}
	deny.Store(true)
	a.selectItem(s, *s.Selected)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected != nil || s.Items[0].ReadingState != "unavailable" || len(s.Items) != 30 || s.Items[1].ReadingState == "unavailable" {
		t.Fatal("denial retained body/copy or affected unrelated rows")
	}
	a.SetReadingPolicyRevision("revoked")
	if s.Selected != nil || s.Loaded {
		t.Fatal("policy revision not invalidated")
	}
}
func TestReadingLateReplyDoesNotCrossPaneOrRefillCache(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, "# Session learning (session s)\n\nOld content.")
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Items = []Item{readingTestItem("old", "v")}
	s.Items[0].Title = "知识"
	a.startTitles(s)
	<-entered
	a.epoch++
	a.cancelTitles(s)
	s.Items = []Item{readingTestItem("new", "v")}
	s.Items[0].Title = "New page"
	close(release)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no canceled completion")
	}
	for len(updates) > 0 {
		fn := <-updates
		fn()
	}
	if s.Items[0].Title != "New page" {
		t.Fatal("late result replaced page")
	}
	a.titles.mu.Lock()
	count := len(a.titles.cache)
	a.titles.mu.Unlock()
	if count != 0 {
		t.Fatal("canceled result refilled cache")
	}
}
func TestReadingVersionlessRefreshAndOversize(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, strings.Repeat("x", 256*1024+1))
	}))
	defer server.Close()
	r := newReadingTitles()
	c := Client{Config: Config{ServiceURL: server.URL}}
	i := readingTestItem("d", "")
	i.Document.Updated = ""
	if keyForReading("s", i, "1") == keyForReading("s", i, "2") {
		t.Fatal("versionless reused across catalogs")
	}
	for n := 0; n < 2; n++ {
		if _, e := r.read(context.Background(), c, i, "1", false, true); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("oversize cached")
	}
}

func TestReadingTimeoutKeepsConfirmedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { <-req.Context().Done() }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	a.titles.timeout = 5 * time.Millisecond
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	i := readingTestItem("d", "v")
	i.Body = "# Session learning (session s)\n\nConfirmed content."
	i.BodyLoaded = true
	s := a.state()
	s.Items = []Item{i}
	s.Selected = &i
	a.selectItem(s, i)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected == nil || s.Selected.Body != i.Body || !s.Selected.BodyLoaded || s.Error == "" || len(s.Items) != 1 {
		t.Fatal("timeout erased valid body or claimed success")
	}
}

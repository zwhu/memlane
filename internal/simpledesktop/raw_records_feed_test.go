package simpledesktop

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

func TestRawRecordsScrollAppendsStableSnapshotAndRefreshes(t *testing.T) {
	var added atomic.Bool
	var firstPages, cursorPages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/projects/p/raw-records":
			q := r.URL.Query()
			if q.Get("kind") != "all" || q.Get("limit") != "30" {
				t.Error("unexpected raw page request", q)
			}
			resp := v2RawRecordsResponse{DatasetID: "ds", CacheReady: true}
			addDoc := func(id, date string) {
				resp.Items = append(resp.Items, v2RawRecordItem{ItemKind: "document",
					Document: &Document{ID: id, Dataset: "ds", Name: id + ".md", Created: date}})
			}
			start, end := 0, 30
			switch q.Get("cursor") {
			case "":
				firstPages.Add(1)
				resp.NextCursor = "snapshot-30"
				if added.Load() {
					addDoc("new", "2026-11-17T00:00:00Z")
					end--
					resp.NextCursor = "updated-29"
				}
			case "snapshot-30":
				cursorPages.Add(1)
				start, end, resp.NextCursor = 30, 60, "snapshot-60"
			case "snapshot-60":
				cursorPages.Add(1)
				start, end = 60, 65
			default:
				t.Error("unexpected cursor", q)
			}
			for n := start; n < end; n++ {
				addDoc(fmt.Sprintf("raw-%02d", n), "2026-11-16T00:00:00Z")
			}
			resp.HasMore = resp.NextCursor != ""
			json.NewEncoder(w).Encode(resp)
		case "/v1/datasets/ds/documents/raw-00/body":
			body := "keep reading"
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: "raw-00", DatasetID: "ds", Body: &body})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Name: "p", Dataset: "ds"}}}, nil)
	a.access.Loaded = true
	updates := make(chan func(), 300)
	a.Update = func(fn func()) { updates <- fn }
	a.page, a.kind = "知识", "原始记录"
	a.service = "已连接"
	a.access.Next = time.Now().Add(time.Hour)
	s := a.state()
	t.Cleanup(func() {
		a.cancelTitles(s)
		if s.cancel != nil {
			s.cancel()
		}
	})
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if len(s.Items) != 30 || !s.More {
		t.Fatalf("first batch %d more=%v", len(s.Items), s.More)
	}
	selected := s.Items[0]
	selected.BodyLoaded = true
	selected.Body = "keep reading"
	s.Selected = &selected
	added.Store(true)
	tt := ui.NewTester(a.View, 1200, 820)
	for n := 0; n < 12 && len(s.Items) < 65; n++ {
		tt.Scroll(350, 600, 0, 10000)
		tt.Frame()
		if s.RawAppending {
			drainReading(t, updates, func() bool { return !s.RawAppending })
		}
	}
	if len(s.Items) != 65 || s.More {
		t.Fatalf("scroll did not append all records: %d more=%v", len(s.Items), s.More)
	}
	if tt.HasText("下一页") || tt.HasText("上一页") {
		t.Fatal("manual paging remains")
	}
	if firstPages.Load() != 1 || cursorPages.Load() != 2 {
		t.Fatal("append did not follow the two cursor pages", firstPages.Load(), cursorPages.Load())
	}
	seen := map[string]bool{}
	for _, i := range s.Items {
		if seen[i.key()] || i.ID == "new" {
			t.Fatal("snapshot drift or duplicate")
		}
		seen[i.key()] = true
	}
	if s.Selected == nil || s.Selected.Body != "keep reading" {
		t.Fatal("append lost reader")
	}
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if len(s.Items) != 30 || s.Items[0].ID != "new" || !s.More || s.RawNextCursor != "updated-29" ||
		s.Selected == nil || s.Selected.Body != "keep reading" || firstPages.Load() != 2 {
		t.Fatalf("refresh did not replace the cursor snapshot and retain the reader: %d %v", len(s.Items), s.More)
	}
}

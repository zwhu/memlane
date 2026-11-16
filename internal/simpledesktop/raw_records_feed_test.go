package simpledesktop

import (
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

func TestRawRecordsScrollAppendsStableSnapshotAndRefreshes(t *testing.T) {
	var added atomic.Bool
	var catalogs atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/datasets/") {
			catalogs.Add(1)
			rows := []Document{}
			if added.Load() {
				rows = append(rows, Document{ID: "new", Dataset: "ds", Name: "new.md", Created: "2026-11-17T00:00:00Z"})
			}
			for n := 0; n < 65; n++ {
				rows = append(rows, Document{ID: fmt.Sprintf("raw-%02d", n), Dataset: "ds", Name: fmt.Sprintf("raw-%02d.md", n), Created: "2026-11-16T00:00:00Z"})
			}
			json.NewEncoder(w).Encode(rows)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{}, "has_more": false})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Name: "p", Dataset: "ds"}}}, nil)
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
	drainReading(t, updates, func() bool { return !s.Loading })
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
	}
	if len(s.Items) != 65 || s.More {
		t.Fatalf("scroll did not append all records: %d more=%v", len(s.Items), s.More)
	}
	if tt.HasText("下一页") || tt.HasText("上一页") {
		t.Fatal("manual paging remains")
	}
	if catalogs.Load() != 1 {
		t.Fatal("append re-enumerated changing official catalog")
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
	drainReading(t, updates, func() bool { return !s.Loading })
	if len(s.Items) != 65 || s.Items[0].ID != "new" || !s.More {
		t.Fatalf("refresh lost displayed range or new item: %d %v", len(s.Items), s.More)
	}
}

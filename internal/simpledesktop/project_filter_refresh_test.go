package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawFilterReturnRefreshesCachedAllWithoutDroppingReading(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/datasets/") {
			json.NewEncoder(w).Encode([]Document{})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{
			{ID: "today", Dataset: "ds", Updated: "2026-11-15T00:51:00+08:00"},
			{ID: "yesterday", Dataset: "ds", Updated: "2026-11-14T22:11:00+08:00"},
		}, "has_more": false})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	updates := make(chan func(), 100)
	a.Update = func(fn func()) { updates <- fn }
	a.page, a.kind = "知识", "原始记录"
	a.syncReadingIdentity()
	all := a.state()
	old := Item{ID: "yesterday", Dataset: "ds", Kind: "会话", BodyLoaded: true, Body: "keep reading"}
	all.Loaded, all.Items, all.Selected = true, []Item{old}, &old
	all.ListScroll.Y = 42
	t.Cleanup(func() {
		for _, pane := range a.panes {
			a.cancelTitles(pane)
			if pane.cancel != nil {
				pane.cancel()
			}
			if pane.readCancel != nil {
				pane.readCancel()
			}
		}
	})
	a.switchProjectKind("会话")
	drainReading(t, updates, func() bool { return !a.state().Loading })
	if !containsReadingItem(a.state().Items, (Item{ID: "today", Dataset: "ds", Kind: "会话"}).key()) {
		t.Fatal("new session is absent from session filter")
	}
	a.switchProjectKind("原始记录")
	if a.state() != all || all.Selected == nil || all.Selected.Body != "keep reading" || all.ListScroll.Y != 42 {
		t.Fatal("filter return discarded cached reading or scroll")
	}
	if !all.Loading {
		t.Fatal("all filter reused stale cache after session filter saw today's session")
	}
	drainReading(t, updates, func() bool { return !all.Loading })
	if len(all.Items) != 2 || all.Items[0].ID != "today" {
		t.Fatal("today's session missing from refreshed all filter")
	}
}

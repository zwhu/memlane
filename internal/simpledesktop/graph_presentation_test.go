package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectGraphTabsStayAlignedAcrossPages(t *testing.T) {
	a := projectUIFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	knowledge, ok := tt.Find("记忆")
	if !ok {
		t.Fatal("knowledge tab absent")
	}
	clickProjectUI(t, tt, "图谱")
	for frame := 0; frame < 12; frame++ {
		tt.Frame()
		graph, ok := tt.Find("记忆")
		if !ok || graph.Y != knowledge.Y || graph.X != knowledge.X || graph.H != knowledge.H {
			t.Fatalf("tabs moved on graph frame %d: knowledge=%+v graph=%+v", frame, knowledge, graph)
		}
		if a.page != "图谱" {
			t.Fatalf("graph selection changed during repaint: %s", a.page)
		}
	}
}

func TestProjectRefreshKeepsListAndReadingGeometry(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	s.Knowledge = knowledgeFeedState{Initialized: true}
	s.Items[0].Body = "Stable reading body"
	s.Selected.Title = "Stable list title"
	s.Selected.Body = "Stable reading body"
	tt := ui.NewTester(a.View, 1200, 820)
	list, ok := tt.Find("阅读记忆：Stable reading body")
	if !ok {
		t.Fatal("list title absent")
	}
	body, ok := tt.Find("阅读记忆：Stable reading body")
	if !ok {
		t.Fatal("body absent")
	}
	s.Loading, s.Reading = true, true
	tt.Frame()
	afterList, _ := tt.Find("阅读记忆：Stable reading body")
	afterBody, _ := tt.Find("阅读记忆：Stable reading body")
	if list != afterList || body != afterBody {
		t.Fatalf("refresh moved content: list %+v -> %+v; body %+v -> %+v", list, afterList, body, afterBody)
	}
}

func TestGraphIdenticalRefreshPreservesCanvasEpoch(t *testing.T) {
	g := &GraphHost{}
	p := Project{ID: "p"}
	raw := []byte(`{"nodes":[{"id":"a","name":"A"}],"links":[],"color_maps":{}}`)
	if err := g.apply(context.Background(), p, "ds", raw); err != nil {
		t.Fatal(err)
	}
	epoch := g.epoch
	if err := g.applyCurrent(context.Background(), p, "ds", raw, &epoch); err != nil {
		t.Fatal(err)
	}
	if g.epoch != epoch {
		t.Fatal("identical refresh replaces the official canvas frame")
	}
	stale := epoch - 1
	if err := g.applyCurrent(context.Background(), p, "ds", raw, &stale); err == nil {
		t.Fatal("identical payload bypassed stale-result rejection")
	}
	changed := []byte(`{"nodes":[{"id":"a","name":"Updated"}],"links":[],"color_maps":{}}`)
	if err := g.applyCurrent(context.Background(), p, "ds", changed, &epoch); err != nil {
		t.Fatal(err)
	}
	if g.epoch != epoch+1 {
		t.Fatal("changed graph was not published")
	}
}

func TestProjectRefreshKeepsResolvedTitleUntilHydration(t *testing.T) {
	d := lessonDocument()
	d.ContentHash = "stable"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "# Session learning (session s)\n\nResolved knowledge title.")
			return
		}
		json.NewEncoder(w).Encode([]Document{d})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: d.Dataset}}}, nil)
	updates := make(chan func(), 100)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	a.syncReadingIdentity()
	s := a.state()
	s.Loaded = true
	s.Items = []Item{{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d, Title: "Resolved knowledge title.", ReadingState: "ready"}}
	defer func() {
		a.cancelTitles(s)
		if s.cancel != nil {
			s.cancel()
		}
	}()
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading })
	if s.Items[0].Title != "Resolved knowledge title." {
		t.Fatalf("catalog refresh replaced readable title before hydration: %q", s.Items[0].Title)
	}
}

func TestProjectHeaderDividerAndFilterInsets(t *testing.T) {
	a := projectUIFixture()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "原始记录")
	divider, ok := tt.Find("Divider")
	if !ok {
		t.Fatal("raw-record split divider absent")
	}
	img := tt.Image()
	x := int(divider.X + divider.W/2)
	top, body := img.At(x, int(homeHeaderHeight+6)), img.At(x, 300)
	if top != body {
		t.Fatalf("divider stops below project tabs: x=%d header=%v body=%v", x, top, body)
	}
	clickProjectUI(t, tt, "原始记录")
	filter, ok := tt.Find("全部")
	if !ok {
		t.Fatal("record filter absent")
	}
	clickProjectUI(t, tt, "图谱")
	graph, ok := tt.Find("实体关系")
	if !ok {
		t.Fatal("graph filter absent")
	}
	if graph.Y != filter.Y || graph.X != filter.X {
		t.Fatalf("secondary filter insets differ: records=%+v graph=%+v", filter, graph)
	}
}

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestKnowledgeFeedFiltersBeforeBatchAndKeepsScroll(t *testing.T) {
	const preview = "真实知识全文。第二句必须保留。"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/projects/p/knowledge":
			q := r.URL.Query()
			if q.Get("limit") != "30" {
				t.Error("knowledge pages must request a bounded result", q)
			}
			start, err := strconv.Atoi(q.Get("cursor"))
			if q.Has("cursor") && err != nil {
				t.Error("invalid cursor", q)
			}
			end := min(start+30, 85)
			resp := v2ProjectKnowledgeResponse{DatasetID: "dataset", TotalFiltered: 85, HasMore: end < 85}
			if resp.HasMore {
				resp.NextCursor = strconv.Itoa(end)
			}
			for n := start; n < end; n++ {
				resp.Items = append(resp.Items, v2KnowledgeItem{
					DocID: fmt.Sprint(2*n + 1), DatasetID: "dataset", UpdatedAt: "v1", PreviewText: preview,
				})
			}
			json.NewEncoder(w).Encode(resp)
		default:
			if r.URL.Path == "/v1/datasets/dataset/documents/1/body" {
				body := preview
				json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: "1", DatasetID: "dataset", Body: &body})
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	updates := make(chan func(), 200)
	a.Update = func(fn func()) { updates <- fn }
	a.page, a.kind = "知识", "知识"
	s := a.state()
	s.ListScroll.Y = 80
	a.loadKnowledgeFeed(s)
	ready := func() bool {
		if !s.Loaded || s.Loading || s.Knowledge.Appending {
			return false
		}
		for _, i := range s.Items {
			if i.BodyLoaded || i.Preview != preview {
				return false
			}
		}
		return true
	}
	drainReading(t, updates, ready)
	if len(s.Items) != 30 || s.Knowledge.TotalFiltered != 85 || len(s.Knowledge.Catalog) != 0 || !s.More {
		t.Fatalf("first batch=%d total=%d more=%v", len(s.Items), s.Knowledge.TotalFiltered, s.More)
	}
	first := s.Items[0].Key()
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, ready)
	if len(s.Items) != 60 || s.ListScroll.Y != 80 || s.Items[0].Key() != first {
		t.Fatal("append replaced batch or lost scroll")
	}
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, ready)
	seen := map[string]bool{}
	for _, i := range s.Items {
		if seen[i.Key()] || !documentKnowledge(*i.Document) {
			t.Fatal("duplicate or raw material in feed")
		}
		seen[i.Key()] = true
	}
	if len(seen) != 85 || s.More {
		t.Fatal("final batch incomplete")
	}
	a.loadKnowledgeFeed(s)
	drainReading(t, updates, ready)
	if len(s.Items) != 30 || s.Knowledge.TotalFiltered != 85 || !s.More || s.Items[0].Key() != first || s.ListScroll.Y != 80 || s.Items[0].Preview != preview {
		t.Fatal("refresh lost content, range or position")
	}
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected == nil || knowledgeCardBody(*s.Selected) != preview {
		t.Fatalf("selected detail did not retain its official body: selected=%+v error=%q", s.Selected, s.Error)
	}
	// Official denial must remove full text and copy eligibility.
	a.InvalidateReading("dataset")
	for _, i := range s.Items {
		if i.BodyLoaded || i.Preview != "" || knowledgeCardBody(i) != "" {
			t.Fatal("revoked content retained")
		}
	}
}

func TestKnowledgeThreePaneSelectionCopyAndScroll(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1536, 1024}} {
		a := projectUIFixture()
		s := a.state()
		s.Knowledge = knowledgeFeedState{Initialized: true}
		s.Items = nil
		for n := 0; n < 9; n++ {
			d := lessonDocument()
			d.ID = fmt.Sprint(n)
			d.Dataset = "ds"
			body := fmt.Sprintf("第 %d 条记忆：%s", n, strings.Repeat("保留真实知识内容，不能凭空生成标题。", n%3+1))
			s.Items = append(s.Items, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d, Body: "# Session learning (session s)\n\n" + body, BodyLoaded: true, ReadingState: "ready", Date: "2026-11-16", Source: "s"})
		}
		s.Knowledge.Catalog = append([]Item(nil), s.Items...)
		s.Selected = &s.Items[0]
		tt := ui.NewTester(a.View, wh[0], wh[1])
		fixtureImage(t, tt, fmt.Sprintf("knowledge-three-pane-%d", wh[0]))
		for _, obsolete := range []string{"最近知识", "最近更新", "上一页", "下一页", "展开完整标题", "搜索当前内容"} {
			if tt.HasText(obsolete) {
				t.Fatalf("obsolete control %s", obsolete)
			}
		}
		clickProjectUI(t, tt, "复制正文")
		if tt.Clipboard() != knowledgeCardBody(s.Items[0]) {
			t.Fatal("copy did not use complete visible statement")
		}
		if _, ok := tt.Find("记忆列表"); !ok {
			t.Fatal("three-pane list missing")
		}
		if a.page != "知识" || a.kind != "知识" {
			t.Fatal("copy navigated away")
		}
		s.ListScroll.Y = min(float32(100), s.ListScroll.MaxY)
		tt.Frame()
		beforeScroll := s.ListScroll.Y
		clickProjectUI(t, tt, knowledgeCardBody(s.Items[4]))
		if s.Selected == nil || s.Selected.ID != "4" || s.ListScroll.Y != beforeScroll {
			t.Fatalf("selecting visible item changed feed scroll: sel=%v scroll=%v before=%v", s.Selected, s.ListScroll.Y, beforeScroll)
		}
	}
}

func TestKnowledgeFeedRejectsLateProjectReply(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/p/knowledge" {
			http.NotFound(w, r)
			return
		}
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{Items: []v2KnowledgeItem{{DocID: "old", DatasetID: "dataset"}}})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	a.page, a.kind = "知识", "知识"
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	a.loadKnowledgeFeed(s)
	<-started
	a.epoch++
	close(release)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(time.Second):
		t.Fatal("missing callback")
	}
	if s.Loaded || len(s.Items) != 0 {
		t.Fatal("late old-project catalog applied")
	}
}

func TestKnowledgeThreePaneInlineDetailAndDarkAppearance(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	s.Items[0].Document.Name = ""
	s.Items[0].Body = "第一句正文。第二句正文，不能当成另一个标题。"
	s.Selected = &s.Items[0]
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(a.View, 1200, 820)
		tt.SetDark(dark)
		tt.Frame()
		if !tt.HasText("第一句正文。第二句正文，不能当成另一个标题。") || tt.HasText("展开完整标题") || tt.HasText("记忆详情弹框") {
			t.Fatalf("three-pane detail wrong (dark=%v)", dark)
		}
		if tok.Dark != dark {
			t.Fatalf("V4 tokens did not follow dark=%v", dark)
		}
	}
}

func TestKnowledgeThreePaneAsyncHydrationUpdatesListAndReader(t *testing.T) {
	a := projectUIFixture()
	a.Update = nil
	s := a.state()
	s.Knowledge = knowledgeFeedState{Initialized: true}
	s.Items = nil
	for n := 0; n < 5; n++ {
		d := lessonDocument()
		d.ID = fmt.Sprint(n)
		d.Dataset = "ds"
		s.Items = append(s.Items, Item{ID: d.ID, Dataset: "ds", Kind: "知识", Document: &d, ReadingState: "pending"})
	}
	s.Selected = &s.Items[0]
	tt := ui.NewTester(a.View, 1200, 820)
	for n := range s.Items {
		s.Items[n].Body = fmt.Sprintf("水合完成条目 %d 正文", n)
		s.Items[n].BodyLoaded = true
		s.Items[n].ReadingState = "ready"
	}
	s.Selected = &s.Items[0]
	tt.Frame()
	if !tt.HasText("水合完成条目 0 正文") || !tt.HasText("水合完成条目 4 正文") {
		t.Fatal("hydrated bodies missing in three-pane list")
	}
	fixtureImage(t, tt, "three-pane-hydrated")
}

func TestKnowledgeThreePanePendingAppendKeepsExistingRows(t *testing.T) {
	a := projectUIFixture()
	a.Update = nil
	s := a.state()
	s.Knowledge = knowledgeFeedState{Initialized: true}
	s.Items = nil
	for n := 0; n < 5; n++ {
		d := lessonDocument()
		d.ID = fmt.Sprint(n)
		body := fmt.Sprintf("条目%d %s", n, strings.Repeat("正文内容长短不同，按完整文本排列。", n%2+1))
		s.Items = append(s.Items, Item{ID: d.ID, Dataset: "ds", Kind: "知识", Document: &d, Body: body, BodyLoaded: true, ReadingState: "ready"})
	}
	s.Selected = &s.Items[0]
	tt := ui.NewTester(a.View, 1200, 820)
	before, ok := tt.Find(knowledgeCardBody(s.Items[0]))
	if !ok {
		t.Fatal("existing row missing")
	}
	for n := 5; n < 10; n++ {
		s.Items = append(s.Items, Item{ID: fmt.Sprint(n), Dataset: "ds", Kind: "知识", ReadingState: "pending"})
	}
	tt.Frame()
	after, _ := tt.Find(knowledgeCardBody(s.Items[0]))
	if after != before {
		t.Fatalf("pending append moved an existing row: %+v -> %+v", before, after)
	}
	for n := 5; n < 10; n++ {
		s.Items[n].Body = fmt.Sprintf("条目%d 新批次完成。", n)
		s.Items[n].BodyLoaded = true
		s.Items[n].ReadingState = "ready"
	}
	tt.Frame()
	final, _ := tt.Find(knowledgeCardBody(s.Items[0]))
	if final != before {
		t.Fatalf("completed append moved an existing row: %+v -> %+v", before, final)
	}
}

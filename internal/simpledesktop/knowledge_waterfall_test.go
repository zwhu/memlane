package simpledesktop

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
	docs := []Document{}
	for n := 0; n < 170; n++ {
		d := lessonDocument()
		d.ID = fmt.Sprint(n)
		d.Updated = "v1"
		if n%2 == 0 {
			d.Metadata = map[string]any{}
		}
		docs = append(docs, d)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "# Session learning (session s)\n\n真实知识全文。第二句必须保留。")
			return
		}
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		end := min(off+100, len(docs))
		json.NewEncoder(w).Encode(docs[min(off, len(docs)):end])
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	updates := make(chan func(), 200)
	a.Update = func(fn func()) { updates <- fn }
	a.page, a.kind = "知识", "知识"
	s := a.state()
	s.ListScroll.Y = 80
	a.loadKnowledgeFeed(s)
	ready := func() bool {
		if !s.Loaded || s.Loading {
			return false
		}
		for _, i := range s.Items {
			if !i.BodyLoaded {
				return false
			}
		}
		return true
	}
	drainReading(t, updates, ready)
	if len(s.Items) != 30 || len(s.Knowledge.Catalog) != 85 || !s.More {
		t.Fatalf("first batch=%d catalog=%d more=%v", len(s.Items), len(s.Knowledge.Catalog), s.More)
	}
	first := s.Items[0].key()
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, ready)
	if len(s.Items) != 60 || s.ListScroll.Y != 80 || s.Items[0].key() != first {
		t.Fatal("append replaced batch or lost scroll")
	}
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, ready)
	seen := map[string]bool{}
	for _, i := range s.Items {
		if seen[i.key()] || !i.Document.knowledge() {
			t.Fatal("duplicate or raw material in feed")
		}
		seen[i.key()] = true
	}
	if len(seen) != 85 || s.More {
		t.Fatal("final batch incomplete")
	}
	a.loadKnowledgeFeed(s)
	drainReading(t, updates, ready)
	if len(s.Items) != 85 || s.ListScroll.Y != 80 || knowledgeCardBody(s.Items[0]) != "真实知识全文。第二句必须保留。" {
		t.Fatal("refresh lost content, range or position")
	}
	// Official denial must remove full text and copy eligibility.
	a.InvalidateReading("dataset")
	for _, i := range s.Items {
		if i.BodyLoaded || knowledgeCardBody(i) != "" {
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
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "body")
			return
		}
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		d := lessonDocument()
		json.NewEncoder(w).Encode([]Document{d})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
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

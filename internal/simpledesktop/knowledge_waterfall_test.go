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

func TestKnowledgeWaterfallCardActionsAndMasonry(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1536, 1024}} {
		a := projectUIFixture()
		s := a.state()
		s.Knowledge = knowledgeFeedState{Initialized: true}
		s.Items = nil
		for n := 0; n < 9; n++ {
			d := lessonDocument()
			d.ID = fmt.Sprint(n)
			d.Dataset = "ds"
			body := strings.Repeat("保留真实知识内容，不能凭空生成标题。", n%3+1)
			s.Items = append(s.Items, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d, Body: "# Session learning (session s)\n\n" + body, BodyLoaded: true, ReadingState: "ready", Date: "2026-11-16", Source: "s"})
		}
		tt := ui.NewTester(a.View, wh[0], wh[1])
		fixtureImage(t, tt, fmt.Sprintf("knowledge-waterfall-%d", wh[0]))
		for _, obsolete := range []string{"最近知识", "最近更新", "上一页", "下一页", "展开完整标题"} {
			if tt.HasText(obsolete) {
				t.Fatalf("obsolete control %s", obsolete)
			}
		}
		clickProjectUI(t, tt, "复制记忆")
		if tt.Clipboard() != knowledgeCardBody(s.Items[0]) {
			t.Fatal("copy did not use complete visible statement")
		}
		card, ok := tt.Find("记忆卡片")
		if !ok || card.W <= 0 {
			t.Fatal("card missing")
		}
		if a.page != "知识" || a.kind != "知识" {
			t.Fatal("copy navigated away")
		}
		// A selected source/detail does not remove the feed's independent scroll.
		s.ListScroll.Y = min(float32(100), s.ListScroll.MaxY)
		tt.Frame()
		beforeScroll := s.ListScroll.Y
		s.Knowledge.Detail = true
		tt.Frame()
		clickProjectUI(t, tt, "关闭记忆详情")
		if s.Knowledge.Detail || s.ListScroll.Y != beforeScroll {
			t.Fatalf("detail return reset feed state: size=%v detail=%v scroll=%v before=%v max=%v", wh, s.Knowledge.Detail, s.ListScroll.Y, beforeScroll, s.ListScroll.MaxY)
		}
	}
}

func TestKnowledgeFeedRejectsLateProjectReply(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
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

func TestKnowledgeModalEscapePreservesFeed(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	s.Items[0].Document.Name = ""
	s.Items[0].Body = "第一句正文。第二句正文，不能当成另一个标题。"
	s.Selected = &s.Items[0]
	tt := ui.NewTester(a.View, 1200, 820)
	before, _ := tt.Find("记忆卡片")
	clickProjectUI(t, tt, "阅读记忆："+knowledgeCardBody(s.Items[0]))
	if !s.Knowledge.Detail || !tt.HasText("记忆详情弹框") || !tt.HasText("关闭记忆详情") || tt.HasText("展开完整标题") {
		t.Fatal("knowledge did not open as an untitled modal")
	}
	tt.Key(0, ui.KeyEscape)
	if s.Knowledge.Detail || tt.HasText("记忆详情弹框") {
		t.Fatal("Escape did not dismiss modal")
	}
	after, _ := tt.Find("记忆卡片")
	if before != after {
		t.Fatal("closing modal moved underlying feed", before, after)
	}
	clickProjectUI(t, tt, "阅读记忆："+knowledgeCardBody(s.Items[0]))
	tt.ClickAt(10, 80)
	if s.Knowledge.Detail {
		t.Fatal("outside click did not dismiss modal")
	}
}

// A batch arrives as metadata, then its very different body heights resolve.
// Equal pending placeholders must not permanently choose unequal final columns.
func TestKnowledgeWaterfallAsyncBodiesDoNotLeaveAnEmptyColumn(t *testing.T) {
	a := projectUIFixture()
	a.Update = nil
	s := a.state()
	s.Knowledge = knowledgeFeedState{Initialized: true}
	s.Items = nil
	for n := 0; n < 9; n++ {
		d := lessonDocument()
		d.ID = fmt.Sprint(n)
		d.Dataset = "ds"
		s.Items = append(s.Items, Item{ID: d.ID, Dataset: "ds", Kind: "知识", Document: &d, ReadingState: "pending"})
	}
	tt := ui.NewTester(a.View, 1200, 820)
	for n := range s.Items {
		body := strings.Repeat("这段真实正文比较长，需要按实际高度排布，避免中间一列出现大片空白。", 20)
		if n%3 == 1 {
			body = "短记忆。"
		}
		s.Items[n].Body = fmt.Sprintf("%d %s", n, body)
		s.Items[n].BodyLoaded = true
		s.Items[n].ReadingState = "ready"
	}
	tt.Frame()
	bottoms := [3]float32{}
	maxBody := float32(0)
	for _, i := range s.Items {
		r, ok := tt.Find("阅读记忆：" + knowledgeCardBody(i))
		if !ok {
			t.Fatal("resolved body missing")
		}
		col := s.Knowledge.Columns[i.key()]
		bottoms[col] = max(bottoms[col], r.Y+r.H)
		maxBody = max(maxBody, r.H)
	}
	gap := max(bottoms[0], bottoms[1], bottoms[2]) - min(bottoms[0], bottoms[1], bottoms[2])
	if gap > maxBody+192 {
		t.Fatalf("resolved masonry has an empty column: bottoms=%v gap=%.0f maxBody=%.0f", bottoms, gap, maxBody)
	}
	fixtureImage(t, tt, "waterfall-asynchronous-heights")
}

func TestKnowledgeWaterfallPendingAppendKeepsExistingCards(t *testing.T) {
	a := projectUIFixture()
	a.Update = nil
	s := a.state()
	s.Knowledge = knowledgeFeedState{Initialized: true}
	s.Items = nil
	for n := 0; n < 9; n++ {
		d := lessonDocument()
		d.ID = fmt.Sprint(n)
		body := fmt.Sprintf("%d %s", n, strings.Repeat("正文内容长短不同，按完整文本排列。", n%3+1))
		s.Items = append(s.Items, Item{ID: d.ID, Dataset: "ds", Kind: "知识", Document: &d, Body: body, BodyLoaded: true, ReadingState: "ready"})
	}
	tt := ui.NewTester(a.View, 1200, 820)
	before := map[string]ui.Rect{}
	for _, i := range s.Items {
		before[i.key()], _ = tt.Find("阅读记忆：" + knowledgeCardBody(i))
	}
	for n := 9; n < 18; n++ {
		s.Items = append(s.Items, Item{ID: fmt.Sprint(n), Dataset: "ds", Kind: "知识", ReadingState: "pending"})
	}
	tt.Frame()
	for _, i := range s.Items[:9] {
		r, _ := tt.Find("阅读记忆：" + knowledgeCardBody(i))
		if r != before[i.key()] {
			t.Fatal("pending append moved an existing card")
		}
	}
	if len(s.Knowledge.Columns) != 9 {
		t.Fatal("pending placeholders permanently assigned a column")
	}
	for n := 9; n < 18; n++ {
		s.Items[n].Body = fmt.Sprintf("%d 新批次完成后才参与排布。", n)
		s.Items[n].BodyLoaded = true
		s.Items[n].ReadingState = "ready"
	}
	tt.Frame()
	for _, i := range s.Items[:9] {
		r, _ := tt.Find("阅读记忆：" + knowledgeCardBody(i))
		if r != before[i.key()] {
			t.Fatal("completed append moved an existing card")
		}
	}
	if len(s.Knowledge.Columns) != 18 {
		t.Fatal("completed batch not placed")
	}
}

package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSourceHistoryStartsAtFirstPageAndReturnsToKnowledge(t *testing.T) {
	// A retained scroll offset requires the same, genuinely scrollable source body.
	originalBody := strings.Repeat("Original knowledge body with enough saved content to scroll.\n\n", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/datasets/ds/data":
			fmt.Fprint(w, `[]`)
		case "/api/v1/datasets/ds/data/k/raw":
			fmt.Fprint(w, originalBody)
		case "/api/v1/sessions":
			fmt.Fprint(w, `{"sessions":[{"session_id":"session","dataset_id":"ds","user_id":"owner"}]}`)
		case "/api/v1/sessions/session":
			fmt.Fprint(w, `{"session_id":"session","dataset_id":"ds","user_id":"owner","msg_count":43,"tool_calls":997,"qas":[{"qa_id":"tail","question":"recent unrelated question"}]}`)
		case "/api/v1/sessions/session/history":
			offset := 0
			fmt.Sscan(r.URL.Query().Get("qa_offset"), &offset)
			traceOffset := 0
			fmt.Sscan(r.URL.Query().Get("trace_offset"), &traceOffset)
			qas, traces := []map[string]any{}, []map[string]any{}
			for n := offset; n < min(offset+20, 43); n++ {
				qas = append(qas, map[string]any{"qa_id": fmt.Sprint(n), "question": fmt.Sprintf("source question %d", n), "answer": "saved answer"})
			}
			for n := traceOffset; n < min(traceOffset+20, 997); n++ {
				traces = append(traces, map[string]any{"trace_id": fmt.Sprint(n), "origin_function": "read"})
			}
			json.NewEncoder(w).Encode(map[string]any{"session_id": "session", "dataset_id": "ds", "user_id": "owner", "history_paging": true,
				"msg_count": 43, "tool_calls": 997, "qa_offset": offset, "trace_offset": traceOffset, "page_limit": 20, "qa_more": offset < 40, "trace_more": true,
				"qas": qas, "traces": traces})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Client = Client{Config: Config{ServiceURL: server.URL}}
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	knowledgePane := s
	s.Selected.Body = originalBody
	s.Selected.Source = "session"
	key := s.Selected.key()
	s.DetailScroll[key] = &ui.ScrollState{Y: 90}
	a.source(s)
	drainReading(t, updates, func() bool { return !s.Reading })
	s = a.state()
	if a.kind != "原始记录" || s == knowledgePane || !containsReadingItem(s.Items, s.Selected.key()) {
		t.Fatal("source stayed under the knowledge tab or lacks selected raw record")
	}
	if s.Selected.Detail.QAs[0].Question != "source question 0" {
		t.Fatal("source still opens an arbitrary recent tail")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("来源记忆") || !tt.HasText("原始记录筛选") || !tt.HasText("项目视图") {
		t.Fatal("opening source hides the knowledge list or project tabs")
	}
	if s.ListSelection != 0 || !tt.HasText("返回来源记忆") {
		t.Fatal("source lost the originating knowledge highlight or return action")
	}
	fixtureImage(t, tt, "source-history-first-page")
	scroll := s.DetailScroll[s.Selected.key()]
	scroll.Y = scroll.MaxY
	tt.Frame()
	drainReading(t, updates, func() bool { return !s.Reading })
	if len(s.Selected.Detail.QAs) != 40 || s.Selected.Detail.QAs[20].Question != "source question 20" {
		t.Fatal("next source page did not load")
	}
	scroll.Y = 0
	tt.Frame()
	clickProjectUI(t, tt, "工具记录")
	if !s.SessionTools || !tt.HasText("继续加载工具记录") {
		t.Fatal("source tool pages are not reachable through the UI")
	}
	fixtureImage(t, tt, "source-history-tools")
	toolScroll := s.DetailScroll[s.Selected.key()+"/tools"]
	toolScroll.Y = toolScroll.MaxY
	tt.Frame()
	drainReading(t, updates, func() bool { return !s.Reading })
	if len(s.Selected.Detail.Traces) != 40 || len(s.Selected.Detail.QAs) != 40 || s.Selected.Detail.Traces[20].ID != "20" {
		t.Fatal("tool append replaced previous records or QA reading")
	}
	toolScroll.Y = 0
	tt.Frame()
	clickProjectUI(t, tt, "返回来源记忆")
	drainReading(t, updates, func() bool { return !s.Reading })
	s = a.state()
	if a.kind != "知识" || s != knowledgePane || s.Selected == nil || s.Selected.key() != key || s.DetailScroll[key].Y != 90 || len(s.History) != 0 {
		t.Fatal("source navigation lost original knowledge state")
	}
}

func TestHistoryPageRejectsDifferentIdentity(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"session_id":"session","dataset_id":"another","user_id":"owner","history_paging":true}`)
	}))
	defer s.Close()
	c := Client{Config: Config{ServiceURL: s.URL}}
	_, err := c.sessionHistory(context.Background(), Item{ID: "session", Dataset: "ds", Session: &Session{User: "owner"}}, 0, 0)
	if err == nil {
		t.Fatal("history accepted another dataset")
	}
}

func TestHistoryRevocationClearsPagedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	item := Item{ID: "session", Dataset: "ds", Session: &Session{User: "owner"}}
	s := &pane{Selected: &item, SourcePage: &SessionHistoryPage{SessionDetail: SessionDetail{Session: Session{ID: "session", Dataset: "ds"}}}}
	a.panes["source"] = s
	a.loadSessionPage(s, 20, 0)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected != nil || s.SourcePage != nil {
		t.Fatal("revoked session retained its page or copyable content")
	}
}

func TestSessionCopyMatchesSelectedRecordType(t *testing.T) {
	item := Item{ID: "session", Dataset: "ds", Body: "loaded question and answer", Detail: &SessionDetail{Traces: []Trace{{ID: "trace", Name: "read"}}}}
	s := &pane{SessionTools: true, SourcePage: &SessionHistoryPage{SessionDetail: SessionDetail{Session: Session{ID: "session", Dataset: "ds"}}}}
	if got := sessionCopyBody(s, item); got == item.Body || !strings.Contains(got, "trace") {
		t.Fatal("tool tab copied unrelated QAs", got)
	}
	s.SessionTools = false
	if sessionCopyBody(s, item) != item.Body {
		t.Fatal("QA tab lost its visible page")
	}
}

func TestHistoryReadIgnoredAfterReturningToKnowledge(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"session_id":"session","dataset_id":"ds","user_id":"owner","history_paging":true}`)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	s := &pane{Selected: &Item{ID: "session", Dataset: "ds", Session: &Session{User: "owner"}}}
	a.loadSessionPage(s, 20, 0)
	<-started
	s.Selected = &Item{ID: "knowledge", Dataset: "ds"}
	close(release)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(time.Second):
		t.Fatal("history completion missing")
	}
	if s.Selected.ID != "knowledge" {
		t.Fatal("late history replaced the current knowledge")
	}
}

func TestSourceReturnsToKnowledgeFeedForAnotherSelection(t *testing.T) {
	a := projectUIFixture()
	s := a.state()
	s.Knowledge.Initialized = true
	origin := *s.Selected
	other := origin
	other.ID, other.Body = "other", "Another knowledge entry"
	s.Items = append(s.Items, other)
	s.History = []Item{origin}
	s.Knowledge.Detail = true
	s.Selected = &Item{ID: "session", Dataset: "ds", Session: &Session{ID: "session", Dataset: "ds"}, Detail: &SessionDetail{Session: Session{ID: "session", Dataset: "ds"}}}
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "关闭记忆详情")
	clickProjectUI(t, tt, "阅读记忆：Another knowledge entry")
	if s.Selected == nil || s.Selected.key() != other.key() || len(s.History) != 0 || !s.Knowledge.Detail {
		t.Fatal("cannot read another knowledge after source return")
	}
}

func TestSessionListAndSourceUseSameHistoryReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			fmt.Fprint(w, `{"sessions":[{"session_id":"session","dataset_id":"ds","user_id":"owner"}]}`)
		case "/api/v1/sessions/session/history":
			fmt.Fprint(w, `{"session_id":"session","dataset_id":"ds","user_id":"owner","msg_count":1,"tool_calls":0,"history_paging":true,"page_limit":20,"qas":[{"qa_id":"q","question":"same first question"}]}`)
		default:
			t.Errorf("legacy detail endpoint still used: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Client = Client{Config: Config{ServiceURL: server.URL}}
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	item := Item{ID: "session", Dataset: "ds", Kind: "会话", Session: &Session{ID: "session", Dataset: "ds", User: "owner"}}
	a.selectItem(s, item)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.SourcePage == nil || s.Selected.Detail.QAs[0].Question != "same first question" {
		t.Fatal("list did not use shared history reader")
	}
	fromSource, page, err := a.Client.sourceWithHistory(context.Background(), Item{Dataset: "ds", Source: "session"})
	if err != nil || page == nil || fromSource.Detail.QAs[0].Question != s.Selected.Detail.QAs[0].Question {
		t.Fatal("source and direct session differ", err)
	}
}

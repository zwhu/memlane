package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
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
	if a.kind != "知识" || s != knowledgePane || !sourceSession(s) {
		t.Fatal("source did not stay in place under the knowledge tab")
	}
	if s.Selected.Detail.QAs[0].Question != "source question 0" {
		t.Fatal("source still opens an arbitrary recent tail")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("来源记忆") || !tt.HasText("在原始记录中打开") || !tt.HasText("项目视图") {
		t.Fatal("opening source in place missing context bar or project tabs")
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
	clickProjectUI(t, tt, "Another knowledge entry")
	if s.Selected == nil || s.Selected.key() != other.key() || len(s.History) != 0 {
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

func TestSessionDetailStationaryPanelTabSwitchAndVirtualization(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a := projectUIFixture()
			setTestAppearance(t, a, appearance)
			a.kind = "原始记录"
			s := a.state()

			now := time.Now().UTC().Format(time.RFC3339)
			sess1 := Session{ID: "codex_large-session", Dataset: "ds", User: "owner", Updated: now}
			page1 := SessionHistoryPage{
				Paging:      true,
				Limit:       20,
				SessionDetail: SessionDetail{Session: sess1},
			}
			var longAnswer strings.Builder
			for n := 0; n < 60; n++ {
				fmt.Fprintf(&longAnswer, "Detailed session answer paragraph %03d explaining layout and virtualization.\n\n", n)
			}
			longAnswer.WriteString("END_OF_LONG_ANSWER_000")
			for n := 0; n < 160; n++ {
				ans := "Saved concise answer."
				if n == 0 {
					ans = longAnswer.String()
				}
				page1.QAs = append(page1.QAs, QA{
					ID:       fmt.Sprintf("qa-%03d", n),
					Question: fmt.Sprintf("history question %03d", n),
					Answer:   ans,
				})
			}
			var longJSON strings.Builder
			longJSON.WriteString("{\n")
			for n := 0; n < 150; n++ {
				comma := ","
				if n == 149 {
					comma = ""
				}
				fmt.Fprintf(&longJSON, "  \"k%03d\": \"step %03d\"%s\n", n, n, comma)
			}
			longJSON.WriteString("}")
			for n := 0; n < 200; n++ {
				params := json.RawMessage(`{"path":"a.go"}`)
				result := json.RawMessage(`{"ok":true}`)
				if n == 0 {
					params = json.RawMessage(longJSON.String())
					result = json.RawMessage(longJSON.String())
				}
				page1.Traces = append(page1.Traces, Trace{
					ID:     fmt.Sprintf("tr-%03d", n),
					Name:   fmt.Sprintf("history tool %03d", n),
					Status: "success",
					Params: params,
					Result: result,
				})
			}
			page1.Count, page1.ToolCount = len(page1.QAs), len(page1.Traces)

			item1, history1 := sessionItemFromHistoryPage(Item{Title: "Large history session", Kind: "会话"}, page1)
			for n := 0; n < 3; n++ {
				subSess := Session{
					ID:              fmt.Sprintf("codex_sub-%d", n),
					Dataset:         "ds",
					User:            "owner",
					Updated:         now,
					IsSubagent:      true,
					ParentSessionID: sess1.ID,
					SubagentRole:    "research",
				}
				item1.Subagents = append(item1.Subagents, Item{
					ID:             subSess.ID,
					Dataset:        "ds",
					Kind:           "会话",
					Title:          fmt.Sprintf("subagent topic %d", n),
					SessionSummary: "已保存 2 轮问答 · 5 条工具记录",
					Date:           now,
					Session:        &subSess,
				})
			}

			sess2 := Session{ID: "codex_second-session", Dataset: "ds", User: "owner", Updated: now}
			page2 := SessionHistoryPage{
				Paging: true,
				Limit:  20,
				SessionDetail: SessionDetail{
					Session:   sess2,
					Label:     "Second session title",
					Count:     1,
					ToolCount: 0,
					QAs:       []QA{{ID: "s2-q0", Question: "second session question", Answer: "second session answer"}},
				},
			}
			item2, history2 := sessionItemFromHistoryPage(Item{Title: "Second session title", Kind: "会话"}, page2)

			s.Items = []Item{item1, item2}
			s.RawCatalog = s.Items
			s.Loaded = true
			s.Selected = &item1
			s.SourcePage = history1

			tt := ui.NewTester(a.View, 1200, 820)
			tt.Advance(350 * time.Millisecond)
			tt.Frame()
			fixtureImage(t, tt, "raw-session-qa-"+appearance)

			toolbarBefore, okBar := tt.Find("项目内容工具栏")
			tabsBefore, okTabs := tt.Find("会话内容类型")
			if !okBar || !okTabs || !tt.HasText("history question 000") {
				t.Fatalf("[%s] missing stationary header or initial QA row: bar=%v tabs=%v", appearance, okBar, okTabs)
			}
			if tt.HasText("history question 159") {
				t.Fatalf("[%s] QA history should be virtualized without building row 159", appearance)
			}
			assertHeaderStationary := func(stage string) {
				t.Helper()
				toolbarNow, ok1 := tt.Find("项目内容工具栏")
				tabsNow, ok2 := tt.Find("会话内容类型")
				if !ok1 || !ok2 || toolbarNow != toolbarBefore ||
					tabsNow.X != tabsBefore.X || tabsNow.Y != tabsBefore.Y || tabsNow.H != tabsBefore.H ||
					math.Abs(float64(tabsNow.W-tabsBefore.W)) > 2 {
					t.Fatalf("[%s] %s moved header or segmented bar: bar %+v->%+v tabs %+v->%+v",
						appearance, stage, toolbarBefore, toolbarNow, tabsBefore, tabsNow)
				}
			}
			docEnter0 := a.motionRuns.docEnter
			fade0 := a.motionRuns.docBodyFade

			// 1. Switch 问答 -> 工具记录: header/tabs stationary, no docEnter replay, body fades cleanly, virtualized.
			clickProjectUI(t, tt, "工具记录")
			tt.Advance(350 * time.Millisecond)
			tt.Frame()
			assertHeaderStationary("switching to tools")
			if a.motionRuns.docEnter != docEnter0 || a.motionRuns.docBodyFade <= fade0 {
				t.Fatalf("[%s] expected no docEnter replay (%d->%d) and incremented docBodyFade (%d->%d)",
					appearance, docEnter0, a.motionRuns.docEnter, fade0, a.motionRuns.docBodyFade)
			}
			if !tt.HasText("history tool 000") || tt.HasText("history tool 199") {
				t.Fatalf("[%s] tool history should virtualize rows", appearance)
			}
			toolFrames := make([]time.Duration, 0, 15)
			for n := 0; n < 15; n++ {
				t0 := time.Now()
				tt.Frame()
				toolFrames = append(toolFrames, time.Since(t0))
			}
			sort.Slice(toolFrames, func(i, j int) bool { return toolFrames[i] < toolFrames[j] })
			if med := toolFrames[len(toolFrames)/2]; med > 12*time.Millisecond {
				t.Errorf("[%s] tools view median frame %v exceeds 12ms budget", appearance, med)
			}

			// Expanding a trace with long JSON keeps header stationary and does not trigger content fade.
			fadeBeforeExpand := a.motionRuns.docBodyFade
			clickProjectUI(t, tt, "history tool 000 · success")
			tt.Frame()
			fixtureImage(t, tt, "raw-session-tools-expanded-"+appearance)
			if !s.Tools[item1.key()+"/"+item1.Detail.Traces[0].ID] || !tt.HasText("step 000") {
				t.Fatalf("[%s] expanding trace 0 did not reveal JSON body: %v", appearance, tt.Texts())
			}
			if a.motionRuns.docBodyFade != fadeBeforeExpand {
				t.Fatalf("[%s] expanding trace must not trigger docBodyFade (%d->%d)", appearance, fadeBeforeExpand, a.motionRuns.docBodyFade)
			}
			assertHeaderStationary("expanding trace")

			// 2. Switch 工具记录 -> 子会话 (3) -> 问答.
			fadeBeforeSub := a.motionRuns.docBodyFade
			clickProjectUI(t, tt, "子会话 (3)")
			tt.Advance(350 * time.Millisecond)
			tt.Frame()
			if !s.SessionSubagents || !tt.HasText("subagent topic 0") || a.motionRuns.docEnter != docEnter0 || a.motionRuns.docBodyFade <= fadeBeforeSub {
				t.Fatalf("[%s] subagents switch failed stationary/fade invariants", appearance)
			}
			assertHeaderStationary("subagents switch")

			fadeBeforeQA := a.motionRuns.docBodyFade
			clickProjectUI(t, tt, "问答")
			tt.Advance(350 * time.Millisecond)
			tt.Frame()
			if s.SessionTools || s.SessionSubagents || !tt.HasText("history question 000") || tt.HasText("history question 159") {
				t.Fatalf("[%s] switching back to 问答 failed virtualization or state", appearance)
			}
			if a.motionRuns.docEnter != docEnter0 || a.motionRuns.docBodyFade <= fadeBeforeQA {
				t.Fatalf("[%s] switching back to 问答 replayed docEnter or missed docBodyFade", appearance)
			}
			assertHeaderStationary("switching back to QA")
			qaFrames := make([]time.Duration, 0, 15)
			for n := 0; n < 15; n++ {
				t0 := time.Now()
				tt.Frame()
				qaFrames = append(qaFrames, time.Since(t0))
			}
			sort.Slice(qaFrames, func(i, j int) bool { return qaFrames[i] < qaFrames[j] })
			if med := qaFrames[len(qaFrames)/2]; med > 12*time.Millisecond {
				t.Errorf("[%s] QA view median frame %v exceeds 12ms budget", appearance, med)
			}

			// 3. Switch to another record: panel/header toolbar Y stays stationary, text & fade update.
			fadeBeforeItem2 := a.motionRuns.docBodyFade
			s.Selected = &item2
			s.SourcePage = history2
			s.SessionTools = false
			s.SessionSubagents = false
			tt.Advance(350 * time.Millisecond)
			tt.Frame()
			toolbarItem2, _ := tt.Find("项目内容工具栏")
			if toolbarItem2.Y != toolbarBefore.Y {
				t.Fatalf("[%s] switching item shifted header toolbar Y: %v -> %v", appearance, toolbarBefore.Y, toolbarItem2.Y)
			}
			if !tt.HasText("Second session title") || !tt.HasText("second session question") {
				t.Fatalf("[%s] second record content missing: %v", appearance, tt.Texts())
			}
			if a.motionRuns.docEnter <= docEnter0 || a.motionRuns.docBodyFade <= fadeBeforeItem2 {
				t.Fatalf("[%s] switching item did not arm docBodyFade/docEnter", appearance)
			}

			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Frame()
		})
	}
}

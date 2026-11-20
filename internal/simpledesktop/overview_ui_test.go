package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Pure UI fixture: no Start, model, write endpoint, directory discovery, or
// daily config. Output images are opt-in and explicitly labelled fixture.
func overviewFixture(t *testing.T) *App {
	t.Helper()
	state := filepath.Join(t.TempDir(), "client-state.json")
	b, _ := json.Marshal(firstUseState{Version: 1, Completed: time.Now().UTC().Format(time.RFC3339), Date: time.Now().Format("2006-01-02"), Zone: "fixture"})
	if err := os.WriteFile(state, b, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ClientStateFile: state}
	for n := 0; n < 13; n++ {
		name := fmt.Sprintf("Project %02d", n)
		path := fmt.Sprintf("/synthetic/team-%02d/project", n)
		if n < 2 {
			name = "same"
		}
		if n == 2 {
			name = "跨 Agent 项目记忆与长期上下文管理客户端 memory-client-exploration"
		}
		cfg.Projects = append(cfg.Projects, Project{ID: fmt.Sprint(n), Name: name, Path: path, Dataset: fmt.Sprint(n)})
	}
	a := New(cfg, nil)
	a.service = "已连接"
	// Static overview fixture has no server; ignore background callbacks instead
	// of executing them unsynchronized off the UI thread.
	a.Update = func(func()) {}
	a.overviewState.Loaded = true
	a.overviewState.RawLoaded = true
	a.overviewState.ActivityLoaded = true
	a.overviewState.AllProjects = true
	for n, title := range []string{
		"docs/development.md documents config/daily-runtime.example.json fields, credentials and the daily development workflow.",
		strings.Repeat("LongTechnicalIdentifier_", 22),
		"跨 Agent 项目记忆与长期上下文管理：同名项目路径、来源与逐层返回都要保持。",
		"Fourth knowledge for pagination",
	} {
		d := Document{ID: fmt.Sprint(n), Dataset: "0", Name: title, Created: time.Now().UTC().Format(time.RFC3339)}
		a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: title, Document: &d})
	}
	a.overviewState.Events = []overviewEvent{
		{Key: "complete", Title: "召回记忆", State: "已完成", Project: "synthetic", At: time.Now()},
		{Key: "running", Title: "整理知识", State: "处理中", Project: "synthetic", At: time.Now()},
		{Key: "failed", Title: "读取来源", State: "失败", Project: "synthetic", At: time.Now(), Record: activityRow{ID: "fixture-row", Run: "fixture-run", ErrorClass: "BrokenPipeError"}, Failures: []activityRow{{ID: "fixture-row", Run: "fixture-run", ErrorClass: "BrokenPipeError"}}},
	}
	return a
}

func fixtureImage(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MEMLANE_OVERVIEW_UI_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "fixture-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err = png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

// Same authored labels as the fixed A screenshot, separate from both the
// stress fixture and the live official store. No content is written to Cognee.
func overviewSelectedAFixture(t *testing.T) *App {
	a := overviewFixture(t)
	names := []string{"memlane", "same", "background-window", "跨 Agent 项目记忆与长期上下文管理客户端 / memory-client-exploration", "automatic", "same", "beta", "worktree", "base", "alpha", "race", "PA Native manual", "frontend-platform"}
	for i, name := range names {
		a.Config.Projects[i].Name = name
		a.Config.Projects[i].Path = "~/work/" + strings.ReplaceAll(name, " / ", "/")
	}
	a.Config.Projects[1].Path = "~/work/team-two/same"
	a.Config.Projects[5].Path = "~/work/team-one/same"
	a.overviewState.AllProjects = false
	titles := []string{"docs/development.md documents config/daily-runtime.example.json fields, credentials and the daily development workflow.", "Sandbox-denied httptest listeners abort `sh scripts/go.sh test ./internal/simpledesktop`.", "internal/simpledesktop/client.go sends X-Api-Key from token_file without Bearer fallback.", "技术说明先给出结果，再解释关键依据", "异步切换页面时核对请求所属对象"}
	a.overviewState.Snapshot.Knowledge = nil
	for i, title := range titles {
		at := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 46, 0, 0, time.Local)
		if i == 0 {
			at = at.Add(10 * time.Minute)
		}
		if i >= 3 {
			at = at.AddDate(0, 0, -1)
		}
		d := Document{ID: fmt.Sprint(i), Dataset: "0", Name: title, Created: at.Format(time.RFC3339)}
		a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: title, Document: &d})
	}
	a.overviewState.Snapshot.Raw = 6
	for i := range a.overviewState.Events {
		a.overviewState.Events[i].Project = "memlane"
		a.overviewState.Events[i].Record.Dataset = "0"
		clock := []struct{ h, m int }{{14, 44}, {14, 17}, {14, 10}}[i]
		a.overviewState.Events[i].At = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), clock.h, clock.m, 0, 0, time.Local)
	}
	return a
}

func TestOverviewActivityProjectUsesStableScope(t *testing.T) {
	a := overviewFixture(t)
	if _, ok := a.overviewActivityProject(overviewEvent{Project: "same"}); ok {
		t.Fatal("display name used as identity")
	}
	if i, ok := a.overviewActivityProject(overviewEvent{Project: "same", Record: activityRow{Dataset: "1"}}); !ok || i != 1 {
		t.Fatal("same-name project was misrouted")
	}
	a.Config.Projects[0].Dataset = "1"
	if _, ok := a.overviewActivityProject(overviewEvent{Record: activityRow{Dataset: "1"}}); ok {
		t.Fatal("ambiguous scope was routed")
	}
}

func TestOverviewLayoutAndContinuousUIFixture(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) {
			if os.Getenv("MEMLANE_OVERVIEW_UI_EVIDENCE") != "" {
				selected := overviewSelectedAFixture(t)
				baseline := ui.NewTester(selected.overviewShell, size[0], size[1])
				baseline.SetScale(2)
				fixtureImage(t, baseline, fmt.Sprintf("%dx%d-selected-a", size[0], size[1]))
			}
			a := overviewFixture(t)
			// Exclude lifecycle polling; exercise the actual homepage and sidebar.
			tt := ui.NewTester(a.overviewShell, size[0], size[1])
			fixtureImage(t, tt, fmt.Sprintf("%dx%d-home", size[0], size[1]))
			if a.overviewState.Scroll.MaxX != 0 || a.overviewState.SidebarScroll.MaxX != 0 {
				t.Fatal("horizontal overflow")
			}
			knowledge, _ := tt.Find(a.overviewState.Snapshot.Knowledge[0].Title)
			dynamic, _ := tt.Find("近期动态")
			if knowledge.X+knowledge.W > dynamic.X || knowledge.W < 200 || dynamic.X > float32(size[0]-200) {
				t.Fatalf("columns overlap or squeeze: knowledge=%+v dynamic=%+v", knowledge, dynamic)
			}
			if tt.HasText("下一页") || tt.HasText("上一页") {
				t.Fatal("obsolete pagination visible")
			}
			day := time.Now().Format("2006-01-02")
			if err := tt.Click(day + " · 新增 4 条记忆"); err != nil {
				t.Fatal(err)
			}
			if a.overviewState.Day != day {
				t.Fatal("date not selected")
			}
			if err := tt.Click("返回最近记忆"); err != nil {
				t.Fatal(err)
			}
			if a.overviewState.Day != "" {
				t.Fatal("date not cleared")
			}
			if err := tt.Click("失败操作：failed"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("查看失败详情"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("失败操作：failed"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("复制诊断信息"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(tt.Clipboard(), "fixture-run") || a.overviewState.CopiedEvent != "failed" {
				t.Fatal("diagnostic clipboard/feedback missing")
			}
			// Collapse diagnostic before checking project layout.
			if err := tt.Click("失败操作：failed"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("收起失败详情"); err != nil {
				t.Fatal(err)
			}
			a.showOverviewProjects()
			tt.Frame()
			// Reach the real bottom without replacing the dataset or resetting views.
			a.overviewState.Scroll.Y = a.overviewState.Scroll.MaxY
			tt.Frame()
			fixtureImage(t, tt, fmt.Sprintf("%dx%d-projects", size[0], size[1]))
			if a.overviewState.Scroll.MaxX != 0 || a.overviewState.Scroll.MaxY == 0 {
				t.Fatalf("project grid scroll: %+v", a.overviewState.Scroll)
			}
			if !tt.HasText("Project 12") {
				t.Fatal("all projects missing")
			}
			footer, _ := tt.Find("服务 · 已连接")
			if footer.Y > float32(size[1]) || footer.Y < float32(size[1]-70) {
				t.Fatalf("sidebar footer moved: %+v", footer)
			}
			a.overviewState.SidebarScroll.Y = a.overviewState.SidebarScroll.MaxY
			tt.Frame()
			if a.overviewState.Scroll.Y != a.overviewState.Scroll.MaxY {
				t.Fatal("sidebar scrolling changed body position")
			}
		})
	}
}

func TestOverviewLoadingEmptyAndPartialFailureUI(t *testing.T) {
	a := overviewFixture(t)
	v := &a.overviewState
	v.Loaded = false
	v.RawLoaded = false
	v.Loading = true
	v.ActivityLoaded = false
	v.ActivityLoading = true
	v.Snapshot = overviewSnapshot{}
	v.Events = nil
	tt := ui.NewTester(a.overviewHome, 980, 758)
	if !tt.HasText("读取中…") || tt.HasText("暂无记忆") || !tt.HasText("—") {
		t.Fatal("loading masquerades as zero/empty")
	}
	fixtureImage(t, tt, "loading")
	v.Loaded = true
	v.RawLoaded = true
	v.Loading = false
	v.ActivityLoaded = true
	v.ActivityLoading = false
	tt.Frame()
	if !tt.HasText("暂无记忆") || !tt.HasText("暂无动态") {
		t.Fatal("successful empty missing")
	}
	fixtureImage(t, tt, "empty")
	v.Snapshot.Knowledge = []Item{{ID: "kept", Title: "Last valid knowledge"}}
	v.Snapshot.Raw = 9
	v.Error = "fixture timeout"
	v.ActivityError = "fixture unavailable"
	v.Loading = true
	tt.Frame()
	if !tt.HasText("Last valid knowledge") || !tt.HasText("9") {
		t.Fatal("partial failure removed good content")
	}
	fixtureImage(t, tt, "partial-refresh")
}

func TestOverviewFixedListsAreCappedAndScrollIndependently(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) {
			a := overviewFixture(t)
			v := &a.overviewState
			v.Events = nil
			for n := 0; n < 60; n++ {
				v.Events = append(v.Events, overviewEvent{Key: fmt.Sprint(n), Title: fmt.Sprintf("Synthetic event %02d", n), State: "已完成", Project: "synthetic", At: time.Now()})
				v.Snapshot.Knowledge = append(v.Snapshot.Knowledge, Item{ID: fmt.Sprint("extra", n), Dataset: "0", Title: fmt.Sprintf("Extra knowledge %02d", n)})
			}
			tt := ui.NewTester(a.overviewShell, size[0], size[1])
			v.Scroll.Y = 120
			tt.Frame()
			left, _ := tt.Find("最近记忆内容")
			right, _ := tt.Find("近期动态内容")
			if left.H < homeOverviewListHeight-0.1 || left.H > homeOverviewListHeight+0.1 || right.H < homeOverviewListHeight-0.1 || right.H > homeOverviewListHeight+0.1 {
				t.Fatalf("unbounded panels: %+v %+v", left, right)
			}
			if d := left.Y - right.Y; d < -2 || d > 2 {
				t.Fatal("panel tops differ", left, right)
			}
			if len(a.overviewDisplayedRows(time.Now())) != 20 || !tt.HasText("Synthetic event 49") || tt.HasText("Synthetic event 50") {
				t.Fatal("list caps incorrect")
			}
			if tt.HasText("下一页") || tt.HasText("查看全部动态") || tt.HasText("加载更多动态") {
				t.Fatal("obsolete footer controls visible")
			}
			outerMax, outerY := v.Scroll.MaxY, v.Scroll.Y
			tt.Scroll(right.X+20, right.Y+80, 0, 180)
			if v.ActivityScroll.Y <= 0 || v.Scroll.Y != outerY || v.KnowledgeScroll.Y != 0 {
				t.Fatal("activity scroll leaked")
			}
			tt.Scroll(left.X+20, left.Y+80, 0, 180)
			if v.KnowledgeScroll.Y <= 0 || v.Scroll.Y != outerY {
				t.Fatal("knowledge scroll leaked")
			}
			v.Events = v.Events[:1]
			v.Snapshot.Knowledge = v.Snapshot.Knowledge[:1]
			tt.Frame()
			if v.Scroll.MaxY != outerMax {
				t.Fatal("short lists changed fixed height")
			}
			fixtureImage(t, tt, fmt.Sprintf("%dx%d-fixed-lists", size[0], size[1]))
		})
	}
}

func TestOverviewHeaderUsesContentInsets(t *testing.T) {
	a := overviewFixture(t)
	tt := ui.NewTester(a.projectHeader, 980, 48)
	if _, ok := tt.Find("总览"); ok {
		t.Fatal("outer toolbar should omit page title")
	}
	search, ok := tt.Find("搜索记忆与原始记录")
	if !ok {
		t.Fatal("missing search trigger")
	}
	if search.X+search.W < 980-12-1 || search.X+search.W > 980-12+1 {
		t.Fatalf("toolbar does not align with body insets: search=%+v", search)
	}
}

func TestOverviewSidebarHasNoProjectMenus(t *testing.T) {
	a := overviewFixture(t)
	tt := ui.NewTester(a.overviewShell, 1200, 820)
	for _, label := range projectLabels(a.Config.Projects) {
		if _, ok := tt.Find("项目菜单：" + label); ok {
			t.Fatal("sidebar still contains a project menu", label)
		}
	}
}

func TestOverviewReturnIgnoresHistoryFromBeforeThisVisit(t *testing.T) {
	a := overviewFixture(t)
	// No network callbacks can publish into this navigation-only fixture.
	a.Update = func(func()) {}
	a.page = "知识"
	a.project = 0
	p := a.state()
	p.History = []Item{{ID: "old-knowledge"}, {ID: "old-source"}}
	a.page = "总览"
	a.overviewState.Day = "2026-11-13"
	a.overviewState.Offset = 3
	a.overviewState.Scroll.Y = 123
	a.navigate("知识", 0)
	if a.overviewSourceFloor[p] != 2 {
		t.Fatal("visit did not capture the old source boundary")
	}
	// A newly followed source still unwinds once, stopping at that boundary.
	p.History = append(p.History, Item{ID: "new-source-parent"})
	a.returnToOverview()
	if a.page != "知识" || len(p.History) != 2 || a.overviewSourceFloor[p] != 2 {
		t.Fatal("new source did not stop at the visit boundary")
	}
	a.returnToOverview()
	if a.page != "总览" || len(p.History) != 2 {
		t.Fatal("old reading history consumed the homepage return")
	}
	if a.overviewState.Day != "2026-11-13" || a.overviewState.Offset != 3 || a.overviewState.Scroll.Y != 123 {
		t.Fatal("homepage state lost")
	}
	// A new explicit item selection owns a fresh source stack, even if this
	// pane had an older stack when it was re-entered from the homepage.
	a.navigate("知识", 0)
	a.selectItem(p, Item{ID: "new-item"})
	if a.overviewSourceFloor[p] != 0 {
		t.Fatal("new item retained the old source boundary")
	}
	p.History = []Item{{ID: "new-item"}}
	a.returnToOverview()
	if a.page != "知识" || len(p.History) != 0 {
		t.Fatal("fresh source did not return to its item")
	}
	a.returnToOverview()
	if a.page != "总览" {
		t.Fatal("fresh item did not return to homepage")
	}
}

func TestOverviewRefreshFailureKeepsViewAndExpandedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "p"}}}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })
	v := &a.overviewState
	v.Loaded = true
	v.RawLoaded = true
	v.Snapshot = overviewSnapshot{Raw: 9, Knowledge: []Item{{ID: "kept"}}}
	v.Day = "2026-11-13"
	v.Offset = 3
	v.Scroll.Y = 123
	v.ActivityScroll.Y = 42
	v.ActivityLimit = 60
	v.ExpandedEvent = "failed"
	v.ExpandedReport = "kept report"
	v.ActivityExpanded = true
	v.AllProjects = true
	a.loadOverview()
	// A later selection while requests are in flight must survive their callbacks.
	v.Day = "2026-11-12"
	drainReading(t, updates, func() bool { return !v.Loading && !v.ActivityLoading })
	if v.Snapshot.Raw != 9 || len(v.Snapshot.Knowledge) != 1 || v.Day != "2026-11-12" || v.Offset != 3 || v.Scroll.Y != 123 || v.ActivityScroll.Y != 42 || v.ActivityLimit != 60 || v.ExpandedEvent != "failed" || v.ExpandedReport != "kept report" || !v.ActivityExpanded || !v.AllProjects {
		t.Fatal("refresh reset valid content or view state")
	}
	if v.Error == "" || v.ActivityError == "" {
		t.Fatal("refresh failure hidden")
	}
}

func TestOverviewTitleKeepsReadableSentenceForTwoLineLayout(t *testing.T) {
	body := "docs/development.md documents config/daily-runtime.example.json fields, credentials and the daily development workflow. Another sentence."
	i := Item{Kind: "知识", Document: &Document{Name: "generated.txt"}, Body: body, BodyLoaded: true}
	// Reuse the existing readable sentence parser, with a homepage-only bound.
	i.Document.Name = ""
	got := overviewReadingTitle(i)
	if got != strings.Split(body, ". Another")[0]+"." {
		t.Fatalf("unexpected title %q", got)
	}
	if len([]rune(firstReadingSentence(body))) > 81 {
		t.Fatal("other reading title limit changed")
	}
}

func TestOverviewDirectoryRefreshPreservesDateAndRejectsStaleCallbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "p", Name: "original"}}}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })
	v := &a.overviewState
	v.Day = "2026-11-13"
	v.Offset = 3
	v.Scroll.Y = 100
	v.ActivityLimit = 40
	v.ExpandedEvent = "kept"
	a.Config.Projects[0].Name = "renamed"
	a.loadOverview()
	if v.Day != "2026-11-13" || v.ExpandedEvent != "kept" {
		t.Fatal("display metadata changed content identity")
	}
	// Hold both responses, change the range, then execute the old callbacks.
	oldContent, oldActivity := <-updates, <-updates
	a.Config.Common = []Project{{Dataset: "new-common"}}
	a.Client.Config = a.Config
	a.loadOverview()
	v.Day = "2026-11-12"
	oldContent()
	oldActivity()
	if !v.Loading || !v.ActivityLoading || v.Day != "2026-11-12" || v.Offset != 3 || v.Scroll.Y != 100 || v.ActivityLimit != 40 {
		t.Fatal("old callback changed new range/selection")
	}
	drainReading(t, updates, func() bool { return !v.Loading && !v.ActivityLoading })
}

func TestOverviewCurrentRuntimeReadOnly(t *testing.T) {
	path := os.Getenv("MEMLANE_OVERVIEW_READONLY_CONFIG")
	if path == "" {
		t.Skip("explicit live read-only check only")
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if cfg.ProjectControl != nil {
		d, err := (ProjectControlClient{Config: *cfg.ProjectControl}).directory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		a.applyAccessDirectory(d)
	}
	snapshot, err := a.Client.overview(ctx)
	if err != nil || snapshot.RawError != "" {
		t.Fatalf("read-only snapshot: %v %s", err, snapshot.RawError)
	}
	// Independently classify the official complete page results and stable IDs.
	knowledge, documents := map[string]bool{}, map[string]bool{}
	days := map[string]int{}
	excluded := 0
	for _, p := range overviewProjects(a.Config) {
		rows, err := a.Client.overviewDocuments(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range rows {
			key := d.Dataset + "/" + d.ID
			if d.knowledge() {
				if !knowledge[key] {
					knowledge[key] = true
					if day, ok := d.knowledgeDay(time.Local); ok {
						days[day]++
					}
				}
			} else if derivedMaterial(d) {
				excluded++
			} else {
				documents[key] = true
			}
		}
	}
	sessions, err := a.Client.overviewSessions(ctx, overviewProjects(a.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(knowledge) != len(snapshot.Knowledge) || len(documents)+len(sessions) != snapshot.Raw {
		t.Fatal("official enumeration differs from overview")
	}
	first, err := firstUseDays(a.Config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	statePath := cfg.ClientStateFile
	if statePath == "" {
		statePath = filepath.Join(filepath.Dir(path), "client-state.json")
	}
	state, _ := os.ReadFile(statePath)
	receipt := map[string]any{"checked_at": time.Now().UTC().Format(time.RFC3339), "source": "live official read-only; client date association only, no official/model/index writes", "knowledge": len(knowledge), "raw_sessions": len(sessions), "raw_documents": len(documents), "raw_total": snapshot.Raw, "excluded_derived": excluded, "knowledge_dates": days, "used_days": first, "client_state_sha256": fmt.Sprintf("%x", sha256.Sum256(state)), "projects": len(a.Config.Projects), "scopes": len(overviewProjects(a.Config))}
	data, _ := json.MarshalIndent(receipt, "", "  ")
	if dir := os.Getenv("MEMLANE_OVERVIEW_UI_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "official-readonly.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(data))
}

func TestOverviewRefreshKeepsResolvedTitles(t *testing.T) {
	doc := Document{ID: "k", Dataset: "p", Name: "", Created: "2026-11-13T00:00:00Z", ContentHash: "stable", Metadata: map[string]any{"node_set": []any{"session_learnings"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/data") {
			json.NewEncoder(w).Encode([]Document{doc})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{}})
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p"}}}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })
	v := &a.overviewState
	v.Snapshot.Knowledge[0].Title = "Resolved first sentence."
	v.Snapshot.Knowledge[0].ReadingState = "ready"
	a.recentScope = "already preheated old catalog"
	a.loadOverview()
	drainReading(t, updates, func() bool { return !v.Loading && !v.ActivityLoading })
	if v.Snapshot.Knowledge[0].Title != "Resolved first sentence." || a.recentScope != "" {
		t.Fatal("catalog publication lost readable title or blocked title hydration")
	}
}

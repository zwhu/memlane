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
			name = "跨 Agent 项目知识与长期上下文管理客户端 memory-client-exploration"
		}
		cfg.Projects = append(cfg.Projects, Project{ID: fmt.Sprint(n), Name: name, Path: path, Dataset: fmt.Sprint(n)})
	}
	a := New(cfg, nil)
	a.service = "已连接"
	a.Update = func(fn func()) { fn() }
	a.overviewState.Loaded = true
	a.overviewState.RawLoaded = true
	a.overviewState.ActivityLoaded = true
	a.overviewState.AllProjects = true
	for n, title := range []string{
		"docs/development.md documents config/daily-runtime.example.json fields, credentials and the daily development workflow.",
		strings.Repeat("LongTechnicalIdentifier_", 22),
		"跨 Agent 项目知识与长期上下文管理：同名项目路径、来源与逐层返回都要保持。",
		"Fourth knowledge for pagination",
	} {
		d := Document{ID: fmt.Sprint(n), Dataset: "0", Name: title, Created: time.Now().UTC().Format(time.RFC3339)}
		a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: title, Document: &d})
	}
	a.overviewState.Events = []overviewEvent{
		{Key: "complete", Title: "召回知识", State: "已完成", Project: "synthetic", At: time.Now()},
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

func TestOverviewLayoutAndContinuousUIFixture(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) {
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
			if err := tt.Click("下一页"); err != nil {
				t.Fatal(err)
			}
			if a.overviewState.Offset != 3 || !tt.HasText("Fourth knowledge for pagination") {
				t.Fatal("pagination failed")
			}
			if err := tt.Click("上一页"); err != nil {
				t.Fatal(err)
			}
			day := time.Now().Format("2006-01-02")
			if err := tt.Click(day + " · 新增 4 条知识"); err != nil {
				t.Fatal(err)
			}
			if a.overviewState.Day != day {
				t.Fatal("date not selected")
			}
			if err := tt.Click("显示最近知识"); err != nil {
				t.Fatal(err)
			}
			if a.overviewState.Day != "" {
				t.Fatal("date not cleared")
			}
			if err := tt.Click("查看失败详情"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("复制诊断信息"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(tt.Clipboard(), "fixture-run") || a.overviewState.CopiedEvent != "failed" {
				t.Fatal("diagnostic clipboard/feedback missing")
			}
			// Collapse diagnostic before checking project layout.
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
			footer, _ := tt.Find("本机服务 · 已连接")
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
	if !tt.HasText("正在读取统计…") || tt.HasText("暂无知识") || !tt.HasText("—") {
		t.Fatal("loading masquerades as zero/empty")
	}
	fixtureImage(t, tt, "loading")
	v.Loaded = true
	v.RawLoaded = true
	v.Loading = false
	v.ActivityLoaded = true
	v.ActivityLoading = false
	tt.Frame()
	if !tt.HasText("暂无知识") || !tt.HasText("暂无动态") {
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

func TestOverviewExpandedActivityStaysAlignedAndScrollsIndependently(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) {
			a := overviewFixture(t)
			v := &a.overviewState
			for n := 0; n < 30; n++ {
				v.Events = append(v.Events, overviewEvent{Key: fmt.Sprint(n), Title: fmt.Sprintf("Synthetic event %02d", n), State: "已完成", Project: "synthetic", At: time.Now()})
			}
			v.More = true
			tt := ui.NewTester(a.overviewShell, size[0], size[1])
			// Keep the footer controls inside the viewport at both window sizes.
			v.Scroll.Y = 120
			tt.Frame()
			outerMax, outerY := v.Scroll.MaxY, v.Scroll.Y
			if err := tt.Click("查看全部动态"); err != nil {
				t.Fatal(err)
			}
			if v.Scroll.MaxY != outerMax || v.Scroll.Y != outerY || v.ActivityScroll.MaxY <= 0 {
				t.Fatalf("expanded activity enlarged/moved the page: outer=%+v activity=%+v", v.Scroll, v.ActivityScroll)
			}
			left, _ := tt.Find("下一页")
			right, _ := tt.Find("收起动态")
			if d := (left.Y + left.H) - (right.Y + right.H); d < -2 || d > 2 {
				t.Fatalf("section bottoms differ: knowledge=%+v activity=%+v", left, right)
			}
			heading, _ := tt.Find("近期动态")
			tt.Scroll(heading.X+20, heading.Y+80, 0, 180)
			if v.ActivityScroll.Y <= 0 || v.Scroll.Y != outerY {
				t.Fatalf("activity wheel leaked to page: outer=%+v activity=%+v", v.Scroll, v.ActivityScroll)
			}
			v.ExpandedEvent = "failed"
			v.ExpandedReport = strings.Repeat("Synthetic diagnostic line\n", 100)
			tt.Frame()
			if v.Scroll.MaxY != outerMax {
				t.Fatal("diagnostic expanded the page")
			}
			fixtureImage(t, tt, fmt.Sprintf("%dx%d-bounded-activity", size[0], size[1]))
			if err := tt.Click("收起动态"); err != nil {
				t.Fatal(err)
			}
			if v.ActivityExpanded || v.ActivityScroll.Y != 0 || v.Scroll.MaxY != outerMax {
				t.Fatal("collapse changed outer layout or retained hidden scroll")
			}
			// The last page contains one short item. It must not inherit a
			// three-row minimum or show a clipped third activity by default.
			v.Offset = 3
			v.ExpandedEvent = ""
			tt.Frame()
			if v.Scroll.MaxY >= outerMax || v.ActivityScroll.MaxY > 6 {
				t.Fatalf("short page kept a tall/overflowing panel: outer=%+v activity=%+v", v.Scroll, v.ActivityScroll)
			}
			left, _ = tt.Find("下一页")
			right, _ = tt.Find("查看全部动态")
			if d := (left.Y + left.H) - (right.Y + right.H); d < -2 || d > 2 {
				t.Fatalf("short-page actions not aligned: left=%+v right=%+v", left, right)
			}
			fixtureImage(t, tt, fmt.Sprintf("%dx%d-compact-activity", size[0], size[1]))
			shortMax := v.Scroll.MaxY
			if err := tt.Click("查看全部动态"); err != nil {
				t.Fatal(err)
			}
			if v.Scroll.MaxY != shortMax || v.ActivityScroll.MaxY <= 0 {
				t.Fatal("short-page expansion grew the page")
			}
		})
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
					if at, ok := officialTime(d.Created); ok {
						days[at.In(time.Local).Format("2006-01-02")]++
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
	receipt := map[string]any{"checked_at": time.Now().UTC().Format(time.RFC3339), "source": "live official read-only; no model/index/write", "knowledge": len(knowledge), "raw_sessions": len(sessions), "raw_documents": len(documents), "raw_total": snapshot.Raw, "excluded_derived": excluded, "knowledge_dates": days, "used_days": first, "client_state_sha256": fmt.Sprintf("%x", sha256.Sum256(state)), "projects": len(a.Config.Projects), "scopes": len(overviewProjects(a.Config))}
	data, _ := json.MarshalIndent(receipt, "", "  ")
	if dir := os.Getenv("MEMLANE_OVERVIEW_UI_EVIDENCE"); dir != "" {
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

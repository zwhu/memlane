package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func readingControlConfig(t *testing.T, url string) *ProjectControlConfig {
	t.Helper()
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("synthetic-management-test-token-"+token), 0600); err != nil {
		t.Fatal(err)
	}
	return &ProjectControlConfig{URL: url, ManagementTokenFile: token}
}

func TestOverviewBootstrapUsesScopedTotalsAndSourceDayBuckets(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("read-only request expected")
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/overview/bootstrap":
			calls++
			if r.URL.Query().Get("recent_limit") != "20" {
				t.Error("overview did not request a bounded recent list")
			}
			totalRaw := 110
			json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
				TotalKnowledgeCount: 103, TotalRawCount: &totalRaw,
				Projects:   []v2OverviewProjectSummary{{DatasetID: "p", SessionCount: 102, RawDocCount: 1}, {DatasetID: "code", RawDocCount: 7}},
				DayBuckets: []v2OverviewDayBucket{{Day: "2026-11-13", Count: 103}},
				RecentItems: []v2KnowledgeItem{
					{DocID: "lesson", DatasetID: "p", EffectiveDate: "2026-11-13", CreatedAt: "2026-11-16T23:00:00"},
					{DocID: "unrelated", DatasetID: "code"},
				},
			})
		default:
			t.Errorf("out of scope request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL),
		Projects: []Project{{Dataset: "p", CodeDataset: "code"}, {Dataset: "p"}}, Common: []Project{{Dataset: "p"}}}
	a := New(cfg, nil)
	a.access.Loaded = true
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })
	v := a.overviewState
	if calls != 1 || v.Error != "" || v.V2TotalKnowledge != 103 || v.Snapshot.Raw != 103 ||
		len(v.Snapshot.Knowledge) != 1 || v.Snapshot.Knowledge[0].ID != "lesson" ||
		v.V2DayBuckets["2026-11-13"] != 103 || v.V2DayBuckets["2026-11-16"] != 0 {
		t.Fatalf("unexpected overview bootstrap: calls=%d state=%+v", calls, v)
	}
}

func TestOverviewBootstrapFirstUseAndProtectedDirectory(t *testing.T) {
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/overview/bootstrap":
			fmt.Fprint(w, `{"generation":1,"first_use_days":42,"day_buckets":[{"day":"2026-03-08","count":1}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	a := New(Config{ServiceURL: coldStartTestURL, ProjectControl: readingControlConfig(t, coldStartTestURL),
		ClientStateFile: filepath.Join(t.TempDir(), "client-state.json")}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	directory := AccessDirectory{Projects: []AccessProject{
		{ID: "visible", Name: "Visible", Dataset: "project"},
		{ID: "profiles-by-name", Name: "ml-user-profiles", Dataset: "profiles"},
		{ID: "profiles-by-dataset-name", Name: "Profiles", DatasetName: "ml-user-profiles"},
		{ID: "protected-by-server", Name: "Protected", Protected: true},
	}}
	a.applyAccessDirectory(directory)
	if got := a.access.Directory.Projects; len(got) != 1 || got[0].ID != "visible" {
		t.Fatalf("access directory projects = %+v", got)
	}
	if got := a.Config.Projects; len(got) != 1 || got[0].ID != "visible" {
		t.Fatalf("sidebar projects = %+v", got)
	}
	if directory.Projects[1].ID != "profiles-by-name" {
		t.Fatal("filter mutated the caller's directory")
	}
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading })
	if !a.overviewState.Loaded {
		t.Fatalf("bootstrap failed: %s", a.overviewState.Error)
	}
	now := time.Now()
	if days, err, ready := a.overviewFirstUseDays(now); days != 42 || err != nil || !ready {
		t.Fatalf("bootstrap fallback = %d, %v, %v", days, err, ready)
	}
	drainReading(t, updates, func() bool { return a.overviewFirstUse.Err != nil })
	if days, err, ready := a.overviewFirstUseDays(now); days != 42 || err != nil || !ready {
		t.Fatalf("bootstrap fallback after client-state error = %d, %v, %v", days, err, ready)
	}
	view := ui.NewTester(a.overviewHome, 1200, 820)
	view.Frame()
	if !view.HasText("42") || view.HasText("使用时间暂不可用。") {
		t.Fatalf("overview did not render the bootstrap fallback: %v", view.Texts())
	}
	a.overviewFirstUse.FallbackDays = 0
	a.overviewState.V2DayBuckets = map[string]int{now.AddDate(0, 0, -2).Format(time.DateOnly): 1, "invalid": 1}
	if days, err, ready := a.overviewFirstUseDays(now); days != 3 || err != nil || !ready {
		t.Fatalf("day bucket fallback = %d, %v, %v", days, err, ready)
	}
}

func TestOverviewFirstUseCalendarAndMissingState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "client-state.json")
	c := Config{ConfigFile: filepath.Join(root, "runtime.json")}
	if _, err := firstUseDays(c, time.Now()); err == nil {
		t.Fatal("missing state must not invent day")
	}
	state := firstUseState{Version: 1, Completed: "2026-03-08T07:00:00Z", Date: "2026-03-08", Zone: "America/New_York"}
	b, _ := json.Marshal(state)
	os.WriteFile(path, b, 0600)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		date string
		want int
	}{{"2026-03-08", 1}, {"2026-03-09", 2}, {"2026-03-07", 1}} {
		now, _ := time.ParseInLocation("2006-01-02", check.date, loc)
		got, err := firstUseDays(c, now)
		if err != nil || got != check.want {
			t.Fatalf("%s got %d %v", check.date, got, err)
		}
	}
	state.Date = "bad"
	b, _ = json.Marshal(state)
	os.WriteFile(path, b, 0600)
	if _, err := firstUseDays(c, time.Now()); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestOverviewActivityGroupsRunsAndNeverAssumesBackgroundCompletion(t *testing.T) {
	projects := []Project{{Dataset: "p", Name: "Project"}}
	rows := []activityRow{
		{ID: "start", Run: "run", Dataset: "p", Pipeline: "memify_pipeline", Status: "DATASET_PROCESSING_STARTED", Created: "2026-11-13T01:00:00Z"},
		{ID: "done", Run: "run", Dataset: "p", Pipeline: "memify_pipeline", Status: "DATASET_PROCESSING_COMPLETED", Outcome: "succeeded", Created: "2026-11-13T01:01:00Z"},
		{ID: "bg", Run: "background", Dataset: "p", Operation: "remember", Background: true, Outcome: "succeeded", Created: "2026-11-13T02:00:00Z"},
		{ID: "child", Run: "child-run", Parent: "background", Dataset: "p", Pipeline: "add_pipeline", Status: "DATASET_PROCESSING_COMPLETED", Created: "2026-11-13T02:01:00Z"},
		{ID: "fail", Run: "recall", Operation: "recall", Outcome: "failed", Created: "2026-11-13T03:00:00Z"},
		{ID: "other", Run: "other", Dataset: "unrelated", Operation: "recall", Outcome: "succeeded", Created: "2026-11-13T04:00:00Z"},
	}
	events := aggregateActivity(rows, projects)
	if len(events) != 3 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].Title != "召回记忆" || events[0].State != "失败" || events[0].Project != "全局" {
		t.Fatalf("failed recall lost: %+v", events[0])
	}
	if events[1].State != "已启动" {
		t.Fatalf("background accepted became completion: %+v", events[1])
	}
	if events[2].State != "已完成" {
		t.Fatalf("terminal run missing: %+v", events[2])
	}
}

func TestOverviewActivityReadsBeyondOnePageAndFiltersScope(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rows := []activityRow{}
		for i := offset; i < min(offset+100, 125); i++ {
			dataset := "other"
			if i >= 100 {
				dataset = "p"
			}
			rows = append(rows, activityRow{ID: fmt.Sprint(i), Run: fmt.Sprint("run", i), Dataset: dataset, Operation: "remember", Outcome: "succeeded", Created: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)})
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	events, more, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{Dataset: "p", Name: "P"}}}}).overviewActivity(context.Background(), 20)
	if err != nil || calls != 2 || len(events) != 20 || !more {
		t.Fatalf("events=%d calls=%d more=%v err=%v", len(events), calls, more, err)
	}
}

func TestCompletedBackgroundOperationUsesOfficialEndTime(t *testing.T) {
	row := activityRow{ID: "dcim-improve", Operation: "improve", Background: true, Outcome: "succeeded"}
	if activityState(row) != "已启动" {
		t.Fatal("submission alone was treated as completed")
	}
	row.Ended = "unknown"
	if activityState(row) != "已启动" {
		t.Fatal("invalid timestamp was treated as completion")
	}
	row.Ended = "2026-11-15T01:51:05.610946"
	if activityState(row) != "已完成" {
		t.Fatal("completed background operation still shows started")
	}
	row.Outcome = "failed"
	if activityState(row) != "失败" {
		t.Fatal("end time hid failure")
	}
}

func TestOverviewActivityUsesSingleLargePageAndLinearAggregation(t *testing.T) {
	calls := 0
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotLimit = r.URL.Query().Get("limit")
		limit, _ := strconv.Atoi(gotLimit)
		if limit <= 0 {
			limit = 100
		}
		rows := make([]activityRow, 0, limit)
		base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
		for i := 0; i < limit; i++ {
			runID := fmt.Sprintf("run-%d", i/3)
			rows = append(rows, activityRow{
				ID:        fmt.Sprintf("id-%d", i),
				Run:       runID,
				Dataset:   "p",
				Operation: "improve",
				Outcome:   "succeeded",
				Created:   base.Add(-time.Duration(i) * time.Second).Format(time.RFC3339),
			})
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	events, more, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{Dataset: "p", Name: "P"}}}}).overviewActivity(context.Background(), 50)
	if err != nil || calls != 1 || gotLimit != "250" || len(events) != 50 || !more {
		t.Fatalf("expected 1 single page with limit=250: calls=%d limit=%q events=%d more=%v err=%v", calls, gotLimit, len(events), more, err)
	}

	// Verify O(N) aggregateActivity handles 2000 parent/child rows quickly.
	projects := []Project{{Dataset: "p", Name: "P"}}
	many := make([]activityRow, 0, 2000)
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		rootID := fmt.Sprintf("root-%d", i)
		many = append(many,
			activityRow{ID: rootID, Run: rootID, Dataset: "p", Operation: "improve", Background: true, Outcome: "succeeded", Created: base.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)},
			activityRow{ID: fmt.Sprintf("child-%d", i), Run: fmt.Sprintf("child-%d", i), Parent: rootID, Dataset: "p", Pipeline: "memify_pipeline", Outcome: "failed", Created: base.Add(-time.Duration(i)*time.Minute + time.Second).Format(time.RFC3339)},
		)
	}
	t0 := time.Now()
	agg := aggregateActivity(many, projects)
	elapsed := time.Since(t0)
	if len(agg) != 1000 || agg[0].State != "失败" || len(agg[0].Failures) != 1 {
		t.Fatalf("unexpected aggregation: len=%d first=%+v", len(agg), agg[0])
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("aggregateActivity too slow for 2000 rows: %v", elapsed)
	}
}

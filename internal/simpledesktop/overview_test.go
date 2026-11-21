package simpledesktop

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
)

func TestOverviewFullPagesScopeAndDerivedMaterials(t *testing.T) {
	docs := make([]Document, 0, 106)
	for n := 0; n < 103; n++ {
		docs = append(docs, Document{ID: fmt.Sprint(n), Dataset: "p", Created: "2026-11-16T23:00:00", Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": "2026-11-13"}})
	}
	docs = append(docs, Document{ID: "session-copy", Dataset: "p", Metadata: map[string]any{"node_set": []any{"user_sessions_from_cache"}}}, Document{ID: "trace-copy", Dataset: "p", Metadata: map[string]any{"node_set": []any{"agent_trace_feedbacks"}}}, Document{ID: "attachment", Dataset: "p", Metadata: map[string]any{"session_id": "s0"}})
	sessionCalls, docCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("read-only request expected")
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		switch r.URL.Path {
		case "/api/v1/datasets/p/data":
			docCalls++
			end := min(offset+100, len(docs))
			json.NewEncoder(w).Encode(docs[offset:end])
		case "/api/v1/sessions":
			sessionCalls++
			rows := []Session{}
			for i := offset; i < min(offset+100, 102); i++ {
				rows = append(rows, Session{ID: fmt.Sprint("s", i), Dataset: "p", User: "u"})
			}
			if offset == 100 {
				rows = append(rows, Session{ID: "unrelated", Dataset: "code", User: "u"})
			}
			json.NewEncoder(w).Encode(map[string]any{"sessions": rows, "has_more": offset == 0})
		default:
			t.Errorf("out of scope request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{Config: Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p", CodeDataset: "code"}, {Dataset: "p"}}, Common: []Project{{Dataset: "p"}}}}
	snapshot, err := client.overview(context.Background())
	if err != nil || snapshot.RawError != "" || len(snapshot.Knowledge) != 103 || snapshot.Raw != 103 {
		t.Fatalf("knowledge=%d raw=%d rawErr=%s err=%v", len(snapshot.Knowledge), snapshot.Raw, snapshot.RawError, err)
	}
	if docCalls != 2 || sessionCalls != 2 {
		t.Fatalf("must traverse once in parallel and dedup dataset: docs=%d sessions=%d", docCalls, sessionCalls)
	}
	zone := time.FixedZone("+8", 8*3600)
	if got := overviewBuckets(snapshot, zone)["2026-11-13"]; got != 103 {
		t.Fatalf("source date must be independent of processing date: %d", got)
	}
}

func TestOverviewRejectsChangingPagesAndKeepsKnowledgeOnSessionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			http.Error(w, "unavailable", 503)
			return
		}
		json.NewEncoder(w).Encode([]Document{{ID: "knowledge", Dataset: "p", Created: "2026-11-13T01:00:00Z", Metadata: map[string]any{"node_set": []any{"session_learnings"}}}})
	}))
	defer server.Close()
	snapshot, err := (Client{Config: Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p"}}}}).overview(context.Background())
	if err != nil || len(snapshot.Knowledge) != 1 || snapshot.RawError == "" {
		t.Fatalf("knowledge lost on session failure: %+v %v", snapshot, err)
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
	events, more, err := (Client{Config: Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p", Name: "P"}}}}).overviewActivity(context.Background(), 20)
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
	events, more, err := (Client{Config: Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p", Name: "P"}}}}).overviewActivity(context.Background(), 50)
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

func TestOverviewBootstrapBuildsSnapshotTitlesRecallsAndActivity(t *testing.T) {
	projects := []Project{{ID: "p1", Name: "memlane", Dataset: "ds1"}}
	resp := overviewBootstrapResponse{
		Documents: []Document{{
			ID:       "doc1",
			Dataset:  "ds1",
			Name:     "text_0780c6ebf543a429fd39d8b6fee044e1",
			Created:  "2026-11-20T12:00:00Z",
			Metadata: map[string]any{"node_set": []any{"session_learnings"}, "first_source_date": "2026-11-18"},
		}},
		Sessions: []Session{{ID: "s1", User: "u1", Dataset: "ds1"}},
		Previews: map[string]string{"ds1/doc1": "First preview sentence for overview. Second sentence."},
		PipelineRuns: []activityRow{{
			ID:        "r1",
			Run:       "run1",
			Dataset:   "ds1",
			Operation: "improve",
			Outcome:   "succeeded",
			Created:   "2026-11-20T12:05:00Z",
		}},
		RecallEvents: []RecallEvent{{
			ID:        "ev1",
			AgentID:   "a1",
			AgentName: "Gemini App",
			Channel:   "automatic",
			Received:  "2026-11-20T12:06:00Z",
			Hits:      []RecallHit{{Dataset: "ds1", Document: "doc1"}},
		}},
		RecallStartedAt: "2026-11-10T00:00:00Z",
		RecallCounts: map[string]*KnowledgeRecallStats{
			"ds1/doc1": {Count: 7, LastRecalledAt: "2026-11-20T12:06:00Z"},
		},
	}
	snapshot, events, more, hasRecall := buildOverviewFromBootstrap(Client{}, projects, resp, 50)
	if !hasRecall || snapshot.Raw != 1 || len(snapshot.Knowledge) != 1 {
		t.Fatalf("unexpected snapshot: raw=%d knowledge=%d hasRecall=%v", snapshot.Raw, len(snapshot.Knowledge), hasRecall)
	}
	k := snapshot.Knowledge[0]
	if k.ReadingState != "ready" || k.Title != "First preview sentence for overview." {
		t.Fatalf("preview title not applied: state=%q title=%q", k.ReadingState, k.Title)
	}
	if k.RecallStats == nil || k.RecallStats.Count != 7 {
		t.Fatalf("recall stats missing: %+v", k.RecallStats)
	}
	if more || len(events) != 2 || events[0].Recall == nil || events[0].Title != "Gemini App 自动召回" {
		t.Fatalf("unexpected events: more=%v events=%+v", more, events)
	}
}

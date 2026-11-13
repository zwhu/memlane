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
	"strings"
	"testing"
	"time"
)

func TestOverviewFullPagesScopeAndDerivedMaterials(t *testing.T) {
	docs := make([]Document, 0, 106)
	for n := 0; n < 103; n++ {
		docs = append(docs, Document{ID: fmt.Sprint(n), Dataset: "p", Created: "2026-11-12T23:00:00", Metadata: map[string]any{"node_set": []any{"session_learnings"}}})
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
	if docCalls != 4 || sessionCalls != 4 {
		t.Fatalf("must traverse twice and dedup dataset: docs=%d sessions=%d", docCalls, sessionCalls)
	}
	zone := time.FixedZone("+8", 8*3600)
	if got := overviewBuckets(snapshot, zone)["2026-11-13"]; got != 103 {
		t.Fatalf("UTC to local date: %d", got)
	}
}

func TestOverviewRejectsChangingPagesAndKeepsKnowledgeOnSessionError(t *testing.T) {
	for _, changing := range []bool{true, false} {
		t.Run(fmt.Sprint(changing), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/sessions" {
					http.Error(w, "unavailable", 503)
					return
				}
				calls++
				id := "knowledge"
				if changing && calls > 1 {
					id = "replaced"
				}
				json.NewEncoder(w).Encode([]Document{{ID: id, Dataset: "p", Created: "2026-11-13T01:00:00Z", Metadata: map[string]any{"node_set": []any{"session_learnings"}}}})
			}))
			defer server.Close()
			snapshot, err := (Client{Config: Config{ServiceURL: server.URL, Projects: []Project{{Dataset: "p"}}}}).overview(context.Background())
			if changing {
				if err == nil || !strings.Contains(err.Error(), "正在更新") {
					t.Fatalf("accepted unstable statistics: %v", err)
				}
			} else if err != nil || len(snapshot.Knowledge) != 1 || snapshot.RawError == "" {
				t.Fatalf("knowledge lost on session failure: %+v %v", snapshot, err)
			}
		})
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
	if events[0].Title != "召回知识" || events[0].State != "失败" || events[0].Project != "全局" {
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

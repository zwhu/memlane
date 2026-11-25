package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sourceLesson(id, day string) Document {
	return Document{ID: id, Dataset: "project-a", Created: "2026-11-16T04:30:00Z",
		Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": day,
			"state_key": "example-state", "lesson_type": "state"}}
}

func TestKnowledgeOptionBDatePriorityAndCreatedAtFallback(t *testing.T) {
	d := sourceLesson("unlinked", "2026-11-15")
	d.Metadata["source_date_status"] = "unconfirmed"
	if day, ok := documentKnowledgeDay(d, time.Local); !ok || day != "2026-11-15" {
		t.Fatalf("legacy unconfirmed session_date was blocked under Option B: day=%q ok=%v", day, ok)
	}
	d.Metadata["first_source_date"] = "2026-11-14"
	d.Metadata["source_date_status"] = "linked_qa"
	if day, ok := documentKnowledgeDay(d, time.Local); !ok || day != "2026-11-14" {
		t.Fatal("explicit QA origin was ignored", day)
	}
}

func TestKnowledgeDayUsesOwnTurnDateAndFallsBackToLocalCreatedTime(t *testing.T) {
	d := sourceLesson("lesson", "2026-11-15")
	location := time.FixedZone("UTC-10", -10*3600)
	day, ok := documentKnowledgeDay(d, location)
	if !ok || day != "2026-11-15" {
		t.Fatalf("source day = %q, %v", day, ok)
	}
	if d.Created != "2026-11-16T04:30:00Z" {
		t.Fatal("actual creation time changed")
	}
	buckets := overviewBuckets(overviewSnapshot{Knowledge: []Item{{Document: &d}}}, location)
	if buckets["2026-11-15"] != 1 || buckets["2026-11-16"] != 0 {
		t.Fatalf("buckets = %#v", buckets)
	}
	for _, missing := range []string{"", "2026-02-30", "not-a-date"} {
		x := sourceLesson("fallback", missing)
		if day, ok := documentKnowledgeDay(x, location); !ok || day != "2026-11-15" {
			t.Fatalf("expected createdAt local fallback 2026-11-15, got %q %v", day, ok)
		}
		x.Created = ""
		if _, ok := documentKnowledgeDay(x, location); ok {
			t.Fatal("empty createdAt produced a date")
		}
	}
}

func TestKnowledgeFirstSourceSurvivesReprocessingAndProjectBoundaries(t *testing.T) {
	root := t.TempDir()
	c := Client{Config: Config{ClientStateFile: filepath.Join(root, "client-state.json"), ServiceURL: "http://127.0.0.1:1"}}
	rows := []Document{sourceLesson("first-id", "2026-11-15")}
	if err := c.rememberKnowledgeDays(rows); err != nil {
		t.Fatal(err)
	}
	rows = []Document{sourceLesson("replacement-id", "2026-11-16"), sourceLesson("other-project", "2026-11-16")}
	rows[1].Dataset = "project-b"
	if err := c.rememberKnowledgeDays(rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].FirstSourceDate != "2026-11-15" || rows[1].FirstSourceDate != "2026-11-16" {
		t.Fatalf("source dates = %#v", rows)
	}
	for _, path := range mustGlob(t, root, "knowledge-source-dates-*.json") {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "example-state") || strings.Contains(string(data), "replacement-id") {
			t.Fatal("date cache retained original identifiers/text instead of hashed identity")
		}
	}
}

func mustGlob(t *testing.T, root, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil || len(paths) != 1 {
		t.Fatalf("date cache: %v %v", paths, err)
	}
	return paths
}

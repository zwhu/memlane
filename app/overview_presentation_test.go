package app

import (
	"strings"
	"testing"
	"time"
)

func TestOverviewFailureGroupingPreservesDiagnosticsAndProjectBoundaries(t *testing.T) {
	at := time.Now()
	rows := []overviewEvent{
		{Key: "one", Project: "daily-cli", State: "失败", At: at, Record: activityRow{Run: "one", ErrorClass: "BrokenPipeError"}},
		{Key: "two", Project: "daily-cli", State: "失败", At: at, Record: activityRow{Run: "two", ErrorClass: "BrokenPipeError"}},
		{Key: "other", Project: "memlane", State: "失败", At: at, Record: activityRow{Run: "other", ErrorClass: "BrokenPipeError"}},
		{Key: "different-cause", Project: "daily-cli", State: "失败", At: at, Record: activityRow{Run: "different", ErrorClass: "PermissionDeniedError"}},
		{Key: "same-name-other-dataset", Project: "daily-cli", State: "失败", At: at, Record: activityRow{Run: "separate", Dataset: "other-project", ErrorClass: "BrokenPipeError"}},
	}
	grouped := groupOverviewFailures(rows)
	if len(grouped) != 4 || grouped[0].Title != "daily-cli 失败 ×2" || len(grouped[0].Failures) != 2 {
		t.Fatal("same failure was not grouped independently per project")
	}
	report := failureReport(grouped[0], Config{})
	if !strings.Contains(report, "one") || !strings.Contains(report, "two") || len(rows[0].Failures) != 0 {
		t.Fatal("grouping lost diagnostics or modified original activity data")
	}
}

func TestProjectMiddleNamesAndKnowledgePreview(t *testing.T) {
	a := middleProjectName("memlane-ollama-thinking-coding-model-a")
	b := middleProjectName("memlane-ollama-thinking-coding-model-b")
	if a == b || !strings.HasSuffix(a, "del-a") || !strings.Contains(a, "…") {
		t.Fatal("project suffix identity was lost")
	}
	if knowledgePreview("python3 -m unittest -v passes 4 tests with 0 failures") != "python3 -m unittest -v passes 4 tests with 0 failures" {
		t.Fatal("test output was rewritten")
	}
	item := Item{Kind: "知识", Title: "python3 -m unittest -v passes 4 tests with 0 failures"}
	if projectDisplayTitle(item) != "" {
		t.Fatal("list preview became a fabricated detail title")
	}
}

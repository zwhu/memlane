package simpledesktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOverviewFailureReportIncludesEvidenceWithoutSensitiveLogPayloads(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "logs"), 0700)
	log := `2026-11-13T03:34:38.830027 [ERROR] Recall endpoint error BrokenPipeError
Traceback (most recent call last):
  File "/runtime/multiprocessing/util.py", line 442, in _flush_std_streams
    sys.stderr.flush()
Authorization: Bearer secret-log-token
Question: private-session-text
BrokenPipeError: private-error-payload
2026-11-13T03:34:39.000000 [INFO] unrelated event
`
	os.WriteFile(filepath.Join(root, "logs", "official.log"), []byte(log), 0600)
	failed := activityRow{ID: "failure-row", Run: "run-id", Dataset: "p", Operation: "recall", Origin: "api", ErrorClass: "BrokenPipeError", Created: "2026-11-13T03:34:38.850027", Outcome: "failed"}
	event := overviewEvent{Key: "run-id", Title: "召回知识", State: "失败", Project: "Project", At: time.Date(2026, 11, 13, 3, 34, 38, 0, time.UTC), Failures: []activityRow{failed}}
	report := failureReport(event, Config{OfficialData: root, ServiceURL: "http://user:secret-url-password@127.0.0.1:61255/?key=secret-query-token", TokenFile: "secret-token-file", ServiceCommit: "fixed-commit"})
	for _, want := range []string{"BrokenPipeError", "run-id", "failure-row", "_flush_std_streams", "fixed-commit", "127.0.0.1:61255"} {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q in report", want)
		}
	}
	for _, secret := range []string{"secret-log-token", "private-session-text", "private-error-payload", "secret-url-password", "secret-query-token", "secret-token-file", "Authorization:"} {
		if strings.Contains(report, secret) {
			t.Fatalf("sensitive payload copied: %s", secret)
		}
	}
	if !strings.Contains(report, "尚未按任务 ID 精确确认") {
		t.Fatal("temporal candidate claimed to be an exact join")
	}
	failed.Created = "2026-11-13T06:00:00Z"
	if got := failureLogCandidate(root, failed); got != "" {
		t.Fatal("unrelated historical log matched")
	}
}

func TestOverviewFailureReportDoesNotInventMissingDetails(t *testing.T) {
	event := overviewEvent{Key: "x", State: "失败"}
	report := failureReport(event, Config{})
	if !strings.Contains(report, "官方未返回失败详情") || strings.Contains(report, "BrokenPipeError") {
		t.Fatal("invented failure context")
	}
}

func TestOverviewChildFailureRetainsItsOriginalDiagnosticIdentity(t *testing.T) {
	rows := []activityRow{
		{ID: "accepted", Run: "operation", Dataset: "p", Operation: "improve", Background: true, Outcome: "succeeded", Created: "2026-11-13T03:30:53Z"},
		{ID: "failed-row", Run: "pipeline", Parent: "operation", Dataset: "p", Pipeline: "memify_pipeline", ErrorClass: "BrokenPipeError", Outcome: "failed", Status: "DATASET_PROCESSING_ERRORED", Created: "2026-11-13T03:30:52Z"},
	}
	events := aggregateActivity(rows, []Project{{Dataset: "p", Name: "Project"}})
	if len(events) != 1 || events[0].State != "失败" || len(events[0].Failures) != 1 || events[0].Failures[0].Run != "pipeline" {
		t.Fatalf("child failure lost: %+v", events)
	}
	if eventFailureReason(events[0]) != "连接管道已断开" {
		t.Fatal("human readable error missing")
	}
}

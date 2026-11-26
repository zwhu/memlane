package app

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
	event := overviewEvent{Key: "run-id", Title: "召回记忆", State: "失败", Project: "Project", At: time.Date(2026, 11, 13, 3, 34, 38, 0, time.UTC), Failures: []activityRow{failed}}
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
	if !strings.Contains(report, "官方未返回失败详情") || !strings.Contains(report, "发生时间：时间未知") || strings.Contains(report, "BrokenPipeError") {
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

func TestOverviewFailureDetailCollapsesParentWrapperAndFormatsScreenshotScenario(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "logs"), 0700)
	parent := activityRow{
		ID:        "6299cdd3-51df-4c45-bd3f-c58bf80ce11e",
		Run:       "7c467ee6-3fb2-444d-ba80-9f55b90ca45b",
		Dataset:   "0785e3d2-881a-58c4-883e-cc3d2dcf76ef",
		Operation: "improve",
		Origin:    "api",
		Outcome:   "failed",
		Created:   "2026-11-20T20:21:46.247797",
		Started:   "2026-11-20T19:57:53.712232",
		Ended:     "2026-11-20T20:21:46.240281",
	}
	child := activityRow{
		ID:         "e43280b7-f87f-4c87-bd98-cb00b76e87d8",
		Run:        "e967a7e9-d20a-4184-85e3-4886ba4ffdea",
		Parent:     parent.Run,
		Dataset:    parent.Dataset,
		Pipeline:   "memify_pipeline",
		Origin:     "api",
		ErrorClass: "ConnectTimeout",
		Outcome:    "failed",
		Status:     "DATASET_PROCESSING_ERRORED",
		Created:    "2026-11-20T20:21:42.579387",
		Started:    "2026-11-20T20:20:26.078927",
		Ended:      "2026-11-20T20:21:42.565162",
	}
	event := overviewEvent{
		Key:      parent.Run,
		Title:    "提炼记忆",
		State:    "失败",
		Project:  "memlane",
		At:       time.Date(2026, 11, 20, 20, 21, 46, 240281000, time.UTC),
		Record:   parent,
		Failures: []activityRow{parent, child},
	}
	cfg := Config{
		OfficialData:  root,
		ServiceURL:    "http://127.0.0.1:61255",
		ServiceCommit: "ec6f40859e5f2df9e7862dde27580ac3268ed43",
	}
	cst := time.FixedZone("CST", 8*3600)
	detail := buildFailureDetail(event, cfg, cst)
	if detail.ReasonLabel != "连接超时" || detail.ErrorClass != "ConnectTimeout" {
		t.Fatalf("unexpected primary reason: %+v", detail)
	}
	if detail.StageLabel != "提炼记忆流水线" || detail.StageRaw != "memify_pipeline" {
		t.Fatalf("unexpected stage: %+v", detail)
	}
	if detail.FailureCount != 1 || len(detail.Items) != 1 {
		t.Fatalf("expected parent wrapper collapsed from Items, got count=%d items=%+v", detail.FailureCount, detail.Items)
	}
	if detail.Items[0].LocalTime != "2026/11/21 04:20:26 – 04:21:42" || detail.Items[0].Duration != "1 分 16 秒" {
		t.Fatalf("unexpected child local time/duration: %+v", detail.Items[0])
	}
	if len(detail.TechRecords) != 2 || detail.TechRecords[0].StageRaw != "memify_pipeline" || !detail.TechRecords[1].IsParentWrapper {
		t.Fatalf("unexpected tech records ordering/wrapper flag: %+v", detail.TechRecords)
	}

	// Child missing Started/Ended borrows outer parent timing window for readability (23m53s)
	// while TechRecords keeps raw empty UTC fields untouched.
	childNoTiming := child
	childNoTiming.Started, childNoTiming.Ended = "", ""
	eventBorrowed := event
	eventBorrowed.Failures = []activityRow{parent, childNoTiming}
	borrowed := buildFailureDetail(eventBorrowed, cfg, cst)
	if borrowed.LocalTime != "2026/11/21 03:57:53 – 04:21:46" || borrowed.Duration != "23 分 53 秒" {
		t.Fatalf("unexpected borrowed window: time=%q dur=%q", borrowed.LocalTime, borrowed.Duration)
	}
	if borrowed.TechRecords[0].StartedUTC != "" || borrowed.TechRecords[0].EndedUTC != "" {
		t.Fatalf("raw tech record fabricated child timestamps: %+v", borrowed.TechRecords[0])
	}

	report := failureReport(event, cfg)
	if idxErr, idxParent := strings.Index(report, "ConnectTimeout"), strings.Index(report, "操作：提炼记忆任务（improve）"); idxErr < 0 || idxParent < 0 || idxErr > idxParent {
		t.Fatalf("expected ConnectTimeout prioritized before improve wrapper in report:\n%s", report)
	}
	if strings.Contains(report, "错误类型：\n") || strings.Contains(report, "父操作 ID：\n") {
		t.Fatalf("report retained blank label lines:\n%s", report)
	}
}

func TestOverviewFailureDetailMergedMultipleFailures(t *testing.T) {
	earlier := activityRow{ID: "a1", Run: "r1", Pipeline: "memify_pipeline", ErrorClass: "ConnectTimeout", Created: "2026-11-20T18:00:00Z"}
	later := activityRow{ID: "a2", Run: "r2", Pipeline: "cognify_pipeline", ErrorClass: "BadGatewayError", Created: "2026-11-20T19:00:00Z"}
	event := overviewEvent{
		Key:      "r2",
		Title:    "memlane 失败 ×2",
		State:    "失败",
		Project:  "memlane",
		At:       time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC),
		Failures: []activityRow{earlier, later},
	}
	detail := buildFailureDetail(event, Config{}, time.UTC)
	if detail.FailureCount != 2 || len(detail.Items) != 2 {
		t.Fatalf("expected 2 merged failures, got %+v", detail)
	}
	if detail.Items[0].ErrorClass != "BadGatewayError" || detail.Items[1].ErrorClass != "ConnectTimeout" {
		t.Fatalf("expected newest failure first, got %+v", detail.Items)
	}
}

func TestOverviewFailureDurationLocalWindowAndExplanations(t *testing.T) {
	if got := formatDurationZH(400 * time.Millisecond); got != "小于 1 秒" {
		t.Fatalf("sub-second=%q", got)
	}
	if got := formatDurationZH(42 * time.Second); got != "42 秒" {
		t.Fatalf("seconds=%q", got)
	}
	if got := formatDurationZH(5 * time.Minute); got != "5 分钟" {
		t.Fatalf("exact minutes=%q", got)
	}
	if got := formatDurationZH(23*time.Minute + 52528*time.Millisecond); got != "23 分 53 秒" {
		t.Fatalf("minutes+seconds=%q", got)
	}
	if got := formatDurationZH(2*time.Hour + 15*time.Minute); got != "2 小时 15 分" {
		t.Fatalf("hours+minutes=%q", got)
	}
	cst := time.FixedZone("CST", 8*3600)
	win, dur := formatLocalWindow("2026-11-20T15:50:00Z", "2026-11-20T16:10:00Z", "", time.Time{}, cst)
	if win != "2026/11/20 23:50:00 – 2026/11/21 00:10:00" || dur != "20 分钟" {
		t.Fatalf("cross-day window=%q dur=%q", win, dur)
	}
	fb, fbDur := formatLocalWindow("", "", "", time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), cst)
	if fb != "2026/11/20 20:00:00" || fbDur != "" {
		t.Fatalf("fallback=%q dur=%q", fb, fbDur)
	}
	if unk, _ := formatLocalWindow("", "", "", time.Time{}, cst); unk != "时间未知" {
		t.Fatalf("unknown=%q", unk)
	}
	for class, want := range map[string]string{
		"ConnectTimeout":            "连接超时",
		"ReadTimeout":               "等待响应超时",
		"TimeoutError":              "操作超时",
		"ConnectError":              "网络连接失败",
		"BadGatewayError":           "Agent 模型代理异常",
		"BrokenPipeError":           "连接管道已断开",
		"PermissionDeniedError":     "访问被拒绝",
		"RateLimitError":            "请求频率受限",
		"":                          "官方未提供详细原因",
		"CustomError":               "CustomError",
		"engine_unavailable":        "提炼服务暂时无法连接",
		"engine_timeout":            "提炼服务响应超时",
		"context_deadline_exceeded": "提炼服务响应超时",
		"capture_paused":            "自动采集已暂停",
		"capture_filtered":          "因采集规则未采集",
	} {
		exp := describeFailureClass(class)
		if exp.Label != want || exp.Summary == "" || exp.Hint == "" {
			t.Fatalf("class=%q got=%+v wantLabel=%q", class, exp, want)
		}
	}
}

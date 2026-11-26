package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/daemon"
)

func TestCaptureFilterGatewayFlow(t *testing.T) {
	ctx := context.Background()
	s, store, fake := testServer(t, nil)
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	patchRequest := func(id, target, line string) string {
		return fmt.Sprintf(`{"project_id":"p","host":"traex","kind":"capture_trace","session_id":"session","message_id":%q,"content":{"origin_function":"apply_patch","method_params":{"command":%q}}}`,
			id, "*** Begin Patch\n*** Update File: "+target+"\n@@\n+"+line+"\n*** End Patch")
	}
	ok := agentRequest(t, s, "/ingest", patchRequest("ordinary", "foo.go", "obj.key = item.key"), 202)
	if ok["accepted"] != true || ok["status"] != "pending" {
		t.Fatal(ok)
	}
	const path, raw, secret = "certs/server.key", "body-stays-local", "sk-abcdefghijklmnopqrstuvwxyz01234"
	filtered := agentRequest(t, s, "/ingest", patchRequest("sensitive", path, raw), 200)
	if filtered["accepted"] != false || filtered["status"] != "filtered" || filtered["filtered"] != true ||
		filtered["filter_rule"] != "*.key" || filtered["filter_source"] != "apply_patch" || filtered["filter_category"] != "sensitive_path" {
		t.Fatal(filtered)
	}
	_, err := time.Parse(time.RFC3339Nano, fmt.Sprint(filtered["occurred_at"]))
	must(t, err)
	secretReply := agentRequest(t, s, "/ingest", fmt.Sprintf(`{"project_id":"p","session_id":%q,"content":{"question":"q","answer":%q}}`, path, secret), 200)
	if secretReply["filter_rule"] != "api_token" || secretReply["filter_category"] != "secret_value" {
		t.Fatal(secretReply)
	}
	for _, denied := range []string{path, raw, secret} {
		if strings.Contains(fmt.Sprint(filtered, secretReply), denied) {
			t.Fatal("filter receipt disclosed private content")
		}
	}
	if countRows(t, store, "capture_inbox") != 1 || countRows(t, store, "capture_receipts") != 1 {
		t.Fatal("filtered records reached inbox or receipts")
	}
	if processed, err := s.inbox.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("ordinary patch was not processed: %v, %v", processed, err)
	}
	if len(fake.ingest) != 1 {
		t.Fatal("ordinary patch never reached the engine")
	}
	ingested, err := json.Marshal(fake.ingest[0].Content)
	must(t, err)
	if !strings.Contains(string(ingested), "obj.key = item.key") {
		t.Fatal("ordinary patch did not reach the engine with body intact")
	}

	changed := decodeResponse[projectView](t, manage(t, s, "PUT", "/api/projects/p/auto-capture",
		`{"auto_capture":false,"actor":"system","reason":"maintenance"}`, 200))
	_, err = time.Parse(time.RFC3339Nano, changed.AutoCaptureUpdatedAt)
	must(t, err)
	if changed.AutoCapture || changed.AutoCaptureActor != "system" || changed.AutoCaptureReason != "maintenance" {
		t.Fatal(changed.projectSummary)
	}
	agentRequest(t, s, "/ingest", patchRequest("paused", path, raw), 403)
	manage(t, s, "PUT", "/api/projects/p/auto-capture", `{"auto_capture":true}`, 200)
	fake.ingestErr = errors.New("dial tcp: connection refused")
	agentRequest(t, s, "/ingest", patchRequest("failure", "foo.go", "item.key"), 202)
	if processed, err := s.inbox.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("failed capture was not processed: %v, %v", processed, err)
	}
	for _, route := range []string{"/api/projects/p", "/api/projects", "/api/overview/bootstrap", "/api/runtime/monitor",
		"/api/projects/p/filtered-captures", "/v1/projects/p/filtered-captures"} {
		response := manage(t, s, "GET", route, "", 200).Body.String()
		for _, denied := range []string{path, raw, secret} {
			if strings.Contains(response, denied) {
				t.Fatalf("%s disclosed private content", route)
			}
		}
		if !strings.Contains(response, `"filtered_count":2`) || !strings.Contains(response, "TRAE CLI 补丁路径命中") ||
			!strings.Contains(response, fmt.Sprint(filtered["occurred_at"])) {
			t.Fatalf("%s omitted filter evidence: %s", route, response)
		}
	}
	project := decodeResponse[projectView](t, manage(t, s, "GET", "/api/projects/p", "", 200))
	if len(project.Inbox.RecentFailures) != 1 || project.LastErrorAt == "" {
		t.Fatal(project.Inbox)
	}
	failure := project.Inbox.RecentFailures[0]
	if failure.LastErrorAt != project.LastErrorAt || failure.ErrorCode != "engine_unavailable" || failure.Summary == "" || failure.NextAction == "" {
		t.Fatal(failure)
	}
	request(t, s, "GET", "/api/projects/p/filtered-captures", "", map[string]string{"X-Api-Key": testAgent}, 403)
	manage(t, s, "POST", "/api/projects/p/retry", "", 200)
	project = decodeResponse[projectView](t, manage(t, s, "GET", "/api/projects/p", "", 200))
	if project.LastErrorAt != "" || project.Inbox.Failed != 0 || project.FilteredCount != 2 {
		t.Fatal(project)
	}
}

type monitorTestEngine struct {
	engine.MemoryEngine
	err error
}

func (e *monitorTestEngine) Health(context.Context) (engine.HealthStatus, error) {
	return engine.HealthStatus{OK: e.err == nil}, e.err
}

func TestRuntimeMonitorEventTimes(t *testing.T) {
	var e *monitorTestEngine
	var service daemon.ServiceStatus
	s, _, _ := testServer(t, func(cfg *Config) {
		e = &monitorTestEngine{MemoryEngine: cfg.Engine, err: context.DeadlineExceeded}
		cfg.Engine = e
		cfg.DaemonStatus = func(context.Context) map[string]any {
			return map[string]any{"services": []daemon.ServiceStatus{service}}
		}
	})
	probe := func() map[string]any {
		return decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/runtime/monitor?include_logs=1", "", 200))
	}
	first := probe()
	if first["state"] != "degraded" || first["last_ok_at_iso"] != "" || first["last_error_at"] != first["checked_at_iso"] ||
		!strings.Contains(fmt.Sprint(first["status_summary"]), "超时") || first["next_action"] == "" {
		t.Fatal(first)
	}
	e.err = nil
	healthy := probe()
	if healthy["state"] != "ready" || healthy["last_ok_at_iso"] != healthy["checked_at_iso"] || healthy["last_error_at"] != first["last_error_at"] {
		t.Fatal(healthy)
	}
	exit := time.Now().UTC()
	service = daemon.ServiceStatus{Role: "cognee-engine", ExitedAt: &exit, TailLines: []string{"error with Bearer test-private-token"}}
	got := probe()
	if got["state"] != "exited" || got["last_error_at"] != exit.Format(time.RFC3339Nano) || got["last_ok_at_iso"] != healthy["last_ok_at_iso"] ||
		!strings.Contains(fmt.Sprint(got["log_tail"]), "[REDACTED]") || strings.Contains(fmt.Sprint(got), "test-private-token") {
		t.Fatal(got)
	}
}

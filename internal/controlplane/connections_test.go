package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func resultOf[T any](result T, err error) (any, error) { return result, err }

func TestTypedClientMethods(t *testing.T) {
	ctx := context.Background()
	const (
		id        = "codex+home?#"
		path      = "/v1/connections/codex+home%3F%23/"
		op        = "ui-cli-example"
		opPath    = "/v1/connections/operations/" + op
		dryRun    = `{"dry_run":true}`
		revision  = 4
		body      = `{"expected_revision":4,"operation_id":"ui-cli-example"}`
		shaBody   = `{"expected_revision":4,"operation_id":"ui-cli-example","proposed_sha256":"reviewed"}`
		action    = `{"revision":4,"operation_id":"ui-cli-example","outcome":"ok","phase":"complete","connection":{"instance_id":"codex+home?#","needs_hook_repair":true,"repair_reason":"stale","repair_summary":"remove stale hook"}}`
		receipt   = `{"recorded":true,"operation_id":"ui-cli-example","safe_to_unlock":true,"result":` + action + `}`
		directory = `{"revision":4,"connections":[{"instance_id":"codex+home?#"}]}`
	)
	wantAction := ConnectionActionResult{
		Revision: revision, OperationID: op, Outcome: "ok", Phase: "complete",
		Connection: AgentConnection{ID: id, NeedsHookRepair: true, RepairReason: "stale", RepairSummary: "remove stale hook"},
	}
	wantReceipt := ConnectionOperationReceipt{Recorded: true, OperationID: op, SafeToUnlock: true, Result: &wantAction}
	wantDirectory := ConnectionsDirectory{Revision: revision, Connections: []AgentConnection{{ID: id}}}
	tests := []struct {
		name, method, target, body, response string
		call                                 func(*Client) (any, error)
		want                                 any
	}{
		{"capabilities", "GET", "/v1/capabilities", "", `{"api_versions":["v1"],"reading_contract":"catalog-cursor-v1","features":["demo"]}`,
			func(c *Client) (any, error) { return resultOf(c.Capabilities(ctx)) },
			ControlCapabilities{APIVersions: []string{"v1"}, ReadingContract: "catalog-cursor-v1", Features: []string{"demo"}}},
		{"connections", "GET", "/v1/connections", "", directory,
			func(c *Client) (any, error) { return resultOf(c.Connections(ctx, false)) }, wantDirectory},
		{"force connections", "GET", "/v1/connections?force=1", "", directory,
			func(c *Client) (any, error) { return resultOf(c.Connections(ctx, true)) }, wantDirectory},
		{"preview", "GET", path + "preview", "", `{"revision":4,"ok":true,"target":"/host/config","preview":{"baseline_sha256":"before","proposed_sha256":"new"},"diff_summary":"change","preserve_other_plugins":true,"requires_reload":false,"requires_host_trust":true}`,
			func(c *Client) (any, error) { return resultOf(c.PreviewConnection(ctx, id)) },
			ConnectionPreview{Revision: revision, OK: true, Target: "/host/config", Preview: &CodexConnectionPreview{BaselineSHA256: "before", ProposedSHA: "new"},
				DiffSummary: "change", PreserveOtherPlugins: new(true), RequiresReload: new(false), RequiresHostTrust: new(true)}},
		{"blocked preview", "GET", path + "preview", "", `{"revision":4,"ok":false,"blockers":["host_configuration_changed"],"blocker_details":[{"code":"host_configuration_changed","status":409,"phase":"pre_mutation"}]}`,
			func(c *Client) (any, error) { return resultOf(c.PreviewConnection(ctx, id)) },
			ConnectionPreview{Revision: revision, Blockers: []string{"host_configuration_changed"},
				BlockerDetails: []BlockerDetail{{Code: "host_configuration_changed", Status: 409, Phase: "pre_mutation"}}}},
		{"prepare", "POST", path + "prepare", body, action,
			func(c *Client) (any, error) { return resultOf(c.PrepareConnection(ctx, id, revision, op)) }, wantAction},
		{"activate", "POST", path + "activate", shaBody, action,
			func(c *Client) (any, error) { return resultOf(c.ActivateConnection(ctx, id, revision, op, "reviewed")) }, wantAction},
		{"review", "POST", path + "review", body, action,
			func(c *Client) (any, error) { return resultOf(c.ReviewConnection(ctx, id, revision, op)) }, wantAction},
		{"allow capture", "POST", path + "allow_capture", shaBody, action,
			func(c *Client) (any, error) { return resultOf(c.AllowCapture(ctx, id, revision, op, "reviewed")) }, wantAction},
		{"defer capture", "POST", path + "defer_capture", shaBody, action,
			func(c *Client) (any, error) { return resultOf(c.DeferCapture(ctx, id, revision, op, "reviewed")) }, wantAction},
		{"environment", "POST", "/v1/connections/environment", `{"kind":"codex","profile":"default","expected_revision":4}`, directory,
			func(c *Client) (any, error) { return resultOf(c.SelectEnvironment(ctx, "codex", "default", revision)) }, wantDirectory},
		{"repair", "POST", path + "repair-trae-hooks", `{"expected_revision":4,"operation_id":"ui-cli-example","reviewed_sha":"reviewed"}`,
			`{"revision":4,"operation_id":"ui-cli-example","repaired":true,"preserved_third_party_groups":2,"removed_events":["end"],"reviewed_sha":"reviewed"}`,
			func(c *Client) (any, error) { return resultOf(c.RepairTraeHooks(ctx, id, revision, op, "reviewed")) },
			TraeHooksRepairResult{Revision: revision, OperationID: op, Repaired: true, PreservedThirdPartyGroups: 2, RemovedEvents: []string{"end"}, ReviewedSHA: "reviewed"}},
		{"preview repair", "POST", "/v1/connections/trae%2Fhome/repair-trae-hooks", dryRun,
			`{"needs_hook_repair":true,"repairable":true,"mode":"roll_forward","repair_reason":"stale","repair_summary":"update hooks","baseline_sha256":"baseline","proposed_sha256":"proposed","reviewed_sha":"reviewed","removed_events":["end"],"retained_events":["start"],"updated_events":["stop"],"preserved_third_party_groups":2,"backup_plan":"backup hooks","roll_forward_artifact":true,"blockers":[{"code":"example","status":409,"phase":"pre_mutation"}]}`,
			func(c *Client) (any, error) { return resultOf(c.PreviewRepairTraeHooks(ctx, "trae/home")) },
			TraeHooksRepairResult{NeedsHookRepair: true, Repairable: true, Mode: "roll_forward", RepairReason: "stale",
				RepairSummary: "update hooks", BaselineSHA256: "baseline", ProposedSHA256: "proposed", ReviewedSHA: "reviewed",
				RemovedEvents: []string{"end"}, RetainedEvents: []string{"start"}, UpdatedEvents: []string{"stop"},
				PreservedThirdPartyGroups: 2, BackupPlan: "backup hooks", RollForwardArtifact: true,
				Blockers: []BlockerDetail{{Code: "example", Status: 409, Phase: "pre_mutation"}}}},
		{"operation", "GET", opPath, "", receipt,
			func(c *Client) (any, error) { return resultOf(c.ConnectionOperation(ctx, op)) }, wantReceipt},
		{"hook repair receipt", "GET", opPath, "",
			`{"recorded":true,"operation_id":"ui-cli-example","action":"repair-trae-hooks","status":"complete","result":{"repaired":true,"mode":"orphan_cleanup","removed_events":["SessionEnd"],"retained_events":["Stop"],"preserved_third_party_groups":2}}`,
			func(c *Client) (any, error) { return resultOf(c.ConnectionOperation(ctx, op)) },
			ConnectionOperationReceipt{Recorded: true, OperationID: op, Action: "repair-trae-hooks", Status: "complete",
				Result: &ConnectionActionResult{Repaired: true, Mode: "orphan_cleanup", RemovedEvents: []string{"SessionEnd"},
					RetainedEvents: []string{"Stop"}, PreservedThirdPartyGroups: 2}}},
		{"reconcile with revision", "POST", opPath + "/reconcile", `{"expected_revision":4}`, receipt,
			func(c *Client) (any, error) { return resultOf(c.ReconcilePendingOperation(ctx, op, revision)) }, wantReceipt},
		{"reconcile with zero revision", "POST", opPath + "/reconcile", `{"expected_revision":0}`, receipt,
			func(c *Client) (any, error) { return resultOf(c.ReconcilePendingOperation(ctx, op, 0)) }, wantReceipt},
		{"preview reconcile", "POST", "/v1/connections/operations/ui%2Fcli/reconcile", dryRun,
			`{"recorded":true,"operation_id":"ui/cli","revision":4,"instance_id":"codex+home?#","action":"activate","status":"unknown","phase":"pre_mutation","error_code":"interrupted","reconcilable":true,"safe_to_unlock":false,"result":null}`,
			func(c *Client) (any, error) { return resultOf(c.PreviewReconcilePendingOperation(ctx, "ui/cli")) },
			ConnectionOperationReceipt{Recorded: true, OperationID: "ui/cli", Revision: revision, InstanceID: id,
				Action: "activate", Status: "unknown", Phase: "pre_mutation", ErrorCode: "interrupted", Reconcilable: true}},
		{"preview reconcile already complete", "POST", opPath + "/reconcile", dryRun,
			`{"recorded":true,"operation_id":"ui-cli-example","revision":4,"action":"activate","status":"complete","outcome":"ok","phase":"complete","already_complete":true,"safe_to_unlock":true,"result":` + action + `}`,
			func(c *Client) (any, error) { return resultOf(c.PreviewReconcilePendingOperation(ctx, op)) },
			ConnectionOperationReceipt{Recorded: true, OperationID: op, Revision: revision, Action: "activate", Status: "complete",
				Outcome: "ok", Phase: "complete", AlreadyComplete: true, SafeToUnlock: true, Result: &wantAction}},
		{"preview reconcile unrecorded", "POST", opPath + "/reconcile", `{"dry_run":true,"instance_id":"codex+home?#","action":"activate"}`,
			`{"recorded":false,"status":"unrecorded","reconcilable":true,"safe_to_unlock":false}`,
			func(c *Client) (any, error) {
				return resultOf(c.PreviewReconcilePendingOperationFor(ctx, op, id, "activate"))
			}, ConnectionOperationReceipt{Status: "unrecorded", Reconcilable: true}},
		{"apply reconcile unrecorded", "POST", opPath + "/reconcile", `{"expected_revision":4,"instance_id":"codex+home?#","action":"activate"}`,
			`{"recorded":true,"revision":5,"operation_id":"ui-cli-example","instance_id":"codex+home?#","action":"activate","status":"complete","outcome":"rejected","phase":"pre_mutation","reconcilable":true,"reconciled":true,"safe_to_unlock":true}`,
			func(c *Client) (any, error) {
				return resultOf(c.ReconcilePendingOperationFor(ctx, op, revision, id, "activate"))
			}, ConnectionOperationReceipt{Recorded: true, Revision: 5, OperationID: op, InstanceID: id, Action: "activate",
				Status: "complete", Outcome: "rejected", Phase: "pre_mutation", Reconcilable: true, Reconciled: true, SafeToUnlock: true}},
		{"apply reconcile with context and zero revision", "POST", opPath + "/reconcile", `{"expected_revision":0,"instance_id":"codex+home?#","action":"activate"}`, receipt,
			func(c *Client) (any, error) {
				return resultOf(c.ReconcilePendingOperationFor(ctx, op, 0, id, "activate"))
			}, wantReceipt},
		{"runtime monitor", "GET", "/v1/runtime/monitor", "", `{"state":"running","processes":{"worker":{"pid":42}}}`,
			func(c *Client) (any, error) { return resultOf(c.RuntimeMonitor(ctx, false)) },
			ServiceMonitorSnapshot{State: "running", Processes: map[string]MonitoredProcess{"worker": {PID: 42}}}},
		{"runtime monitor with logs", "GET", "/v1/runtime/monitor?include_logs=1", "", `{"log_tail":["log"]}`,
			func(c *Client) (any, error) { return resultOf(c.RuntimeMonitor(ctx, true)) }, ServiceMonitorSnapshot{LogTail: []string{"log"}}},
		{"generation", "GET", "/v1/generation", "", `{"revision":4,"active":{"id":"model","revision":2},"profiles":{"model":[{"revision":2,"profile":{"temperature":0}}]}}`,
			func(c *Client) (any, error) { return resultOf(c.GenerationState(ctx)) },
			GenerationDirectory{Revision: revision, Active: &GenerationRef{ID: "model", Revision: 2},
				Profiles: map[string][]GenerationRow{"model": {{Revision: 2, Profile: GenerationProfile{Temperature: new(float64)}}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			origin, configureTransport := testHTTPServer(t, "127.0.0.1:12345", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.RequestURI != tc.target {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.RequestURI, tc.method, tc.target)
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if tc.body == "" {
					if len(raw) != 0 {
						t.Errorf("unexpected request body %s", raw)
					}
				} else {
					var got, want map[string]any
					err := json.Unmarshal(raw, &got)
					if err != nil || json.Unmarshal([]byte(tc.body), &want) != nil || !reflect.DeepEqual(got, want) {
						t.Errorf("request body %s, want %s", raw, tc.body)
					}
				}
				fmt.Fprint(w, tc.response)
			})
			client := testClient(t, origin)
			configureTransport(client)

			got, err := tc.call(client)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result=%#v err=%v, want %#v", got, err, tc.want)
			}
		})
	}
}

func TestReconcilePendingOperationRejectsNegativeRevision(t *testing.T) {
	origin, configureTransport := testHTTPServer(t, "127.0.0.1:12345", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected reconcile request: %s %s", r.Method, r.RequestURI)
		w.WriteHeader(http.StatusBadRequest)
	})
	client := testClient(t, origin)
	configureTransport(client)
	_, err := client.ReconcilePendingOperationFor(t.Context(), "reviewed-operation", -1, "", "activate")
	if err == nil || err.Error() != "expected_revision must be non-negative" {
		t.Fatalf("reconcile error = %v", err)
	}
}

func TestPreMutationControlError(t *testing.T) {
	origin, configureTransport := testHTTPServer(t, "127.0.0.1:12345", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"loud secret canary","code":"revision_conflict","phase":"pre_mutation"}`)
	})
	client := testClient(t, origin)
	configureTransport(client)
	_, err := client.ActivateConnection(context.Background(), "codex", 4, "ui-cli-example", "sha")
	var ce *ControlError
	if !errors.As(err, &ce) || !ce.IsConflict() || !ce.IsPreMutationRejected() ||
		!ce.IsCode(http.StatusConflict, "revision_conflict") || strings.Contains(ce.Error(), "canary") {
		t.Fatalf("unexpected conflict: %#v, %v", ce, err)
	}
	ce = decodeControlError(http.StatusConflict, strings.NewReader(`{"error":"revision_conflict"}`), "")
	if ce.IsPreMutationRejected() || !ce.IsCode(http.StatusConflict, "revision_conflict") {
		t.Fatalf("unexpected legacy conflict: %#v", ce)
	}
}

func TestConnectionPreviewAndCapabilitiesHelpers(t *testing.T) {
	var decoded ConnectionPreview
	if err := json.Unmarshal([]byte(`{"revision":4,"plan_digest":"reviewed-plan"}`), &decoded); err != nil || decoded.PlanDigest != "reviewed-plan" {
		t.Fatalf("decoded preview = %+v, %v", decoded, err)
	}
	preview := ConnectionPreview{
		ProposedSHA256: "top", ReviewedSHA: "reviewed",
		Preview:    &CodexConnectionPreview{ProposedSHA: "preview"},
		Connection: &AgentConnection{Preview: &CodexConnectionPreview{ProposedSHA: "connection"}},
	}
	for _, want := range []string{"top", "preview", "connection", "reviewed"} {
		if got := preview.EffectiveProposedSHA(); got != want {
			t.Fatalf("effective SHA = %q, want %q", got, want)
		}
		switch want {
		case "top":
			preview.ProposedSHA256 = ""
		case "preview":
			preview.Preview = nil
		case "connection":
			preview.Connection = nil
		}
	}
	caps := ControlCapabilities{ReadingContract: "catalog-cursor-v1", Features: []string{"demo"}}
	if !caps.HasFeature("catalog-cursor-v1") || !caps.HasFeature("demo") || caps.HasFeature("missing") {
		t.Fatal("unexpected capability lookup")
	}
}

package settings

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/controlplane"
)

func TestRecordedConnectionReceiptRequiresFullScope(t *testing.T) {
	t.Parallel()
	o := generationUIOperation{Operation: "ui-repair", Profile: "trae", Kind: "connection_repair_trae_hooks"}
	valid := controlplane.ConnectionOperationReceipt{
		Recorded: true, Status: "complete", Reconciled: true,
		OperationID: o.Operation, InstanceID: o.Profile, Action: " repair_trae_hooks ",
	}
	if !connectionReceiptMatches(o, valid) {
		t.Fatal("legacy repair action should match after normalization")
	}
	for _, test := range []struct {
		field string
		set   func(*controlplane.ConnectionOperationReceipt, string)
	}{
		{"operation_id", func(r *controlplane.ConnectionOperationReceipt, value string) { r.OperationID = value }},
		{"instance_id", func(r *controlplane.ConnectionOperationReceipt, value string) { r.InstanceID = value }},
		{"action", func(r *controlplane.ConnectionOperationReceipt, value string) { r.Action = value }},
	} {
		for _, value := range []string{"", "unrelated"} {
			t.Run(test.field+"/"+value, func(t *testing.T) {
				t.Parallel()
				receipt := valid
				test.set(&receipt, value)
				if connectionReceiptMatches(o, receipt) {
					t.Fatalf("accepted invalid recorded receipt: %+v", receipt)
				}
				receipt.Recorded = false
				if connectionReceiptMatches(o, receipt) != (value == "") {
					t.Fatalf("incorrectly matched unrecorded receipt: %+v", receipt)
				}
				receipt.Recorded = true
				root := t.TempDir()
				a := newTestHost(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
				if err := a.recordGenerationOperation(o); err != nil {
					t.Fatal(err)
				}
				a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method + " " + r.URL.Path {
					case "GET " + connectionOperationPath(o.Operation), "POST " + connectionOperationPath(o.Operation) + "/reconcile":
						json.NewEncoder(w).Encode(receipt)
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
				})}
				updates := make(chan func(), 8)
				a.dispatch = func(fn func()) { updates <- fn }
				assertPending := func() {
					t.Helper()
					drainReading(t, updates, func() bool { return !a.integrations.Busy })
					ops, err := a.loadGenerationOperations()
					if err != nil || len(ops) != 1 || ops[0].Operation != o.Operation {
						t.Fatalf(".ui-operations lost the original operation: %v, %v", ops, err)
					}
				}
				a.reviewConnectionOperation(o)
				assertPending()
				a.integrations.PendingReconcile[o.Operation] = controlplane.ConnectionOperationReceipt{Status: "unrecorded", Reconcilable: true}
				a.reconcilePendingConnectionOperation(o)
				assertPending()
				if !strings.Contains(a.integrations.Error, "解锁回执尚未确认") {
					t.Fatal("scope mismatch did not reject the unlock response")
				}
			})
		}
	}
}

func TestPendingConnectionReconcileRequiresExplicitSafeUnlock(t *testing.T) {
	// ui.NewTester shares MyGo's global scene.Atlas; keep UI raster tests sequential.
	for _, scenario := range []string{"safe", "explicit_zero_revision", "unrecorded", "unrecorded_apply", "unrecorded_nonterminal", "unrecorded_reconciled", "ambiguous", "post_mutation", "ambiguous_on_apply", "terminal_rejection"} {
		t.Run(scenario, func(t *testing.T) {
			var previews, applies atomic.Int32
			op := generationUIOperation{Operation: "ui-reconcile", Kind: "connection_prepare", Profile: "pi"}
			row := AgentConnection{ID: op.Profile, Kind: "pi", Detected: true, Status: "not_connected"}
			directory := ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{row}}
			var previewRevision *int64
			expectedRevision := directory.Revision
			if scenario == "safe" {
				previewRevision = new(int64(11))
				expectedRevision = *previewRevision
			} else if scenario == "explicit_zero_revision" {
				previewRevision = new(int64)
				expectedRevision = 0
			}
			unrecorded409 := strings.HasPrefix(scenario, "unrecorded_")
			safe := unrecorded409 || previewRevision != nil || scenario == "ambiguous_on_apply"
			var mutations atomic.Int32
			var originalOperation string
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				operation := op.Operation
				if unrecorded409 {
					operation = originalOperation
				}
				switch r.URL.Path {
				case "/v1/connections":
					json.NewEncoder(w).Encode(directory)
				case "/v1/projects":
					json.NewEncoder(w).Encode(AccessDirectory{})
				case "/v1/connections/pi/prepare":
					mutations.Add(1)
					var input struct {
						Operation string `json:"operation_id"`
					}
					json.NewDecoder(r.Body).Decode(&input)
					originalOperation = input.Operation
					w.WriteHeader(http.StatusConflict)
					w.Write([]byte(`{"error":"revision_conflict"}`))
				case "/v1/generation":
					json.NewEncoder(w).Encode(GenerationDirectory{})
				case connectionOperationPath(operation):
					receipt := controlplane.ConnectionOperationReceipt{Recorded: true, Status: "unknown", InstanceID: op.Profile, OperationID: operation, Action: "prepare"}
					if unrecorded409 || scenario == "unrecorded" {
						w.Write([]byte(`{"recorded":false}`))
						return
					}
					if scenario == "terminal_rejection" {
						receipt.Status, receipt.Outcome, receipt.ErrorCode, receipt.Phase = "complete", "rejected", "revision_conflict", "pre_mutation"
					}
					json.NewEncoder(w).Encode(receipt)
				case connectionOperationPath(operation) + "/reconcile":
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if r.Method != http.MethodPost {
						t.Error("reconcile endpoint requires POST")
					}
					if body["dry_run"] == true {
						previews.Add(1)
						if len(body) != 3 || body["instance_id"] != op.Profile || body["action"] != "prepare" {
							t.Error("preview contains mutation fields")
						}
						phase := "pre_mutation"
						if scenario == "post_mutation" {
							phase = "post_mutation"
						}
						if unrecorded409 || scenario == "unrecorded" {
							json.NewEncoder(w).Encode(controlplane.ConnectionOperationReceipt{Status: "unrecorded", Phase: phase, Reconcilable: safe})
							return
						}
						json.NewEncoder(w).Encode(struct {
							controlplane.ConnectionOperationReceipt
							Revision *int64 `json:"revision,omitempty"`
						}{
							ConnectionOperationReceipt: controlplane.ConnectionOperationReceipt{
								OperationID: operation, InstanceID: op.Profile, Action: "prepare",
								Recorded: true, Status: "unknown", Phase: phase, Reconcilable: safe || scenario == "post_mutation",
							},
							Revision: previewRevision,
						})
						return
					}
					applies.Add(1)
					if body["expected_revision"] != float64(expectedRevision) || len(body) != 3 || body["instance_id"] != op.Profile || body["action"] != "prepare" {
						t.Errorf("unexpected reconcile payload %v", body)
					}
					if scenario == "ambiguous_on_apply" {
						w.WriteHeader(http.StatusConflict)
						json.NewEncoder(w).Encode(map[string]string{"error": ambiguousConnectionOperation, "phase": "post_mutation"})
						return
					}
					receipt := controlplane.ConnectionOperationReceipt{OperationID: operation, InstanceID: op.Profile, Action: "prepare", Recorded: true, Reconciled: true}
					switch scenario {
					case "unrecorded_apply":
						receipt.Recorded = false
					case "unrecorded_nonterminal":
						receipt.Status, receipt.Reconciled = "unknown", false
					}
					json.NewEncoder(w).Encode(receipt)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			a := newTestHost(Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json"),
				ProjectControl: &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t)}}, nil)
			a.integrations.Connections = directory
			a.client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 8)
			a.dispatch = func(fn func()) { updates <- fn }
			if unrecorded409 {
				a.connectAgents("prepare", []string{op.Profile})
				drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.access.Loading })
				if mutations.Load() != 1 || len(a.integrations.PendingOperations) != 1 {
					t.Fatal("beta.2 conflict did not leave a pending prepare")
				}
			} else if err := a.recordGenerationOperation(op); err != nil {
				t.Fatal(err)
			}
			tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1000, 500)
			clickProjectUI(t, tt, "核对连接：pi")
			drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.integrations.Loading })
			tt.Frame()
			journalLength := func(want int) {
				t.Helper()
				ops, err := a.loadGenerationOperations()
				if err != nil || len(ops) != want {
					t.Fatalf("journal length want %d, got %v, %v", want, ops, err)
				}
			}
			if scenario == "terminal_rejection" {
				journalLength(0)
				if previews.Load() != 0 || applies.Load() != 0 || !strings.Contains(a.integrations.Message, "revision_conflict") {
					t.Fatal("terminal rejection was not treated as conclusive")
				}
				return
			}
			journalLength(1)
			if pending := a.integrations.pendingConnection(op.Profile); pending == nil ||
				a.integrations.PendingReconcileHasRevision[pending.Operation] != (previewRevision != nil) {
				t.Fatal("revision presence did not match the preview payload")
			}
			if previews.Load() != 1 || applies.Load() != 0 || tt.HasText("安全解锁") != safe {
				t.Fatal("preview did not gate unlock with explicit confirmation")
			}
			if !safe {
				if !tt.HasText("该待核对操作存在写入风险，需人工核对") {
					t.Fatal("ambiguity warning absent")
				}
				clickProjectUI(t, tt, "复制排障指令交给 Agent")
				if tt.Clipboard() != agentTakeoverConnectionTroubleshootPrompt("pi") {
					t.Fatal("reconcile escape action copied wrong prompt")
				}
				return
			}
			clickProjectUI(t, tt, "安全解锁")
			drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.integrations.Loading })
			if applies.Load() != 1 || unrecorded409 && mutations.Load() != 1 {
				t.Fatal("explicit unlock did not submit a single reconcile")
			}
			if scenario == "ambiguous_on_apply" {
				journalLength(1)
				if !strings.Contains(a.integrations.Error, ambiguousConnectionOperation) {
					t.Fatal("ambiguous apply did not surface the conflict")
				}
			} else if scenario == "unrecorded_apply" || scenario == "unrecorded_nonterminal" {
				journalLength(1)
				if !strings.Contains(a.integrations.Error, "请重新核对") {
					t.Fatal("unconfirmed apply did not request another check")
				}
			} else {
				journalLength(0)
				if _, present := a.integrations.PendingReconcile[op.Operation]; present {
					t.Fatal("unlock confirmation survived terminal receipt")
				}
				if _, present := a.integrations.PendingReconcileHasRevision[op.Operation]; present {
					t.Fatal("preview revision presence survived terminal receipt")
				}
			}
		})
	}
}

func TestConnectionActionKeeps409PendingWithoutVerifiedRejection(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, phase, receipt string
		wantReads, wantOps   int
	}{
		{"raw", "", "", 0, 1},
		{"post_mutation", "post_mutation", "", 0, 1},
		{"no_receipt", "pre_mutation", `{"recorded":false}`, 1, 1},
		{"unknown", "pre_mutation", `{"recorded":true,"status":"unknown"}`, 1, 1},
		{"wrong_outcome", "pre_mutation", `{"recorded":true,"status":"complete","outcome":"applied"}`, 1, 1},
		{"rejected", "pre_mutation", `{"recorded":true,"status":"complete","outcome":"rejected"}`, 1, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			var reads atomic.Int32
			row := AgentConnection{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"}
			cfg := Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json")}
			var operation string
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/connections":
					json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 1, Connections: []AgentConnection{row}})
				case "/v1/connections/pi/prepare":
					var payload struct {
						Operation string `json:"operation_id"`
					}
					json.NewDecoder(r.Body).Decode(&payload)
					operation = payload.Operation
					ops, err := loadGenerationOperationsFromDir(cfg.GenerationFile + ".ui-operations")
					if err != nil || len(ops) != 1 || ops[0].Operation != operation {
						t.Error("prepare preceded durable journal write")
					}
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]string{"error": "revision_conflict", "phase": scenario.phase})
				case "/v1/projects":
					json.NewEncoder(w).Encode(AccessDirectory{})
				default:
					reads.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != connectionOperationPath(operation) {
						t.Error("unexpected receipt route", r.Method, r.URL.Path)
					}
					var receipt controlplane.ConnectionOperationReceipt
					if err := json.Unmarshal([]byte(scenario.receipt), &receipt); err != nil {
						t.Error(err)
					}
					receipt.OperationID, receipt.InstanceID, receipt.Action = operation, row.ID, "prepare"
					json.NewEncoder(w).Encode(receipt)
				}
			})
			cfg.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t)}
			a := newTestHost(cfg, nil)
			a.client.HTTP = &http.Client{Transport: transport}
			updates := make(chan func(), 8)
			a.dispatch = func(fn func()) { updates <- fn }
			a.connectAgentsReviewed("prepare", []string{row.ID}, "")
			drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.access.Loading })
			ops, err := a.loadGenerationOperations()
			if err != nil || len(ops) != scenario.wantOps || int(reads.Load()) != scenario.wantReads ||
				!strings.Contains(a.integrations.ItemMessages[row.ID], "revision_conflict") {
				t.Fatalf("incorrect 409 recovery: reads=%d, ops=%v, err=%v, messages=%v", reads.Load(), ops, err, a.integrations.ItemMessages)
			}
		})
	}
}

func TestPendingTraeRepairReviewsHostBeforeClearingOriginalOperation(t *testing.T) {
	// ui.NewTester shares MyGo's global scene.Atlas; keep UI raster tests sequential.
	for _, scenario := range []struct {
		name                     string
		terminal, rowNeedsReview bool
	}{
		{name: "confirmed", terminal: true},
		{name: "still_unknown"},
		{name: "row_requires_review", rowNeedsReview: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			a := newTestHost(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
			op := generationUIOperation{Operation: "ui-repair-interrupted", Kind: "connection_repair_trae_hooks", Profile: "trae"}
			row := AgentConnection{ID: op.Profile, Kind: "trae_ide", Status: "not_connected"}
			if scenario.rowNeedsReview {
				row.Status = "requires_review"
			}
			a.integrations.Connections = ConnectionsDirectory{Revision: 4, Connections: []AgentConnection{row}}
			if err := a.recordGenerationOperation(op); err != nil {
				t.Fatal(err)
			}
			var reads, reviews atomic.Int32
			a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/connections":
					json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{row}})
				case "GET /v1/generation":
					json.NewEncoder(w).Encode(GenerationDirectory{})
				case "GET " + connectionOperationPath(op.Operation):
					read := reads.Add(1)
					switch {
					case read == 2 && reviews.Load() != 1:
						t.Error("original receipt was re-read before host review")
					case read == 2 && scenario.terminal:
						json.NewEncoder(w).Encode(controlplane.ConnectionOperationReceipt{
							Recorded: true, Status: "complete", OperationID: op.Operation, InstanceID: op.Profile, Action: "repair_trae_hooks",
						})
					case scenario.rowNeedsReview:
						w.Write([]byte(`{"recorded":false}`))
					default:
						json.NewEncoder(w).Encode(controlplane.ConnectionOperationReceipt{
							Recorded: true, Status: "unknown", Phase: "post_mutation",
							OperationID: op.Operation, InstanceID: op.Profile, Action: "repair-trae-hooks",
						})
					}
				case "POST " + connectionOperationPath(op.Operation) + "/reconcile":
					var payload map[string]any
					json.NewDecoder(r.Body).Decode(&payload)
					if payload["dry_run"] != true || payload["action"] != "repair-trae-hooks" {
						t.Errorf("unexpected repair reconcile payload %v", payload)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
				case "POST /v1/connections/trae/review":
					reviews.Add(1)
					var payload map[string]any
					json.NewDecoder(r.Body).Decode(&payload)
					if payload["expected_revision"] != float64(9) || payload["operation_id"] == "" || payload["operation_id"] == op.Operation {
						t.Errorf("host review must use a fresh operation and directory revision: %v", payload)
					}
					w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})}
			updates := make(chan func(), 8)
			a.dispatch = func(fn func()) { updates <- fn }
			tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1200, 820)
			clickProjectUI(t, tt, "核对连接："+connectionName(row.Kind))
			drainReading(t, updates, func() bool { return !a.integrations.Busy })
			tt.Frame()
			if tt.HasText("安全解锁") || !tt.HasText("核对连接状态") || !tt.HasText("复制排障指令交给 Agent") {
				t.Fatalf("missing host recovery actions: %v", tt.Texts())
			}
			clickProjectUI(t, tt, "核对连接状态")
			drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.integrations.Loading })
			ops, err := a.loadGenerationOperations()
			if err != nil || reviews.Load() != 1 || reads.Load() != 2 {
				t.Fatalf("review=%d reads=%d journal=%v err=%v", reviews.Load(), reads.Load(), ops, err)
			}
			if scenario.terminal {
				if len(ops) != 0 || !strings.Contains(a.integrations.Message, "确认原操作回执") {
					t.Fatal("confirmed original receipt was not cleared")
				}
			} else if len(ops) != 1 || ops[0] != op || a.integrations.Connections.Revision != 9 ||
				!strings.Contains(a.integrations.Message, "原操作回执仍待确认") {
				t.Fatal("unconfirmed receipt was lost or its review status was not refreshed")
			}
		})
	}
}

func TestTraeHookRepairRowOffersHostReviewWithOrWithoutBlockedPreview(t *testing.T) {
	// ui.NewTester shares MyGo's global scene.Atlas; keep UI raster tests sequential.
	for _, blockedPreview := range []bool{false, true} {
		root := t.TempDir()
		a := newTestHost(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
		row := AgentConnection{ID: "trae", Kind: "trae_ide", Status: "requires_review", NeedsHookRepair: true}
		directory := ConnectionsDirectory{Revision: 4, Connections: []AgentConnection{row}}
		a.integrations.Connections = directory
		if blockedPreview {
			a.integrations.TraeRepairPreview = map[string]controlplane.TraeHooksRepairResult{row.ID: {Repairable: false}}
		}
		var reviews atomic.Int32
		a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method + " " + r.URL.Path {
			case "GET /v1/connections":
				json.NewEncoder(w).Encode(directory)
			case "POST /v1/connections/trae/review":
				reviews.Add(1)
				json.NewEncoder(w).Encode(map[string]any{"revision": 5, "connection": row})
			case "GET /v1/projects":
				json.NewEncoder(w).Encode(AccessDirectory{})
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		})}
		updates := make(chan func(), 8)
		a.dispatch = func(fn func()) { updates <- fn }
		tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1200, 820)
		if !tt.HasText("核对连接状态") || !blockedPreview && !tt.HasText("一键修复 TRAE Hooks") {
			t.Fatalf("missing repair or review action: %v", tt.Texts())
		}
		clickProjectUI(t, tt, "核对连接状态")
		drainReading(t, updates, func() bool { return !a.integrations.Busy && !a.access.Loading })
		if reviews.Load() != 1 {
			t.Fatal("host review was not requested")
		}
	}
}

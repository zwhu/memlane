package settings

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGenerationAgentBridgeRoundTripKeepsKindProviderAndValidationIdentity(t *testing.T) {
	for _, kind := range []string{"codex", "pi", "traex"} {
		t.Run(kind, func(t *testing.T) {
			profile := GenerationProfile{Mode: "agent_bridge", BridgeKind: kind, Model: "selected-model"}
			if kind == "pi" {
				profile.BridgeProvider = "deepseek"
			}
			s := IntegrationState{}
			s.editProfile("selected", GenerationRow{Revision: 1, Profile: profile})
			if s.draftProfile() != profile || !s.cleanSaved() {
				t.Fatal("editing changed the selected Agent or invalidated its unchanged revision", s.draftProfile())
			}
			raw, err := json.Marshal(s.draftProfile())
			if err != nil {
				t.Fatal(err)
			}
			var decoded GenerationProfile
			if json.Unmarshal(raw, &decoded) != nil || decoded != profile {
				t.Fatal("bridge identity lost in management request")
			}
			s.BridgeKind = map[string]string{"codex": "traex", "pi": "codex", "traex": "pi"}[kind]
			if s.cleanSaved() {
				t.Fatal("switching Agent reused validation from a different bridge")
			}
		})
	}
}

func TestAgentBridgeEditorUsesReadOnlyNativeDefaultAndRefreshesSnapshot(t *testing.T) {
	for _, kind := range []string{"codex", "pi", "traex"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestHost(Config{}, nil)
			a.integrations = IntegrationState{Editor: true, Loaded: true, DraftMode: "agent_bridge", BridgeKind: kind, Model: "obsolete-saved-model", ProfileID: "new", Generation: GenerationDirectory{AgentBridges: map[string]GenerationProfile{kind: {Mode: "agent_bridge", BridgeKind: kind, Model: "native-current", BridgeProvider: "deepseek"}}}}
			tt := ui.NewTester(func(c *ui.Context) { a.generationEditorPanel(c) }, 1200, 820)
			if !tt.HasText("native-current") || !tt.HasText("Agent 当前模型") {
				t.Fatal("native default missing")
			}
			if _, ok := tt.Find("处理模型 ID"); ok {
				t.Fatal("Agent model can still be manually overridden")
			}
			if _, ok := tt.Find("pi已有供应商"); ok {
				t.Fatal("pi provider can still be overridden")
			}
			if a.integrations.draftProfile().Model != "native-current" {
				t.Fatal("save reused old model")
			}
			profile := a.integrations.Generation.AgentBridges[kind]
			profile.Model = "native-next"
			a.integrations.Generation.AgentBridges[kind] = profile
			tt.Frame()
			if !tt.HasText("native-next") || a.integrations.draftProfile().Model != "native-next" {
				t.Fatal("Agent default change was not reflected")
			}
		})
	}
}

func TestAgentValidationDoesNotClaimAnOldDefaultIsStillVerified(t *testing.T) {
	ref := &GenerationRef{ID: "agent", Revision: 1}
	actual := AgentModelDefaults{Model: "current", Provider: "trae", ReasoningEffort: "medium"}
	g := GenerationDirectory{Profiles: map[string][]GenerationRow{"agent": {{Revision: 1, Digest: "one", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "traex", Model: "historical"}}}}, AgentDefaults: map[string]AgentModelDefaults{"traex": actual}, Validations: map[string]GenerationValidation{"v": {Digest: "one", RegisteredRevision: 1, Status: "passed", Plain: true, Schema: true, Quality: true, AgentDefaults: &actual}}}
	if !g.validated(ref) {
		t.Fatal("matching native default validation lost")
	}
	next := actual
	next.Model = "next"
	g.AgentDefaults["traex"] = next
	if g.validated(ref) {
		t.Fatal("changed native model inherited old validation")
	}
	if g.Validations["v"].AgentDefaults.Model != "current" {
		t.Fatal("historical validation was rewritten")
	}
}

func TestSwitchAgentBridgeResultUnknownShowsConfirmingModel(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "token")
	if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	activeRef := GenerationRef{ID: "agent-codex", Revision: 1}
	targetRef := GenerationRef{ID: "agent-traex", Revision: 1}
	directory := GenerationDirectory{
		Revision:         3,
		HandoffAvailable: true,
		Active:           &activeRef,
		AgentBridges: map[string]GenerationProfile{
			"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-5.4"},
			"traex": {Mode: "agent_bridge", BridgeKind: "traex", Model: "gpt-6.2"},
		},
		Profiles: map[string][]GenerationRow{
			"agent-codex": {{Revision: 1, Digest: "d1", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-5.4"}}},
			"agent-traex": {{Revision: 1, Digest: "d2", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "traex", Model: "gpt-6.2"}}},
		},
	}

	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/handoffs":
			var body struct {
				OperationID string `json:"operation_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{
					Operation: body.OperationID,
					Reference: targetRef,
					Status:    "unknown",
					Reason:    "model_apply_unconfirmed",
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/generation/handoffs/"):
			op := strings.TrimPrefix(r.URL.Path, "/v1/generation/handoffs/")
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{
					Operation: op,
					Reference: targetRef,
					Status:    "unknown",
					Reason:    "model_apply_unconfirmed",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			out := directory
			out.ActiveHandoff = &GenerationHandoffReceipt{
				Operation: "op-active",
				Reference: targetRef,
				Status:    "unknown",
				Reason:    "model_apply_unconfirmed",
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	cfg := Config{
		GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: key},
	}
	a := newTestHost(cfg, nil)
	a.client.HTTP = server.client
	a.integrations = IntegrationState{
		Loaded:           true,
		GenerationLoaded: true,
		Generation:       directory,
		Editor:           true,
		DraftMode:        "agent_bridge",
		BridgeKind:       "traex",
		ProfileID:        "agent-traex",
		Model:            "gpt-6.2",
		Saved:            &targetRef,
	}
	updates := make(chan func(), 32)
	a.dispatch = func(f func()) { updates <- f }

	a.switchAgentBridge()
	drainReading(t, updates, func() bool { return !a.integrations.Busy })

	if strings.Contains(a.integrations.Error, "仍使用原模型") {
		t.Fatalf("result_unknown must not claim switch was not applied: error=%q", a.integrations.Error)
	}
	if a.integrations.HandoffOperation == "" || a.integrations.Handoff.Handoff == nil || a.integrations.Handoff.Handoff.Status != "unknown" {
		t.Fatalf("expected unknown handoff preserved for reconciliation: op=%q handoff=%+v", a.integrations.HandoffOperation, a.integrations.Handoff.Handoff)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Children(func() { a.generationWorkspaceFor(c, false, true) })
	}, 1200, 820)
	tt.Frame()
	if !tt.HasText("正在确认使用的模型") {
		t.Fatalf("expected Settings UI to show 正在确认使用的模型, texts=%v", tt.Texts())
	}
	if tt.HasText("切换未生效，仍使用原模型") {
		t.Fatalf("Settings UI falsely claimed old model is still active: texts=%v", tt.Texts())
	}
}

func TestActiveHandoffReceiptPrefersGatewayConfirmingOverStaleLocalTerminal(t *testing.T) {
	s := IntegrationState{
		Handoff: GenerationHandoffDirectory{
			Handoff: &GenerationHandoffReceipt{Operation: "op-old", Status: "applied"},
		},
		Generation: GenerationDirectory{
			ActiveHandoff: &GenerationHandoffReceipt{Operation: "op-new", Status: "unknown"},
		},
	}
	got := s.activeHandoffReceipt()
	if got == nil || got.Operation != "op-new" || got.Status != "unknown" {
		t.Fatalf("expected gateway confirming ActiveHandoff to override stale local receipt, got %+v", got)
	}
}

func TestSwitchAgentBridgeNeedsSaveRecordsOperationAndPollsUnknownToApplied(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "token")
	if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	activeRef := GenerationRef{ID: "agent-codex", Revision: 1}
	targetRef := GenerationRef{ID: "agent-traex", Revision: 1}
	directory := GenerationDirectory{
		Revision:         3,
		HandoffAvailable: true,
		Active:           &activeRef,
		AgentBridges: map[string]GenerationProfile{
			"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-5.4"},
			"traex": {Mode: "agent_bridge", BridgeKind: "traex", Model: "gpt-6.2"},
		},
		Profiles: map[string][]GenerationRow{
			"agent-codex": {{Revision: 1, Digest: "d1", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-5.4"}}},
		},
	}

	var opRecordedBeforePost bool
	var polledOp string
	cfg := Config{
		GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{ManagementTokenFile: key},
	}
	a := newTestHost(cfg, nil)
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/generation/profiles/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": targetRef})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/handoffs":
			if pending, err := a.loadGenerationOperations(); err == nil && len(pending) == 1 && pending[0].Kind == "handoffs" {
				opRecordedBeforePost = true
			}
			var body struct {
				OperationID string `json:"operation_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{
					Operation: body.OperationID,
					Reference: targetRef,
					Status:    "unknown",
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/generation/handoffs/"):
			polledOp = strings.TrimPrefix(r.URL.Path, "/v1/generation/handoffs/")
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{
					Operation: polledOp,
					Reference: targetRef,
					Status:    "applied",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			out := directory
			if polledOp != "" {
				out.Active = &targetRef
			} else {
				out.ActiveHandoff = &GenerationHandoffReceipt{
					Operation: "op-pending",
					Reference: targetRef,
					Status:    "unknown",
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	a.config.ProjectControl.URL = server.URL
	a.client.HTTP = server.client
	a.integrations = IntegrationState{
		Loaded:           true,
		GenerationLoaded: true,
		Generation:       directory,
		Editor:           true,
		DraftMode:        "agent_bridge",
		BridgeKind:       "traex",
		ProfileID:        "agent-traex",
		Model:            "gpt-6.2",
		Saved:            nil, // triggers needsSave == true
	}
	updates := make(chan func(), 32)
	a.dispatch = func(f func()) { updates <- f }

	a.switchAgentBridge()
	// Wait until poll finishes and clears HandoffOperation.
	drainReading(t, updates, func() bool { return !a.integrations.Busy && a.integrations.HandoffOperation == "" })

	if !opRecordedBeforePost {
		t.Fatal("needsSave path did not persist local handoff operation before POST /v1/generation/handoffs")
	}
	if polledOp == "" {
		t.Fatal("switchAgentBridge did not poll unknown handoff to terminal applied state")
	}
	if a.integrations.Generation.Active == nil || *a.integrations.Generation.Active != targetRef {
		t.Fatalf("expected active updated to targetRef after poll, got %+v", a.integrations.Generation.Active)
	}
}

func TestPollGenerationHandoffUnlocksWhenFreshDirectoryConfirmsActive(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "token")
	if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	targetRef := GenerationRef{ID: "agent-traex", Revision: 1}
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/generation/handoffs/"):
			op := strings.TrimPrefix(r.URL.Path, "/v1/generation/handoffs/")
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{Operation: op, Reference: targetRef, Status: "unknown"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			_ = json.NewEncoder(w).Encode(GenerationDirectory{
				Revision:         4,
				HandoffAvailable: true,
				Active:           &targetRef,
				ActiveHandoff:    nil,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	cfg := Config{
		GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: key},
	}
	a := newTestHost(cfg, nil)
	a.client.HTTP = server.client
	a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
	updates := make(chan func(), 32)
	a.dispatch = func(f func()) { updates <- f }

	a.pollGenerationHandoff("op-confirmed-by-dir")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })

	if a.integrations.HandoffOperation != "" || a.integrations.Handoff.Handoff != nil {
		t.Fatalf("expected settings unlocked when fresh directory confirms active without ActiveHandoff: op=%q handoff=%+v",
			a.integrations.HandoffOperation, a.integrations.Handoff.Handoff)
	}
}

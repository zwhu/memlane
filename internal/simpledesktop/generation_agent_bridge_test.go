package simpledesktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestGenerationAgentBridgeRoundTripKeepsKindProviderAndValidationIdentity(t *testing.T) {
	for _, kind := range []string{"codex", "pi", "traex"} {
		t.Run(kind, func(t *testing.T) {
			profile := GenerationProfile{Mode: "agent_bridge", BridgeKind: kind, Model: "selected-model"}
			if kind == "pi" {
				profile.BridgeProvider = "deepseek"
			}
			s := integrationState{}
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
			a := New(Config{}, nil)
			a.integrations = integrationState{Editor: true, Loaded: true, DraftMode: "agent_bridge", BridgeKind: kind, Model: "obsolete-saved-model", ProfileID: "new", Generation: GenerationDirectory{AgentBridges: map[string]GenerationProfile{kind: {Mode: "agent_bridge", BridgeKind: kind, Model: "native-current", BridgeProvider: "deepseek"}}}}
			tt := ui.NewTester(func(c *ui.Context) { a.generationEditorModal(c) }, 1200, 820)
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

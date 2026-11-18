package simpledesktop

import (
	"encoding/json"
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

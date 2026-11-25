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

func TestOllamaEditorHidesCloudCredentialAndKeepsSavedConfiguration(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "Agent 与模型"
	s := &a.integrations
	s.Loaded, s.GenerationLoaded = true, true
	row := GenerationRow{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "qwen3:4b", Endpoint: "http://127.0.0.1:11434"}}
	s.editProfile("local", row)
	s.GenerationProviderDialog = true
	s.APIKey = "previous-provider-draft"
	s.CredentialVersion = "previous-provider-version"
	p := s.draftProfile()
	if p.Provider != "ollama" || p.CredentialVersion != "" || !s.cleanSaved() {
		t.Fatal("local draft inherited cloud credential state")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range tt.Texts() {
		if label == "API Key" || label == "Provider API Key" {
			t.Fatal("local provider showed a cloud credential field")
		}
	}
	if !tt.HasText("Ollama") || !tt.HasText("填写Ollama 地址，例如 http://127.0.0.1:11434，无需 API Key；模型需已在服务中可用。") {
		t.Fatalf("local provider editor did not explain its address/auth contract: %q", tt.Texts())
	}
}

func TestOllamaSaveUsesExistingManagementAPIWithoutACloudKeyOrActiveSwitch(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 8)
	profile := GenerationProfile{Mode: "provider", Provider: "ollama", Model: "qwen3:4b", Endpoint: "http://127.0.0.1:11434"}
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 40) {
			t.Error("missing management authentication")
		}
		if r.Method == "PUT" && r.URL.Path == "/v1/generation/profiles/local" {
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["api_key"] != nil {
				t.Error("local save sent an invalid body or a cloud key")
			}
			var submitted GenerationProfile
			if json.Unmarshal(body["profile"], &submitted) != nil || submitted != profile {
				t.Error("local provider identity/address changed")
			}
			json.NewEncoder(w).Encode(map[string]any{"result": GenerationRef{ID: "local", Revision: 1}})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/generation" {
			json.NewEncoder(w).Encode(GenerationDirectory{Profiles: map[string][]GenerationRow{"local": {{Revision: 1, Profile: profile}}}})
			return
		}
		t.Error("save issued an unexpected request")
		http.Error(w, "unexpected request", 500)
	})

	a := newTestHost(Config{GenerationSetupOnly: true, GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.client.HTTP = server.client
	updates := make(chan func(), 8)
	a.dispatch = func(fn func()) { updates <- fn }
	a.integrations.editProfile("local", GenerationRow{Profile: profile})
	a.integrations.APIKey = "previous-provider-draft"
	a.saveGeneration()
	if !a.integrations.Busy {
		t.Fatalf("local save rejected before request: %s", a.integrations.Error)
	}
	generationUpdate(t, updates)
	if a.integrations.Error != "" || a.integrations.Busy || !a.integrations.cleanSaved() {
		t.Fatalf("save not reconciled: %s", a.integrations.Error)
	}
	if a.integrations.Generation.Active != nil || a.integrations.Generation.Next != nil {
		t.Fatal("save selected or activated the local model")
	}
	if len(requests) != 2 {
		t.Fatal("save did not use exactly PUT then configuration read")
	}
}

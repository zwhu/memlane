package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGenerationParameterRoundTripInvalidationAndExplicitZero(t *testing.T) {
	zero, limit := 0.0, 8192
	s := integrationState{}
	row := GenerationRow{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "synthetic", Endpoint: "http://127.0.0.1:11434", Temperature: &zero, MaxOutputTokens: &limit}}
	s.editProfile("local", row)
	if s.Temperature != "0" || s.MaxOutputTokens != "8192" || !s.cleanSaved() {
		t.Fatal("saved parameters did not reopen cleanly")
	}
	raw, err := json.Marshal(s.draftProfile())
	if err != nil || !strings.Contains(string(raw), `"temperature":0`) || !strings.Contains(string(raw), `"max_output_tokens":8192`) {
		t.Fatalf("explicit zero or token limit lost: %s, %v", raw, err)
	}
	for _, value := range []string{"0.6", "NaN", "Inf", "-1", "3", "bad"} {
		s.Temperature = value
		if s.cleanSaved() {
			t.Fatalf("edited or invalid parameter retained validation: %q", value)
		}
	}
	s.Temperature = "0"
	for _, value := range []string{"4096", "", "0", "-1", "8192.0", "131073", "bad"} {
		s.MaxOutputTokens = value
		if s.cleanSaved() {
			t.Fatalf("edited or invalid limit retained validation: %q", value)
		}
	}
	s.editProfile("legacy", GenerationRow{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "old", Endpoint: "http://127.0.0.1:11434"}})
	if s.Temperature != "" || s.MaxOutputTokens != "" || s.draftProfile().Temperature != nil || s.draftProfile().MaxOutputTokens != nil || !s.cleanSaved() {
		t.Fatal("legacy profile inherited another profile's parameters")
	}
}

func TestGenerationParameterDraftLabelsSavedValidation(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "知识处理"
	row := GenerationRow{Revision: 1, Digest: "original", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "synthetic", Endpoint: "http://127.0.0.1:11434"}}
	a.integrations = integrationState{Loaded: true, Editor: true, Generation: GenerationDirectory{Profiles: map[string][]GenerationRow{"local": {row}}, Validations: map[string]GenerationValidation{"v": {Status: "passed", Digest: "original", Plain: true, Schema: true, Quality: true}}}}
	a.integrations.editProfile("local", row)
	a.integrations.MaxOutputTokens = "8192"
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("已保存配置的原验证：验证通过") {
		t.Fatal("changed parameters displayed an unqualified validation success")
	}
}

func TestGenerationInvalidParametersRejectBeforeJournalOrRequest(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.integrations = integrationState{Loaded: true, Editor: true, ProfileID: "local", DraftMode: "provider", ProviderLabel: "Ollama", Model: "synthetic", Endpoint: "http://127.0.0.1:11434"}
	for _, values := range [][2]string{{"NaN", "8192"}, {"0.6", "0"}, {"0.6", "8192.0"}} {
		a.integrations.Temperature, a.integrations.MaxOutputTokens = values[0], values[1]
		a.saveGeneration()
		if a.integrations.Error == "" || a.integrations.Busy || a.integrations.SaveOperation != "" || len(a.integrations.PendingOperations) != 0 || requests.Load() != 0 {
			t.Fatal("invalid parameter created a pending write or reached the server")
		}
	}
}

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

func TestGenerationAddAndEditStayInlineAndDiscardRemovesSecret(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations.Loaded = true
	a.integrations.Generation.Profiles = map[string][]GenerationRow{"provider": {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "openai", Model: "saved-model"}}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(16).Children(func() { a.generationSettings(c) })
	}, 1200, 820)
	if !tt.HasText("供应商协议") {
		t.Fatal("saved Provider fields are not inline")
	}
	clickProjectUI(t, tt, "添加配置")
	if !a.integrations.Editor || !tt.HasText("Provider 配置") || !tt.HasText("供应商协议") {
		t.Fatal("new Provider fields missing inline")
	}
	// A new form must not turn omitted options into client-selected values.
	raw, err := json.Marshal(a.integrations.draftProfile())
	if err != nil || strings.Contains(string(raw), `"temperature"`) || strings.Contains(string(raw), `"max_output_tokens"`) {
		t.Fatalf("new Provider form synthesized generation parameters: %s, %v", raw, err)
	}
	fixtureImage(t, tt, "generation-add-modal")
	a.integrations.APIKey = "private-unsaved-key"
	clickProjectUI(t, tt, "取消修改")
	if a.integrations.APIKey != "" || !tt.HasText("供应商协议") {
		t.Fatal("dismissed editor kept a live credential field")
	}
	clickProjectUI(t, tt, "配置模型：provider")
	if !tt.HasText("Provider 配置") || a.integrations.Model != "saved-model" {
		t.Fatal("existing profile did not open in the shared editor")
	}
}

func TestProviderDeletionConfirmsExactProfileAndReconcilesUnknownWithoutResending(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "unknown"}[unknown], func(t *testing.T) {
			root := t.TempDir()
			key := filepath.Join(root, "token")
			if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			var posts atomic.Int32
			profile := GenerationRow{Revision: 2, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "reviewed-model"}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
					var payload map[string]any
					json.NewDecoder(r.Body).Decode(&payload)
					if r.URL.Path != "/v1/generation/profiles/provider/delete" || payload["profile_revision"] != float64(2) || payload["expected_revision"] != float64(5) {
						t.Error("delete did not preserve the reviewed profile and directory version")
					}
					if unknown {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": "provider", "deleted": true}})
				} else if strings.Contains(r.URL.Path, "/operations/") {
					json.NewEncoder(w).Encode(map[string]any{"recorded": true, "result": map[string]any{"id": "provider", "deleted": true}})
				} else {
					// Failed writes may still have committed; an unchanged list is not a receipt.
					json.NewEncoder(w).Encode(GenerationDirectory{Revision: 5, Profiles: map[string][]GenerationRow{"provider": {profile}}})
				}
			}))
			defer server.Close()
			a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: key}}, nil)
			a.integrations = integrationState{Loaded: true, Generation: GenerationDirectory{Revision: 5, Profiles: map[string][]GenerationRow{"provider": {profile}}}}
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(24).Gap(16).Children(func() { a.generationSettings(c) })
			}, 1200, 820)
			clickProjectUI(t, tt, "删除模型：provider")
			if posts.Load() != 0 || !tt.HasText("删除模型配置？") {
				t.Fatal("delete skipped explicit confirmation")
			}
			if !unknown {
				fixtureImage(t, tt, "generation-delete-modal")
			}
			clickProjectUI(t, tt, "确认删除模型：provider")
			drainReading(t, updates, func() bool { return !a.integrations.Busy })
			if unknown {
				if !a.integrations.pendingGenerationProfile("provider") || len(a.integrations.Generation.Profiles["provider"]) == 0 {
					t.Fatal("unknown deletion was silently hidden or lost its operation")
				}
				a.reviewPendingGeneration(a.integrations.PendingOperations[0])
				drainReading(t, updates, func() bool { return !a.integrations.Busy })
			}
			if posts.Load() != 1 || len(a.integrations.Generation.Profiles["provider"]) != 0 || a.integrations.pendingGenerationProfile("provider") {
				t.Fatal("deletion was not acknowledged or recovery repeated the write")
			}
		})
	}
}

func TestProviderDeletionProtectsEveryActiveRevisionAndPendingWork(t *testing.T) {
	s := integrationState{Generation: GenerationDirectory{Active: &GenerationRef{ID: "provider", Revision: 1}}}
	if s.generationDeleteBlock("provider") == "" {
		t.Fatal("active profile can be deleted via a later revision")
	}
	s.Generation.Active, s.Generation.Next = nil, &GenerationRef{ID: "provider", Revision: 1}
	if s.generationDeleteBlock("provider") == "" {
		t.Fatal("next startup profile can be deleted")
	}
	s.Generation.Next = nil
	s.Generation.Validations = map[string]GenerationValidation{"unknown": {Reference: &GenerationRef{ID: "provider", Revision: 1}, Status: "result_unknown"}}
	if s.generationDeleteBlock("provider") == "" {
		t.Fatal("unknown validation can lose its configuration")
	}
}

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

func TestGenerationHandoffConfirmsThenRecoversOriginalOperationWithoutResending(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "token")
	if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	ref := GenerationRef{ID: "fast", Revision: 1}
	directory := GenerationDirectory{Revision: 5, HandoffAvailable: true, Active: &GenerationRef{ID: "old", Revision: 1},
		Profiles:    map[string][]GenerationRow{"fast": {{Revision: 1, Digest: "d", Profile: GenerationProfile{Mode: "provider", Provider: "openai", Model: "fast-model"}}}},
		Validations: map[string]GenerationValidation{"v": {Digest: "d", Status: "passed", Plain: true, Schema: true, Quality: true}}}
	var posts atomic.Int32
	var confirmed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			var body struct {
				Profile  GenerationRef `json:"profile"`
				Expected int64         `json:"expected_revision"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Profile != ref || body.Expected != 5 {
				t.Error("handoff changed reviewed profile/revision")
			}
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/v1/generation" {
			value := directory
			if confirmed.Load() {
				value.Active = &ref
			}
			json.NewEncoder(w).Encode(value)
			return
		}
		value := GenerationHandoffDirectory{Pending: 12, Unknown: 1}
		if strings.Contains(r.URL.Path, "/handoffs/") {
			status := "unknown"
			if confirmed.Load() {
				status = "applied"
			}
			value.Handoff = &GenerationHandoffReceipt{Operation: strings.TrimPrefix(r.URL.Path, "/v1/generation/handoffs/"), Reference: ref, Status: status}
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	cfg := Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: key}}
	a := New(cfg, nil)
	a.integrations.Loaded = true
	a.integrations.Generation = directory
	updates := make(chan func(), 32)
	a.Update = func(f func()) { updates <- f }
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Padding(24).Children(func() { a.generationSettings(c) }) }, 1200, 820)
	clickProjectUI(t, tt, "更换配置")
	drainReading(t, updates, func() bool { return !a.integrations.HandoffLoading })
	tt.Frame()
	if !tt.HasText("待整理 12 个会话") || !tt.HasText("另有 1 个会话等待核对结果") {
		t.Fatal("missing actual backlog preview")
	}
	fixtureImage(t, tt, "generation-handoff-modal")
	clickProjectUI(t, tt, "取消")
	if posts.Load() != 0 || a.integrations.HandoffOpen {
		t.Fatal("cancelling preview mutated model")
	}
	clickProjectUI(t, tt, "更换配置")
	drainReading(t, updates, func() bool { return !a.integrations.HandoffLoading })
	tt.Frame()
	clickProjectUI(t, tt, "Provider · OpenAI · fast-model")
	clickProjectUI(t, tt, "切换并接续")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	if posts.Load() != 1 || a.integrations.HandoffOperation == "" {
		t.Fatal("lost reply discarded original handoff")
	}
	pending, err := a.loadGenerationOperations()
	if err != nil || len(pending) != 1 || pending[0].Kind != "handoffs" {
		t.Fatal("missing durable original operation", err)
	}
	confirmed.Store(true)
	reopened := New(cfg, nil)
	reopened.integrations.Generation = directory
	reopened.Update = a.Update
	reopened.reviewPendingGeneration(pending[0])
	drainReading(t, updates, func() bool { return !reopened.integrations.Busy })
	if posts.Load() != 1 || reopened.integrations.HandoffOperation != "" || reopened.integrations.Generation.Active == nil || *reopened.integrations.Generation.Active != ref {
		t.Fatal("reopen did not reconcile without another POST")
	}
}

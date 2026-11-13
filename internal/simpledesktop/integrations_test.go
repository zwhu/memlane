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
	"time"

	"github.com/egoist/mygo/ui"
)

func TestGenerationEditingSeparatesActiveRevisionAndInvalidatesDraftValidation(t *testing.T) {
	s := integrationState{Generation: GenerationDirectory{Active: &GenerationRef{ID: "old", Revision: 1}, Profiles: map[string][]GenerationRow{
		"api": {{Revision: 2, Digest: "verified", Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "test-model", Endpoint: "https://example.test/v1", CredentialConfigured: true, CredentialVersion: "version"}}}},
		Validations: map[string]GenerationValidation{"v": {Status: "passed", Digest: "verified", Plain: true, Schema: true, Quality: true}}}}
	s.editProfile("api", s.Generation.Profiles["api"][0])
	if !s.cleanSaved() || !s.Generation.validated(s.Saved) {
		t.Fatal("saved validated candidate missing")
	}
	s.Model = "edited-model"
	if s.cleanSaved() {
		t.Fatal("edited draft used old validation")
	}
	if s.Generation.Active.ID != "old" {
		t.Fatal("editing switched active processing")
	}
	s.Model = "test-model"
	s.APIKey = "replacement"
	if s.cleanSaved() {
		t.Fatal("new key reused old validation")
	}
}

func TestGenerationNewestUnknownResultWithdrawsOldSuccessAndShowsSafeReason(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "知识处理"
	ref := &GenerationRef{ID: "api", Revision: 1}
	row := GenerationRow{Revision: 1, Digest: "same", Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "synthetic", Endpoint: "https://example.test/v1"}}
	g := GenerationDirectory{Profiles: map[string][]GenerationRow{"api": {row}}, Validations: map[string]GenerationValidation{
		"z-old": {Status: "passed", Digest: "same", Plain: true, Schema: true, Quality: true, RegisteredRevision: 2},
		"a-new": {Status: "result_unknown", Digest: "same", ErrorCode: "validation_timeout", RegisteredRevision: 3, StructuredMode: "json_object", ModeEvidence: "official_adapter_request"},
	}}
	if g.validated(ref) {
		t.Fatal("old success concealed a newer unknown result")
	}
	a.integrations = integrationState{Loaded: true, Editor: true, ProfileID: "api", DraftMode: "provider", ProviderLabel: "OpenAI 兼容", Model: "synthetic", Endpoint: "https://example.test/v1", Saved: ref, Generation: g, ValidationOperation: "a-new", Validation: g.Validations["a-new"]}
	a.integrations.ValidationOperation = ""
	a.integrations.Validation = GenerationValidation{}
	a.integrations.editProfile("api", row)
	if a.integrations.ValidationOperation != "a-new" || a.integrations.Validation.ErrorCode != "validation_timeout" {
		t.Fatal("opening saved profile lost its original validation receipt")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("验证等待超时，结果未确认；请核对原验证。") {
		t.Fatal("timeout reason missing from rendered settings")
	}
	if !tt.HasText("本次结构化请求：JSON 对象") {
		t.Fatal("observed request mode missing from rendered settings")
	}
	a.integrations.Validation.ErrorCode = "synthetic-secret-never-show"
	tt = ui.NewTester(a.View, 1200, 820)
	for _, text := range tt.Texts() {
		if strings.Contains(text, "synthetic-secret-never-show") {
			t.Fatal("unknown error body leaked to settings")
		}
	}
	for key, v := range g.Validations {
		v.RegisteredRevision = 0
		g.Validations[key] = v
	}
	if g.validated(ref) {
		t.Fatal("unordered legacy conflict was treated as validated")
	}
}

func TestGenerationUnknownSaveClearsInputAndDoesNotRepeatMutation(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			mutations.Add(1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["api_key"] != "synthetic-secret" {
				t.Error("key missing from authenticated request")
			}
			w.WriteHeader(503)
			w.Write([]byte(`{"error":"unconfirmed"}`))
			return
		}
		w.Write([]byte(`{"recorded":false}`))
	}))
	defer server.Close()
	a := New(Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.integrations = integrationState{Loaded: true, Editor: true, ProfileID: "api", DraftMode: "provider", ProviderLabel: "OpenAI", Model: "test", Endpoint: "https://example.test/v1", APIKey: "synthetic-secret"}
	a.saveGeneration()
	if a.integrations.KeyEpoch == 0 {
		t.Fatal("submitted password widget retained its old identity")
	}
	if a.integrations.APIKey != "" {
		t.Fatal("submitted key retained in editor")
	}
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("save did not return")
	}
	if a.integrations.SaveOperation == "" {
		t.Fatal("unknown save lost original operation")
	}
	a.saveGeneration()
	if mutations.Load() != 1 {
		t.Fatal("unknown write repeated")
	}
	a.reviewGenerationSave()
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("review did not return")
	}
	if mutations.Load() != 1 || a.integrations.SaveOperation == "" {
		t.Fatal("absent receipt was treated as safe to resubmit")
	}
}

func TestIntegrationSettingsUseSeparatePagesAndMaskProviderKey(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "知识处理"
	a.integrations = integrationState{Loaded: true, Editor: true, ProfileID: "api", DraftMode: "provider", ProviderLabel: "OpenAI", Model: "synthetic", Endpoint: "https://example.test/v1", APIKey: "synthetic-secret-never-show",
		Generation: GenerationDirectory{Active: &GenerationRef{ID: "existing-codex", Revision: 1}, Profiles: map[string][]GenerationRow{"existing-codex": {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", Model: "old-model"}}}}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"当前知识处理", "供应商协议", "API 地址", "API Key", "保存配置", "下次启动使用"} {
		if !tt.HasText(label) {
			t.Fatal("missing", label)
		}
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "synthetic-secret-never-show") {
			t.Fatal("key appeared in rendered text")
		}
	}
	if tt.HasText("准备所选 Agent") {
		t.Fatal("Agent and Provider actions mixed")
	}
	fixtureImage(t, tt, "multi-agent-provider-1200x820-editor")
}

func TestIntegrationAgentListKeepsPartialSuccessAndShowsHostLimits(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "服务与连接"
	a.integrations = integrationState{Loaded: true, Selected: map[string]bool{"pi": true}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "codex", Kind: "codex", Status: "configured_pending_new_session", NewSessions: 1},
		{ID: "pi", Kind: "pi", Detected: true, Status: "prepared", Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}},
		{ID: "traex", Kind: "traex", Detected: true, Status: "activation_unknown"},
		{ID: "claude", Kind: "claude_code", Detected: true},
		{ID: "cursor", Kind: "cursor"},
	}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"Codex", "pi", "TraeX", "Claude Code", "Cursor", "启用已准备项", "准备所选 Agent"} {
		if !tt.HasText(label) {
			t.Fatal("missing Agent control", label)
		}
	}
	if !a.integrations.Selected["pi"] {
		t.Fatal("prepared selection lost")
	}
	fixtureImage(t, tt, "multi-agent-1200x820-connections")
}

func TestIntegrationRecoveryActionsRemainVisibleWithoutAnOpenEditor(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "知识处理"
	a.integrations = integrationState{Loaded: true, PendingOperations: []generationUIOperation{
		{Operation: "ui-unconfirmed", Kind: "save", Profile: "saved-before-exit"},
	}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"待核对操作", "保存配置 · saved-before-exit", "核对原操作"} {
		if !tt.HasText(label) {
			t.Fatal("reopened recovery control missing", label)
		}
	}
	if a.integrations.Editor {
		t.Fatal("recovery depended on an open editor")
	}
}

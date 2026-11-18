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

func TestFirstGenerationSetupReadsOnlyConfigurationAndReusesProviderEditor(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		if r.URL.Path != "/v1/generation" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 40) {
			http.Error(w, "unexpected first-setup request", 500)
			return
		}
		json.NewEncoder(w).Encode(GenerationDirectory{Profiles: map[string][]GenerationRow{}, Validations: map[string]GenerationValidation{}})
	}))
	defer server.Close()
	a := New(Config{GenerationSetupOnly: true, ServiceURL: server.URL,
		GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.Start()
	a.SetWindowActive(true)
	generationUpdate(t, updates)
	if got := <-requests; got != "GET /v1/generation" {
		t.Fatalf("first setup sent %q", got)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("配置记忆处理") || !tt.HasText("添加配置") || !a.integrations.Loaded {
		t.Fatal("first setup did not show the existing provider editor")
	}
	if len(requests) != 0 || a.integrations.Generation.Active != nil || a.integrations.Generation.Next != nil {
		t.Fatal("first setup read content or fabricated an active configuration")
	}
}

func TestGenerationTerminalValidationClearsObsoleteRunningMessage(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GenerationValidation{Status: "passed", Plain: true, Schema: true, Quality: true})
	}))
	defer server.Close()
	a := New(Config{GenerationSetupOnly: true, GenerationFile: filepath.Join(root, "generation.json"),
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	operation := "ui-first-validation"
	if err := a.recordGenerationOperation(generationUIOperation{Operation: operation, Kind: "validations", Profile: "api", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	a.integrations.ValidationOperation = operation
	a.integrations.Message = "正在验证，会使用所选 Provider 的额度"
	a.pollGenerationValidation(operation)
	generationUpdate(t, updates)
	if a.integrations.Validation.Status != "passed" || a.integrations.Message != "" {
		t.Fatal("completed validation still claims a model call is running")
	}
	if a.integrations.ValidationCancel != nil {
		a.integrations.ValidationCancel()
	}
}

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
	for _, label := range []string{"当前模型", "供应商协议", "API 地址", "API Key", "保存配置", "下次启动使用"} {
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
	a.integrations = integrationState{Loaded: true, ConnectionBulk: true, Selected: map[string]bool{"pi": true}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "codex", Kind: "codex", Status: "configured_pending_new_session", NewSessions: 1},
		{ID: "pi", Kind: "pi", Detected: true, Status: "prepared", Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}},
		{ID: "traex", Kind: "traex", Detected: true, Status: "activation_unknown"},
		{ID: "claude", Kind: "claude_code", Detected: true},
		{ID: "cursor", Kind: "cursor"},
	}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"Codex", "pi", "TraeX", "Claude Code", "其他 Agent（1）", "确认连接（1）", "等待确认", "等待保存验证"} {
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

func TestConnectionUnknownMutationSurvivesReopenAndDoesNotResubmit(t *testing.T) {
	for _, action := range []string{"prepare", "activate"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			token := filepath.Join(root, "manager.token")
			if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			var mutations atomic.Int32
			var receiptReady atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					mutations.Add(1)
					pending, err := New(Config{ProjectControl: &ProjectControlConfig{ManagementTokenFile: token}}, nil).loadGenerationOperations()
					var payload map[string]any
					json.NewDecoder(r.Body).Decode(&payload)
					if err != nil || len(pending) != 1 || pending[0].Kind != "connection_"+action || pending[0].Operation != payload["operation_id"] {
						t.Error("installation preceded durable original operation", pending, err)
					}
					w.WriteHeader(503)
					w.Write([]byte(`{"error":"unconfirmed"}`))
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/connections/operations/") {
					if receiptReady.Load() {
						w.Write([]byte(`{"recorded":true,"status":"complete","result":{"connection":{"instance_id":"pi-test","status":"prepared"}}}`))
					} else {
						w.Write([]byte(`{"recorded":false}`))
					}
					return
				}
				status := "not_connected"
				if action == "activate" {
					status = "prepared"
				}
				json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 1, Connections: []AgentConnection{{ID: "pi-test", Kind: "pi", Detected: true, Status: status, Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}}}})
			}))
			defer server.Close()
			config := Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}
			a := New(config, nil)
			updates := make(chan func(), 16)
			a.Update = func(fn func()) { updates <- fn }
			a.connectAgents(action, []string{"pi-test"})
			deadline := time.After(5 * time.Second)
			for a.integrations.Busy {
				select {
				case fn := <-updates:
					fn()
				case <-deadline:
					t.Fatal("connection did not finish")
				}
			}
			reopened := New(config, nil)
			pending, err := reopened.loadGenerationOperations()
			if err != nil || len(pending) != 1 {
				t.Fatalf("unknown connection operation lost on reopen: %v %v", pending, err)
			}
			reopened.integrations.PendingOperations = pending
			reopened.Update = a.Update
			reopened.connectAgents(action, []string{"pi-test"})
			if reopened.integrations.Busy || mutations.Load() != 1 {
				t.Fatal("unknown connection mutation resubmitted")
			}
			for _, ready := range []bool{false, true} {
				receiptReady.Store(ready)
				reopened.reviewConnectionOperation(pending[0])
				deadline = time.After(5 * time.Second)
				for reopened.integrations.Busy {
					select {
					case fn := <-updates:
						fn()
					case <-deadline:
						t.Fatal("receipt review did not finish")
					}
				}
				remaining, err := reopened.loadGenerationOperations()
				if err != nil || (len(remaining) == 0) != ready {
					t.Fatalf("incorrect review outcome: %v %v", remaining, err)
				}
				if mutations.Load() != 1 {
					t.Fatal("receipt review repeated installation")
				}
			}
		})
	}
}

func TestConnectionBatchCancellationPreservesFirstSuccessAndSkipsRemaining(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}()
	var mutations atomic.Int32
	var prepared atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if mutations.Add(1) != 1 {
				t.Error("second batch item started after cancellation")
			}
			close(started)
			<-release
			prepared.Store(true)
			w.Write([]byte(`{"revision":2,"connection":{"instance_id":"pi-first","kind":"pi","detected":true,"status":"prepared","preview":{"proposed_sha256":"reviewed"}}}`))
			return
		}
		status := "not_connected"
		if prepared.Load() {
			status = "prepared"
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 2, Connections: []AgentConnection{
			{ID: "pi-first", Kind: "pi", Detected: true, Status: status, Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}},
			{ID: "traex-second", Kind: "traex", Detected: true, Status: "not_connected"},
		}})
	}))
	// Release the synthetic installer before waiting for server shutdown, including failures.
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
		server.Close()
	}()
	a := New(Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.connectAgents("prepare", []string{"pi-first", "traex-second"})
	deadline := time.After(5 * time.Second)
	waiting := true
	for waiting {
		select {
		case fn := <-updates:
			fn()
		case <-started:
			waiting = false
		case <-deadline:
			t.Fatal("first installer did not start")
		}
	}
	a.integrations.BatchStop.Store(true)
	released.Store(true)
	close(release)
	for a.integrations.Busy {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("cancelled batch did not finish")
		}
	}
	if mutations.Load() != 1 || len(a.integrations.Connections.Connections) != 2 || a.integrations.Connections.Connections[0].Status != "prepared" {
		t.Fatal("cancellation lost successful item or started remaining item")
	}
	if pending, err := a.loadGenerationOperations(); err != nil || len(pending) != 0 {
		t.Fatalf("successful original receipt not cleared: %v %v", pending, err)
	}
}

func TestConnectionBatchRechecksEligibilityFromServerBeforeMutation(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	var mutations atomic.Int32
	rows := []AgentConnection{
		{ID: "missing", Kind: "cursor", Status: "not_connected"},
		{ID: "connected", Kind: "pi", Detected: true, Status: "configured_pending_new_session"},
		{ID: "conflict", Kind: "traex", Detected: true, Status: "requires_review"},
		{ID: "staging", Kind: "codex", Detected: true, Status: "staging_unknown"},
		{ID: "identity", Kind: "claude_code", Detected: true, Status: "agent_creation_unconfirmed"},
		{ID: "prepared", Kind: "pi", Detected: true, Status: "prepared", Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			mutations.Add(1)
			t.Error("stale ineligible batch selection mutated host")
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 1, Connections: rows})
	}))
	defer server.Close()
	a := New(Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	a.connectAgents("prepare", ids)
	deadline := time.After(5 * time.Second)
	for a.integrations.Busy {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("batch did not finish")
		}
	}
	if mutations.Load() != 0 {
		t.Fatal("ineligible item submitted")
	}
	if !connectionActionAllowed(rows[5], "activate") {
		t.Fatal("prepared item cannot be explicitly activated")
	}
}

func TestConnectionMissingComponentCannotPrepare(t *testing.T) {
	for _, detected := range []bool{true, false} {
		row := AgentConnection{ID: "pi-fixture", Kind: "pi", Status: "component_unavailable", Detected: detected}
		if connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate") {
			t.Fatal("missing component must not allow preparation or activation")
		}
		expected := "暂时无法连接"
		if !detected {
			expected = "未安装"
		}
		if got := connectionStatus(row); got != expected {
			t.Fatalf("status = %q, want %q", got, expected)
		}
	}
}

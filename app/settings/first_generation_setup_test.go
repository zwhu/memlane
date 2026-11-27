package settings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func firstGenerationFixture() (*testHost, GenerationRef) {
	a := setupLayoutFixture()
	a.environment.InitialStep, a.environment.RuntimeStarted = 3, true
	ref := GenerationRef{ID: "provider-setup", Revision: 2}
	row := GenerationRow{Revision: ref.Revision, Digest: "fixture-digest",
		Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "example-model", Endpoint: "https://example.test/v1"}}
	s := &a.integrations
	s.Loaded, s.GenerationLoaded = true, true
	s.Generation = GenerationDirectory{Revision: 5, Profiles: map[string][]GenerationRow{ref.ID: {row}}}
	s.editProfile(ref.ID, row)
	return a, ref
}

func TestFirstGenerationSetupGuideAndEnableReasons(t *testing.T) {
	a, ref := firstGenerationFixture()
	s := &a.integrations
	a.onboarding.page = onboardingProcess
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			a.appearance = theme
			tt := ui.NewTester(a.View, 1200, 820)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			beta3RequireWindowLabels(t, tt, "选择如何提炼记忆", "用已连接的 Agent", "直接调用模型 API", "服务类型",
				"模型", "接口地址", "API Key", "验证并启用", "稍后配置，先进入 Memlane")
			for _, forbidden := range []string{"保存草稿", "设为待启用", "启用并完成", "Provider 配置"} {
				if tt.HasText(forbidden) {
					t.Fatalf("wizard exposed settings-only action %q: %v", forbidden, tt.Texts())
				}
			}
			if _, shown := tt.Find("采样温度"); shown {
				t.Fatal("advanced fields were open by default")
			}
			if _, shown := tt.Find("最大输出 Token"); shown {
				t.Fatal("advanced fields were open by default")
			}
		})
	}
	// The settings and legacy resume paths still require each separate receipt.
	cases := []struct {
		change func()
		want   string
	}{
		{func() { a.environment.RuntimeStarted = false }, "记忆服务尚未启动"},
		{func() { a.environment.RuntimeStarted = true; s.Model = "unsaved" }, "请先保存当前草稿"},
		{func() { s.Model = "example-model" }, "草稿已保存，请先进行真实验证"},
		{func() { s.Validation = GenerationValidation{Status: "running"} }, "正在进行真实验证"},
		{func() { s.Validation = GenerationValidation{Status: "failed"} }, "验证尚未通过"},
		{func() {
			s.Validation = GenerationValidation{Status: "passed"}
			s.Generation.Validations = map[string]GenerationValidation{
				"passed": {Status: "passed", Digest: "fixture-digest", Plain: true, Schema: true, Quality: true},
			}
		}, "请先将当前配置设为待启用"},
		{func() { s.Generation.Next = &ref }, ""},
	}
	for _, tc := range cases {
		tc.change()
		if got := a.firstGenerationEnableBlocker(); !strings.Contains(got, tc.want) || tc.want == "" && got != "" {
			t.Fatalf("expected %q, got %q", tc.want, got)
		}
	}
}

func TestFirstGenerationSetupShowsLivePhaseAndSlowNotice(t *testing.T) {
	a, _ := firstGenerationFixture()
	s := &a.integrations
	s.ValidationOperation = "slow"
	s.Validation = GenerationValidation{Status: "running", Phase: "probe"}
	s.ValidationStartedAt = time.Now().Add(-17 * time.Second)
	tt := ui.NewTester(func(c *ui.Context) { a.generationValidationStatus(c) }, 900, 300)
	for _, label := range []string{"正在向模型发送真实验证", "已等待 17 秒", "响应较慢", "停止验证"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q: %v", label, tt.Texts())
		}
	}
	if tt.HasText("重新验证") {
		t.Fatal("in-flight validation offered a retry")
	}
	now := time.Unix(1000, 0)
	if elapsed := generationValidationElapsed(now, time.Time{}, GenerationValidation{Status: "running", StartedAt: 984.25}); elapsed != 15750*time.Millisecond {
		t.Fatalf("server start time not reflected: %v", elapsed)
	}
	s.rememberGenerationDraft()
	if s.GenerationDrafts[s.ProfileID].ValidationStartedAt != s.ValidationStartedAt {
		t.Fatal("switching profiles would lose the client timer")
	}
}

func TestFirstGenerationSetupRetriesSameSavedCandidate(t *testing.T) {
	for _, status := range []string{"failed", "result_unknown"} {
		t.Run(status, func(t *testing.T) {
			a, ref := firstGenerationFixture()
			s := &a.integrations
			code := "dns_resolution_failed"
			if status == "result_unknown" {
				code = "validation_timeout"
			}
			s.ValidationOperation = "ui-original-validation"
			s.Validation = GenerationValidation{Status: status, ErrorCode: code, FailedPhase: "probe",
				DiagnosticCode: "dns_resolution_failed", HTTPStatus: 502, NetworkError: "gaierror", ElapsedMs: 1200,
				BridgeErrorCode: "credential-secret-in-bridge-code"}
			tokenPath := filepath.Join(t.TempDir(), "manager")
			if err := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			type request struct {
				Profile   GenerationRef `json:"profile"`
				Operation string        `json:"operation_id"`
			}
			requests := make(chan request, 1)
			directory := s.Generation
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/validations":
					var body request
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests <- body
					w.Write([]byte("{}"))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
					json.NewEncoder(w).Encode(directory)
				default:
					http.NotFound(w, r)
				}
			})
			a.client.HTTP = &http.Client{Transport: transport}
			a.config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:18777", ManagementTokenFile: tokenPath}
			a.dispatch = func(func()) {}
			if status == "result_unknown" {
				if err := a.recordGenerationOperation(generationUIOperation{Operation: s.ValidationOperation, Kind: "validations", Profile: ref.ID, Revision: ref.Revision}); err != nil {
					t.Fatal(err)
				}
			}
			tt := ui.NewTester(func(c *ui.Context) { a.generationValidationStatus(c) }, 900, 300)
			for _, want := range []string{"失败阶段：向模型发送真实验证", "DNS 解析失败", "HTTP 502", "耗时 1.2 秒", "重新验证"} {
				if !tt.HasText(want) {
					t.Fatalf("missing %q: %v", want, tt.Texts())
				}
			}
			if status == "failed" && !tt.HasText("无法解析模型服务地址。") {
				t.Fatalf("missing failure reason: %v", tt.Texts())
			}
			if status == "result_unknown" && !tt.HasText("验证超时，请重试。") {
				t.Fatalf("timeout copy was lost: %v", tt.Texts())
			}
			if tt.HasText("核对原验证") || tt.HasText("已保留当前保存的配置") {
				t.Fatalf("verbose boilerplate should not clutter validation status: %v", tt.Texts())
			}
			if tt.HasText("credential-secret") || tt.HasText("gaierror") || tt.HasText("dns_resolution_failed") {
				t.Fatal("rendered raw diagnostic code")
			}
			select {
			case <-requests:
				t.Fatal("validation retried automatically")
			default:
			}
			clickProjectUI(t, tt, "重新验证")
			select {
			case got := <-requests:
				if got.Profile != ref || got.Operation == "ui-original-validation" {
					t.Fatalf("unexpected retry: %+v", got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("explicit retry did not start")
			}
			if *s.Saved != ref || !s.cleanSaved() || s.ValidationStartedAt.IsZero() || s.Validation.Phase != "queued" {
				t.Fatal("retry lost saved configuration or initial timing")
			}
			if _, err := os.Stat(filepath.Join(a.generationOperationsDir(), "ui-original-validation.json")); !os.IsNotExist(err) {
				t.Fatalf("original pending operation was not cleared: %v", err)
			}
		})
	}
}

func TestFirstGenerationSetupNeverDisplaysUntrustedDiagnosticStrings(t *testing.T) {
	a, _ := firstGenerationFixture()
	s := &a.integrations
	s.ValidationOperation = "unsafe"
	s.Validation = GenerationValidation{Status: "failed", ErrorCode: "private-error", DiagnosticCode: "private-diagnostic",
		NetworkError: "private-network", BridgeErrorCode: "private-bridge", FailedPhase: "private-phase", HTTPStatus: 600}
	tt := ui.NewTester(func(c *ui.Context) { a.generationValidationStatus(c) }, 900, 300)
	if tt.HasText("private-") || tt.HasText("HTTP 600") || !tt.HasText("验证未完成，请重试。") || !tt.HasText("重新验证") {
		t.Fatalf("unexpected validation display: %v", tt.Texts())
	}
}

func TestFirstGenerationSetupPoll404TransitionsToRetryableUnknownState(t *testing.T) {
	a, ref := firstGenerationFixture()
	tokenPath := filepath.Join(t.TempDir(), "manager")
	if err := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/generation/validations/") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"generation_validation_not_found"}`))
			return
		}
		json.NewEncoder(w).Encode(a.integrations.Generation)
	})
	a.client.HTTP = &http.Client{Transport: transport}
	a.config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:18777", ManagementTokenFile: tokenPath}
	updates := make(chan func(), 8)
	a.dispatch = func(fn func()) { updates <- fn }
	a.integrations.ValidationOperation = "ui-missing-op"
	a.integrations.Validation = GenerationValidation{Status: "queued"}
	a.pollGenerationValidationWithTiming("ui-missing-op", time.Millisecond, 2*time.Second)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for 404 poll update")
	}
	if a.integrations.Validation.Status != "result_unknown" || a.integrations.Validation.ErrorCode != "validation_not_recorded" {
		t.Fatalf("404 poll left validation stuck: %+v", a.integrations.Validation)
	}
	if !a.integrations.canRetryGenerationValidation() || *a.integrations.Saved != ref {
		t.Fatal("404 poll did not expose retry while preserving saved candidate")
	}
}

type focusedGenerationHarness struct {
	a         *testHost
	updates   chan func()
	outcome   atomic.Value
	mu        sync.Mutex
	mutations []string
}

func newFocusedGenerationHarness(t *testing.T, outcome string) *focusedGenerationHarness {
	t.Helper()
	config, _ := beta3SetupRuntimeConfig(t)
	a := newTestHost(config, nil)
	a.initialSetup = true
	a.onboarding.page = onboardingProcess
	a.environment.InitialStep = 3
	a.environment.RuntimeStarted = true
	a.environment.RuntimePreparation = bootstrap.FirstRuntime{ConfigPath: config.ConfigFile}
	h := &focusedGenerationHarness{a: a, updates: make(chan func(), 32)}
	h.outcome.Store(outcome)
	a.dispatch = func(fn func()) { h.updates <- fn }

	ref := GenerationRef{ID: "provider-setup", Revision: 1}
	digest := strings.Repeat("d", 64)
	directory := GenerationDirectory{Revision: 4, Profiles: map[string][]GenerationRow{}, Validations: map[string]GenerationValidation{}}
	initial := GenerationDirectory{Revision: directory.Revision}
	a.integrations.Generation, a.integrations.GenerationLoaded = initial, true
	row := GenerationRow{Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "demo-model", Endpoint: "https://example.test/v1"}}
	a.selectGenerationProfile(ref.ID, row)
	a.integrations.APIKey, a.integrations.Temperature = "test-api-key", "0"
	var validationOp string
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		var req struct {
			Operation string          `json:"operation_id"`
			Revision  int64           `json:"expected_revision"`
			Profile   json.RawMessage `json:"profile"`
			Key       string          `json:"api_key"`
		}
		if r.Method != http.MethodGet {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request %s: %v", r.URL.Path, err)
			}
			if req.Revision != directory.Revision || req.Operation == "" {
				t.Errorf("stale revision or missing operation for %s: %+v", r.URL.Path, req)
			}
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/generation/profiles/"+ref.ID:
			h.mutations = append(h.mutations, "save")
			var profile GenerationProfile
			if err := json.Unmarshal(req.Profile, &profile); err != nil {
				t.Error(err)
			}
			if profile.Temperature == nil || *profile.Temperature != 0 || profile.MaxOutputTokens != nil || req.Key != "test-api-key" {
				t.Errorf("wrong generation payload: %s", req.Profile)
			}
			profile.CredentialConfigured, profile.CredentialVersion = true, "saved-credential"
			directory.Profiles[ref.ID] = []GenerationRow{{Revision: ref.Revision, Digest: digest, Profile: profile}}
			directory.Revision++
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": ref.ID, "revision": ref.Revision, "digest": digest}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/validations":
			h.mutations = append(h.mutations, "validate")
			var candidate GenerationRef
			if err := json.Unmarshal(req.Profile, &candidate); err != nil || candidate != ref {
				t.Errorf("wrong validation reference: %+v (%v)", candidate, err)
			}
			validationOp = req.Operation
			directory.Revision++
			dirRef := ref
			directory.Validations[validationOp] = GenerationValidation{Status: "running", Digest: digest, Reference: &dirRef, RegisteredRevision: directory.Revision}
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"operation_id": validationOp, "status": "queued"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation/validations/"+validationOp:
			validation := directory.Validations[validationOp]
			switch h.outcome.Load().(string) {
			case "pass", "activation conflict", "activation unknown":
				validation.Status, validation.Plain, validation.Schema, validation.Quality = "passed", true, true, true
			case "fail":
				validation.Status, validation.HTTPStatus, validation.ErrorCode = "failed", 401, "authentication_failed"
			case "timeout":
				validation.Status, validation.ErrorCode = "result_unknown", "validation_timeout"
			}
			directory.Validations[validationOp] = validation
			json.NewEncoder(w).Encode(validation)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/generation/validations/"+validationOp:
			h.mutations = append(h.mutations, "stop")
			validation := directory.Validations[validationOp]
			validation.Status, validation.ErrorCode = "failed", "validation_interrupted"
			directory.Validations[validationOp] = validation
			directory.Revision++
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"operation_id": validationOp, "local_process_exited": true}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/generation/next":
			h.mutations = append(h.mutations, "next")
			if !directory.validated(&ref) || directory.Active != nil {
				t.Errorf("next requested before validation or with an earlier active: %+v", directory)
			}
			directory.Next = &ref
			directory.Revision++
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"next": directory.Next}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			json.NewEncoder(w).Encode(directory)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	a.activateFirstRuntime = func(_ context.Context, prepared bootstrap.FirstRuntime, got bootstrap.FirstGenerationReference, gotDigest string, revision int64) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.mutations = append(h.mutations, "activate")
		if prepared.ConfigPath != config.ConfigFile || got.ID != ref.ID || got.Revision != ref.Revision ||
			gotDigest != digest || revision != directory.Revision || !directory.validated(&ref) ||
			directory.Next == nil || *directory.Next != ref || directory.Active != nil {
			t.Errorf("activation had wrong or incomplete prerequisites: %+v %s %d", got, gotDigest, revision)
		}
		switch h.outcome.Load().(string) {
		case "activation conflict":
			directory.Revision++
			return &ControlResponseError{Status: 409, Code: "revision_conflict", Phase: "pre_mutation"}
		case "activation unknown":
			return &ControlResponseError{Status: 409, Code: "revision_conflict", Phase: "post_mutation"}
		}
		directory.Active, directory.Next = &ref, nil
		var runtime map[string]any
		b, err := os.ReadFile(prepared.ConfigPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &runtime); err != nil {
			return err
		}
		runtime["first_install"] = bootstrap.FirstInstallation{Schema: 1, StoreID: config.FirstInstall.StoreID, Setup: false}
		runtime["generation_mode"], runtime["model"] = "provider", directory.Profiles[ref.ID][0].Profile.Model
		active, _ := json.Marshal(map[string]any{"schema_version": 2, "active": directory.Active, "profiles": directory.Profiles})
		if err := os.WriteFile(config.GenerationFile, active, 0600); err != nil {
			return err
		}
		state := []byte(`{"schema_version":1,"initialization_completed_at":"2026-11-25T12:34:56Z","first_used_date":"2026-11-25","first_used_timezone":"UTC"}`)
		if err := os.WriteFile(filepath.Join(filepath.Dir(config.ConfigFile), "client-state.json"), state, 0600); err != nil {
			return err
		}
		b, err = json.Marshal(runtime)
		if err != nil {
			return err
		}
		return os.WriteFile(prepared.ConfigPath, b, 0600)
	}
	return h
}

func (h *focusedGenerationHarness) requireMutations(t *testing.T, want ...string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if !reflect.DeepEqual(h.mutations, want) {
		t.Fatalf("mutations %v, want %v", h.mutations, want)
	}
}

func TestFocusedOnboardingValidationEnablesOnlyAfterAllFourOperations(t *testing.T) {
	h := newFocusedGenerationHarness(t, "pass")
	tt := ui.NewTester(h.a.View, 1200, 820)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickProjectUI(t, tt, "模型 ID")
	if !tt.Focused("模型 ID") {
		t.Fatal("model field did not receive keyboard focus")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Compose("模型", 2)
	tt.Key(0, ui.KeyEnter)
	tt.Compose("", 0)
	if h.a.integrations.Busy {
		t.Fatal("Enter or IME confirmation in the model field started verification")
	}
	h.requireMutations(t)
	clickProjectUI(t, tt, "验证并启用")
	drainReading(t, h.updates, func() bool { return !h.a.integrations.Busy })
	h.requireMutations(t, "save", "validate", "next", "activate")
	if h.a.onboardingPage() != onboardingDone || !h.a.initialSetup || h.a.integrations.Generation.Active == nil {
		t.Fatalf("successful activation did not reach the completion page: page=%v error=%q", h.a.onboardingPage(), h.a.onboardingGeneration.failure)
	}
	tt.Frame()
	beta3RequireWindowLabels(t, tt, "Memlane 已就绪", "进入 Memlane")
}

func TestFocusedOnboardingFailedVerificationKeepsSavedProfileForRetry(t *testing.T) {
	h := newFocusedGenerationHarness(t, "fail")
	h.a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return !h.a.integrations.Busy })
	h.requireMutations(t, "save", "validate")
	g, s := &h.a.onboardingGeneration, &h.a.integrations
	if g.phase != onboardingValidating || s.Generation.Active != nil || s.Generation.Next != nil || !s.cleanSaved() ||
		s.APIKey != "" || s.Temperature != "0" {
		t.Fatalf("failed validation lost saved profile or selected an active profile: %+v", g)
	}
	tt := ui.NewTester(h.a.View, 1200, 820)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	for _, want := range []string{"保存配置", "发送一次简短测试请求", "启用并启动记忆提炼", "HTTP 401",
		"请检查凭据和模型权限", "修改配置", "重新验证"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	h.outcome.Store("pass")
	clickProjectUI(t, tt, "重新验证")
	drainReading(t, h.updates, func() bool { return !s.Busy })
	h.requireMutations(t, "save", "validate", "validate", "next", "activate")
}

func TestFocusedOnboardingStopsSlowValidationWithoutSelectingNext(t *testing.T) {
	h := newFocusedGenerationHarness(t, "slow")
	h.a.runOnboardingGeneration()
	g, s := &h.a.onboardingGeneration, &h.a.integrations
	drainReading(t, h.updates, func() bool { return g.validation.Status == "running" })
	g.validationStarted = time.Now().Add(-16 * time.Second)
	tt := ui.NewTester(h.a.View, 1200, 820)
	tt.SetDark(true)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if !tt.HasText("响应较慢") {
		t.Fatalf("missing slow notice: %v", tt.Texts())
	}
	tt.Scroll(600, 550, 0, 700)
	fixtureImage(t, tt, "focused-processing-slow-dark")
	clickProjectUI(t, tt, "停止验证")
	drainReading(t, h.updates, func() bool { return !s.Busy })
	h.requireMutations(t, "save", "validate", "stop")
	if !strings.Contains(g.failure, "已停止验证") || !s.cleanSaved() || s.Generation.Active != nil || s.Generation.Next != nil {
		t.Fatalf("cancellation did not preserve saved, inactive config: %+v", g)
	}
}

func TestFocusedOnboardingGenerationDefaultsToLoggedInCLI(t *testing.T) {
	a, _ := firstGenerationFixture()
	a.onboarding.page = onboardingProcess
	a.integrations.Editor = false
	a.integrations.Generation = GenerationDirectory{
		Revision: 1,
		AgentBridges: map[string]GenerationProfile{
			"pi": {Mode: "agent_bridge", BridgeKind: "pi", Model: "pi-default-model"},
		},
		DetectedProcessingCLIs: []string{"pi", "traex"},
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	s := &a.integrations
	if s.DraftMode != "agent_bridge" || s.BridgeKind != "pi" || !tt.HasText("推荐") || !tt.HasText("pi-default-model · 已登录") {
		t.Fatalf("logged-in CLI was not recommended and selected: %v", tt.Texts())
	}
	fixtureImage(t, tt, "focused-processing-agent-light")
	clickProjectUI(t, tt, "复制 TRAE CLI 登录命令")
	if tt.Clipboard() != "traecli login" || s.BridgeKind != "pi" {
		t.Fatal("disabled CLI did not expose its login command")
	}
	clickProjectUI(t, tt, "直接调用模型 API")
	if s.ProviderLabel != "OpenAI 兼容" || !tt.HasText("API Key") {
		t.Fatalf("unexpected API defaults: %v", tt.Texts())
	}
	s.ProviderLabel = "Ollama"
	tt.Frame()
	if _, shown := tt.Find("API Key"); shown {
		t.Fatal("Ollama should hide the API key field")
	}
}

type generationTestTransport func(*http.Request) (*http.Response, error)

func (f generationTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOnboardingValidationNotRegisteredCanBeRetried(t *testing.T) {
	h := newFocusedGenerationHarness(t, "pass")
	a := h.a
	original := a.client.HTTP.Transport
	var dropped atomic.Bool
	a.client.HTTP.Transport = generationTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/generation/validations" && !dropped.Swap(true) {
			return nil, io.ErrUnexpectedEOF
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/generation/validations/") {
			h.mu.Lock()
			unregistered := len(h.mutations) == 1
			h.mu.Unlock()
			if unregistered {
				return queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{"error": "generation_validation_not_found"})
				}).RoundTrip(r)
			}
		}
		if r.URL.Path == "/v1/connections" {
			return queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(ConnectionsDirectory{})
			}).RoundTrip(r)
		}
		return original.RoundTrip(r)
	})
	a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	if !a.integrations.hasPendingGeneration() {
		t.Fatal("the disconnected POST did not leave a receipt to check")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "核对原操作")
	drainReading(t, h.updates, func() bool {
		return a.integrations.Validation.Status == "result_unknown" && !a.integrations.Loading
	})
	tt.Frame()
	if a.integrations.hasPendingGeneration() || !tt.HasText("服务端未登记这次验证") {
		t.Fatalf("unregistered operation still locks the wizard: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "重新验证")
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	h.requireMutations(t, "save", "validate", "next", "activate")
	if a.onboardingPage() != onboardingDone {
		t.Fatal("retry did not enable generation")
	}
}

func TestOnboardingActivationPreMutationRejectionRefreshesRevision(t *testing.T) {
	h := newFocusedGenerationHarness(t, "activation conflict")
	a := h.a
	a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"重新验证", "修改配置", "稍后配置，先进入 Memlane"} {
		if !tt.HasText(label) {
			t.Fatalf("missing action %q after pre-mutation rejection: %v", label, tt.Texts())
		}
	}
	if tt.HasText("核对启用结果") || !a.canDeferFirstGenerationSetup() || a.integrations.Generation.Revision != 8 {
		t.Fatalf("stale activation retained: revision=%d", a.integrations.Generation.Revision)
	}
	h.outcome.Store("pass")
	clickProjectUI(t, tt, "重新验证")
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	h.requireMutations(t, "save", "validate", "next", "activate", "validate", "next", "activate")
	if a.onboardingPage() != onboardingDone {
		t.Fatal("retry did not enable generation at the refreshed revision")
	}
}

func TestOnboardingUnknownActivationKeepsOriginalRequest(t *testing.T) {
	h := newFocusedGenerationHarness(t, "activation unknown")
	a := h.a
	a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("核对启用结果") || tt.HasText("修改配置") || a.canDeferFirstGenerationSetup() {
		t.Fatalf("unknown activation unlocked editing: %v", tt.Texts())
	}
	h.outcome.Store("pass")
	clickProjectUI(t, tt, "核对启用结果")
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	h.requireMutations(t, "save", "validate", "next", "activate", "activate")
}

func TestFocusedOnboardingRecordedTimeoutClearsJournalAndAllowsImmediateRetry(t *testing.T) {
	prevPoll := onboardingValidationPollInterval
	onboardingValidationPollInterval = time.Millisecond
	t.Cleanup(func() { onboardingValidationPollInterval = prevPoll })

	h := newFocusedGenerationHarness(t, "timeout")
	a := h.a

	a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	if a.integrations.hasPendingGeneration() {
		t.Fatalf("recorded validation_timeout left pending op locking wizard: %+v", a.integrations.PendingOperations)
	}
	if !strings.Contains(a.onboardingGeneration.failure, "验证超时") {
		t.Fatalf("missing timeout failure copy: %q", a.onboardingGeneration.failure)
	}

	h.outcome.Store("pass")
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "重新验证")
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	if a.onboardingPage() != onboardingDone {
		t.Fatalf("retry after recorded timeout did not complete onboarding: page=%v failure=%q", a.onboardingPage(), a.onboardingGeneration.failure)
	}
}

func TestFocusedOnboardingStopValidation404ClearsJournalAndUnlocksRetry(t *testing.T) {
	prevPoll := onboardingValidationPollInterval
	onboardingValidationPollInterval = time.Millisecond
	t.Cleanup(func() { onboardingValidationPollInterval = prevPoll })

	h := newFocusedGenerationHarness(t, "slow")
	a := h.a
	orig := a.client.HTTP.Transport
	a.client.HTTP.Transport = generationTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/generation/validations/") {
			return queueTestTransport(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"generation_validation_not_found"}`))
			}).RoundTrip(r)
		}
		return orig.RoundTrip(r)
	})

	a.runOnboardingGeneration()
	drainReading(t, h.updates, func() bool { return a.onboardingGeneration.validation.Status == "running" })
	a.stopOnboardingValidation()
	drainReading(t, h.updates, func() bool { return !a.integrations.Busy })
	if a.integrations.hasPendingGeneration() || !strings.Contains(a.onboardingGeneration.failure, "已停止验证") {
		t.Fatalf("delete 404 did not unlock onboarding: pending=%+v failure=%q", a.integrations.PendingOperations, a.onboardingGeneration.failure)
	}
}

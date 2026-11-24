package simpledesktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func firstGenerationFixture() (*App, GenerationRef) {
	a := setupLayoutFixture()
	a.environment.InitialStep, a.environment.RuntimeStarted = 3, true
	ref := GenerationRef{ID: "provider-setup", Revision: 2}
	row := GenerationRow{Revision: ref.Revision, Digest: "fixture-digest",
		Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "example-model", Endpoint: "https://example.test/v1"}}
	s := &a.integrations
	s.Loaded = true
	s.Generation = GenerationDirectory{Revision: 5, Profiles: map[string][]GenerationRow{ref.ID: {row}}}
	s.editProfile(ref.ID, row)
	return a, ref
}

func TestFirstGenerationSetupGuideAndEnableReasons(t *testing.T) {
	a, ref := firstGenerationFixture()
	s := &a.integrations
	tt := ui.NewTester(a.View, 1200, 820)
	for _, want := range []string{"保存草稿", "真实验证", "验证（会消耗额度）", "启用并完成", "设为待启用"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
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
		{func() { s.Generation.Next = &ref }, "验证已通过，点击「启用并完成」即可开始使用"},
	}
	for _, tc := range cases {
		tc.change()
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Fatalf("missing %q: %v", tc.want, tt.Texts())
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
			a.Client.HTTP = &http.Client{Transport: transport}
			a.Config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:18777", ManagementTokenFile: tokenPath}
			a.Update = func(func()) {}
			if status == "result_unknown" {
				if err := a.recordGenerationOperation(generationUIOperation{Operation: s.ValidationOperation, Kind: "validations", Profile: ref.ID, Revision: ref.Revision}); err != nil {
					t.Fatal(err)
				}
			}
			tt := ui.NewTester(func(c *ui.Context) { a.generationValidationStatus(c) }, 900, 300)
			for _, want := range []string{"失败阶段：向模型发送真实验证", "DNS 解析失败", "HTTP 502", "耗时 1.2 秒",
				"已保留当前保存的配置，无需重新填写。", "请检查 API 地址与 DNS", "重新验证", "核对原验证"} {
				if !tt.HasText(want) {
					t.Fatalf("missing %q: %v", want, tt.Texts())
				}
			}
			if status == "result_unknown" && !tt.HasText("验证等待超时，结果未确认；请核对原验证。") {
				t.Fatal("timeout copy was lost")
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
	if tt.HasText("private-") || tt.HasText("HTTP 600") || !tt.HasText("已保留当前保存的配置") {
		t.Fatalf("unexpected validation display: %v", tt.Texts())
	}
}

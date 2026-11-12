package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
	"testing"
	"time"
)

func TestSettingsCandidateDesignFrames(t *testing.T) {
	for _, scene := range []string{"environment", "agents", "processing", "service", "diagnostics", "updates", "window"} {
		for _, width := range []int{1200, 800} {
			a := New("0.0.8-synthetic", nil)
			a.visualFixture = true
			a.page = "设置"
			a.settingsSection = "environment"
			a.maintenanceSection = "components"
			a.runtimeStatus = RuntimeStatus{Phase: "ready", Installed: true}
			a.serviceState = "ready"
			a.stopRuntime = func() {}
			a.prepareRuntime = func() {}
			a.ConfigureWindowActions(func() {}, func() {}, func() {})
			a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{WorkerID: "rust", ModelID: "e5"}, Official: &official.Controller{}}
			occupied := int64(512000000)
			for _, name := range []string{"rust", "e5", "helper / LaunchAgent", "cognee-service", "cognee-codex-plugin", "cognee-bge-zh"} {
				a.environment.Items = append(a.environment.Items, EnvironmentItem{Name: name, State: "verified_reusable", OccupiedBytes: &occupied})
			}
			a.environment.CheckedAt = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
			a.agents.Home = "/synthetic/home"
			a.agents.PiDir = "/synthetic/pi-config"
			a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}, {Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "codex-cli 0.167.0"}}
			a.agents.Connections = []records.Connection{{ID: "synthetic-pi", Agent: "pi", Name: "pi", Enabled: true, Generation: 3}}
			a.agents.Installs = map[string]agents.Installation{"synthetic-pi": {Connection: a.agents.Connections[0], Options: agents.InstallOptions{ComputerLevel: true, AgentPath: "/synthetic/pi", PiDirectory: "/synthetic/pi-config"}, ProtocolGeneration: 3, ProtocolVerifiedAt: "2026-11-11T09:00:00+08:00", AgentVersion: "0.84.1"}}
			spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-v4.1-flash", Connection: "synthetic-pi", ConnectionGeneration: 3}
			rev := records.ProcessingRevision{ID: "synthetic-revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "synthetic-validation", Revision: "synthetic-revision", State: "succeeded"}}
			a.processing = ProcessingUI{Mode: "agent", Loaded: true, CandidatesLoaded: true, CheckedAt: time.Now(), Drafts: map[string]records.ProcessingSpec{"agent": spec}, Candidates: []ProcessingAgentCandidate{{Agent: "pi", Name: "pi", Version: "0.84.1", Connection: "synthetic-pi", Generation: 3, Provider: spec.Provider, Model: spec.Model, Available: true}}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": rev}, Current: records.ProcessingSelection{Active: true, Generation: 4, Revision: &rev}, Runtime: map[string]records.ProcessingReadiness{rev.ID: {State: "ready"}}}}
			switch scene {
			case "agents", "processing":
				a.settingsSection = scene
				a.maintenanceSection = ""
			case "environment":
			default:
				a.maintenanceSection = scene
			}
			a.schedules.Loaded = true
			a.schedules.Plans = []records.Schedule{{Enabled: true}}
			a.updateText = "当前已是最新版本"
			a.keepService = true
			tt := ui.NewTester(a.View, width, map[bool]int{true: 620, false: 820}[width == 800])
			if scene == "agents" && (tt.HasText("配置尚未安装完成，请重新连接。") || tt.HasText("未找到安装")) {
				t.Fatal("incomplete installation fixture")
			}
			if scene == "environment" {
				n, ok := tt.Find("本地向量模型")
				if !ok || (width == 1200 && n.Y+n.H > 756) {
					t.Fatalf("model card outside first viewport width %d: %#v", width, n)
				}
			}
			if scene == "processing" {
				if !tt.HasText("未取得验证结果") || !tt.HasText("未知 · 未取得隔离验证结果") || tt.HasText("结构校验通过") {
					t.Fatal("aggregate success fabricated individual proof")
				}
			}
			if scene == "service" {
				if !tt.HasText("已启用 1 个计划 · 按各自授权执行") {
					t.Fatal("loaded schedule fact absent")
				}
			}
			writeScreenshot(t, tt, fmt.Sprintf("%s-%d.png", scene, width))
			if a.libraryContext != nil || a.firstLaunch.Context != nil {
				t.Fatal("readonly fixture unexpectedly gained runtime context")
			}
		}
	}
}

func TestProcessingCandidateRequiresIndividualHTTPProof(t *testing.T) {
	a := New("test", nil)
	spec := records.ProcessingSpec{Mode: "provider", Model: "exact"}
	rev := records.ProcessingRevision{Spec: spec, Validation: &records.ProcessingValidation{State: "succeeded", HTTP: &records.ProcessingHTTPReceipt{RequestedModel: "other", RequestSent: true, ResponseComplete: true, Status: 200, NativeSchemaRequested: true, SchemaValid: true}}}
	a.processing = ProcessingUI{Mode: "provider", Drafts: map[string]records.ProcessingSpec{"provider": spec}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"provider": rev}}}
	rev.ID = "saved"
	rev.Validation.Revision = "saved"
	a.processing.Configuration.Saved["provider"] = rev
	tt := ui.NewTester(a.processingValidationFacts, 800, 300)
	if tt.HasText("结构校验通过") {
		t.Fatal("wrong-model receipt reused")
	}
	rev.Validation.HTTP.RequestedModel = "exact"
	a.processing.Configuration.Saved["provider"] = rev
	tt.Frame()
	if !tt.HasText("结构校验通过") || !tt.HasText("未知 · 未取得隔离验证结果") {
		t.Fatal("real schema evidence or unknown isolation absent")
	}
}

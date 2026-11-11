package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func capabilityUIFixture(agent string) *App {
	a := New("synthetic-evidence", nil)
	spec := records.ProcessingSpec{Mode: "agent", Agent: agent, Provider: "synthetic-provider", Model: "synthetic-model", Connection: "synthetic-connection", ConnectionGeneration: 3}
	evidence := &records.ProcessingCapabilityEvidence{ValidationID: "validation", Revision: "revision", Attempt: 2, DependencyDigest: "dependency", Agent: agent, ResponseComplete: true, StructureValid: true, IsolationConfigured: true, IsolationPolicy: map[string]string{"pi": "pi_private_no_session_no_tools_v1", "codex": "codex_private_ephemeral_no_tools_v1"}[agent], Reaped: true, GroupGone: true}
	rev := records.ProcessingRevision{ID: "revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "validation", Revision: "revision", Attempt: 2, DependencyDigest: "dependency", State: "succeeded", Evidence: evidence}}
	a.page = "设置"
	a.settingsSection = "processing"
	a.visualFixture = true
	a.processing = ProcessingUI{Mode: "agent", Loaded: true, CandidatesLoaded: true, Drafts: map[string]records.ProcessingSpec{"agent": spec}, Candidates: []ProcessingAgentCandidate{{Agent: agent, Name: agent, Version: "synthetic", Connection: spec.Connection, Generation: 3, Provider: spec.Provider, Model: spec.Model, Available: true}}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": rev}, Current: records.ProcessingSelection{Active: true, Generation: 1, Revision: &rev}, Runtime: map[string]records.ProcessingReadiness{rev.ID: {State: "ready"}}}}
	return a
}
func TestProcessingEvidencePresentationRequiresExactIdentity(t *testing.T) {
	for _, field := range []string{"nil", "validation", "revision", "attempt", "dependency", "agent", "blank-owner", "draft"} {
		t.Run(field, func(t *testing.T) {
			a := capabilityUIFixture("pi")
			rev := a.processing.Configuration.Saved["agent"]
			e := rev.Validation.Evidence
			switch field {
			case "nil":
				rev.Validation.Evidence = nil
			case "validation":
				e.ValidationID = "other"
			case "revision":
				e.Revision = "other"
			case "attempt":
				e.Attempt++
			case "dependency":
				e.DependencyDigest = "other"
			case "agent":
				e.Agent = "codex"
			case "blank-owner":
				rev.Validation.DependencyDigest = ""
			case "draft":
				draft := a.processing.Drafts["agent"]
				draft.Model = "other"
				a.processing.Drafts["agent"] = draft
			}
			a.processing.Configuration.Saved["agent"] = rev
			tt := ui.NewTester(a.processingValidationFacts, 800, 300)
			for _, label := range []string{"已收到模型响应", "结构校验通过", "本次本机隔离已配置"} {
				if tt.HasText(label) {
					t.Fatal("foreign or legacy evidence trusted", field, label)
				}
			}
			if !tt.HasText("未取得验证结果") {
				t.Fatal("unknown outcome hidden")
			}
		})
	}
}
func TestProcessingEvidencePresentationIndependentFactsAndPolicies(t *testing.T) {
	for _, agent := range []string{"pi", "codex"} {
		a := capabilityUIFixture(agent)
		rev := a.processing.Configuration.Saved["agent"]
		e := rev.Validation.Evidence
		e.StructureValid = false
		tt := ui.NewTester(a.processingValidationFacts, 800, 300)
		if !tt.HasText("已收到模型响应") || tt.HasText("结构校验通过") || !tt.HasText("本次本机隔离已配置") {
			t.Fatal("individual outcomes conflated", agent)
		}
		e.IsolationPolicy = "other-policy"
		tt.Frame()
		if tt.HasText("本次本机隔离已配置") {
			t.Fatal("unknown launch policy trusted")
		}
		e.IsolationPolicy = map[string]string{"pi": "pi_private_no_session_no_tools_v1", "codex": "codex_private_ephemeral_no_tools_v1"}[agent]
		e.IsolationConfigured = false
		e.StructureValid = true
		e.ResponseComplete = false
		tt.Frame()
		if tt.HasText("结构校验通过") || tt.HasText("已收到模型响应") || tt.HasText("本次本机隔离已配置") {
			t.Fatal("missing observations promoted")
		}
	}
}
func TestProcessingEvidenceReadonlyFrames(t *testing.T) {
	for _, agent := range []string{"pi", "codex"} {
		for _, width := range []int{1200, 800} {
			a := capabilityUIFixture(agent)
			tt := ui.NewTester(a.View, width, 720)
			for _, label := range []string{"已收到模型响应", "结构校验通过", "本次本机隔离已配置"} {
				if !tt.HasText(label) {
					t.Fatal("owned fact absent", agent, width, label)
				}
			}
			if tt.HasText("端到端采集已验证") || tt.HasText("提炼会话不会采集") {
				t.Fatal("local configuration exaggerated")
			}
			writeScreenshot(t, tt, fmt.Sprintf("evidence-%s-%d.png", agent, width))
		}
	}
}

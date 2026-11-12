//go:build e2e

package desktop

import (
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// SetSettingsVisualFixture is read-only synthetic input for the isolated e2e
// bundle. No runtime context, IPC, Agent home, credential or write callback.
func (a *App) SetSettingsVisualFixture(scene string) {
	if scene == "" {
		return
	}
	a.visualFixture = true
	a.page, a.settingsSection, a.maintenanceSection = "设置", "environment", "components"
	a.runtimeStatus = RuntimeStatus{Phase: "ready", Installed: true}
	a.serviceState = "ready"
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{WorkerID: "synthetic-runtime", ModelID: "synthetic-model"}, Official: &official.Controller{}}
	occupied := int64(1024)
	for _, name := range []string{"synthetic-runtime", "synthetic-model", "helper / LaunchAgent", "cognee-service", "cognee-codex-plugin", "cognee-bge-zh"} {
		a.environment.Items = append(a.environment.Items, EnvironmentItem{Name: name, State: "verified_reusable", OccupiedBytes: &occupied})
	}
	a.environment.CheckedAt = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	a.agents.Home = "/synthetic/home"
	a.agents.PiDir = "/synthetic/pi-config"
	a.agents.Found = []agents.Discovery{{Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "合成版本"}, {Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "合成版本"}, {Agent: "trae", Name: "TRAE", Version: "未检测到"}}
	conn := records.Connection{ID: "synthetic-pi", Agent: "pi", Name: "pi", Enabled: true, Generation: 3}
	a.agents.Connections = []records.Connection{conn}
	a.agents.Installs = map[string]agents.Installation{conn.ID: {Connection: conn, Options: agents.InstallOptions{ComputerLevel: true, AgentPath: "/synthetic/pi", PiDirectory: a.agents.PiDir}, ProtocolGeneration: 3, ProtocolVerifiedAt: "2026-11-12T09:00:00Z"}}
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "synthetic-provider", Model: "synthetic-model", Connection: conn.ID, ConnectionGeneration: 3}
	rev := records.ProcessingRevision{ID: "synthetic-revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "synthetic-validation", Revision: "synthetic-revision", State: "succeeded"}}
	a.processing = ProcessingUI{Mode: "agent", Loaded: true, CandidatesLoaded: true, Drafts: map[string]records.ProcessingSpec{"agent": spec}, Candidates: []ProcessingAgentCandidate{{Agent: "pi", Name: "pi", Version: "合成版本", Connection: conn.ID, Generation: 3, Provider: spec.Provider, Model: spec.Model, Available: true}}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": rev}, Runtime: map[string]records.ProcessingReadiness{rev.ID: {State: "ready"}}}}
	a.backups.Loaded = true
	a.backups.Items = []records.BackupReceipt{{ID: "synthetic-backup", CreatedAt: "2026-11-12T09:00:00Z", Bytes: 1024, Schema: 26, Verification: "verified"}}
	a.updateText = "合成验收：没有接入更新服务"
	switch {
	case strings.HasPrefix(scene, "environment"):
		if scene != "environment" {
			a.environment.Items[0].State = "needs_preparation"
			a.environment.Items[0].Bytes = 1024
			a.environment.Items[0].ExpectedBytes = 2048
		}
		if scene == "environment-progress" {
			a.runtimeStatus.Phase = "downloading"
			a.runtimeStatus.Components = []components.PreparationReceipt{{ID: "synthetic-runtime", Phase: "downloading", Downloaded: 512, Total: 1024}}
		}
		if scene == "environment-failed" {
			a.runtimeStatus.Phase = "failed"
			a.runtimeStatus.Message = "合成验收：组件准备失败，已保存下载保留"
		}
	case strings.HasPrefix(scene, "agent"):
		a.settingsSection = "agents"
		if scene == "agent-detail" || scene == "agent-remove" {
			a.agents.Selection = "connection-" + conn.ID
		}
		if scene == "agent-remove" {
			a.agents.SettingsRemoveConfirmation = conn.ID
		}
	case strings.HasPrefix(scene, "processing"):
		a.settingsSection = "processing"
		if scene == "processing-provider" {
			a.processing.Mode = "provider"
		}
		if scene == "processing-magpie" {
			a.processing.Mode = "magpie"
		}
		if scene == "processing-validating" {
			rev.Validation.State = "validating"
			a.processing.Configuration.Saved["agent"] = rev
		}
		if scene == "processing-active" {
			a.processing.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &rev}
		}
	case strings.HasPrefix(scene, "service"):
		a.maintenanceSection = "service"
		if scene == "service-stopped" {
			a.runtimeStatus.Phase = "stopped"
			a.serviceState = "stopped"
		}
		if scene == "service-failed" {
			a.runtimeStatus.Phase = "failed"
			a.serviceState = "failed"
			a.maintenanceNext.Recovery = "exhausted"
		}
	case scene == "backup", scene == "restore":
		a.maintenanceSection = "backups"
		if scene == "restore" {
			a.backups.RestoreSelection = a.backups.Items[0]
		}
	case scene == "index":
		a.maintenanceSection = "index"
	case scene == "update":
		a.maintenanceSection = "updates"
	case scene == "about":
		a.maintenanceSection = "diagnostics"
	}
}

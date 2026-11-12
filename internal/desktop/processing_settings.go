package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

// Runtime reads may include two sequential five-second metadata probes per
// Agent revision, plus storage/IPC. These are bounded end-to-end UI budgets,
// not longer model validation or per-probe limits. Activation resolves once
// and then reads readiness again, so it needs a separate round-trip margin.
const (
	processingRuntimeMetadataTimeout     = 20 * time.Second
	processingValidationBeginTimeout     = 20 * time.Second
	processingActivationRoundTripTimeout = 30 * time.Second
)

type ProcessingAgentCandidate struct {
	Agent, Name, Version, Connection, Provider, Model, Reason string
	Generation                                                int64
	Available                                                 bool
}

// ProcessingUI is presentation state for the service-owned processing ledger.
// It does not infer success from a selected card or from a completed IPC call.
type ProcessingUI struct {
	HTTP             processingHTTPUI
	Mode             string
	Drafts           map[string]records.ProcessingSpec
	Configuration    records.ProcessingConfiguration
	Candidates       []ProcessingAgentCandidate
	CandidatesLoaded bool
	Loaded           bool
	CheckedAt        time.Time
	ProgressMode     string
	Progress         *records.ProcessingValidationProgress
	ValidationID     string
	Notice, Error    string
	ReadError        string
	Sequence         uint64
	Loading          bool
	Refreshing       bool
	RefreshSequence  uint64
	Busy             bool
	Polling          bool
}

func (s *ProcessingUI) ensure() {
	if s.Mode == "" {
		s.Mode = "agent"
	}
	if s.Drafts == nil {
		s.Drafts = map[string]records.ProcessingSpec{}
	}
	if s.Configuration.Saved == nil {
		s.Configuration.Saved = map[string]records.ProcessingRevision{}
	}
}

func (s *ProcessingUI) draft() records.ProcessingSpec {
	s.ensure()
	return s.Drafts[s.Mode]
}

func (s *ProcessingUI) saved() (records.ProcessingRevision, bool) {
	s.ensure()
	v, ok := s.Configuration.Saved[s.Mode]
	return v, ok && v.ID != ""
}

func (s *ProcessingUI) sameSavedDraft() bool {
	saved, ok := s.saved()
	return ok && reflect.DeepEqual(saved.Spec, s.draft())
}

func (s *ProcessingUI) validDraft() bool {
	draft := s.draft()
	if draft.Model == "" || strings.TrimSpace(draft.Model) != draft.Model || len(draft.Model) > 256 {
		return false
	}
	switch draft.Mode {
	case "agent":
		return (draft.Agent == "pi" || draft.Agent == "codex") && draft.Connection != "" && draft.ConnectionGeneration > 0 && draft.Provider != ""
	case "provider":
		return processingKnownSupplier(draft.Provider) && processingProviderEndpoint(draft.Endpoint) && processingHTTPProtocol(draft.Protocol) && draft.Credential != "" && draft.CredentialRevision > 0
	case "magpie":
		return processingLocalGateway(draft.Endpoint) && processingHTTPProtocol(draft.Protocol) && draft.Credential == "" && draft.CredentialRevision == 0 && draft.Provider == ""
	}
	return false
}

func (s *ProcessingUI) availableDraft() bool {
	draft := s.draft()
	if draft.Mode == "provider" && s.HTTP.Editing == draft.Provider && s.HTTP.Editing != "" && (s.HTTP.Secret != "" || s.HTTP.Endpoint != draft.Endpoint || s.HTTP.Protocol != draft.Protocol) {
		return false
	}
	if draft.Mode == "magpie" && s.HTTP.GatewayEndpoint != "" && s.HTTP.GatewayEndpoint != draft.Endpoint {
		return false
	}
	if s.draft().Mode != "agent" {
		return s.validDraft()
	}
	if !s.CandidatesLoaded {
		return true
	}
	for _, candidate := range s.Candidates {
		if candidate.Available && processingCandidateSelected(draft, candidate) {
			return true
		}
	}
	return false
}

func (s *ProcessingUI) currentConnectionAvailable() bool {
	current := s.Configuration.Current
	if !s.CandidatesLoaded || !current.Active || current.Revision == nil || current.Revision.Spec.Mode != "agent" {
		return true
	}
	for _, candidate := range s.Candidates {
		if candidate.Available && processingCandidateSelected(current.Revision.Spec, candidate) {
			return true
		}
	}
	return false
}

func (s *ProcessingUI) CanSave() bool {
	return !s.Loading && !s.Busy && !s.Polling && !s.HTTP.CredentialBusy && s.validDraft() && s.availableDraft() && !s.sameSavedDraft()
}

func (s *ProcessingUI) CanValidate() bool {
	saved, ok := s.saved()
	if !ok || s.Loading || s.Busy || s.Polling || s.HTTP.CredentialBusy || !s.sameSavedDraft() || !s.availableDraft() {
		return false
	}
	return saved.Validation == nil || saved.Validation.State == "succeeded" || saved.Validation.State == "failed" || saved.Validation.State == "cancelled" || saved.Validation.State == "interrupted" || saved.Validation.State == "superseded"
}

func (s *ProcessingUI) CanCancel() bool {
	saved, ok := s.saved()
	return ok && !s.Busy && saved.Validation != nil && saved.Validation.State == "validating" && !saved.Validation.CancelRequested
}

func (s *ProcessingUI) CanActivate() bool {
	saved, ok := s.saved()
	if !ok || s.Loading || s.Busy || s.Polling || s.HTTP.CredentialBusy || !s.sameSavedDraft() || !s.availableDraft() || !s.runtimeReady(saved.ID) || s.ReadError != "" || saved.Validation == nil || saved.Validation.State != "succeeded" || saved.Validation.CancelRequested {
		return false
	}
	current := s.Configuration.Current
	return !current.Active || current.Revision == nil || current.Revision.ID != saved.ID
}

func (s *ProcessingUI) runtimeReady(revision string) bool {
	return s.Configuration.Runtime[revision].State == "ready"
}
func (s *ProcessingUI) currentRuntimeReady() bool {
	current := s.Configuration.Current
	return s.ReadError == "" && current.Active && current.Revision != nil && s.runtimeReady(current.Revision.ID)
}

func (s *ProcessingUI) currentLabel() string {
	if !s.Loaded {
		if s.ReadError != "" {
			return "记忆处理读取失败"
		}
		return "记忆处理待读取"
	}
	if s.ReadError != "" {
		return "记忆处理读取失败；保留上次配置"
	}
	current := s.Configuration.Current
	if !current.Active || current.Revision == nil {
		if len(s.Configuration.Saved) > 0 {
			return "记忆处理已保存，待启用"
		}
		return "记忆处理未配置"
	}
	if !s.currentConnectionAvailable() {
		return "处理连接已变化，需重新验证"
	}
	switch s.Configuration.Runtime[current.Revision.ID].State {
	case "ready":
		return "当前方式：" + processingSpecLabel(current.Revision.Spec)
	case "dependency_changed":
		return "处理依赖已变化，需重新验证"
	case "verification_required":
		return "处理方式需重新验证并明确启用"
	case "unavailable":
		return "处理依赖暂不可用，请检查并重新验证"
	default:
		return "当前处理状态待读取"
	}
}

func (a *App) settingsSectionLabel() string {
	key := a.selectedSettingsSection()
	for _, item := range settingsSections {
		if item.key == key {
			return item.label
		}
	}
	if key == "window" {
		return "窗口与退出"
	}
	return "环境与组件"
}

func (a *App) processingToolbarStatus() string {
	setup := a.firstLaunch.Open && a.firstLaunchStep() == "processing"
	if a.settingsSection != "processing" && !setup {
		return ""
	}
	if a.processing.Loaded && a.processing.currentRuntimeReady() && a.processing.currentConnectionAvailable() {
		return processingSpecLabel(a.processing.Configuration.Current.Revision.Spec) + " · 当前使用"
	}
	return a.processing.currentLabel()
}

func (a *App) settingsSidebar(c *ui.Context) { a.settingsNavigation(c) }

func (a *App) settingsShell(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		switch a.settingsSection {
		case "processing":
			a.processingSettings(c)
		case "agents":
			a.agentSettingsPage(c)
		default:
			a.maintenanceSettings(c)
		}
	})
}

func (a *App) processingSettings(c *ui.Context) {
	s := &a.processing
	s.ensure()
	setup := a.firstLaunch.Open && a.firstLaunchStep() == "processing"
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("processing-content-"+s.Mode)).TrackScroll(a.settingsScrollState("processing-"+s.Mode)).Grow(1).MinHeight(0).Padding(30, 32, 24, 32).Gap(18).Children(func() {
			title := "记忆处理"
			if s.Mode == "provider" {
				title = "自配 LLM"
			}
			if s.Mode == "magpie" {
				title = "Magpie"
			}
			if setup {
				title = "配置记忆处理"
			}
			settingsHeading(c, title, "可配置多种方式，明确选择其中一种使用。")
			current := s.currentLabel()
			if s.currentRuntimeReady() && s.currentConnectionAvailable() {
				current = processingSpecLabel(s.Configuration.Current.Revision.Spec)
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "当前使用").FontSize(design.Small).TextColor(design.Muted)
				ui.Text(c, current).FontSize(design.Small).Bold()
			})
			ui.Row(c).Gap(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				for _, tab := range []struct{ mode, label string }{{"provider", "自配 LLM"}, {"agent", "已连接 Agent"}, {"magpie", "Magpie"}} {
					button := ui.Button(c.Key("processing-tab-"+tab.mode), tab.label).Label(tab.label).Height(42).Padding(10, 18).FontSize(design.Small).BorderWidth(0).Background(ui.Transparent)
					if s.Mode == tab.mode {
						button.BorderWidth(0, 0, 2, 0).BorderColor(ui.Hex("#7891ab"))
					}
					mode := tab.mode
					button.OnClick(func() { s.Mode = mode })
				}
			})
			a.processingModeHeader(c)
			switch s.Mode {
			case "provider":
				a.processingProviderPage(c)
			case "magpie":
				a.processingMagpiePage(c)
			default:
				a.processingAgents(c)
			}
			a.processingValidationFacts(c)
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(design.Small).TextColor(ui.Hex("#a04343")).Selectable()
			}
			if s.ReadError != "" {
				ui.Text(c, s.ReadError).FontSize(design.Small).TextColor(ui.Hex("#a04343")).Selectable()
				quietButton(c, "重新读取配置").Disabled(s.Loading || s.Busy || s.Polling).OnClick(a.refreshProcessing)
			}
			var receipt *records.ProcessingHTTPReceipt
			if saved, ok := s.saved(); ok && s.sameSavedDraft() && saved.Validation != nil {
				receipt = saved.Validation.HTTP
			}
			if s.Progress != nil && s.Progress.HTTP != nil && s.ProgressMode == s.Mode {
				receipt = s.Progress.HTTP
			}
			if receipt != nil {
				a.processingHTTPReceipt(c, *receipt)
			}
			saved, hasSaved := s.saved()
			if hasSaved && s.sameSavedDraft() && saved.Validation != nil && saved.Validation.State == "succeeded" {
				ui.Text(c, "重新验证会先停用正在使用的此方式；验证完成后仍需明确设为当前使用。").FontSize(design.Small).TextColor(design.Muted)
			}
			failed := hasSaved && s.sameSavedDraft() && saved.Validation != nil && (saved.Validation.State == "failed" || saved.Validation.State == "interrupted" || saved.Validation.State == "superseded")
			if s.Notice != "" && s.Error == "" && (a.processingModeState() == "有未保存修改" || failed) {
				ui.Text(c, s.Notice).FontSize(design.Small).TextColor(design.Muted).Selectable()
			}
		})
		ui.Row(c).MinHeight(58).Wrap().Shrink(0).Padding(12, 30).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if setup {
				quietButton(c, "稍后设置").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
				ui.Spacer(c)
			}
			ui.Button(c, "取消修改").Disabled(s.Busy || s.Polling || s.HTTP.CredentialBusy || !a.processingHasDraftChanges()).OnClick(a.cancelProcessingChanges)
			ui.Button(c, "保存配置").Disabled(!s.CanSave()).OnClick(a.saveProcessing)
			if s.CanCancel() {
				ui.Button(c, "取消验证").OnClick(a.cancelProcessingValidation)
			} else {
				label := "验证配置"
				if saved, ok := s.saved(); ok && saved.Validation != nil && saved.Validation.State == "succeeded" {
					label = "重新验证"
				}
				ui.Button(c, label).Disabled(!s.CanValidate()).OnClick(a.validateProcessing)
			}
			if setup {
				firstLaunchPrimary(c, "完成配置").Key("first-launch-complete").Disabled(!a.canCompleteFirstLaunch()).OnClick(a.completeFirstLaunch)
			}
		})
	})
}

func (a *App) processingModeHeader(c *ui.Context) {
	s := &a.processing
	label := map[string]string{"provider": "自配 LLM", "agent": "已连接 Agent", "magpie": "Magpie"}[s.Mode]
	state := a.processingModeState()
	ui.Row(c).MinHeight(48).Padding(10, 12).AlignItems(ui.Center).Gap(10).Background(design.Surface).Border(1, design.Border).Radius(6).Children(func() {
		ui.Text(c, label).Bold()
		ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted)
		ui.Spacer(c)
		ui.Button(c, "设为当前使用").Height(34).Disabled(!s.CanActivate()).OnClick(a.activateProcessing)
	})
}

func (a *App) processingModeState() string {
	s := &a.processing
	if s.Loading {
		return "正在读取真实配置"
	}
	if s.Busy {
		return "正在提交操作"
	}
	if saved, ok := s.saved(); ok && s.sameSavedDraft() {
		if !s.availableDraft() {
			if s.Mode == "agent" {
				return "原连接已变化，请重新选择并保存"
			}
			return "供应商或本机网关配置不完整，请检查并保存"
		}
		if saved.Validation != nil && saved.Validation.State == "succeeded" && !s.runtimeReady(saved.ID) {
			if s.Configuration.Runtime[saved.ID].State == "dependency_changed" {
				return "依赖已变化，请重新验证"
			}
			if s.Configuration.Runtime[saved.ID].State == "unavailable" {
				return "依赖暂不可用，请检查并重新验证"
			}
			return "旧验证已保存，当前依赖待读取"
		}
		if saved.Validation != nil && saved.Validation.State == "validating" {
			if saved.Validation.CancelRequested {
				return "正在取消验证"
			}
			return "正在验证；完成后需明确启用"
		}
		if s.Configuration.Current.Active && s.Configuration.Current.Revision != nil && s.Configuration.Current.Revision.ID == saved.ID && s.currentRuntimeReady() {
			return "当前正在使用"
		}
		if saved.Validation != nil {
			switch saved.Validation.State {
			case "validating":
				if saved.Validation.CancelRequested {
					return "正在取消验证"
				}
				return "正在验证"
			case "succeeded":
				return "已保存并验证，可启用"
			case "failed":
				return "验证失败，可重试"
			case "cancelled":
				return "验证已取消"
			case "interrupted":
				return "验证被服务重启中断"
			case "superseded":
				return "验证已被替代，请重新验证"
			}
		}
		return "已保存，等待验证"
	}
	if s.validDraft() {
		return "有未保存修改"
	}
	return "尚未配置"
}

func (a *App) processingUnavailable(c *ui.Context, title, detail string) {
	ui.Column(c).Margin(16, 0).Padding(18).Gap(8).Border(1, design.Border).Radius(6).Background(ui.Hex("#fafbfd")).Children(func() {
		ui.Text(c, title).Bold()
		ui.Text(c, detail).TextColor(design.Muted)
	})
}

func (a *App) processingAgents(c *ui.Context) {
	s := &a.processing
	if len(s.Candidates) == 0 {
		a.processingUnavailable(c, "没有可用的处理 Agent", "请先在设置 → Agent 连接完成 pi 或 Codex 连接及验证。")
		return
	}
	labels := []string{"请选择已连接 Agent"}
	selected := labels[0]
	candidates := map[string]ProcessingAgentCandidate{}
	for _, candidate := range s.Candidates {
		label := candidate.Name
		if !candidate.Available {
			label += " · 暂不可用"
		}
		if _, duplicate := candidates[label]; duplicate {
			label += " · " + candidate.Connection
		}
		labels = append(labels, label)
		candidates[label] = candidate
		if processingCandidateSelected(s.draft(), candidate) {
			selected = label
		}
	}
	ui.Text(c, "选择已连接 Agent").FontSize(design.Small).TextColor(design.Muted)
	previous := selected
	ui.Select(c.Key("processing-agent-choice"), &selected, labels).Disabled(s.Busy || s.Polling)
	if previous != selected {
		if candidate, ok := candidates[selected]; ok {
			a.selectProcessingAgent(candidate)
		}
	}
	ui.Text(c, "当前模型").FontSize(design.Small).TextColor(design.Muted)
	model := "尚未选择已连接 Agent"
	if draft := s.draft(); draft.Agent == "pi" || draft.Agent == "codex" {
		model = draft.Model
		if model == "" {
			model = "当前模型未报告"
		}
	}
	ui.Text(c, model).Padding(12).Border(1, design.Border).Radius(5).Selectable()
	firstLaunchNotice(c, "模型与账号额度", "使用此 Agent 当前模型与账号额度。")
}

func processingSpecLabel(spec records.ProcessingSpec) string {
	switch spec.Mode {
	case "agent":
		if spec.Agent == "pi" {
			return "pi · " + spec.Provider + " / " + spec.Model
		}
		if spec.Agent == "codex" && spec.Provider != "" {
			return "Codex · " + spec.Provider + " / " + spec.Model
		}
		return strings.TrimSpace(spec.Agent + " · " + spec.Model)
	case "provider":
		return strings.TrimSpace(spec.Provider + " · " + spec.Model)
	case "magpie":
		return "Magpie · " + spec.Model
	default:
		return "尚未设置"
	}
}

func processingCandidateSelected(spec records.ProcessingSpec, candidate ProcessingAgentCandidate) bool {
	return spec.Mode == "agent" && spec.Connection == candidate.Connection && spec.ConnectionGeneration == candidate.Generation && spec.Provider == candidate.Provider && spec.Model == candidate.Model
}

func (a *App) selectProcessingAgent(candidate ProcessingAgentCandidate) {
	if !candidate.Available || (candidate.Agent != "pi" && candidate.Agent != "codex") {
		return
	}
	a.processing.ensure()
	a.processing.Drafts["agent"] = records.ProcessingSpec{Mode: "agent", Agent: candidate.Agent, Provider: candidate.Provider, Model: candidate.Model, Connection: candidate.Connection, ConnectionGeneration: candidate.Generation}
	a.processing.Error = ""
	a.processing.Notice = "已选择真实连接；保存不会自动启用。"
}

func (a *App) processingHasDraftChanges() bool {
	s := &a.processing
	if s.Mode == "magpie" && s.HTTP.GatewayTouched {
		endpoint := "http://127.0.0.1:3425/v1"
		if saved, ok := s.saved(); ok {
			endpoint = saved.Spec.Endpoint
		}
		if s.HTTP.GatewayEndpoint != endpoint {
			return true
		}
	}
	if s.Mode == "provider" {
		h := &s.HTTP
		if h.Secret != "" {
			return true
		}
		if h.Editing != "" {
			profile, ok := h.Profiles[h.Editing]
			if !ok || profile.Endpoint != h.Endpoint || profile.Protocol != h.Protocol {
				return true
			}
		}
	}
	draft := s.draft()
	return draft.Mode != "" && !s.sameSavedDraft()
}

func (a *App) cancelProcessingChanges() {
	s := &a.processing
	s.ensure()
	if saved, ok := s.saved(); ok {
		s.Drafts[s.Mode] = saved.Spec
	} else {
		delete(s.Drafts, s.Mode)
	}
	s.ensureHTTP()
	if s.Mode == "magpie" {
		s.HTTP.GatewayEndpoint = "http://127.0.0.1:3425/v1"
		if saved, ok := s.saved(); ok {
			s.HTTP.GatewayEndpoint = saved.Spec.Endpoint
		}
		s.HTTP.GatewaySettings = false
		s.HTTP.GatewayTouched = false
	} else if s.Mode == "provider" {
		s.HTTP.Secret = ""
		s.HTTP.CredentialAttempt = nil
		s.HTTP.CredentialAttemptDigest = ""
		s.HTTP.View = ""
		s.HTTP.Editing = ""
		s.HTTP.Endpoint, s.HTTP.Protocol = "", ""
		s.HTTP.SettingsDrafts = nil
	}
	s.Error = ""
	s.Notice = "已恢复此方式上次保存的配置；当前使用未改变。"
}

func processingCandidates(ctx context.Context, clientRoot string, connections []records.Connection) []ProcessingAgentCandidate {
	out := []ProcessingAgentCandidate{}
	for _, connection := range connections {
		if !connection.Enabled || (connection.Agent != "pi" && connection.Agent != "codex") {
			continue
		}
		candidate := ProcessingAgentCandidate{Agent: connection.Agent, Name: connection.Name, Connection: connection.ID, Generation: connection.Generation}
		if candidate.Name == "" {
			candidate.Name = connection.Agent
		}
		if !filepath.IsAbs(clientRoot) {
			candidate.Reason = "需先完成 Agent 连接"
			out = append(out, candidate)
			continue
		}
		installation, err := agents.LoadInstallation(clientRoot, connection.ID)
		if err == nil {
			candidate.Version = installation.AgentVersion
		}
		if err != nil || installation.Connection.ID != connection.ID || installation.Connection.Generation != connection.Generation {
			candidate.Reason = "需先完成 Agent 连接"
			out = append(out, candidate)
			continue
		}
		if _, verified := agents.ConnectionVerification(installation, connection.Generation); !verified {
			candidate.Reason = "需先完成真实连接验证"
			out = append(out, candidate)
			continue
		}
		if err = agents.CheckInstallation(installation); err != nil {
			candidate.Reason = "Agent 连接已变化，请重新验证"
			out = append(out, candidate)
			continue
		}
		if connection.Agent == "codex" {
			selected, err := agents.CodexCompletionSelection(ctx, installation, installation.Options.Bridge)
			if err != nil {
				candidate.Reason = "无法确认 Codex 当前账号与模型，请在 Codex 中配置后重新检查"
				if agents.CodexCompletionErrorCode(err) == "codex_profile_unsupported" {
					candidate.Reason = "当前 Codex 模型档案尚不支持处理记忆"
				}
				out = append(out, candidate)
				continue
			}
			candidate.Provider, candidate.Model, candidate.Version = selected.Provider, selected.Model, selected.Version
			candidate.Available = true
			candidate.Reason = selected.Provider + " / " + selected.Model
			out = append(out, candidate)
			continue
		}
		if installation.AgentVersion != "0.84.1" {
			candidate.Reason = "当前 pi 版本尚未通过处理桥验证"
			out = append(out, candidate)
			continue
		}
		var selected struct {
			Provider string `json:"defaultProvider"`
			Model    string `json:"defaultModel"`
		}
		data, readErr := os.ReadFile(filepath.Join(installation.Options.PiDirectory, "settings.json"))
		if readErr != nil || len(data) > 65536 || json.Unmarshal(data, &selected) != nil || selected.Provider == "" || selected.Model == "" {
			candidate.Reason = "无法读取 pi 当前模型"
			out = append(out, candidate)
			continue
		}
		candidate.Provider, candidate.Model = selected.Provider, selected.Model
		if selected.Provider != "deepseek" && selected.Provider != "ollama" {
			candidate.Reason = "当前模型尚未通过处理桥验证"
			out = append(out, candidate)
			continue
		}
		candidate.Available = true
		candidate.Reason = selected.Provider + " / " + selected.Model
		out = append(out, candidate)
	}
	return out
}

func (a *App) refreshProcessing() {
	a.readProcessing(false)
}

// Routine observations must not repeatedly turn a usable form into a blocking
// loading screen or launch an Agent model/account probe every two seconds.
func (a *App) pollProcessing() {
	if !a.processing.CheckedAt.IsZero() && time.Since(a.processing.CheckedAt) < 15*time.Second {
		return
	}
	a.readProcessing(a.processing.Loaded)
}

func (a *App) readProcessing(background bool) {
	s := &a.processing
	s.ensure()
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || s.Refreshing || s.Busy || s.Polling {
		return
	}
	s.Loading, s.Refreshing = !background, true
	s.Sequence++
	sequence := s.Sequence
	s.RefreshSequence = sequence
	clientRoot := a.agents.Root
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, processingRuntimeMetadataTimeout)
		defer cancel()
		configuration, err := library.ProcessingConfiguration(ctx)
		var connections []records.Connection
		if err == nil {
			connections, err = library.Connections(ctx)
		}
		candidates := processingCandidates(ctx, clientRoot, connections)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.library.Root == library.Root && s.RefreshSequence == sequence {
				s.Loading, s.Refreshing = false, false
			}
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.CheckedAt = time.Now()
			if err != nil {
				s.ReadError = "读取记忆处理配置失败；请重新读取。"
				return
			}
			s.Configuration = configuration
			if !s.HTTP.GatewayTouched {
				if saved, ok := configuration.Saved["magpie"]; ok {
					s.HTTP.GatewayEndpoint = saved.Spec.Endpoint
				}
			}
			s.Loaded = true
			s.Candidates = candidates
			s.CandidatesLoaded = true
			for mode, saved := range configuration.Saved {
				if _, exists := s.Drafts[mode]; !exists {
					s.Drafts[mode] = saved.Spec
				}
			}
			if saved, ok := s.saved(); ok && s.sameSavedDraft() && saved.Validation != nil && s.Notice == "" && (saved.Validation.State == "failed" || saved.Validation.State == "interrupted" || saved.Validation.State == "superseded") {
				s.Notice = processingValidationNotice(*saved.Validation)
			}
			s.ReadError = ""
		})
	}()
}

// A terminal probe refreshes the owner observation and selection generation;
// it does not rescan Agent installations or enable the saved revision.
func (a *App) refreshProcessingState() {
	s := &a.processing
	if a.libraryContext == nil || a.dispatch == nil || s.Loading || s.Busy || s.Polling {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence := s.Sequence
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, processingRuntimeMetadataTimeout)
		defer cancel()
		config, err := library.ProcessingConfiguration(ctx)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.Loading = false
			s.CheckedAt = time.Now()
			if err != nil {
				s.ReadError = "读取最新验证与启用状态失败；请重新读取后再启用。"
				return
			}
			s.Configuration = config
			s.Loaded = true
			s.ReadError = ""
		})
	}()
}

func (a *App) saveProcessing() {
	s := &a.processing
	if a.libraryContext == nil || a.dispatch == nil || !s.CanSave() {
		return
	}
	draft := s.draft()
	expected := ""
	if saved, ok := s.saved(); ok {
		expected = saved.ID
	}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	owner, library := a.libraryContext, a.library
	s.Error, s.Notice = "", "正在保存配置…"
	go func() {
		ctx, cancel := context.WithTimeout(owner, 10*time.Second)
		defer cancel()
		revision, err := library.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: expected, Spec: draft})
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "保存配置失败：" + processingActionError(err, draft.Mode)
				s.Notice = ""
				return
			}
			s.Configuration.Saved[draft.Mode] = revision
			s.Drafts[draft.Mode] = revision.Spec
			if s.ProgressMode == draft.Mode {
				s.Progress = nil
				s.ProgressMode = ""
				s.ValidationID = ""
			}
			s.Notice = "配置已保存；还需完成真实验证后才能设为当前使用。"
		})
	}()
}

func (a *App) validateProcessing() {
	s := &a.processing
	saved, ok := s.saved()
	if !ok || a.libraryContext == nil || a.dispatch == nil || !s.CanValidate() {
		return
	}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	owner, library := a.libraryContext, a.library
	mode := s.Mode
	s.Error, s.Notice = "", "正在启动真实模型验证…"
	go func() {
		ctx, cancel := context.WithTimeout(owner, processingValidationBeginTimeout)
		defer cancel()
		validation, err := library.ValidateProcessing(ctx, records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: saved.ID})
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "启动验证失败：" + processingActionError(err, mode)
				s.Notice = ""
				return
			}
			revision := s.Configuration.Saved[mode]
			revision.Validation = &validation
			if s.Configuration.Runtime == nil {
				s.Configuration.Runtime = map[string]records.ProcessingReadiness{}
			}
			s.Configuration.Runtime[revision.ID] = records.ProcessingReadiness{State: "verification_required"}
			s.Configuration.Saved[mode] = revision
			s.ValidationID = validation.ID
			s.ProgressMode = mode
			s.Progress = nil
			s.Polling = true
			s.Notice = "正在验证当前模型的完整结构化输出…"
			a.pollProcessingValidationMode(validation.ID, revision.ID, mode)
		})
	}()
}

func (a *App) pollProcessingValidation(id, revisionID string) {
	a.pollProcessingValidationMode(id, revisionID, "agent")
}

func (a *App) pollProcessingValidationMode(id, revisionID, mode string) {
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, 130*time.Second)
		defer cancel()
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			status, err := library.ProcessingValidation(ctx, id)
			if err == nil {
				terminal := status.Validation.State != "validating"
				a.dispatch(func() {
					s := &a.processing
					if s.ValidationID != id || a.library.Root != library.Root {
						return
					}
					revision := s.Configuration.Saved[mode]
					if revision.ID != revisionID {
						return
					}
					revision.Validation = &status.Validation
					s.Configuration.Saved[mode] = revision
					s.Progress = status.Progress
					if terminal {
						s.Polling = false
						s.Notice = processingValidationNotice(status.Validation)
						a.refreshProcessingState()
					}
				})
				if terminal {
					return
				}
			}
			select {
			case <-ctx.Done():
				if owner.Err() == nil {
					a.dispatch(func() {
						if a.processing.ValidationID == id && a.processing.Polling {
							a.processing.Polling = false
							a.processing.Error = "验证状态读取超时；服务端任务不会因此被标记成功。"
						}
					})
				}
				return
			case <-ticker.C:
			}
		}
	}()
}

func processingValidationNotice(validation records.ProcessingValidation) string {
	switch validation.State {
	case "succeeded":
		return "真实验证通过；仍需明确点击“设为当前使用”。"
	case "cancelled":
		return "验证已取消；保存的配置保留，未自动启用。"
	case "interrupted":
		return "验证被服务重启中断；保存的配置保留，未自动启用。"
	default:
		if validation.ErrorCode != "" {
			if validation.Diagnostics != nil {
				stage := map[string]string{"queue": "排队检查", "preflight": "验证前检查", "model": "模型调用", "postflight": "验证后检查"}[validation.Diagnostics.Stage]
				names := []string{}
				labels := map[string]string{"settings.json": "Agent 设置", "models.json": "模型配置", "auth.json": "账号配置", "adapter_protocol": "连接协议", "processing_configuration": "处理配置", "agent_version": "Agent 版本", "agent_executable": "Agent 程序", "agent_configuration_directory": "Agent 配置位置", "helper_executable": "后台程序", "extension": "连接扩展", "connection_verification": "连接验证", "independent_credential": "供应商凭据", "gateway_model": "网关模型目录"}
				for _, name := range validation.Diagnostics.ChangedNames {
					if label := labels[name]; label != "" {
						names = append(names, label)
					}
				}
				if len(names) > 0 {
					return stage + "未通过：" + strings.Join(names, "、") + "发生变化；保存的配置保留，未自动启用。"
				}
				if validation.Diagnostics.ResolveCode != "" {
					return stage + "未通过：连接或模型依赖需要重新核对（" + safeProcessingCode(validation.Diagnostics.ResolveCode) + "）；保存的配置保留，未自动启用。"
				}
			}
			return "验证未通过（" + safeProcessingCode(validation.ErrorCode) + "）；保存的配置保留，未自动启用。"
		}
		return "验证未通过；保存的配置保留，未自动启用。"
	}
}

func (a *App) cancelProcessingValidation() {
	s := &a.processing
	saved, ok := s.saved()
	if !ok || saved.Validation == nil || a.libraryContext == nil || a.dispatch == nil || !s.CanCancel() {
		return
	}
	id := saved.Validation.ID
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	owner, library := a.libraryContext, a.library
	mode := s.Mode
	s.Error, s.Notice = "", "正在请求取消验证…"
	go func() {
		ctx, cancel := context.WithTimeout(owner, 10*time.Second)
		defer cancel()
		err := library.CancelProcessingValidation(ctx, id)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "取消验证失败：" + processingActionError(err, mode)
				s.Notice = ""
				return
			}
			revision := s.Configuration.Saved[mode]
			if revision.Validation != nil && revision.Validation.ID == id {
				copy := *revision.Validation
				copy.CancelRequested = true
				revision.Validation = &copy
				s.Configuration.Saved[mode] = revision
			}
			s.Notice = "取消请求已送达；等待 daemon 确认进程终态。"
		})
	}()
}

func (a *App) activateProcessing() {
	s := &a.processing
	saved, ok := s.saved()
	if !ok || a.libraryContext == nil || a.dispatch == nil || !s.CanActivate() {
		return
	}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	owner, library := a.libraryContext, a.library
	s.Error, s.Notice = "", "正在设为当前使用…"
	expected := s.Configuration.Current.Generation
	go func() {
		ctx, cancel := context.WithTimeout(owner, processingActivationRoundTripTimeout)
		defer cancel()
		_, err := library.ActivateProcessing(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: saved.ID, ExpectedGeneration: expected})
		var configuration records.ProcessingConfiguration
		if err == nil {
			configuration, err = library.ProcessingConfiguration(ctx)
		}
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "启用配置失败：" + processingActionError(err, saved.Spec.Mode)
				s.Notice = ""
				return
			}
			s.Configuration = configuration
			s.Loaded = true
			s.CheckedAt = time.Now()
			s.ReadError = ""
			s.Notice = "已设为当前使用；只影响之后启动的记忆处理任务。"
		})
	}()
}

// Individual outcomes need their own evidence; aggregate success is not a check receipt.
func (a *App) processingValidationFacts(c *ui.Context) {
	s := &a.processing
	statuses := [3]string{"未取得验证结果", "未取得验证结果", "未知 · 未取得隔离验证结果"}
	if saved, ok := s.saved(); ok && s.sameSavedDraft() && saved.Validation != nil && saved.Validation.Revision == saved.ID {
		validation := saved.Validation
		evidence := validation.Evidence
		if saved.Spec.Mode == "agent" && evidence != nil && validation.ID != "" && validation.Attempt > 0 && validation.DependencyDigest != "" &&
			evidence.ValidationID == validation.ID && evidence.Revision == saved.ID && evidence.Attempt == validation.Attempt &&
			evidence.DependencyDigest == validation.DependencyDigest && evidence.Agent == saved.Spec.Agent {
			if evidence.ResponseComplete {
				statuses[0] = "已收到模型响应"
			}
			if evidence.ResponseComplete && evidence.StructureValid {
				statuses[1] = "结构校验通过"
			}
			policy := map[string]string{"pi": "pi_private_no_session_no_tools_v1", "codex": "codex_private_ephemeral_no_tools_v1"}[evidence.Agent]
			if evidence.IsolationConfigured && policy != "" && evidence.IsolationPolicy == policy {
				statuses[2] = "本次本机隔离已配置"
			}
		}
		r := saved.Validation.HTTP
		if r != nil && r.RequestedModel == saved.Spec.Model {
			if r.RequestSent && r.ResponseComplete && r.Status >= 200 && r.Status < 300 {
				statuses[0] = "已收到模型响应"
			}
			if r.NativeSchemaRequested && r.ResponseComplete && r.SchemaValid {
				statuses[1] = "结构校验通过"
			}
		}
	}
	ui.Column(c).Key("processing-validation-facts").Gap(8).Children(func() {
		ui.Text(c, "整理能力验证").FontSize(12).Bold()
		labels := []string{"模型请求与响应", "结构化提炼输出", "本次本机隔离"}
		for i, label := range labels {
			ui.Row(c).Gap(12).Padding(10, 0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				ui.Text(c, label).FontSize(design.Small).Grow(1).MinWidth(0)
				ui.Text(c, statuses[i]).Key(fmt.Sprintf("processing-check-%d", i)).FontSize(design.Small).TextColor(design.Muted)
			})
		}
	})
}

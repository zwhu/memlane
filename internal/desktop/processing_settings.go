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

type ProcessingAgentCandidate struct {
	Agent, Name, Version, Connection, Provider, Model, Reason string
	Generation                                                int64
	Available                                                 bool
}

// ProcessingUI is presentation state for the service-owned processing ledger.
// It does not infer success from a selected card or from a completed IPC call.
type ProcessingUI struct {
	Mode             string
	Drafts           map[string]records.ProcessingSpec
	Configuration    records.ProcessingConfiguration
	Candidates       []ProcessingAgentCandidate
	CandidatesLoaded bool
	Progress         *records.ProcessingValidationProgress
	ValidationID     string
	Notice, Error    string
	Sequence         uint64
	Loading          bool
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
	return draft.Mode == "agent" && draft.Agent == "pi" && draft.Connection != "" && draft.ConnectionGeneration > 0 && draft.Provider != "" && draft.Model != ""
}

func (s *ProcessingUI) availableDraft() bool {
	if !s.CandidatesLoaded {
		return true
	}
	draft := s.draft()
	for _, candidate := range s.Candidates {
		if candidate.Available && processingCandidateSelected(draft, candidate) {
			return true
		}
	}
	return false
}

func (s *ProcessingUI) CanSave() bool {
	return !s.Loading && !s.Busy && !s.Polling && s.validDraft() && s.availableDraft() && !s.sameSavedDraft()
}

func (s *ProcessingUI) CanValidate() bool {
	saved, ok := s.saved()
	if !ok || s.Loading || s.Busy || s.Polling || !s.sameSavedDraft() || !s.availableDraft() {
		return false
	}
	return saved.Validation == nil || saved.Validation.State == "failed" || saved.Validation.State == "cancelled" || saved.Validation.State == "interrupted" || saved.Validation.State == "superseded"
}

func (s *ProcessingUI) CanCancel() bool {
	saved, ok := s.saved()
	return ok && !s.Busy && saved.Validation != nil && saved.Validation.State == "validating" && !saved.Validation.CancelRequested
}

func (s *ProcessingUI) CanActivate() bool {
	saved, ok := s.saved()
	if !ok || s.Loading || s.Busy || s.Polling || !s.sameSavedDraft() || !s.availableDraft() || saved.Validation == nil || saved.Validation.State != "succeeded" || saved.Validation.CancelRequested {
		return false
	}
	current := s.Configuration.Current
	return !current.Active || current.Revision == nil || current.Revision.ID != saved.ID
}

func (a *App) settingsSectionLabel() string {
	switch a.settingsSection {
	case "agents":
		return "Agent 连接"
	case "processing":
		return "记忆处理"
	default:
		return "环境与组件"
	}
}

func (a *App) processingToolbarStatus() string {
	setup := a.firstLaunch.Open && a.firstLaunchStep() == "processing"
	if a.settingsSection != "processing" && !setup {
		return ""
	}
	current := a.processing.Configuration.Current
	if !current.Active || current.Revision == nil {
		return "记忆处理待配置"
	}
	return processingSpecLabel(current.Revision.Spec) + " · 当前使用"
}

func (a *App) settingsSidebar(c *ui.Context) {
	ui.Column(c).Key("settings-navigation").Width(196).Shrink(0).Background(design.SidebarFill).Children(func() {
		ui.Row(c).Height(design.Toolbar).Shrink(0).DragWindow()
		ui.Row(c).Padding(28, 28, 20, 28).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons["brand"]).FontSize(24).TextColor(design.Ink)
			ui.Text(c, "Memlane").FontSize(18).Bold()
		})
		ui.Text(c, "设置").FontSize(design.Small).TextColor(design.Muted).Padding(20, 28, 14, 28)
		ui.Scroll(c).Grow(1).Padding(0, 12).Gap(6).Children(func() {
			for _, item := range []struct{ key, label string }{{"environment", "环境与组件"}, {"agents", "Agent 连接"}, {"processing", "记忆处理"}} {
				button := ui.Button(c.Key("settings-"+item.key), item.label).Label(item.label).Height(36).Padding(8, 12).Justify(ui.Start).BorderWidth(0).Background(ui.Transparent)
				if a.settingsSection == item.key {
					button.Background(design.Selected)
				} else if button.Hovered() {
					button.Background(ui.Hex("#eef3f8"))
				}
				key := item.key
				button.OnClick(func() {
					a.settingsSection = key
					if key == "environment" {
						a.ensureEnvironment()
					}
					if key == "agents" {
						a.refreshAgents(true)
					}
					if key == "processing" {
						a.refreshProcessing()
					}
				})
			}
		})
		quietButton(c, "返回日常首页").Margin(20, 28).OnClick(func() { a.navigate("总览", "") })
	})
}

func (a *App) settingsShell(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		switch a.settingsSection {
		case "processing":
			a.processingSettings(c)
		case "agents":
			a.agentSettingsPage(c)
		default:
			a.environmentSettings(c)
		}
	})
}

func (a *App) processingSettings(c *ui.Context) {
	s := &a.processing
	s.ensure()
	setup := a.firstLaunch.Open && a.firstLaunchStep() == "processing"
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Grow(1).Padding(28, 30, 24, 30).Gap(18).Children(func() {
			title := "记忆处理"
			if setup {
				title = "配置记忆处理"
			}
			ui.Text(c, title).FontSize(design.Heading).Bold()
			ui.Divider(c)
			current := "尚未设置"
			if s.Configuration.Current.Active && s.Configuration.Current.Revision != nil {
				current = processingSpecLabel(s.Configuration.Current.Revision.Spec)
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "当前使用").FontSize(design.Small).TextColor(design.Muted)
				ui.Text(c, current).FontSize(design.Small).Bold()
			})
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(3).Padding(3).Radius(5).Background(ui.Hex("#edf2f7")).Children(func() {
					for _, tab := range []struct{ mode, label string }{{"provider", "LLM provider"}, {"agent", "使用已连接 Agent"}, {"magpie", "使用 Magpie"}} {
						button := ui.Button(c.Key("processing-tab-"+tab.mode), tab.label).Label(tab.label).Height(32).Padding(6, 12).FontSize(design.Small).BorderWidth(0).Background(ui.Transparent)
						if s.Mode == tab.mode {
							button.Background(ui.Hex("#ffffff"))
						}
						mode := tab.mode
						button.OnClick(func() { s.Mode = mode; s.Error = ""; s.Notice = "" })
					}
				})
				ui.Text(c, "可配置多种方式，选择一种用于记忆处理。").FontSize(design.Small).TextColor(design.Muted).Grow(1).MinWidth(0)
			})
			a.processingModeHeader(c)
			switch s.Mode {
			case "provider":
				a.processingUnavailable(c, "LLM provider 暂不可用", "供应商配置和模型验证尚未开放。请先使用已连接的 pi。")
			case "magpie":
				a.processingUnavailable(c, "Magpie 暂不可用", "Magpie 接入尚未开放。请先使用已连接的 pi。")
			default:
				a.processingAgents(c)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(design.Small).TextColor(ui.Hex("#a04343")).Selectable()
			}
			saved, hasSaved := s.saved()
			failed := hasSaved && s.sameSavedDraft() && saved.Validation != nil && (saved.Validation.State == "failed" || saved.Validation.State == "interrupted")
			if s.Notice != "" && s.Error == "" && (a.processingModeState() == "有未保存修改" || failed) {
				ui.Text(c, s.Notice).FontSize(design.Small).TextColor(design.Muted).Selectable()
			}
		})
		ui.Row(c).Height(58).Shrink(0).Padding(12, 30).Gap(10).Justify(ui.End).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if setup {
				quietButton(c, "稍后设置").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
				ui.Spacer(c)
			}
			ui.Button(c, "取消修改").Disabled(s.Busy || s.Polling || !a.processingHasDraftChanges()).OnClick(a.cancelProcessingChanges)
			ui.Button(c, "保存配置").Disabled(!s.CanSave()).OnClick(a.saveProcessing)
			if s.CanCancel() {
				ui.Button(c, "取消验证").OnClick(a.cancelProcessingValidation)
			} else {
				ui.Button(c, "验证配置").Disabled(!s.CanValidate()).OnClick(a.validateProcessing)
			}
			if setup {
				firstLaunchPrimary(c, "完成配置").Key("first-launch-complete").Disabled(!a.canCompleteFirstLaunch()).OnClick(a.completeFirstLaunch)
			}
		})
	})
}

func (a *App) processingModeHeader(c *ui.Context) {
	s := &a.processing
	label := map[string]string{"provider": "LLM provider", "agent": "已连接 Agent", "magpie": "Magpie"}[s.Mode]
	state := a.processingModeState()
	ui.Row(c).Height(54).AlignItems(ui.Center).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
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
		if s.Configuration.Current.Active && s.Configuration.Current.Revision != nil && s.Configuration.Current.Revision.ID == saved.ID {
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
		a.processingUnavailable(c, "没有可用的处理 Agent", "请先在“设置 → Agent 连接”完成 pi 连接及真实读写验证。")
		return
	}
	// Keep the two-column geometry from the approved design. A single real
	// candidate occupies one column; unavailable candidates are never invented.
	width, _ := c.Size()
	available := width - 196 - 60
	if a.firstLaunch.Open {
		available = width - 196 - 154 - 60
	}
	cardWidth := (available - 14) / 2
	for start := 0; start < len(s.Candidates); start += 2 {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			for index := start; index < min(start+2, len(s.Candidates)); index++ {
				candidate := s.Candidates[index]
				selected := processingCandidateSelected(s.draft(), candidate)
				button := ui.Button(c.Key("processing-agent-"+candidate.Connection), "").Label(candidate.Name).Width(cardWidth).Height(108).Padding(18).Border(1, ui.Hex("#d8e2ed")).Background(ui.Hex("#ffffff")).Justify(ui.Start).AlignItems(ui.Start).Disabled(!candidate.Available).Children(func() {
					ui.Column(c).Grow(1).Gap(8).AlignItems(ui.Start).Children(func() {
						ui.Row(c).FillWidth().Children(func() {
							icon := icons[candidate.Agent]
							if icon == nil {
								icon = icons["Agent"]
							}
							ui.Icon(c, icon).FontSize(26).TextColor(design.Muted).Margin(0, 10, 0, 0)
							ui.Text(c, candidate.Name).Bold()
							ui.Spacer(c)
							if selected {
								ui.Text(c, "●").TextColor(ui.Hex("#688db9"))
							} else {
								ui.Text(c, "○").TextColor(design.Muted)
							}
						})
						version := candidate.Version
						if version == "" {
							version = "版本待确认"
						}
						ui.Row(c).FillWidth().Padding(8, 0, 0, 36).Children(func() {
							ui.Text(c, version).FontSize(10).TextColor(design.Muted)
							ui.Spacer(c)
							state := "已连接"
							if !candidate.Available {
								state = "暂不可用"
							}
							ui.Text(c, state).FontSize(10).TextColor(design.Muted)
						})
						if !candidate.Available {
							ui.Text(c, "当前连接尚不能用于记忆处理").FontSize(10).TextColor(design.Muted)
						}
					})
				})
				if selected {
					button.Background(ui.Hex("#eef4fb")).Border(1, ui.Hex("#8caac9"))
				}
				candidateCopy := candidate
				button.OnClick(func() { a.selectProcessingAgent(candidateCopy) })
			}
		})
	}
	if draft := s.draft(); draft.Agent == "pi" {
		ui.Text(c, fmt.Sprintf("使用 pi 当前模型 %s / %s 处理记忆，会消耗该账号的模型额度。", draft.Provider, draft.Model)).FontSize(design.Small).TextColor(design.Muted).Padding(8, 0)
	}
}

func processingSpecLabel(spec records.ProcessingSpec) string {
	switch spec.Mode {
	case "agent":
		if spec.Agent == "pi" {
			return "pi · " + spec.Provider + " / " + spec.Model
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
	if !candidate.Available || candidate.Agent != "pi" {
		return
	}
	a.processing.ensure()
	a.processing.Drafts["agent"] = records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: candidate.Provider, Model: candidate.Model, Connection: candidate.Connection, ConnectionGeneration: candidate.Generation}
	a.processing.Error = ""
	a.processing.Notice = "已选择真实连接；保存不会自动启用。"
}

func (a *App) processingHasDraftChanges() bool {
	draft := a.processing.draft()
	if draft.Mode == "" {
		return false
	}
	return !a.processing.sameSavedDraft()
}

func (a *App) cancelProcessingChanges() {
	s := &a.processing
	s.ensure()
	if saved, ok := s.saved(); ok {
		s.Drafts[s.Mode] = saved.Spec
	} else {
		delete(s.Drafts, s.Mode)
	}
	s.Error = ""
	s.Notice = "已恢复此方式上次保存的配置；当前使用未改变。"
}

func processingCandidates(clientRoot string, connections []records.Connection) []ProcessingAgentCandidate {
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
		if connection.Agent == "codex" {
			candidate.Reason = "Codex 处理桥尚未接入"
			out = append(out, candidate)
			continue
		}
		if err != nil || installation.Connection.ID != connection.ID || installation.Connection.Generation != connection.Generation {
			candidate.Reason = "需先完成 Agent 连接"
			out = append(out, candidate)
			continue
		}
		if installation.VerifiedGeneration != connection.Generation || installation.VerifiedAt == "" || installation.VerificationError != "" {
			candidate.Reason = "需先完成真实连接验证"
			out = append(out, candidate)
			continue
		}
		if err = agents.CheckInstallation(installation); err != nil {
			candidate.Reason = "Agent 连接已变化，请重新验证"
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
	s := &a.processing
	s.ensure()
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || s.Busy || s.Polling {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence := s.Sequence
	clientRoot := a.agents.Root
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		configuration, err := a.library.ProcessingConfiguration(ctx)
		var connections []records.Connection
		if err == nil {
			connections, err = a.library.Connections(ctx)
		}
		candidates := processingCandidates(clientRoot, connections)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "读取记忆处理配置失败：" + err.Error()
				return
			}
			s.Configuration = configuration
			s.Candidates = candidates
			s.CandidatesLoaded = true
			for mode, saved := range configuration.Saved {
				if _, exists := s.Drafts[mode]; !exists {
					s.Drafts[mode] = saved.Spec
				}
			}
			s.Error = ""
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
	s.Error, s.Notice = "", "正在保存配置…"
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer cancel()
		revision, err := a.library.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: expected, Spec: draft})
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = "保存配置失败：" + err.Error()
				s.Notice = ""
				return
			}
			s.Configuration.Saved[draft.Mode] = revision
			s.Drafts[draft.Mode] = revision.Spec
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
	s.Error, s.Notice = "", "正在启动真实 pi 验证…"
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer cancel()
		validation, err := a.library.ValidateProcessing(ctx, records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: saved.ID})
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = "启动验证失败：" + err.Error()
				s.Notice = ""
				return
			}
			revision := s.Configuration.Saved[s.Mode]
			revision.Validation = &validation
			s.Configuration.Saved[s.Mode] = revision
			s.ValidationID = validation.ID
			s.Polling = true
			s.Notice = "正在验证当前模型、完整结构化输出与进程回收…"
			a.pollProcessingValidation(validation.ID, revision.ID)
		})
	}()
}

func (a *App) pollProcessingValidation(id, revisionID string) {
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 130*time.Second)
		defer cancel()
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			status, err := a.library.ProcessingValidation(ctx, id)
			if err == nil {
				terminal := status.Validation.State != "validating"
				a.dispatch(func() {
					s := &a.processing
					if s.ValidationID != id {
						return
					}
					revision := s.Configuration.Saved["agent"]
					if revision.ID != revisionID {
						return
					}
					revision.Validation = &status.Validation
					s.Configuration.Saved["agent"] = revision
					s.Progress = status.Progress
					if terminal {
						s.Polling = false
						s.Notice = processingValidationNotice(status.Validation)
					}
				})
				if terminal {
					return
				}
			}
			select {
			case <-ctx.Done():
				if a.libraryContext.Err() == nil {
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
		return "验证已取消；当前使用配置未改变。"
	case "interrupted":
		return "验证被服务重启中断；当前使用配置未改变。"
	default:
		if validation.ErrorCode != "" {
			return "验证未通过（" + validation.ErrorCode + "）；当前使用配置未改变。"
		}
		return "验证未通过；当前使用配置未改变。"
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
	s.Error, s.Notice = "", "正在请求取消验证…"
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer cancel()
		err := a.library.CancelProcessingValidation(ctx, id)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = "取消验证失败：" + err.Error()
				s.Notice = ""
				return
			}
			revision := s.Configuration.Saved[s.Mode]
			if revision.Validation != nil && revision.Validation.ID == id {
				copy := *revision.Validation
				copy.CancelRequested = true
				revision.Validation = &copy
				s.Configuration.Saved[s.Mode] = revision
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
	s.Error, s.Notice = "", "正在设为当前使用…"
	expected := s.Configuration.Current.Generation
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer cancel()
		_, err := a.library.ActivateProcessing(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: saved.ID, ExpectedGeneration: expected})
		var configuration records.ProcessingConfiguration
		if err == nil {
			configuration, err = a.library.ProcessingConfiguration(ctx)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = "启用配置失败：" + err.Error()
				s.Notice = ""
				return
			}
			s.Configuration = configuration
			s.Notice = "已设为当前使用；只影响之后启动的记忆处理任务。"
		})
	}()
}

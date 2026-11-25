package simpledesktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

type generationEditorDraft struct {
	Profile                                                              GenerationProfile
	Model, Endpoint, ProviderLabel, APIKey, Temperature, MaxOutputTokens string
	Saved                                                                *GenerationRef
	SavedSignature, SaveOperation, ValidationOperation                   string
	CredentialConfigured                                                 bool
	ValidationStartedAt                                                  time.Time
	Validation                                                           GenerationValidation
	Error, Message                                                       string
}

func (s *integrationState) rememberGenerationDraft() {
	if !s.Editor || s.ProfileID == "" {
		return
	}
	if s.GenerationDrafts == nil {
		s.GenerationDrafts = map[string]generationEditorDraft{}
	}
	if s.GenerationModeSelection == nil {
		s.GenerationModeSelection = map[string]string{}
	}
	s.GenerationDrafts[s.ProfileID] = generationEditorDraft{
		Profile: s.draftProfile(), Model: s.Model, Endpoint: s.Endpoint, ProviderLabel: s.ProviderLabel,
		APIKey: s.APIKey, Temperature: s.Temperature, MaxOutputTokens: s.MaxOutputTokens,
		Saved: s.Saved, SavedSignature: s.SavedSignature, SaveOperation: s.SaveOperation,
		ValidationOperation: s.ValidationOperation, ValidationStartedAt: s.ValidationStartedAt,
		CredentialConfigured: s.CredentialConfigured, Validation: s.Validation, Error: s.Error, Message: s.Message,
	}
	s.GenerationModeSelection[s.DraftMode] = s.ProfileID
}
func (a *App) selectGenerationProfile(id string, row GenerationRow) {
	s := &a.integrations
	s.GenerationAgentApply = false
	if s.Editor && s.ProfileID == id {
		return
	}
	s.rememberGenerationDraft()
	s.HandoffOpen = false
	s.HandoffRef = nil
	s.HandoffRequest++
	s.editProfile(id, row)
	s.Error, s.Message = "", ""
	if row.Revision == 0 {
		s.Saved = nil
		s.SavedSignature = ""
	}
	if d, ok := s.GenerationDrafts[id]; ok {
		s.DraftMode = d.Profile.Mode
		s.BridgeKind, s.BridgeProvider = d.Profile.BridgeKind, d.Profile.BridgeProvider
		s.Model, s.Endpoint, s.ProviderLabel, s.APIKey = d.Model, d.Endpoint, d.ProviderLabel, d.APIKey
		s.Temperature, s.MaxOutputTokens = d.Temperature, d.MaxOutputTokens
		s.CredentialVersion = d.Profile.CredentialVersion
		s.CredentialConfigured = d.CredentialConfigured
		s.Saved, s.SavedSignature, s.SaveOperation = d.Saved, d.SavedSignature, d.SaveOperation
		s.ValidationOperation, s.Validation = d.ValidationOperation, d.Validation
		s.ValidationStartedAt = d.ValidationStartedAt
		s.Error, s.Message = d.Error, d.Message
	}
	s.GenerationDraftMode = s.DraftMode
	if s.GenerationModeSelection == nil {
		s.GenerationModeSelection = map[string]string{}
	}
	s.GenerationModeSelection[s.DraftMode] = id
	if s.ValidationOperation != "" && (s.Validation.Status == "running" || s.Validation.Status == "queued") {
		a.pollGenerationValidation(s.ValidationOperation)
	}
}
func (a *App) chooseGenerationSource(mode string) {
	s := &a.integrations
	s.GenerationAgentApply = false
	if s.Busy || (s.Editor && s.DraftMode == mode) {
		return
	}
	s.rememberGenerationDraft()
	if id := s.GenerationModeSelection[mode]; id != "" {
		if d, ok := s.GenerationDrafts[id]; ok {
			a.selectGenerationProfile(id, GenerationRow{Profile: d.Profile})
			return
		}
	}
	for _, group := range generationWorkspaceGroups(s.Generation, mode) {
		for _, item := range group.Items {
			if mode == "agent_bridge" && !s.availableProcessingBridge(item.Icon) {
				continue
			}
			a.selectGenerationProfile(item.ID, item.Row)
			return
		}
	}
	a.newGenerationProfile(mode)
}
func (a *App) newGenerationProfile(mode string) {
	profile := GenerationProfile{Mode: "provider", Provider: "openai"}
	if mode == "agent_bridge" {
		profile = GenerationProfile{Mode: mode, BridgeKind: "codex"}
	}
	a.selectGenerationProfile("api-"+integrationOperation(), GenerationRow{Profile: profile})
}
func (a *App) discardGenerationDraft() {
	s := &a.integrations
	if s.SaveOperation != "" || s.Busy {
		return
	}
	s.GenerationAgentApply = false
	id := s.ProfileID
	delete(s.GenerationDrafts, id)
	rows := s.Generation.Profiles[id]
	if len(rows) > 0 {
		s.Editor = false
		a.selectGenerationProfile(id, rows[len(rows)-1])
		return
	}
	mode := s.DraftMode
	s.Editor = false
	s.APIKey = ""
	s.ProfileID = ""
	a.chooseGenerationSource(mode)
}
func (s *integrationState) connectedProcessingAgent(kind string) bool {
	for _, row := range s.Connections.Connections {
		if row.Kind == kind && row.Status == "configured_pending_new_session" {
			return true
		}
	}
	return false
}
func (s *integrationState) availableProcessingBridge(kind string) bool {
	_, ok := s.Generation.AgentBridges[kind]
	return ok
}

func (s *integrationState) hasAvailableProcessingBridge() bool {
	return len(s.Generation.AgentBridges) > 0
}

func (a *App) generationCanUse(ref *GenerationRef) bool {
	s := &a.integrations
	row, ok := s.Generation.row(ref)
	return ok && s.Generation.validated(ref) &&
		(row.Profile.Mode != "agent_bridge" || s.availableProcessingBridge(row.Profile.BridgeKind))
}

func (a *App) generationCandidateStatus(c *ui.Context) {
	s := &a.integrations
	setup := a.initialSetup || a.canActivateFirstRuntimeFromSettings()
	bridge := s.DraftMode == "agent_bridge"
	if bridge && !s.hasAvailableProcessingBridge() {
		return
	}
	saved := s.cleanSaved()
	current := saved && s.Generation.Active != nil && *s.Generation.Active == *s.Saved
	nextSelected := saved && s.Generation.Next != nil && *s.Generation.Next == *s.Saved
	needsValidation := bridge && !s.Generation.validated(s.Saved)
	busy := a.environment.Busy || s.Busy || s.HandoffOpen || s.HandoffOperation != "" || s.SaveOperation != ""
	if !current && (!setup || !nextSelected || needsValidation) {
		ui.Row(c).FillWidth().MinHeight(44).Padding(sp2, sp1).AlignItems(ui.Center).Justify(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			available := !bridge || s.availableProcessingBridge(s.BridgeKind)
			switch {
			case bridge && !saved:
				v4SmallPrimaryButton(c, "保存配置", a.saveGeneration).Disabled(busy || !available)
			case needsValidation:
				v4SmallPrimaryButton(c, "验证（会消耗额度）", func() {
					a.generationAction("validations")
				}).Disabled(busy || !available || generationValidationActive(s.Validation) || s.hasPendingGeneration())
			default:
				label := "切换"
				if setup {
					label = "设为待启用"
				}
				v4SmallPrimaryButton(c, label, func() {
					if !setup && s.Generation.HandoffAvailable {
						a.openGenerationHandoff(s.Saved)
					} else {
						a.generationAction("next")
					}
				}).Disabled(busy || !saved || !a.generationCanUse(s.Saved) || s.pendingGenerationProfile(s.ProfileID))
			}
		})
	}
	if bridge && (s.ValidationOperation != "" || s.Error != "" || s.Message != "") {
		ui.Column(c).FillWidth().Padding(sp2, sp1).Gap(sp2).Children(func() {
			if s.ValidationOperation != "" {
				a.generationValidationStatus(c)
			}
			if s.Error != "" {
				homeText(c, s.Error, homeMeta).TextColor(tok.Danger)
			} else if s.Message != "" {
				homeText(c, s.Message, homeMeta)
			}
		})
	}
}

func (a *App) generationWorkspace(c *ui.Context) {
	a.generationWorkspaceFor(c, true, true)
}

// Selection and configuration share drafts, but only selection can enable a candidate.
func (a *App) generationWorkspaceFor(c *ui.Context, edit, enable bool) {
	ui.Column(c).FillWidth().Gap(0).Children(func() { a.generationWorkspaceContent(c, edit, enable) })
}
func (a *App) generationWorkspaceContent(c *ui.Context, edit, enable bool) {
	s := &a.integrations
	if !s.Editor && (a.initialSetup || a.canActivateFirstRuntimeFromSettings()) {
		if row, ok := s.Generation.row(s.Generation.Next); ok {
			a.selectGenerationProfile(s.Generation.Next.ID, row)
		}
	}
	if !s.Editor {
		mode := s.GenerationDraftMode
		if mode == "" {
			mode = "provider"
			if row, ok := s.Generation.row(s.Generation.Active); ok {
				mode = row.Profile.Mode
			}
		}
		a.chooseGenerationSource(mode)
	}
	title := "模型配置"
	desc := ""
	if enable {
		title = "提炼方式"
		desc = "当前使用：" + generationLabel(s.Generation, s.Generation.Active)
	}
	selected := 0
	if s.DraftMode == "provider" {
		selected = 1
	}
	sourceDesc := "让 Codex、pi、TRAE CLI、Gemini 等本机已登录 CLI 提炼记忆，无需填写 API Key，也不依赖先完成采集接入"
	if s.DraftMode == "provider" {
		sourceDesc = "填写 OpenAI 兼容或 Ollama 等接口，由 Memlane 直接调用"
	}

	v4Group(c, title, desc, nil, func() {
		if enable && s.Handoff.Handoff != nil {
			ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				a.generationHandoffStatus(c)
			})
		}
		v4Row(c, v4RowOptions{
			Key:   "generation-source-row",
			Title: "提炼来源",
			Desc:  sourceDesc,
			Control: func() {
				v4Segmented(c, "generation-source", "提炼来源", &selected, []string{"用本机 Agent CLI 提炼", "直接调用模型 API"}, func() {
					if s.Busy {
						return
					}
					mode := "agent_bridge"
					if selected == 1 {
						mode = "provider"
					}
					a.chooseGenerationSource(mode)
				})
			},
		})
		if !edit {
			a.generationProfileCards(c, false)
			if enable {
				a.generationCandidateStatus(c)
			}
			v4Row(c, v4RowOptions{
				Key:       "settings-goto-agents",
				Title:     "管理 Agent 与模型",
				Desc:      "连接或检查本机 Coding Agents、添加与编辑模型 Provider",
				Clickable: true,
				Chevron:   true,
				OnClick: func() {
					a.access.SettingsSection = "Agent 与模型"
				},
			})
		}
	})

	if enable && edit && s.DraftMode != "agent_bridge" {
		a.generationCandidateStatus(c)
	}
	if enable && s.HandoffOpen {
		a.generationHandoffPanel(c)
		return
	}
	if edit {
		a.generationProfileCards(c, true)
		if s.DraftMode != "agent_bridge" {
			a.generationEditorPanel(c)
		} else if enable {
			a.generationCandidateStatus(c)
		}
	}
}

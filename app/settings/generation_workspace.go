package settings

import (
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
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

func (s *IntegrationState) rememberGenerationDraft() {
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

func (a *Controller) selectGenerationProfile(id string, row GenerationRow) {
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

func (a *Controller) chooseGenerationSource(mode string) {
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

func (a *Controller) newGenerationProfile(mode string) {
	profile := GenerationProfile{Mode: "provider", Provider: "openai"}
	if mode == "agent_bridge" {
		profile = GenerationProfile{Mode: mode, BridgeKind: "codex"}
	}
	a.selectGenerationProfile("api-"+integrationOperation(), GenerationRow{Profile: profile})
}

func (a *Controller) discardGenerationDraft() {
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

func (s *IntegrationState) connectedProcessingAgent(kind string) bool {
	for _, row := range s.Connections.Connections {
		if row.Kind == kind && row.Status == "configured_pending_new_session" {
			return true
		}
	}
	return false
}

func (s *IntegrationState) availableProcessingBridge(kind string) bool {
	_, ok := s.Generation.AgentBridges[kind]
	return ok
}

func (s *IntegrationState) hasAvailableProcessingBridge() bool {
	return len(s.Generation.AgentBridges) > 0
}

func (a *Controller) generationCanUse(ref *GenerationRef) bool {
	s := &a.integrations
	row, ok := s.Generation.row(ref)
	if !ok {
		return false
	}
	if row.Profile.Mode == "agent_bridge" {
		if !s.availableProcessingBridge(row.Profile.BridgeKind) {
			return false
		}
		if !a.initialSetup && !a.canActivateFirstRuntimeFromSettings() {
			return true
		}
	}
	return s.Generation.validated(ref)
}

func (a *Controller) agentBridgeMatchesActive() bool {
	s := &a.integrations
	if s.Generation.Active == nil || s.Saved == nil || *s.Generation.Active != *s.Saved {
		return false
	}
	row, ok := s.Generation.row(s.Saved)
	if !ok || row.Profile.Mode != "agent_bridge" {
		return false
	}
	draft := s.draftProfile()
	return row.Profile.BridgeKind == draft.BridgeKind
}

func (a *Controller) generationCandidateStatus(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	setup := a.initialSetup || a.canActivateFirstRuntimeFromSettings()
	bridge := s.DraftMode == "agent_bridge"
	if bridge && !s.hasAvailableProcessingBridge() {
		return
	}
	saved := s.cleanSaved()
	current := saved && s.Generation.Active != nil && *s.Generation.Active == *s.Saved
	if bridge && !setup {
		current = a.agentBridgeMatchesActive()
	}
	nextSelected := saved && s.Generation.Next != nil && *s.Generation.Next == *s.Saved
	needsValidation := bridge && setup && !s.Generation.validated(s.Saved)
	confirmingModel := false
	if row := s.activeHandoffReceipt(); row != nil && isConfirmingHandoffStatus(row.Status) {
		confirmingModel = true
	}
	busy := a.environment.Busy || s.Busy || s.HandoffOpen || s.HandoffOperation != "" || s.SaveOperation != "" || confirmingModel
	if !current && (!setup || !nextSelected || needsValidation) {
		ui.Row(c).FillWidth().MinHeight(44).Padding(sp2, sp4).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			available := !bridge || s.availableProcessingBridge(s.BridgeKind)
			switch {
			case bridge && !setup:
				kitui.SmallPrimaryButton(c, tok, "切换", a.switchAgentBridge).Disabled(busy || !available)
			case bridge && !saved:
				kitui.SmallPrimaryButton(c, tok, "保存配置", a.saveGeneration).Disabled(busy || !available)
			case needsValidation:
				kitui.SmallPrimaryButton(c, tok, "验证（会消耗额度）", func() {
					a.generationAction("validations")
				}).Disabled(busy || !available || generationValidationActive(s.Validation) || s.hasPendingGeneration())
			default:
				label := "切换"
				if setup {
					label = "设为待启用"
				}
				kitui.SmallPrimaryButton(c, tok, label, func() {
					if !setup && s.Generation.HandoffAvailable {
						a.openGenerationHandoff(s.Saved)
					} else {
						a.generationAction("next")
					}
				}).Disabled(busy || !saved || !a.generationCanUse(s.Saved) || s.pendingGenerationProfile(s.ProfileID))
			}
		})
	}
	showValidation := bridge && setup && s.ValidationOperation != ""
	showTransientMessage := s.Busy && s.Message != "" && s.MessageScope != "connections"
	if bridge && (showValidation || s.Error != "" || showTransientMessage) {
		ui.Column(c).FillWidth().Padding(sp2, sp4).Gap(sp2).Children(func() {
			if showValidation {
				a.generationValidationStatus(c)
			}
			if s.Error != "" {
				kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger)
			} else if showTransientMessage {
				kitui.Text(c, tok, s.Message, kitui.RoleMeta)
			}
		})
	}
}

func (a *Controller) generationWorkspace(c *ui.Context) {
	a.generationWorkspaceFor(c, true, true)
}

// Selection and configuration share drafts, but only selection can enable a candidate.
func (a *Controller) generationWorkspaceFor(c *ui.Context, edit, enable bool) {
	ui.Column(c).FillWidth().Gap(0).Children(func() { a.generationWorkspaceContent(c, edit, enable) })
}

func (a *Controller) generationWorkspaceContent(c *ui.Context, edit, enable bool) {
	tok := a.tokens()
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
	activeHandoff := s.activeHandoffReceipt()
	if enable {
		title = "提炼方式"
		if activeHandoff != nil && isConfirmingHandoffStatus(activeHandoff.Status) {
			desc = "正在确认使用的模型"
		} else {
			desc = "当前：" + generationLabel(s.Generation, s.Generation.Active)
		}
	}
	selected := 0
	if s.DraftMode == "provider" {
		selected = 1
	}
	sourceDesc := "复用本机已登录的 CLI，无需 API Key"
	if s.DraftMode == "provider" {
		sourceDesc = "直连 OpenAI 兼容或 Ollama 接口"
	}
	showingEmptyGuide := s.DraftMode == "agent_bridge" && !s.hasAvailableProcessingBridge()
	var groupActions func()
	if !edit && s.DraftMode == "provider" {
		groupActions = func() {
			kitui.SmallButton(c, tok, "添加配置", kitui.IconPlus, func() {
				a.newGenerationProfile("provider")
				s.GenerationProviderDialog = true
			}).Disabled(s.Busy)
		}
	}

	if !edit && enable {
		a.generationPendingOperations(c)
	}

	kitui.Group(c, tok, title, desc, groupActions, func() {
		if enable && activeHandoff != nil {
			ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() {
				a.generationHandoffStatus(c)
			})
		}
		kitui.Row(c, tok, kitui.RowOptions{
			Key:      "generation-source-row",
			Title:    "提炼来源",
			Desc:     sourceDesc,
			NoBorder: showingEmptyGuide,
			Control: func() {
				kitui.Segmented(c, tok, "generation-source", "提炼来源", &selected, []string{"用本机 Agent CLI 提炼", "直接调用模型 API"}, func() {
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
			if enable && s.DraftMode == "agent_bridge" {
				a.generationCandidateStatus(c)
			}
		}
	})

	if !edit {
		a.generationProviderDialog(c)
		a.generationDeleteModal(c)
	}

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

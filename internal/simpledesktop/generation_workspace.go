package simpledesktop

import "github.com/egoist/mygo/ui"

type generationEditorDraft struct {
	Profile                                                              GenerationProfile
	Model, Endpoint, ProviderLabel, APIKey, Temperature, MaxOutputTokens string
	Saved                                                                *GenerationRef
	SavedSignature, SaveOperation, ValidationOperation                   string
	CredentialConfigured                                                 bool
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
	s.GenerationDrafts[s.ProfileID] = generationEditorDraft{s.draftProfile(), s.Model, s.Endpoint, s.ProviderLabel, s.APIKey, s.Temperature, s.MaxOutputTokens, s.Saved, s.SavedSignature, s.SaveOperation, s.ValidationOperation, s.CredentialConfigured, s.Validation, s.Error, s.Message}
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
func (a *App) generationCanUse(ref *GenerationRef) bool {
	s := &a.integrations
	if row, ok := s.Generation.row(ref); ok && row.Profile.Mode == "agent_bridge" {
		return s.connectedProcessingAgent(row.Profile.BridgeKind)
	}
	return s.Generation.validated(ref)
}

func (a *App) applySelectedProcessingAgent() {
	if a.integrations.Generation.HandoffAvailable {
		a.openGenerationHandoff(a.integrations.Saved)
	} else {
		a.generationAction("next")
	}
}
func (a *App) generationCandidateStatus(c *ui.Context) {
	s := &a.integrations
	if s.DraftMode == "agent_bridge" {
		current := s.Generation.Active != nil && s.Saved != nil && *s.Generation.Active == *s.Saved && s.cleanSaved()
		if !current {
			ui.Row(c).FillWidth().MinHeight(44).Padding(sp2, sp1).AlignItems(ui.Center).Justify(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				v4SmallPrimaryButton(c, "切换", func() {
					if s.cleanSaved() {
						a.applySelectedProcessingAgent()
					} else {
						s.GenerationAgentApply = true
						a.saveGeneration()
					}
				}).Disabled(s.Busy || s.HandoffOpen || s.HandoffOperation != "" || s.SaveOperation != "" || !s.connectedProcessingAgent(s.BridgeKind))
			})
		}
		if s.Error != "" {
			ui.Row(c).FillWidth().Padding(sp2, sp1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				homeText(c, s.Error, homeMeta).TextColor(tok.Danger)
			})
		}
		return
	}
	current := s.cleanSaved() && s.Generation.Active != nil && s.Saved != nil && *s.Generation.Active == *s.Saved
	if current {
		return
	}

	ui.Row(c).FillWidth().MinHeight(44).Padding(sp2, sp1).AlignItems(ui.Center).Justify(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		v4SmallPrimaryButton(c, "切换", func() {
			if s.Generation.HandoffAvailable {
				a.openGenerationHandoff(s.Saved)
			} else {
				a.generationAction("next")
			}
		}).Disabled(s.Busy || s.HandoffOpen || s.HandoffOperation != "" || s.pendingGenerationProfile(s.ProfileID) || !s.cleanSaved() || !s.Generation.validated(s.Saved))
	})
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
	sourceDesc := "让 Codex、Gemini 等已连接的 Agent 用它自己的模型和额度提炼记忆，无需填写 API Key"
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
				v4Segmented(c, "generation-source", "提炼来源", &selected, []string{"用已连接的 Agent 提炼", "直接调用模型 API"}, func() {
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

	if enable && edit {
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
		}
	}
}

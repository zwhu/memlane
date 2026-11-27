package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *IntegrationState) draftProfile() GenerationProfile {
	profile := GenerationProfile{Mode: s.DraftMode, Model: strings.TrimSpace(s.Model)}
	if s.DraftMode == "agent_bridge" {
		profile.BridgeKind = s.BridgeKind
		if profile.BridgeKind == "" {
			profile.BridgeKind = "codex"
		}
		if profile.BridgeKind == "pi" {
			profile.BridgeProvider = s.BridgeProvider
		}
		if defaults, ok := s.Generation.AgentBridges[profile.BridgeKind]; ok {
			profile.Model, profile.BridgeProvider = defaults.Model, defaults.BridgeProvider
		}
		return profile
	}
	providers := map[string]string{"OpenAI": "openai", "Anthropic": "anthropic", "Gemini": "gemini", "OpenAI 兼容": "custom", "Ollama": "ollama"}
	profile.Provider = providers[s.ProviderLabel]
	profile.Endpoint = strings.TrimSpace(s.Endpoint)
	profile.Temperature, profile.MaxOutputTokens, _ = generationParameters(s.Temperature, s.MaxOutputTokens)
	if profile.Provider != "ollama" {
		profile.CredentialVersion = s.CredentialVersion
	}
	return profile
}

func (s *IntegrationState) signature() string {
	p := s.draftProfile()
	base := fmt.Sprintf("%s|%s|%s|%s|%s", p.Mode, p.Provider, p.Model, p.Endpoint, p.CredentialVersion)
	if p.Mode == "provider" {
		base += "|" + strings.TrimSpace(s.Temperature) + "|" + strings.TrimSpace(s.MaxOutputTokens)
	} else {
		base += "|" + p.BridgeKind + "|" + p.BridgeProvider
	}
	return base
}

func (s *IntegrationState) cleanSaved() bool {
	return s.generationParameterError() == nil && s.Saved != nil && (s.APIKey == "" || s.draftProfile().Provider == "ollama") && s.signature() == s.SavedSignature && s.SaveOperation == ""
}

func (s *IntegrationState) editProfile(id string, row GenerationRow) {
	s.KeyEpoch++
	if s.ValidationCancel != nil {
		s.ValidationCancel()
		s.ValidationCancel = nil
	}
	s.Editor = true
	s.ProfileID = id
	s.DraftMode = row.Profile.Mode
	s.BridgeKind, s.BridgeProvider = row.Profile.BridgeKind, row.Profile.BridgeProvider
	s.Model = row.Profile.Model
	s.Endpoint = row.Profile.Endpoint
	s.Temperature, s.MaxOutputTokens = "", ""
	if row.Profile.Temperature != nil {
		s.Temperature = strconv.FormatFloat(*row.Profile.Temperature, 'g', -1, 64)
	}
	if row.Profile.MaxOutputTokens != nil {
		s.MaxOutputTokens = strconv.Itoa(*row.Profile.MaxOutputTokens)
	}
	s.APIKey = ""
	s.CredentialVersion = row.Profile.CredentialVersion
	s.CredentialConfigured = row.Profile.CredentialConfigured
	s.ProviderLabel = map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
	s.Saved = &GenerationRef{ID: id, Revision: row.Revision}
	s.SavedSignature = s.signature()
	s.SaveOperation = ""
	s.ValidationOperation, s.Validation = s.Generation.latestValidation(row.Digest)
	s.ValidationStartedAt = time.Time{}
}

func (a *Controller) saveGeneration() {
	client, ok := a.accessClient()
	s := &a.integrations
	s.GenerationAgentApply = false
	if !ok || s.Busy || s.SaveOperation != "" {
		return
	}
	profile := s.draftProfile()
	if profile.Mode == "agent_bridge" && profile.Model == "" {
		s.Error = "尚未读取到本机 CLI 的模型，请确认 CLI 已登录并重新检测。"
		return
	}
	if err := s.generationParameterError(); err != nil {
		s.Error = err.Error()
		return
	}
	if profile.Model == "" || (profile.Mode == "provider" && (profile.Endpoint == "" || (profile.Provider != "ollama" && s.APIKey == "" && !s.CredentialConfigured))) {
		s.Error = "请填写模型、API 地址和密钥"
		return
	}
	op := integrationOperation()
	if err := a.recordGenerationOperation(generationUIOperation{Operation: op, Kind: "save", Profile: s.ProfileID}); err != nil {
		s.Error = "无法保存操作回执位置，本次配置尚未发送"
		return
	}
	s.SaveOperation = op
	s.Busy = true
	s.Error = ""
	key := s.APIKey
	if profile.Provider == "ollama" {
		key = ""
	}
	s.APIKey = ""
	s.KeyEpoch++
	id := s.ProfileID
	revision := s.Generation.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		payload := map[string]any{"expected_revision": revision, "operation_id": op, "profile": profile}
		if key != "" {
			payload["api_key"] = key
		}
		var response struct {
			Result GenerationRef `json:"result"`
		}
		err := client.request(ctx, "PUT", "/v1/generation/profiles/"+url.PathEscape(id), payload, &response)
		submitted := err == nil
		key = ""
		delete(payload, "api_key")
		var directory GenerationDirectory
		if err == nil {
			err = client.request(ctx, "GET", "/v1/generation", nil, &directory)
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.GenerationAgentApply = false
				var rejected *ControlResponseError
				if !submitted && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.SaveOperation = ""
					s.Error = "保存未通过，请检查配置后再次保存；新密钥需要重新填写。"
					if rejected.Status == http.StatusConflict || rejected.Code != "" {
						s.Error = "保存未通过：" + ControlUserError(err) + "；新密钥需要重新填写。"
					}
					return
				}
				s.Error = "保存结果尚未确认，请核对原操作。"
				return
			}
			s.Generation = directory
			row, found := directory.row(&response.Result)
			if !found {
				s.GenerationAgentApply = false
				s.Error = "尚未找到原保存回执"
				return
			}
			if !a.finishGenerationOperation(op) {
				s.GenerationAgentApply = false
				return
			}
			s.editProfile(id, row)
			s.Message = "配置已保存，当前使用未改变"
			s.MessageScope = "generation"
		})
	}()
}

func (a *Controller) reviewGenerationSave() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.SaveOperation == "" {
		return
	}
	s.Busy = true
	op := s.SaveOperation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var receipt struct {
			Recorded bool          `json:"recorded"`
			Result   GenerationRef `json:"result"`
		}
		var directory GenerationDirectory
		err := client.request(ctx, "GET", "/v1/generation/operations/"+url.PathEscape(op), nil, &receipt)
		if err == nil && receipt.Recorded {
			err = client.request(ctx, "GET", "/v1/generation", nil, &directory)
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil || !receipt.Recorded {
				s.Error = "尚无可确认的原操作回执，不会重复提交"
				return
			}
			row, found := directory.row(&receipt.Result)
			if !found {
				s.Error = "原保存配置尚未找到"
				return
			}
			s.Generation = directory
			if !a.finishGenerationOperation(op) {
				return
			}
			s.editProfile(receipt.Result.ID, row)
			s.Error = ""
			s.Message = "原保存结果已核对，当前使用未改变"
			s.MessageScope = "generation"
		})
	}()
}

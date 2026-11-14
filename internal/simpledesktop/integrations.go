package simpledesktop

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type ConnectionCapability struct {
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Condition string `json:"condition"`
}
type AgentConnection struct {
	ID              string                          `json:"instance_id"`
	Kind            string                          `json:"kind"`
	Status          string                          `json:"status"`
	Detected        bool                            `json:"detected"`
	AgentRegistered bool                            `json:"agent_registered"`
	NewSessions     int                             `json:"new_sessions"`
	ErrorCode       string                          `json:"error_code"`
	Preview         *CodexConnectionPreview         `json:"preview"`
	Capabilities    map[string]ConnectionCapability `json:"capabilities"`
}
type ConnectionsDirectory struct {
	Revision    int64             `json:"revision"`
	Connections []AgentConnection `json:"connections"`
}
type GenerationRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type GenerationProfile struct {
	Mode                 string   `json:"mode"`
	Provider             string   `json:"provider,omitempty"`
	BridgeKind           string   `json:"bridge_kind,omitempty"`
	Model                string   `json:"model"`
	Endpoint             string   `json:"endpoint,omitempty"`
	Temperature          *float64 `json:"temperature,omitempty"`
	MaxOutputTokens      *int     `json:"max_output_tokens,omitempty"`
	CredentialVersion    string   `json:"credential_version,omitempty"`
	CredentialConfigured bool     `json:"credential_configured,omitempty"`
}
type GenerationRow struct {
	Revision int64             `json:"revision"`
	Digest   string            `json:"digest"`
	Profile  GenerationProfile `json:"profile"`
}
type GenerationValidation struct {
	Status             string `json:"status"`
	Digest             string `json:"digest"`
	ErrorCode          string `json:"error_code"`
	Plain              bool   `json:"plain_passed"`
	Schema             bool   `json:"schema_passed"`
	Quality            bool   `json:"quality_passed"`
	RegisteredRevision int64  `json:"registered_revision"`
	StructuredMode     string `json:"structured_mode"`
	ModeEvidence       string `json:"structured_mode_evidence"`
}
type GenerationDirectory struct {
	Revision     int64                           `json:"revision"`
	Active       *GenerationRef                  `json:"active"`
	Next         *GenerationRef                  `json:"next"`
	Profiles     map[string][]GenerationRow      `json:"profiles"`
	Validations  map[string]GenerationValidation `json:"validations"`
	NextError    string                          `json:"next_error"`
	StartupError string                          `json:"startup_error"`
}

func (g GenerationDirectory) row(ref *GenerationRef) (GenerationRow, bool) {
	if ref == nil {
		return GenerationRow{}, false
	}
	for _, row := range g.Profiles[ref.ID] {
		if row.Revision == ref.Revision {
			return row, true
		}
	}
	return GenerationRow{}, false
}
func (g GenerationDirectory) validated(ref *GenerationRef) bool {
	row, ok := g.row(ref)
	if !ok {
		return false
	}
	var latest int64
	for _, v := range g.Validations {
		if v.Digest == row.Digest && v.RegisteredRevision > latest {
			latest = v.RegisteredRevision
		}
	}
	found := false
	for _, v := range g.Validations {
		if v.Digest != row.Digest || v.RegisteredRevision != latest {
			continue
		}
		found = true
		if v.Status != "passed" || !v.Plain || !v.Schema || !v.Quality {
			return false
		}
	}
	return found
}

func (g GenerationDirectory) latestValidation(digest string) (string, GenerationValidation) {
	if digest == "" {
		return "", GenerationValidation{}
	}
	var latest int64
	for _, row := range g.Validations {
		if row.Digest == digest && row.RegisteredRevision > latest {
			latest = row.RegisteredRevision
		}
	}
	operation := ""
	var result GenerationValidation
	for id, row := range g.Validations {
		if row.Digest != digest || row.RegisteredRevision != latest {
			continue
		}
		if operation != "" {
			return "", GenerationValidation{}
		}
		operation, result = id, row
	}
	return operation, result
}

type integrationState struct {
	PendingOperations                                                                           []generationUIOperation
	Connections                                                                                 ConnectionsDirectory
	Generation                                                                                  GenerationDirectory
	ShowUnavailable bool
	Loaded, Loading, Busy                                                                       bool
	Request                                                                                     uint64
	Error, Message                                                                              string
	Selected                                                                                    map[string]bool
	ItemMessages                                                                                map[string]string
	BatchStop                                                                                   *atomic.Bool
	Editor                                                                                      bool
	ProfileID, ProviderLabel, DraftMode, BridgeKind, Model, Endpoint, APIKey, CredentialVersion string
	Temperature, MaxOutputTokens                                                                string
	CredentialConfigured                                                                        bool
	Saved                                                                                       *GenerationRef
	SavedSignature                                                                              string
	SaveOperation, ValidationOperation                                                          string
	NextOperation                                                                               string
	Validation                                                                                  GenerationValidation
	ValidationCancel                                                                            context.CancelFunc
	KeyEpoch                                                                                    uint64
}

func integrationOperation() string { return "ui-" + rand.Text() }
func connectionName(kind string) string {
	switch kind {
	case "codex":
		return "Codex"
	case "pi":
		return "pi"
	case "traex":
		return "TraeX"
	case "claude_code":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "opencode":
		return "OpenCode"
	}
	return kind
}
func connectionStatus(row AgentConnection) string {
	switch row.Status {
	case "component_unavailable":
		if !row.Detected {
			return "未安装"
		}
		return "暂时无法连接"
	case "integration_pending":
		if row.Detected {
			return "暂不支持连接"
		}
		return "未安装 · 暂不支持连接"
	case "prepared":
		return "等待确认"
	case "configured_pending_new_session":
		if row.NewSessions > 0 {
			return "已连接"
		}
		return "等待新会话"
	case "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown":
		return "连接结果待确认"
	case "agent_creation_unconfirmed":
		return "连接结果待确认"
	}
	if !row.Detected {
		return "未安装"
	}
	return "未连接"
}

func connectionActionAllowed(row AgentConnection, action string) bool {
	if action == "review" {
		return row.ID != ""
	}
	if !row.Detected {
		return false
	}
	if action == "prepare" {
		return row.Status == "" || row.Status == "not_connected" || (row.Kind == "opencode" && row.Status == "prepared")
	}
	return action == "activate" && row.Status == "prepared" && row.Preview != nil && row.Preview.ProposedSHA != ""
}
func (a *App) RefreshIntegrations() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Loading || s.Busy || a.Update == nil {
		return
	}
	pending, err := a.loadGenerationOperations()
	if err != nil {
		s.Error = "无法读取本机待核对操作，已停止发送新配置请求"
		return
	}
	s.PendingOperations = pending
	s.Loading = true
	s.Request++
	request := s.Request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var connections ConnectionsDirectory
		var generation GenerationDirectory
		var err error
		if !a.Config.GenerationSetupOnly {
			err = client.request(ctx, "GET", "/v1/connections", nil, &connections)
		}
		if err == nil {
			err = client.request(ctx, "GET", "/v1/generation", nil, &generation)
		}
		a.Update(func() {
			if request != s.Request {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				return
			}
			s.Error = ""
			s.Loaded = true
			s.Connections = connections
			s.Generation = generation
			if s.Selected == nil {
				s.Selected = map[string]bool{}
			}
			if s.ItemMessages == nil {
				s.ItemMessages = map[string]string{}
			}
		})
	}()
}
func (a *App) connectAgents(action string, identifiers []string) {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || len(identifiers) == 0 || a.Update == nil {
		return
	}
	pending, err := a.loadGenerationOperations()
	if err != nil {
		s.Error = "无法读取本机待核对操作，已停止发送新接入请求"
		return
	}
	s.PendingOperations = pending
	var eligible []string
	for _, id := range identifiers {
		if action == "review" || s.pendingConnection(id) == nil {
			eligible = append(eligible, id)
		}
	}
	if len(eligible) == 0 {
		s.Message = "请先核对 Agent 原操作；未重新提交"
		return
	}
	identifiers = eligible
	s.Busy = true
	s.Error = ""
	s.Message = ""
	stop := &atomic.Bool{}
	s.BatchStop = stop
	if s.ItemMessages == nil {
		s.ItemMessages = map[string]string{}
	}
	go func() {
		for _, id := range identifiers {
			if stop.Load() {
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			var current ConnectionsDirectory
			err := client.request(ctx, "GET", "/v1/connections", nil, &current)
			var target *AgentConnection
			for _, row := range current.Connections {
				if row.ID == id {
					value := row
					target = &value
					break
				}
			}
			if err == nil && target == nil {
				err = fmt.Errorf("Agent 接入配置已变化，请重新读取")
			}
			if err == nil && !connectionActionAllowed(*target, action) {
				cancel()
				continue
			}
			op := ""
			if err == nil {
				op = integrationOperation()
				payload := map[string]any{"expected_revision": current.Revision, "operation_id": op}
				if action == "activate" {
					if target.Preview == nil || target.Status != "prepared" {
						cancel()
						continue
					}
					payload["proposed_sha256"] = target.Preview.ProposedSHA
				}
				if action != "review" {
					recorded := make(chan error, 1)
					a.Update(func() {
						recorded <- a.recordGenerationOperation(generationUIOperation{Operation: op, Kind: "connection_" + action, Profile: id})
					})
					select {
					case err = <-recorded:
					case <-ctx.Done():
						err = ctx.Err()
					}
				}
				var result struct {
					Revision   int64           `json:"revision"`
					Connection AgentConnection `json:"connection"`
				}
				if err == nil {
					err = client.request(ctx, "POST", "/v1/connections/"+url.PathEscape(id)+"/"+action, payload, &result)
					if err == nil && result.Connection.ID != id {
						err = fmt.Errorf("Agent 原操作回执未确认，请核对")
					}
				}
			}
			var directory ConnectionsDirectory
			readErr := client.request(ctx, "GET", "/v1/connections", nil, &directory)
			cancel()
			a.Update(func() {
				if readErr == nil {
					s.Connections = directory
				}
				if err != nil {
					s.ItemMessages[id] = err.Error()
				} else {
					if action != "review" {
						a.finishGenerationOperation(op)
					}
					s.ItemMessages[id] = ""
     if action == "activate" { delete(s.Selected,id) }
				}
			})
		}
		a.Update(func() { s.Busy = false; s.BatchStop = nil; a.RefreshProjectDirectory() })
	}()
}
func (s *integrationState) draftProfile() GenerationProfile {
	profile := GenerationProfile{Mode: s.DraftMode, Model: strings.TrimSpace(s.Model)}
	if s.DraftMode == "agent_bridge" {
		profile.BridgeKind = "codex"
		return profile
	}
	providers := map[string]string{"OpenAI": "openai", "Anthropic": "anthropic", "Gemini": "gemini", "OpenAI 兼容": "custom", "Ollama（本机接口）": "ollama"}
	profile.Provider = providers[s.ProviderLabel]
	profile.Endpoint = strings.TrimSpace(s.Endpoint)
	profile.Temperature, profile.MaxOutputTokens, _ = generationParameters(s.Temperature, s.MaxOutputTokens)
	if profile.Provider != "ollama" {
		profile.CredentialVersion = s.CredentialVersion
	}
	return profile
}
func (s *integrationState) signature() string {
	p := s.draftProfile()
	base := fmt.Sprintf("%s|%s|%s|%s|%s", p.Mode, p.Provider, p.Model, p.Endpoint, p.CredentialVersion)
	if p.Mode == "provider" {
		base += "|" + strings.TrimSpace(s.Temperature) + "|" + strings.TrimSpace(s.MaxOutputTokens)
	}
	return base
}
func (s *integrationState) cleanSaved() bool {
	return s.generationParameterError() == nil && s.Saved != nil && (s.APIKey == "" || s.draftProfile().Provider == "ollama") && s.signature() == s.SavedSignature && s.SaveOperation == ""
}
func (s *integrationState) editProfile(id string, row GenerationRow) {
	s.KeyEpoch++
	if s.ValidationCancel != nil {
		s.ValidationCancel()
		s.ValidationCancel = nil
	}
	s.Editor = true
	s.ProfileID = id
	s.DraftMode = row.Profile.Mode
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
	s.ProviderLabel = map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama（本机接口）"}[row.Profile.Provider]
	s.Saved = &GenerationRef{ID: id, Revision: row.Revision}
	s.SavedSignature = s.signature()
	s.SaveOperation = ""
	s.ValidationOperation, s.Validation = s.Generation.latestValidation(row.Digest)
}
func (a *App) saveGeneration() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.SaveOperation != "" {
		return
	}
	profile := s.draftProfile()
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
		a.Update(func() {
			s.Busy = false
			if err != nil {
				var rejected *ControlResponseError
				if !submitted && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.SaveOperation = ""
					s.Error = "保存未通过，请检查配置后再次保存；新密钥需要重新填写。"
					return
				}
				s.Error = "保存结果尚未确认，请核对原操作。"
				return
			}
			s.Generation = directory
			row, found := directory.row(&response.Result)
			if !found {
				s.Error = "尚未找到原保存回执"
				return
			}
			if !a.finishGenerationOperation(op) {
				return
			}
			s.editProfile(id, row)
			s.Message = "配置已保存，当前使用未改变"
		})
	}()
}
func (a *App) reviewGenerationSave() {
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
		a.Update(func() {
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
		})
	}()
}
func (a *App) generationAction(action string) {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || !s.cleanSaved() || (action != "validations" && action != "next") {
		return
	}
	ref := *s.Saved
	revision := s.Generation.Revision
	op := integrationOperation()
	if err := a.recordGenerationOperation(generationUIOperation{Operation: op, Kind: action, Profile: ref.ID, Revision: ref.Revision}); err != nil {
		s.Error = "无法保存操作回执位置，本次请求尚未发送"
		return
	}
	s.Busy = true
	s.Error = ""
	if action == "validations" {
		s.ValidationOperation = op
		s.Validation = GenerationValidation{Status: "queued"}
	}
	if action == "next" {
		s.NextOperation = op
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		method := "POST"
		if action == "next" {
			method = "PUT"
		}
		var result any
		err := client.request(ctx, method, "/v1/generation/"+action, map[string]any{"expected_revision": revision, "operation_id": op, "profile": ref}, &result)
		var directory GenerationDirectory
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.Update(func() {
			s.Busy = false
			if readErr == nil {
				s.Generation = directory
			}
			if err != nil {
				s.Error = err.Error()
				var rejected *ControlResponseError
				if action == "next" && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.NextOperation = ""
				}
			} else if action == "next" {
				if !a.finishGenerationOperation(op) {
					return
				}
				s.NextOperation = ""
				s.Message = "已设为下次启动使用；当前任务保持原配置"
			} else {
				s.Message = "正在验证，会使用所选 Provider 的额度"
			}
			if action == "validations" {
				var rejected *ControlResponseError
				if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.Validation = GenerationValidation{Status: "failed"}
					return
				}
				a.pollGenerationValidation(op)
			}
		})
	}()
}
func (a *App) reviewGenerationNext() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.NextOperation == "" {
		return
	}
	s.Busy = true
	op := s.NextOperation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var receipt struct {
			Recorded bool `json:"recorded"`
			Result   struct {
				Next *GenerationRef `json:"next"`
			} `json:"result"`
		}
		var directory GenerationDirectory
		err := client.request(ctx, "GET", "/v1/generation/operations/"+url.PathEscape(op), nil, &receipt)
		if err == nil && receipt.Recorded {
			err = client.request(ctx, "GET", "/v1/generation", nil, &directory)
		}
		a.Update(func() {
			s.Busy = false
			if err != nil || !receipt.Recorded || receipt.Result.Next == nil {
				s.Error = "待应用设置尚无确认回执，不会重复提交"
				return
			}
			s.Generation = directory
			if !a.finishGenerationOperation(op) {
				return
			}
			s.NextOperation = ""
			s.Error = ""
			s.Message = "原待应用设置已核对"
		})
	}()
}
func (a *App) pollGenerationValidation(operation string) {
	a.pollGenerationValidationWithTiming(operation, 2*time.Second, 7*time.Minute)
}

func (a *App) pollGenerationValidationWithTiming(operation string, interval, budget time.Duration) {
	client, ok := a.accessClient()
	if !ok || operation == "" {
		return
	}
	if a.integrations.ValidationCancel != nil {
		a.integrations.ValidationCancel()
	}
	// The local provider has a 360s backend budget. Observe its terminal receipt
	// beyond that budget without cancelling or resubmitting the model operation.
	parent, stop := context.WithTimeout(context.Background(), budget)
	a.integrations.ValidationCancel = stop
	go func() {
		defer stop()
		defer func() {
			if errors.Is(parent.Err(), context.DeadlineExceeded) {
				a.Update(func() {
					s := &a.integrations
					if s.ValidationOperation == operation && (s.Validation.Status == "running" || s.Validation.Status == "queued") {
						s.Error = "等待验证回执超时，请核对原验证；不会重新发起验证。"
					}
				})
			}
		}()
		for {
			ctx, cancel := context.WithTimeout(parent, 10*time.Second)
			var receipt GenerationValidation
			err := client.request(ctx, "GET", "/v1/generation/validations/"+url.PathEscape(operation), nil, &receipt)
			cancel()
			if parent.Err() != nil {
				return
			}
			terminal := err != nil || (receipt.Status != "running" && receipt.Status != "queued")
			a.Update(func() {
				if a.integrations.ValidationOperation != operation {
					return
				}
				if terminal {
					a.integrations.Message = ""
				}
				if err != nil {
					a.integrations.Error = "验证回执暂时不可用，请核对原验证"
				} else {
					a.integrations.Validation = receipt
					if receipt.Status == "passed" || receipt.Status == "failed" {
						a.finishGenerationOperation(operation)
					}
				}
				if terminal {
					a.RefreshIntegrations()
				}
			})
			if terminal {
				return
			}
			select {
			case <-time.After(interval):
			case <-parent.Done():
				return
			}
		}
	}()
}
func (a *App) cancelGenerationValidation() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.ValidationOperation == "" {
		return
	}
	s.Busy = true
	operation := s.ValidationOperation
	revision := s.Generation.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var receipt any
		err := client.request(ctx, "DELETE", "/v1/generation/validations/"+url.PathEscape(operation), map[string]any{"expected_revision": revision, "operation_id": integrationOperation()}, &receipt)
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = "停止验证的结果未确认，请核对原验证"
			} else {
				s.Message = "已请求停止本机验证；远端请求结果仍以回执为准"
			}
			a.pollGenerationValidation(operation)
		})
	}()
}

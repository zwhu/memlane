package desktop

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func (a *App) loadProcessingSuppliers() {
	s := &a.processing
	s.ensureHTTP()
	h := &s.HTTP
	if a.libraryContext == nil || a.dispatch == nil || h.ProfilesLoading || h.CredentialBusy {
		return
	}
	h.ProfilesLoading = true
	h.Sequence++
	seq := h.Sequence
	owner, library := a.libraryContext, a.library
	epoch := s.HTTP.Epoch
	go func() {
		ctx, cancel := context.WithTimeout(owner, 5*time.Second)
		defer cancel()
		profiles, err := library.ProcessingSuppliers(ctx)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if h.Sequence != seq || a.library.Root != library.Root || a.processing.HTTP.Epoch != epoch {
				return
			}
			h.ProfilesLoading = false
			if err != nil {
				h.Error = "供应商配置读取失败；请重试。"
				return
			}
			h.ProfilesLoaded = true
			h.Error = ""
			h.Profiles = map[string]records.ProcessingSpec{}
			h.SupplierRevisions = map[string]int64{}
			for _, profile := range profiles {
				// Only the approved supplier directory can create local Provider targets.
				if !processingKnownSupplier(profile.Provider) {
					continue
				}
				h.Profiles[profile.Provider] = records.ProcessingSpec{Mode: "provider", Provider: profile.Provider, Endpoint: profile.Endpoint, Protocol: profile.Protocol, Credential: profile.Credential, CredentialRevision: profile.CredentialRevision}
				h.SupplierRevisions[profile.Provider] = profile.Revision
			}
			if pending := h.CredentialAttempt; pending != nil {
				if profile, ok := h.Profiles[pending.Provider]; ok && profile.Credential != pending.Credential && h.SupplierRevisions[pending.Provider] != pending.ExpectedRevision {
					h.CredentialAttempt = nil
					h.CredentialAttemptDigest = ""
				}
			}
			if len(h.Profiles) > 0 {
				a.readProcessingModels("provider", false)
			}
		})
	}()
}
func processingKnownSupplier(id string) bool {
	for _, template := range processingSupplierTemplates {
		if id == template.ID {
			return true
		}
	}
	return false
}
func (a *App) canSaveProcessingSupplier() bool {
	h := &a.processing.HTTP
	if !h.ProfilesLoaded || h.ProfilesLoading || h.CredentialBusy || !processingKnownSupplier(h.Editing) || !processingProviderEndpoint(h.Endpoint) || !processingHTTPProtocol(h.Protocol) {
		return false
	}
	if h.Secret != "" {
		return len(h.Secret) <= 8192 && !strings.ContainsAny(h.Secret, "\r\n")
	}
	old, ok := h.Profiles[h.Editing]
	return ok && old.Endpoint == h.Endpoint && old.Protocol == h.Protocol && old.Credential != "" && old.CredentialRevision > 0
}
func (a *App) saveProcessingSupplier() {
	s := &a.processing
	h := &s.HTTP
	if !a.canSaveProcessingSupplier() {
		return
	}
	if h.Secret == "" {
		h.View = ""
		h.Notice = "继续使用已保存的独立供应商配置。"
		a.readProcessingModels("provider", false)
		return
	}
	if a.libraryContext == nil || a.dispatch == nil {
		return
	}
	// Every new secret gets a fresh immutable reference, never another Agent's
	// credential or an address-mutated old reference.
	secret := []byte(h.Secret)
	fingerprint := records.MergeDigest([]string{h.Editing, h.Endpoint, h.Protocol, h.Secret})
	if h.CredentialAttempt == nil || h.CredentialAttemptDigest != fingerprint {
		h.CredentialAttempt = &records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, ExpectedRevision: h.SupplierRevisions[h.Editing], Provider: h.Editing, Endpoint: h.Endpoint, Protocol: h.Protocol}
		h.CredentialAttemptDigest = fingerprint
	}
	request := *h.CredentialAttempt
	request.Secret = secret
	h.CredentialBusy = true
	h.Error = ""
	h.Notice = "正在保存独立凭据…"
	h.Sequence++
	seq := h.Sequence
	owner, library := a.libraryContext, a.library
	epoch := s.HTTP.Epoch
	go func() {
		defer clear(secret)
		ctx, cancel := context.WithTimeout(owner, 10*time.Second)
		defer cancel()
		reference, err := library.SaveProcessingCredential(ctx, request)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if h.Sequence != seq || a.library.Root != library.Root || a.processing.HTTP.Epoch != epoch {
				return
			}
			h.CredentialBusy = false
			if err != nil {
				h.Error = "独立凭据未取得保存回执；原配置与输入保留，请重试。"
				return
			}
			if reference.Supplier == nil || reference.Supplier.Provider != request.Provider || reference.Supplier.Endpoint != request.Endpoint || reference.Supplier.Protocol != request.Protocol || reference.Credential == "" || reference.Revision < 1 {
				h.Error = "供应商保存回执不完整；请重新读取配置。"
				return
			}
			profile := records.ProcessingSpec{Mode: "provider", Provider: request.Provider, Endpoint: request.Endpoint, Protocol: request.Protocol, Credential: reference.Credential, CredentialRevision: reference.Revision}
			h.Profiles[request.Provider] = profile
			h.SupplierRevisions[request.Provider] = reference.Supplier.Revision
			h.ProfilesLoaded = true
			h.Secret = ""
			delete(h.SettingsDrafts, request.Provider)
			h.CredentialAttempt = nil
			h.CredentialAttemptDigest = ""
			h.Error = ""
			h.Notice = "独立凭据已保存；未验证模型、未切换当前使用。"
			h.View = ""
			if draft := s.Drafts["provider"]; draft.Provider == profile.Provider {
				profile.Model = draft.Model
				s.Drafts["provider"] = profile
			}
			catalog := s.catalog("provider")
			catalog.Sequence++
			catalog.Loading = false
			catalog.Loaded = false
			a.readProcessingModels("provider", false)
		})
	}()
}

func (a *App) readProcessingModels(mode string, more bool) {
	s := &a.processing
	s.ensureHTTP()
	catalog := s.catalog(mode)
	if a.libraryContext == nil || a.dispatch == nil || catalog.Loading {
		return
	}
	targets := []records.ProcessingSpec{}
	if mode == "magpie" {
		if !processingLocalGateway(s.HTTP.GatewayEndpoint) {
			catalog.Error = "仅支持带端口的本机 http 网关地址。"
			return
		}
		targets = append(targets, records.ProcessingSpec{Mode: "magpie", Endpoint: s.HTTP.GatewayEndpoint, Protocol: "openai-chat"})
	} else {
		for _, supplier := range processingSupplierTemplates {
			if profile, ok := s.HTTP.Profiles[supplier.ID]; ok {
				targets = append(targets, profile)
			}
		}
	}
	if len(targets) == 0 {
		catalog.Error = "请先保存供应商的独立配置。"
		return
	}
	catalog.Loading = true
	catalog.Sequence++
	seq := catalog.Sequence
	owner, library := a.libraryContext, a.library
	epoch := s.HTTP.Epoch
	prior := append([]records.ProcessingModel{}, catalog.Catalog.Models...)
	priorSources := map[string]records.ProcessingSpec{}
	for provider, source := range catalog.Sources {
		priorSources[provider] = source
	}
	after := map[string]string{}
	if more {
		for provider, cursor := range catalog.NextByProvider {
			after[provider] = cursor
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(owner, 20*time.Second)
		defer cancel()
		models := []records.ProcessingModel{}
		if more {
			models = prior
		}
		next := map[string]string{}
		sources := map[string]records.ProcessingSpec{}
		if more {
			for provider, source := range priorSources {
				sources[provider] = source
			}
		}
		errors := []string{}
		success := 0
		readMS := int64(0)
		for _, target := range targets {
			namespace := target.Provider
			if mode == "magpie" {
				namespace = "magpie"
			}
			if more && after[namespace] == "" {
				continue
			}
			page, err := library.ProcessingModels(ctx, records.ProcessingModelQuery{Spec: target, After: after[namespace]})
			if err != nil {
				errors = append(errors, processingSupplierName(namespace)+"："+processingHTTPError(err))
				continue
			}
			success++
			sources[namespace] = target
			readMS = max(readMS, page.ReadMS)
			for _, model := range page.Models {
				if mode == "provider" {
					model.Provider = target.Provider
				}
				duplicate := false
				for _, existing := range models {
					if existing.ID == model.ID && existing.Provider == model.Provider {
						duplicate = true
						break
					}
				}
				if !duplicate {
					models = append(models, model)
				}
			}
			if page.HasMore {
				next[namespace] = page.LastID
			}
		}
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if catalog.Sequence != seq || a.library.Root != library.Root || a.processing.HTTP.Epoch != epoch {
				return
			}
			catalog.Loading = false
			if success == 0 {
				catalog.Error = "模型目录读取失败：" + strings.Join(errors, "；") + "。保留上次列表，可刷新重试。"
				return
			}
			catalog.Error = ""
			if len(errors) > 0 {
				catalog.Error = "部分模型目录读取失败：" + strings.Join(errors, "、") + "。可刷新重试。"
			}
			catalog.Loaded = true
			catalog.Catalog = records.ProcessingModelCatalog{Models: models, HasMore: len(next) > 0, ReadMS: readMS}
			catalog.NextByProvider = next
			catalog.Sources = sources
			if mode == "magpie" {
				catalog.Catalog.Endpoint = targets[0].Endpoint
				catalog.Catalog.Protocol = targets[0].Protocol
			}
		})
	}()
}

func (a *App) chooseProcessingHTTPModel(mode, provider, id string) {
	s := &a.processing
	s.ensureHTTP()
	if id == "" {
		return
	}
	spec := records.ProcessingSpec{}
	if mode == "magpie" {
		catalog := s.catalog(mode)
		if !catalog.Loaded || catalog.Catalog.Endpoint != s.HTTP.GatewayEndpoint {
			return
		}
		found := false
		for _, model := range catalog.Catalog.Models {
			if model.ID == id {
				found = true
				break
			}
		}
		if !found {
			return
		}
		spec = records.ProcessingSpec{Mode: "magpie", Endpoint: catalog.Catalog.Endpoint, Protocol: catalog.Catalog.Protocol, Model: id}
	} else {
		profile, ok := s.HTTP.Profiles[provider]
		if !ok {
			return
		}
		catalog := s.catalog(mode)
		manual := false
		for _, model := range catalog.Manual {
			if model.ID == id && model.Provider == provider {
				manual = true
				break
			}
		}
		source, loaded := catalog.Sources[provider]
		if !manual {
			if !catalog.Loaded || !loaded || source != profile {
				return
			}
			found := false
			for _, model := range catalog.Catalog.Models {
				if model.ID == id && model.Provider == provider {
					found = true
					break
				}
			}
			if !found {
				return
			}
		}
		spec = profile
		spec.Model = id
	}
	s.Drafts[mode] = spec
	s.Error = ""
	s.Notice = "已选择模型；保存和验证后，仍需明确设为当前使用。"
}
func (a *App) addProcessingManualModel() {
	s := &a.processing
	h := &s.HTTP
	id := strings.TrimSpace(h.ManualID)
	profile, ok := h.Profiles[h.ManualSupplier]
	if !ok || id == "" || len(id) > 256 {
		h.Error = "选择已配置供应商并填写其实际模型 ID。"
		return
	}
	catalog := s.catalog("provider")
	for _, model := range append(append([]records.ProcessingModel{}, catalog.Catalog.Models...), catalog.Manual...) {
		if model.ID == id && model.Provider == h.ManualSupplier {
			a.chooseProcessingHTTPModel("provider", h.ManualSupplier, id)
			h.View = ""
			h.ManualID = ""
			return
		}
	}
	catalog.Manual = append(catalog.Manual, records.ProcessingModel{ID: id, Provider: profile.Provider})
	a.chooseProcessingHTTPModel("provider", profile.Provider, id)
	h.ManualID = ""
	h.View = ""
	h.Error = ""
}
func (a *App) openProcessingMagpie(_ *ui.Context) {
	if a.libraryContext == nil || a.dispatch == nil {
		return
	}
	owner, library := a.libraryContext, a.library
	epoch := a.processing.HTTP.Epoch
	go func() {
		ctx, cancel := context.WithTimeout(owner, 5*time.Second)
		defer cancel()
		err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Magpie").Run()
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.library.Root != library.Root || a.processing.HTTP.Epoch != epoch {
				return
			}
			if err != nil {
				a.processing.HTTP.Error = "未能打开 Magpie，请先在本机安装或打开该应用。"
			}
		})
	}()
}

func processingHTTPError(err error) string {
	var service *serviceipc.ServiceError
	if errors.As(err, &service) {
		switch service.Code {
		case "provider_authentication_failed", "independent_credential_unavailable":
			return "独立凭据失效或权限不足"
		case "provider_rate_limited":
			return "服务额度或请求频率受限"
		case "model_catalog_invalid":
			return "服务返回的模型目录格式不正确"
		case "provider_timeout":
			return "服务请求超时"
		case "model_not_available":
			return "所选模型已不在当前目录中"
		case "model_catalog_incomplete":
			return "模型目录尚未读取完整"
		case "provider_request_invalid":
			return "服务未接受该请求或结构化输出格式"
		case "provider_unavailable":
			return "服务暂时无法连接"
		case "invalid_configuration":
			return "地址或接口格式无效"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "读取超时"
	}
	return "未取得有效目录回执"
}

func processingActionError(err error, mode string) string {
	if mode == "provider" || mode == "magpie" {
		label := processingHTTPError(err)
		if mode == "magpie" {
			label = strings.ReplaceAll(label, "独立凭据", "网关访问")
		}
		if label == "未取得有效目录回执" {
			return "未取得有效操作回执；请重试或重新读取配置。"
		}
		return label
	}
	// Agent errors also use the service's bounded code, not an arbitrary message.
	var service *serviceipc.ServiceError
	if errors.As(err, &service) {
		return safeProcessingCode(service.Code)
	}
	return "操作未确认，请重试或重新读取配置。"
}
func safeProcessingCode(code string) string {
	if code == "" || len(code) > 64 {
		return "unknown_error"
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "unknown_error"
		}
	}
	return code
}

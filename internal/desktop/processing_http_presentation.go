package desktop

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type processingSupplierTemplate struct{ ID, Name, Endpoint, Protocol, Mark string }

// This is the approved configuration directory, never a fabricated model list.
var processingSupplierTemplates = []processingSupplierTemplate{
	{"deepseek", "DeepSeek", "https://api.deepseek.com/v1", "openai-chat", "D"},
	{"moonshot", "Kimi", "https://api.moonshot.ai/v1", "openai-chat", "K"},
	{"zhipu", "Zhipu GLM", "https://open.bigmodel.cn/api/paas/v4", "openai-chat", "G"},
	{"openai", "OpenAI", "https://api.openai.com/v1", "openai-responses", "O"},
	{"custom", "自定义接口", "", "openai-chat", "…"},
}

func processingSupplierName(id string) string {
	for _, s := range processingSupplierTemplates {
		if s.ID == id {
			return s.Name
		}
	}
	if id != "" {
		return id
	}
	return "供应商未报告"
}
func processingLocalGateway(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") && u.Port() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func processingHTTPProtocol(protocol string) bool {
	return protocol == "openai-chat" || protocol == "openai-responses" || protocol == "anthropic-messages"
}
func processingProviderEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return len(endpoint) <= 2048 && strings.TrimSpace(endpoint) == endpoint && err == nil && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}

type processingCatalogUI struct {
	Catalog                     records.ProcessingModelCatalog
	Sources                     map[string]records.ProcessingSpec
	NextByProvider              map[string]string
	Query, Filter, After, Error string
	Loading, Loaded             bool
	Sequence                    uint64
	Favorites                   map[string]bool
	Manual                      []records.ProcessingModel
	Scroll                      ui.ScrollState
}

type processingHTTPUI struct {
	Epoch                                                                              string
	CredentialAttempt                                                                  *records.ProcessingCredentialWrite
	CredentialAttemptDigest                                                            string
	View, Editing, Endpoint, Protocol, Secret, Error, Notice, ManualSupplier, ManualID string
	GatewayEndpoint                                                                    string
	GatewaySettings                                                                    bool
	GatewayTouched                                                                     bool
	// Profiles contain public references only; the password field is never
	// populated from a saved profile, Agent configuration or gateway account.
	Profiles                                        map[string]records.ProcessingSpec
	ProfilesLoaded, ProfilesLoading, CredentialBusy bool
	Sequence                                        uint64
	SupplierRevisions                               map[string]int64
	Catalogs                                        map[string]*processingCatalogUI
}

func (s *ProcessingUI) ensureHTTP() {
	if s.HTTP.Epoch == "" {
		s.HTTP.Epoch = uuid.NewString()
	}
	if s.HTTP.Profiles == nil {
		s.HTTP.Profiles = map[string]records.ProcessingSpec{}
	}
	if s.HTTP.SupplierRevisions == nil {
		s.HTTP.SupplierRevisions = map[string]int64{}
	}
	if s.HTTP.Catalogs == nil {
		s.HTTP.Catalogs = map[string]*processingCatalogUI{}
	}
	if s.HTTP.GatewayEndpoint == "" {
		s.HTTP.GatewayEndpoint = "http://127.0.0.1:3425/v1"
		if saved, ok := s.Configuration.Saved["magpie"]; ok && saved.Spec.Endpoint != "" {
			s.HTTP.GatewayEndpoint = saved.Spec.Endpoint
		}
	}
}
func (s *ProcessingUI) catalog(mode string) *processingCatalogUI {
	s.ensureHTTP()
	if s.HTTP.Catalogs[mode] == nil {
		s.HTTP.Catalogs[mode] = &processingCatalogUI{Filter: "all", Favorites: map[string]bool{}}
	}
	return s.HTTP.Catalogs[mode]
}
func processingModelKey(spec records.ProcessingSpec, model records.ProcessingModel) string {
	return spec.Mode + "\x00" + spec.Provider + "\x00" + spec.Endpoint + "\x00" + spec.Protocol + "\x00" + model.ID
}

func (a *App) processingProviderPage(c *ui.Context) {
	s := &a.processing
	s.ensureHTTP()
	if !s.HTTP.ProfilesLoaded && !s.HTTP.ProfilesLoading && s.HTTP.Error == "" {
		a.loadProcessingSuppliers()
	}
	switch s.HTTP.View {
	case "suppliers":
		a.processingSupplierManager(c)
		return
	case "manual":
		a.processingManualModel(c)
		return
	}
	a.processingModelPicker(c, "provider")
	if s.HTTP.Error != "" {
		ui.Text(c, s.HTTP.Error).FontSize(design.Small).TextColor(design.Muted)
		quietButton(c, "重新读取供应商配置").Disabled(s.HTTP.ProfilesLoading).OnClick(a.loadProcessingSuppliers)
	}
	if s.HTTP.Notice != "" {
		ui.Text(c, s.HTTP.Notice).FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) processingSupplierManager(c *ui.Context) {
	s := &a.processing
	h := &s.HTTP
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "供应商配置").Bold()
		ui.Spacer(c)
		quietButton(c, "返回模型列表").Disabled(h.CredentialBusy).OnClick(func() { h.View = ""; h.Secret = "" })
	})
	for _, supplier := range processingSupplierTemplates {
		profile, configured := h.Profiles[supplier.ID]
		ui.Button(c.Key("processing-supplier-"+supplier.ID), "").Label("配置 "+supplier.Name).Disabled(h.CredentialBusy).Justify(ui.Start).Padding(12, 14).Border(1, design.Border).Radius(6).Background(ui.Transparent).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				processingSupplierIcon(c, supplier.ID, 20)
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, supplier.Name).Bold()
					address := supplier.Endpoint
					if configured {
						address = profile.Endpoint
					}
					if address == "" {
						address = "自定义服务地址"
					}
					ui.Text(c, address).FontSize(design.Small).TextColor(design.Muted).SingleLine().MinWidth(0)
				})
				state := "未配置"
				if !h.ProfilesLoaded {
					state = "配置待读取"
				}
				if configured {
					state = "已配置独立凭据"
				}
				ui.Text(c, state).FontSize(10).TextColor(design.Muted)
			})
		}).OnClick(func() {
			h.Editing = supplier.ID
			h.Endpoint, h.Protocol, h.Secret = supplier.Endpoint, supplier.Protocol, ""
			if configured {
				h.Endpoint, h.Protocol = profile.Endpoint, profile.Protocol
			}
			h.Error = ""
		})
	}
	if h.Editing != "" {
		ui.Column(c).Padding(16).Gap(10).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "配置 "+processingSupplierName(h.Editing)).Bold()
			ui.Text(c, "API 地址").FontSize(design.Small)
			ui.TextInput(c.Key("processing-provider-endpoint"), &h.Endpoint).Label("供应商 API 地址").Height(32).Disabled(h.CredentialBusy)
			ui.Text(c, "接口格式").FontSize(design.Small)
			ui.Select(c.Key("processing-provider-protocol"), &h.Protocol, []string{"openai-chat", "openai-responses", "anthropic-messages"}).Label("供应商接口格式").Disabled(h.CredentialBusy)
			ui.Text(c, "API Key").FontSize(design.Small)
			placeholder := "输入此供应商的独立密钥"
			if old, ok := h.Profiles[h.Editing]; ok && old.Endpoint == h.Endpoint && old.Protocol == h.Protocol {
				placeholder = "已有独立凭据；留空保留"
			}
			ui.TextInput(c.Key("processing-provider-secret"), &h.Secret).Password().Label("供应商独立 API Key").Placeholder(placeholder).Height(32).Disabled(h.CredentialBusy)
			ui.Text(c, "同一供应商的模型复用此配置。修改地址或接口格式后需重新提供密钥。").FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "保存供应商配置").Disabled(!a.canSaveProcessingSupplier()).AlignSelf(ui.Start).OnClick(a.saveProcessingSupplier)
		})
	}
	if h.Error != "" {
		{
			quietButton(c, "重新读取供应商配置").Disabled(h.ProfilesLoading).OnClick(a.loadProcessingSuppliers)
		}
		ui.Text(c, h.Error).FontSize(design.Small).TextColor(design.Muted)
	}
	if h.Notice != "" {
		ui.Text(c, h.Notice).FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) processingMagpiePage(c *ui.Context) {
	s := &a.processing
	s.ensureHTTP()
	h := &s.HTTP
	catalog := s.catalog("magpie")
	if h.GatewaySettings {
		ui.Row(c).Children(func() {
			ui.Text(c, "本机连接设置").Bold()
			ui.Spacer(c)
			quietButton(c, "返回 Magpie 模型").OnClick(func() { h.GatewaySettings = false })
		})
		ui.Text(c, "本机网关地址").FontSize(design.Small)
		ui.TextInput(c.Key("processing-magpie-endpoint"), &h.GatewayEndpoint).Label("Magpie 本机网关地址").Height(32).OnChange(func() { h.GatewayTouched = true })
		ui.Text(c, "仅在 Magpie 使用非默认本机端口时修改；无需访问凭据。").FontSize(design.Small).TextColor(design.Muted)
		quietButton(c, "重新检测").Disabled(catalog.Loading || !processingLocalGateway(h.GatewayEndpoint)).OnClick(func() { h.GatewaySettings = false; a.readProcessingModels("magpie", false) })
		return
	}
	if catalog.Loaded && catalog.Catalog.Endpoint != h.GatewayEndpoint {
		catalog.Sequence++
		catalog.Loading = false
		catalog.Loaded = false
		catalog.Error = ""
	}
	if !catalog.Loaded && !catalog.Loading && catalog.Error == "" {
		a.readProcessingModels("magpie", false)
	}
	if !catalog.Loaded {
		ui.Column(c).MinHeight(230).Padding(30).Gap(14).AlignItems(ui.Center).Justify(ui.Center).Border(1, design.Border).Radius(7).Children(func() {
			title := "未检测到正在运行的 Magpie"
			if catalog.Error != "" {
				title = "暂时无法连接 Magpie"
			}
			if a.libraryContext == nil {
				title = "等待本机记忆服务"
			}
			if catalog.Loading {
				title = "正在检测本机 Magpie"
			}
			ui.Text(c, title).Bold()
			description := "打开 Magpie 后，重新检测即可使用已配置的模型。"
			if catalog.Loading {
				description = "连接后自动读取模型目录。"
			}
			ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
			ui.Row(c).Gap(12).Children(func() {
				quietButton(c, "打开 Magpie").OnClick(func() { a.openProcessingMagpie(c) })
				quietButton(c, "连接设置").OnClick(func() { h.GatewaySettings = true })
				quietButton(c, "重新检测").Disabled(catalog.Loading).OnClick(func() { a.readProcessingModels("magpie", false) })
			})
			if catalog.Error != "" {
				ui.Text(c, catalog.Error).FontSize(design.Small).TextColor(design.Muted)
			}
		})
		return
	}
	ui.Row(c).Gap(12).Children(func() {
		label := "Magpie 已连接"
		if catalog.Error != "" {
			label = "Magpie 状态未确认，保留上次目录"
		}
		ui.Text(c, label).Bold()
		quietButton(c, "连接设置").OnClick(func() { h.GatewaySettings = true })
		quietButton(c, "刷新模型").Disabled(catalog.Loading).OnClick(func() { a.readProcessingModels("magpie", false) })
	})
	a.processingModelPicker(c, "magpie")
}

func (a *App) processingModelPicker(c *ui.Context, mode string) {
	s := &a.processing
	catalog := s.catalog(mode)
	if spec := s.Drafts[mode]; spec.Model != "" && (!s.sameSavedDraft() || a.firstLaunch.Open) {
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "记忆处理模型").FontSize(design.Small).TextColor(design.Muted)
			ui.Text(c, spec.Model).Bold().SingleLine().MinWidth(0)
		})
	}
	ui.Column(c).Border(1, design.Border).Radius(7).Children(func() {
		ui.Row(c).Gap(10).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
			ui.TextInput(c.Key("processing-model-search-"+mode), &catalog.Query).Label("搜索处理模型").Placeholder("搜索模型名称或模型 ID").Grow(1).MinWidth(0).BorderWidth(0)
			if mode == "provider" {
				quietButton(c, "供应商配置").OnClick(func() { s.HTTP.View = "suppliers" })
			} else {
				quietButton(c, "在 Magpie 中管理").OnClick(func() { a.openProcessingMagpie(c) })
			}
		})
		ui.Row(c).Height(320).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(54).Shrink(0).Padding(6).Gap(6).Background(ui.Hex("#f5f8fc")).Children(func() {
				for _, filter := range []struct{ id, label, mark string }{{"all", "全部模型", "▦"}, {"favorites", "常用模型", "☆"}, {"deepseek", "DeepSeek", "D"}, {"moonshot", "Kimi", "K"}, {"zhipu", "Zhipu GLM", "G"}, {"openai", "OpenAI", "O"}} {
					b := ui.Button(c.Key("processing-model-filter-"+mode+filter.id), "").Label(filter.label).Size(36, 34).Padding(0).BorderWidth(0).Tooltip(filter.label).Children(func() {
						if filter.id == "all" || filter.id == "favorites" {
							ui.Text(c, filter.mark)
						} else {
							processingSupplierIcon(c, filter.id, 18)
						}
					})
					if catalog.Filter == filter.id {
						b.Background(design.Selected)
					} else {
						b.Background(ui.Transparent)
					}
					b.OnClick(func() { catalog.Filter = filter.id })
				}
			})
			ui.Scroll(c).Key("processing-model-list-" + mode).TrackScroll(&catalog.Scroll).Grow(1).MinWidth(0).Padding(12).Gap(4).Children(func() { a.processingModelRows(c, mode) })
		})
		ui.Row(c).Gap(10).Padding(8, 12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			label := "模型目录待读取"
			if catalog.Loading {
				label = "正在读取模型目录"
			} else if catalog.Loaded {
				label = fmt.Sprintf("%d 个模型", len(catalog.Catalog.Models))
			}
			ui.Text(c, label).FontSize(10).TextColor(design.Muted)
			ui.Spacer(c)
			if mode == "provider" {
				quietButton(c, "刷新模型").Disabled(catalog.Loading || len(s.HTTP.Profiles) == 0).OnClick(func() { a.readProcessingModels(mode, false) })
				quietButton(c, "添加模型 ID").OnClick(func() { s.HTTP.View = "manual" })
			}
			if catalog.Catalog.HasMore {
				quietButton(c, "更多模型").Disabled(catalog.Loading).OnClick(func() { a.readProcessingModels(mode, true) })
			}
		})
	})
	if catalog.Error != "" {
		ui.Text(c, catalog.Error).FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) processingModelRows(c *ui.Context, mode string) {
	s := &a.processing
	catalog := s.catalog(mode)
	models := []records.ProcessingModel{}
	seen := map[string]bool{}
	for _, model := range append(append([]records.ProcessingModel{}, catalog.Catalog.Models...), catalog.Manual...) {
		key := model.Provider + "\x00" + model.ID
		if !seen[key] {
			seen[key] = true
			models = append(models, model)
		}
	}
	query := strings.ToLower(strings.TrimSpace(catalog.Query))
	groups := map[string][]records.ProcessingModel{}
	spec := s.Drafts[mode]
	for _, model := range models {
		provider := model.Provider
		if provider == "" && mode == "provider" {
			provider = spec.Provider
		}
		if catalog.Filter != "all" && catalog.Filter != "favorites" && provider != catalog.Filter {
			continue
		}
		target := spec
		if mode == "provider" {
			if profile, ok := s.HTTP.Profiles[provider]; ok {
				target = profile
			}
		}
		key := processingModelKey(target, model)
		if catalog.Filter == "favorites" && !catalog.Favorites[key] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(model.ID+" "+model.Name+" "+processingSupplierName(provider)), query) {
			continue
		}
		groups[provider] = append(groups[provider], model)
	}
	keys := []string{}
	for provider := range groups {
		keys = append(keys, provider)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		title := "没有找到匹配的模型"
		if !catalog.Loaded && len(catalog.Manual) == 0 {
			title = "先配置供应商并读取模型目录"
		}
		if mode == "provider" && catalog.Loaded && len(models) == 0 {
			title = "供应商没有返回可用模型"
		}
		if mode == "magpie" && catalog.Loaded && len(models) == 0 {
			title = "Magpie 没有返回可用模型"
		}
		ui.Text(c, title).FontSize(design.Small).TextColor(design.Muted).Padding(16, 0)
	}
	for _, provider := range keys {
		ui.Text(c, processingSupplierName(provider)).FontSize(10).TextColor(design.Muted).Margin(8, 0, 4, 0)
		for _, model := range groups[provider] {
			target := spec
			if mode == "provider" {
				if profile, ok := s.HTTP.Profiles[provider]; ok {
					target = profile
				}
			}
			key := processingModelKey(target, model)
			manual := false
			for _, added := range catalog.Manual {
				if added.ID == model.ID && added.Provider == provider {
					manual = true
					break
				}
			}
			selectable := mode == "magpie" && catalog.Loaded && catalog.Catalog.Endpoint == s.HTTP.GatewayEndpoint || mode == "provider" && (manual || catalog.Loaded && catalog.Sources[provider] == target)
			ui.Row(c).Gap(6).Children(func() {
				name := model.Name
				if name == "" {
					name = model.ID
				}
				b := ui.Button(c.Key("processing-model-"+key), "").Label("选择模型 "+model.ID).Disabled(!selectable).Justify(ui.Start).Grow(1).MinWidth(0).Padding(9, 8).Gap(10).BorderWidth(0).Background(ui.Transparent).Children(func() {
					processingSupplierIcon(c, provider, 18)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, name).SingleLine().MinWidth(0)
						subtitle := model.ID
						if manual {
							subtitle += " · 手动添加，待验证"
						}
						if mode == "magpie" {
							subtitle += " · via Magpie"
						}
						ui.Text(c, subtitle).FontSize(10).TextColor(design.Muted).SingleLine().MinWidth(0)
					})
					if spec.Model == model.ID && (mode == "magpie" && spec.Endpoint == catalog.Catalog.Endpoint || mode == "provider" && spec.Provider == provider && spec.Endpoint == target.Endpoint && spec.Protocol == target.Protocol && spec.Credential == target.Credential && spec.CredentialRevision == target.CredentialRevision) {
						ui.Text(c, "✓")
					}
				})
				if spec.Model == model.ID && (mode == "magpie" && spec.Endpoint == catalog.Catalog.Endpoint || mode == "provider" && spec.Provider == provider && spec.Endpoint == target.Endpoint && spec.Protocol == target.Protocol && spec.Credential == target.Credential && spec.CredentialRevision == target.CredentialRevision) {
					b.Background(design.ListSelected)
				}
				b.OnClick(func() { a.chooseProcessingHTTPModel(mode, provider, model.ID) })
				star := "☆"
				if catalog.Favorites[key] {
					star = "★"
				}
				quietButton(c, star).Key("processing-model-favorite-" + key).Tooltip("常用模型：" + model.ID).OnClick(func() { catalog.Favorites[key] = !catalog.Favorites[key] })
			})
		}
	}
}

func (a *App) processingManualModel(c *ui.Context) {
	h := &a.processing.HTTP
	ui.Row(c).Children(func() {
		ui.Text(c, "添加模型 ID").Bold()
		ui.Spacer(c)
		quietButton(c, "返回模型列表").OnClick(func() { h.View = "" })
	})
	options := []string{}
	for _, supplier := range processingSupplierTemplates {
		if _, ok := h.Profiles[supplier.ID]; ok {
			options = append(options, supplier.ID)
		}
	}
	if h.ManualSupplier == "" && len(options) > 0 {
		h.ManualSupplier = options[0]
	}
	ui.Text(c, "供应商").FontSize(design.Small)
	if len(options) == 0 {
		ui.Text(c, "先保存供应商的独立配置。").FontSize(design.Small)
		quietButton(c, "供应商配置").OnClick(func() { h.View = "suppliers" })
		return
	}
	ui.Select(c.Key("processing-manual-supplier"), &h.ManualSupplier, options).Label("手动模型所属供应商")
	ui.TextInput(c.Key("processing-manual-id"), &h.ManualID).Label("实际供应商模型 ID").Placeholder("输入该供应商提供的模型 ID").Height(32)
	quietButton(c, "添加并选择模型").Disabled(strings.TrimSpace(h.ManualID) == "" || len(h.ManualID) > 256).OnClick(a.addProcessingManualModel)
	if h.Error != "" {
		ui.Text(c, h.Error).FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) processingHTTPReceipt(c *ui.Context, receipt records.ProcessingHTTPReceipt) {
	ui.Column(c).Gap(6).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
		title := "本次模型回执"
		if !a.processing.sameSavedDraft() {
			title = "原保存配置的模型回执"
		}
		ui.Text(c, title).Bold()
		meta(c, "请求模型", receipt.RequestedModel)
		reported := receipt.ReportedModel
		if reported == "" {
			reported = "上游未报告"
		}
		meta(c, "返回模型", reported)
		if a.processing.Mode == "magpie" {
			provider := receipt.GatewayProvider
			if provider == "" {
				provider = "网关未报告"
			}
			meta(c, "网关报告供应商", provider)
			model := receipt.GatewayModel
			if model == "" {
				model = "网关未报告"
			}
			meta(c, "网关报告模型", model)
		}
		if receipt.LocalCancelled {
			ui.Text(c, "本地请求已取消；上游是否停止及计费状态未确认。").FontSize(design.Small).TextColor(design.Muted)
		}
	})
}

var processingSupplierIcons = map[string]*ui.SVG{
	"deepseek": environmentSVG("processing-provider-deepseek"),
	"moonshot": environmentSVG("processing-provider-moonshot"),
	"zhipu":    environmentSVG("processing-provider-zhipu"),
	"openai":   environmentSVG("processing-provider-openai"),
}

func processingSupplierIcon(c *ui.Context, id string, size float32) {
	if icon := processingSupplierIcons[id]; icon != nil {
		ui.Icon(c, icon).FontSize(size).TextColor(design.Muted)
	} else {
		ui.Icon(c, icons["全部记忆"]).FontSize(size).TextColor(design.Muted)
	}
}

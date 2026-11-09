package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/modelbridge"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type OfficialPreparationUI struct {
	Status                                             official.Status
	Provider, Model, Endpoint, Key                     string
	ModelPresent, EditingModel, Busy                   bool
	Sequence                                           int64
	Cancel                                             context.CancelFunc
	ChangedAt                                          time.Time
	UsePi                                              bool
	PiExecutable, PiAuthDirectory, PiProvider, PiModel string
}

func (a *App) configureOfficialPreparation(controller *RuntimeController) {
	if controller.Config.Official == nil {
		return
	}
	controller.Official = &official.Controller{Root: filepath.Join(controller.Config.Root, "official"), Config: *controller.Config.Official}
	s := &a.officialPreparation
	s.UsePi = true
	home, _ := os.UserHomeDir()
	s.PiAuthDirectory = os.Getenv("PI_CODING_AGENT_DIR")
	if s.PiAuthDirectory == "" {
		s.PiAuthDirectory = filepath.Join(home, ".pi/agent")
	}
	var selected struct {
		Provider string `json:"defaultProvider"`
		Model    string `json:"defaultModel"`
	}
	if raw, err := os.ReadFile(filepath.Join(s.PiAuthDirectory, "settings.json")); err == nil && len(raw) < 65536 && json.Unmarshal(raw, &selected) == nil {
		s.PiProvider, s.PiModel = selected.Provider, selected.Model
	}
	if raw, err := os.ReadFile(filepath.Join(controller.Config.Root, "official-onboarding.json")); err == nil && len(raw) < 65536 {
		_ = json.Unmarshal(raw, &s.Status)
	}
	if s.Status.Phase == "ready" {
		s.Status.Phase = "requires_verification"
		s.Status.Message = "已有组件与模型校验记录；本次服务可用性仍需复核。确认准备后复用组件并验证当前服务。"
	}
	if s.Status.Phase == "preparing" || s.Status.Phase == "verifying" || s.Status.Phase == "downloading" || s.Status.Phase == "installing" || s.Status.Phase == "checking" {
		s.Status.Phase = "interrupted"
		s.Status.Message = "上次官方环境准备已中断，请核对后重试。"
	}
	if settings, err := controller.Official.Settings(); err == nil {
		s.ModelPresent = settings.ModelBridge != nil || (settings.Environment["LLM_API_KEY"] != "" && settings.Environment["LLM_MODEL"] != "")
		if settings.ModelBridge != nil {
			s.PiExecutable, s.PiAuthDirectory, s.PiProvider, s.PiModel = settings.ModelBridge.Executable, settings.ModelBridge.AuthHome, settings.ModelBridge.Provider, settings.ModelBridge.Model
		} else if s.ModelPresent {
			s.UsePi = false
		}
		s.Provider = settings.Environment["LLM_PROVIDER"]
		s.Model = settings.Environment["LLM_MODEL"]
		s.Endpoint = settings.Environment["LLM_ENDPOINT"]
	}
}
func (a *App) saveOfficialPi() {
	r := a.firstLaunch.Controller
	s := &a.officialPreparation
	if r == nil || r.Official == nil || s.Busy {
		return
	}
	if s.PiExecutable == "" {
		for _, found := range a.agents.Found {
			if found.Agent == "pi" && found.ConnectionError() == nil {
				s.PiExecutable = found.Path
				break
			}
		}
	}
	if s.PiExecutable == "" {
		s.Status.Message = "未发现可用 pi，请先检查本机 Agent 安装。"
		return
	}
	if err := r.Official.SelectPiBridge(modelbridge.Config{Executable: s.PiExecutable, AuthHome: s.PiAuthDirectory, Provider: s.PiProvider, Model: s.PiModel, Helper: a.agents.Bridge}); err != nil {
		s.Status.Message = "提炼 Agent 未保存：" + err.Error()
		return
	}
	s.ModelPresent = true
	s.EditingModel = false
	s.Model = "pi · " + s.PiProvider + " / " + s.PiModel
	s.Status.Phase = "configured"
	s.Status.Message = "已选择 pi；模型可用性尚待真实验证。"
	a.saveOfficialStatus()
}
func (a *App) saveOfficialStatus() {
	r := a.firstLaunch.Controller
	if r == nil {
		return
	}
	_ = serviceipc.WriteJSON(filepath.Join(r.Config.Root, "official-onboarding.json"), a.officialPreparation.Status)
}
func (a *App) saveOfficialModel() {
	r := a.firstLaunch.Controller
	if r == nil || r.Official == nil {
		return
	}
	s := &a.officialPreparation
	settings, err := r.Official.Settings()
	if err != nil {
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			s.Status.Message = err.Error()
			return
		}
		settings = official.Settings{Environment: map[string]string{"DEFAULT_USER_EMAIL": "memlane-local@example.com", "DEFAULT_USER_PASSWORD": hex.EncodeToString(random), "EMBEDDING_PROVIDER": "fastembed", "EMBEDDING_MODEL": "BAAI/bge-small-zh-v1.5", "EMBEDDING_DIMENSIONS": "512", "EMBEDDING_MAX_TOKENS": "512"}}
	}
	if s.Key != "" {
		settings.Environment["LLM_API_KEY"] = s.Key
	}
	s.Key = ""
	settings.Environment["LLM_PROVIDER"] = s.Provider
	settings.Environment["LLM_MODEL"] = s.Model
	settings.Environment["LLM_ENDPOINT"] = s.Endpoint
	if settings.Environment["LLM_API_KEY"] == "" || s.Model == "" {
		s.Status.Message = "请提供 Cognee 独立模型和授权。"
		return
	}
	if err = r.Official.SaveSettings(settings); err != nil {
		s.Status.Message = "模型条件未保存：" + err.Error()
		return
	}
	s.ModelPresent = true
	s.EditingModel = false
	s.Status.Phase = "configured"
	s.Status.Message = "模型条件已保存，尚未验证可用。"
	a.saveOfficialStatus()
}
func (a *App) prepareOfficial() {
	r := a.firstLaunch.Controller
	if r == nil || r.Official == nil {
		return
	}
	s := &a.officialPreparation
	if s.Busy {
		return
	}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	s.Status = official.Status{Phase: "preparing", Message: "验证提炼 Agent 与已准备的记忆服务…"}
	s.ChangedAt = time.Now()
	a.saveOfficialStatus()
	ctx, cancel := context.WithTimeout(a.libraryContext, 20*time.Minute)
	s.Cancel = cancel
	go func() {
		defer cancel()
		err := r.Official.Prepare(ctx, func(status official.Status) {
			a.dispatch(func() {
				if s.Sequence != sequence {
					return
				}
				if s.Status.Phase != status.Phase {
					s.ChangedAt = time.Now()
				}
				s.Status = status
				if status.Endpoint != "" {
					a.cognee.Endpoint = status.Endpoint
				}
				a.saveOfficialStatus()
			})
		})
		cancelled := ctx.Err() != nil
		a.dispatch(func() {
			if s.Sequence != sequence {
				return
			}
			s.Busy = false
			s.Cancel = nil
			if err != nil {
				s.Status.Phase = "failed"
				s.Status.Message = "官方环境准备失败：" + err.Error()
			}
			if cancelled {
				s.Status.Phase = "cancelled"
				s.Status.Message = "本轮准备已取消，已校验组件保留，未验证为可用。"
			}
			a.saveOfficialStatus()
		})
	}()
}
func (a *App) cogneePreparation(c *ui.Context) {
	s := &a.officialPreparation
	r := a.firstLaunch.Controller
	if r == nil {
		return
	}
	ui.Text(c, "提炼 Agent 与记忆服务").Bold()
	if r.Official == nil {
		ui.Text(c, "尚无已配置的官方服务组件来源。请配置真实服务与模型组件后准备，或绑定兼容的已有本机服务。").LineHeight(1.7)
		return
	}
	if s.Busy || s.Status.Phase == "ready" || s.Status.Phase == "failed" || s.Status.Phase == "cancelled" {
		stage := s.Status.Phase
		if s.Busy && stage != "verifying" {
			stage = "preparing"
		}
		a.preparationBlocks(c, "official-preparation-blocks", stage, a.firstLaunch.State.ReducedMotion, s.ChangedAt)
	}
	ui.Text(c, "记忆服务和本地依赖在初始化时统一准备。提炼通过本机 Agent 执行，复用该 Agent 的登录与模型配置。").FontSize(design.Small).LineHeight(1.7)
	if s.ModelPresent && !s.EditingModel {
		label := s.Model
		if s.UsePi {
			label = "pi · " + s.PiProvider + " / " + s.PiModel
		}
		meta(c, "提炼执行者", label+" · 已配置")
		quietButton(c, "修改提炼执行者").Disabled(s.Busy).OnClick(func() { s.EditingModel = true })
	} else if s.UsePi {
		meta(c, "提炼 Agent", "pi")
		ui.TextInput(c.Key("pi-model-auth"), &s.PiAuthDirectory).Label("pi 配置目录").Disabled(s.Busy)
		ui.TextInput(c.Key("pi-model-provider"), &s.PiProvider).Label("当前模型服务").Disabled(s.Busy)
		ui.TextInput(c.Key("pi-model-name"), &s.PiModel).Label("当前模型").Disabled(s.Busy)
		actionButton(c, "使用 pi 作为提炼 Agent", nil).Key("pi-model-save").Disabled(s.Busy).OnClick(a.saveOfficialPi)
	} else {
		ui.TextInput(c.Key("cognee-model-provider"), &s.Provider).Placeholder("Cognee LLM provider").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-name"), &s.Model).Placeholder("用户选定的模型 ID").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-endpoint"), &s.Endpoint).Placeholder("该模型的 API 地址").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-key"), &s.Key).Password().Placeholder("Cognee 独立模型凭据").Disabled(s.Busy)
		actionButton(c, "保存独立模型条件", nil).Key("cognee-model-save").Disabled(s.Busy).OnClick(a.saveOfficialModel)
	}
	ui.Text(c, "验证将复用已准备的组件，并通过所选 Agent 执行一次小型模型探测，使用该 Agent 的模型额度。采集与项目范围另行确认。").FontSize(design.Small).LineHeight(1.7)
	ui.Row(c).Gap(10).Children(func() {
		actionButton(c, "验证提炼 Agent 并继续", nil).Key("cognee-prepare").Disabled(s.Busy || !s.ModelPresent).OnClick(a.prepareOfficial)
		quietButton(c, "取消本轮准备").Key("cognee-prepare-cancel").Disabled(!s.Busy).OnClick(func() {
			if s.Cancel != nil {
				s.Cancel()
			}
			r.Official.Cancel()
		})
	})
	if s.Status.Message != "" {
		ui.Text(c, s.Status.Message).Key("cognee-prepare-result").Selectable().LineHeight(1.7)
	}
	if s.Status.Total > 0 {
		ui.Text(c, fmt.Sprintf("组件准备 %.2f / %.2f MB", float64(s.Status.Downloaded)/1e6, float64(s.Status.Total)/1e6)).FontSize(design.Small)
	}
	if s.Status.Phase == "ready" {
		meta(c, "实际服务", s.Status.Endpoint)
		meta(c, "已验证模型", fmt.Sprintf("%s · 嵌入 %d 维", s.Status.Model, s.Status.EmbeddingDimensions))
	}
}

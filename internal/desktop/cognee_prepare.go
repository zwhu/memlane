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
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type OfficialPreparationUI struct {
	Status                           official.Status
	Provider, Model, Endpoint, Key   string
	ModelPresent, EditingModel, Busy bool
	Sequence                         int64
	Cancel                           context.CancelFunc
	ChangedAt                        time.Time
}

func (a *App) configureOfficialPreparation(controller *RuntimeController) {
	if controller.Config.Official == nil {
		return
	}
	controller.Official = &official.Controller{Root: filepath.Join(controller.Config.Root, "official"), Config: *controller.Config.Official}
	s := &a.officialPreparation
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
		s.ModelPresent = true
		s.Provider = settings.Environment["LLM_PROVIDER"]
		s.Model = settings.Environment["LLM_MODEL"]
		s.Endpoint = settings.Environment["LLM_ENDPOINT"]
	}
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
	s.Status = official.Status{Phase: "preparing", Message: "准备已确认的独立官方环境…"}
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
	ui.Text(c, "独立官方环境").Bold()
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
	ui.Text(c, "复用本机 Codex 与其登录；Cognee 服务、FastEmbed 模型和官方插件位于此 App 的独立目录。不会采集既有聊天。").FontSize(design.Small).LineHeight(1.7)
	if s.ModelPresent && !s.EditingModel {
		meta(c, "Cognee 模型条件", s.Model+" · 已配置")
		quietButton(c, "修改独立模型条件").Disabled(s.Busy).OnClick(func() { s.EditingModel = true })
	} else {
		ui.TextInput(c.Key("cognee-model-provider"), &s.Provider).Placeholder("Cognee LLM provider").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-name"), &s.Model).Placeholder("用户选定的模型 ID").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-endpoint"), &s.Endpoint).Placeholder("该模型的 API 地址").Disabled(s.Busy)
		ui.TextInput(c.Key("cognee-model-key"), &s.Key).Password().Placeholder("Cognee 独立模型凭据").Disabled(s.Busy)
		actionButton(c, "保存独立模型条件", nil).Key("cognee-model-save").Disabled(s.Busy).OnClick(a.saveOfficialModel)
	}
	ui.Text(c, "确认后校验／下载独立组件、启动 Cognee，并执行数据库与小型模型可用探测（按所选模型计费）。Agent 的采集与项目范围另行确认。").FontSize(design.Small).LineHeight(1.7)
	ui.Row(c).Gap(10).Children(func() {
		actionButton(c, "确认准备并验证官方环境", nil).Key("cognee-prepare").Disabled(s.Busy || !s.ModelPresent).OnClick(a.prepareOfficial)
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

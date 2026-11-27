package settings

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) continueEnvironment() {
	s := &a.environment
	if a.canEnterConnectionStep() {
		a.setInitialStep(3)
		return
	}
	if a.config.FirstInstall != nil && (s.ownerInspecting || !s.OwnerStatusChecked || s.OwnerStatus == nil) {
		s.onboardingStartRequested = true
		a.InspectOwnerRuntimeStatusAsync()
		return
	}
	a.beginFirstRuntime(3)
}

func (a *Controller) environmentDiagnosticAgents(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	if s.AgentOpen {
		ui.Column(c).FillWidth().Gap(sp2).Margin(sp2, 0).Children(func() {
			ui.Text(c, "在已有 Agent 的新会话中粘贴提示词").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
			ui.Row(c).Gap(sp2).Children(func() {
				for _, entry := range s.DiagnosticAgents {
					agent := entry
					label := "复制给 " + agent.Name
					if agent.Application != "" && a.host.OpenExistingAgent != nil {
						label = "打开 " + agent.Name
					}
					kitui.SmallButton(c, tok, label, nil, func() {
						_, prompt := a.environmentDiagnostic()
						c.WriteClipboard(prompt)
						s.Message = "已复制，请在 " + agent.Name + " 新会话中粘贴"
						if agent.Application != "" && a.host.OpenExistingAgent != nil {
							if err := a.host.OpenExistingAgent(agent.Application); err != nil {
								s.Message = "提示词已复制，请手动打开 " + agent.Name
							}
						}
					})
				}
				kitui.SmallButton(c, tok, "复制完整提示词", kitui.IconCopy, func() {
					_, prompt := a.environmentDiagnostic()
					c.WriteClipboard(prompt)
					s.Message = "已复制排查提示词"
				})
			})
		})
	}
}

func (a *Controller) syncDownloadSourceIdx() {
	s := &a.environment
	mode := bootstrap.NormalizeDownloadSource(s.DownloadSource)
	s.DownloadSource = mode
	if mode == bootstrap.DownloadSourceOfficial {
		s.DownloadSourceIdx = 1
	} else {
		s.DownloadSourceIdx = 0
	}
}

func (a *Controller) switchDownloadSource(mode string) {
	s := &a.environment
	normalized := bootstrap.NormalizeDownloadSource(mode)
	if s.Busy || s.Loading {
		a.syncDownloadSourceIdx()
		return
	}
	if s.DownloadSource == normalized {
		a.syncDownloadSourceIdx()
		return
	}
	s.DownloadSource = normalized
	a.syncDownloadSourceIdx()
	if normalized == bootstrap.DownloadSourceOfficial {
		s.Message = "已切换到国外官方源"
	} else {
		s.Message = "已切换到国内镜像源"
	}
	if a.canDispatch() {
		a.detectEnvironment(true)
	}
}

func (a *Controller) downloadSourceSelector(c *ui.Context, key string) {
	tok := a.tokens()
	s := &a.environment
	a.syncDownloadSourceIdx()
	labels := []string{"国内镜像源", "国外官方源"}
	kitui.Segmented(c, tok, key, "下载来源", &s.DownloadSourceIdx, labels, func() {
		if s.DownloadSourceIdx == 1 {
			a.switchDownloadSource(bootstrap.DownloadSourceOfficial)
		} else {
			a.switchDownloadSource(bootstrap.DownloadSourceChina)
		}
	})
}

func (a *Controller) copyManualInstallScript(c *ui.Context) {
	s := &a.environment
	script := bootstrap.ManualInstallScript(s.Plan, s.Environment, s.DownloadSource)
	c.WriteClipboard(script)
	s.Message = "已复制终端安装命令，执行后请点击「重新检测」"
}

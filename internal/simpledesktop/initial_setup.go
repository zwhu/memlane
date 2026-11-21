package simpledesktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func (a *App) setInitialStep(step int) {
	if a.environment.Busy || a.integrations.Busy {
		return
	}
	if a.Config.FirstInstall != nil {
		if err := bootstrap.SaveFirstWizardStep(a.Config.ConfigFile, step); err != nil {
			a.environment.Error = bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	a.environment.InitialStep = step
	if step >= 2 && a.environment.RuntimeStarted {
		a.RefreshIntegrations()
	}
}

func (a *App) beginFirstRuntime(step int) {
	s := &a.environment
	if s.Busy || a.integrations.Busy || a.Update == nil {
		return
	}
	options, err := a.environmentOptions()
	if err != nil {
		s.Error = bootstrap.RedactInstallerText(err.Error())
		return
	}
	configuration := a.Config.ConfigFile
	if configuration == "" {
		configuration, err = bootstrap.DefaultRuntimeConfig()
		if err != nil {
			s.Error = bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	s.Busy, s.Error, s.RuntimeStage = true, "", "启动记忆服务"
	s.RuntimeStarted = false
	go func() {
		baseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		ctx, releaseStartup := bootstrap.TrackStartupContext(baseCtx)
		defer releaseStartup()
		environment, err := bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
		var prepared bootstrap.FirstRuntime
		if err == nil {
			prepared, err = environment.PrepareFirstRuntime(ctx, configuration, memlane.RuntimeAssets)
		}
		loaded, loadError := LoadConfig(configuration)
		if err == nil && loadError != nil {
			err = loadError
		}
		if err == nil {
			err = bootstrap.StartFirstRuntime(ctx, prepared)
		}
		if err == nil {
			loaded, loadError = LoadConfig(configuration)
			if loadError != nil {
				err = loadError
			}
		}
		complete := false
		if err == nil {
			complete, err = bootstrap.FirstInitializationComplete(configuration)
		}
		if err == nil && !complete && step >= 1 {
			err = bootstrap.SaveFirstWizardStep(configuration, step)
		}
		a.Update(func() {
			s.Busy = false
			if loadError == nil && loaded.FirstInstall != nil {
				a.Config = loaded
				a.Client = Client{Config: loaded}
				a.configError = ""
			}
			if environment != nil {
				s.Environment = environment
			}
			if len(prepared.Plan.Items) > 0 {
				s.Plan = prepared.Plan
				s.Loaded = true
			}
			s.RuntimePreparation = prepared
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
				return
			}
			s.RuntimeStarted = true
			s.RuntimeStage = ""
			if complete {
				a.initialSetup = false
				a.page = "总览"
				a.Start()
				return
			}
			if step < 1 {
				step = 2
			}
			s.InitialStep = step
			a.RefreshIntegrations()
		})
	}()
}

func (a *App) enableFirstRuntime() {
	s := &a.environment
	g := a.integrations.Generation
	row, ok := g.row(g.Next)
	if !ok || !g.validated(g.Next) || s.Busy || a.integrations.Busy || a.integrations.hasPendingGeneration() {
		return
	}
	prepared := s.RuntimePreparation
	if !s.RuntimeStarted || prepared.ConfigPath == "" {
		s.Error = "记忆服务尚未就绪，请先重新检查。"
		return
	}
	reference := bootstrap.FirstGenerationReference{ID: g.Next.ID, Revision: g.Next.Revision}
	s.Busy, s.Error, s.RuntimeStage = true, "", "启用记忆提炼"
	a.integrations.Busy = true
	a.integrations.Request++
	a.integrations.Loading = false
	go func() {
		baseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		ctx, releaseStartup := bootstrap.TrackStartupContext(baseCtx)
		defer releaseStartup()
		err := bootstrap.ActivateFirstRuntime(ctx, prepared, reference, row.Digest, g.Revision)
		loaded, loadError := LoadConfig(prepared.ConfigPath)
		if err == nil && loadError != nil {
			err = loadError
		}
		complete := false
		if err == nil {
			complete, err = bootstrap.FirstInitializationComplete(prepared.ConfigPath)
		}
		a.Update(func() {
			s.Busy = false
			a.integrations.Busy = false
			if loadError == nil {
				a.Config = loaded
				a.Client = Client{Config: loaded}
			}
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
				return
			}
			if !complete {
				s.Error = "初始化回执尚未确认，已保留原配置与进度。"
				return
			}
			s.RuntimeStage = ""
			s.RuntimeStarted = true
			a.initialSetup = false
			a.page = "总览"
			a.Start()
		})
	}()
}

func (a *App) initialSetupContent(c *ui.Context) {
	a.initialSetupShell(c)
}

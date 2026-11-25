package simpledesktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func wizardStageForStep(step int) string {
	return [...]string{bootstrap.WizardStageSelectAgent, bootstrap.WizardStagePrepareEnv, bootstrap.WizardStageConnectAndEnable}[step-1]
}

func (a *App) canEnterConnectionStep() bool {
	s := &a.environment
	return s.RuntimeStarted || (a.Config.FirstInstall != nil && s.Plan.Complete() && s.staleRuntimeOwner())
}

func (a *App) setInitialStep(step int) {
	s := &a.environment
	if step < 1 || step > 3 || s.Busy || s.RestartingStaleDaemon || a.integrations.Busy ||
		(step == 3 && !a.canEnterConnectionStep()) {
		return
	}
	if a.Config.FirstInstall != nil {
		if err := bootstrap.SaveFirstWizardState(a.Config.ConfigFile, step, wizardStageForStep(step)); err != nil {
			s.Error = bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	if step != s.InitialStep {
		s.pinnedPreviewRequested = nil
	}
	s.InitialStep = step
	if step == 3 && s.RuntimeStarted {
		a.refreshWizardIntegrations()
	}
}

func (a *App) refreshWizardIntegrations() {
	a.integrations.PinnedAgentKinds = a.environment.SelectedAgents
	a.RefreshIntegrations()
}

func (a *App) isSetupOnlyRuntime() bool {
	return a.setupDeferredMode || a.Config.GenerationMode == "unconfigured" ||
		(a.Config.FirstInstall != nil && a.Config.FirstInstall.Setup)
}

func (a *App) canActivateFirstRuntimeFromSettings() bool {
	return !a.initialSetup && a.Config.FirstInstall != nil && a.isSetupOnlyRuntime()
}

func (a *App) canDeferFirstGenerationSetup() bool {
	s := &a.environment
	return s.RuntimeStarted && !s.Busy && !s.RestartingStaleDaemon && !s.staleRuntimeOwner() && !a.integrations.Busy
}

func (a *App) deferFirstGenerationSetup() {
	s := &a.environment
	if !a.initialSetup || s.InitialStep != 3 || !a.canDeferFirstGenerationSetup() {
		return
	}
	if a.Config.FirstInstall != nil && a.Config.ConfigFile != "" {
		if err := bootstrap.SaveFirstSetupDeferred(a.Config.ConfigFile); err != nil {
			s.Error = bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	a.initialSetup, a.setupDeferredMode = false, true
	a.setupDeferredToastDismissed = false
	a.page = "总览"
	a.Start()
}

func (a *App) beginFirstRuntime(step int) {
	s := &a.environment
	if s.Busy || s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving || s.RestartingStaleDaemon || a.integrations.Busy || a.Update == nil ||
		(a.Config.FirstInstall != nil && (!s.OwnerStatusChecked || s.OwnerStatus == nil || s.staleRuntimeOwner())) {
		return
	}
	if step == 0 {
		s.AutoRuntimeAttempted = true
		s.RuntimeStartError = ""
	}
	s.Busy, s.Error, s.RuntimeStage = true, "", "启动记忆服务"
	s.RuntimeStarted = false
	config, selection := a.Config, *s
	prepareAndStart := a.PrepareAndStartFirstRuntimeFn
	go func() {
		baseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		ctx, releaseStartup := bootstrap.TrackStartupContext(baseCtx)
		defer releaseStartup()
		configuration := config.ConfigFile
		var err error
		if configuration == "" {
			configuration, err = bootstrap.DefaultRuntimeConfig()
		}
		var options bootstrap.EnvironmentOptions
		var environment *bootstrap.Environment
		var prepared bootstrap.FirstRuntime
		if err == nil {
			if prepareAndStart != nil {
				prepared, err = prepareAndStart(ctx, config, selection)
			} else {
				options, err = loadEnvironmentOptions(config, selection)
				if err == nil {
					environment, err = bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
				}
				if err == nil {
					prepared, err = environment.PrepareFirstRuntime(ctx, configuration, memlane.RuntimeAssets)
				}
				if err == nil {
					err = bootstrap.StartFirstRuntime(ctx, prepared)
				}
			}
		}
		loaded, loadError := LoadConfig(configuration)
		if err == nil {
			err = loadError
		}
		complete := false
		if err == nil {
			complete, err = bootstrap.FirstInitializationComplete(configuration)
		}
		if err == nil && !complete && step >= 1 {
			err = bootstrap.SaveFirstWizardState(configuration, step, wizardStageForStep(step))
		}
		a.Update(func() {
			s.Busy, s.RuntimeStage = false, ""
			if loadError == nil && loaded.FirstInstall != nil {
				a.Config = loaded
				a.Client.Config = loaded
				a.configError = ""
			}
			if environment != nil {
				a.applyEnvironmentOptions(options)
				s.Environment = environment
			}
			if len(prepared.Plan.Items) > 0 {
				s.Plan, s.Loaded = prepared.Plan, true
			}
			s.RuntimePreparation = prepared
			if err != nil {
				if step == 0 {
					s.RuntimeStartError = bootstrap.RedactInstallerText(err.Error())
					a.Start()
				} else {
					s.Error = bootstrap.RedactInstallerText(err.Error())
				}
				return
			}
			s.RuntimeStarted = true
			s.RuntimeStartError = ""
			if step == 0 && !complete && a.isSetupOnlyRuntime() {
				a.initialSetup, a.setupDeferredMode = false, true
				a.Start()
				return
			}
			if complete {
				a.initialSetup, a.setupDeferredMode = false, false
				a.page = "总览"
				a.Start()
				return
			}
			s.InitialStep = step
			a.refreshWizardIntegrations()
		})
	}()
}

func (a *App) enableFirstRuntime() {
	if a.firstGenerationEnableBlocker() == "" {
		a.activateFirstRuntime(false)
	}
}

func (a *App) settingsFirstActivationBlocker() string {
	s := &a.integrations
	if !s.generationReady() {
		return "请先读取记忆提炼配置。"
	}
	if !s.Editor && s.Generation.Next == nil {
		return "请先选择并保存记忆提炼配置。"
	}
	return a.firstGenerationEnableBlocker()
}

func (a *App) activateFirstRuntimeFromSettings() {
	if a.canActivateFirstRuntimeFromSettings() && a.settingsFirstActivationBlocker() == "" {
		a.activateFirstRuntime(true)
	}
}

func (a *App) activateFirstRuntime(fromSettings bool) {
	s := &a.environment
	g := a.integrations.Generation
	row, ok := g.row(g.Next)
	if !ok || a.Update == nil {
		return
	}
	prepared := s.RuntimePreparation
	if prepared.ConfigPath == "" && !fromSettings {
		s.Error = "记忆服务尚未就绪，请先重新检查。"
		return
	}
	config, selection := a.Config, *s
	activate := a.ActivateFirstRuntimeFn
	if activate == nil {
		activate = bootstrap.ActivateFirstRuntime
	}
	checkComplete := a.FirstInitCompleteFn
	if checkComplete == nil {
		checkComplete = bootstrap.FirstInitializationComplete
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
		var err error
		if prepared.ConfigPath == "" {
			prepared, err = prepareSettingsFirstRuntime(ctx, config, selection)
		}
		if err == nil {
			err = activate(ctx, prepared, reference, row.Digest, g.Revision)
		}
		var loaded Config
		if err == nil {
			loaded, err = LoadConfig(prepared.ConfigPath)
		}
		complete := false
		if err == nil {
			complete, err = checkComplete(prepared.ConfigPath)
		}
		var clearErr error
		if err == nil && complete {
			clearErr = bootstrap.ClearFirstSetupDeferred(prepared.ConfigPath)
		}
		a.Update(func() {
			s.Busy, s.RuntimeStage = false, ""
			a.integrations.Busy = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
				return
			}
			if !complete {
				s.Error = "初始化回执尚未确认，已保留原配置与进度。"
				return
			}
			if clearErr != nil {
				s.Error = "无法清除延后配置回执：" + bootstrap.RedactInstallerText(clearErr.Error())
				if fromSettings {
					a.integrations.Message = s.Error
				}
			}
			a.Config, a.Client.Config = loaded, loaded
			a.overviewFirstUse = overviewFirstUseState{}
			a.completion = "尚未检查"
			a.monitor.Request++
			a.monitor.Polling = false
			s.RuntimePreparation = prepared
			s.RuntimeStarted = true
			a.initialSetup, a.setupDeferredMode = false, false
			if !fromSettings {
				a.page = "总览"
			}
			a.Start()
		})
	}()
}

func prepareSettingsFirstRuntime(ctx context.Context, config Config, selection environmentSetupState) (bootstrap.FirstRuntime, error) {
	options, err := loadEnvironmentOptions(config, selection)
	if err != nil {
		return bootstrap.FirstRuntime{}, err
	}
	environment, err := bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
	if err != nil {
		return bootstrap.FirstRuntime{}, err
	}
	return environment.PrepareFirstRuntime(ctx, config.ConfigFile, memlane.RuntimeAssets)
}

func (a *App) initialSetupContent(c *ui.Context) {
	a.initialSetupShell(c)
}

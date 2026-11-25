package settings

import (
	"context"
	"errors"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
)

func wizardStageForStep(step int) string {
	return [...]string{bootstrap.WizardStageSelectAgent, bootstrap.WizardStagePrepareEnv, bootstrap.WizardStageConnectAndEnable}[step-1]
}

func (a *Controller) canEnterConnectionStep() bool {
	s := &a.environment
	return s.RuntimeStarted || (a.config.FirstInstall != nil && s.Plan.Complete() && s.staleRuntimeOwner())
}

func (a *Controller) setInitialStep(step int) {
	s := &a.environment
	if step < 1 || step > 3 || s.Busy || s.RestartingStaleDaemon || a.integrations.Busy ||
		(step == 3 && !a.canEnterConnectionStep()) {
		return
	}
	if a.config.FirstInstall != nil {
		if err := bootstrap.SaveFirstWizardState(a.config.ConfigFile, step, wizardStageForStep(step)); err != nil {
			s.onboardingInstallFailureDigest = ""
			s.OnboardingRuntimeFailed = false
			s.Error = bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	if step != s.InitialStep {
		s.pinnedPreviewRequested = nil
	}
	s.InitialStep = step
	a.onboarding.page = onboardingAgents + onboardingPage(step-1)
	if step == 3 && s.RuntimeStarted {
		a.refreshWizardIntegrations()
	}
}

func (a *Controller) refreshWizardIntegrations() {
	a.integrations.PinnedAgentKinds = a.environment.SelectedAgents
	a.RefreshIntegrations()
}

func (a *Controller) isSetupOnlyRuntime() bool {
	return a.setupDeferredMode || a.config.GenerationMode == "unconfigured" ||
		(a.config.FirstInstall != nil && a.config.FirstInstall.Setup)
}

func (a *Controller) canActivateFirstRuntimeFromSettings() bool {
	return !a.initialSetup && a.config.FirstInstall != nil && a.isSetupOnlyRuntime()
}

func (a *Controller) canDeferFirstGenerationSetup() bool {
	s := &a.environment
	return s.RuntimeStarted && !s.Busy && !s.RestartingStaleDaemon && !s.staleRuntimeOwner() &&
		!a.integrations.Busy && a.onboardingGeneration.activationRef == nil
}

func (a *Controller) deferFirstGenerationSetup() {
	s := &a.environment
	if !a.initialSetup || s.InitialStep != 3 || !a.canDeferFirstGenerationSetup() {
		return
	}
	if a.config.FirstInstall != nil && a.config.ConfigFile != "" {
		if err := bootstrap.SaveFirstSetupDeferred(a.config.ConfigFile); err != nil {
			s.Error = "无法保存延后配置：" + bootstrap.RedactInstallerText(err.Error())
			return
		}
	}
	a.initialSetup, a.setupDeferredMode = false, true
	a.setupDeferredToastDismissed = false
	a.navigateOverview()
	a.Start()
}

func (a *Controller) beginFirstRuntime(step int) {
	tok := a.tokens()
	s := &a.environment
	if s.Busy || s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving || s.RestartingStaleDaemon || a.integrations.Busy || !a.canDispatch() ||
		(a.config.FirstInstall != nil && (!s.OwnerStatusChecked || s.OwnerStatus == nil || s.staleRuntimeOwner())) {
		return
	}
	if step == 0 {
		s.AutoRuntimeAttempted = true
		s.RuntimeStartError = ""
	}
	s.Busy, s.Error, s.RuntimeStage = true, "", "启动记忆服务"
	s.RuntimeStarted = false
	config, selection := a.config, *s
	wizard := a.initialSetup
	baseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	if wizard {
		s.Cancel = cancel
		s.OnboardingRuntimeFailed = false
	}
	prepareAndStart := a.prepareAndStartFirstRuntime
	go func() {
		defer cancel()
		if wizard {
			lines := makeOnboardingLogLines("[service] 校验上游源码并启动本机记忆服务", false)
			a.dispatch(func() { s.appendOnboardingLog(lines) })
		}
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
				options, err = loadEnvironmentOptions(tok, config, selection)
				if err == nil {
					environment, err = bootstrap.NewEnvironment(ctx, a.host.BootstrapAssets, options)
				}
				if err == nil {
					prepared, err = environment.PrepareFirstRuntime(ctx, configuration, a.host.RuntimeAssets)
				}
				if err == nil {
					err = bootstrap.StartFirstRuntime(ctx, prepared)
				}
			}
		}
		startFailed := err != nil && !errors.Is(err, context.Canceled)
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
		var lines []onboardingLogLine
		if wizard {
			if errors.Is(err, context.Canceled) {
				lines = makeOnboardingLogLines("[service] "+environmentInstallStopped, false)
			} else if err != nil {
				lines = makeOnboardingLogLines("[service] "+err.Error(), startFailed)
			} else {
				lines = makeOnboardingLogLines("[service] 本地记忆服务已通过健康检查", false)
			}
		}
		a.dispatch(func() {
			s.Busy, s.RuntimeStage = false, ""
			if wizard {
				s.Cancel = nil
				s.OnboardingRuntimeFailed = startFailed
				s.appendOnboardingLog(lines)
			}
			if loadError == nil && loaded.FirstInstall != nil {
				a.loadRuntimeConfig(loaded)
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
				if wizard && errors.Is(err, context.Canceled) {
					s.Message = environmentInstallStopped
					return
				}
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
				a.navigateOverview()
				a.Start()
				return
			}
			s.InitialStep = step
			a.onboarding.page = onboardingConnect
			a.showOnboardingToast("本地记忆服务已就绪")
			a.refreshWizardIntegrations()
		})
	}()
}

func (a *Controller) settingsFirstActivationBlocker() string {
	s := &a.integrations
	if !s.generationReady() {
		return "请先读取记忆提炼配置。"
	}
	if !s.Editor && s.Generation.Next == nil {
		return "请先选择并保存记忆提炼配置。"
	}
	return a.firstGenerationEnableBlocker()
}

func (a *Controller) activateFirstRuntimeFromSettings() {
	if !a.canActivateFirstRuntimeFromSettings() || a.settingsFirstActivationBlocker() != "" {
		return
	}
	s := &a.environment
	g := a.integrations.Generation
	row, ok := g.row(g.Next)
	if !ok || !a.canDispatch() {
		return
	}
	prepared := s.RuntimePreparation
	config, selection := a.config, *s
	activate := a.activateFirstRuntime
	if activate == nil {
		activate = bootstrap.ActivateFirstRuntime
	}
	checkComplete := a.firstInitComplete
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
			prepared, err = a.prepareSettingsFirstRuntime(ctx, config, selection)
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
		a.dispatch(func() {
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
			a.acceptFirstRuntimeActivation(loaded, prepared, clearErr, true)
		})
	}()
}

func (a *Controller) acceptFirstRuntimeActivation(loaded Config, prepared bootstrap.FirstRuntime, clearErr error, fromSettings bool) {
	s := &a.environment
	if clearErr != nil {
		s.Error = "无法清除延后配置回执：" + bootstrap.RedactInstallerText(clearErr.Error())
		if fromSettings {
			a.integrations.Message = s.Error
		}
	}
	a.loadRuntimeConfig(loaded)
	a.monitor.CompletionStatus = "尚未检查"
	a.monitor.Request++
	a.monitor.Polling = false
	s.RuntimePreparation = prepared
	s.RuntimeStarted = true
	a.setupDeferredMode = false
	if fromSettings {
		a.initialSetup = false
		a.Start()
	}
}

func (a *Controller) prepareSettingsFirstRuntime(ctx context.Context, config Config, selection EnvironmentSetupState) (bootstrap.FirstRuntime, error) {
	tok := a.tokens()
	options, err := loadEnvironmentOptions(tok, config, selection)
	if err != nil {
		return bootstrap.FirstRuntime{}, err
	}
	environment, err := bootstrap.NewEnvironment(ctx, a.host.BootstrapAssets, options)
	if err != nil {
		return bootstrap.FirstRuntime{}, err
	}
	return environment.PrepareFirstRuntime(ctx, config.ConfigFile, a.host.RuntimeAssets)
}

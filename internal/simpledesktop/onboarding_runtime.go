package simpledesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func (s *environmentSetupState) staleRuntimeOwner() bool {
	return s.OwnerStatus != nil && s.OwnerStatus.StaleVersionDetected && !s.OwnerStatus.DeveloperCheckout
}

func (a *App) shouldInspectDailyRuntimeOwner() bool {
	s := &a.environment
	return !a.initialSetup && a.Config.FirstInstall != nil &&
		(!s.RuntimeStarted || !s.OwnerStatusChecked || s.staleRuntimeOwner())
}

type ownerInspectMode uint8

const (
	ownerInspectAuto ownerInspectMode = iota
	ownerInspectRetry
	ownerInspectStatusOnly
)

func (a *App) inspectOwnerRuntimeStatusAsync() {
	a.inspectOwnerRuntimeStatus(ownerInspectAuto)
}

func (a *App) inspectOwnerRuntimeStatusOnlyAsync() {
	a.inspectOwnerRuntimeStatus(ownerInspectStatusOnly)
}

func (a *App) inspectOwnerRuntimeStatus(mode ownerInspectMode) {
	s := &a.environment
	if a.Config.FirstInstall == nil || s.ownerInspecting || s.RestartingStaleDaemon || a.Update == nil {
		return
	}
	resumeStartup := !s.OwnerStatusChecked && !s.RuntimeStarted
	s.ownerInspecting = true
	path, root := a.Config.ConfigFile, os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
	inspect := a.ownerRuntimeInspector()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := inspect(ctx, path, root)
		a.Update(func() {
			s.ownerInspecting = false
			if path != a.Config.ConfigFile || s.RestartingStaleDaemon {
				s.onboardingStartRequested = false
				return
			}
			s.OwnerStatusChecked = err == nil
			if err == nil {
				wasStale := s.staleRuntimeOwner()
				s.OwnerStatus = &status
				if wasStale && !s.staleRuntimeOwner() && mode != ownerInspectStatusOnly {
					s.FastLaneChecked = false
				}
			}
			if a.initialSetup {
				if s.onboardingStartRequested {
					s.onboardingStartRequested = false
					if err != nil {
						s.OnboardingRuntimeFailed = false
						s.Error = "无法核对后台服务版本，请重新检查。"
					} else {
						a.continueEnvironment()
					}
					return
				}
				if mode == ownerInspectStatusOnly {
					s.FastLaneChecked = true
					if err != nil {
						s.OnboardingRuntimeFailed = false
						s.Error = "无法核对后台服务版本，请重新检查。"
					} else if !s.staleRuntimeOwner() && !s.RuntimeStarted {
						s.Error = s.StaleDaemonError
						if s.Error == "" {
							s.Error = "旧版后台已停止，但新版服务尚未启动，请点击重试启动。"
						}
					}
					return
				}
				if err != nil && s.OwnerStatus == nil {
					s.OnboardingRuntimeFailed = false
					s.Error = "无法核对后台服务版本，请重新检测。"
				}
				a.advanceFirstRuntimeStartup()
				return
			}
			if mode == ownerInspectStatusOnly {
				s.AutoRuntimeAttempted = true
			}
			if err != nil {
				s.RuntimeStartError = "无法核对后台服务状态，请重新检查。"
				return
			}
			switch {
			case s.RuntimeStarted || s.staleRuntimeOwner():
				s.RuntimeStartError = ""
			case mode == ownerInspectStatusOnly:
				s.RuntimeStartError = s.StaleDaemonError
				if s.RuntimeStartError == "" {
					s.RuntimeStartError = "旧版后台已停止，但新版服务尚未启动，请点击重试启动。"
				}
			case !s.AutoRuntimeAttempted || mode == ownerInspectRetry:
				a.beginFirstRuntime(0)
			}
			if mode != ownerInspectStatusOnly && resumeStartup && !s.Busy &&
				(!s.AutoRuntimeAttempted || s.RuntimeStarted || s.staleRuntimeOwner()) {
				a.Start()
			}
		})
	}()
}

func (a *App) canRetryDailyRuntimeStartup() bool {
	s := &a.environment
	return !a.initialSetup && a.Config.FirstInstall != nil &&
		!s.RuntimeStarted && !s.staleRuntimeOwner() && (s.AutoRuntimeAttempted || s.RuntimeStartError != "")
}

func (a *App) retryDailyRuntimeStartup() {
	s := &a.environment
	if !a.canRetryDailyRuntimeStartup() || s.Busy || s.Loading || s.ownerInspecting || s.RestartingStaleDaemon || a.Update == nil {
		return
	}
	s.RuntimeStartError = ""
	a.inspectOwnerRuntimeStatus(ownerInspectRetry)
}

func (s *environmentSetupState) dailyRuntimeRecoveryAction() string {
	if !s.OwnerStatusChecked && !s.AutoRuntimeAttempted {
		return "重新检查"
	}
	return "重试启动"
}

func (a *App) dailyRuntimeStartupError(c *ui.Context) {
	if !a.canRetryDailyRuntimeStartup() {
		return
	}
	s := &a.environment
	action, title, message := s.dailyRuntimeRecoveryAction(), "记忆服务启动失败", s.RuntimeStartError
	if action == "重新检查" {
		title = "无法核对后台服务状态"
	}
	if message == "" {
		message = "后台服务尚未启动，可点击右侧按钮重试。"
	}
	v4Row(c, v4RowOptions{
		Key: "daily-runtime-startup-error", Title: title, Error: message,
		Control: func() {
			v4SmallButton(c, action, v4IconRefresh, a.retryDailyRuntimeStartup).Disabled(s.ownerInspecting || s.Busy || s.Loading)
		},
	})
}

func (a *App) ownerRuntimeInspector() func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error) {
	if a.InspectOwnerRuntimeStatusFn != nil {
		return a.InspectOwnerRuntimeStatusFn
	}
	return func(ctx context.Context, path, root string) (bootstrap.OwnerRuntimeStatus, error) {
		return bootstrap.InspectOwnerRuntimeStatus(ctx, path, root, memlane.RuntimeAssets)
	}
}

func (a *App) advanceFirstRuntimeStartup() {
	s := &a.environment
	if !a.initialSetup || a.Config.FirstInstall == nil || !s.OwnerStatusChecked || s.OwnerStatus == nil ||
		!s.Loaded || s.Loading || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving || s.Busy || a.integrations.Busy || s.RuntimeStarted || s.FastLaneChecked {
		return
	}
	if s.InitialStep <= 1 && !s.staleRuntimeOwner() {
		return
	}
	s.FastLaneChecked = true
	if !s.Plan.Complete() {
		if s.InitialStep == 3 || (s.InitialStep <= 1 && s.staleRuntimeOwner()) {
			a.setInitialStep(2)
		}
		return
	}
	if s.staleRuntimeOwner() {
		a.setInitialStep(3)
		return
	}
	if s.InitialStep == 3 {
		a.beginFirstRuntime(3)
	}
}

func (a *App) installWorkerInspector() func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
	if a.InspectInstallWorkerFn != nil {
		return a.InspectInstallWorkerFn
	}
	return bootstrap.InspectInstallWorker
}

func (a *App) inspectInstallWorkerAsync() {
	s := &a.environment
	if s.workerInspecting || s.Busy || s.RestartingStaleDaemon || a.Update == nil {
		return
	}
	s.workerInspecting = true
	s.WorkerPollNext = time.Now().Add(time.Second)
	root := os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
	inspect := a.installWorkerInspector()
	wizard, watching := a.initialSetup, a.onboarding.watchWorker
	wasActive, previous := s.ExternalWorkerActive, s.ExternalWorker
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		worker, active, err := inspect(ctx, root)
		finished := !active && (wasActive || watching && onboardingWorkerFinished(worker, previous))
		var lines []onboardingLogLine
		if wizard && (active || finished) {
			lines = onboardingWorkerLogLines(worker, previous)
		}
		a.Update(func() {
			s.workerInspecting = false
			defer a.advanceFirstRuntimeStartup()
			if err != nil && !active {
				return
			}
			s.ExternalWorker, s.ExternalWorkerActive = worker, active
			if finished {
				a.onboarding.watchWorker = false
			}
			s.appendOnboardingLog(lines)
			if active && a.initialSetup && a.onboardingPage() < onboardingEnv {
				s.InitialStep = 2
				a.onboarding.page = onboardingEnv
			}
			if finished {
				if a.initialSetup && (a.Config.FirstInstall == nil || worker.State == "completed" && worker.StartRuntime) {
					a.reloadConfigAfterWorker(worker)
				} else {
					a.refreshEnvironment()
				}
			}
		})
	}()
}

func onboardingWorkerFinished(worker, previous bootstrap.InstallWorkerState) bool {
	switch worker.State {
	case "completed", "failed", "cancelled", "needs_review":
		// A prior terminal receipt can still be present while the user is
		// pasting the takeover command into a new Agent session.
		return worker.State != previous.State || worker.OperationID != previous.OperationID ||
			worker.PID != previous.PID || worker.Started != previous.Started
	default:
		return false
	}
}

func (a *App) reloadConfigAfterWorker(worker bootstrap.InstallWorkerState) {
	currentPath := a.Config.ConfigFile
	go func() {
		path := currentPath
		var err error
		if path == "" {
			path, err = bootstrap.DefaultRuntimeConfig()
		}
		runtimeCompleted := worker.State == "completed" && worker.StartRuntime && worker.ConfigPath != "" &&
			filepath.Clean(worker.ConfigPath) == filepath.Clean(path)
		var loaded Config
		if err == nil {
			loaded, err = LoadConfig(path)
		}
		a.Update(func() {
			s := &a.environment
			if !a.initialSetup || currentPath != a.Config.ConfigFile || s.Busy {
				return
			}
			if err == nil && loaded.FirstInstall != nil && (a.Config.FirstInstall == nil || runtimeCompleted) {
				a.Config, a.Client.Config = loaded, loaded
				a.configError = ""
				s.FastLaneChecked = false
				if runtimeCompleted && !s.RuntimeStarted {
					// The completed receipt selects the resume phase. The owner
					// and service still need verification before enabling tools.
					s.InitialStep, a.onboarding.page = 3, onboardingConnect
					s.OwnerStatusChecked = false
				}
				a.inspectOwnerRuntimeStatusAsync()
			}
			a.refreshEnvironment()
		})
	}()
}

func (a *App) restartStaleRuntimeOwner() {
	s := &a.environment
	if !s.OwnerStatusChecked || !s.staleRuntimeOwner() || s.OwnerStatus.OwnerFingerprint == "" ||
		s.RestartingStaleDaemon || s.ownerInspecting || s.Busy || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving || a.integrations.Busy || a.Update == nil {
		return
	}
	s.RestartingStaleDaemon, s.StaleDaemonError = true, ""
	config, selection, wizard := a.Config, *s, a.initialSetup
	prepared, fingerprint := s.RuntimePreparation, s.OwnerStatus.OwnerFingerprint
	restart, inspect := a.RestartStaleRuntimeOwnerFn, a.ownerRuntimeInspector()
	if restart == nil {
		restart = func(ctx context.Context, prepared bootstrap.FirstRuntime, fingerprint string) error {
			_, err := bootstrap.RestartStaleRuntimeOwner(ctx, prepared, fingerprint, memlane.RuntimeAssets)
			return err
		}
	}
	go func() {
		if wizard {
			lines := makeOnboardingLogLines("[service] 校验组件并切换至新版后台服务", false)
			a.Update(func() { s.appendOnboardingLog(lines) })
		}
		baseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		ctx, releaseStartup := bootstrap.TrackStartupContext(baseCtx)
		defer releaseStartup()
		root := os.Getenv("MEMLANE_BOOTSTRAP_ROOT")
		var err error
		if prepared.ConfigPath == "" {
			var options bootstrap.EnvironmentOptions
			var environment *bootstrap.Environment
			options, err = loadEnvironmentOptions(config, selection)
			if err == nil {
				environment, err = bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
			}
			if err == nil {
				prepared, err = environment.PrepareFirstRuntimeForUpgrade(ctx, config.ConfigFile, memlane.RuntimeAssets)
			}
		}
		if err == nil {
			err = restart(ctx, prepared, fingerprint)
		}
		var loaded Config
		var status bootstrap.OwnerRuntimeStatus
		if err == nil {
			loaded, err = LoadConfig(prepared.ConfigPath)
		}
		if err == nil {
			status, err = inspect(ctx, prepared.ConfigPath, root)
			if err == nil && status.StaleVersionDetected {
				err = errors.New("后台仍在运行旧版，请核对服务状态")
			}
		}
		var lines []onboardingLogLine
		if wizard {
			if err != nil {
				lines = makeOnboardingLogLines("[service] "+err.Error(), true)
			} else {
				lines = makeOnboardingLogLines("[service] 新版后台服务已启动", false)
			}
		}
		a.Update(func() {
			s.RestartingStaleDaemon = false
			s.appendOnboardingLog(lines)
			if err != nil {
				s.StaleDaemonError = bootstrap.RedactInstallerText(err.Error())
				s.OwnerStatusChecked = false
				s.RuntimeStarted = false
				a.inspectOwnerRuntimeStatusOnlyAsync()
				return
			}
			s.OwnerStatus, s.OwnerStatusChecked, s.RuntimeStarted = &status, true, true
			s.RuntimeStartError, s.StaleDaemonError = "", ""
			s.RuntimePreparation = prepared
			a.Config, a.Client.Config = loaded, loaded
			a.configError = ""
			a.reloadGraphAssetsAfterOwnerSwitch(loaded.GraphAssets)
			if a.initialSetup {
				a.setInitialStep(3)
			} else {
				a.RefreshIntegrations()
				a.health()
			}
		})
	}()
}

func (a *App) reloadGraphAssetsAfterOwnerSwitch(dir string) {
	assetsChanged := dir != a.graphAssetsDir
	closedGraph := assetsChanged && a.graph != nil
	a.graphAssetsDir = dir
	if !assetsChanged && a.graph != nil && a.graphAttachErr == nil {
		return
	}
	if closedGraph {
		a.cancelGraphLoad()
		a.graph.close()
		a.graph = nil
		a.graphSelection = ""
	}
	if assetsChanged || (a.graphAssetsErr != nil && a.graphError == a.graphAssetsErr.Error()) ||
		(a.graphAttachErr != nil && a.graphError == a.graphAttachErr.Error()) {
		a.graphError = ""
	}
	a.graphAttachErr = nil
	a.graphAssetsChecked = false
	a.graphAssets, a.graphAssetsErr, a.graphAssetsCheck = nil, nil, nil
	a.verifyGraphAssetsAsync()
	if a.graphAttachRequested || a.page == "图谱" {
		a.EnsureGraphAttached()
	}
	if closedGraph && a.page == "图谱" {
		a.loadGraph()
	}
}

func (a *App) staleDaemonBanner(c *ui.Context) {
	s := &a.environment
	if !s.staleRuntimeOwner() {
		return
	}
	ui.Column(c.Key("stale-daemon-banner")).FillWidth().Padding(sp4).Gap(sp2).
		Radius(radius).Background(tok.Warning.Alpha(0.08)).Border(1, tok.Warning.Alpha(0.25)).Children(func() {
		homeText(c, "检测到旧版后台仍在运行", homeBody).FontWeight(600).TextColor(tok.Warning)
		homeText(c, "为兼容新版功能并保留现有记忆库与已下载组件（零重新下载），请一键切换至新版服务。", homeMeta)
		ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
			v4SmallPrimaryButton(c, "立即切换至新版服务", a.restartStaleRuntimeOwner).
				Disabled(!s.OwnerStatusChecked || s.RestartingStaleDaemon || s.ownerInspecting || s.Busy || s.ExternalWorkerActive || s.workerInspecting || s.selectedAgentsSaving || a.integrations.Busy || s.OwnerStatus.OwnerFingerprint == "")
			if !s.OwnerStatusChecked && !s.ownerInspecting && !s.RestartingStaleDaemon {
				v4SmallButton(c, "重新检查", v4IconRefresh, a.inspectOwnerRuntimeStatusOnlyAsync)
			}
			v4SmallButton(c, "复制救援指令交给 Agent", v4IconCopy, func() {
				c.WriteClipboard(agentTakeoverBeta2RescuePrompt())
				s.StaleRescueCopied = true
			})
		})
		if s.RestartingStaleDaemon {
			homeText(c, "正在切换后台服务…", homeMeta)
		}
		if s.StaleRescueCopied {
			homeText(c, "已复制救援指令，请在 Agent 的新会话中粘贴执行", homeMeta)
		}
		if s.StaleDaemonError != "" {
			homeText(c, s.StaleDaemonError, homeMeta).TextColor(tok.Danger)
		}
	})
}

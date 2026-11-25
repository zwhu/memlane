package settings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
)

const environmentInstallStopped = "已停止，已完成的准备会保留。"

func (a *Controller) runEnvironment(approve bool) {
	s := &a.environment
	if s.ExternalWorkerActive {
		s.Message = "命令行正在后台安装组件，请等待其完成或查看上方实时进度。"
		return
	}
	if s.Busy || s.Loading || s.selectedAgentsSaving || s.workerInspecting || s.RestartingStaleDaemon || s.Environment == nil || !a.canDispatch() {
		return
	}
	environment, plan := s.Environment, s.Plan
	initialSetup := a.initialSetup
	journal := s.Journal
	if !approve && journal.ApprovedDigest != "" && journal.Operation != "" {
		plan = journal.Plan
	}
	s.Busy, s.Review, s.Error, s.Message = true, false, "", ""
	s.RuntimeStage = ""
	s.Progress = bootstrap.InstallProgress{}
	s.onboardingRedetectRequired = false
	s.OnboardingRuntimeFailed = false
	s.onboardingInstallFailureDigest = ""
	s.Generation++
	generation := s.Generation
	ctx, cancel := context.WithCancel(context.Background())
	s.Cancel = cancel
	pluginDestination := a.managedPluginUpdateDestination(plan)
	configFile := a.config.ConfigFile
	officialData := a.config.OfficialData
	inspectWorker := a.installWorkerInspector()
	go func() {
		defer cancel()
		inspectionCtx, endInspection := context.WithTimeout(ctx, 5*time.Second)
		worker, active, inspectErr := inspectWorker(inspectionCtx, environment.Options.Root)
		endInspection()
		if active || inspectErr != nil || ctx.Err() != nil {
			stopped := errors.Is(ctx.Err(), context.Canceled)
			a.dispatch(func() {
				if s.Generation != generation {
					return
				}
				s.Busy, s.Cancel = false, nil
				s.ExternalWorker, s.ExternalWorkerActive = worker, active
				if active {
					s.Message = "命令行正在后台安装组件，请等待其完成或查看上方实时进度。"
				} else if stopped {
					s.Message = environmentInstallStopped
				} else {
					s.Error = "无法核对命令行安装进度，请重新检测后再试。"
					s.onboardingRedetectRequired = true
				}
			})
			return
		}
		installer := environment.Installer()
		running, failureLogged := false, false
		installer.OnChange = func(j bootstrap.Journal) {
			var lines []onboardingLogLine
			if initialSetup && running && j.Status == bootstrap.Failed && !failureLogged {
				step := j.Steps[j.ActiveStep]
				if step.Status == bootstrap.Failed {
					lines = makeOnboardingLogLines("["+j.ActiveStep+"] 失败："+step.Error+"\n"+step.Log, true)
					failureLogged = true
				}
			}
			a.dispatch(func() {
				if s.Generation == generation {
					s.Journal = j
					s.appendOnboardingLog(lines)
				}
			})
		}
		opID, err := bootstrap.ResolveInstallOperationID(environment.Options.Root, "")
		if err == nil {
			switch {
			case approve:
				err = installer.Review(opID, plan, plan.Digest(), true)
			case journal.Operation != opID || journal.ApprovedDigest == "":
				err = bootstrap.ErrConsent
			}
		}
		if err == nil {
			if initialSetup {
				line := fmt.Sprintf("[install] 已同意安装计划 · SHA-256 %s · 下载来源：%s", plan.Digest(), onboardingSourceLabel(environment.Options.DownloadSource))
				if !approve {
					line = "[install] 恢复已同意的安装计划 · SHA-256 " + plan.Digest()
				}
				log := makeOnboardingLogLines(line, false)
				a.dispatch(func() {
					if s.Generation == generation {
						s.appendOnboardingLog(log)
					}
				})
			}
			runCtx := a.trackEnvironmentProgress(ctx, generation, initialSetup, plan)
			running = true
			err = installer.Run(runCtx, opID, plan)
		}
		var updatedPluginDir string
		var failedPluginApplyDir string
		if err == nil && pluginDestination != "" {
			candidate := filepath.Join(pluginDestination, "plugin")
			if _, applyErr := bootstrap.ApplyManagedPluginUpdate(configFile, officialData, candidate, environment.Plugin.Manifest); applyErr != nil {
				err = applyErr
				failedPluginApplyDir = candidate
			} else {
				updatedPluginDir = candidate
				environment.Options.Plugin = candidate
				environment.Plugin.ExistingPath = candidate
				if _, selErr := bootstrap.LoadSelections(environment.Options.Root, configFile); selErr == nil {
					_ = bootstrap.SaveSelections(environment.Options, configFile)
				}
			}
		}
		var detected bootstrap.Plan
		if err == nil {
			detected, err = environment.Detect(context.Background())
		}
		var safeError string
		var lines []onboardingLogLine
		cancelled := errors.Is(err, context.Canceled)
		if cancelled {
			if initialSetup {
				lines = makeOnboardingLogLines("[install] "+environmentInstallStopped, false)
			}
		} else if err != nil {
			safeError = bootstrap.RedactInstallerText(err.Error())
			if initialSetup && !failureLogged {
				lines = makeOnboardingLogLines("[install] "+safeError, true)
			}
		} else if initialSetup {
			lines = makeOnboardingLogLines("[install] 环境组件已准备完成", false)
		}
		a.dispatch(func() {
			if s.Generation != generation {
				return
			}
			s.Busy, s.Cancel = false, nil
			s.Progress = bootstrap.InstallProgress{}
			s.appendOnboardingLog(lines)
			s.onboardingRedetectRequired = errors.Is(err, bootstrap.ErrPlanChanged)
			if cancelled {
				s.Message = environmentInstallStopped
			} else if err != nil {
				s.Error = safeError
				if failureLogged {
					s.onboardingInstallFailureDigest = plan.Digest()
				}
				if failedPluginApplyDir != "" {
					s.PendingPluginApplyDir = failedPluginApplyDir
				}
			} else {
				s.Plan = detected
				if updatedPluginDir != "" {
					s.PendingPluginApplyDir = ""
					s.Plugin = updatedPluginDir
					if a.config.ProjectControl != nil {
						a.config.ProjectControl.PluginDir = updatedPluginDir
					}
					s.Message = "Agent 插件已更新并写入配置，重启日常服务后后台将加载新版插件。"
				} else {
					s.Message = "环境组件已准备完成"
					if a.initialSetup && s.InitialStep == 2 && detected.Complete() {
						a.continueEnvironment()
					}
				}
			}
		})
	}()
}

func (a *Controller) declineEnvironment() {
	s := &a.environment
	if s.Busy {
		return
	}
	s.Review = false
	if s.Environment == nil || s.ExternalWorkerActive || !a.canDispatch() {
		return
	}
	s.Busy = true
	environment, plan := s.Environment, s.Plan
	go func() {
		opID, err := bootstrap.ResolveInstallOperationID(environment.Options.Root, "")
		if err == nil {
			err = environment.Installer().Review(opID, plan, plan.Digest(), false)
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else {
				s.Message = "暂不安装，已有环境和准备进度会保留。"
			}
		})
	}()
}

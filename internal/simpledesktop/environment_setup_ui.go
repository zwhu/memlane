package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
)

type environmentSetupState struct {
	PathDialog                                     bool
	PathDraft                                      environmentPathDraft
	CopiedPath                                     string
	Loaded, Loading, Busy, Review, Details, Manual bool
	Plan                                           bootstrap.Plan
	Journal                                        bootstrap.Journal
	Environment                                    *bootstrap.Environment
	Error, Message                                 string
	Python, UV, Source, Archive, ModelCache        string
	Plugin                                         string
	PendingPluginApplyDir                          string
	SelectionLoaded                                bool
	DownloadSource                                 string
	DownloadSourceIdx                              int
	Generation                                     uint64
	Cancel                                         context.CancelFunc
	InitialStep                                    int
	Exporting                                      bool
	AgentOpen                                      bool
	DiagnosticAgents                               []bootstrap.DiagnosticAgent
	RuntimeStarted                                 bool
	RuntimeStartError                              string
	AutoRuntimeAttempted                           bool
	RuntimeStage                                   string
	OnboardingRuntimeFailed                        bool
	onboardingInstallFailureDigest                 string
	onboardingStartRequested                       bool
	OnboardingLogOpen                              bool
	OnboardingLog                                  []onboardingLogLine
	onboardingLogScroll                            ui.ScrollState
	onboardingRedetectRequired                     bool
	RuntimePreparation                             bootstrap.FirstRuntime

	Hosts                                     []bootstrap.DetectedHost
	HostsLoaded, HostsLoading                 bool
	HostDetectGen                             uint64
	SelectedAgents                            []string
	SelectedAgentsExplicit                    bool
	selectedAgentsSaving, selectedAgentsDirty bool
	pinnedPreviewRequested                    map[string]pinnedPreviewVersion
	TechDetailsOpen, ReviewTechOpen           bool
	Progress                                  bootstrap.InstallProgress
	ExternalWorker                            bootstrap.InstallWorkerState
	ExternalWorkerActive                      bool
	WorkerPollNext                            time.Time
	workerInspecting                          bool
	OwnerStatus                               *bootstrap.OwnerRuntimeStatus
	OwnerStatusChecked                        bool
	ownerInspecting                           bool
	RestartingStaleDaemon                     bool
	StaleDaemonError                          string
	StaleRescueCopied                         bool
	FastLaneChecked                           bool
}

func loadEnvironmentOptions(config Config, s environmentSetupState) (bootstrap.EnvironmentOptions, error) {
	options := bootstrap.EnvironmentOptions{Root: os.Getenv("MEMLANE_BOOTSTRAP_ROOT"), Python: s.Python, UV: s.UV, Source: s.Source, Archive: s.Archive, ModelCache: s.ModelCache, Plugin: s.Plugin, ProtectedStore: config.OfficialData, DownloadSource: s.DownloadSource}
	if !s.SelectionLoaded {
		options.Python, options.Source, options.Archive = config.Python, config.OfficialSource, config.OfficialArchive
		if config.ProjectControl != nil {
			options.Plugin = config.ProjectControl.PluginDir
		}
		if config.ServiceEnvironmentFile != "" {
			// Decode only model selection metadata; never expose the environment or
			// credentials to diagnostics, clipboard or component subprocesses.
			b, err := os.ReadFile(config.ServiceEnvironmentFile)
			if err != nil {
				return options, err
			}
			var fields struct {
				Model  string `json:"EMBEDDING_MODEL"`
				HFHome string `json:"HF_HOME"`
			}
			if err := json.Unmarshal(b, &fields); err != nil {
				return options, err
			}
			if fields.Model != "" && fields.Model != "BAAI/bge-small-zh-v1.5" {
				return options, errors.New("已有向量模型与首次准备模型不同，请保留原模型配置并核对兼容性")
			}
			options.ModelCache = fields.HFHome
		}
		selection, err := bootstrap.LoadSelections(options.Root, config.ConfigFile)
		if err == nil {
			options.Python, options.UV, options.Source, options.Archive, options.ModelCache = selection.Python, selection.UV, selection.Source, selection.Archive, selection.ModelCache
			if selection.Plugin != "" {
				options.Plugin = selection.Plugin
			}
			if selection.DownloadSource != "" {
				options.DownloadSource = selection.DownloadSource
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return options, err
		}
	}
	options.DownloadSource = bootstrap.NormalizeDownloadSource(options.DownloadSource)
	if options.Root != "" && config.OfficialData != "" {
		root, _ := filepath.Abs(options.Root)
		store, _ := filepath.Abs(config.OfficialData)
		for p := root; ; p = filepath.Dir(p) {
			if p == store {
				return options, errors.New("组件位置不能位于日常记忆库内")
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	return options, nil
}

func (a *App) applyEnvironmentOptions(options bootstrap.EnvironmentOptions) {
	s := &a.environment
	if s.SelectionLoaded {
		return
	}
	s.Python, s.UV, s.Source, s.Archive, s.ModelCache = options.Python, options.UV, options.Source, options.Archive, options.ModelCache
	s.Plugin = options.Plugin
	s.DownloadSource = options.DownloadSource
	a.syncDownloadSourceIdx()
	s.SelectionLoaded = true
}

func (a *App) refreshEnvironment() {
	a.detectEnvironment(false)
}

func (a *App) detectEnvironment(saveSelection bool) {
	a.detectEnvironmentThen(saveSelection, nil)
}

func (a *App) detectEnvironmentThen(saveSelection bool, afterDetection func()) {
	s := &a.environment
	if s.Busy || s.Loading || a.Update == nil {
		return
	}
	s.Loading, s.Review, s.ReviewTechOpen, s.Error = true, false, false, ""
	s.onboardingInstallFailureDigest = ""
	s.Generation++
	generation := s.Generation
	config, selection, initialSetup := a.Config, *s, a.initialSetup
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		options, optionsErr := loadEnvironmentOptions(config, selection)
		err := optionsErr
		var environment *bootstrap.Environment
		if err == nil {
			environment, err = bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
		}
		if err == nil && saveSelection {
			err = bootstrap.SaveSelections(options, config.ConfigFile)
		}
		var plan bootstrap.Plan
		var journal bootstrap.Journal
		if err == nil {
			plan, err = environment.Detect(ctx)
			journal, _ = environment.Installer().Snapshot()
		}
		var historicalLog []onboardingLogLine
		if initialSetup && len(selection.OnboardingLog) == 0 && journal.Status == bootstrap.Failed {
			if step := journal.Steps[journal.ActiveStep]; step.Status == bootstrap.Failed {
				historicalLog = makeOnboardingLogLines("["+journal.ActiveStep+"] "+step.Error+"\n"+step.Log, true)
			}
		}
		var safeError string
		if err != nil {
			safeError = bootstrap.RedactInstallerText(err.Error())
		}
		a.Update(func() {
			if s.Generation != generation {
				return
			}
			s.Loading, s.Loaded = false, true
			defer a.advanceFirstRuntimeStartup()
			if optionsErr != nil {
				s.onboardingRedetectRequired = true
				s.Error = safeError
				return
			}
			a.applyEnvironmentOptions(options)
			s.Environment, s.Plan, s.Journal = environment, plan, journal
			s.onboardingRedetectRequired = err != nil
			if err != nil {
				s.Error = safeError
				return
			}
			s.appendOnboardingLog(historicalLog)
			if afterDetection != nil {
				afterDetection()
				return
			}
			if !plan.Complete() {
				for _, item := range plan.Items {
					if journal.Status == bootstrap.Failed && environmentRowStatus(item, journal, false) == bootstrap.Failed {
						s.Error = bootstrap.RedactInstallerText(journal.Steps[item.Requirement.ID].Error)
						s.onboardingInstallFailureDigest = journal.ApprovedDigest
						break
					}
				}
			}
		})
	}()
}

const environmentInstallStopped = "已停止，已完成的准备会保留。"

func (a *App) runEnvironment(approve bool) {
	s := &a.environment
	if s.ExternalWorkerActive {
		s.Message = "命令行正在后台安装组件，请等待其完成或查看上方实时进度。"
		return
	}
	if s.Busy || s.Loading || s.selectedAgentsSaving || s.workerInspecting || s.RestartingStaleDaemon || s.Environment == nil || a.Update == nil {
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
	configFile := a.Config.ConfigFile
	officialData := a.Config.OfficialData
	inspectWorker := a.installWorkerInspector()
	go func() {
		defer cancel()
		inspectionCtx, endInspection := context.WithTimeout(ctx, 5*time.Second)
		worker, active, inspectErr := inspectWorker(inspectionCtx, environment.Options.Root)
		endInspection()
		if active || inspectErr != nil || ctx.Err() != nil {
			stopped := errors.Is(ctx.Err(), context.Canceled)
			a.Update(func() {
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
			a.Update(func() {
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
				a.Update(func() {
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
		a.Update(func() {
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
					if a.Config.ProjectControl != nil {
						a.Config.ProjectControl.PluginDir = updatedPluginDir
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

func (a *App) declineEnvironment() {
	s := &a.environment
	if s.Busy {
		return
	}
	s.Review = false
	if s.Environment == nil || s.ExternalWorkerActive || a.Update == nil {
		return
	}
	s.Busy = true
	environment, plan := s.Environment, s.Plan
	go func() {
		opID, err := bootstrap.ResolveInstallOperationID(environment.Options.Root, "")
		if err == nil {
			err = environment.Installer().Review(opID, plan, plan.Digest(), false)
		}
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else {
				s.Message = "暂不安装，已有环境和准备进度会保留。"
			}
		})
	}()
}

func (a *App) environmentDiagnostic() (string, string) {
	report, prompt, err := bootstrap.Diagnostics(a.environmentDiagnosticInput())
	if err != nil {
		return "", err.Error()
	}
	return report, prompt
}

func (a *App) environmentDiagnosticInput() bootstrap.DiagnosticInput {
	s := &a.environment
	input := bootstrap.DiagnosticInput{Operation: "first-install", Stage: "环境准备", Platform: "macOS", Error: s.Error, Actions: []string{"检测已有依赖", "保留已有环境及安装进度"}}
	plan := s.Plan
	input.Plan = &plan
	if s.Journal.Operation != "" {
		j := s.Journal
		input.Journal = &j
		if j.ActiveStep != "" {
			input.Stage = j.ActiveStep
			if step, ok := j.Steps[j.ActiveStep]; ok {
				input.ExitCode, input.InstallerLog = step.ExitCode, step.Log
			}
		}
	}
	if len(s.OnboardingLog) > 0 && (a.initialSetup || input.InstallerLog == "") {
		input.InstallerLog = s.onboardingLogText()
	}
	if s.RuntimeStage != "" {
		input.Stage = s.RuntimeStage
		input.Actions = append(input.Actions, "保留首次内容库及后台所有权，等待真实服务回执")
	}
	return input
}

func (a *App) exportEnvironmentDiagnostic() {
	s := &a.environment
	if s.Exporting || a.ChooseDiagnosticPath == nil {
		return
	}
	path, err := a.ChooseDiagnosticPath()
	if err != nil {
		s.onboardingInstallFailureDigest = ""
		s.OnboardingRuntimeFailed = false
		s.Error = bootstrap.RedactInstallerText(err.Error())
		return
	}
	if path == "" {
		return
	}
	input := a.environmentDiagnosticInput()
	s.Exporting = true
	go func() {
		err := bootstrap.ExportDiagnostic(path, input)
		a.Update(func() {
			s.Exporting = false
			if err != nil {
				s.onboardingInstallFailureDigest = ""
				s.OnboardingRuntimeFailed = false
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else {
				s.Message = "诊断包已导出"
			}
		})
	}()
}

func environmentSize(n int64) string {
	if n < 0 {
		return "尚未确定"
	}
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
func environmentLabel(status bootstrap.Status) string {
	switch status {
	case bootstrap.Available, bootstrap.Ready:
		return "可用"
	case bootstrap.Installing:
		return "正在安装"
	case bootstrap.Failed:
		return "失败"
	case bootstrap.Cancelled:
		return "已停止"
	case "unconfirmed":
		return "准备结果待核对"
	case bootstrap.Incompatible:
		return "不兼容"
	default:
		return "需要安装"
	}
}

func environmentRowStatus(item bootstrap.Item, j bootstrap.Journal, busy bool) bootstrap.Status {
	if item.Observation.Status == bootstrap.Available {
		return bootstrap.Available
	}
	for _, old := range j.Plan.Items {
		if old.Requirement == item.Requirement {
			if step, ok := j.Steps[item.Requirement.ID]; ok {
				if step.Status == bootstrap.Installing && !busy {
					return "unconfirmed"
				}
				return step.Status
			}
		}
	}
	return item.Observation.Status
}

func (a *App) managedPluginUpdateDestination(plan bootstrap.Plan) string {
	if a.initialSetup || a.Config.ProjectControl == nil || a.Config.ProjectControl.PluginDir == "" || a.Config.ConfigFile == "" {
		return ""
	}
	for _, item := range plan.Items {
		if item.Requirement.ID == "agent-plugin" && item.Observation.Status != bootstrap.Available &&
			(a.service == "已连接" || isPluginPolicyUpdate(item, item.Observation.Status)) {
			return item.Requirement.Destination
		}
	}
	return ""
}

func isPluginPolicyUpdate(item bootstrap.Item, status bootstrap.Status) bool {
	return item.Requirement.ID == "agent-plugin" && status == bootstrap.Incompatible && bootstrap.IsPluginPolicyUpdate(item.Observation)
}

func isDailyPluginUpdateAvailable(a *App, item bootstrap.Item, status bootstrap.Status) bool {
	return a != nil && !a.initialSetup && a.service == "已连接" &&
		item.Requirement.ID == "agent-plugin" &&
		status == bootstrap.Incompatible &&
		item.Observation.Status == bootstrap.Incompatible &&
		a.Config.ProjectControl != nil &&
		a.Config.ProjectControl.PluginDir != ""
}

func isOnlyPluginUpdate(a *App, plan bootstrap.Plan, j bootstrap.Journal, busy bool) bool {
	if len(plan.Items) == 0 || plan.Complete() {
		return false
	}
	hasUpdate := false
	for _, item := range plan.Items {
		status := environmentRowStatus(item, j, busy)
		if status == bootstrap.Available {
			continue
		}
		if !isDailyPluginUpdateAvailable(a, item, status) && !isPluginPolicyUpdate(item, status) {
			return false
		}
		hasUpdate = true
	}
	return hasUpdate
}

func environmentIncompatibleHint(a *App, row bootstrap.Item) string {
	if row.Observation.Status != bootstrap.Incompatible {
		return ""
	}
	if isPluginPolicyUpdate(row, row.Observation.Status) {
		if isDailyPluginUpdateAvailable(a, row, row.Observation.Status) {
			return bootstrap.PluginPolicyUpdateReasonPrefix + "，不影响当前服务使用。点击下方「更新 Agent 插件」确认后会自动切换配置，原插件目录会保留以便回退。"
		}
		return bootstrap.PluginPolicyUpdateReasonPrefix + "。点击下方「更新 Agent 插件」确认后即可更新，原插件目录会保留。"
	}
	if isDailyPluginUpdateAvailable(a, row, row.Observation.Status) {
		return "当前服务正在使用旧版接入插件，不影响正常使用。点击下方「更新 Agent 插件」确认后，会下载新版并自动切换配置，原插件目录会保留以便回退。"
	}
	return bootstrap.RedactInstallerText(row.Observation.Reason)
}

func (a *App) hasPendingPluginConfigApply() bool {
	s := &a.environment
	return !a.initialSetup && s.PendingPluginApplyDir != "" &&
		a.Config.ProjectControl != nil && a.Config.ProjectControl.PluginDir != s.PendingPluginApplyDir
}

func (a *App) applyPendingPluginConfig() {
	s := &a.environment
	if s.Busy || s.Loading || s.Environment == nil || !a.hasPendingPluginConfigApply() || a.Config.ConfigFile == "" {
		return
	}
	environment := s.Environment
	candidate := s.PendingPluginApplyDir
	configFile := a.Config.ConfigFile
	officialData := a.Config.OfficialData
	s.Busy, s.Error, s.Message = true, "", ""
	s.Generation++
	generation := s.Generation
	go func() {
		_, err := bootstrap.ApplyManagedPluginUpdate(configFile, officialData, candidate, environment.Plugin.Manifest)
		var detected bootstrap.Plan
		if err == nil {
			environment.Options.Plugin = candidate
			environment.Plugin.ExistingPath = candidate
			if _, selErr := bootstrap.LoadSelections(environment.Options.Root, configFile); selErr == nil {
				_ = bootstrap.SaveSelections(environment.Options, configFile)
			}
			detected, err = environment.Detect(context.Background())
		}
		a.Update(func() {
			if s.Generation != generation {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
				return
			}
			s.PendingPluginApplyDir = ""
			s.Plugin = candidate
			if a.Config.ProjectControl != nil {
				a.Config.ProjectControl.PluginDir = candidate
			}
			s.Plan = detected
			s.Message = "Agent 插件已更新并写入配置，重启日常服务后后台将加载新版插件。"
		})
	}()
}

func (a *App) environmentSetup(c *ui.Context, initial bool) {
	if initial {
		a.onboardingEnvironment(c)
		a.environmentReviewModal(c)
		a.initialEnvironmentDetails(c)
		return
	}
	s := &a.environment
	if !s.Loaded && !s.Loading && a.Update != nil {
		a.refreshEnvironment()
	}
	pendingPluginApply := a.hasPendingPluginConfigApply()
	ui.Column(c.Key("settings-environment-section")).FillWidth().Gap(sp3).Children(func() {
		v4Group(c, "运行环境", "", func() {
			if s.Plan.Complete() && !s.Busy && !pendingPluginApply {
				v4Badge(c, "已就绪", "ok")
			}
			if s.Loading && s.Loaded {
				v4Spinner(c)
			}
			v4SmallButton(c, "重新检测", v4IconRefresh, a.refreshEnvironment).Disabled(s.Loading || s.Busy)
			label := "查看详情"
			if s.Details {
				label = "收起详情"
			}
			v4SmallButton(c, label, nil, func() { s.Details = !s.Details })
		}, func() {
			if s.Loading && !s.Loaded {
				v4SkeletonGroup(c, 3, 2, true, true)
				return
			}
			v4Row(c, v4RowOptions{
				Key:   "settings-download-source-row",
				Title: "下载来源",
				Desc:  "国内镜像源优先走清华 / 阿里云 / 中科大 / npmmirror / ModelScope，仍按固定 SHA256 严格校验",
				Control: func() {
					a.downloadSourceSelector(c, "settings-download-source")
				},
			})
			for _, item := range s.Plan.Items {
				row := item
				status := environmentRowStatus(row, s.Journal, s.Busy)
				dailyUpdate := isDailyPluginUpdateAvailable(a, row, status)
				dotState := environmentDotState(status)
				valueLabel := environmentLabel(status)
				valueColor := environmentStatusColor(status)
				var extraHint string
				var extraColor ui.Color
				var detailReason string
				if pendingPluginApply && row.Requirement.ID == "agent-plugin" {
					dotState = "warn"
					valueLabel = "待写入配置"
					valueColor = tok.Warning
					extraHint = "新版 Agent 插件已准备并校验完成，但尚未写入当前日常配置；请点击下方「写入插件配置」重试。"
					extraColor = tok.Text2
				} else if policyUpdate := isPluginPolicyUpdate(row, status); dailyUpdate || policyUpdate {
					dotState = "run"
					valueLabel = "有更新"
					if policyUpdate {
						valueLabel = "策略有更新"
					}
					valueColor = tok.Accent
					extraHint = environmentIncompatibleHint(a, row)
					extraColor = tok.Text2
					if s.Details && row.Observation.Reason != "" {
						detailReason = "技术详情：" + bootstrap.RedactInstallerText(row.Observation.Reason)
					}
				} else if status == bootstrap.Incompatible {
					if hint := environmentIncompatibleHint(a, row); hint != "" {
						extraHint = hint
						extraColor = tok.Warning
					}
				} else if s.Details && row.Observation.Reason != "" && status != bootstrap.Available && status != bootstrap.Ready {
					extraHint = bootstrap.RedactInstallerText(row.Observation.Reason)
					extraColor = tok.Text3
				}
				var descExtra func()
				if extraHint != "" || detailReason != "" {
					hintText, hintCol, techText := extraHint, extraColor, detailReason
					descExtra = func() {
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							if hintText != "" {
								ui.Text(c, hintText).Font("system-ui").FontSize(fsCaption).TextColor(hintCol).LineHeight(1.5)
							}
							if techText != "" {
								ui.Text(c, techText).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
							}
						})
					}
				}
				v4Row(c, v4RowOptions{
					Key:       "environment-" + row.Requirement.ID,
					Title:     dependencyDisplayName(row.Requirement),
					Desc:      dependencyDisplayVersion(row, s.Environment),
					DescMono:  true,
					DescExtra: descExtra,
					Tooltip:   row.Requirement.Version,
					Leading: func() {
						ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
							v4StatusDot(c, dotState)
							dependencyMark(c, row.Requirement.ID)
						})
					},
					Value:      valueLabel,
					ValueColor: valueColor,
				})
			}
		})

		if s.Error != "" {
			ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp1).Radius(radius).Background(tok.Danger.Alpha(0.08)).Children(func() {
				ui.Text(c, "准备未完成").Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Danger)
				summary := ui.Text(c, bootstrap.RedactInstallerText(s.Error)).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text2).LineHeight(1.6)
				if !s.Details {
					summary.MaxLines(2)
				}
			})
		}
		if s.Message != "" {
			ui.Text(c, s.Message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
		}
		if s.Busy {
			stageTitle := "正在准备依赖组件…"
			var stopFn func()
			if s.RuntimeStage != "" {
				stageTitle = s.RuntimeStage + "…"
			} else {
				stopFn = func() {
					if s.Cancel != nil {
						s.Cancel()
					}
				}
			}
			a.setupStageBanner(c, stageTitle, "请稍候，完成后将自动校验依赖状态。", stopFn)
		} else {
			needInstall := !s.Plan.Complete() && len(s.Plan.Items) > 0
			if needInstall || pendingPluginApply || s.Error != "" {
				actionLabel := "安装缺少的依赖"
				if isOnlyPluginUpdate(a, s.Plan, s.Journal, s.Busy) {
					actionLabel = "更新 Agent 插件"
				}
				ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
					if pendingPluginApply {
						v4SmallPrimaryButton(c, "写入插件配置", a.applyPendingPluginConfig).Disabled(s.Loading)
					} else if needInstall {
						v4SmallPrimaryButton(c, actionLabel, func() { s.Review = true }).Disabled(s.Loading)
						v4SmallButton(c, "复制安装命令", v4IconCopy, func() { a.copyManualInstallScript(c) }).Disabled(s.Loading)
					}
					if s.Error != "" {
						v4SmallButton(c, "重试", v4IconRefresh, func() {
							if s.Journal.ApprovedDigest != "" {
								a.runEnvironment(false)
							} else {
								a.refreshEnvironment()
							}
						})
						v4SmallButton(c, "用 Agent 排查", nil, func() {
							_, prompt := a.environmentDiagnostic()
							c.WriteClipboard(prompt)
							s.Message = "已复制排查提示词"
							s.AgentOpen = true
							s.DiagnosticAgents = bootstrap.ExistingDiagnosticAgents(nil, nil)
						})
					}
				})
			}
		}
		a.environmentDiagnosticAgents(c)
		if s.Details {
			a.environmentLocations(c)
			ui.Row(c.Key("environment-detail-actions")).FillWidth().AlignItems(ui.Center).Gap(sp2).Padding(sp2, 0, 0, 0).Children(func() {
				v4SmallButton(c, "复制诊断材料", v4IconCopy, func() {
					report, _ := a.environmentDiagnostic()
					c.WriteClipboard(report)
					s.Message = "脱敏诊断材料已复制"
				})
				v4SmallButton(c, "导出诊断包", nil, a.exportEnvironmentDiagnostic).Disabled(s.Exporting || a.ChooseDiagnosticPath == nil)
				ui.Spacer(c)
				v4SmallButton(c, "指定已有路径", nil, a.openEnvironmentPaths).Disabled(s.Busy || s.Loading)
			})
		}
	})
	a.environmentReviewModal(c)
	a.environmentPathsDialog(c)
}

func (a *App) continueEnvironment() {
	s := &a.environment
	if a.canEnterConnectionStep() {
		a.setInitialStep(3)
		return
	}
	if a.Config.FirstInstall != nil && (s.ownerInspecting || !s.OwnerStatusChecked || s.OwnerStatus == nil) {
		s.onboardingStartRequested = true
		a.inspectOwnerRuntimeStatusAsync()
		return
	}
	a.beginFirstRuntime(3)
}

func (a *App) environmentDiagnosticAgents(c *ui.Context) {
	s := &a.environment
	if s.AgentOpen {
		ui.Column(c).FillWidth().Gap(sp2).Margin(sp2, 0).Children(func() {
			ui.Text(c, "在已有 Agent 的新会话中粘贴提示词").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
			ui.Row(c).Gap(sp2).Children(func() {
				for _, entry := range s.DiagnosticAgents {
					agent := entry
					label := "复制给 " + agent.Name
					if agent.Application != "" && a.OpenExistingAgent != nil {
						label = "打开 " + agent.Name
					}
					v4SmallButton(c, label, nil, func() {
						_, prompt := a.environmentDiagnostic()
						c.WriteClipboard(prompt)
						s.Message = "已复制，请在 " + agent.Name + " 新会话中粘贴"
						if agent.Application != "" && a.OpenExistingAgent != nil {
							if err := a.OpenExistingAgent(agent.Application); err != nil {
								s.Message = "提示词已复制，请手动打开 " + agent.Name
							}
						}
					})
				}
				v4SmallButton(c, "复制完整提示词", v4IconCopy, func() {
					_, prompt := a.environmentDiagnostic()
					c.WriteClipboard(prompt)
					s.Message = "已复制排查提示词"
				})
			})
		})
	}
}

func (a *App) syncDownloadSourceIdx() {
	s := &a.environment
	mode := bootstrap.NormalizeDownloadSource(s.DownloadSource)
	s.DownloadSource = mode
	if mode == bootstrap.DownloadSourceOfficial {
		s.DownloadSourceIdx = 1
	} else {
		s.DownloadSourceIdx = 0
	}
}

func (a *App) switchDownloadSource(mode string) {
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
	if a.Update != nil {
		a.detectEnvironment(true)
	}
}

func (a *App) downloadSourceSelector(c *ui.Context, key string) {
	s := &a.environment
	a.syncDownloadSourceIdx()
	labels := []string{"国内镜像源", "国外官方源"}
	v4Segmented(c, key, "下载来源", &s.DownloadSourceIdx, labels, func() {
		if s.DownloadSourceIdx == 1 {
			a.switchDownloadSource(bootstrap.DownloadSourceOfficial)
		} else {
			a.switchDownloadSource(bootstrap.DownloadSourceChina)
		}
	})
}

func (a *App) copyManualInstallScript(c *ui.Context) {
	s := &a.environment
	script := bootstrap.ManualInstallScript(s.Plan, s.Environment, s.DownloadSource)
	c.WriteClipboard(script)
	s.Message = "已复制终端安装命令，执行后请点击「重新检测」"
}

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type EnvironmentSetupState struct {
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

func loadEnvironmentOptions(tok *kitui.Tokens, config Config, s EnvironmentSetupState) (bootstrap.EnvironmentOptions, error) {
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

func (a *Controller) applyEnvironmentOptions(options bootstrap.EnvironmentOptions) {
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

func (a *Controller) refreshEnvironment() {
	a.detectEnvironment(false)
}

func (a *Controller) detectEnvironment(saveSelection bool) {
	a.detectEnvironmentThen(saveSelection, nil)
}

func (a *Controller) detectEnvironmentThen(saveSelection bool, afterDetection func()) {
	tok := a.tokens()
	s := &a.environment
	if s.Busy || s.Loading || !a.canDispatch() {
		return
	}
	s.Loading, s.Review, s.ReviewTechOpen, s.Error = true, false, false, ""
	s.onboardingInstallFailureDigest = ""
	s.Generation++
	generation := s.Generation
	config, selection, initialSetup := a.config, *s, a.initialSetup
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		options, optionsErr := loadEnvironmentOptions(tok, config, selection)
		err := optionsErr
		var environment *bootstrap.Environment
		if err == nil {
			environment, err = bootstrap.NewEnvironment(ctx, a.host.BootstrapAssets, options)
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
		a.dispatch(func() {
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

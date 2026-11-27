package settings

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// Controller owns the settings and first-install state for one desktop window.
type Controller struct {
	host                Host
	config              Config
	client              Client
	dispatch            func(func())
	baseReadingIdentity string
	fallbackTokens      *kitui.Tokens

	environment                 EnvironmentSetupState
	initialSetup                bool
	onboarding                  onboardingState
	onboardingConnection        onboardingConnectionState
	onboardingGeneration        onboardingGenerationState
	setupDeferredMode           bool
	setupDeferredToastDismissed bool
	integrations                IntegrationState
	access                      ProjectAccessState
	toolGraph                   toolGraphUIState
	poseEnter                   kitui.PoseTracker
	commonAccessScroll          ui.ScrollState
	monitor                     ServiceMonitorState
	loginError                  string
	trayCache                   trayFlyoutCache
	codeBusy                    bool

	detectLocalHosts              func(bootstrap.HostDetectOptions) []bootstrap.DetectedHost
	prepareAndStartFirstRuntime   func(context.Context, Config, EnvironmentSetupState) (bootstrap.FirstRuntime, error)
	activateFirstRuntime          func(context.Context, bootstrap.FirstRuntime, bootstrap.FirstGenerationReference, string, int64) error
	firstInitComplete             func(string) (bool, error)
	inspectOwnerRuntimeStatusHook func(context.Context, string, string) (bootstrap.OwnerRuntimeStatus, error)
	restartStaleRuntimeOwnerHook  func(context.Context, bootstrap.FirstRuntime, string) error
	inspectInstallWorkerHook      func(context.Context, string) (bootstrap.InstallWorkerState, bool, error)
}

func New(c Config, err error, host Host) *Controller {
	a := &Controller{host: host, config: c, client: Client{Config: c, HTTP: host.HTTP}, fallbackTokens: kitui.LightTokens(), monitor: ServiceMonitorState{EngineStatus: "尚未检查", CompletionStatus: "尚未检查"}}
	if host.Dispatcher != nil {
		a.dispatch = host.Dispatcher.Dispatch
	}
	a.refreshBaseIdentity()
	if c.GenerationSetupOnly {
		a.access.SettingsSection = "Agent 连接"
	}
	if err != nil {
		a.initialSetup = c.ConfigFile == "" || (c.FirstInstallDefault && errors.Is(err, os.ErrNotExist))
	}
	if err == nil && c.FirstInstall != nil {
		complete, stateError := bootstrap.FirstInitializationComplete(c.ConfigFile)
		a.initialSetup = !complete
		if stateError != nil {
			a.environment.Error = bootstrap.RedactInstallerText(stateError.Error())
		}
		if !complete {
			deferred, deferErr := bootstrap.FirstSetupDeferred(c.ConfigFile)
			switch {
			case deferErr != nil:
				a.environment.Error = bootstrap.RedactInstallerText(deferErr.Error())
			case deferred:
				a.initialSetup, a.setupDeferredMode = false, true
			}
		}
		if a.initialSetup {
			state, stepError := bootstrap.ReadFirstWizardState(c.ConfigFile)
			a.environment.InitialStep = state.Step
			if stepError != nil && a.environment.Error == "" {
				a.environment.Error = bootstrap.RedactInstallerText(stepError.Error())
			}
		}
	}
	if a.initialSetup && a.environment.InitialStep == 0 {
		a.environment.InitialStep = 1
	}
	return a
}

func (a *Controller) tokens() *kitui.Tokens {
	if a.host.Theme != nil {
		return a.host.Theme.Tokens()
	}
	return a.fallbackTokens
}

func (a *Controller) windowIsHidden() bool {
	return a.host.WindowHidden != nil && a.host.WindowHidden()
}
func (a *Controller) windowIsInactive() bool {
	return a.host.WindowInactive != nil && a.host.WindowInactive()
}
func (a *Controller) coldStartOverviewPending() bool {
	return a.host.OverviewPending != nil && a.host.OverviewPending()
}

func (a *Controller) setConfig(config Config) {
	changed := a.client.Config.ServiceURL != config.ServiceURL || a.client.Config.TokenFile != config.TokenFile
	a.config, a.client.Config = config, config
	if changed {
		a.refreshBaseIdentity()
	}
	if a.host.ConfigChanged != nil {
		a.host.ConfigChanged(config)
	}
}

func (a *Controller) loadRuntimeConfig(config Config) {
	a.setConfig(config)
	if a.host.RuntimeConfigLoaded != nil {
		a.host.RuntimeConfigLoaded(config)
	}
}

func (a *Controller) Start() {
	if a.initialSetup {
		if a.onboardingPage() == onboardingDone {
			return
		}
		a.refreshLocalHosts()
		a.inspectInstallWorkerAsync()
		if a.config.FirstInstall != nil {
			a.InspectOwnerRuntimeStatusAsync()
			if a.environment.RuntimeStarted && a.environment.InitialStep == 3 {
				a.refreshWizardIntegrations()
			}
		}
		if !a.environment.RuntimeStarted {
			a.refreshEnvironment()
		}
		return
	}
	if a.config.FirstInstall != nil && !a.environment.RuntimeStarted && !a.environment.OwnerStatusChecked {
		a.InspectOwnerRuntimeStatusAsync()
		return
	}
	if a.config.GenerationSetupOnly && !a.setupDeferredMode {
		a.InspectOwnerRuntimeStatusAsync()
		a.RefreshIntegrations()
		return
	}
	if a.host.RuntimeReady != nil {
		a.host.RuntimeReady()
		return
	}
	a.RefreshProjectDirectory()
	a.RefreshIntegrations()
	a.health()
}

func (a *Controller) navigateOverview() {
	if a.host.Router != nil {
		a.host.Router.NavigateOverview()
	}
}

func (a *Controller) SetSection(section string) { a.access.SettingsSection = section }

// OpenGlobal selects a tab and refreshes the settings data on navigation.
func (a *Controller) OpenGlobal(section string) {
	a.access.OpenProject = ""
	if section != "" {
		a.SetSection(section)
	}
	a.RefreshIntegrations()
	if a.host.RefreshOpenAtLogin != nil {
		a.host.RefreshOpenAtLogin()
	}
}

// CurrentConfig returns the current runtime configuration and project registry view.
func (a *Controller) CurrentConfig() Config { return a.config }

// UseFirstGenerationView reports whether to render the dedicated setup-only editor.
func (a *Controller) UseFirstGenerationView() bool {
	return a.config.GenerationSetupOnly && !a.setupDeferredMode
}
func (a *Controller) InitialSetup() bool       { return a.initialSetup }
func (a *Controller) SetupOnlyRuntime() bool   { return a.isSetupOnlyRuntime() }
func (a *Controller) ViewGlobal(c *ui.Context) { a.globalSettingsPage(c) }
func (a *Controller) ViewOnboarding(c *ui.Context) {
	if a.host.Theme != nil && a.host.Theme.ReduceMotion() {
		c.DisableAnimations()
	}
	a.initialSetupShell(c)
}
func (a *Controller) ViewProject(c *ui.Context, projectID string) {
	a.access.OpenProject = projectID
	a.projectSettingsPage(c)
}
func (a *Controller) ViewCommonPermissions(c *ui.Context) { a.commonDatasetSettings(c) }
func (a *Controller) ViewDeferredBanner(c *ui.Context)    { a.setupDeferredGenerationBanner(c, true) }

func (a *Controller) authEpoch() uint64 {
	if a.host.Session == nil {
		return 0
	}
	return a.host.Session.AuthEpoch()
}

// ClearProjectQueues withdraws cached queue and unread data after the host advances its session fence.
func (a *Controller) ClearProjectQueues(dataset string) {
	for key, q := range a.access.Queues {
		if dataset != "" && !strings.HasSuffix(key, "/"+dataset) {
			continue
		}
		resetProjectQueue(q)
	}
}

func (a *Controller) invalidateReading(dataset string) {
	if a.host.InvalidateDataset != nil {
		a.host.InvalidateDataset(dataset)
		return
	}
	a.ClearProjectQueues(dataset)
	if a.host.Session != nil {
		a.host.Session.InvalidateReading()
	}
}

func (a *Controller) setReadingPolicyRevision(revision string) {
	if a.client.PolicyRevision == revision {
		return
	}
	a.client.PolicyRevision = revision
	if a.host.ReadingPolicyChanged != nil {
		a.host.ReadingPolicyChanged(revision)
	} else {
		a.invalidateReading("")
	}
}

func (a *Controller) refreshBaseIdentity() {
	if a.host.BaseReadingIdentity == nil {
		_, a.baseReadingIdentity, _ = readingIdentityPair(a.client)
	}
}

func (a *Controller) cachedBaseReadingIdentity() string {
	if a.host.BaseReadingIdentity != nil {
		return a.host.BaseReadingIdentity()
	}
	return a.baseReadingIdentity
}

func (a *Controller) currentProject() (Project, bool) {
	if a.host.CurrentProject != nil {
		return a.host.CurrentProject()
	}
	return Project{}, false
}

func (a *Controller) handleKnowledgeCatalogVersionChanged(projectID, datasetID, version string) {
	if a.host.KnowledgeCatalogChanged != nil {
		a.host.KnowledgeCatalogChanged(projectID, datasetID, version)
	}
}

func (a *Controller) knowledgeWriterHasPending(projectID, datasetID string) bool {
	return a.host.UnreadWritePending != nil && a.host.UnreadWritePending(projectID, datasetID)
}

func (a *Controller) reportCodeStatus(status, problem string) {
	if a.host.ProjectCodeStatus != nil {
		a.host.ProjectCodeStatus(a.codeBusy, status, problem)
	}
}

func (a *Controller) openProjectCreation(id string) {
	if a.host.EditProject != nil {
		a.host.EditProject(id)
	}
}

func (a *Controller) openSettingsFromDeferredBanner() {
	a.SetSection("记忆提炼")
	a.access.OpenProject = ""
	if a.host.Router != nil {
		a.host.Router.NavigateSettings("记忆提炼")
	}
}

func (a *Controller) AppearancePreference() string {
	if a.host.Appearance != nil {
		return a.host.Appearance()
	}
	return "system"
}

func (a *Controller) setAppearancePreference(mode string) {
	if a.host.SetAppearance != nil {
		a.host.SetAppearance(mode)
	}
}

func (a *Controller) KeepAliveOnQuit() bool {
	return a.host.KeepAliveOnQuit == nil || a.host.KeepAliveOnQuit()
}

func (a *Controller) SetKeepAliveOnQuit(keep bool) {
	if a.host.SetKeepAliveOnQuit != nil {
		a.host.SetKeepAliveOnQuit(keep)
	}
}

func (a *Controller) OpenAtLogin() bool {
	return a.host.OpenAtLogin != nil && a.host.OpenAtLogin()
}

func (a *Controller) ToggleOpenAtLogin(open bool) {
	if a.host.ToggleOpenAtLogin != nil {
		a.host.ToggleOpenAtLogin(open)
		return
	}
	if a.host.SetOpenAtLogin != nil {
		a.loginError = ""
		if err := a.host.SetOpenAtLogin(open); err != nil {
			a.loginError = "无法更新登录启动设置：" + err.Error()
		}
	}
}

func (a *Controller) health() { a.refreshServiceMonitor() }

func (a *Controller) serviceConnected() bool {
	if a.host.ServiceConnected != nil {
		return a.host.ServiceConnected()
	}
	return a.monitor.EngineStatus == "已连接"
}

// RefreshOnFocus checks first-run state and pending Agent receipts on activation.
// The window owner handles focus debouncing and credential checks.
func (a *Controller) RefreshOnFocus(inSettings bool) {
	if a.environment.RuntimeStarted || (!a.initialSetup && inSettings) {
		a.RefreshIntegrations()
	}
	if a.initialSetup {
		a.refreshLocalHosts()
		a.inspectInstallWorkerAsync()
		a.InspectOwnerRuntimeStatusAsync()
		a.refreshEnvironment()
	} else if a.shouldInspectDailyRuntimeOwner() {
		a.InspectOwnerRuntimeStatusAsync()
	}
}

func (a *Controller) ViewRuntimeAlerts(c *ui.Context) {
	if a.environment.staleRuntimeOwner() || a.canRetryDailyRuntimeStartup() {
		ui.Column(c).FillWidth().Shrink(0).Margin(0, 0, sp3, 0).Children(func() {
			a.staleDaemonBanner(c)
			a.dailyRuntimeStartupError(c)
		})
	}
}

func (a *Controller) ViewDeferredToast(c *ui.Context, inSettings bool) {
	if a.showSetupDeferredToast(inSettings) {
		a.setupDeferredToast(c)
	}
}

func (a *Controller) ViewFirstGeneration(c *ui.Context) { a.generationSetupPage(c) }
func (a *Controller) OnboardingShortcuts(c *ui.Context) {
	a.onboardingShortcuts(c)
}
func (a *Controller) ViewDialogs(c *ui.Context) { a.toolGraphDialog(c) }

// RefreshServiceMonitor reads the current model, control plane and service statuses.
func (a *Controller) RefreshServiceMonitor() { a.refreshServiceMonitor() }

// EnableServicePolling lets the window release health polling after startup.
func (a *Controller) EnableServicePolling(enabled bool) { a.monitor.PollEnabled = enabled }

// RefreshVisibleStatus advances time-based reads from the window's refresh cadence.
func (a *Controller) RefreshVisibleStatus(now time.Time) {
	if a.windowIsHidden() || a.windowIsInactive() || a.initialSetup || a.UseFirstGenerationView() {
		return
	}
	if !now.Before(a.access.Next) {
		a.RefreshProjectDirectory()
	}
	a.refreshCurrentProjectQueueIfDue(now)
	a.RefreshSidebarQueueIfDue(now)
	a.RefreshServiceMonitorIfDue(now)
}

func (a *Controller) refreshCurrentProjectQueueIfDue(now time.Time) {
	if a.windowIsInactive() || a.windowIsHidden() || a.coldStartOverviewPending() || !a.canDispatch() || !a.access.Loaded || a.access.Busy {
		return
	}
	p, ok := a.activeAccessProject()
	if !ok {
		return
	}
	q := a.ProjectQueue(p)
	if !q.Loading && !a.access.Busy && (!q.Loaded || !now.Before(q.Next)) {
		a.LoadProjectQueue(q, p)
	}
}

func (a *Controller) activeAccessProject() (AccessProject, bool) {
	targetID := a.access.OpenProject
	targetDataset := ""
	if targetID == "" {
		if p, ok := a.currentProject(); ok {
			targetID, targetDataset = p.ID, p.Dataset
		}
	}
	if targetID == "" && targetDataset == "" {
		return AccessProject{}, false
	}
	for _, p := range a.access.Directory.Projects {
		if targetID != "" && p.ID == targetID {
			return p, true
		}
	}
	if targetDataset != "" {
		for _, p := range a.access.Directory.Projects {
			if p.Dataset == targetDataset {
				return p, true
			}
		}
	}
	return AccessProject{}, false
}

func (a *Controller) RefreshTrayStatus(now time.Time) {
	a.RefreshSidebarQueueForTray(now)
	if !now.Before(a.monitor.NextPollAt) {
		a.PollServiceMonitor(false)
	}
}

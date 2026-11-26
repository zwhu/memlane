package app

import (
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/engine"
)

func (a *App) settingsHost() settings.Host {
	shell := appShellAdapter{a}
	return settings.Host{
		Dispatcher: shell, Router: shell, Session: shell, Theme: shell,
		DispatchReady:   func() bool { return a.Update != nil },
		BootstrapAssets: engine.BootstrapAssets, RuntimeAssets: memlane.RuntimeAssets, BrandMark: sidebarBrandMark,
		WindowHidden:    func() bool { return a.windowHidden },
		WindowInactive:  func() bool { return a.windowInactive },
		OverviewPending: a.coldStartOverviewPending,
		ConfigChanged:   func(config Config) { a.Config, a.Client.Config = config, config },
		RuntimeConfigLoaded: func(Config) {
			a.configError = ""
			a.overviewFirstUse = overviewFirstUseState{}
			a.loadProjectVisibility()
		},
		RuntimeReady: a.startRuntime,
		ProjectsChanged: func(previous, _ Config) {
			a.reconcileProjectReading(previous.Projects, previous.Common)
		},
		CurrentProject: a.current, EditProject: a.openProjectCreation,
		OpenProjectManagement: func(id string) {
			for index, project := range a.Config.Projects {
				if project.ID == id {
					a.openProjectSettings(index)
					return
				}
			}
		},
		BaseReadingIdentity:     a.cachedBaseReadingIdentity,
		ReadingPolicyChanged:    a.SetReadingPolicyRevision,
		InvalidateDataset:       a.InvalidateReading,
		KnowledgeCatalogChanged: a.handleKnowledgeCatalogVersionChanged,
		UnreadWritePending:      a.knowledgeWriterHasPending,
		ReloadGraphAssets:       a.reloadGraphAssetsAfterOwnerSwitch,
		RefreshGraph:            a.loadGraph,
		IndexProjectCode:        a.indexProjectCode,
		ProjectCodeBusy:         func() bool { return a.graphController.CodeBusy },
		ProjectCodeStatus: func(busy bool, status, problem string) {
			if busy || a.settingsCodeBusy {
				a.settingsCodeBusy, a.graphController.CodeBusy = busy, busy
			}
			a.graphController.Error = problem
			if status != "" {
				a.graphController.CodeStatus = status
			}
		},
		ChooseProjectFolder: func() (string, error) {
			if a.ChooseProjectFolder != nil {
				return a.ChooseProjectFolder()
			}
			return "", nil
		},
		ChooseDiagnosticPath: func() (string, error) {
			if a.ChooseDiagnosticPath != nil {
				return a.ChooseDiagnosticPath()
			}
			return "", nil
		},
		OpenExistingAgent: func(path string) error {
			if a.OpenExistingAgent != nil {
				return a.OpenExistingAgent(path)
			}
			return errors.New("无法打开 Agent")
		},
		OpenExternalURL: func(url string) error {
			if a.OpenExternalURL != nil {
				return a.OpenExternalURL(url)
			}
			return errors.New("无法打开链接")
		},
		Appearance: a.AppearancePreference, SetAppearance: a.setAppearancePreference,
		KeepAliveOnQuit: a.KeepAliveOnQuit, SetKeepAliveOnQuit: a.SetKeepAliveOnQuit,
		RefreshOpenAtLogin: a.RefreshOpenAtLoginAsync,
		OpenAtLogin:        a.OpenAtLogin, ToggleOpenAtLogin: a.ToggleOpenAtLogin, OpenAtLoginError: func() string { return a.desktopPrefs.LoginError },
		RenderVersionUpdate: a.versionUpdateSettings,
	}
}

func (a *App) Start() {
	a.syncSettingsTransport()
	a.settingsController.Start()
}

func (a *App) syncSettingsTransport() {
	a.settingsController.UseTransport(a.Config, a.Client.HTTP)
}

func (a *App) RefreshProjectDirectory() {
	a.syncSettingsTransport()
	a.settingsController.RefreshProjectDirectory()
}

func (a *App) RefreshIntegrations() {
	a.syncSettingsTransport()
	a.settingsController.RefreshIntegrations()
}

func (a *App) applyAccessDirectory(directory AccessDirectory) {
	a.syncSettingsTransport()
	a.settingsController.ApplyAccessDirectory(directory)
}

func (a *App) health() {
	a.syncSettingsTransport()
	a.settingsController.RefreshServiceMonitor()
}

func (a *App) refreshServiceMonitorIfDue(now time.Time) {
	a.syncSettingsTransport()
	a.settingsController.RefreshServiceMonitorIfDue(now)
}

func (a *App) RefreshVisibleStatus(now time.Time) {
	if a.windowHidden || a.windowInactive || a.settingsController.InitialSetup() || a.Config.GenerationSetupOnly {
		return
	}
	if a.isExternalManagedStoreStopped() {
		return
	}
	a.syncSettingsTransport()
	a.settingsController.RefreshVisibleStatus(now)
	a.refreshOverviewIfDue(now)
}

func (a *App) refreshSidebarQueueIfDue(now time.Time) {
	a.syncSettingsTransport()
	a.settingsController.RefreshSidebarQueueIfDue(now)
}

func (a *App) refreshSidebarQueueForTray(now time.Time) {
	a.syncSettingsTransport()
	a.settingsController.RefreshSidebarQueueForTray(now)
}

func (a *App) pollServiceMonitor(allowLogs bool) {
	a.syncSettingsTransport()
	a.settingsController.PollServiceMonitor(allowLogs)
}

func (a *App) projectQueueStateForAsync(p AccessProject) *ProjectQueueState {
	return a.settingsController.ProjectQueue(p)
}

func (a *App) projectQueueStateFor(p AccessProject) *ProjectQueueState {
	return a.settingsController.ProjectQueue(p)
}

func (a *App) existingProjectQueueState(p AccessProject) *ProjectQueueState {
	return a.settingsController.ExistingProjectQueue(p)
}

func (a *App) isSetupOnlyRuntime() bool {
	a.syncSettingsTransport()
	return a.settingsController.SetupOnlyRuntime()
}

func (a *App) serviceCogneeStatus() (string, string, ui.Color) {
	return a.settingsController.ServiceCogneeStatus()
}

type ProjectQueueState = settings.ProjectQueueState
type UnreadState = reading.UnreadScopeStateOf[Item]

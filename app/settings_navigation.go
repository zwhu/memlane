package app

// Open the project's inline Management tab while preserving caller location.
func (a *App) openProjectSettings(index int) {
	if index < 0 || index >= len(a.Config.Projects) {
		return
	}
	id, origin := a.Config.Projects[index].ID, a.readingLocation()
	a.navigateKind("知识", index, "管理")
	a.access.OpenProject, a.settingsOrigin = id, origin
	a.access.RevealQueueProject = ""
}

// Open the project's Management tab and scroll directly to its active status/anomaly notice.
func (a *App) openProjectQueueStatus(index int) {
	if index < 0 || index >= len(a.Config.Projects) {
		return
	}
	a.syncSettingsTransport()
	a.settingsController.OpenProjectQueue(a.Config.Projects[index].ID)
}
func (a *App) openSettingsFromDeferredBanner() {
	appShellAdapter{a}.NavigateSettings("记忆提炼")
}

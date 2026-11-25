package app

import (
	"time"

	"github.com/zwhu/memlane/app/settings"
)

type v2MessageSummaryResponse = settings.ProjectMessageSummary
type v2UnreadPreviewItem = settings.UnreadPreviewItem
type BacklogJob = settings.BacklogJob

type ServiceMonitorSnapshot = settings.ServiceMonitorSnapshot
type MonitoredProcess = settings.MonitoredProcess
type DiskMonitorState = settings.DiskMonitorState
type MonitorBusinessSummary = settings.MonitorBusinessSummary
type MonitorRecallQueryStats = settings.MonitorRecallQueryStats
type MonitorHookError = settings.MonitorHookError

func (a *App) loadProjectQueue(q *ProjectQueueState, p AccessProject) {
	a.syncSettingsTransport()
	a.settingsController.LoadProjectQueue(q, p)
}

func (a *App) refreshServiceMonitor() {
	a.health()
}

func settingsFixture() *App {
	a := projectUIFixture()
	a.Config.ProjectControl = &ProjectControlConfig{}
	a.Config.ServiceURL = "http://127.0.0.1:60000"
	a.Config.Model = "当前配置的 Codex 模型"
	a.monitor.CompletionStatus = "端点已连接（登录状态以实际请求为准）"
	access := "read"
	p := AccessProject{ID: "p", Name: "memlane", Aliases: []string{"/synthetic/memlane"}, Dataset: "ds", Status: "verified", DesiredRevision: 4, Overrides: map[string]ProjectOverride{"agent": {Access: &access}}, Applied: map[string]AccessApplied{"agent": {Status: "verified", Revision: 4}}}
	a.applyAccessDirectory(AccessDirectory{CodexConnection: &CodexConnection{Status: "configured_pending_new_session", NewSessions: 2, Home: "/synthetic/codex"}, Revision: 4, Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: false}, Agents: []AccessAgent{{ID: "agent", Name: "本机 Codex", Installation: &AgentInstallation{Status: "configured"}}}, Projects: []AccessProject{p, {ID: "same", Name: "另一个项目", Dataset: "other"}}, Bindings: []ProjectBinding{{AgentID: "agent", ProjectID: "p", HostSessionKey: "synthetic-session-0001"}, {AgentID: "agent", ProjectID: "same", HostSessionKey: "do-not-show"}}})
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	a.access.Queues = map[string]*ProjectQueueState{}
	for _, p := range a.access.Directory.Projects {
		a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset] = &ProjectQueueState{Loaded: true, Next: time.Now().Add(time.Hour)}
	}
	return a
}

func syntheticAccessDirectory() AccessDirectory {
	return AccessDirectory{Revision: 1, Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true}, Agents: []AccessAgent{{ID: "codex", Name: "Codex"}}, Projects: []AccessProject{{ID: "p", Name: "Project", Dataset: "dataset", Aliases: []string{"/synthetic/project"}, Status: "verified", DesiredRevision: 1, Applied: map[string]AccessApplied{"codex": {Status: "verified", Revision: 1}}}}}
}

func projectQueueFixture() (*App, *ProjectQueueState) {
	a := settingsFixture()
	a.page = "设置"
	a.access.OpenProject = "p"
	a.settingsOrigin = &readingLocation{Page: "知识", ProjectID: "p"}
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.BacklogLoaded = true
	q.Loaded, q.InboxPending, q.InboxFailed = true, 2, 1
	return a, q
}

package app

import (
	"net/http"
	"strings"

	"github.com/zwhu/memlane/app/settings"
)

type ProjectPolicy = settings.ProjectPolicy
type ProjectOverride = settings.ProjectOverride
type AccessApplied = settings.AccessApplied
type AccessProject = settings.AccessProject
type AccessAgent = settings.AccessAgent
type AgentInstallation = settings.AgentInstallation
type ProjectBinding = settings.ProjectBinding
type AccessDirectory = settings.AccessDirectory
type CodexConnection = settings.CodexConnection
type CodexConnectionPreview = settings.CodexConnectionPreview

type ProjectControlClient struct {
	Config ProjectControlConfig
	HTTP   *http.Client
}

func (a *App) accessClient() (ProjectControlClient, bool) {
	if a.Config.ProjectControl == nil || strings.TrimSpace(a.Config.ProjectControl.URL) == "" {
		return ProjectControlClient{}, false
	}
	return ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}, true
}

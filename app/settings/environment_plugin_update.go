package settings

import (
	"context"

	"github.com/zwhu/memlane/internal/bootstrap"
)

func (a *Controller) managedPluginUpdateDestination(plan bootstrap.Plan) string {
	if a.initialSetup || a.config.ProjectControl == nil || a.config.ProjectControl.PluginDir == "" || a.config.ConfigFile == "" {
		return ""
	}
	for _, item := range plan.Items {
		if item.Requirement.ID == "agent-plugin" && item.Observation.Status != bootstrap.Available &&
			(a.serviceConnected() || isPluginPolicyUpdate(item, item.Observation.Status)) {
			return item.Requirement.Destination
		}
	}
	return ""
}

func isPluginPolicyUpdate(item bootstrap.Item, status bootstrap.Status) bool {
	return item.Requirement.ID == "agent-plugin" && status == bootstrap.Incompatible && bootstrap.IsPluginPolicyUpdate(item.Observation)
}

func isDailyPluginUpdateAvailable(a *Controller, item bootstrap.Item, status bootstrap.Status) bool {
	return a != nil && !a.initialSetup && a.serviceConnected() &&
		item.Requirement.ID == "agent-plugin" &&
		status == bootstrap.Incompatible &&
		item.Observation.Status == bootstrap.Incompatible &&
		a.config.ProjectControl != nil &&
		a.config.ProjectControl.PluginDir != ""
}

func isOnlyPluginUpdate(a *Controller, plan bootstrap.Plan, j bootstrap.Journal, busy bool) bool {
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

func environmentIncompatibleHint(a *Controller, row bootstrap.Item) string {
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

func (a *Controller) hasPendingPluginConfigApply() bool {
	s := &a.environment
	return !a.initialSetup && s.PendingPluginApplyDir != "" &&
		a.config.ProjectControl != nil && a.config.ProjectControl.PluginDir != s.PendingPluginApplyDir
}

func (a *Controller) applyPendingPluginConfig() {
	s := &a.environment
	if s.Busy || s.Loading || s.Environment == nil || !a.hasPendingPluginConfigApply() || a.config.ConfigFile == "" {
		return
	}
	environment := s.Environment
	candidate := s.PendingPluginApplyDir
	configFile := a.config.ConfigFile
	officialData := a.config.OfficialData
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
		a.dispatch(func() {
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
			if a.config.ProjectControl != nil {
				a.config.ProjectControl.PluginDir = candidate
			}
			s.Plan = detected
			s.Message = "Agent 插件已更新并写入配置，重启日常服务后后台将加载新版插件。"
		})
	}()
}

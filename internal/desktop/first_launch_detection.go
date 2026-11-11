package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/platform"
)

func environmentDetectedReady(items []EnvironmentItem, controller *RuntimeController) bool {
	states := map[string]string{}
	for _, item := range items {
		states[item.Name] = item.State
	}
	required := []string{controller.Config.WorkerID, controller.Config.ModelID, "helper / LaunchAgent"}
	if controller.Config.CodeExtractorID != "" {
		required = append(required, controller.Config.CodeExtractorID)
	}
	if controller.Official != nil {
		required = append(required, "cognee-service", "cognee-bge-zh", "cognee-codex-plugin")
	}
	for _, id := range required {
		if !preparedEnvironmentItem(states[id]) {
			return false
		}
	}
	return states["后台服务"] == string(platform.Enabled) && states["记忆服务"] == "ready" && (controller.Official == nil || states["Cognee 数据服务"] == "ready")
}

// Returning to a saved step still waits for current observations in the first
// screen. A completed setup retains its existing automatic local preparation,
// but that preparation now stays behind the same startup screen until settled.
func (a *App) checkResumedFirstLaunch() {
	s := &a.firstLaunch
	if s.Controller == nil || s.Context == nil || s.Dispatch == nil {
		return
	}
	if !s.Startup {
		s.ReturnStep, s.ReturnOpen = s.State.Step, !s.State.Completed && !s.State.Skipped
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Sequence++
	sequence := s.Sequence
	s.Startup, s.StartupPreparing, s.Open = true, false, false
	s.beginDetection()
	s.State.Message = "正在检查本机环境…"
	ctx, cancel := context.WithTimeout(s.Context, 40*time.Second)
	s.Cancel = cancel
	controller, dispatch := s.Controller, s.Dispatch
	go func() {
		defer cancel()
		items, err := controller.DiscoverEnvironmentWithProgress(ctx, func(message string) {
			dispatch(func() {
				if s.Sequence == sequence && s.Startup {
					s.State.Message = message
				}
			})
		})
		home, _ := os.UserHomeDir()
		selected := map[string]string{}
		if raw, e := os.ReadFile(filepath.Join(controller.Config.Root, "agent-paths.json")); e == nil && len(raw) < 65536 {
			_ = json.Unmarshal(raw, &selected)
		}
		dispatch(func() {
			if s.Sequence == sequence && s.Startup {
				s.State.Message = "正在检查本机 Agent…"
			}
		})
		discovered := agents.Discover(ctx, home, selected)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if s.Context.Err() != nil {
			return
		}
		dispatch(func() {
			if sequence != s.Sequence || !s.Startup {
				return
			}
			s.Cancel = nil
			s.State.Items, s.State.Agents = items, discovered
			if err != nil {
				s.DetectionFailed = true
				s.State.Message = "本机检查未完成：" + err.Error()
				return
			}
			a.adoptDetectedEnvironment(items)
			if environmentDetectedReady(items, controller) {
				a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true, Message: "本机记忆环境已就绪", Service: platform.Enabled})
				a.finishStartupDetection()
				return
			}
			stopped := false
			for _, item := range items {
				if item.Name == "记忆服务" && item.State == "stopped" {
					stopped = true
				}
			}
			if stopped {
				a.SetRuntimeStatus(RuntimeStatus{Phase: "stopped", Installed: true, Message: "记忆服务已主动停止"})
				if s.State.Completed || s.State.Skipped {
					a.finishStartupDetection()
				} else {
					s.DetectionFailed = true
					s.State.Message = "记忆服务已主动停止。请先明确启动服务，再继续配置。"
				}
				return
			}
			// First-run preparation still requires its environment-page confirmation.
			// Only an already completed environment may resume automatically here.
			if (s.State.Completed || (s.State.Stage == "ready" && s.ReturnStep != "environment")) && a.prepareRuntime != nil {
				s.StartupPreparing = true
				s.State.Message = "正在准备并核验本机记忆服务…"
				a.prepareRuntime()
				return
			}
			s.State.Step, s.State.Stage = "environment", "results"
			s.Detecting, s.Startup, s.Open = false, false, s.ReturnOpen
			s.State.Message = "本机检测已完成，请核对实际结果。"
			if a.onNavigate != nil {
				a.onNavigate()
			}
		})
	}()
}

func (a *App) finishStartupDetection() {
	s := &a.firstLaunch
	s.Detecting, s.DetectionFailed, s.Startup, s.StartupPreparing = false, false, false, false
	s.Open = s.ReturnOpen
	if a.runtimeStatus.Phase == "ready" {
		s.State.Step, s.State.Stage = s.ReturnStep, "ready"
		a.serviceState = "ready"
	}
	s.State.Message = a.runtimeStatus.Message
	a.loadExperiencePreview()
	a.loadMaterialPreview()
	if a.onNavigate != nil {
		a.onNavigate()
	}
}

func (a *App) firstLaunchStartupRuntime(status RuntimeStatus) {
	s := &a.firstLaunch
	if !s.Startup || !s.StartupPreparing {
		return
	}
	s.State.Message = status.Message
	switch status.Phase {
	case "ready":
		a.finishStartupDetection()
	case "failed", "authorization", "cancelled", "stopped":
		s.StartupPreparing = false
		s.DetectionFailed = true
	}
}

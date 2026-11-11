package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/platform"
	"github.com/zwhu/memlane/internal/serviceipc"
)

var ErrRuntimeChanging = errors.New("runtime lifecycle is changing")

type RuntimeAvailability struct {
	LibraryRoot                                            string
	RecoveryOwner                                          string
	RecoveryGeneration                                     int64
	Recovery                                               string
	RecoveryAttempts, RecoveryLimit, RecoveryWindowSeconds int
	Phase, Message                                         string
	Service                                                platform.ServiceState
	State                                                  string
	ScheduleError                                          string
}

// ObserveAvailability only reads the stop flag, system registration and health.
// It never registers a helper, downloads components, clears stop or retries jobs.
func (r *RuntimeController) ObserveAvailability(ctx context.Context) (RuntimeAvailability, error) {
	if !r.lifecycle.TryLock() {
		return RuntimeAvailability{}, ErrRuntimeChanging
	}
	defer r.lifecycle.Unlock()
	return r.observeAvailability(ctx)
}

func (r *RuntimeController) observeAvailability(ctx context.Context) (RuntimeAvailability, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeAvailability{}, err
	}
	root := filepath.Join(r.Config.Root, "data")
	stopped, err := serviceipc.IsStopped(root)
	if err != nil {
		return RuntimeAvailability{}, err
	}
	if stopped {
		return RuntimeAvailability{LibraryRoot: root, Phase: "stopped", Message: "记忆服务已主动停止"}, nil
	}
	var state platform.ServiceState
	if !r.Config.ReuseRunningService {
		state, err = platform.ServiceStatus(r.Config.ServicePlist)
		if err != nil {
			return RuntimeAvailability{}, err
		}
	}
	result := RuntimeAvailability{LibraryRoot: root, Service: state}
	switch state {
	case platform.RequiresApproval:
		result.Phase, result.Message = "authorization", "后台许可已撤销或需要批准，请在系统设置的登录项与扩展中允许 Memlane 后重试。"
		return result, nil
	case platform.NotRegistered:
		result.Phase, result.Message = "unavailable", "后台服务尚未注册；已保存的原文保留，请重试。"
		return result, nil
	case platform.NotFound:
		result.Phase, result.Message = "unavailable", "后台服务组件缺失，请重新安装客户端。"
		return result, nil
	}
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var health struct {
		State                 string `json:"state"`
		RecoveryOwner         string `json:"recovery_owner"`
		RecoveryGeneration    int64  `json:"recovery_generation"`
		Recovery              string `json:"recovery"`
		RecoveryAttempts      int    `json:"recovery_attempts"`
		RecoveryLimit         int    `json:"recovery_limit"`
		RecoveryWindowSeconds int    `json:"recovery_window_seconds"`
		Version               string `json:"service_version"`
		ScheduleError         string `json:"schedule_error"`
	}
	if err = serviceipc.Call(probe, root, serviceipc.Request{Op: "health"}, &health); err != nil {
		result.Phase, result.Message = "unavailable", "后台服务暂不可用；已保存的原文保留，请重试。"
	} else if health.Version != "0.0.1" {
		result.Phase, result.Message = "unavailable", "后台服务版本不兼容，请重新安装客户端。"
	} else if health.State != "ready" {
		result.Phase, result.Message = "unavailable", "记忆引擎暂不可用；已保存的原文保留，请重试。"
	} else {
		result.Phase, result.Message = "ready", "本地记忆服务已就绪"
	}
	if err == nil && health.Version == "0.0.1" {
		result.State, result.ScheduleError = health.State, health.ScheduleError
		result.RecoveryOwner, result.RecoveryGeneration = health.RecoveryOwner, health.RecoveryGeneration
		result.Recovery, result.RecoveryAttempts, result.RecoveryLimit, result.RecoveryWindowSeconds = health.Recovery, health.RecoveryAttempts, health.RecoveryLimit, health.RecoveryWindowSeconds
	}
	return result, nil
}

// Called only on the window's serialized update boundary. Preparation and
// explicit stop/resume own their state; late observations cannot replace them.
func (a *App) ApplyRuntimeAvailability(observation RuntimeAvailability) {
	if observation.LibraryRoot != "" && observation.LibraryRoot != a.library.Root {
		return
	}
	if !a.runtimeStatus.Installed || a.backups.RestoreLocked || a.backups.Busy || a.firstLaunch.Detecting {
		return
	}
	switch a.runtimeStatus.Phase {
	case "checking_availability", "ready", "authorization", "unavailable":
	default:
		return
	}
	if n := &a.maintenanceNext; n.Awaiting && n.WaitingOwner != "" && (observation.RecoveryOwner == "" || observation.RecoveryOwner == n.WaitingOwner && observation.RecoveryGeneration < n.WaitingGeneration) {
		return
	}
	a.maintenanceNext.Recovery = observation.Recovery
	a.maintenanceNext.Attempts, a.maintenanceNext.Limit, a.maintenanceNext.WindowSeconds = observation.RecoveryAttempts, observation.RecoveryLimit, observation.RecoveryWindowSeconds
	a.maintenanceNext.Awaiting = false
	a.runtimeStatus.Phase = observation.Phase
	a.runtimeStatus.Message = observation.Message
	a.runtimeStatus.Service = observation.Service
	a.serviceState = observation.Phase
	if observation.State != "" {
		a.serviceState = observation.State
		a.schedules.RuntimeError = observation.ScheduleError
	}
	a.ReconcileFirstLaunchAvailability()
}

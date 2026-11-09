package official

import (
	"context"
	"path/filepath"
	"time"
)

// ObserveEnvironment checks the already running, inspected local component.
// It neither starts a service, logs in, verifies an LLM nor prepares components.
func (c *Controller) ObserveEnvironment(ctx context.Context, executable string) Status {
	if executable == "" {
		return Status{Phase: "not_verified", Message: "Cognee 组件尚未校验，准备后可检查服务"}
	}
	if !c.gate.TryLock() {
		return Status{Phase: "not_verified", Message: "Cognee 服务正在准备，本次检查尚未完成"}
	}
	defer c.gate.Unlock()
	state, err := c.ReadState()
	if err != nil {
		return Status{Phase: "unavailable", Message: "Cognee 数据服务尚未运行"}
	}
	if state.Phase == "stopped" {
		return Status{Phase: "stopped", Message: "Cognee 数据服务已停止"}
	}
	if !running(state, executable, filepath.Join(c.Root, "cognee-config.json")) {
		return Status{Phase: "unavailable", Message: "Cognee 数据服务尚未运行"}
	}
	return observeEnvironmentHealth(ctx, state.Endpoint)
}

func observeEnvironmentHealth(ctx context.Context, endpoint string) Status {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var health struct{ Status, Health, Version string }
	if err := Request(probe, endpoint, "/health", "GET", "", "", nil, &health); err != nil {
		return Status{Phase: "not_verified", Message: "Cognee 数据服务健康检查未完成：" + err.Error()}
	}
	if health.Status != "ready" || health.Health != "healthy" || health.Version != "1.6.7" {
		return Status{Phase: "unavailable", Message: "Cognee 数据服务健康或版本检查未通过"}
	}
	return Status{Phase: "ready", Message: "Cognee 图、向量与关系库健康检查通过"}
}

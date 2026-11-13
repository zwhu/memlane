package simpledesktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

type pendingBufferView struct {
	Status       PendingMaterials
	Loaded, Busy bool
	Message      string
}

func (a *App) pendingMaterialsEditor(c *ui.Context, p AccessProject, agent AccessAgent) {
	if agent.Installation == nil {
		ui.Button(c, "准备此 Agent 的受管插件配置").Disabled(a.access.Busy).OnClick(func() {
			a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
				var result AccessDirectory
				err := client.request(ctx, "POST", "/v1/agents/"+agent.ID+"/installation", map[string]any{"expected_revision": revision}, &result)
				return result, err
			})
		})
		ui.Text(c, "准备配置不改动宿主正在运行的会话；实际加载后才会登记新项目。").FontSize(10).TextColor(muted)
		return
	}
	if a.access.Buffers == nil {
		a.access.Buffers = map[string]*pendingBufferView{}
	}
	count := 0
	for _, row := range a.access.Directory.Bindings {
		binding := row
		if binding.ProjectID != p.ID || binding.AgentID != agent.ID {
			continue
		}
		count++
		key := agent.ID + "/" + binding.HostSessionKey
		view := a.access.Buffers[key]
		if view == nil {
			view = &pendingBufferView{}
			a.access.Buffers[key] = view
		}
		ui.Column(c.Key("pending-"+key)).Gap(6).Padding(8, 0).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, fmt.Sprintf("会话 %d · %s", count, shortSessionLabel(binding.HostSessionKey))).FontSize(11).Bold()
				ui.Button(c, "检查会话保存结果").Disabled(view.Busy || p.Dataset == "").OnClick(func() { a.runPendingAction(view, p, agent, binding, "show") })
			})
			if view.Loaded {
				ui.Text(c, fmt.Sprintf("尚未保存 %d 条；保存结果不明确 %d 条", view.Status.Pending, view.Status.Unknown)).FontSize(10).TextColor(muted)
				if view.Status.Blocked != nil {
					ui.Text(c, "已阻塞，不会自动重试；恢复权限也不会自动补交。").FontSize(10).TextColor(muted)
				}
				if view.Status.Pending > 0 {
					ui.Row(c).Gap(8).Children(func() {
						ui.Button(c, "重新保存这些记录").Disabled(view.Busy).OnClick(func() { a.runPendingAction(view, p, agent, binding, "replay") })
						ui.Button(c, "丢弃未保存记录").Disabled(view.Busy).OnClick(func() { a.runPendingAction(view, p, agent, binding, "discard") })
					})
				}
			}
			if view.Message != "" {
				ui.Text(c, view.Message).FontSize(11).TextColor(muted)
			}
		})
	}
	if count == 0 {
		ui.Text(c, "配置已准备，尚无已登记的新会话；已有会话不会静默迁移。").FontSize(10).TextColor(muted)
	}
}

func (a *App) runPendingAction(view *pendingBufferView, p AccessProject, agent AccessAgent, binding ProjectBinding, action string) {
	if view.Busy || agent.Installation == nil {
		return
	}
	if action != "show" && !view.Loaded {
		return
	}
	view.Busy = true
	view.Message = "正在核对原会话材料…"
	python, installation, signature := a.Config.Python, *agent.Installation, view.Status.Signature
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		result, err := runPendingMaterials(ctx, python, installation, binding, p.Dataset, action, signature)
		a.Update(func() {
			view.Busy = false
			if err != nil {
				view.Message = err.Error()
				return
			}
			if action == "show" {
				view.Status = result
				view.Loaded = true
				view.Message = "仅核对原待发材料，不读取禁用期间的新正文。"
			} else {
				view.Loaded = false
				if action == "discard" {
					view.Message = fmt.Sprintf("已丢弃 %d 条原待发材料", result.Discarded)
				} else {
					view.Message = fmt.Sprintf("本次补交 %d 条，仍有 %d 条待发；处理任务状态以官方记录为准。", result.Drained, result.Pending)
				}
			}
		})
	}()
}

func shortSessionLabel(key string) string {
	r := []rune(key)
	if len(r) > 22 {
		return string(r[:10]) + "…" + string(r[len(r)-8:])
	}
	return key
}

package simpledesktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

type pendingBufferView struct {
	Status           PendingMaterials
	Loaded, Busy     bool
	Message          string
	ReadError        string
	ReadCode         string
	Details          []pendingMessageDetail
	DetailsLoaded    bool
	DetailsError     string
	Observed         bool
	Added            int
	Action           string
	CheckDetailsOpen bool
	DiagnosticsOpen  bool
	ExpandedMessages map[string]bool
	AllMessagesOpen  bool
}

func (v *pendingBufferView) observe(status PendingMaterials) {
	if v.Observed && status.Pending > v.Status.Pending {
		v.Added += status.Pending - v.Status.Pending
	}
	if status.Signature == v.Status.Signature && v.Status.Checked {
		status.Checked, status.Matched, status.SessionID = v.Status.Checked, v.Status.Matched, v.Status.SessionID
	}
	if status.Signature != v.Status.Signature {
		v.Details = nil
		v.DetailsLoaded = false
		v.ExpandedMessages = nil
		v.AllMessagesOpen = false
	}
	v.Status = status
	v.ReadError = ""
	v.ReadCode = ""
	v.Loaded, v.Observed = true, true
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
				textButton(c, "检查会话保存结果", nil).Disabled(view.Busy || p.Dataset == "").OnClick(func() { a.runPendingAction(view, p, agent, binding, "show") })
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
	view.Action = action
	if action == "details" {
		view.DetailsError = ""
	} else {
		view.Message = "正在核对原会话材料…"
	}
	python, installation, signature := a.Config.Python, *agent.Installation, view.Status.Signature
	operator := pendingOperatorConfig(a.Config)
	identity, _ := readingIdentity(a.Client)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		result, err := runPendingMaterials(ctx, python, installation, binding, p.Dataset, action, signature, operator)
		a.Update(func() {
			view.Busy = false
			current, _ := readingIdentity(a.Client)
			if identity != current {
				view.Details = nil
				view.DetailsLoaded = false
				view.Loaded = false
				view.Message = "项目权限已变化，请重新读取保存状态"
				return
			}
			if action == "details" {
				if view.Status.Signature != signature && view.Status.Signature != result.Signature {
					view.Details = nil
					view.DetailsLoaded = false
					view.DetailsError = "消息列表已更新，请重新查看。"
					return
				}
				if identity != current || err != nil {
					view.Details = nil
					view.DetailsLoaded = false
					view.DetailsError = "消息摘要暂时无法读取，原消息未更改。"
					return
				}
				view.observe(result)
				view.Details = result.Details
				view.DetailsLoaded = true
				view.DetailsError = ""
				return
			}
			if err != nil {
				view.Message = err.Error()
				return
			}
			if action == "show" || action == "check" {
				view.observe(result)
				if action == "check" {
					view.Message = pendingCheckMessage(installation, result)
				} else {
					view.Message = "保存状态已更新。"
				}
			} else {
				view.observe(result)
				view.Status.Signature = ""
				view.Status.Checked = false
				view.Added = 0
				if action == "discard" {
					view.Message = fmt.Sprintf("已丢弃 %d 条原待发材料", result.Discarded)
				} else {
					view.Message = fmt.Sprintf("本次已保存／核对消除 %d 条，剩余 %d 条待确认保存。", result.Drained, result.Pending)
					if result.Pending > 0 {
						view.Message += " 单批已结束，可继续保存下一批。"
					}
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

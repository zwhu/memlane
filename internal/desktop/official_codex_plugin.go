package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) officialCodexPluginSettings(c *ui.Context, connection records.Connection) {
	s := &a.agents
	i, installed := s.Installs[connection.ID]
	ui.Text(c, "Cognee 官方插件").Bold()
	ui.Text(c, "安装后仍需在 Codex 中确认信任并重载；服务配置与新对话保存分别验证。").FontSize(design.Small).TextColor(design.Muted)
	if r, ok := s.OfficialPluginReceipts[connection.ID]; ok && s.OfficialPluginGenerations[connection.ID] == connection.Generation {
		label := "插件已安装，等待信任、重载与采集验证"
		if r.State == "installed_disabled" {
			label = "插件已安装但已禁用，请在 Codex 中管理启用状态"
		}
		if r.State == "not_installed" {
			label = "尚未安装官方插件"
		}
		if h, found := s.OfficialPluginHooks[connection.ID]; found {
			switch h.State {
			case "review_required":
				label = "插件已安装，等待 Codex 确认信任并重载"
			case "hooks_ready":
				label = "插件 hooks 已核对，服务与自动采集仍待验证"
			case "hooks_disabled":
				label = "插件 hooks 已禁用，请在 Codex 中核对"
			default:
				label = "插件 hooks 尚未完整核对，请重载 Codex 后重试"
			}
		}
		ui.Text(c, label).Key("official-plugin-state").FontSize(design.Small)
		ui.Text(c, "以上为上次核对结果；配置或信任变化后请重新核对。").FontSize(design.Small).TextColor(design.Muted)
	}
	if installed && i.CaptureConsent != nil && i.CaptureConsent.Enabled {
		ui.Text(c, "旧自动记录仍开启，先停止并核对待发送记录，再切换官方插件。").FontSize(design.Small)
		quietButton(c, "停止旧自动记录").Key("official-plugin-stop-legacy").Disabled(s.Busy || s.Context == nil).OnClick(func() { a.changeCodexCapture(connection, false) })
		quietButton(c, "查看待发送原文").OnClick(a.openAgentDelivery)
	}
	quietButton(c, "安装 Cognee 官方插件").Key("official-plugin-install").Disabled(s.Busy || s.Loading || s.Context == nil || !connection.Enabled || a.officialController() == nil || !installed || !filepath.IsAbs(i.Options.AgentPath) || !filepath.IsAbs(i.Options.CodexDirectory) || i.Connection.Generation != connection.Generation).OnClick(func() { a.installOfficialCodexPlugin(connection) })
	quietButton(c, "核对官方插件").Key("official-plugin-inspect").Disabled(s.Busy || s.Loading || s.Context == nil || !connection.Enabled || a.officialController() == nil || !installed || !filepath.IsAbs(i.Options.AgentPath) || !filepath.IsAbs(i.Options.CodexDirectory) || i.Connection.Generation != connection.Generation).OnClick(func() { a.manageOfficialCodexPlugin(connection, false) })
}

// Inspect only this client's own adapter inventory, including disconnected
// entries. An unreadable inventory must not permit a second capture path.
func officialPluginLegacyInventory(root string) ([]agents.Installation, error) {
	entries, err := os.ReadDir(filepath.Join(root, "agent-adapters"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || len(entries) > 4096 {
		return nil, errors.New("legacy_inventory_unverified")
	}
	var result []agents.Installation
	for _, entry := range entries {
		if !entry.IsDir() || !manualUUID(entry.Name()) {
			return nil, errors.New("legacy_inventory_unverified")
		}
		info, e := os.Lstat(filepath.Join(root, "agent-adapters", entry.Name(), "installation.json"))
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return nil, errors.New("legacy_inventory_unverified")
		}
		i, e := agents.LoadInstallation(root, entry.Name())
		if e != nil || i.Connection.ID != entry.Name() {
			return nil, errors.New("legacy_inventory_unverified")
		}
		result = append(result, i)
	}
	return result, nil
}

func (a *App) installOfficialCodexPlugin(connection records.Connection) {
	a.manageOfficialCodexPlugin(connection, true)
}

func (a *App) manageOfficialCodexPlugin(connection records.Connection, install bool) {
	s := &a.agents
	controller := a.officialController()
	i, ok := s.Installs[connection.ID]
	if s.Busy || s.Loading || s.Context == nil || s.Dispatch == nil || controller == nil || !ok || !connection.Enabled || connection.Agent != "codex" || i.Connection.Generation != connection.Generation || !filepath.IsAbs(i.Options.CodexDirectory) || !filepath.IsAbs(i.Options.AgentPath) {
		return
	}
	s.Sequence++
	sequence, epoch, root, home, life, dispatch := s.Sequence, a.libraryEpoch, s.Root, s.Home, s.Context, s.Dispatch
	client := a.library
	s.Busy = true
	s.Notice = "正在核对旧采集配置并安装官方插件…"
	if !install {
		s.Notice = "正在核对官方插件文件与 Codex hooks 信任状态…"
	}
	delete(s.OfficialPluginReceipts, connection.ID)
	delete(s.OfficialPluginHooks, connection.ID)
	go func() {
		ctx, cancel := context.WithTimeout(life, time.Minute)
		defer cancel()
		var receipt agents.OfficialCodexPluginReceipt
		var hooks *agents.CodexPluginHookInspection
		legacy, err := officialPluginLegacyInventory(root)
		if err == nil {
			matched := false
			for _, current := range legacy {
				if current.Connection.ID == connection.ID && current.Connection.Generation == connection.Generation && current.Options.AgentPath == i.Options.AgentPath && current.Options.CodexDirectory == i.Options.CodexDirectory {
					matched = true
				}
			}
			if !matched {
				err = errors.New("installation_changed")
			}
		}
		if err == nil {
			var connections []records.Connection
			connections, err = client.Connections(ctx)
			if err == nil {
				matched := false
				for _, current := range connections {
					if current.ID == connection.ID && current.Agent == "codex" && current.Enabled && current.Generation == connection.Generation {
						matched = true
					}
				}
				if !matched {
					err = errors.New("connection_changed")
				}
			}
		}
		if err == nil {
			var manager *components.Manager
			var feed components.Feed
			manager, feed, err = controller.Manager(ctx)
			if err == nil {
				var descriptor components.Descriptor
				for _, d := range feed.Components {
					if d.ID == "cognee-codex-plugin" {
						descriptor = d
						break
					}
				}
				opts := agents.OfficialCodexPluginOptions{Executable: i.Options.AgentPath, Home: home, CodexDirectory: i.Options.CodexDirectory, Manager: manager, Component: descriptor, Legacy: legacy, Confirmed: install}
				if install {
					receipt, err = agents.InstallOfficialCodexPlugin(ctx, opts)
				} else {
					var result agents.OfficialCodexPluginInspection
					result, err = agents.VerifyOfficialCodexPlugin(ctx, opts)
					receipt, hooks = result.Installation, result.Hooks
				}
			}
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryEpoch != epoch || s.Root != root || s.Sequence != sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.Notice = "官方插件安装未确认，请核对组件与现有配置后重试。"
				if !install {
					s.Notice = "官方插件状态尚未核实，请检查组件与 Codex 配置后重试。不会据旧结果显示已就绪。"
				}
				if receipt.State == "migration_required" {
					s.Notice = "旧自动记录仍开启。请先停止旧采集并核对待发送记录，再安装官方插件；原有材料保留。"
				}
				return
			}
			if s.OfficialPluginReceipts == nil {
				s.OfficialPluginReceipts = map[string]agents.OfficialCodexPluginReceipt{}
			}
			s.OfficialPluginReceipts[connection.ID] = receipt
			if s.OfficialPluginGenerations == nil {
				s.OfficialPluginGenerations = map[string]int64{}
			}
			s.OfficialPluginGenerations[connection.ID] = connection.Generation
			if hooks != nil {
				if s.OfficialPluginHooks == nil {
					s.OfficialPluginHooks = map[string]agents.CodexPluginHookInspection{}
				}
				s.OfficialPluginHooks[connection.ID] = *hooks
			}
			s.Notice = "官方插件安装已核对，尚未验证自动采集。请在 Codex 中确认信任并重载。"
			if !install {
				s.Notice = "官方插件状态已重新读取；服务配置与真实自动采集仍需分别验证。"
			}
		})
	}()
}

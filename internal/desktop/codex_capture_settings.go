package desktop

import (
	"context"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func codexCaptureStateLabel(i agents.Installation, status *agents.CodexCaptureStatus, readFailure string) (string, string) {
	if i.CaptureConsent == nil || !i.CaptureConsent.Enabled {
		return "未开启自动记录", "工具连接和对话采集分别授权。"
	}
	if i.CaptureHooks == nil {
		return "采集配置待安装", "授权已保存；尚未安装采集配置。"
	}
	if readFailure != "" {
		return "采集状态待核对", readFailure
	}
	if status == nil || status.Consent != i.CaptureConsent.ID {
		return "等待 Codex 确认与重载", "请按 Codex 的提示确认新增 hook 并重新打开 CLI；尚未收到新对话，不表示采集已可用。"
	}
	if status.Receipt.Code != "" || status.Receipt.State == "retained_error" {
		if !status.Receipt.LocalRetained {
			return "采集异常", "尚未取得本机持久化回执，请检查采集状态。"
		}
		return "采集异常", "本次事件已在本机暂存，原文采集尚未完成；请查看待发送记录。"
	}
	if status.Receipt.State == "retained_pending_assignment" {
		return "已收到对话 · 待归属", "原始记录已在本机保留，尚未取得项目保存回执。"
	}
	if status.Receipt.State == "delivered" && len(status.Receipt.Entries) > 0 {
		confirmed := true
		for _, entry := range status.Receipt.Entries {
			confirmed = confirmed && entry.PersistedConfirmed && entry.SavedRevision > 0 && manualUUID(entry.Memory)
		}
		if confirmed {
			return "最近原文已保存", "最近采集事件的原文已取得保存回执；索引与其他待发送材料分别核对。"
		}
	}
	return "已收到采集事件", "尚未取得本次原文的完整持久化回执。"
}

func sameCaptureHome(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	// Existing config directories normally resolve. An unavailable directory
	// remains unavailable rather than falling back to a different home.
	x, e := filepath.EvalSymlinks(a)
	if e != nil {
		return false
	}
	y, e := filepath.EvalSymlinks(b)
	return e == nil && x == y
}

func (a *App) codexCaptureSettings(c *ui.Context, connection records.Connection) {
	s := &a.agents
	i, ok := s.Installs[connection.ID]
	if !ok || !i.Options.ComputerLevel {
		return
	}
	status := s.CaptureStatus[connection.ID]
	label, detail := codexCaptureStateLabel(i, status, s.CaptureErrors[connection.ID])
	if !connection.Enabled {
		label, detail = "连接已断开", "连接恢复并获准采集前，不记录新对话。"
	}
	ui.Column(c).Key("codex-daily-capture").Gap(8).Padding(12).Border(1, design.Border).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "正常对话自动记录").Bold().Grow(1)
			ui.Text(c, label).Key("capture-state").FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Text(c, "允许记录此电脑 Codex CLI 授权后新增的对话，作为原始记录保存在本机。未登记仓库的材料先保留待归属；不导入历史或自动整理知识。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6)
		ui.Text(c, detail).Key("capture-state-detail").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6)
		daily := sameCaptureHome(i.Options.CodexDirectory, a.machineConnectionDirectory("codex"))
		if !daily {
			ui.Text(c, "当前连接使用另一配置目录；请先连接日常使用的 Codex 配置，不能把隔离目录当成日常采集。").FontSize(design.Small).TextColor(design.Muted)
		}
		if status != nil && i.CaptureConsent != nil && status.Consent == i.CaptureConsent.ID {
			meta(c, "最近收到事件", status.ObservedAt)
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			if i.CaptureConsent == nil || !i.CaptureConsent.Enabled || i.CaptureHooks == nil {
				_, verified := agents.ConnectionVerification(i, connection.Generation)
				button := "授权并开启自动记录"
				if i.CaptureConsent != nil && i.CaptureConsent.Enabled {
					button = "安装采集配置"
				}
				quietButton(c, button).Key("capture-authorize").Disabled(!daily || !verified || !connection.Enabled || s.Busy || s.Loading || s.Context == nil || !filepath.IsAbs(s.Bridge)).OnClick(func() { a.changeCodexCapture(connection, true) })
			} else {
				quietButton(c, "停止自动记录").Key("capture-revoke").Disabled(s.Busy || s.Context == nil).OnClick(func() { a.changeCodexCapture(connection, false) })
			}
			quietButton(c, "查看待发送原文").Key("capture-open-retained").OnClick(a.openAgentDelivery)
		})
	})
}

func (a *App) changeCodexCapture(connection records.Connection, enabled bool) {
	s := &a.agents
	if s.Busy || s.Loading || s.Context == nil || s.Dispatch == nil || connection.Agent != "codex" {
		return
	}
	s.Sequence++
	sequence, epoch, root, life, dispatch, bridge := s.Sequence, a.libraryEpoch, s.Root, s.Context, s.Dispatch, s.Bridge
	s.Busy = true
	s.Notice = "正在保存采集授权…"
	go func() {
		ctx, cancel := context.WithTimeout(life, 10*time.Second)
		defer cancel()
		i, err := agents.SetCodexCaptureConsent(root, connection.ID, connection.Generation, enabled, true)
		if err == nil && enabled {
			i, err = agents.InstallCodexCaptureHooks(ctx, root, connection.ID, true, bridge)
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryEpoch != epoch || s.Root != root || s.Sequence != sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.Notice = "采集设置尚未完成，请重新读取连接并检查配置；不会显示采集已可用。"
			} else {
				if s.Installs == nil {
					s.Installs = map[string]agents.Installation{}
				}
				s.Installs[connection.ID] = i
				s.Notice = "采集配置已安装，等待 Codex 确认并重载后接收新对话。"
				if !enabled {
					s.Notice = "已停止自动记录；此前保留的原始记录不删除。"
				}
			}
			a.refreshAgents(false)
		})
	}()
}

package simpledesktop

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func codexConnectionLabel(connection CodexConnection) string {
	switch connection.Status {
	case "prepared":
		return "接入配置已准备，尚未启用"
	case "configured_pending_new_session":
		if connection.NewSessions > 0 {
			return "配置已启用；已收到新会话接入回执"
		}
		return "配置已启用，等待新会话加载"
	case "staging_unknown", "activation_unknown":
		return "原操作结果未确认，请核对原操作"
	case "agent_creation_unconfirmed":
		return "原 Agent 身份创建待核对，不会重复创建"
	case "staging_not_complete":
		return "已核对：安装准备未完成，可明确重新准备"
	case "requires_review":
		return "连接配置需核对，保留已有设置"
	default:
		return "尚未接入"
	}
}

func (a *App) codexConnectionSettings(c *ui.Context) {
	connection := a.access.Directory.CodexConnection
	if connection == nil {
		ui.Text(c, "正在读取Codex 接入状态…").FontSize(12).TextColor(muted)
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		agentLogo(c, agentBrandIcon("codex"), 16)
		ui.Text(c, "Codex · "+codexConnectionLabel(*connection)).FontSize(12).Bold()
	})
	ui.Text(c, "CLI 与桌面端共用此连接；此前未保存的会话记录保留。").FontSize(11).LineHeight(1.7).TextColor(muted)
	if connection.Home != "" {
		ui.Text(c, "配置目录："+connection.Home).FontSize(10).TextColor(muted)
	}
	if connection.Status == "configured_pending_new_session" && connection.NewSessions > 0 {
		return
	}
	unknown := connection.Status == "staging_unknown" || connection.Status == "activation_unknown" || connection.Status == "requires_review" || connection.Status == "agent_creation_unconfirmed"
	ui.Row(c).Gap(12).Children(func() {
		ui.Button(c, "准备Codex 接入").Disabled(a.access.Busy || unknown).OnClick(func() { a.changeCodexConnection("prepare", "") })
		textButton(c, "核对原接入操作", nil).Disabled(a.access.Busy).OnClick(func() { a.changeCodexConnection("review", "") })
	})
	preview := connection.Preview
	if preview == nil {
		return
	}
	ui.Text(c, fmt.Sprintf("启用将替换 %d 个旧记忆连接入口，使用独立 Agent 身份；其他插件、模型和登录配置保留。", len(preview.DisabledPlugins))).FontSize(11).LineHeight(1.7)
	if len(connection.LegacyConsumers) > 0 {
		ui.Text(c, "仍有旧记忆采集进程运行，暂不能启用。请先交接旧连接；不会自动结束编码会话或删除待发材料。").FontSize(11).TextColor(muted).LineHeight(1.7)
	}
	if len(connection.ProjectConfigConflicts) > 0 {
		ui.Text(c, "已有项目单独开启旧记忆连接，请核对这些项目设置后再启用。").FontSize(11).TextColor(muted)
	}
	ui.Button(c, "启用Codex 连接").Disabled(a.access.Busy || connection.Status != "prepared" || len(connection.LegacyConsumers) > 0 || len(connection.ProjectConfigConflicts) > 0).OnClick(func() {
		a.changeCodexConnection("activate", preview.ProposedSHA)
	})
}

func (a *App) changeCodexConnection(action, reviewedSHA string) {
	a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
		var result AccessDirectory
		payload := map[string]any{"expected_revision": revision}
		if action == "activate" {
			payload["proposed_sha256"] = reviewedSHA
		}
		err := client.request(ctx, "POST", "/v1/codex/"+action, payload, &result)
		return result, err
	})
}

// Display-only cleanup; registered Agent names and identities remain intact.
func agentDisplayName(name string) string {
	for _, prefix := range []string{"本机 ", "本地 "} {
		name = strings.TrimPrefix(name, prefix)
	}
	if name == "TraeX" {
		return "TRAE CLI"
	}
	return name
}

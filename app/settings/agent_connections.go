package settings

import (
	"context"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

var loadAgentVersionsFn = loadAgentVersions

func integrationOperation() string { return controlplane.NewUIOperationID() }

func connectionName(kind string) string {
	if kind == "antigravity" {
		return "Gemini App"
	}
	switch kind {
	case "codex":
		return "Codex"
	case "pi":
		return "pi"
	case "traex":
		return "TRAE CLI"
	case "trae_ide":
		return "TRAE CN"
	case "claude_code":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "opencode":
		return "OpenCode"
	}
	return kind
}

func connectionStatus(row AgentConnection) string {
	if needsTraeHooksRepair(row) {
		return "Hooks 需修复"
	}
	if row.Capture != nil && row.Capture.State == "awaiting_generation_activation" {
		return "等待启用提炼后验证保存"
	}
	if captureConnection(row) {
		if row.Capture == nil {
			return "等待采集验证"
		}
		switch row.Capture.State {
		case "needs_permission":
			return "需要允许自动采集"
		case "deferred":
			return "自动采集未开启"
		case "waiting_session":
			return "等待新会话验证"
		case "waiting_message":
			return "等待消息采集"
		case "healthy":
			return "自动采集正常"
		case "check_failed":
			return "采集状态读取失败"
		case "project_disabled":
			return "项目未开启自动采集"
		case "project_write_denied":
			return "Agent 无项目写入权限"
		case "project_pending":
			return "项目设置待确认"
		default:
			return "正在检查自动采集…"
		}
	}
	switch row.Status {
	case "verification_environment":
		return "需要更换配置目录"
	case "environment_ambiguous":
		return "请选择配置目录"
	case "environment_missing", "environment_unavailable":
		return "配置目录不可用"
	case "environment_receipt_unconfirmed":
		return "配置目录待核对"
	case "component_unavailable":
		if !row.Detected {
			return "未安装"
		}
		return "暂时无法连接"
	case "integration_pending":
		if row.Detected {
			return "暂不支持连接"
		}
		return "未安装 · 暂不支持连接"
	case "staging_not_complete":
		return "上次准备未完成"
	case "prepared":
		return "等待确认"
	case "configured_pending_new_session":
		if row.NewSessions > 0 {
			return "已连接"
		}
		if row.ReceiptOverdue {
			return "尚未确认连接"
		}
		return "等待新会话"
	case "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown":
		if row.Status == "requires_review" && row.ErrorCode == "host_executable_changed" {
			return "应用已更新，待核对"
		}
		return "连接结果待确认"
	case "agent_creation_unconfirmed":
		return "连接结果待确认"
	}
	if !row.Detected {
		return "未安装"
	}
	return "未连接"
}

func connectionNeedsReview(row AgentConnection) bool {
	switch row.Status {
	case "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown", "agent_creation_unconfirmed":
		return true
	}
	return false
}

func needsTraeHooksRepair(row AgentConnection) bool {
	return row.NeedsHookRepair || row.ErrorCode == "trae_ide_hook_changed_requires_review" || row.ErrorCode == "trae_ide_unsupported_hook_events"
}

func connectionReviewNotice(name string, row AgentConnection) string {
	switch row.ErrorCode {
	case "trae_ide_hook_changed_requires_review", "trae_ide_unsupported_hook_events":
		return formatControlConflictMessage(row.ErrorCode, "")
	}
	if !connectionNeedsReview(row) {
		return ""
	}
	switch row.ErrorCode {
	case "host_executable_changed":
		return "检测到 " + name + " 应用已更新，点击右侧「核对连接」确认后即可恢复正常。"
	case "antigravity_plugin_not_enabled":
		return name + " 中 Memlane 插件尚未启用，请点击右侧「核对连接」检查。"
	case "connection_artifact_changed", "connection_binding_changed", "antigravity_installation_binding_changed":
		return "插件文件或绑定配置已变动，请点击右侧「核对连接」检查。"
	case "antigravity_installation_requires_review", "traex_installation_requires_review", "claude_installation_requires_review", "cursor_loader_changed_requires_review":
		return name + " 的插件安装状态与记录不一致，请点击右侧「核对连接」检查。"
	case "":
		if row.Status != "requires_review" {
			return "上一次连接操作结果尚未确认，请点击右侧「核对连接」。"
		}
		return ""
	default:
		return "连接状态需要核对（" + row.ErrorCode + "），请点击右侧「核对连接」。"
	}
}

func captureConnection(row AgentConnection) bool {
	return (row.Kind == "codex" || row.Kind == "pi" || row.Kind == "traex" || row.Kind == "trae_ide" || row.Kind == "antigravity") &&
		(row.Status == "configured_pending_new_session" || row.Status == "environment_receipt_unconfirmed")
}

func connectionActionAllowed(row AgentConnection, action string) bool {
	if action == "allow_capture" || action == "defer_capture" {
		return row.Kind == "codex" && captureConnection(row) && row.Capture != nil && row.Capture.ReviewSHA != "" &&
			(row.Capture.State == "needs_permission" || row.Capture.State == "deferred")
	}
	if action == "review" {
		return row.ID != ""
	}
	if !row.Detected {
		return false
	}
	if action == "prepare" {
		return row.Status == "" || row.Status == "not_connected" || row.Status == "staging_not_complete" || (row.Kind == "opencode" && row.Status == "prepared")
	}
	return action == "activate" && row.Status == "prepared" && row.Preview != nil && row.Preview.ProposedSHA != ""
}

func (a *Controller) RefreshConnections() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Loading || s.Busy || !a.canDispatch() {
		return
	}
	pending, err := a.loadGenerationOperations()
	if err != nil {
		s.Error = "无法读取待核对操作，已停止发送新配置请求"
		return
	}
	s.PendingOperations = pending
	s.Loading = true
	s.Request++
	s.NextReceiptCheck = time.Now().Add(10 * time.Second)
	request := s.Request
	config := a.config
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var connections ConnectionsDirectory
		var err error
		if !config.GenerationSetupOnly {
			err = client.request(ctx, "GET", "/v1/connections?force=1", nil, &connections)
		}
		if err == nil {
			loadAgentVersionsFn(ctx, config, connections.Connections)
		}
		a.dispatch(func() {
			if request != s.Request {
				return
			}
			s.Loading = false
			if err != nil {
				s.ConnectionsError = ControlUserError(err)
				s.Error = s.ConnectionsError
				a.captureReadFailed()
				return
			}
			s.ConnectionsError, s.Error = "", ""
			s.Loaded, s.ConnectionsLoaded = true, true
			a.acceptConnectionsDirectory(connections)
			if s.HostVersions == nil {
				s.HostVersions = map[string]string{}
			}
			for _, row := range connections.Connections {
				s.HostVersions[row.Kind] = row.HostVersion
			}
			if s.Selected == nil {
				s.Selected = map[string]bool{}
			}
			if s.ItemMessages == nil {
				s.ItemMessages = map[string]string{}
			}
			a.preloadPinnedConnectionPreviews()
		})
	}()
}

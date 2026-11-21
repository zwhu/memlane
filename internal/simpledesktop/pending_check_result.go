package simpledesktop

import "fmt"

// OpenCode acknowledges exact original markers during check and returns the
// remaining queue. The Python reader only reports matches without consuming
// uncertain entries. Keep those two receipt contracts distinct in the UI.
func pendingCheckMessage(installation AgentInstallation, result PendingMaterials) string {
	remaining := result.Unknown
	if installation.HostKind != "opencode" {
		remaining -= result.Matched
	}
	if result.Matched < 0 || remaining < 0 {
		return "核对计数尚未确认，请重新读取采集状态；未重新发送。"
	}
	if installation.HostKind == "opencode" {
		message := fmt.Sprintf("已核对：%d 条记录已确认采集，%d 条采集结果仍未知。未重新发送。", result.Matched, remaining)
		if remaining > 0 {
			message += " 未匹配不代表采集失败，原记录继续保留。"
		}
		return message
	}
	message := fmt.Sprintf("已核对：%d 条内容在服务端最近记录中匹配，%d 条仍无法确认。未重新发送。", result.Matched, remaining)
	if remaining > 0 {
		message += " 官方仅返回最近 20 条；未找到不代表采集失败，原记录继续保留。"
	}
	return message
}

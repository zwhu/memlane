package simpledesktop

import "strings"

func agentTakeoverFreshInstallPrompt(selectedAgents []string) string {
	prompt := `帮我完成本机 Memlane 的首次安装与配置（无需安装 Skill）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 按 next_actions 顺序推进：所有 requires=explicit_confirmation 的 --apply 操作，先给我看 --plan / --dry-run 预览并等我确认；若返回退出码 20，按 retry_after_ms 用 doctor --operation-id <id> --json 轮询进度直到完成。
3. 严禁读取或输出任何 token/密钥/.local/聊天内容，严禁手动修改 hooks.json/Cognee 源码。若返回退出码 11 或同一问题重试 3 次仍失败，立刻停止并把 human_steps 交给我。`
	var names []string
	for _, kind := range selectedAgents {
		if name := takeoverAgentName(kind); name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		prompt += "\n（我计划优先接入的 Agent：" + strings.Join(names, "、") + "）"
	}
	return prompt
}

func agentTakeoverBeta2RescuePrompt() string {
	return `帮我安全恢复升级后卡住的 Memlane（旧版后台 / 409 待核对 / TRAE CN hooks 报错）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 严格按 blockers 先运行对应 repair 的 --dry-run --json 给我确认（保留原记忆库、已下载组件和未知回执，不手工改文件、不读凭据）；确认后执行 --apply，并用 verify --stage setup --expect-zero-downloads --expect-runtime-current --json 复验。
3. 若返回退出码 11 或 40（需人工核对）或重试 3 次失败，立刻停止交给我处理。`
}

func agentTakeoverConnectionTroubleshootPrompt(kind string) string {
	if takeoverAgentName(kind) == "" {
		kind = "<kind>"
	}
	return `帮我排查 Memlane 与本机 Coding Agent 的连接或自动采集问题：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --include-logs --json
2. 仅通过 next_actions 中的 connect / repair 命令先预览、经我确认再 --apply，不手工编辑宿主配置文件或读取凭据。
3. 完成后运行 verify --stage connection --agent ` + kind + ` --json 核验，若返回退出码 11 请把重启宿主/开启全局 Hooks/新会话验证步骤交给我。`
}

func agentTakeoverInstallCLIPrompt(hostName string) string {
	hostName = strings.TrimSpace(hostName)
	if hostName == "" {
		hostName = "当前 Agent"
	}
	return "我正在使用 " + hostName + "。可以帮我检查本机是否已有 Codex CLI 或 TRAE CLI 吗？Memlane 需要一个可用的 CLI，把会话整理成记忆。如果需要安装，请先提供官方安装来源和预计改动，等我确认后再执行。请勿读取或输出密钥。安装后告诉我如何在 Memlane 中重新检测并完成提炼设置。"
}

func takeoverAgentName(kind string) string {
	switch kind {
	case "trae_ide":
		return "TRAE CN"
	case "codex", "pi", "antigravity", "traex", "claude_code", "cursor", "opencode":
		return connectionName(kind)
	default:
		return ""
	}
}

# 记忆处理 CLI 文案调整（2026-11-19）

用户要求：“你在文案上给我体现吧，需要绑定 cli 才行”。本轮只明确当前四种 Agent 处理桥的 CLI 前提，不新增接入或模型任务。

## 文案

- 来源分段项由“使用 Agent”改为“使用 Agent CLI”。
- Agent 来源下显示“需先在「Agent 与模型」中绑定对应 CLI，才能用于整理记忆。”
- 候选名称显示 Codex CLI、pi CLI、TraeX CLI、Gemini CLI；顺序保持。
- Gemini 的模型来源显示“agy CLI 默认配置”，对应 App 自带的 agy。
- 未接入候选显示“CLI 未绑定”，沿用现有连接状态及禁用条件。
- 编辑态标题显示“选择 Agent CLI”；当前使用与切换确认复用相同名称。

当前实现使用 CLI，不表示其他宿主永远只能经 CLI 接入。TRAE IDE 内部命令的源码证据及尚未运行验证的边界见[调研报告](../research-trae-ide-memory-processing-20261119.md)。

## 检查与边界

- `python3 scripts/task.py -- python3 scripts/lint.py` 通过。
- `sh scripts/go.sh test ./internal/simpledesktop` 通过；更新已有文案断言，保留来源浏览不启用、固定顺序及未连接禁用的行为用例。
- 已打开仓库要求的首页、详情设计图。实际 Dev 窗口可读取服务页与总览，但窗口在操作间重建，旧窗口 ID 失效；两次点击被 Computer Use 拒绝为“not prepared”，即使之前读取了状态。停止继续尝试；CLI 文案的实窗对照未完成。这是工具／观察问题，不能记为产品文案验收通过。
- 本轮未改运行配置、连接目标、模型或日常处理来源；未发起模型、安装、服务启停或 release。

## 用户截图来源核对

“服务 → 运行环境”及“复制诊断材料／导出诊断包／指定已有路径”来自 `50cb621`（2026-11-18 20:33:12），文件为 `internal/simpledesktop/environment_setup_ui.go`。该页不属于本轮 CLI 文案改动。

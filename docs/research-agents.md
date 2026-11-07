# Agent 接入与后台执行核查

核查日期：2026-11-07。结论是先实现 **Codex + pi**，共用 Memlane 的受控记忆入口；TRAE 与 traex 分别适配。Skill 负责引导使用，MCP / Extension 负责读写工具，CLI / RPC 负责后台整理，三者不能互相替代。

本轮只阅读官方资料、本地源码，并执行 CLI 的版本和帮助命令。没有调用模型、读取真实聊天或凭据、修改 Agent 配置，也没有完成记忆读写实测。以下接口事实与设计建议分开记录。

## 1. 本机与上游证据

| 对象 | 已核查事实 | 对实现的影响 |
| --- | --- | --- |
| Codex CLI | 本机 `codex --version` 为 `0.147.0`；`exec --help` 有 JSONL、JSON Schema、临时会话、指定工作目录、只读沙箱；`mcp add --help` 有 stdio / Streamable HTTP | 足以设计接入与任务执行适配器，仍需真实认证、工具调用及失败路径验证 |
| pi | 本机已安装包 `@earendil-works/pi-coding-agent` 为 `0.84.1`，`--help` 支持 RPC、Extension 与工具白名单；另有本地源码仓库指向 `earendil-works/pi`，提交 `651d5d6a53690b3ec87b72fca0b3f993ac5ec748`，其 package 版本为 `0.83.0` | 已安装包与源码 checkout 不是完全相同版本；旧版 Extension/RPC 需要针对实际安装包复验，不能把当前上游能力直接当成本机能力 |
| pi 当前上游 | 官方在 2026-09-29 宣布内置 MCP；当前 MCP 文档支持 stdio / Streamable HTTP、用户及项目配置 | 新版本优先 MCP，不再笼统描述为“pi 不支持 MCP” |
| TRAE | 官方文档支持 Skill、MCP、项目配置及安装链接；本轮没有操作实际 TRAE UI | 先做记忆工具接入，不把 IDE 本身当成已验证的后台执行器 |
| traex | 本机 `traex --version` 自报 `traecli 0.213.1`，安装渠道为 `internal`；帮助有 `exec`、`mcp`、ACP 和实验性 app-server | 当前设备可按 TRAE CLI 接口设计适配；不能据此承诺外部用户均能安装同一发行版 |

pi 新版依据：[官方公告](https://earendil.com/posts/you-said-no-mcp/)、[官方 MCP 文档](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/mcp.md)。旧版以本地实际读取的 [固定提交源码](https://github.com/earendil-works/pi/tree/651d5d6a53690b3ec87b72fca0b3f993ac5ec748/packages/coding-agent) 为准；网页工具未成功抓取该历史提交，不能将它标为线上复验通过。

## 2. Codex

**事实。** OpenAI 官方文档提供 `codex exec` 非交互执行，`--json` 输出事件流，`--output-schema` 约束最终结果，`--ephemeral` 避免落盘会话；默认复用已保存的 CLI 认证。事件包括完成与失败，不能以“进程启动成功”代替任务成功。[官方非交互文档](https://learn.chatgpt.com/docs/non-interactive-mode)

MCP 支持 stdio 与 Streamable HTTP，配置可位于用户或可信项目的 `config.toml`，有工具筛选及启动、调用超时。官方描述同一 Codex host 的本地客户端可以共享配置；具体桌面安装与 CLI 是否确属同一 host，仍须实机确认。[官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)

**建议。** 日常记忆工具先用 Memlane 自带的 stdio 桥接程序注册 MCP，避免依赖终端 PATH、外部 Node/Python 安装或固定端口。桥接程序只连本机后台服务，不各自启动一份 Cognee。配套轻量 Skill 说明何时检索、如何保存、如何声明当前项目。

后台整理使用独立 `codex exec` 任务，输入是本次选中的知识快照，输出是严格校验的整理提案。显式设置临时会话、只读沙箱、非交互审批策略和独立工作目录。不要使用绕过沙箱的开关，也不要复用用户正在进行的 chat。`--ignore-user-config` 不等于关闭所有插件、规则或工具，具体隔离效果须单独验收；在无法证明不能调用现有写工具前，不启用无人值守整理。

**边界。** 复用 CLI 登录不等于无限额度、免费调用或可抽取登录凭据供 Cognee 直连。Memlane 调用用户授权的 CLI；由 CLI 处理认证。云端推理仍会传输所选内容。暂不调用实验性 app-server 来替代已有 `exec`，以减少首版适配面。

## 3. pi

**事实。** 当前官方 MCP 文档有 `pi mcp add`，用户配置为 `~/.pi/agent/mcp.json`，项目配置为 `.pi/mcp.json`，项目配置受信任状态约束。修改配置后需要按版本要求重载；连接成功与工具实际可调用须分别验证。[官方 MCP 文档](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/mcp.md)

本机旧版已有 `pi.registerTool()` Extension 接口，执行上下文提供 `ctx.cwd`；CLI 支持显式加载 Extension、关闭自动发现，以及工具白名单。可用薄 Extension 将记忆工具转发给 Memlane，同样不直接打开 Cognee 数据文件。[固定提交 Extension 文档](https://github.com/earendil-works/pi/blob/651d5d6a53690b3ec87b72fca0b3f993ac5ec748/packages/coding-agent/docs/extensions.md)

RPC 是基于 stdin/stdout 的 JSONL，`prompt` 的 `success: true` 只表示接受请求，之后仍可能失败；应继续消费事件直至终态。支持请求 ID 和 `abort`。JSONL 必须按 LF 分帧，不能把合法 JSON 字符串内的 Unicode 分隔符当成新记录。[固定提交 RPC 文档](https://github.com/earendil-works/pi/blob/651d5d6a53690b3ec87b72fca0b3f993ac5ec748/packages/coding-agent/docs/rpc.md)

源码还提供结构化结果工具示例：以 schema 定义参数，工具返回结果后终止当前执行。这比从自然语言或 Markdown 中提取 JSON 更可靠，但服务端仍需校验。[结构化结果示例](https://github.com/earendil-works/pi/blob/651d5d6a53690b3ec87b72fca0b3f993ac5ec748/packages/coding-agent/examples/extensions/structured-output.ts)

**建议。** 初次探测根据版本和可用命令选择原生 MCP 或兼容 Extension，不擅自升级 pi。后台整理走 RPC，每次任务建立隔离会话；关闭自动加载项目上下文、Skill 和其他 Extension，只显式加载 Memlane 的结果提交工具并设置工具白名单。不要混淆 `--no-tools` 与 `--no-builtin-tools`：前者默认关闭全部工具，后者仍允许 Extension 工具。禁用内置工具不是完整 OS 沙箱，还需验证 Extension 不暴露任意文件和命令能力。

## 4. TRAE 与 traex

**TRAE 事实。** 官方 Skill 文档解释由说明与配套资源扩展工作流，它不是数据库连接协议。[官方 Skill 介绍](https://www.trae.ai/blog/trae_tutorial_0115)

本轮成功读取的是中国站官方文档：支持全局和项目 MCP；项目配置为 `.trae/mcp.json`，支持 `command/args/env` 与 HTTP 配置，并可用 `${workspaceFolder}` 注入当前目录。国际站对应页面本轮未成功获取，因此国际版路径和行为仍需验证。[添加 MCP Server](https://docs.trae.cn/ide_add-mcp-servers)

中国版还提供 `trae-cn://.../mcp-import` 安装链接，由 TRAE 展示导入确认。链接是配置导入入口，不能把链接打开等同于连接成功。[安装链接](https://docs.trae.cn/ide_mcp-server-install-links)

**建议。** 探测 App 版本及发行渠道后选择安装入口；优先使用官方配置导入机制。只增补 Memlane 对应条目，保留其他配置。内存操作仍在 Memlane 栏内，外部 Agent / 系统自己的授权窗口属于接入过程，不用自制弹框包裹。未核实配置位置时，提供可复制配置，不猜测后覆盖用户文件。

**traex 本地事实。** `traex exec --help` 支持 `--json`、`--output-schema`、`--ephemeral`、`--cd` 和沙箱选项；`traex mcp add --help` 支持 stdio / Streamable HTTP。`--ignore-user-config` 的帮助文本仍提到 `CODEX_HOME`，与顶层 TRAE 配置说明不一致，不能把这个参数的隔离语义照搬 Codex。以上均来自本机 `0.213.1` 的只读帮助输出，无公开发行兼容性证明。

**建议。** 为 traex 建独立适配器，底层可共享 CLI JSONL 解析组件，但单独测参数、配置和取消行为。日常 MCP 读写可以先于后台整理上线。ACP 与实验性 app-server 暂不进入首条链路。

## 5. 共用接入契约（Memlane 设计建议）

Agent 适配器只实现发现、能力探测、安装/移除自己的配置、连接诊断和任务执行。记忆业务规则统一放在 Memlane 服务，避免四种 Agent 各有一套作用域、审批和历史规则。

建议最小工具包括 `memory_search`、`memory_get`、`memory_save`、`memory_propose_change`、`memory_job_status`。写入先产生可靠回执，索引未完成时明确返回状态。Agent 提交的内容按产品规则接收，区分原始记录与用户/Agent 明确保存的知识。会改动已有知识的 Agent 请求走提案；直接用户编辑由 App 的保存动作生效。不要直接将 Cognee 的覆盖、删除等底层操作作为无约束 MCP 工具公开。

每个请求携带 `protocol_version`、`request_id`、`connection_id`、项目上下文和来源引用。幂等键覆盖重试；同一键不同正文返回冲突，不能悄悄当作重复成功。结果返回 `record_id`、当前版本与 `accepted/indexing/ready/failed` 等状态。工具名称和字段属于建议，尚未实现。

作用域需要做到：

1. 服务按已注册连接与允许范围校验请求，不信任模型随意填入的 `agent_name` 或 `project_id`。
2. 项目由实际 workspace / `cwd` 规范化并映射到用户登记的仓库身份；Git worktree、符号链接和同名目录不能直接用名称合并。
3. MCP 服务进程的启动目录不保证就是当前 chat 的项目。优先用版本支持的项目配置、明确绑定的工作区参数或 Extension 上下文；无法确认时请求选择范围，不能默认检索所有项目。
4. 默认检索当前项目与已允许的全局范围；另一个项目需要显式选择。MCP roots、Skill 提示和数据标签都不能替代服务端校验。
5. 原始出处不要求复制整个聊天记录；保存 Agent 实际提交的片段及可用的来源标识。服务失联应返回明确错误或持久化队列回执，不伪装为空结果。

这层主要约束正常 Agent 的误操作及范围混淆，不声称能隔离同一 macOS 账户下具备任意执行能力的恶意程序。

## 6. 后台整理的统一任务边界（设计建议）

- 统一 `probe → start → events → cancel → result` 接口；将能力记录为 `memory_tools`、`structured_output`、`cancel`、`headless`、`workspace_context`，不因为安装了 Skill 就标为全部支持。
- 每个任务固定输入记录及其版本，输出 schema 包含新增摘要、标签和待确认修改。Agent 不能直接访问生产库写凭据，也不能调用正常连接中的删除、覆盖工具。
- 服务验证 schema、来源 ID、版本和作用域；旧版本上的提案不能覆盖用户后来编辑的内容。确认后才提交业务事务并更新 Cognee 索引。
- 队列默认单并发；超时、取消、退出非零、认证失败、配额不足、schema 不符分别记录。一次任务可有多次 attempt，最终应用动作必须幂等。
- pi 先发 RPC `abort`；CLI 任务先请求正常终止，再按宽限期结束任务进程组。取消后的晚到结果不再应用。具体时限以真实耗时测试确定，不从工具默认超时反推。
- 本地记录任务摘要、提案及错误分类，日志隐藏凭据；失败时不自动切换到另一个用户未选择的云端模型。
- 用户选择一个具备实际后台能力的 Agent 作为统一整理执行者。首轮建议先验证 Codex，pi 为并行可行的实现候选；TRAE IDE 不默认具有后台执行能力。

## 7. 首次接入验收

| 检查 | 通过条件 |
| --- | --- |
| 发现与状态 | 找到可执行文件 / App 仅显示“已发现”；完成配置仍区分待重载；真实工具往返后才显示“可用” |
| Codex → pi | 用合成数据写入项目 A，pi 检索得到原文、来源和版本；项目 B 默认查不到 A 的专属知识 |
| 旧版 pi | 0.84.1 通过 Extension 完成相同读写；新版 MCP 路径独立验证 |
| 作用域切换 | 同时打开两个项目和切换 worktree 后，不沿用另一个项目的绑定；不明范围时明确报错 |
| 受控整理 | 恶意/错误提案、旧版本提案和未确认覆盖不能修改有效知识；只允许规则内的新摘要、标签自动提交 |
| 取消与异常 | 超时和取消保留记录；不会出现孤儿 CLI、重复落库或把服务错误当成零命中 |
| 配置变更 | 添加、升级、断开只影响 Memlane 条目，能从备份恢复；不删除其他 MCP / Skill |
| traex 与 TRAE | 分别记录版本、渠道、真实 MCP 工具调用与重载要求；本地帮助存在不能代替验收 |

这些是下一阶段应执行的验收，不是本轮已完成测试。相关产品边界见 [产品定义](product.md)。

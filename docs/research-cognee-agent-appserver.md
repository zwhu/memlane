# Cognee 使用本地 Agent 模型：MCP Sampling 与 app-server

2026-11-09。回答产品讨论中的两个问题：能否让 Cognee 通过本地 Agent 的 app-server 使用其模型；Cognee MCP 是否已有这种能力。本记录保存研究阶段的官方源码、安装版本与协议核对，研究本身没有执行真实模型闭环或改变生产启动与认证逻辑。随后完成的 pi 模型桥实现、技术闭环与事实质量未通过项另见 [pi 验证记录](pi-model-bridge-validation.md)；Codex app-server 模型桥仍待实现与实测。

## 已确认的结论

Cognee 1.6.7 已有 `mcp-sampling` provider：在具备 Sampling 能力的 MCP 客户端连接中，Cognee 可以请求客户端执行模型推理，无须独立的模型 API Key。普通 MCP 工具调用与 Sampling 是不同方向的请求，Agent 能调用 Cognee 工具不等于已经允许 Cognee 请求模型。

研究开始时，Memlane 使用独立 Python Cognee HTTP 服务，显式要求模型和 API Key，并使用 `litellm_native` 框架完成环境探测与提炼；该服务没有 MCP 请求中的客户端会话。因此当时实现的 API Key 要求不代表 Cognee 只能直接调用模型供应商 API。选择 MCP Sampling 需要调整运行上下文、配置分派与可用性检查。后续 pi 实现已将无模型 Key 的本地服务准备与实际推理验证分开，实际范围以验证记录为准。

对于不能直接提供 Sampling 的 Agent，可以由 Memlane 管理适配层，将 Cognee 模型请求转为 app-server 的独立任务。此路径属于待实现与待实测的适配方案；当前 app-server 协议字段可满足部分必要条件，不能据此声明完整提炼链路已通过。

## 官方已有案例：Claude observer

2026-11-09 补充核对 `cognee-integrations` 固定提交 `997fd6f72253aa97b49996eb1b032793f0257935` 的 Claude Code README、`_observer.py` 和 `claude-observer.py`。这是官方仓库已经提供的桥接实现，本文只读核对源码，没有在本机安装或运行 Claude observer。

本地模式没有用户自选的 LLM API Key/provider 且 Claude CLI 可用时，插件可将 Cognee 的 `custom` provider 指向本机 OpenAI-compatible HTTP shim。Cognee 的 `/v1/chat/completions` 请求由 shim 转为 `claude -p --safe-mode --no-session-persistence --output-format json` 子进程调用；结构化 Schema 通过 `--json-schema` 传递，超过 argv 限制时源码退回提示词约束。Claude 使用自身登录，调用计入其模型用量。

`--safe-mode` 与 `COGNEE_OBSERVER_CHILD` 用于隔离自定义配置并防止再次进入采集 hooks。shim 管理并发、超时和 loopback 访问 token；传给 Cognee 的 `LLM_API_KEY` 是本地 shim 的访问凭据，无须用户提供云模型 Key。向量化继续由独立本地 embedding provider 完成。这一案例采用 HTTP → CLI 子进程桥接，不能冒称官方已有 Codex app-server 桥接。

来源：[官方案例说明](https://github.com/topoteretes/cognee-integrations/blob/997fd6f72253aa97b49996eb1b032793f0257935/integrations/claude-code/README.md#claude-observer-local-mode-on-your-claude-subscription)、[桥接服务实现](https://github.com/topoteretes/cognee-integrations/blob/997fd6f72253aa97b49996eb1b032793f0257935/integrations/claude-code/scripts/claude-observer.py)、[检测与 provider 配置](https://github.com/topoteretes/cognee-integrations/blob/997fd6f72253aa97b49996eb1b032793f0257935/integrations/claude-code/scripts/_observer.py)。

## Cognee MCP Sampling 的实际契约

核对版本为官方发行提交 `eaa33be5c58cd14584e64e3e0ed2e1479472ee09`（Cognee 1.6.7）。本机安装的以下两个文件已与该提交的原始文件逐字节比较，内容相同：

| 文件 | SHA256 |
| --- | --- |
| `mcp_sampling/adapter.py` | `9255f1aed614776e22bf656fca06c9483bc58b78f9840b8dc92def1bb3861577` |
| `mcp_sampling/session_context.py` | `e731beaaf4961626d506dd60bfb6fe19520deb9826b249c45b13e15ee121c32a` |

- `MCPSamplingAdapter` 调用 MCP 客户端的 `sampling/createMessage`；该分派不要求 API Key 或模型 endpoint。
- Sampling 返回文本。适配器将 Pydantic response model 的 JSON Schema 放入提示词，随后用 `model_validate_json` 验证；失败时有限次数请求修正。不能把普通文本返回视为已通过知识格式校验。
- `get_sampling_session()` 从 MCP SDK 的当前请求上下文获取连接，并确认客户端声明了 `sampling` 能力。没有请求会话或客户端未授权时返回不可用，适配器抛出 `MCPSamplingUnavailableError`。
- 独立 HTTP 服务不会自动获得另一个 MCP 进程中的连接。后台任务即使继承了请求上下文，也仍依赖客户端连接存活；请求结束、Agent 退出后的任务生命周期需另外实测和管理。
- 此 provider 当前位于 `instructor` 分派分支；`resolve_structured_output_framework` 不会因 provider 为 Sampling 自动覆盖 `litellm_native`。不能只改 `LLM_PROVIDER` 就认定现有 Memlane 已接通。

来源：

1. [官方 Sampling adapter](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/llm/structured_output_framework/litellm_instructor/llm/mcp_sampling/adapter.py)。
2. [官方会话与能力检查](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/llm/structured_output_framework/litellm_instructor/llm/mcp_sampling/session_context.py)。
3. [官方 provider 分派](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/llm/structured_output_framework/litellm_instructor/llm/get_llm_client.py)。
4. [官方框架配置](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/infrastructure/llm/config.py)。

## app-server 适配的边界

本机当前 PATH 中为 `codex-cli 0.147.0`。其 stable 与 experimental JSON Schema 均生成成功，临时目录已清理；没有读取用户模型凭据或执行真实模型任务。本次协议研究由 research 技能安排的子 Agent 核对官方 `rust-v0.147.0` 文档和本机 schema。

- `thread/start` 包含 `model`、`modelProvider`、`baseInstructions`、`developerInstructions`、`ephemeral`、`config`、`sandbox` 与 `approvalPolicy`。
- `turn/start` 包含 `input`、`threadId` 与 `outputSchema`；后者约束该轮最终助手消息。
- `turn/interrupt` 使用 `threadId` 和 `turnId`；中断请求的 ACK 不能代替 `turn/completed` 的最终状态。取消后台终端还需要单独处理。
- `ephemeral` 可用于内存会话，但不能据此认定所有工具、MCP、插件、hook、技能或账户日志均已禁用。

来源：[Codex 0.147.0 app-server 官方文档](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/app-server/README.md)。stdio 为默认 JSONL 传输，该版本的 WebSocket 在官方文档中标为 experimental / unsupported；首个适配优先验证 stdio。

当前 Codex 0.147.0 不支持可直接复用的 MCP Sampling 回调，已核对发行提交 `be6e8eac029b183056b7e4402879f15d2c85f61b` 的源码：

| 检查点 | 事实与出处 |
| --- | --- |
| 初始化能力 | [rmcp_client.rs](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/codex-mcp/src/rmcp_client.rs#L959-L982) 使用默认 ClientCapabilities，只设置 elicitation 和已知 extensions，没有设置 sampling。 |
| 客户端处理 | [LoggingClientHandler](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/rmcp-client/src/logging_client_handler.rs#L40-L141) 没有覆盖 create_message；[请求路由](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/rmcp-client/src/elicitation_client_service.rs#L92-L155) 的其他请求委托该默认处理器。 |
| 默认能力与处理 | 固定依赖 [rmcp 3.0.0](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/Cargo.lock#L11624-L11627) 的 [sampling 默认值为 None](https://docs.rs/rmcp/3.0.0/src/rmcp/model/capabilities.rs.html#174-194)，[默认 create_message 返回 method_not_found](https://docs.rs/rmcp/3.0.0/src/rmcp/handler/client.rs.html#97-105)。 |

研究 Agent 从官方 registry 只读下载的 rmcp archive SHA256 为 `fcd2b6dd3b18129368955f32661a7718969e8c152c7d8866434c09cf15a512e0`，与该 lock 一致。上述结论仅覆盖当前核对版本，不推断其他 Agent 或未来 Codex；没有执行真实握手或模型任务。

工具隔离仍须实测：[dynamicTools](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/app-server/README.md#L1726-L1730) 是额外注册的工具，空列表不关闭内置工具。[无执行 environment 时不注册 shell](https://github.com/openai/codex/blob/be6e8eac029b183056b7e4402879f15d2c85f61b/codex-rs/core/src/tools/spec_plan.rs#L876-L882) 可以构成受限实验的一部分，但不能据此宣布所有 MCP、技能、插件、hooks 或其他工具均已关闭。

Cognee 当前模型适配器使用可配置的 HTTP endpoint。一个可选实现是提供本机兼容模型 API 的桥，内部调用 Codex app-server，返回经 Schema 校验的文本。这个 HTTP endpoint 与 app-server 的 stdio/WebSocket JSON-RPC 协议不同，需要实际转换。桥自己的本机访问凭据可以由 App 管理；Agent 的模型认证继续由 Agent 自己处理。

## 对初始化与产品配置的影响

用户已确认 Cognee 服务和必要本地组件应与基础环境一起初始化，见 [产品定义](product.md) 和 [环境准备契约](ui-redesign/a-environment.md)。组件准备、服务健康、提炼模型可用与 Agent 连接授权分别取证。

用户随后明确选择本地 Agent 桥接作为需求方向，见 [完整需求](agent-model-bridge-requirements.md)。默认用户配置变为选择提炼 Agent，并验证它的登录、模型能力和后台任务可用性，无需为 Cognee 另填云模型 API Key；现有直连模型实现保留为历史证据。第一版复用统一后台执行者规则，具体使用哪款 Agent 仍由用户选择；不把本机 Agent 运行解释为模型推理必然离线。

后续最小真实验证需覆盖：官方 Cognee 提炼流程实际调用所选 Agent；结构化产物和完整出处进入既有接收链路；提炼会话不触发日常记忆采集或递归保存；退出、取消、超时、认证失效和额度耗尽有准确回执；既有有效知识继续遵守编辑、版本与审核规则。MCP 客户端支持与后台连接寿命要逐 Agent 验证。

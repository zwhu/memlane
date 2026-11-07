# P3 研究依据与本机协议记录

> 当前实施基线：P3-C1 正式服务端已通过，见 [C1 验收](p3-validation.md#8-p3-c1-正式服务端验收)。早期“本轮/未接生产”段落描述隔离实验历史。全部剩余切片与外部条件见 [交付矩阵](p3-delivery-plan.md)，后续通过项仅以对应证据为准。

2026-11-08 重生成。本文区分外部研究、固定版本源码结论、本机协议事实和设计建议。产品语义以 [产品定义](product.md) 为准，实际验证见 [P3 记录](p3-validation.md)，实施建议见 [技术](p3-technical-analysis.md) 与 [架构](p3-architecture.md)。

## 1. 记忆组织的研究依据

Anthropic 的 [The components of agent memory](https://www.anthropic.com/research/components-of-agent-memory)（2026-11-06）讨论可搜索经历、记忆笔记、更高层表示和注入上下文；[dreaming cookbook](https://platform.claude.com/cookbook/managed-agents-cma-dreaming-with-a-dynamic-workflow) 展示回看材料后组织记忆的流程。

这些资料支持“保留经历、组织知识、回看来源”的设计方向，不规定 Memlane 的排名、数据表、预算或本地 Agent 协议。表示会膨胀、注入错误会影响后续任务、维护需要计算；新增摘要数量不能代替质量验收。

Memlane 只接收实际提交的来源片段与引用，没有完整聊天日志。摘要之间若同源，不能算成多个独立证据。使用频率、采用和真实性是不同事实；排名不能自动触发遗忘、跨项目提升或覆盖。

## 2. Cognee improve 的边界

固定版本仍为 Cognee-RS `151905c302fc9b5b9aad88d6b13931c09f0eac30` 与 Python Cognee 1.6.7。本轮沿用此前固定源码核查结论，没有运行生产 improve 或重新宣称其功能已验收。

Rust 的 [orchestrator](https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/lib/src/api/improve/mod.rs)、[stages](https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/lib/src/api/improve/stages.rs) 与 [config](https://github.com/topoteretes/cognee-rs/blob/151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/lib/src/api/improve/config.rs) 注册九个阶段：

| 阶段 | 关键边界 |
| --- | --- |
| feedback_weights | 会修改图中的反馈权重，不是业务知识的使用排名 |
| persist_session_qa | 持久化会话问答，并有 fatal 失败语义 |
| persist_agent_traces | 写入轨迹和处理进度 |
| extract_agent_context | 依赖实际 session 和配置 |
| distill_sessions | 发布提炼知识；此 Rust 版本主要消费 Q&A，与 Python 不完全相同 |
| update_user_preferences | 可能写入或裁剪偏好 |
| build_truth_subspace | 派生处理，不等于用户确认的真实性 |
| review_conflicts | 此固定 Rust 版本仍以 not_ported 跳过 |
| global_context_index | 构建索引并记水位，不等于项目经验进入业务全局范围 |

Python 对应 [improve](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/api/v1/improve/improve.py) 和 [stages](https://github.com/topoteretes/cognee/blob/eaa33be5c58cd14584e64e3e0ed2e1479472ee09/cognee/modules/improve/stages.py) 同样包含 add/cognify、持久化、偏好和水位副作用；冲突复核有实现，也不代表纯预览。

因此第一条 P3 链路采用 Go 冻结/审核、Agent 纯提案、业务事务应用、既有 worker 索引。替换 Python 或影子 dataset 不能自动消除确认前写入问题；以后复用上游阶段需单独验证全部副作用。

## 3. traex 本机事实与隔离验证

本机 `traecli` 链接到 `traex`，版本 `0.214.1`、internal 发行。`exec --help` 支持 JSONL、output-schema、ephemeral、指定目录、read-only、ignore-user-config/rules、disallowed-tool。帮助中仍有 CODEX_HOME 文本，不能照搬 Codex 隔离语义。

使用 [TRAE CLI 使用指南](https://bytedance.larkoffice.com/wiki/HWUPwVssCi0KSPkOCZtcwznmnYg) 核对：TRAE_HOME 是共享资源根，TRAECLI_HOME 是私有运行目录，认证由 CLI 的 auth.json 处理。只改变私有 home 不会移动全局技能和主配置。

本轮沿用已有 Trae 登录与当前 GPT-6.1-Sol 选择。临时共享/私有 home、空任务目录、认证文件链接均由实验创建；不编辑用户配置，不读真实记忆或聊天。

实际 debug prompt-input 发现即使 resource_dirs=[]，全局 .agents/skills 仍会注入。按入口路径禁用后，提示输入只剩权限/运行环境/合成 prompt，没有技能目录或记忆注入。对应记录为 `build/p3/traex-isolated-prompt-input.json`。

模型在本机需要 code_mode_only 的 provider 适配：

| 尝试 | 实际结论 |
| --- | --- |
| 关闭 code-mode host | 返回 Code Mode unavailable 错误项；不能仅因最后有消息就通过 |
| 对带点 model slug 使用点路径 override | CLI 把键拆成嵌套 map，配置类型失败；应传整个 TOML 表 |
| 使用 direct 工具模式 | 本机返回 Astra raw-chat 无法表示全部 advertised tools 的错误 |
| 保留必需 host，code_mode_only，工具显式禁止 | 真实纯提案通过，无工具/错误事件，正常终态和进程组收口共同通过 |

实验关闭 shell/unified_exec、plugins、hooks、memories、多 Agent、浏览器/电脑/图像、skill 搜索与自动依赖安装等能力，不注册 MCP；保留协议所需 host，并传工具禁止项。保留 host 不能描述成没有任何运行辅助进程，进程组取消须包括它。

## 4. Codex 与 pi 的历史和后续

Codex 0.147.0 的 P2 日常工具往返已通过；本轮后台正常调用曾因账号额度失败，真实取消通过。原始失败在 `build/p3/codex-real-normal.json`，用户随后指定 traex，当前验证不再等待该额度。Codex 的失败没有被改写为成功。

pi 0.84.1 的日常 Extension 已通过。其 RPC prompt 接收不代表完成，agent_end 后可能继续重试/排队，本机 agent_settled 才是完整收口事件；abort 与进程退出另验。[RPC 文档](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md) 随 main 变化，实际适配必须核对已安装包。pi 后台提案本轮未实现。

## 5. 结论允许什么

真实 Agent 的 schema 输出、终态与进程不能互相代替。服务的事务、revision、来源与幂等门禁须在最终提交时再次检查。当前证据足以进入 P3-C 正式契约与服务审核切片；没有证明整体知识质量提升、长期无人值守、调度恢复或完整图谱。

正式生产迁移、质量基线、长任务/异常取消、attempt 租约与重启恢复、摘要依赖失效，以及排名/初始化/计划仍需相应验证。实验参数和接口继续标为实验。

## P3-C1 当前进展

2026-11-08：最小正式服务端合并契约、v2/v3 迁移与备份、持久任务/attempt 栅栏、确认/拒绝/取消和类型化私有 IPC 已实现，真实服务验收见 [P3-C1 记录](p3-validation.md#8-p3-c1-正式服务端验收)。此前文中的“正式契约待收口/未进服务”仅对应隔离实验历史；本次仍未实现或验收 P3 UI、摘要/标签、调度、排名、初始化、图谱与其他 Agent。下一切片为栏内审核 UI 与实际窗口对照。

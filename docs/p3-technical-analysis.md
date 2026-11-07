# P3 技术方案与实验结论

2026-11-08 重生成。用户已将本轮后台验证执行器改为 traex；生产基线为 P2 `70ddc0f`。本文区分已存在代码、隔离实验和待定正式接口，实际命令与结果见 [验证记录](p3-validation.md)。

配套：[产品](p3-product-design.md)、[架构](p3-architecture.md)、[研究](research-p3-memory.md)、[Goal prompt](p3-goal-prompt.md)。

## 1. 实际复用能力

P2 SQLite schema v2 包含 memories、versions、operations、jobs、index_bindings、scopes、memory_metadata、activities、connections、repositories。jobs 是索引任务，不是整理任务。历史方案里的 proposals/job_attempts 等对象不代表已经存在。

复用 Store.Apply 的正文、revision、幂等、来源元信息与持久索引任务；抽出事务内 applyTx 供多条目业务事务使用。复用 Current、Resolve、Requeue、IndexCurrent 和 MarkIndexed 处理当前版本及索引一致性，Rust worker 和本地 E5-base int8 保持既有实现。

P2 agents.Run 面向日常连接验证，要求真实 memory_* 回执。实验 RunP3Experiment 单独承担纯提案，不向原验证器混入冲突语义。

## 2. 本轮执行路径

Go 固定输入 → 本机 TRAE CLI 独立 exec → 输出受限 JSON → 宿主再次校验 → 服务只登记建议 → 显式确认 → 单事务应用 → 既有索引任务。

本机 0.214.1 internal 安装的登录机制与已选择 GPT-6.1-Sol 沿用。隔离 TRAE_HOME 和 TRAECLI_HOME，只为 CLI 链接已有认证文件，不提取或复制密钥，不修改用户配置。项目目录和会话都是新建临时目录，不加载生产记忆工具。

独立 home 和 resource_dirs=[] 仍会发现全局 .agents/skills，必须按入口路径显式禁用，并关闭 bundled skills、plugins、hooks、memories、shell/unified_exec、多 Agent、浏览器等能力。实际 debug prompt-input 用来核对无技能和记忆注入。

GPT-6.1-Sol 的本机协议需要 code-mode host；关闭它会产生错误事件。direct 工具模式在本机产生 raw-chat 适配错误。最终保留必需 host，采用 code_mode_only，并显式禁止工具执行。保留 host 不代表允许日常记忆写入；真实正常调用无工具事件，错误事件继续拒绝。具体失败原文与有效配置记录在研究和验证文档。

## 3. 实验契约与原子合并

实验 schema 为 memlane-p3-experiment-v1，只处理同 dataset 两条有效知识的一项 propose_merge。task/attempt、输入 SHA-256、目标 revision、来源 quote 与提案 ID 全部由宿主绑定；自动批准、未知动作和来源扩大被拒绝。

OpenP3Experiment 只在显式的临时验证路径创建四张实验附加表。生产 Open、daemon、IPC/MCP 没有启用，user_version 保持 2；这不是正式迁移。

Stage 在事务内校验取消与 attempt，只保存建议。Apply 在同一事务里校验所有来源，再提交新知识 v1、两条归档版本、两项具体来源快照、应用回执及必要索引任务。任一来源过期则整项拒绝，SQL 中途失败完整回滚。相同提案或回执只产生一次事实。

取消先持久提交则拒绝迟到结果；应用先提交则保留知识与回执。CLI context 取消落实为进程组结束、等待和存活检查，缓冲迟到结果不会交付。索引与 SQLite 没有跨库事务；索引失败保留正文/回执，沿用持久任务重试。

## 4. 正式接口仍需解决什么

进入 P3-C 前制定：授权范围、冻结来源的正式字段、合并归档/恢复、确认和拒绝权限、错误类型、幂等回执、兼容迁移与一致性备份。实验方法名和表名不能直接升级为公共 API。

多 attempt/租约、崩溃恢复、摘要依赖失效、标签历史、自动产出策略和质量评估仍待实现。网络调用不持有 SQLite 写事务；唯一服务拥有者负责最终门禁。

初始化与统计各自进入切片时再定义，不为本次两个验证创建空模块或空表。P2 旧工具协议保留；可选新字段与正式 schema 迁移须协商和测试。

## 5. 暂不展开的工作

完整 UI、定期调度、排名、初始化批次、代码图谱、pi RPC 和其他 Agent 接入都未进入本轮。生产 Cognee improve 不运行；通用 LLM provider 桥与首个纯提案流程解耦，是否复用上游阶段以后按收益和副作用验证。

无 UI/打包改动时不重跑整套 P0/P1 或体积测量。本轮验证主流程和受影响检查，后续实际交付再核对包、下载量及磁盘占用。

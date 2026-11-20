## 2026-11-20 方案决策原则

选择技术或产品方案时，必须从全局出发思考当前最优解，不要被既有历史文档或旧阶段约束束缚。若全局最优解与历史文档或规范存在冲突：**先给出最优方案**，再明确标注与哪份旧文档/规则冲突以及冲突原因，交由用户判断；用户确认后同步更新相关文档，避免过时约束继续误导后续决策。

## 2026-11-20 Cognee 零侵入原则与记忆日期口径（方案 B）

架构原则：**尽量不侵入 Cognee 源码**；Memlane 适配与扩展均保持在外置网关、外置存储或宿主插件层，不恢复 `lesson_provenance_patch.py` 等上游引擎源码补丁。

记忆日期口径（覆盖 2026-11-16 依赖侵入 Cognee `distill.py` 输出 `source_qa_ids` 的旧规则）：优先沿用外置 `provenance_store` 与客户端日期记录按 `(dataset_id, lesson_type, state_key)` 锁定的最早日期（重整理不漂移、不重复计数）；其次采用 Cognee 原生 `session_date`（由插件及外置采集入口补齐 QA `context.date`）；缺少 `session_date` 的会话记忆回退采用 `createdAt` 对应的本地日历日，不再在总览展示「部分记忆缺少来源日期，未计入热力图。」；实际入库时间 `createdAt` 仍单独保留。

## 2026-11-14 单一日常库

当前运行、Native 展示及后续真实链路验收统一连接同一套日常 Cognee 官方存储（`.local/daily/official-store`），不再为开发另建或切换独立测试库。项目仍按各自 dataset 分组；备份只用于回滚，不作为第二运行库。未核对完成前不删除原材料或运行依赖。

## 开发与 release 阶段边界

- 当前处于开发阶段（`dev`），不构建或验收完整运行包（不进行 DMG、签名、公证、更新或 helper 安装），不将完整包作为开发任务完成门槛；等用户明确要求进入 release 阶段后再开始打包。
- 开发阶段继续进行源码检查、必要测试、真实链路及实际 Native 界面验收；必要的开发调试编译不等于完整运行包交付。
- 开发入口见[开发说明](docs/development.md)，统一使用 `python3 scripts/dev.py`；Go 检查使用 `sh scripts/go.sh test ./...`，Python 检查使用已有 venv 并经由 `python3 scripts/task.py -- <命令>` 执行。

## 开发磁盘安全空间（50 GiB / 55 GiB）

- 至少保留 50 GiB 可用空间；不足先清理本项目可再生缓存／任务载荷，仍不足则停止，不继续下载或构建。
- Go 检查与构建包装已接入 `scripts/task.py`；其他验收／组件准备一律通过 `python3 scripts/task.py -- <命令>` 执行。运行中余量监测低于 55 GiB 停止受管任务。新阈值对新启动的包装器生效，不因修改门禁擅自停止已运行的共享服务。
- 临时环境、引擎／模型重复副本跑完任务即清；长期仅留报告、小型回执及必要开发组件。**禁止清空 `build/`**：其中 `build/p3-prototype` 是有未提交修改的 Git 工作树。

## 运行包与清理依赖保护

- 新 GUI 包不能视为完整运行时替代；清理前核对 daemon/helper 所属完整包、Agent 桥接路径、服务注册路径及组件引用。保护完整包及其资源/签名，不仅保护执行文件；不能仅凭 `ps` 命令名或无运行进程判定可删。
- 切换保留包时同步保留实际依赖集合，只有真实迁移并验证后才清理旧包；依赖未知则保留；无安全清理候选且空间不足时停止构建/下载。
- 复盘见[运行包误清理](docs/engineering/runtime-package-cleanup-incident-20261111.md)；后续卸载待办见[Roadmap](docs/roadmap.md)。

## 工程执行与问题闭环

跨模块实施使用 [plan-change-impact](.agents/skills/plan-change-impact/SKILL.md)，集成／交付验收使用 [verify-continuous-scenarios](.agents/skills/verify-continuous-scenarios/SKILL.md)；局部小改动按需简化。项目填写入口为[影响与验证模板](docs/engineering/task-brief-template.md)，命令工具见[场景执行入口](docs/engineering/scenario-runner.md)。

后续编码遵循[工程规范](docs/engineering/workflow.md)：先列连续主流程用例，再集中开发和定向检查，统一集成验收；失败按同类集中修复，使用[用例模板](docs/engineering/testcase-template.md)与[复盘模板](docs/engineering/incident-template.md)。诊断不得把未知模型调用或进程事实推断为零／已回收。

同一具体问题累计第 4 次验收仍未通过，停止该问题的继续尝试并汇报；「4 次」按同一具体缺陷计数，不按连续 4 轮整体验收失败计数。按触发条件、失败现象与验收标准识别问题，不得将整个页面或功能中的不同缺陷合并凑成 4 次；换版本、路径、Agent 或操作 ID 也不能拆分同一问题以重置次数。失败证据始终保留，详见[停止规则](docs/engineering/workflow.md)。不依赖该问题的工作可继续。

计数示例：四轮分别发现布局、复制、焦点和来源权限问题，各问题分别记 1 次，不触发「四次失败停止」。同一具体缺陷即使中间穿插其他问题，累计第 4 次失败仍须停止该缺陷；停止范围限于该问题及确实依赖它的工作。

## Codebase ownership and communication

1/ This codebase is yours: what it's for and how it's built are your call, and you'll keep building on it.

2/ Before you start, say in a line what you're about to do; brief updates while you work help the user follow along. Close with a short recap that stands on its own (what you found, what you did, and what's next), so a reader who only sees the last message has the full picture.

## 2026-11-18 桥接与直连的模型参数防回归

提交相关修改前必须运行 `python3 scripts/task.py -- python3 scripts/lint.py`。这是不依赖第三方库的 AST 检查，覆盖 `engine/python/` 全部生产模块（含子包）；`ML001` 禁止绕过共享构造器，`ML002` 禁止注入字面量参数，`ML003` 禁止参数回退值，`ML007` 禁止在非白名单模块修改全局 `os.environ`。规则回归使用 `python3 scripts/task.py -- python3 -m unittest discover -s scripts -p test_lint.py`。

未配置的模型选项保持未配置，不补客户端显式采样/输出默认值；用户显式填写的 `0` 必须保留。启动、验证和切换统一使用 `generation_config.native_llm_configuration` / `native_llm_kwargs`，消费者不得独立构造 `LLMConfig` 或继承前一配置环境。修改这些路径须通过 `test_generation_parameters`、`test_generation_probe`、`test_generation_handoff`、`test_generation_startup`，执行要求见[工程规范](docs/engineering/workflow.md)。

## 2026-11-18 Agent 有效模型继承与设置边界

所有 Agent 均复用自身模型设置，技术契约见[统一技术契约](docs/engineering/agent-effective-model-contract-20261118.md)。优先使用明确绑定会话的官方元数据，再继承 Agent 配置中的显式选项；无会话身份只读默认配置并区分来源，不猜最近会话。桥接与 Provider 配置并存时唯一 `active` 决定实际执行；查看、编辑或保存不启用，只有确认后的服务回执更新 `active`（由 `ML004` / `ML005` 守护）。

设置页按「记忆处理 → Agent 与模型 → 服务」组织：列表卡片外使用 `Segmented` 切换处理来源、编辑与确认使用 `Modal`、列表行按固定顺序展示且保留只读模型详情。用户画像生成与导出不得按固定总时长超时中断（由 `ML006` 守护），应观测进程/输出活动并提供显式停止，保存阶段禁止取消并声称未写入。

# Memlane 产品与架构约束

这是面向程序员的 macOS 记忆客户端（MyGo Native，固定版本官方 Cognee 引擎），工作前阅读[产品定义](docs/product.md)、[设计决策](docs/design.md)与领域术语[CONTEXT](CONTEXT.md)。

- **唯一数据源与内容分层**：知识与原始记录以 Cognee 官方存储为唯一主数据源，术语与分层见[内容分层](docs/ui-redesign/content-model.md)；通用记忆（个人偏好／通用经验）与用户画像按来源独立保存，不自动合并为通用记忆。AI 提出的合并、覆盖、删除或跨项目移动在用户确认前不得改变已有有效知识。
- **官方采集与电脑级连接**：Agent 连接与信息采集按[电脑级配置与官方优先规则](docs/ui-redesign/agent-connection-contract.md)执行，优先复用官方插件并用受管独立主体接入；单条拒收与回执未知隔离见[队列隔离记录](docs/engineering/queue-isolation-20261119.md)，当前整体结构见[架构改造记录](docs/engineering/architecture-refactor-20261120.md)。
- **界面与视觉规范**：默认与最小窗口 1200×820（允许放大），首页保留宽幅总览，详情页采用三栏，普通操作在栏内完成，无全局「全部记忆」侧栏入口。项目页的列表栏使用「分类／仅未读／排序」筛选工具条，关键词搜索交给全局 ⌘K。UI 改动须实际查看[视觉规范 V4](docs/ui-redesign/visual-standards-v4-20261120.md)及其[可交互原型](docs/ui-redesign/visual-v4-prototype/)，在浅色和深色两套外观下验收；动效开发遵循[MyGo 动效契约](docs/ui-redesign/motion-implementation.md)，弹簧参数以 V4 为准。旧的 20261112 设计图、设计规格 V3 和 20261113 视觉规范已被 V4 取代，只作历史参考。
- **协作与安全底线**：文档默认中文，区分已实现与待验证，不将示例状态描述为真实接入结果。保留用户无关修改，不提交密钥、真实记忆库、聊天记录或本机 Agent 配置。
